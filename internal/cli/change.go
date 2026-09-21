package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"gitpair/internal/changeset"
	"gitpair/internal/console"
	"gitpair/internal/git"
	"gitpair/internal/lifecycle"
	"gitpair/internal/marker"
	"gitpair/internal/model"
	"gitpair/internal/reviewref"
	"gitpair/internal/survival"
)

func newChangeCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "change",
		Short: "Author-side commands",
		// Without a RunE cobra treats an unmatched subcommand as a help
		// request and exits 0, which is indistinguishable from success for an
		// agent that typo'd the verb.
		RunE: groupUsage("change"),
	}
	cmd.AddCommand(newChangeInitCommand(a), newChangeUseCommand(a), newChangeReadyCommand(a), newChangeUnreadyCommand(a),
		newChangeAbandonCommand(a), newChangeFeedbackCommand(a), newChangeWaitCommand(a),
		newChangeArchiveCommand(a))
	return cmd
}

// --- change init ------------------------------------------------------------

type initOptions struct {
	base     string
	setBase  bool
	id       string
	about    string
	setAbout bool
	noCommit bool
}

func newChangeInitCommand(a *app) *cobra.Command {
	opts := &initOptions{}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create review scaffolding for the current branch",
		Long: `Create changesets/<id>/ with CHANGESET.yaml and ABOUT.md, then commit it.

The changeset ID is what git-pair calls the work from here on: it names the directory, and
once refs exist it names those too. The branch name is only where the default comes from,
so feature/booking-transaction becomes changesets/feature-booking-transaction/ unless you
say otherwise:

  git pair change init --id booking-transaction-v2

An ID is chosen, not derived, so it is never rewritten to fit: --id booking\ v2 is refused
rather than quietly turned into booking-v2, because refs named after a string nobody typed
are not findable by the person who typed it. IDs are unique in git-pair's namespace, and a
collision stops the command instead of appending a suffix.

The commit covers the changeset directory only, so whatever else is staged on
your index stays there. Use --no-commit to leave the scaffolding in the working
tree for your first implementation commit instead.

ABOUT.md gets a scaffold with the standard review headings unless you supply
content, which makes describe-and-initialise a single non-interactive call:

  git pair change init --base main --about "$DESCRIPTION"
  git pair change init --base main --about - < about.md
  cat about.md | git pair change init --base main

Existing content is never overwritten silently: replacing a populated ABOUT.md
takes --set-about, the same way changing a base takes --set-base.`,
		Example: `  git pair change init --base main
  git pair change init --base main --id booking-transaction-v2
  git pair change init --base booking-transaction   # stacked branch
  git pair change init --base main --about - < draft.md`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChangeInit(cmd.Context(), a, opts)
		},
	}
	cmd.Flags().StringVar(&opts.base, "base", "", "ref this changeset is stacked on (default: main, then master)")
	cmd.Flags().StringVar(&opts.id, "id", "", "changeset ID (default: the branch name, normalised)")
	cmd.Flags().BoolVar(&opts.setBase, "set-base", false, "overwrite an existing base value")
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
		return &usageError{fmt.Errorf("%w: run `git switch -c <branch>` before `git pair change init`", changeset.ErrDetachedHead)}
	}
	// Starting a changeset on the integration branch is refused for clarity, not correctness:
	// a changeset is measured against that branch, so one started on it is inert — every
	// directory it carries is already landed. Saying so where the mistake is made beats a
	// status line that never shows the changeset.
	if db, err := changeset.DefaultBranch(ctx, repo, a.defaultBranch); err == nil && branch == db.LocalName() {
		return &usageError{fmt.Errorf("%s is the integration branch, so a changeset started on it can never contain anything: `git switch -c <branch>` first", branch)}
	}
	base := opts.base
	if base == "" {
		// Resolved before the changeset itself. When there is no trunk to infer a base from,
		// the advice the caller needs is `--base <ref>`, and that has to be the error they
		// see rather than the generic "which branch is the integration branch" refusal.
		if base, err = defaultBase(ctx, repo, a.defaultBranch); err != nil {
			return &usageError{err}
		}
		a.warn("base: %s (pass --base to choose a different ref)\n", base)
	}

	// `change init` creates a directory, which is not a resolution question. A stacked branch
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
	if dir.Committed && !dir.Worktree {
		// Retiring a changeset is a commit. Until the deletion is committed the directory's
		// history is still live here, so the name is not free yet.
		return &usageError{fmt.Errorf("changesets/%s/ is deleted in your working tree but the deletion is not committed; commit the deletion before starting a changeset with that name again", id)}
	}
	cs.Exists = dir.Worktree
	if !cs.Exists {
		if err := refuseTakenID(ctx, repo, id); err != nil {
			return err
		}
		if res, err := changeset.ResolveCurrent(ctx, repo, a.defaultBranch); err == nil &&
			res.Selected != nil && res.Selected.Changeset.Slug != id {
			a.warn("warning: %s already carries changeset %q; this branch will hold two changesets, so commands that act on one accept --changeset <id>\n",
				branch, res.Selected.Changeset.Slug)
		}
	}
	if _, err := repo.RevParse(ctx, base); err != nil {
		a.warn("warning: base %q does not resolve yet; spans and status will fail until it does\n", base)
	}

	if own, err := changeset.BaseIsOwnBranch(ctx, repo, base, branch); err != nil {
		return err
	} else if own {
		return &usageError{fmt.Errorf("%w: changeset %q cannot be based on %s, the branch it lives on. The base would move with every commit, so the changeset could never contain anything. Create a branch for the change (`git switch -c <name>`) or pass --base <ancestor-ref>",
			changeset.ErrBaseIsOwnBranch, cs.Slug, base)}
	}

	about, err := aboutContent(opts)
	if err != nil {
		return err
	}

	written, err := changeset.Write(repo, cs, changeset.WriteOptions{
		Base:     base,
		SetBase:  opts.setBase,
		About:    about,
		SetAbout: opts.setAbout,
	})
	if err != nil {
		// These are all "your arguments describe something that already exists"
		// errors rather than repository states, so they exit 2.
		switch {
		case errors.Is(err, changeset.ErrBaseConflict),
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

	described := about != "" || !aboutIsTemplate(ctx, repo, cs)
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
	}, []string{cs.Dir})
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

