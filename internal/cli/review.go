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

	"gitpair/internal/changeset"
	"gitpair/internal/console"
	"gitpair/internal/git"
	"gitpair/internal/lifecycle"
	"gitpair/internal/model"
	"gitpair/internal/reviewops"
	"gitpair/internal/reviewref"
	"gitpair/internal/span"
	"gitpair/internal/tui"
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
  git pair review submit --block | --feedback | --approve

A span whose head is a commit rather than your working tree is a look at history.
The screen opens read-only: no marking, no editing, no submission, and the shortcut
bar says so. v returns you to a span you can review.

Both ends can be named from here: --base-review, --base-commit and --base-ref choose where
the span starts, --head-review, --head-commit and --head-ref where it ends. Inside the
screen, v walks the spans this session has been in and V picks one out.

A ref endpoint is pinned when the span is created. If the ref moves while you review, the
screen keeps the pinned span and offers r to re-pin it.`,
		Example: `  git pair review open
  git pair review open --unreviewed
  git pair review open --since-review=-2
  git pair review open --since-review=-3 --head-review=-1
  git pair review open --base-ref=main --head-commit=abc1234`,
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

Edits made here and committed by ` + "`git pair review submit`" + ` are high-level review
feedback on the whole changeset. The file is the canonical description of the
change and the reviewer edits it directly rather than using comment syntax.`,
		Example: `  git pair review about`,
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
		Example: `  git pair review thread "concurrency tests"
  git pair review thread            # prompts, when attached to a terminal`,
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

The commit carries Review-Outcome and Review-Changeset trailers, and
refs/reviews/<changeset> is moved to the resulting HEAD in the same operation so
the full unsquashed chain stays reachable.

Source edits, inline comments, ABOUT.md edits, and thread files all become part
of the review; a review commit with no changes at all is valid, which is what
makes a clean-tree approval work.`,
		Example: `  git pair review submit --block
  git pair review submit --feedback -m "Non-blocking: naming only"
  git pair review submit --approve`,
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
	result, err := reviewops.Submit(ctx, s.repo, s.cs, outcome, opts.message, !opts.noStage)
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
		return "author: `git pair change feedback`, address it, then `git pair change ready`"
	case model.OutcomeFeedback:
		return "author: `git pair change feedback` to read it; feedback is non-blocking, " +
			"`git pair change complete` when integration is due"
	case model.OutcomeApprove:
		return "author: `git pair change complete` before squash/merge"
	}
	return ""
}

// --- review history ---------------------------------------------------------

func newReviewHistoryCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "history",
		Short: "List review submissions for the current changeset",
		Long: `List every review submission in chronological order.

Only commits carrying a valid Review-Outcome trailer count; ordinary commits do
not appear. Indexes are chronological, so 0 is the first review and -1 is the
most recent, matching ` + "`git pair diff --since-review`" + `.`,
		Example: `  git pair review history
  git pair review history --json
  git pair diff --since-review=-1`,
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
		Long: `List every changeset in this repository whose branch is READY.

Readiness comes from commit history, not a queue file: a changeset is listed
while its branch carries a ready marker that no review submission has answered.
Entries are ordered longest-waiting first.

Branches are read from the repository, not from the checked-out directory, so the
queue says the same thing on main as it does on the changeset's own branch. A
changeset whose content has landed in its base is not listed, and says nothing.

