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
	"gitpr/internal/git"
	"gitpr/internal/marker"
	"gitpr/internal/model"
	"gitpr/internal/survival"
)

func newChangeCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "change",
		Short: "Author-side commands",
	}
	cmd.AddCommand(newChangeInitCommand(a), newChangeReadyCommand(a))
	return cmd
}

// --- change init ------------------------------------------------------------

type initOptions struct {
	base     string
	setBase  bool
	commit   bool
	baseFlag bool
}

func newChangeInitCommand(a *app) *cobra.Command {
	opts := &initOptions{}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create review scaffolding for the current branch",
		Long: `Create changesets/<changeset>/ with CHANGESET.yaml and ABOUT.md.

The changeset directory name is derived from the branch name, so
feature/booking-transaction becomes changesets/feature-booking-transaction/.

The command is idempotent and never overwrites existing changeset content. It
does not commit: scaffolding normally lands in the author's first implementation
commit. Use --commit to make a dedicated commit instead.`,
		Example: `  gitpr change init --base main
  gitpr change init --base booking-transaction   # stacked branch`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.baseFlag = cmd.Flags().Changed("base")
			return runChangeInit(cmd.Context(), a, opts)
		},
	}
	cmd.Flags().StringVar(&opts.base, "base", "", "ref this changeset is stacked on (default: main, then master)")
	cmd.Flags().BoolVar(&opts.setBase, "set-base", false, "overwrite an existing base value")
	cmd.Flags().BoolVar(&opts.commit, "commit", false, "commit the scaffolding instead of leaving it staged for the author")
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

	written, err := changeset.Write(repo, cs, base, opts.setBase)
	if err != nil {
		if errors.Is(err, changeset.ErrBaseConflict) {
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

	if opts.commit {
		if err := repo.StagePaths(ctx, cs.Dir); err != nil {
			return err
		}
		sha, err := marker.Commit(ctx, repo, marker.Message{
			Subject:  fmt.Sprintf("gitpr: initialize changeset %s", cs.Slug),
			Trailers: []string{"GitPR-Changeset=" + cs.Slug},
		})
		if err != nil {
			if isNothingToCommit(err) {
				a.printf("nothing to commit (scaffolding is already tracked)\n")
				return nil
			}
			return err
		}
		a.printf("committed %s\n", short(sha))
		return nil
	}

	if cs.Exists && len(written) > 0 {
		a.printf("\nNext: describe the change in %s, then commit it with your implementation.\n", cs.AboutPath())
	} else if !cs.Exists {
		a.printf("\nNext: fill in %s, commit it with your implementation, then run `gitpr change ready`.\n", cs.AboutPath())
	}
	return nil
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
		return &usageError{fmt.Errorf("working tree must be clean before marking %s ready; commit or stash your changes first", s.cs.Slug)}
	}
	if !s.cs.AboutExists(s.repo) {
		return &usageError{fmt.Errorf("%s is missing; describe the change before marking it ready", s.cs.AboutPath())}
	}
	if s.head == "" {
		return &usageError{fmt.Errorf("nothing to mark ready: the repository has no commits yet")}
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
	template := strings.Split(strings.TrimPrefix(changeset.AboutTemplate(cs.Slug), "# "+cs.Slug+"\n"), "\n")
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
		return strings.Contains(strings.ToLower(ge.Stderr), "nothing to commit")
	}
	return false
}