// refuseTakenID enforces the uniqueness PRD §5 asks for: an id may not already be in
// use, as a directory or as a ref. Nothing is suffixed to dodge a collision — an id
// chosen for you is an id nobody chose, and it is baked into refs the moment the
// changeset is readied. A suggestion is offered only when the candidate is itself free.
func refuseTakenID(ctx context.Context, repo *git.Repo, id string) error {
	if taken, err := reviewref.Taken(ctx, repo, id); err != nil {
		return err
	} else if taken {
		return idTakenError(ctx, repo, id, "git-pair refs already exist for it")
	}
	if dir, err := changeset.DirectoryAt(ctx, repo, id); err != nil {
		return err
	} else if dir.Worktree || dir.Committed {
		return idTakenError(ctx, repo, id, "changesets/"+id+" already exists, possibly left behind by a changeset that has landed")
	}
	return nil
}

func idTakenError(ctx context.Context, repo *git.Repo, id, why string) error {
	if free := freeID(ctx, repo, id); free != "" {
		return &usageError{fmt.Errorf("changeset ID %q is already in use: %s.\n\nChoose another ID:\n\n  git pair change init --id %s", id, why, free)}
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

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// defaultBase picks the repository's trunk without guessing wildly.
// defaultBase is the base `change init` records when the author does not name one. It is the
// integration branch — the same ref the landed test compares trees against — so a changeset
// cannot be measured against one branch while being judged landed by another. The spelling is
// the short branch name, because that is what a person reads in CHANGESET.yaml.
func defaultBase(ctx context.Context, repo *git.Repo, override string) (string, error) {
	db, err := changeset.DefaultBranch(ctx, repo, override)
	if err != nil {
		return "", fmt.Errorf("cannot infer a base: %v; pass --base <ref>", err)
	}
	return db.LocalName(), nil
}

// --- change use -------------------------------------------------------------

func newChangeUseCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "use <changeset-id>",
		Short: "Record which changeset this branch is working on",
		Long: `Record, in the chosen changeset's CHANGESET.yaml, that the other changeset
directories on this branch are ones it is only sharing a branch with.

A branch normally carries one unlanded changeset. It carries more when a sibling's branch was
merged into this one, or when a branch created off a sibling started its own work without naming
that stack with ` + "`change init --base`" + `. git-pair orders what it can — the nearest review ref,
then the base of a stack — and refuses rather than guessing between the rest, because picking one
silently means reading the wrong diff base.

This command is the answer it accepts: one record, in the file of the changeset you chose, which
every command on this branch then reads. ` + "`--changeset <id>`" + ` answers the same question for
one command instead of for the branch.`,
		Example: `  git pair change use booking-transaction
  git pair status`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChangeUse(cmd.Context(), a, args[0])
		},
	}
	return cmd
}

func runChangeUse(ctx context.Context, a *app, id string) error {
	repo, err := a.loadRepo(ctx)
	if err != nil {
		return err
	}
	if err := changeset.ValidateID(id); err != nil {
		return &usageError{err}
	}
	res, err := changeset.ResolveCurrent(ctx, repo, a.defaultBranch)
	if err != nil {
		return usageWrap(err)
	}
	var names []string
	for _, c := range res.Candidates {
		names = append(names, c.Changeset.Slug)
	}

	// The common case is being told about a decision the rule already makes. Saying "already"
	// is worth more than writing a record that changes nothing.
	if res.Selected != nil && res.Selected.Changeset.Slug == id {
		a.printf("this branch already works on %s\n", id)
		return nil
	}
	if len(res.Candidates) == 0 {
		return &usageError{fmt.Errorf("no changeset is in progress on this branch; `git pair change init` starts one")}
	}
	chosen := false
	for _, n := range names {
		if n == id {
			chosen = true
		}
	}
	if !chosen {
		// Being on the branch is not the same as being offered: a candidate is dropped when
		// another changeset's record says it is only sharing the branch. Saying "not a changeset
		// here" to a directory the author can see would be a lie with no way forward, so the
		// record that dropped it is named instead.
		for _, c := range res.Candidates {
			for _, ignored := range c.Ignores {
				if ignored == id {
					return &usageError{fmt.Errorf("%q is on this branch, but %q already claims it: %s records that it ignores %q.\n\nRemove that `ignores:` line from %s, then run `git pair change use %s` again",
						id, c.Changeset.Slug, c.Changeset.MetadataPath(), id, c.Changeset.MetadataPath(), id)}
				}
			}
		}
		return &usageError{fmt.Errorf("%q is not a changeset this branch is working on; it carries %s", id, strings.Join(names, ", "))}
	}

	var ignores []string
	for _, n := range names {
		if n != id {
			ignores = append(ignores, n)
		}
	}
	// Ask the rule before writing. Another candidate may already record a choice, and a record
	// that leaves the branch undecided is not a decision — better to say so now than to leave a
	// commit for the author to undo.
	if check := res.WithIgnores(id, ignores); check.Ambiguous || check.Selected == nil ||
		check.Selected.Changeset.Slug != id {
		return &usageError{fmt.Errorf("recording %q as this branch's changeset would still leave it undecided, because another changeset here records a choice of its own.\n\nRemove its `ignores:` line from the other changeset's %s, then run `git pair change use %s` again",
			id, changeset.MetadataFile, id)}
	}

	cs := changeset.Changeset{Slug: id, Dir: filepath.Join(changeset.Root, id), Exists: true}
	changed, err := changeset.SetIgnores(repo, cs, ignores)
	if err != nil {
		return err
	}
	if !changed {
		a.printf("%s already records this choice\n", cs.MetadataPath())
		return nil
	}
	// Committed on its own, from this changeset's directory only. Leaving it uncommitted would
	// have every later command on the branch refuse until the author remembered to commit it,
	// and the record is a review artifact of this branch rather than a change to review.
	sha, err := marker.CommitPaths(ctx, repo, marker.Message{
		Subject:  fmt.Sprintf("git-pair: work on changeset %s", id),
		Trailers: []string{"Review-Changeset=" + id},
	}, []string{cs.MetadataPath()})
	if err != nil {
		if isNothingToCommit(err) {
			a.printf("nothing to commit (%s already records this choice)\n", cs.MetadataPath())
			return nil
		}
		return err
	}
	a.printf("recorded %s as this branch's changeset: %s now ignores %s\n", id, cs.MetadataPath(), strings.Join(ignores, ", "))
	a.printf("committed %s\n", short(sha))
	return nil
}

// --- change ready -----------------------------------------------------------

type readyOptions struct {
	allowSurviving bool
}

