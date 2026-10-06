package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"gitpair/internal/changeset"
	"gitpair/internal/console"
	"gitpair/internal/git"
	"gitpair/internal/marker"
)

// --- init -------------------------------------------------------------------

// `init` is a top-level command rather than `change init`: it is the first command of the author's
// loop, it is what a fresh agent runs first, and the name says what happens. One spelling is the rule,
// and the group it used to sit in keeps the commands that act on a changeset already in progress
// (plan P3).

type initOptions struct {
	base      string
	setBase   bool
	parent    string
	setParent bool
	id        string
	about     string
	setAbout  bool
	noCommit  bool
}

func newInitCommand(a *app) *cobra.Command {
	opts := &initOptions{}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create review scaffolding for the current branch",
		Long: `Create changesets/<id>/ with CHANGESET.yaml and ABOUT.md, then commit it.

The changeset ID is what git-pair calls the work from here on: it names the directory, and
once refs exist it names those too. The branch name is only where the default comes from,
so feature/booking-transaction becomes changesets/feature-booking-transaction/ unless you
say otherwise:

  git pair init --id booking-transaction-v2

An ID is chosen, not derived, so it is never rewritten to fit: --id booking\ v2 is refused
rather than quietly turned into booking-v2, because refs named after a string nobody typed
are not findable by the person who typed it. IDs are unique in git-pair's namespace, and a
collision stops the command instead of appending a suffix.

A stacked changeset names the branch it sits on with --parent, which records it as
parent: — that branch IS the base, so the two keys are never both written:

  git switch -c booking-transaction-tests
  git pair init --parent booking-transaction

The parent's own changeset is recorded beside it as parent-changeset:, which is what still names
the relationship after the parent lands and its branch is gone. Restacking an existing changeset
takes --set-parent, the same way changing a base takes --set-base; git-pair never restacks a changeset
by itself, because a parent that moved or died is a decision for the author, not a default.

A base named with --base is read the same way: when that branch carries exactly one unlanded changeset, the
work is stacked on it, and the pair above is recorded in place of a plain base. Two or more changesets on
that branch is not a guess to make, so the base stands as written and every candidate is named.

The commit covers the changeset directory only, so whatever else is staged on
your index stays there. Use --no-commit to leave the scaffolding in the working
tree for your first implementation commit instead.

ABOUT.md gets a scaffold with the standard review headings unless you supply
content, which makes describe-and-initialise a single non-interactive call:

  git pair init --base main --about "$DESCRIPTION"
  git pair init --base main --about - < about.md
  cat about.md | git pair init --base main

Existing content is never overwritten silently: replacing a populated ABOUT.md
takes --set-about, the same way changing a base takes --set-base.`,
		Example: `  git pair init --base main
  git pair init --base main --id booking-transaction-v2
  git pair init --parent booking-transaction
  git pair init --base main --about - < draft.md`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChangeInit(cmd.Context(), a, opts)
		},
	}
	cmd.Flags().StringVar(&opts.base, "base", "", "ref this changeset's diff is measured against (default: main, then master)")
	cmd.Flags().StringVar(&opts.id, "id", "", "changeset ID (default: the branch name, normalised)")
	cmd.Flags().BoolVar(&opts.setBase, "set-base", false, "overwrite an existing base value")
	cmd.Flags().StringVar(&opts.parent, "parent", "", "stack this changeset on <branch>, recording it as `base:` together with `base-changeset:")
	cmd.Flags().BoolVar(&opts.setParent, "set-parent", false, "restack an existing changeset onto --parent")
	cmd.Flags().StringVar(&opts.about, "about", "", "ABOUT.md content; - reads it from stdin")
	cmd.Flags().BoolVar(&opts.setAbout, "set-about", false, "overwrite an existing ABOUT.md")
	cmd.Flags().BoolVar(&opts.noCommit, "no-commit", false, "leave the scaffolding uncommitted")
	return cmd
}