--json is the stable contract for notifications, dashboards, and agent
supervisors.`,
		Example: `  git pair review queue
  git pair review queue --json`,
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
	// Branches, not directories. Only a branch can be reviewed, so only a branch
	// can be queued; a changeset directory whose branch is gone is a record rather
	// than work, and the record gets one honest line instead of a warning per slug.
	refs, err := repo.ForEachRef(ctx, "refs/heads")
	if err != nil {
		return err
	}
	type found struct {
		cs       changeset.Changeset
		branches []string
	}
	var order []string
	sets := map[string]*found{}
	var skipped []string
	for _, ref := range refs {
		branch := strings.TrimPrefix(ref.Name, "refs/heads/")
		cs, err := changeset.AtCommit(ctx, repo, branch)
		if err != nil {
			skipped = append(skipped, branch+" (unreadable changeset metadata: "+err.Error()+")")
			continue
		}
		if !cs.Exists {
			continue
		}
		f := sets[cs.Slug]
		if f == nil {
			f = &found{cs: cs}
			sets[cs.Slug] = f
			order = append(order, cs.Slug)
		}
		f.branches = append(f.branches, branch)
	}

	var entries []queueEntry
	for _, slug := range order {
		f := sets[slug]
		// A slug can match more than one branch; the one whose head carries the
		// newest ready marker wins.
		best, _, err := readyEntry(ctx, repo, f.cs, f.branches)
		if err != nil {
			skipped = append(skipped, slug+" ("+err.Error()+")")
			continue
		}
		if best != nil {
			entries = append(entries, *best)
		}
	}

	// Directories no branch accounts for. Read from HEAD's tree, not the working
	// tree, so the answer does not depend on what happens to be checked out.
	head, err := repo.Head(ctx)
	if err != nil && !git.IsUnknownRevision(err) {
		return err
	}
	dirs, err := changeset.DirsAt(ctx, repo, head)
	if err != nil {
		return err
	}
	for _, slug := range dirs {
		if _, ok := sets[slug]; ok {
			continue
		}
		note, err := classifyOrphan(ctx, repo, head, slug)
		if err != nil {
			skipped = append(skipped, slug+" ("+err.Error()+")")
			continue
		}
		if note != "" {
			skipped = append(skipped, note)
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

// classifyOrphan decides what to say about a changeset directory with no branch
// behind it, and returns "" when the honest answer is nothing. Every one of them
// used to print `note: skipped <slug> (no branch matches this changeset directory)`,
// which gave the same warning to two opposite situations: work that was reviewed,
// merged, and had its branch deleted — nothing left for a reviewer to do — and work
// whose branch really did go missing.
func classifyOrphan(ctx context.Context, repo *git.Repo, head, slug string) (string, error) {
	anchor, err := reviewref.Resolve(ctx, repo, slug)
	if errors.Is(err, reviewref.ErrNoReviewRef) {
		// Never anchored means never offered: the directory is a leftover, and the
		// queue has nothing to offer either.
		return "", nil
	}
	if err != nil {
		return "", err
	}
	base, err := changeset.BaseAt(ctx, repo, head, slug)
	if err != nil {
		if errors.Is(err, git.ErrUnknownPath) {
			return "", nil
		}
		return "", err
	}
	if base == "" {
		return "", nil
	}
	// The anchor is a commit that survives the branch, so it can be compared with
	// the base without touching the working tree: identical changeset content on
	// both sides is what a merge leaves behind.
	dir := filepath.Join(changeset.Root, slug)
	changed, err := repo.PathsChanged(ctx, base, anchor, dir)
	if err != nil {
		return "", err
	}
	if len(changed) == 0 {
		return "", nil
	}
	return fmt.Sprintf("%s (anchored at %s, whose %s is not in %s and no branch carries it)",
		slug, short(anchor), dir, base), nil
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

The same span as ` + "`git pair review open --unreviewed`" + `, under a name that says why you are
back: after the author answers a block or feedback, the work to read is what came after
your submission, not the whole changeset a second time. ` + "`review open`" + ` keeps showing the
whole changeset by default. For an earlier review use ` + "`review open --since-review=N`" + `.

Needs a terminal; the author reads the submission itself with ` + "`git pair change feedback`" + `.`,
		Example: `  git pair review reopen`,
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
			return &usageError{fmt.Errorf("%w; run `git pair review open` for the whole changeset", err)}
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
			"`git pair review %s` needs a terminal; use `git pair diff`, `git pair review about`, "+
				"`git pair review thread`, and `git pair review submit` instead", name)}
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