func newChangeReadyCommand(a *app) *cobra.Command {
	opts := &readyOptions{}
	cmd := &cobra.Command{
		Use:   "ready",
		Short: "Mark the current changeset ready for human review",
		Long: `Create a lifecycle commit that puts this changeset in the review queue.

Checks, in order, that the working tree is clean, ABOUT.md exists, and no
non-blank line added by the most recent review submission still survives
unchanged at HEAD. The command is fully non-interactive: it reports what it
found and exits non-zero rather than asking questions.

Readiness is not taken away by a commit. The changeset stays READY until you run
` + "`change unready`" + ` or a reviewer submits, so committing work in progress never drops it out of
the queue on its own — and ` + "`change unready`" + ` is how you leave it on purpose.`,
		Example: `  git pair change ready
  git pair change ready --allow-surviving-review-additions
  git pair change ready --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChangeReady(cmd.Context(), a, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.allowSurviving, "allow-surviving-review-additions", false,
		"acknowledge surviving review additions and mark ready anyway")
	return cmd
}

func runChangeReady(ctx context.Context, a *app, opts *readyOptions) error {
	s, err := a.load(ctx)
	if err != nil {
		return err
	}

	if !s.clean {
		// Repository state, not bad arguments: exit 1 so an agent can tell
		// "fix the tree and retry" apart from "you invoked this wrong".
		return fmt.Errorf("working tree must be clean before marking %s ready; commit or stash your changes first", s.cs.Slug)
	}
	if !s.cs.AboutExists(s.repo) {
		return fmt.Errorf("%s is missing; describe the change before marking it ready", s.cs.AboutPath())
	}
	if s.head == "" {
		return fmt.Errorf("nothing to mark ready: the repository has no commits yet")
	}
	if s.baseIsOwnBranch {
		// Refuse rather than write a marker nobody can read: the marker would
		// land on the commit that *is* the base, i.e. below the range that
		// derives state, so `ready` would report success and `status` would
		// keep saying WORKING.
		return fmt.Errorf("cannot mark %s ready: base %q is this branch itself, so the changeset can never contain commits. Set `base` in %s to an ancestor of %s, or move the work to its own branch",
			s.cs.Slug, s.cs.Base, s.cs.MetadataPath(), s.cs.Branch)
	}
	if err := a.refuseIfAbandoned(ctx, s); err != nil {
		return err
	}

	report, err := survivalCheck(ctx, s)
	if err != nil {
		return err
	}
	if report != nil && !report.Clean() && !opts.allowSurviving {
		printSurvivalReport(a.stderr, *report,
			fmt.Sprintf("Cannot mark changeset %s ready.", s.cs.Slug),
			"git pair change ready --allow-surviving-review-additions")
		printArtifactSurvivals(a.stderr, *report)
		return fmt.Errorf("cannot mark changeset %s ready: %d review addition(s) from %s still survive unchanged",
			s.cs.Slug, len(report.Code), report.ReviewShort)
	}

	// Warn about an ABOUT.md that is still the untouched template: an agent
	// that never described the change is the most common cause of a confused
	// reviewer. Informational only, so it cannot block automation.
	if aboutIsTemplate(ctx, s.repo, s.cs) {
		a.warn("warning: %s still has the empty `change init` template; describe the change for the reviewer\n",
			s.cs.AboutPath())
	}

	sha, err := marker.Commit(ctx, s.repo, marker.ReadyMessage(s.cs.Slug))
	if err != nil {
		return fmt.Errorf("creating ready marker: %w", err)
	}
	// Offering the changeset is the moment its commits stop being disposable. Until
	// now only a review submission anchored them, so work that was offered and never
	// reviewed — or work whose branch exists only in the reflog — could be pruned with
	// its ready marker, and the history the archive is supposed to preserve would be
	// gone before anyone read it. The move is a warning rather than a guarantee, like
	// every review ref: nothing here stops a later `git push --delete` (PRD §13).
	if _, err := reviewref.Update(ctx, s.repo, s.cs.Slug, sha); err != nil {
		return fmt.Errorf("anchoring the ready marker: %w", err)
	}
	printReady(a, s, sha, report, opts.allowSurviving)
	return nil
}

func printReady(a *app, s *session, sha string, report *survival.Report, acknowledged bool) {
	if a.json {
		out := map[string]any{
			"changeset":              s.cs.Slug,
			"branch":                 s.cs.Branch,
			"base":                   s.cs.Base,
			"state":                  string(model.StateReady),
			"head":                   short(sha),
			"ready_commit":           sha,
			"review_queue_visible":   true,
			"acknowledged_survivors": 0,
		}
		if report != nil {
			out["acknowledged_survivors"] = len(report.Code)
			out["surviving_review_artifacts"] = len(report.Artifacts)
		}
		_ = a.emitJSON(out)
		return
	}
	if acknowledged && report != nil && !report.Clean() {
		a.printf("Acknowledged %d surviving review addition(s) from review %s.\n",
			len(report.Code), report.ReviewShort)
	}
	a.printf("Ready: %s\n", s.cs.Slug)
	a.printf("  head:  %s\n", short(sha))
	a.printf("  base:  %s\n", s.cs.Base)
	a.printf("  queue: `git pair review queue` now lists this changeset\n")
}

// aboutIsTemplate reports whether ABOUT.md is byte-identical to the scaffold,
// ignoring the title line which contains the changeset name.
func aboutIsTemplate(ctx context.Context, repo *git.Repo, cs changeset.Changeset) bool {
	data, err := os.ReadFile(filepath.Join(repo.Dir, cs.AboutPath()))
	if err != nil {
		return false
	}
	template := strings.Split(strings.TrimRight(strings.TrimPrefix(changeset.AboutTemplate(cs.Slug), "# "+cs.Slug+"\n"), "\n"), "\n")
	actual := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(actual) < 2 {
		return false
	}
	actual = actual[1:]
	if len(actual) != len(template) {
		return false
	}
	for i := range template {
		if strings.TrimSpace(actual[i]) != strings.TrimSpace(template[i]) {
			return false
		}
	}
	return true
}

func isNothingToCommit(err error) bool {
	var ge *git.Error
	if errors.As(err, &ge) {
		// git sends this notice to stdout, not stderr.
		return strings.Contains(strings.ToLower(ge.Stderr+ge.Stdout), "nothing to commit")
	}
	return false
}

// --- change unready --------------------------------------------------------

func newChangeUnreadyCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unready",
		Short: "Take the current changeset out of the review queue",
		Long: `Record that this changeset is no longer offered for review, and take it out of the queue.

Use it when you want to keep implementing after ` + "`change ready`" + `. The marker says so on
purpose rather than leaving a reviewer to work it out from the diff, which is the difference between
an offer you withdrew and an offer you forgot to withdraw.

The changeset returns to WORKING, and a later ` + "`git pair change ready`" + ` puts it back in the
queue under the same gate as the first time: review additions that still survive unchanged have to be
resolved or acknowledged.

The archive ref moves onto the withdrawal, as it does for every state a command records, so the
retraction is still there to read after the branch is deleted. Otherwise the durable record would name
the offer and only the offer, and a changeset read from the anchor would look like it was still waiting
for a reviewer.

The marker is written only when the changeset is actually in review — READY, APPROVED or FEEDBACK. On
a changeset that is WORKING or BLOCKED there is nothing to withdraw, so the command succeeds without
recording anything and a script can unready unconditionally.`,
		Example: `  git pair change unready
  git pair change unready --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChangeUnready(cmd.Context(), a)
		},
	}
	return cmd
}

