package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"gitpr/internal/changeset"
	"gitpr/internal/console"
	"gitpr/internal/git"
	"gitpr/internal/lifecycle"
	"gitpr/internal/marker"
	"gitpr/internal/model"
	"gitpr/internal/reviewops"
	"gitpr/internal/reviewref"
	"gitpr/internal/span"
	"gitpr/internal/survival"
	"gitpr/internal/tui"
)

func newReviewCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Reviewer-side commands",
		RunE:  groupUsage("review"),
	}
	cmd.AddCommand(
		newReviewOpenCommand(a),
		newReviewReopenCommand(a),
		newReviewAboutCommand(a),
		newReviewThreadCommand(a),
		newReviewSubmitCommand(a),
		newReviewHistoryCommand(a),
		newReviewQueueCommand(a),
		newReviewCloseCommand(a),
	)
	return cmd
}

// --- review open ------------------------------------------------------------

func newReviewOpenCommand(a *app) *cobra.Command {
	opts := &spanOptions{}
	cmd := &cobra.Command{
		Use:   "open",
		Short: "Open the interactive review session",
		Long: `Open a small orchestration screen for the current changeset.

The TUI is not an editor and renders no diff. It lists the files in the chosen
span, tracks which ones you have looked at, and launches your editor, your
difftool, ABOUT.md, and review threads around it, releasing the terminal while
those processes run.

Press s to submit without leaving the screen, then b (block), f (feedback) or
a (approve); Esc cancels. The same outcomes are available from the CLI:
  gitpr review submit --block | --feedback | --approve

A span whose head is a commit rather than your working tree is a look at history.
The screen opens read-only: no marking, no editing, no submission, and the shortcut
bar says so. v returns you to a span you can review.

Both ends can be named from here: --base-review, --base-commit and --base-ref choose where
the span starts, --head-review, --head-commit and --head-ref where it ends. Inside the
screen, v walks the spans this session has been in and V picks one out.

A ref endpoint is pinned when the span is created. If the ref moves while you review, the
screen keeps the pinned span and offers r to re-pin it.`,
		Example: `  gitpr review open
  gitpr review open --unreviewed
  gitpr review open --since-review=-2
  gitpr review open --since-review=-3 --head-review=-1
  gitpr review open --base-ref=main --head-commit=abc1234`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReviewOpen(cmd.Context(), a, opts)
		},
	}
	opts.register(cmd)
	opts.registerHead(cmd)
	return cmd
}

func runReviewOpen(ctx context.Context, a *app, opts *spanOptions) error {
	s, err := a.load(ctx)
	if err != nil {
		return err
	}
	so, err := opts.selector()
	if err != nil {
		return err
	}
	return openSession(ctx, a, s, so, "open", "")
}

// --- review about -----------------------------------------------------------

func newReviewAboutCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "about",
		Short: "Edit the changeset's ABOUT.md",
		Long: `Open changesets/<changeset>/ABOUT.md in $VISUAL or $EDITOR.

Edits made here and committed by ` + "`gitpr review submit`" + ` are high-level review
feedback on the whole changeset. The file is the canonical description of the
change and the reviewer edits it directly rather than using comment syntax.`,
		Example: `  gitpr review about`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.load(cmd.Context())
			if err != nil {
				return err
			}
			return openFile(cmd.Context(), a, s.repo, s.cs.AboutPath(), func() error {
				return writeIfMissing(s.repo, s.cs.AboutPath(), changeset.AboutTemplate(s.cs.Slug))
			})
		},
	}
}

// --- review thread ----------------------------------------------------------

func newReviewThreadCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "thread [title...]",
		Short: "Create or open a focused review thread",
		Long: `Create or open a Markdown discussion file inside the changeset directory.

The title is slugified into the file name, so "concurrency tests" becomes
changesets/<changeset>/concurrency-tests.md. An existing file for the same title
is reopened instead of creating a duplicate.

Threads are ordinary Markdown with no required structure. Git history supplies
authorship and sequencing, so a reply is just an appended section.`,
		Example: `  gitpr review thread "concurrency tests"
  gitpr review thread            # prompts, when attached to a terminal`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReviewThread(cmd.Context(), a, args)
		},
	}
}

