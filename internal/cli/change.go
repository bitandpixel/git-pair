package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"gitpr/internal/changeset"
	"gitpr/internal/console"
	"gitpr/internal/git"
	"gitpr/internal/marker"
	"gitpr/internal/model"
	"gitpr/internal/survival"
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
	cmd.AddCommand(newChangeInitCommand(a), newChangeReadyCommand(a))
	return cmd
}

// --- change init ------------------------------------------------------------

type initOptions struct {
	base     string
	setBase  bool
	about    string
	setAbout bool
	noCommit bool
}

func newChangeInitCommand(a *app) *cobra.Command {
	opts := &initOptions{}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create review scaffolding for the current branch",
		Long: `Create changesets/<changeset>/ with CHANGESET.yaml and ABOUT.md, then commit it.

The changeset directory name is derived from the branch name, so
feature/booking-transaction becomes changesets/feature-booking-transaction/.

The commit covers the changeset directory only, so whatever else is staged on
your index stays there. Use --no-commit to leave the scaffolding in the working
tree for your first implementation commit instead.

ABOUT.md gets a scaffold with the standard review headings unless you supply
content, which makes describe-and-initialise a single non-interactive call:

  gitpr change init --base main --about "$DESCRIPTION"
  gitpr change init --base main --about - < about.md
  cat about.md | gitpr change init --base main

Existing content is never overwritten silently: replacing a populated ABOUT.md
takes --set-about, the same way changing a base takes --set-base.`,
		Example: `  gitpr change init --base main
  gitpr change init --base booking-transaction   # stacked branch
  gitpr change init --base main --about - < draft.md`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChangeInit(cmd.Context(), a, opts)
		},
	}
	cmd.Flags().StringVar(&opts.base, "base", "", "ref this changeset is stacked on (default: main, then master)")
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
		return &usageError{fmt.Errorf("%w: run `git switch -c <branch>` before `gitpr change init`", changeset.ErrDetachedHead)}
	}
	cs, err := changeset.ForBranch(repo, branch)
	if err != nil {
		return &usageError{err}
	}

	base := opts.base
	if base == "" {
		if base, err = defaultBase(ctx, repo); err != nil {
			return &usageError{err}
		}
		a.warn("base: %s (pass --base to choose a different ref)\n", base)
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
		// Both conflicts are "that would discard content you did not say to
		// discard", which is an argument problem rather than a repository state.
		if errors.Is(err, changeset.ErrBaseConflict) || errors.Is(err, changeset.ErrAboutConflict) {
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
		Subject:  fmt.Sprintf("gitpr: initialize changeset %s", cs.Slug),
		Trailers: []string{"GitPR-Changeset=" + cs.Slug},
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
		a.printf("\nNext: implement, commit, then run `gitpr change ready`.\n")
		return
	}
	a.printf("\nNext: fill in %s, commit it with your implementation, then run `gitpr change ready`.\n", cs.AboutPath())
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// defaultBase picks the repository's trunk without guessing wildly.
func defaultBase(ctx context.Context, repo *git.Repo) (string, error) {
	for _, candidate := range []string{"main", "master"} {
		if _, err := repo.RevParse(ctx, "refs/heads/"+candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("cannot infer a base: no main or master branch exists; pass --base <ref>")
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

Any later implementation commit makes this marker stale and returns the
changeset to WORKING.`,
		Example: `  gitpr change ready
  gitpr change ready --allow-surviving-review-additions
  gitpr change ready --json`,
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

	report, err := survivalCheck(ctx, s)
	if err != nil {
		return err
	}
	if report != nil && !report.Clean() && !opts.allowSurviving {
		printSurvivalReport(a.stderr, *report,
			fmt.Sprintf("Cannot mark changeset %s ready.", s.cs.Slug),
			"gitpr change ready --allow-surviving-review-additions")
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
	a.printf("  queue: `gitpr review queue` now lists this changeset\n")
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