func runChangeUnready(ctx context.Context, a *app) error {
	s, err := a.load(ctx)
	if err != nil {
		return err
	}
	if !s.clean {
		return fmt.Errorf("working tree must be clean before taking %s out of review; commit or stash your changes first", s.cs.Slug)
	}
	if err := a.refuseIfAbandoned(ctx, s); err != nil {
		return err
	}
	if !inReview(s.summary.State) {
		return printUnready(a, s, "")
	}
	sha, err := marker.Commit(ctx, s.repo, marker.UnreadyMessage(s.cs.Slug))
	if err != nil {
		return fmt.Errorf("creating unready marker: %w", err)
	}
	// The archive follows the withdrawal for the same reason it follows every other state a command
	// records: the branch is the thing that gets deleted. A retraction that lives only on the branch
	// is a retraction the durable record never received — the ref would keep naming the offer, and a
	// changeset read from the anchor after `git branch -D` would report work the author had explicitly
	// taken back as still waiting for a reviewer. `reviewref.Update` is the one path that moves the
	// ref, and it refuses once the changeset is integrated (§13.3); `marker.Commit` had already refused
	// before the commit, so a landed changeset gets neither a marker nor a move.
	if _, err := reviewref.Update(ctx, s.repo, s.cs.Slug, sha); err != nil {
		return fmt.Errorf("anchoring the unready marker: %w", err)
	}
	return printUnready(a, s, sha)
}

// inReview reports whether the changeset is currently offered to a reviewer, and so
// has a readiness that `change unready` can withdraw. BLOCKED is excluded because the
// author is already expected to act, and the block marker stays the newest marker until
// they ready the changeset again.
func inReview(state model.State) bool {
	switch state {
	case model.StateReady, model.StateApproved, model.StateFeedback:
		return true
	}
	return false
}

// printUnready reports the retraction. An empty sha means there was nothing to retract:
// the command still succeeded, and says why it recorded nothing.
func printUnready(a *app, s *session, sha string) error {
	// The marker is the newest commit, so its state is the derived state by
	// construction. Without one the changeset keeps whatever state it had, which for
	// a BLOCKED changeset is not WORKING.
	state := s.summary.State
	if sha != "" {
		state = model.StateWorking
	}
	if a.json {
		return a.emitJSON(map[string]any{
			"changeset":            s.cs.Slug,
			"branch":               s.cs.Branch,
			"base":                 s.cs.Base,
			"state":                string(state),
			"was":                  string(s.summary.State),
			"recorded":             sha != "",
			"unready_commit":       sha,
			"review_queue_visible": false,
		})
	}
	if sha == "" {
		a.printf("%s is not in the review queue (%s): nothing to withdraw\n", s.cs.Slug, s.summary.Reason)
		return nil
	}
	a.printf("Unready: %s\n", s.cs.Slug)
	a.printf("  head:    %s\n", short(sha))
	a.printf("  was:     %s\n", s.summary.State)
	a.printf("  queue:   `git pair review queue` no longer lists this changeset\n")
	a.printf("  next:    `git pair change ready` puts it back once the work is done\n")
	return nil
}

// --- change abandon ---------------------------------------------------------

func newChangeAbandonCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "abandon",
		Short: "End the current changeset and keep its history reachable",
		Long: `Record that this changeset will not be taken forward, and anchor the record.

Abandoning is a decision about work that is still here, which is why the command needs the branch:
the terminal marker is an ordinary commit on it, and the movable ref moves to that commit, which is
what keeps the whole chain reachable after ` + "`git branch -D`" + `. Once the ref holds the marker,
` + "`git pair review queue`" + ` can stay silent about the changeset even with no branch left — it can
read the ending from the anchor.

Unlike ` + "`change unready`" + `, which withdraws an offer for now, this one closes the changeset:
` + "`change ready`" + `, ` + "`change unready`" + ` and ` + "`review submit`" + ` refuse against it
afterwards, whether they meet it on the branch or on the anchor. That is also what stops a new branch
reusing the name of a changeset that ended.

The state stays WORKING and no state value is added: the ending is reported as
` + "`abandoned_commit`" + ` in ` + "`status --json`" + `, beside the state rather than inside it.

Re-running the command changes nothing and succeeds. Abandoning work whose branch is already gone
refuses: post-merge bookkeeping is not a lifecycle act.`,
		Example: `  git pair change abandon
  git pair change abandon --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChangeAbandon(cmd.Context(), a)
		},
	}
	return cmd
}

func runChangeAbandon(ctx context.Context, a *app) error {
	s, err := a.load(ctx)
	if err != nil {
		return err
	}
	if !s.clean {
		return fmt.Errorf("working tree must be clean before abandoning %s; commit or stash your changes first", s.cs.Slug)
	}
	at, err := terminalRecord(ctx, s.repo, s.cs.Slug, s.cs.Base, s.summary)
	if err != nil {
		return err
	}
	if at != nil {
		// Already ended. Like `change unready` on a changeset that was never offered,
		// the wanted state already holds, so a script can abandon unconditionally.
		return printAbandoned(a, s, at, "")
	}

	sha, err := marker.Commit(ctx, s.repo, marker.AbandonedMessage(s.cs.Slug))
	if err != nil {
		return fmt.Errorf("creating abandon marker: %w", err)
	}
	// The ref is the point of the whole operation: a terminal record on a branch that
	// gets deleted is a record that disappears with it.
	if _, err := reviewref.Update(ctx, s.repo, s.cs.Slug, sha); err != nil {
		return fmt.Errorf("anchoring the abandon marker: %w", err)
	}
	return printAbandoned(a, s, &lifecycle.Event{SHA: sha, Short: short(sha), Kind: lifecycle.KindAbandoned}, sha)
}

// printAbandoned reports the ending. An empty sha means the changeset was already
// abandoned, and the marker named by `at` is the one that says so.
func printAbandoned(a *app, s *session, at *lifecycle.Event, sha string) error {
	if a.json {
		return a.emitJSON(map[string]any{
			"changeset":        s.cs.Slug,
			"branch":           s.cs.Branch,
			"state":            string(model.StateWorking),
			"was":              string(s.summary.State),
			"recorded":         sha != "",
			"abandoned_commit": at.SHA,
			"archive_ref":      reviewref.Archive(s.cs.Slug),
		})
	}
	if sha == "" {
		a.printf("Changeset %s is already abandoned by %s.\n", s.cs.Slug, at.Short)
		return nil
	}
	a.printf("Abandoned changeset %s\n\n", s.cs.Slug)
	a.printf("Terminal marker: %s\n", sha)
	a.printf("Review history stays reachable at %s\n", reviewref.Archive(s.cs.Slug))
	return nil
}

// terminalRecord returns the newest abandon marker for a changeset, looking at the
// branch chain it was derived from and then at the anchor. Both have to be consulted:
// the branch is the thing that gets deleted, and a slug recreated after `git branch -D`
// carries no markers at all, so only the anchor still says the work ended.
func terminalRecord(ctx context.Context, repo *git.Repo, slug, base string, derived lifecycle.Summary) (*lifecycle.Event, error) {
	if derived.Abandoned != nil {
		return derived.Abandoned, nil
	}
	anchor, err := reviewref.Resolve(ctx, repo, slug)
	if errors.Is(err, reviewref.ErrNoArchiveRef) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	summary, err := lifecycle.Summarize(ctx, repo, slug, base, anchor)
	if err != nil {
		return nil, err
	}
	return summary.Abandoned, nil
}

// refuseIfAbandoned is the write gate: nothing records a marker onto a changeset that
// has ended. Exit 1 rather than 2 — the repository says no, and re-running after undoing
// the abandonment (there is no such command; the marker is history) is not a retry.
func (a *app) refuseIfAbandoned(ctx context.Context, s *session) error {
	at, err := terminalRecord(ctx, s.repo, s.cs.Slug, s.cs.Base, s.summary)
	if err != nil {
		return err
	}
	if at != nil {
		return fmt.Errorf("changeset %s was abandoned by %s; git-pair records nothing further for it",
			s.cs.Slug, at.Short)
	}
	return nil
}

// --- change archive ---------------------------------------------------------

type archiveOptions struct {
	allowSurviving  bool
	allowUnreviewed bool
}

func newChangeArchiveCommand(a *app) *cobra.Command {
	opts := &archiveOptions{}
	cmd := &cobra.Command{
		Use:   "archive",
		Short: "Advance the changeset's archive ref to HEAD",
		Long: `Advance the archive ref — the durable ref holding the whole unsquashed chain of