func runReviewThread(ctx context.Context, a *app, titleArgs []string) error {
	s, err := a.load(ctx)
	if err != nil {
		return err
	}
	title := strings.TrimSpace(strings.Join(titleArgs, " "))
	if title == "" {
		title, err = console.PromptLine("Thread title:")
		if err != nil {
			return &usageError{err}
		}
	}
	if title == "" {
		return &usageError{errors.New("a thread needs a title")}
	}
	path, err := s.cs.ThreadPath(title)
	if err != nil {
		return &usageError{err}
	}
	existed := fileExists(s.repo, path)
	if err := writeIfMissing(s.repo, path, changeset.ThreadTemplate(title)); err != nil {
		return err
	}
	if existed {
		a.printf("Opening existing thread %s\n", path)
	} else {
		a.printf("Created thread %s\n", path)
	}
	return openFile(ctx, a, s.repo, path, nil)
}

// --- review submit ----------------------------------------------------------

type submitOptions struct {
	block    bool
	feedback bool
	approve  bool
	message  string
	noStage  bool
}

func newReviewSubmitCommand(a *app) *cobra.Command {
	opts := &submitOptions{}
	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Record a review outcome as a commit",
		Long: `Turn everything in the working tree into a review submission.

Exactly one outcome is required:

  --block      changes are required before integration
  --feedback   non-blocking observations; integration is still permitted
  --approve    the reviewer accepts the current implementation

The commit carries GitPR-Outcome and GitPR-Changeset trailers, and
refs/reviews/<changeset> is moved to the resulting HEAD in the same operation so
the full unsquashed chain stays reachable.

Source edits, inline comments, ABOUT.md edits, and thread files all become part
of the review; a review commit with no changes at all is valid, which is what
makes a clean-tree approval work.`,
		Example: `  gitpr review submit --block
  gitpr review submit --feedback -m "Non-blocking: naming only"
  gitpr review submit --approve`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReviewSubmit(cmd.Context(), a, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.block, "block", false, "blocking review: changes required")
	cmd.Flags().BoolVar(&opts.feedback, "feedback", false, "non-blocking observations")
	cmd.Flags().BoolVar(&opts.approve, "approve", false, "accept the implementation")
	cmd.Flags().StringVarP(&opts.message, "message", "m", "", "additional body text for the review commit")
	cmd.Flags().BoolVar(&opts.noStage, "no-stage", false, "commit only what is already staged")
	return cmd
}

func (o submitOptions) outcome() (model.Outcome, error) {
	var chosen []model.Outcome
	if o.block {
		chosen = append(chosen, model.OutcomeBlock)
	}
	if o.feedback {
		chosen = append(chosen, model.OutcomeFeedback)
	}
	if o.approve {
		chosen = append(chosen, model.OutcomeApprove)
	}
	switch len(chosen) {
	case 1:
		return chosen[0], nil
	case 0:
		return "", &usageError{errors.New("choose exactly one of --block, --feedback, --approve")}
	default:
		return "", &usageError{errors.New("--block, --feedback and --approve are mutually exclusive")}
	}
}