func runChangeInit(ctx context.Context, a *app, opts *initOptions) error {
	repo, err := a.loadRepo(ctx)
	if err != nil {
		return err
	}
	branch, err := repo.CurrentBranch(ctx)
	if err != nil {
		return err
	}
	if branch == "" {
		return &usageError{fmt.Errorf("%w: run `git switch -c <branch>` before `git pair init`", changeset.ErrDetachedHead)}
	}
	// Starting a changeset on the integration branch is refused for clarity, not correctness:
	// a changeset is measured against that branch, so one started on it is inert — every
	// directory it carries is already landed. Saying so where the mistake is made beats a
	// status line that never shows the changeset.
	if db, err := a.resolveDefaultBranch(ctx, repo); err == nil && db.IsBranch(branch) {
		return &usageError{fmt.Errorf("%s is the integration branch, so a changeset started on it can never contain anything: `git switch -c <branch>` first", branch)}
	}
	if opts.parent != "" && opts.base != "" {
		return &usageError{fmt.Errorf("--parent %s and --base %s name the same thing twice: a stacked changeset's parent IS its base. Name the parent, or drop --parent and name the base", opts.parent, opts.base)}
	}
	if opts.parent != "" && opts.parent == branch {
		return &usageError{fmt.Errorf("--parent %s is the branch this changeset lives on, so the stack would move with every commit. Name the branch you branched off", branch)}
	}
	base := opts.base
	if base == "" {
		// Resolved before the changeset itself. When there is no trunk to infer a base from,
		// the advice the caller needs is `--base <ref>`, and that has to be the error they
		// see rather than the generic "which branch is the integration branch" refusal.
		if base, err = a.defaultBase(ctx, repo); err != nil {
			return &usageError{err}
		}
		a.warn("base: %s (pass --base to choose a different ref)\n", base)
		if note := baseDivergence(ctx, repo, base); note != "" {
			a.warn("%s\n", note)
		}
	}

	// `init` creates a directory, which is not a resolution question. A stacked branch
	// carries its parent's changeset directory, and that inherited directory must not stop the
	// child from starting its own: once both exist, `base:` says which is which.
	id := opts.id
	if id == "" {
		if id, err = changeset.SlugFromBranch(branch); err != nil {
			return &usageError{err}
		}
	}
	cs, err := changeset.ForID(id)
	if err != nil {
		return &usageError{err}
	}
	cs.Branch = branch
	dir, err := changeset.DirectoryAt(ctx, repo, id)
	if err != nil {
		return err
	}
	if dir.Committed && !dir.Worktree && !dir.Landed {
		// Retiring a changeset is a commit. Until the deletion is committed the directory's
		// history is still live here, so the name is not free yet. A landed directory is the other
		// reason a path is missing from the working tree, and nothing is pending there.
		return &usageError{fmt.Errorf("changesets/%s/ is deleted in your working tree but the deletion is not committed; commit the deletion before starting a changeset with that name again", id)}
	}
	cs.Exists = dir.Worktree
	if db, err := a.resolveDefaultBranch(ctx, repo); err == nil {
		// A landed changeset is closed, and arriving at one by id is the same mistake `change ready` refuses.
		// The answer comes from the destination's tree, so it does not depend on which command noticed.
		if err := marker.RefuseIntegrated(ctx, repo, id, db); err != nil {
			return err
		}
	}
	if !cs.Exists {
		if err := refuseTakenID(ctx, repo, id); err != nil {
			return err
		}
		// Until the invariant was written down this was a warning, and a warning was the wrong verb: the
		// shape it described is the one the CI job merges, and it named only the changeset the resolver had
		// picked, so a branch whose two directories nothing could order - the clearest case - warned about
		// nothing. `refuseSecondChangeset` below asks the same question of the whole set, once the stack this
		// run is about to record is known.
	}
	if _, err := repo.RevParse(ctx, base); err != nil {
		a.warn("warning: base %q does not resolve yet; spans and status will fail until it does\n", base)
	}

	// The durable half of the stack: which changeset lives on the parent branch. It is discovered,
	// not demanded — a parent that carries none, or carries two, is recorded with just its branch
	// name, because guessing an ID here would write a claim nobody checked into the file that is
	// read after the parent is gone.
	db, dbErr := a.resolveDefaultBranch(ctx, repo)
	baseChangeset := ""
	// stackBase is the branch the stack is recorded on: `--parent` when one was named, and the base when the
	// base turned out to name a branch carrying somebody else's unlanded work.
	stackBase := opts.parent
	if opts.parent != "" {
		if dbErr == nil && db.IsBranch(opts.parent) {
			return &usageError{fmt.Errorf("--parent %s is the integration branch, which is not a stack: a changeset measured against it is not stacked. --base %s is the flag for that", opts.parent, opts.parent)}
		}
		pcs, candidates, why := baseChangesetOn(ctx, repo, opts.parent, db)
		baseChangeset = pcs
		if pcs == "" {
			a.warn("warning: parent %s has no changeset of its own (%s), so parent-changeset: is left empty. The stack is recorded by branch name only%s\n", opts.parent, why, candidateNote(candidates))
		}
	} else if dbErr == nil && base != "" && !db.IsBranch(base) {
		// A base naming a branch that carries exactly one unlanded changeset is a stack, however it was
		// spelled. The pair is recorded rather than the id alone, and the difference is not cosmetic: a
		// `base:` keeps naming that branch after the work on it lands, which is the state where `check` names
		// finished work as the destination, while `parent:` is read against the destination and answers the
		// same question once the parent's branch is gone (PRD 21). Two or more unlanded changesets on that
		// branch is a different situation - nothing here can tell which one this work is stacked on - so the
		// base stands as authored and every candidate is named for the author to pick.
		if pcs, candidates, _ := baseChangesetOn(ctx, repo, base, db); pcs != "" && len(candidates) == 1 {
			stackBase, baseChangeset = base, pcs
			a.warn("parent: %s (the base carries changeset %s, so this work is stacked on it: `parent:` and `parent-changeset:` are recorded in place of a plain `base:`)\n", base, pcs)
		} else if len(candidates) >= 2 {
			a.warn("warning: base %s carries %d unlanded changesets (%s), so no stack is recorded. Pass --parent %s if this work is stacked on one of them\n",
				base, len(candidates), strings.Join(candidates, ", "), base)
		}
	}

	if own, err := changeset.BaseIsOwnBranch(ctx, repo, base, branch); err != nil {
		return err
	} else if own {
		return &usageError{fmt.Errorf("%w: changeset %q cannot be based on %s, the branch it lives on. The base would move with every commit, so the changeset could never contain anything. Create a branch for the change (`git switch -c <name>`) or pass --base <ancestor-ref>",
			changeset.ErrBaseIsOwnBranch, cs.Slug, base)}
	}

	if !cs.Exists {
		if err := a.refuseSecondChangeset(ctx, repo, branch, cs.Slug, baseChangeset, base); err != nil {
			return err
		}
	}

	about, err := aboutContent(opts)
	if err != nil {
		return err
	}

	writeOpts := changeset.WriteOptions{
		Base:     base,
		SetBase:  opts.setBase,
		About:    about,
		SetAbout: opts.setAbout,
	}
	if stackBase != "" {
		writeOpts.Base = ""
		writeOpts.Parent = stackBase
		writeOpts.BaseChangeset = baseChangeset
		// --set-parent stays the author's flag. The inference records a stack that was always there; it does
		// not restack a changeset that had recorded a different one.
		writeOpts.SetParent = opts.setParent
	}
	written, err := changeset.Write(repo, cs, writeOpts)
	if err != nil {
		// These are all "your arguments describe something that already exists"
		// errors rather than repository states, so they exit 2.
		switch {
		case errors.Is(err, changeset.ErrBaseConflict),
			errors.Is(err, changeset.ErrParentWithBase),
			errors.Is(err, changeset.ErrAboutConflict),
			errors.Is(err, changeset.ErrIDMismatch):
			return &usageError{err}
		}
		return err
	}
	for _, w := range written {
		a.printf("created %s\n", w)
	}
	if len(written) == 0 {
		a.printf("unchanged %s (already initialised)\n", cs.Dir)
	}

	described := about != "" || !aboutIsTemplate(repo, cs)
	if opts.noCommit {
		a.printf("not committed (--no-commit)\n")
		printInitNext(a, cs, described)
		return nil
	}

	// Stage first, then commit with --only: git needs the files in the index to
	// accept them as pathspecs, and --only keeps the rest of the index out of
	// this commit.
	if err := repo.StagePaths(ctx, cs.Dir); err != nil {
		return err
	}
	staged, err := repo.HasStagedChanges(ctx, cs.Dir)
	if err != nil {
		// A failure here is not worth inventing a new way for `init` to die:
		// fall through and let the commit itself report.
		staged = true
	}
	if !staged {
		a.printf("nothing to commit (scaffolding is already tracked)\n")
		printInitNext(a, cs, described)
		return nil
	}
	sha, err := marker.CommitPaths(ctx, repo, marker.Message{
		Subject:  fmt.Sprintf("git-pair: initialize changeset %s", cs.Slug),
		Trailers: []string{"Review-Changeset=" + cs.Slug},
	}, []string{cs.Dir}, a.destination(ctx, repo))
	if err != nil {
		if isNothingToCommit(err) {
			a.printf("nothing to commit (scaffolding is already tracked)\n")
			printInitNext(a, cs, described)
			return nil
		}
		return err
	}
	a.printf("committed %s\n", short(sha))
	if status, err := repo.StatusPorcelain(ctx); err == nil && strings.TrimSpace(status) != "" {
		a.printf("left %s uncommitted (not part of the scaffold)\n",
			plural(len(strings.Split(strings.TrimSpace(status), "\n")), "path", "paths"))
	}
	printInitNext(a, cs, described)
	return nil
}