implementation commits, ready markers, review submissions, replies and approvals — to HEAD.

Review submission already moves the ref, so an approval usually leaves it current. What this
command is for is what comes after the review: a reply in a thread, a rewritten ABOUT.md, another
note in the changeset directory. Those are review artifacts, not implementation, and the archive
should not stop where the last review marker happened to fall.

Checks, in order: the working tree is clean; the newest review at HEAD permits integration
(approve or feedback) and still describes what HEAD carries — the tree is compared between that
marker and HEAD, ignoring changesets/<changeset>/, which is why a thread reply does not invalidate
an approval; and no non-blank addition from the most recent review survives unchanged. Then the
ref moves.

Archiving records no commit and moves no state. A reviewer's approve is a judgement about the
code; this is the owner's decision that the reviewed state is what they are taking forward, so the
changeset stays whatever the markers say it is, and it is finished when that archived history is
merged into the deployment branch — ordinary git, which stays yours. ` + "`git pair status`" + `
reports where the archive points.

Already at HEAD, it succeeds and writes nothing. It will not move the archive to a commit behind
its current tip: that would drop archived history from the only ref guaranteeing it stays
reachable, and the refusal names both commits. It never merges, pushes, or squashes.`,
		Example: `  git pair change archive
  git pair change archive --allow-surviving-review-additions
  git pair change archive --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChangeArchive(cmd.Context(), a, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.allowSurviving, "allow-surviving-review-additions", false,
		"acknowledge surviving review additions and archive anyway")
	cmd.Flags().BoolVar(&opts.allowUnreviewed, "allow-unreviewed-changes", false,
		"acknowledge that HEAD carries content beyond the reviewed marker and archive anyway")
	return cmd
}

func runChangeArchive(ctx context.Context, a *app, opts *archiveOptions) error {
	s, err := a.load(ctx)
	if err != nil {
		return err
	}
	if !s.clean {
		return fmt.Errorf("working tree must be clean before archiving %s; commit or stash your changes first", s.cs.Slug)
	}
	// Before the review gate, because the gate would refuse an abandoned changeset too
	// — it derives WORKING — and say the wrong thing about why. This refusal also closes
	// a hazard: archiving reports squash-safety, and an abandoned changeset is one whose
	// archive happens to be current because `change abandon` moved the ref there. There is
	// nothing to take forward, so there is nothing to archive.
	if err := a.refuseIfAbandoned(ctx, s); err != nil {
		return err
	}
	// §13.3: an integrated changeset's archive is frozen. The check is here rather than left to
	// `reviewref.Update` because of the already-there shortcut further down: "the archive points
	// here already, nothing moved" is true and useless to whoever is standing on a changeset that
	// has landed, when the thing they need to hear is that the record exists and the review is over.
	if err := reviewref.RefuseIntegrated(ctx, s.repo, s.cs.Slug); err != nil {
		return err
	}
	// Archiving is the one command that asks whether the reviewed content is still
	// here. The archive it writes is a promise about a reviewed head — it is what an
	// agent is told to trust before squash-merging — so an approval with fresh
	// implementation work stacked on top of it must not be archived (PRD §9.5, §12).
	// Everywhere else a commit after a marker is an observation, not a verdict.
	//
	// The gate is the review at HEAD rather than a lifecycle state named
	// "archivable": archiving records no commit, so there is no state for it to
	// move the changeset into.
	reviewed, err := lifecycle.SummarizeAgainstTreeHEAD(ctx, s.repo, s.cs.Slug, s.cs.Base)
	if err != nil {
		return err
	}
	switch {
	case reviewed.State == model.StateApproved || reviewed.State == model.StateFeedback:
		// Integration is permitted at this head.
	case opts.allowUnreviewed && driftOverWhichToProceed(reviewed):
		// Acknowledged in the output, not hidden: the archive still names this head.
	default:
		return fmt.Errorf("cannot archive %s: latest outcome is %s (%s); archiving needs an approve or feedback at HEAD",
			s.cs.Slug, reviewed.State, reviewed.Reason)
	}

	report, err := survivalCheck(ctx, s)
	if err != nil {
		return err
	}
	if report != nil && !report.Clean() && !opts.allowSurviving {
		printSurvivalReport(a.stderr, *report,
			fmt.Sprintf("Cannot archive changeset %s.", s.cs.Slug),
			"git pair change archive --allow-surviving-review-additions")
		printArtifactSurvivals(a.stderr, *report)
		return fmt.Errorf("cannot archive %s: %d review addition(s) from %s still survive unchanged",
			s.cs.Slug, len(report.Code), report.ReviewShort)
	}

	head, err := s.repo.Head(ctx)
	if err != nil {
		return err
	}
	// Where the archive stands now. Before the first handoff there is no ref at all: the
	// archive comes into existence with the first ready or review submission, which is the
	// point from which there is review history worth keeping.
	was, err := reviewref.Resolve(ctx, s.repo, s.cs.Slug)
	if err != nil && !errors.Is(err, reviewref.ErrNoArchiveRef) {
		return err
	}
	if was == head {
		return printArchive(a, s, reviewed, head, was, report, opts.allowSurviving)
	}
	// Forward only. A target behind the current tip would drop the archived chain from the
	// one ref that keeps it reachable — the case is an author archiving from an old checkout,
	// or after winding their branch back, and in both what gets dropped is the history a
	// squash merge would silently lose. A rebase is not this: rewritten history is neither
	// ahead nor behind, and its markers moved with it.
	if was != "" {
		backwards, err := s.repo.IsAncestor(ctx, head, was)
		if err != nil {
			return err
		}
		if backwards {
			return fmt.Errorf("cannot archive %s at %s: %s is at %s, which is ahead of it. The archive only moves forward; bring this branch up to it, or archive the commit you mean from there",
				s.cs.Slug, short(head), reviewref.Archive(s.cs.Slug), short(was))
		}
	}
	if _, err := reviewref.Update(ctx, s.repo, s.cs.Slug, head); err != nil {
		return err
	}
	return printArchive(a, s, reviewed, head, was, report, opts.allowSurviving)
}