func runReviewSubmit(ctx context.Context, a *app, opts *submitOptions) error {
	outcome, err := opts.outcome()
	if err != nil {
		return err
	}
	s, err := a.load(ctx)
	if err != nil {
		return err
	}
	result, err := reviewops.Submit(ctx, s.repo, s.cs, s.summary, outcome, opts.message, !opts.noStage)
	if err != nil {
		return err
	}

	// A second submission supersedes the first rather than undoing it: the newest
	// review decides the state and the earlier one stays in history. Surfaced here
	// because this is the moment a false start gets corrected.
	previous := ""
	if s.summary.LatestReview != nil {
		previous = s.summary.LatestReview.SHA
	}
	if a.json {
		return a.emitJSON(map[string]any{
			"changeset":       s.cs.Slug,
			"outcome":         string(result.Outcome),
			"commit":          result.Commit,
			"short":           short(result.Commit),
			"review_ref":      result.Ref,
			"files":           result.Files,
			"empty":           result.Empty(),
			"previous_review": previous,
			"next_action":     nextActionFor(result.Outcome),
		})
	}
	a.printf("Review submitted: %s\n", s.cs.Slug)
	a.printf("  outcome: %s\n", result.Outcome)
	a.printf("  commit:  %s\n", short(result.Commit))
	if result.Empty() {
		a.printf("  files:   none (recorded as an empty review commit)\n")
	} else {
		a.printf("  files:   %d changed\n", len(result.Files))
		for _, f := range result.Files {
			a.printf("           %s\n", f)
		}
	}
	a.printf("  ref:     %s -> %s\n", result.Ref, short(result.Commit))
	if s.summary.LatestReview != nil {
		a.printf("  supersedes: %s (the newest submission decides the state)\n",
			reviewLabel(s.summary.LatestReview))
	}
	a.printf("  next:    %s\n", nextActionFor(result.Outcome))
	if clean, err := s.repo.IsClean(ctx); err == nil && !clean {
		a.warn("\nwarning: the working tree is still dirty; those changes are not part of this review\n")
	}
	return nil
}

func nextActionFor(o model.Outcome) string {
	switch o {
	case model.OutcomeBlock:
		return "author: `gitpr change feedback`, address it, then `gitpr change ready`"
	case model.OutcomeFeedback:
		return "author: `gitpr change feedback` to read it; feedback is non-blocking, `gitpr review close` when integration is due"
	case model.OutcomeApprove:
		return "`gitpr review close` before squash/merge"
	}
	return ""
}

// --- review history ---------------------------------------------------------

func newReviewHistoryCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "history",
		Short: "List review submissions for the current changeset",
		Long: `List every review submission in chronological order.

Only commits carrying a valid GitPR-Outcome trailer count; ordinary commits do
not appear. Indexes are chronological, so 0 is the first review and -1 is the
most recent, matching ` + "`gitpr diff --since-review`" + `.`,
		Example: `  gitpr review history
  gitpr review history --json
  gitpr diff --since-review=-1`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.load(cmd.Context())
			if err != nil {
				return err
			}
			if len(s.summary.Reviews) == 0 {
				if a.json {
					return a.emitJSON(map[string]any{"changeset": s.cs.Slug, "reviews": []any{}})
				}
				a.printf("No review submissions for %s yet.\n", s.cs.Slug)
				return nil
			}
			if a.json {
				var out []map[string]any
				for i, r := range s.summary.Reviews {
					out = append(out, map[string]any{
						"index":   i,
						"sha":     r.SHA,
						"short":   r.Short,
						"outcome": string(r.Outcome),
						"subject": r.Subject,
						"author":  r.Author,
						"when":    r.When.UTC().Format(time.RFC3339),
						"age":     lifecycle.Age(r.When, now()),
					})
				}
				return a.emitJSON(map[string]any{"changeset": s.cs.Slug, "reviews": out})
			}
			w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "INDEX\tSHA\tOUTCOME\tAGE\tSUBJECT")
			for i, r := range s.summary.Reviews {
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
					i, r.Short, r.Outcome, lifecycle.Age(r.When, now()), r.Subject)
			}
			return w.Flush()
		},
	}
}

// --- review queue -----------------------------------------------------------

type queueEntry struct {
	Changeset   string `json:"changeset"`
	Branch      string `json:"branch"`
	Base        string `json:"base"`
	State       string `json:"state"`
	Head        string `json:"head"`
	ReadyCommit string `json:"ready_commit"`
	ReadyAge    string `json:"ready_age"`
	ReviewRef   string `json:"review_ref"`
}

func newReviewQueueCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "queue",
		Short: "List changesets ready for human review",
		Long: `List every changeset in this repository whose derived state is READY.

Readiness comes from commit history, not a queue file: a changeset is listed
while its branch head is a ready marker with no implementation commit after it.
Entries are ordered longest-waiting first.

--json is the stable contract for notifications, dashboards, and agent
supervisors.`,
		Example: `  gitpr review queue
  gitpr review queue --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReviewQueue(cmd.Context(), a)
		},
	}
}

func runReviewQueue(ctx context.Context, a *app) error {
	repo, err := a.loadRepo(ctx)
	if err != nil {
		return err
	}
	sets, err := changeset.List(repo)
	if err != nil {
		return err
	}
	var entries []queueEntry
	var skipped []string
	for _, cs := range sets {
		branches, err := changeset.BranchesForSlug(ctx, repo, cs.Slug)
		if err != nil {
			return err
		}
		if len(branches) == 0 {
			skipped = append(skipped, cs.Slug+" (no branch matches this changeset directory)")
			continue
		}
		// A slug can match more than one branch; the one whose head carries the
		// newest ready marker wins.
		best, _, err := readyEntry(ctx, repo, cs, branches)
		if err != nil {
			skipped = append(skipped, cs.Slug+" ("+err.Error()+")")
			continue
		}
		if best != nil {
			entries = append(entries, *best)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return ageLess(entries[i].ReadyAge, entries[j].ReadyAge)
	})

	if a.json {
		return a.emitJSON(map[string]any{
			"ready_for_review": entries,
			"skipped":          skipped,
		})
	}
	if len(entries) == 0 {
		a.printf("READY FOR REVIEW\n\n  nothing is ready\n")
		printSkipped(a, skipped)
		return nil
	}
	a.printf("READY FOR REVIEW\n\n")
	for _, e := range entries {
		a.printf("%s\n", e.Changeset)
		a.printf("  base: %s\n", e.Base)
		a.printf("  ready: %s ago\n", e.ReadyAge)
		a.printf("  head: %s\n", short(e.Head))
		a.printf("\n")
	}
	printSkipped(a, skipped)
	return nil
}

func printSkipped(a *app, skipped []string) {
	for _, s := range skipped {
		a.warn("note: skipped %s\n", s)
	}
}

// readyEntry returns the queue row for cs if one of its branches is READY.
func readyEntry(ctx context.Context, repo *git.Repo, cs changeset.Changeset, branches []string) (*queueEntry, time.Time, error) {
	var entry *queueEntry
	var at time.Time
	var firstErr error
	for _, branch := range branches {
		summary, err := lifecycle.Summarize(ctx, repo, cs.Slug, cs.Base, branch)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if summary.State != model.StateReady || summary.Marker == nil {
			continue
		}
		head, err := repo.RevParse(ctx, branch)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if entry == nil || summary.Marker.When.After(at) {
			at = summary.Marker.When
			entry = &queueEntry{
				Changeset:   cs.Slug,
				Branch:      branch,
				Base:        cs.Base,
				State:       string(summary.State),
				Head:        head,
				ReadyCommit: summary.Marker.SHA,
				ReadyAge:    lifecycle.Age(at, now()),
				ReviewRef:   reviewref.Head(cs.Slug),
			}
		}
	}
	if entry == nil && firstErr != nil {
		return nil, time.Time{}, firstErr
	}
	return entry, at, nil
}

// ageLess orders "18m" before "1h" before "2d" so the longest wait comes first.
func ageLess(a, b string) bool {
	return ageSeconds(a) > ageSeconds(b)
}

func ageSeconds(age string) int64 {
	if age == "" {
		return 0
	}
	var n int64
	unit := age[len(age)-1]
	fmt.Sscanf(age[:len(age)-1], "%d", &n)
	switch unit {
	case 's':
		return n
	case 'm':
		return n * 60
	case 'h':
		return n * 3600
	case 'd':
		return n * 86400
	}
	return 0
}

// --- review close -----------------------------------------------------------

type closeOptions struct {
	allowSurviving bool
}

func newReviewCloseCommand(a *app) *cobra.Command {
	opts := &closeOptions{}
	cmd := &cobra.Command{
		Use:   "close",
		Short: "Finalise the review lifecycle before squash/merge",
		Long: `Archive the complete unsquashed history and mark the changeset closed.

