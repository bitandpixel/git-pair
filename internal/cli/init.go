package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"gitpair/internal/changeset"
	"gitpair/internal/console"
	"gitpair/internal/git"
	"gitpair/internal/marker"
	"gitpair/internal/reviewref"
)

// --- init -------------------------------------------------------------------

// `init` is a top-level command rather than `change init`: it is the first command of the author's
// loop, it is what a fresh agent runs first, and the name says what happens. One spelling is the rule,
// and the group it used to sit in keeps the commands that act on a changeset already in progress
// (plan P3).

type initOptions struct {
	base     string
	setBase  bool
	id       string
	about    string
	setAbout bool
	noCommit bool
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