// refuseTakenID enforces the uniqueness PRD §5 asks for: an id may not already be in use, as a directory —
// active or landed, in the working tree or carried by the current branch. Nothing is suffixed to dodge a
// collision — an id chosen for you is an id nobody chose, and it is baked into the directory the moment the
// changeset is readied. A suggestion is offered only when the candidate is itself free.
func refuseTakenID(ctx context.Context, repo *git.Repo, id string) error {
	if dir, err := changeset.DirectoryAt(ctx, repo, id); err != nil {
		return err
	} else if dir.Worktree || dir.Committed {
		return idTakenError(ctx, repo, id, "changesets/"+id+" already exists, possibly left behind by a changeset that has landed")
	}
	return nil
}

func idTakenError(ctx context.Context, repo *git.Repo, id, why string) error {
	if free := freeID(ctx, repo, id); free != "" {
		return &usageError{fmt.Errorf("changeset ID %q is already in use: %s.\n\nChoose another ID:\n\n  git pair init --id %s", id, why, free)}
	}
	return &usageError{fmt.Errorf("changeset ID %q is already in use: %s. Choose another ID with --id", id, why)}
}

// freeID returns the first unused <id>-<n>, or "" when none of the obvious candidates is
// free or the repository could not be asked.
func freeID(ctx context.Context, repo *git.Repo, id string) string {
	for n := 2; n <= 9; n++ {
		candidate := fmt.Sprintf("%s-%d", id, n)
		if err := refuseTakenID(ctx, repo, candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// aboutContent resolves the ABOUT.md body from --about or piped stdin.
//
// An explicit --about wins. Without it, a pipe is treated as an deliberate act
// and its content is used; a terminal, or a pipe carrying nothing (including
// </dev/null), means "no content was supplied" and the scaffold is written.
func aboutContent(opts *initOptions) (string, error) {
	switch {
	case opts.about == "-":
		piped := console.ReadPipedStdin()
		if piped == "" {
			return "", &usageError{errors.New("--about - needs content on stdin, and stdin is empty")}
		}
		return piped, nil
	case opts.about != "":
		return opts.about, nil
	default:
		return console.ReadPipedStdin(), nil
	}
}

func printInitNext(a *app, cs changeset.Changeset, described bool) {
	if described {
		a.printf("\nNext: implement, commit, then run `git pair change ready`.\n")
		return
	}
	a.printf("\nNext: fill in %s, commit it with your implementation, then run `git pair change ready`.\n", cs.AboutPath())
}

// defaultBase picks the repository's trunk without guessing wildly.
// baseChangesetOn finds the changeset that lives on a parent branch, and says why it could not.
func baseChangesetOn(ctx context.Context, repo *git.Repo, parent string, db changeset.DefaultBranchRef) (string, []string, string) {
	if db.Ref == "" {
		return "", nil, "the integration branch is not resolved, so the parent's changeset cannot be told from its inherited ones"
	}
	res, err := changeset.Resolve(ctx, repo, "refs/heads/"+parent, db)
	if err != nil {
		return "", nil, err.Error()
	}
	candidates := make([]string, 0, len(res.Candidates))
	for _, c := range res.Candidates {
		candidates = append(candidates, c.Changeset.Slug)
	}
	// A branch created from its parent's branch carries the whole unlanded ancestry in its tree: A off trunk,
	// B off A, so B's tree holds A's directory as well as its own. Asking "which changeset is this base the
	// base of?" then gets one answer per level, and the levels below the top are not what the base reveals -
	// the changeset that names them is. So a candidate another candidate records as its own parent has to go
	// before anything is chosen - and that subtraction is the set operation inside `Resolve`, which reads the
	// recorded id from either spelling of the key. What is left here is the answer the resolver gave, and the
	// list the author needs when nothing decided: two siblings sharing a branch stay a refusal, because nothing
	// orders those and nothing here pretends to.
	if res.Selected != nil {
		if selected := res.Selected.Changeset.Slug; slices.Contains(candidates, selected) {
			return selected, candidates, ""
		}
	}
	if len(candidates) == 1 {
		return candidates[0], candidates, ""
	}
	return "", candidates, "no single unlanded changeset on it"
}

// candidateNote is the half of the warning that names what was found. `Resolve` orders candidates by
// nearness and declines to pick when nothing orders them; the author can pick, and needs the ids to do it.
func candidateNote(candidates []string) string {
	if len(candidates) < 2 {
		return ""
	}
	return fmt.Sprintf(" (it carries %d: %s)", len(candidates), strings.Join(candidates, ", "))
}

// defaultBase is the base `init` records when the author does not name one. It is the
// integration branch — the same ref the landed test compares trees against — so a changeset
// cannot be measured against one branch while being judged landed by another. The spelling is
// the short branch name, because that is what a person reads in CHANGESET.yaml.
func (a *app) defaultBase(ctx context.Context, repo *git.Repo) (string, error) {
	db, err := a.resolveDefaultBranch(ctx, repo)
	if err != nil {
		return "", fmt.Errorf("cannot infer a base: %v; pass --base <ref>", err)
	}
	// The branch name, not the ref this clone happens to reach it through. `DefaultBranch` prefers
	// `refs/remotes/origin/HEAD`, which `git clone` records, so the ref it returns is usually a fetch ref even
	// in a clone with a local trunk — and recording that spelling put `base: refs/remotes/origin/main` into a
	// file other machines read, and made `status` report the base as a fetched remote ref for a change that
	// had never been pushed anywhere. What the name resolves to when a diff is measured is a second question,
	// and `changeset.BaseFor` answers it with the fetched copy: the record stays portable, the measurement
	// follows what this clone has fetched.
	name := db.BaseName()
	if _, err := repo.RevParse(ctx, name); err == nil {
		return name, nil
	}
	// Where the name does not resolve, this clone holds the integration branch only under the fetch root,
	// and a base that does not resolve makes every command fail: `cannot resolve changeset base "main"`.
	// The qualified ref is recorded there rather than a name that reads nicer and works nowhere.
	return db.Ref, nil
}

// baseDivergence is the note for a base whose local copy and its remote copy are different commits — the
// state behind a fresh branch reporting `ahead 1, behind 1`. The case worth a sentence is the local copy
// being ahead: the diff is measured against origin's copy (§4), so whatever this clone's trunk has that
// origin does not arrives inside the new changeset's work, because the branch was cut from it. It is a note
// and not a refusal: a base may legitimately be ahead locally, and pushing it is not git-pair's to do (§26).
func baseDivergence(ctx context.Context, repo *git.Repo, base string) string {
	if base == "" {
		return ""
	}
	local, remote := "refs/heads/"+base, "refs/remotes/origin/"+base
	here, err := repo.RevParse(ctx, local)
	if err != nil {
		return ""
	}
	there, err := repo.RevParse(ctx, remote)
	if err != nil || here == there {
		return ""
	}
	return fmt.Sprintf("note: %s is not the same commit here and on origin: %s here that origin does not have, %s on origin that is not here. "+
		"The diff git-pair measures is against origin's copy, so what only this clone's %s has reads as part of this change until you push it; pushing %s is yours.",
		base, countBetween(ctx, repo, local, remote), countBetween(ctx, repo, remote, local), base, base)
}

// countBetween is `git rev-list --count <exclude>..<from>`, and "?" when git will not say — a note that
// cannot count is still worth printing without a number, and is not worth failing `init` over.
func countBetween(ctx context.Context, repo *git.Repo, from, exclude string) string {
	out, err := repo.Git(ctx, "rev-list", "--count", exclude+".."+from)
	if err != nil {
		return "?"
	}
	return strings.TrimSpace(out)
}