Checks, in order: the working tree is clean, the latest effective outcome permits
integration (approve or feedback), and no non-blank addition from the most recent
review survives unchanged. Then the whole chain is anchored under
refs/reviews/, an immutable archive ref is written, and a close marker is
committed.

close and approve are different things: approve is a human judgement, close is
the archival operation. This command never merges, pushes, or squashes — it
prints what is safe to do next.`,
		Example: `  gitpr review close
  gitpr review close --allow-surviving-review-additions`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReviewClose(cmd.Context(), a, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.allowSurviving, "allow-surviving-review-additions", false,
		"acknowledge surviving review additions and close anyway")
	return cmd
}

func runReviewClose(ctx context.Context, a *app, opts *closeOptions) error {
	s, err := a.load(ctx)
	if err != nil {
		return err
	}
	if !s.clean {
		return fmt.Errorf("working tree must be clean before closing %s", s.cs.Slug)
	}
	switch s.summary.State {
	case model.StateApproved, model.StateFeedback:
		// Integration is permitted.
	case model.StateClosed:
		return fmt.Errorf("changeset %s is already closed", s.cs.Slug)
	default:
		return fmt.Errorf("cannot close %s: latest outcome is %s (%s); integration needs approve or feedback",
			s.cs.Slug, s.summary.State, s.summary.Reason)
	}

	report, err := survivalCheck(ctx, s)
	if err != nil {
		return err
	}
	if report != nil && !report.Clean() && !opts.allowSurviving {
		printSurvivalReport(a.stderr, *report,
			fmt.Sprintf("Cannot close changeset %s.", s.cs.Slug),
			"gitpr review close --allow-surviving-review-additions")
		printArtifactSurvivals(a.stderr, *report)
		return fmt.Errorf("cannot close %s: %d review addition(s) from %s still survive unchanged",
			s.cs.Slug, len(report.Code), report.ReviewShort)
	}

	// Anchor the current chain, archive it immutably, then record the close
	// marker naming that archive ref. The movable ref ends on the close commit so
	// the marker itself stays reachable.
	head, err := s.repo.Head(ctx)
	if err != nil {
		return err
	}
	if _, err := reviewref.Update(ctx, s.repo, s.cs.Slug, head); err != nil {
		return err
	}
	archiveRef, created, err := reviewref.ArchiveCommit(ctx, s.repo, s.cs.Slug, head)
	if err != nil {
		return err
	}
	sha, err := marker.Commit(ctx, s.repo, marker.CloseMessage(s.cs.Slug, archiveRef))
	if err != nil {
		return err
	}
	if _, err := reviewref.Update(ctx, s.repo, s.cs.Slug, sha); err != nil {
		return err
	}

	if a.json {
		return a.emitJSON(map[string]any{
			"changeset":           s.cs.Slug,
			"state":               string(model.StateClosed),
			"commit":              sha,
			"review_ref":          reviewref.Head(s.cs.Slug),
			"archive_ref":         archiveRef,
			"archive_created":     created,
			"squash_safe":         true,
			"acknowledged":        report != nil && !report.Clean(),
			"surviving_additions": survivingCodeCount(report),
		})
	}
	if report != nil && !report.Clean() {
		a.printf("Acknowledged %d surviving review addition(s) from review %s.\n\n",
			len(report.Code), report.ReviewShort)
	}
	a.printf("Closed changeset %s\n\n", s.cs.Slug)
	a.printf("Review archive:\n  %s\n\n", archiveRef)
	a.printf("Safe to squash/merge.\n")
	a.printf("Review history stays reachable at %s\n", reviewref.Head(s.cs.Slug))
	return nil
}

func survivingCodeCount(r *survival.Report) int {
	if r == nil {
		return 0
	}
	return len(r.Code)
}

// --- shared file helpers ----------------------------------------------------

// openFile launches the editor, optionally creating the file first.
func openFile(ctx context.Context, a *app, repo *git.Repo, relPath string, ensure func() error) error {
	if ensure != nil {
		if err := ensure(); err != nil {
			return err
		}
	}
	abs := filepath.Join(repo.Dir, relPath)
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("cannot open %s: %w", relPath, err)
	}
	// Refuse rather than hang: an agent running this with no terminal would
	// otherwise block forever waiting on an editor that cannot draw.
	if !console.Interactive() {
		return &usageError{fmt.Errorf(
			"opening an editor needs a terminal; edit %s directly — it is an ordinary file in the working tree",
			relPath)}
	}
	cmd, err := console.EditorCommand(ctx, repo, abs)
	if err != nil {
		return err
	}
	a.printf("Opening %s\n", relPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor exited with an error: %w", err)
	}
	return nil
}

func writeIfMissing(repo *git.Repo, relPath, content string) error {
	abs := filepath.Join(repo.Dir, relPath)
	if _, err := os.Stat(abs); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, []byte(content), 0o644)
}

func fileExists(repo *git.Repo, relPath string) bool {
	info, err := os.Stat(filepath.Join(repo.Dir, relPath))
	return err == nil && !info.IsDir()
}

func newReviewReopenCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reopen",
		Short: "Reopen the review session on what changed since your last review",
		Long: `Open the review session on the span since the most recent review submission.