// driftOverWhichToProceed reports the one refusal an author may acknowledge: the newest
// marker is a review permitting integration, and content outside changesets/<slug>/ has
// arrived on top of it — a README typo fixed after the approval is the case this is for.
// Only `Outcome` picks the marker: non-review markers carry the zero outcome, so a `block`
// and a `change unready` fall through without a special case, and the drift requirement
// keeps the hatch from swallowing a marker git-pair cannot read.
func driftOverWhichToProceed(s lifecycle.Summary) bool {
	m := s.Marker
	return m != nil && m.Outcome.PermitsIntegration() && len(s.Drifted) > 0
}

// printArchive reports where the archive now stands. `was` is the commit the ref pointed at
// before this call, and empty when it did not exist: both are worth distinguishing from a move,
// because "nothing to do" and "created the ref" are different answers to why HEAD is archived.
func printArchive(a *app, s *session, reviewed lifecycle.Summary, head, was string,
	report *survival.Report, acknowledged bool) error {
	archiveRef := reviewref.Archive(s.cs.Slug)
	advanced := was != head
	if a.json {
		out := map[string]any{
			"changeset":                     s.cs.Slug,
			"state":                         string(reviewed.State),
			"acknowledged_unreviewed_paths": len(reviewed.Drifted),
			"head":                          head,
			"short":                         short(head),
			"base":                          s.cs.Base,
			"archive_ref":                   archiveRef,
			"archive_was":                   was,
			"archive_advanced":              advanced,
			"squash_safe":                   true,
			"acknowledged_survivors":        0,
			"surviving_review_artifacts":    0,
		}
		if report != nil {
			out["acknowledged_survivors"] = len(report.Code)
			out["surviving_review_artifacts"] = len(report.Artifacts)
		}
		return a.emitJSON(out)
	}
	if acknowledged && report != nil && !report.Clean() {
		a.printf("Acknowledged %d surviving review addition(s) from review %s.\n\n",
			len(report.Code), report.ReviewShort)
	}
	if len(reviewed.Drifted) > 0 {
		// The archive names this head, so the author is told plainly that it carries
		// content the review did not see.
		a.printf("Acknowledged %d path(s) outside changesets/%s/: %s.\n\n",
			len(reviewed.Drifted), s.cs.Slug, strings.TrimSuffix(reviewed.Reason, "."))
	}
	if !advanced {
		a.printf("Changeset %s is already archived at %s\n\n", s.cs.Slug, short(head))
		a.printf("%s already points there; nothing moved.\n\n", archiveRef)
	} else {
		a.printf("Archived changeset %s at %s\n\n", s.cs.Slug, short(head))
		if was == "" {
			a.printf("%s created\n\n", archiveRef)
		} else {
			a.printf("%s: %s → %s\n\n", archiveRef, short(was), short(head))
		}
	}
	a.printf("Safe to squash/merge.\n")
	a.printf("Review history stays reachable at %s\n", archiveRef)
	return nil
}

// --- change feedback --------------------------------------------------------

type feedbackOptions struct {
	stat      bool
	nameOnly  bool
	changeset string
}