The same span as ` + "`gitpr review open --unreviewed`" + `, under a name that says why you are
back: after the author answers a block or feedback, the work to read is what came after
your submission, not the whole changeset a second time. ` + "`review open`" + ` keeps showing the
whole changeset by default. For an earlier review use ` + "`review open --since-review=N`" + `.

Needs a terminal; the author reads the submission itself with ` + "`gitpr change feedback`" + `.`,
		Example: `  gitpr review reopen`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReviewReopen(cmd.Context(), a)
		},
	}
	return cmd
}

func runReviewReopen(ctx context.Context, a *app) error {
	s, err := a.load(ctx)
	if err != nil {
		return err
	}
	// Reopening continues the review, so the span ends at the working tree: a span
	// that ends at a commit is a look at history, and you cannot resume a review from
	// something you are not allowed to mark, edit, or submit. What the reviewer has
	// typed but not committed still shows up, in the preview's `you` section.
	sel := span.SinceReview(-1)
	sp, err := span.Resolve(ctx, s.repo, s.cs.Base, s.summary, sel)
	if err != nil {
		if errors.Is(err, span.ErrNoReviews) {
			return &usageError{fmt.Errorf("%w; run `gitpr review open` for the whole changeset", err)}
		}
		return &usageError{err}
	}
	note := ""
	if names, err := s.repo.DiffNames(ctx, sp.From, sp.To); err != nil {
		return err
	} else if len(names) == 0 {
		note = fmt.Sprintf("nothing has landed since %s", reviewLabel(s.summary.LatestReview))
	}
	return openSession(ctx, a, s, sel, "reopen", note)
}

// openSession runs the TUI on a chosen span. name is the subcommand to
// blame in the no-terminal message.
// note, if set, is written to stderr before the session takes the screen.
func openSession(ctx context.Context, a *app, s *session, sel span.Selector, name, note string) error {
	if !console.Interactive() {
		return &usageError{fmt.Errorf(
			"`gitpr review %s` needs a terminal; use `gitpr diff`, `gitpr review about`, "+
				"`gitpr review thread`, and `gitpr review submit` instead", name)}
	}
	if note != "" {
		a.warn("%s\n", note)
	}
	err := tui.Run(ctx, tui.Options{Repo: s.repo, Changeset: s.cs, Summary: s.summary, Span: sel})
	if errors.Is(err, tui.ErrQuit) {
		return nil
	}
	return err
}

// reviewLabel names a review submission the way `review history` does.
func reviewLabel(r *lifecycle.Event) string {
	if r == nil {
		return "your last review"
	}
	if r.Outcome == "" {
		return fmt.Sprintf("review %s", r.Short)
	}
	return fmt.Sprintf("review %s (%s)", r.Short, r.Outcome)
}