func newChangeFeedbackCommand(a *app) *cobra.Command {
	opts := &feedbackOptions{}
	cmd := &cobra.Command{
		Use:   "feedback",
		Short: "Show what the most recent review submission told you",
		Long: `Print the diff of the latest review submission: <review>^ .. <review>.

This is the author's command for consuming review feedback. It shows everything the
reviewer introduced — source edits, added comments, ABOUT.md changes, new threads and
replies to existing ones — through git's own diff plumbing, with no custom renderer.

` + "`git pair diff --unreviewed`" + ` is the reviewer's command and answers a different question:
` + "`<last review>..current`" + `, what changed *after* the review. Immediately after a submission
that span is empty, because the review commit is the newest thing on the branch, so it cannot be
how an author reads the feedback that was just written.

Exits non-zero when the changeset has no review submission yet.`,
		Example: `  git pair change feedback
  git pair change feedback --stat
  git pair change feedback --name-only
  git pair change feedback --changeset booking-transaction`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChangeFeedback(cmd.Context(), a, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.stat, "stat", false, "show a diffstat instead of the full diff")
	cmd.Flags().BoolVar(&opts.nameOnly, "name-only", false, "list only the files the review changed")
	cmd.Flags().StringVar(&opts.changeset, "changeset", "",
		"read the changeset with this slug, from whichever branch carries it")
	return cmd
}

func runChangeFeedback(ctx context.Context, a *app, opts *feedbackOptions) error {
	s, err := a.loadFor(ctx, opts.changeset)
	if err != nil {
		return err
	}
	if opts.stat && opts.nameOnly {
		return &usageError{errors.New("--stat and --name-only cannot be combined")}
	}
	review := s.summary.LatestReview
	if review == nil {
		return &usageError{fmt.Errorf(
			"changeset %s has no review submission yet, so there is no feedback to read; "+
				"`git pair change wait` blocks until a reviewer submits one", s.cs.Slug)}
	}
	from, err := reviewParent(ctx, s.repo, review)
	if err != nil {
		return err
	}
	names, err := s.repo.DiffNames(ctx, from, review.SHA)
	if err != nil {
		return err
	}

	a.warn("git pair change feedback: review %s (%s) on %s\n", review.Short, review.Outcome, s.cs.Slug)
	switch {
	case len(names) == 0:
		// A review that touches nothing is still a verdict; saying so beats an
		// empty screen that looks like a broken command.
		a.warn("(the review changed no files: the outcome and its message are the feedback)\n")
		return nil
	case opts.nameOnly:
		for _, n := range names {
			a.printf("%s\n", n)
		}
		return nil
	case opts.stat:
		return s.repo.GitInherit(ctx, "diff", "--stat", from, review.SHA)
	default:
		return console.DiffCommand(s.repo, from, review.SHA, nil).Run()
	}
}

// reviewParent is the state a review submission was made against. A submission with no
// parent is compared against the empty tree, the same reading lifecycle uses for a root
// marker.
func reviewParent(ctx context.Context, repo *git.Repo, review *lifecycle.Event) (string, error) {
	sha, err := repo.RevParse(ctx, review.SHA+"^")
	if err != nil {
		if errors.Is(err, git.ErrUnknownRevision) {
			return git.EmptyTree, nil
		}
		return "", err
	}
	return sha, nil
}

// --- change wait ------------------------------------------------------------

type waitOptions struct {
	fetch    bool
	interval string
	timeout  string
}

func newChangeWaitCommand(a *app) *cobra.Command {
	opts := &waitOptions{}
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Block until a reviewer makes the changeset actionable",
		Long: `Wait for review activity, so an author can hand off and sleep instead of polling.

Exits when the changeset stops being ready and becomes actionable — BLOCKED, FEEDBACK
or APPROVED — and prints what happened. Fully non-interactive.

  git pair change ready
  git pair change wait --fetch --interval 30s --json
  git pair change feedback

Without --fetch only local repository state is re-read, which is enough when the reviewer
works in the same clone. With --fetch every round runs ` + "`git fetch`" + ` against the
configured remote first and also evaluates the branch's remote-tracking ref, so reviews
submitted in another clone end the wait. This stays forge-agnostic: no forge APIs, only
ordinary refs that git fetch brings down.

Exits non-zero when the changeset is still WORKING — it was never handed off, so there is nobody
to wait for — and on timeout. A changeset already BLOCKED, FEEDBACK or APPROVED is the answer you
asked for: it reports at once and exits 0. Without --timeout it waits indefinitely.`,
		Example: `  git pair change wait
  git pair change wait --fetch
  git pair change wait --fetch --interval 30s --timeout 2h --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChangeWait(cmd.Context(), a, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.fetch, "fetch", false, "run `git fetch` before each check")
	cmd.Flags().StringVar(&opts.interval, "interval", "10s", "pause between checks (go duration: 30s, 1m)")
	cmd.Flags().StringVar(&opts.timeout, "timeout", "", "give up after this long (default: wait forever)")
	return cmd
}

// waitResult is what `change wait --json` prints. States use the same spelling as
// `git pair status --json`, so an agent can compare what the two commands report.
type waitResult struct {
	Changeset        string `json:"changeset"`
	PreviousState    string `json:"previous_state"`
	State            string `json:"state"`
	ReviewCommit     string `json:"review_commit,omitempty"`
	ReviewCommitFull string `json:"review_commit_full,omitempty"`
	Ref              string `json:"ref"`
	Fetches          int    `json:"fetches"`
	WaitedSeconds    int    `json:"waited_seconds"`
	TimedOut         bool   `json:"timed_out"`
	NextAction       string `json:"next_action"`
}

// waitInput is what a check found; reportWait turns it into the printed contract.
type waitInput struct {
	PreviousState string
	State         model.State
	Ref           string
	Review        *lifecycle.Event
	Fetches       int
	Waited        int
	TimedOut      bool
}

func runChangeWait(ctx context.Context, a *app, opts *waitOptions) error {
	s, err := a.load(ctx)
	if err != nil {
		return err
	}
	interval, err := parseWaitDuration(opts.interval, "--interval")
	if err != nil {
		return err
	}
	if interval <= 0 {
		return &usageError{fmt.Errorf("--interval must be positive, got %q", opts.interval)}
	}
	var timeout time.Duration
	if opts.timeout != "" {
		timeout, err = parseWaitDuration(opts.timeout, "--timeout")
		if err != nil {
			return err
		}
		if timeout <= 0 {
			return &usageError{fmt.Errorf("--timeout must be positive, got %q", opts.timeout)}
		}
	}

	start, err := lifecycle.SummarizeHEAD(ctx, s.repo, s.cs.Slug, s.cs.Base)
	if err != nil {
		return err
	}
	if start.State == model.StateWorking {
		return fmt.Errorf("changeset %s is %s, not ready: `git pair change ready` puts it in the review queue",
			s.cs.Slug, string(start.State))
	}

	remotes := []string(nil)
	if opts.fetch {
		remotes, err = fetchTargets(ctx, s.repo)
		if err != nil {
			return err
		}
		if len(remotes) == 0 {
			return &usageError{errors.New(
				"no git remote is configured, so --fetch has nothing to fetch; " +
					"add one (`git remote add origin <url>`) or drop --fetch and watch local state only")}
		}
	}

	branch, err := s.repo.CurrentBranch(ctx)
	if err != nil {
		return err
	}
	began := time.Now()
	if !a.json {
		a.warn("git-pair: waiting for review activity on %s (every %s%s)\n",
			s.cs.Slug, interval, fetchNote(remotes))
	}

	fetches := 0
	seen, found, err := pollUntil(ctx, interval, timeout, func() (waitInput, bool, error) {
		if opts.fetch {
			for _, remote := range remotes {
				if err := s.repo.Fetch(ctx, remote); err != nil {
					// A failed fetch is not a reason to stop waiting: the next round
					// may succeed, and the review could land locally anyway.
					a.warn("git-pair: fetch %s failed: %s\n", remote, messageOf(err))
				}
			}
			fetches++
		}
		obs, err := s.observeReview(ctx, branch, opts.fetch)
		if err != nil {
			return waitInput{}, false, err
		}
		waited := int(time.Since(began).Seconds())
		if obs.State != "" {
			return waitInput{PreviousState: stateName(start.State), State: obs.State, Ref: obs.Ref,
				Review: obs.Review, Fetches: fetches, Waited: waited}, true, nil
		}
		// Reported when the wait ends without news, so the answer says where things
		// stand rather than nothing at all.
		return waitInput{PreviousState: stateName(start.State), State: obs.Local, Ref: "HEAD",
			Fetches: fetches, Waited: waited}, false, nil
	})
	if err != nil {
		return err
	}
	seen.TimedOut = !found
	return reportWait(a, s.cs.Slug, seen)
}

// pollUntil calls check every interval until it reports a result, the timeout elapses,
// or ctx is cancelled; a zero timeout waits indefinitely. Split out of runChangeWait so
// the waiting behaviour is testable without a reviewer in the room.
func pollUntil(ctx context.Context, interval, timeout time.Duration,
	check func() (waitInput, bool, error)) (waitInput, bool, error) {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	var last waitInput
	for {
		out, found, err := check()
		if err != nil {
			return waitInput{}, false, err
		}
		if found {
			return out, true, nil
		}
		last = out
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return last, false, nil
		}
		// Sleep no longer than what is left of the timeout: an agent that asked for
		// 30 seconds must not be held for the full polling interval.
		wait := interval
		if !deadline.IsZero() {
			if until := time.Until(deadline); wait > until {
				wait = until
			}
		}
		select {
		case <-ctx.Done():
			return waitInput{}, false, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// observation is what one check of the repository found.
type observation struct {
	// State is an actionable state, or empty when nothing is actionable yet.
	State model.State
	// Local is the state of the working copy this round, actionable or not, so a
	// timeout can report where things actually stand instead of an empty string.
	Local  model.State
	Ref    string
	Review *lifecycle.Event
}

// observeReview looks for review activity that makes the changeset actionable, first in
// the working copy and then — only when fetching — in remote-tracking copies of the same
// branch, which is where a review submitted in another clone lands.
func (s *session) observeReview(ctx context.Context, branch string, includeRemote bool) (observation, error) {
	local, err := lifecycle.SummarizeHEAD(ctx, s.repo, s.cs.Slug, s.cs.Base)
	if err != nil {
		return observation{}, err
	}
	seen := observation{Local: local.State, Ref: "HEAD"}
	if actionable(local.State) {
		seen.State, seen.Review = local.State, local.LatestReview
		return seen, nil
	}
	if !includeRemote || branch == "" {
		return seen, nil
	}
	refs, err := s.repo.ForEachRef(ctx, "refs/remotes")
	if err != nil {
		return observation{}, err
	}
	for _, ref := range refs {
		if filepath.Base(ref.Name) != branch {
			continue
		}
		sum, err := lifecycle.Summarize(ctx, s.repo, s.cs.Slug, s.cs.Base, ref.Name)
		if err != nil {
			// A remote ref can be anything: a changeset that does not exist there,
			// or history without a base. Skipping is correct, failing is not.
			continue
		}
		if actionable(sum.State) {
			seen.State, seen.Ref, seen.Review = sum.State, ref.Name, sum.LatestReview
			return seen, nil
		}
	}
	return seen, nil
}

func reportWait(a *app, slug string, r waitInput) error {
	out := waitResult{
		Changeset: slug, PreviousState: r.PreviousState, State: stateName(r.State),
		Ref: r.Ref, Fetches: r.Fetches, WaitedSeconds: r.Waited, TimedOut: r.TimedOut,
	}
	if r.Review != nil {
		out.ReviewCommit = r.Review.Short
		out.ReviewCommitFull = r.Review.SHA
	}
	out.NextAction = waitNextAction(r.State, r.Ref)

	if a.json {
		if err := a.emitJSON(out); err != nil {
			return err
		}
		if r.TimedOut {
			return errSilent
		}
		return nil
	}
	if r.TimedOut {
		// The exit code says "nothing happened"; the hint says what to do about it.
		a.printf("Timed out waiting for review activity on %s after %ds (%d fetch(es)).\n",
			slug, out.WaitedSeconds, out.Fetches)
		a.printf("  next:    %s\n", out.NextAction)
		return errors.New("timed out waiting for review activity")
	}
	a.printf("Review activity on %s: %s\n", slug, strings.ToUpper(string(r.State)))
	if out.ReviewCommit != "" {
		a.printf("  review:  %s\n", out.ReviewCommit)
	}
	a.printf("  ref:     %s\n", out.Ref)
	a.printf("  waited:  %ds over %d fetch(es)\n", out.WaitedSeconds, out.Fetches)
	a.printf("  next:    %s\n", out.NextAction)
	return nil
}

// actionable are the states where the author has something to do, all of them reached
// from READY by someone else acting on the changeset.
func actionable(s model.State) bool {
	switch s {
	case model.StateBlocked, model.StateFeedback, model.StateApproved:
		return true
	}
	return false
}

// stateName is the contract spelling for an agent: the state names `git pair status --json`
// already prints, so a comparison between the two commands is a comparison of literals.
func stateName(s model.State) string {
	return string(s)
}

func waitNextAction(s model.State, ref string) string {
	if s == "" {
		return "nothing has changed yet; run `git pair status --json` to see where things stand"
	}
	local := ref == "" || ref == "HEAD"
	switch s {
	case model.StateBlocked:
		if !local {
			return fmt.Sprintf("the review landed on %s; bring it into this branch with ordinary "+
				"Git, then `git pair change feedback`", ref)
		}
		return "`git pair change feedback`, address it, then `git pair change ready`"
	case model.StateFeedback:
		if !local {
			return fmt.Sprintf("the review landed on %s; bring it into this branch with ordinary "+
				"Git, then `git pair change feedback`", ref)
		}
		return "`git pair change feedback`; feedback is non-blocking, `git pair change archive` when integration is due"
	case model.StateApproved:
		return "`git pair change archive` before squash/merge"
	case model.StateReady, model.StateWorking:
		// Only reachable on a timeout: nothing became actionable.
		return "still waiting for review activity; run `git pair change wait` again or check `git pair status --json`"
	}
	return ""
}

func fetchNote(remotes []string) string {
	if len(remotes) == 0 {
		return ""
	}
	return ", fetching " + strings.Join(remotes, ", ")
}

func parseWaitDuration(value, flag string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, &usageError{fmt.Errorf("%s expects a duration like 30s or 2m, got %q", flag, value)}
	}
	return d, nil
}

// fetchTargets picks the remotes to fetch: the branch's own upstream remote when it has
// one, otherwise origin, otherwise every configured remote.
func fetchTargets(ctx context.Context, repo *git.Repo) ([]string, error) {
	remotes, err := repo.Remotes(ctx)
	if err != nil {
		return nil, err
	}
	if len(remotes) == 0 {
		return nil, nil
	}
	branch, err := repo.CurrentBranch(ctx)
	if err == nil {
		if up, err := repo.Upstream(ctx, branch); err == nil {
			if remote, _, found := strings.Cut(up, "/"); found {
				for _, r := range remotes {
					if r == remote {
						return []string{remote}, nil
					}
				}
			}
		}
	}
	for _, r := range remotes {
		if r == "origin" {
			return []string{"origin"}, nil
		}
	}
	return remotes, nil
}
