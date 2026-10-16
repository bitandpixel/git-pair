package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"gitpair/internal/changeset"
	"gitpair/internal/console"
	"gitpair/internal/git"
	"gitpair/internal/hooks"
	"gitpair/internal/lifecycle"
	"gitpair/internal/model"
	"gitpair/internal/reviewops"
	"gitpair/internal/span"
	"gitpair/internal/tui"
)

func newReviewCommand(a *app) *cobra.Command {
	opts := &spanOptions{}
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Reviewer-side commands (no subcommand opens the review screen)",
		Long: `Reviewer-side commands, and the review screen itself.

With no subcommand, ` + "`git pair review`" + ` is ` + "`git pair review open`" + `: it takes the terminal
and opens the review session for the current changeset. Every flag below belongs to that screen, so
` + "`git pair review --unreviewed`" + ` and ` + "`git pair review open --unreviewed`" + ` are the same
command spelled two ways.`,
		Example: `  git pair review
  git pair review --unreviewed
  git pair review --base-ref=main --head-commit=abc1234`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The group's own job is the screen, so a word that is not one of its commands is a
			// mistake rather than an argument to it.
			if len(args) > 0 {
				return unknownGroupCommand("review", args)
			}
			return runReviewOpen(cmd.Context(), a, opts, "git pair review")
		},
	}
	opts.register(cmd)
	opts.registerHead(cmd)
	noHooksFlag(cmd, a)
	trustHooksOnceFlag(cmd, a)
	cmd.AddCommand(
		newReviewOpenCommand(a),
		newReviewReopenCommand(a),
		newReviewAboutCommand(a),
		newReviewThreadCommand(a),
		newReviewSubmitCommand(a),
		newReviewHistoryCommand(a),
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

This is what ` + "`git pair review`" + ` does with no subcommand, and it takes the same flags; the name
is here for the readers who look for a verb.

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
			return runReviewOpen(cmd.Context(), a, opts, "git pair review open")
		},
	}
	opts.register(cmd)
	opts.registerHead(cmd)
	noHooksFlag(cmd, a)
	trustHooksOnceFlag(cmd, a)
	return cmd
}

// runReviewOpen opens the session on the span the flags named. invocation is the command as the
// reviewer can type it -- "git pair review open", or "git pair review" for the bare group -- so the
// no-terminal message quotes the one they actually ran.
func runReviewOpen(ctx context.Context, a *app, opts *spanOptions, invocation string) error {
	s, err := a.load(ctx)
	if err != nil {
		return err
	}
	so, err := opts.selector()
	if err != nil {
		return err
	}
	return openSession(ctx, a, s, so, invocation, "")
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
				_, err := s.cs.EnsureAbout(s.repo)
				return err
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
	// allowHookFailure records the submission anyway when a `pre-submit` hook refuses it. It exists because
	// a reviewer's own script is the client's, not the repository's: a hook that misbehaves on the
	// reviewer's machine must not strand a verdict they have decided to record. `check` has no such flag,
	// and the difference is who is standing at the command.
	allowHookFailure bool
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

The commit carries Review-Outcome, Review-Changeset and Review-Head trailers, and it is the whole
submission: no ref is written, because while work is in flight the branch is what holds
the chain. ` + "`git pair integration record`" + ` writes the durable refs, once, at landing.

Review-Head is the commit being reviewed — ` + "`HEAD`" + ` as the submission was made, which is the new
commit's own parent. It is written down because a rebase rewrites the review commit and keeps its
message: the marker that survives says which commit it approved, and ` + "`git pair check`" + ` refuses to
read that approval as covering the rewritten history. Merging the base in rewrites nothing and costs
nothing.

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
	cmd.Flags().BoolVar(&opts.allowHookFailure, "allow-hook-failure", false,
		"record the submission even if a pre-submit hook refuses it")
	noHooksFlag(cmd, a)
	trustHooksOnceFlag(cmd, a)
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

// submitReview is the one place a review submission is made. `review submit` and the review screen's
// approve/feedback/block keys both reach it, and that is the point: a reviewer's own checks have to fire once
// wherever a submission is made, and the screen calling `reviewops.Submit` on its own was exactly the second
// call site where one of them would stop firing without anyone noticing.
//
// The order is stage, hooks, commit. A `pre-submit` hook exists to read what the review commit is about to
// carry — the source edits made in the screen, the thread files, the ABOUT.md edit — and those are only in
// the index once staging has happened. Running the hook first would have it inspect the tree before the
// reviewer's edits were part of the submission.
//
// `reviewops.Submit` is then told not to stage: the hooks may have edited the index in between, and
// re-staging after them would quietly discard what a reviewer's own script decided to leave staged.
func (a *app) submitReview(ctx context.Context, s *session, outcome model.Outcome, body string,
	stageAll, allowHookFailure bool) (reviewops.Result, []hooks.Result, bool, error) {
	// The reviewer's clone owes the same answer as the author's, and asked here rather than at the pre-submit
	// hooks so a refusal stages nothing: the alternative is a reviewer left with a dirty index and a verdict they
	// did not record. It is asked about the two points a submission runs — a review of somebody's `pre-ready`
	// suite is not stopped by a suite this submission never executes. `--trust-once` is the way through when the
	// content under review *is* the hooks: the scripts run, nothing is recorded, and the reviewer gets to say
	// what they think. `--allow-hook-failure` covers refusing scripts, not unread ones, and `--no-hooks` clears
	// nothing here.
	if err := a.hooksPositionRefusal(ctx, s.repo, hooks.PreSubmit, hooks.PostSubmit); err != nil {
		return reviewops.Result{}, nil, false, err
	}
	if stageAll {
		if err := s.repo.StageAll(ctx); err != nil {
			return reviewops.Result{}, nil, false, err
		}
	}
	hc := hookContext{
		Point: hooks.PreSubmit, Slug: s.cs.Slug, Branch: s.cs.Branch, Base: s.cs.Base,
		Head: s.head, Dest: s.trunk.Ref, Outcome: string(outcome),
	}
	if body != "" {
		// The reviewer's prose travels as a file, not as an environment variable: it has newlines in it,
		// it can be long, and a policy hook that greps it for "LGTM" should read it the way it reads any
		// other file.
		f, err := os.CreateTemp("", "git-pair-review-body-*")
		if err != nil {
			return reviewops.Result{}, nil, false, err
		}
		if _, err := f.WriteString(body); err != nil {
			f.Close()
			os.Remove(f.Name())
			return reviewops.Result{}, nil, false, err
		}
		if err := f.Close(); err != nil {
			os.Remove(f.Name())
			return reviewops.Result{}, nil, false, err
		}
		hc.BodyFile = f.Name()
		defer os.Remove(f.Name())
	}

	pre, err := a.runPre(ctx, s, hc)
	overridden := false
	if err != nil {
		if !allowHookFailure {
			return reviewops.Result{}, pre, false, a.refusedBySubmitHook(ctx, s, pre)
		}
		overridden = true
		a.warn("git-pair: the pre-submit hooks refused this submission; --allow-hook-failure records it anyway\n")
	}

	result, err := reviewops.Submit(ctx, s.repo, s.cs, outcome, body, false,
		changeset.MeasureSubmission(ctx, s.repo, s.cs, s.trunk, s.head), s.trunk)
	if err != nil {
		return reviewops.Result{}, pre, overridden, err
	}

	hc.Point = hooks.PostSubmit
	// The post-hook is told the review commit, not the commit it reviewed: what now exists is the submission,
	// and a notification quoting the reviewed head describes a commit the reviewer was looking at rather than
	// the record this run made.
	hc.Head = result.Commit
	hc.BodyFile = ""
	post := a.runPost(ctx, s, hc)
	a.warnIfHooksMovedHead(ctx, s, result.Commit, hooks.PostSubmit, post)
	return result, append(pre, post...), overridden, nil
}

// refusedBySubmitHook is a pre-submit refusal with the one thing a refusal at this point makes ambiguous said
// out loud. The reviewer's content is staged, and there is no commit. Nothing else shows that: the next
// `git pair review submit` would re-stage and be fine, and the next plain `git commit` would land the
// reviewer's source edits as ordinary work with no verdict attached to them. A reviewer who has just been
// refused needs to know which of the two they are about to do.
func (a *app) refusedBySubmitHook(ctx context.Context, s *session, pre []hooks.Result) error {
	refusal := hookError(pre)
	if refusal == nil {
		refusal = fmt.Errorf("the pre-submit hooks refused the submission")
	}
	staged, err := s.repo.HasStagedChanges(ctx)
	if err != nil || !staged {
		return refusal
	}
	return fmt.Errorf("%s; the review was not recorded, and your staged changes are still staged — fix what the "+
		"hook reported and submit again, or unstage them before committing anything else", refusal)
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
	if err := a.refuseIfAbandoned(ctx, s); err != nil {
		return err
	}
	// A submission records all three ends of the diff it reviewed: the parent branch's tip, the commit the
	// diff was measured from, and that diff's identity. The parent moves for reasons the child's history
	// cannot show — its own rebases, its landing, its abandonment — the base stops being nameable when the
	// parent's branch is deleted, and the content is the thing the gate ultimately has to compare. One call
	// measures them, here and in the review screen, so the two surfaces cannot disagree (PRD §21). It
	// records what it could measure and nothing for what it could not, which is why no error comes back
	// from here: a missing measurement is an absence a later reader can see, not a reason to lose the
	// reviewer's verdict.
	result, hookResults, overridden, err := a.submitReview(ctx, s, outcome, opts.message, !opts.noStage, opts.allowHookFailure)
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
	lc := landingContractFor(ctx, s.repo, s)
	if a.json {
		return a.emitJSON(map[string]any{
			"changeset":       s.cs.Slug,
			"outcome":         string(result.Outcome),
			"commit":          result.Commit,
			"short":           short(result.Commit),
			"files":           orEmpty(result.Files),
			"empty":           result.Empty(),
			"previous_review": previous,
			"next_action":     nextActionFor(result.Outcome, lc),
			"hooks":           hooksForJSON(hookResults),
			"overridden":      overridden,
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
	if s.summary.LatestReview != nil {
		a.printf("  supersedes: %s (the newest submission decides the state)\n",
			reviewLabel(s.summary.LatestReview))
	}
	a.printf("  next:    %s\n", nextActionFor(result.Outcome, lc))
	if clean, err := s.repo.IsClean(ctx); err == nil && !clean {
		a.warn("\nwarning: the working tree is still dirty; those changes are not part of this review\n")
	}
	// A post-hook failure is reported the way every other one is: the exit code carries it, the wording says
	// what actually happened, and nothing is taken back. The submission is a published verdict — undoing it
	// would be a write the contract does not account for — so the only honest thing left is to say that the
	// record stands.
	if f, failed := firstFailureAt(hookResults, hooks.PostSubmit); failed {
		return fmt.Errorf("%s has a %s review recorded at %s, but the post-submit hook %q failed with exit %d — "+
			"the submission stands and nothing was undone",
			s.cs.Slug, strings.ToUpper(string(result.Outcome)), short(result.Commit), f.Hook.Name, f.Exit)
	}
	return nil
}

func nextActionFor(o model.Outcome, lc landingContract) string {
	switch o {
	case model.OutcomeBlock:
		return "author: `git pair change feedback`, address it, then `git pair change ready`"
	case model.OutcomeFeedback:
		return "author: `git pair change feedback` to read it; feedback is non-blocking, " +
			landingNextAction(lc)
	case model.OutcomeApprove:
		return "author: " + landingNextAction(lc)
	}
	return ""
}

// --- review history ---------------------------------------------------------

func newReviewHistoryCommand(a *app) *cobra.Command {
	var changesetSlug string
	cmd := &cobra.Command{
		Use:   "history",
		Short: "List review submissions for a changeset",
		Long: `List every review submission in chronological order.

Only commits carrying a valid Review-Outcome trailer count; ordinary commits do
not appear. Indexes are chronological, so 0 is the first review and -1 is the
most recent, matching ` + "`git pair diff --since-review`" + `.

REVIEWER is who made the submission — the author of the review commit, which is
the reviewer rather than the author of the change. ` + "`--json`" + ` reports the
same value as ` + "`reviewer`" + `.

--changeset reads another changeset by slug, from whichever branch carries it.`,
		Example: `  git pair review history
  git pair review history --json
  git pair review history --changeset booking-transaction
  git pair diff --since-review=-1`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.loadFor(cmd.Context(), changesetSlug)
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
						"index": i,
						"sha":   r.SHA,
						"short": r.Short,
						// reviewed_head is the commit this submission spoke about, from its
						// `Review-Head` trailer; absent when the marker names none.
						"reviewed_head": r.ReviewedHead,
						"outcome":       string(r.Outcome),
						"subject":       r.Subject,
						// The reviewer, named for the role rather than for the git field: on a
						// review submission the commit author *is* the reviewer, and "author"
						// means the person who wrote the change everywhere else in git-pair.
						"reviewer": r.Author,
						"when":     r.When.UTC().Format(time.RFC3339),
						"age":      lifecycle.Age(r.When, now()),
					})
				}
				return a.emitJSON(map[string]any{"changeset": s.cs.Slug, "reviews": out})
			}
			w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "INDEX\tSHA\tREVIEWED\tOUTCOME\tREVIEWER\tAGE\tSUBJECT")
			for i, r := range s.summary.Reviews {
				// REVIEWED is the commit the submission spoke about — its `Review-Head`, which is
				// where this commit sits in the line rather than what it changed. REVIEWER is the
				// commit's author, so a changeset reviewed by two people says who said what.
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
					i, r.Short, short(r.ReviewedHead), r.Outcome, r.Author, lifecycle.Age(r.When, now()), r.Subject)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&changesetSlug, "changeset", "",
		"read the changeset with this slug, from whichever branch carries it")
	return cmd
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
		Example: `  git pair review reopen
  git pair review reopen --no-hooks
  git pair review reopen --trust-hooks-once`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReviewReopen(cmd.Context(), a)
		},
	}
	noHooksFlag(cmd, a)
	trustHooksOnceFlag(cmd, a)
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
	sp, err := span.Resolve(ctx, s.repo, s.measureBase(ctx), s.summary, sel)
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
	return openSession(ctx, a, s, sel, "git pair review reopen", note)
}

// openSession runs the TUI on a chosen span. invocation is the command as the reviewer can type it,
// which the no-terminal message quotes back at them: the refusal that names a command they did not
// run reads like a bug in the tool rather than an answer about the terminal.
// note, if set, is written to stderr before the session takes the screen.
func openSession(ctx context.Context, a *app, s *session, sel span.Selector, invocation, note string) error {
	if !console.Interactive() {
		return &usageError{fmt.Errorf(
			"`%s` needs a terminal; use `git pair diff`, `git pair review about`, "+
				"`git pair review thread`, and `git pair review submit` instead", invocation)}
	}
	if note != "" {
		a.warn("%s\n", note)
	}
	err := tui.Run(ctx, tui.Options{
		Repo: s.repo, Changeset: s.cs, Summary: s.summary, Span: sel, Trunk: s.trunk,
		Mouse: tui.ResolveMouse(ctx, s.repo),
		// The screen submits through the same call the command does, so the reviewer's `pre-submit` hooks fire
		// wherever a verdict is recorded. A second path into `reviewops.Submit` would be a path with no hooks.
		Submit: a.screenSubmission(ctx, s),
	})
	if errors.Is(err, tui.ErrQuit) {
		return nil
	}
	return err
}

// screenSubmission is the review screen's submission path: the command's own path, with the one change the
// screen needs because it owns the terminal while the submission runs. The hooks' output comes back inside the
// refusal instead of on stderr, where the frame would paint over it (see app.hookPresenter), so the reviewer
// reads why the hook stopped them without leaving the screen. Everything else is shared with `review submit`,
// which is the point of injecting it: a screen that submitted for itself would be a second surface where the
// reviewer's `pre-submit` hooks do not run.
//
// A refusal ends the submission and not the session. The comments, the ABOUT edit and the staged files all
// exist at that moment, so the screen keeps them and says what was refused; the reviewer fixes the finding and
// presses the outcome key again. Exiting nonzero is what `review submit` does, and it is right there — a
// command that ran and was refused should say so to the shell. Quitting the screen is not that: `q` is the
// ordinary way out of a screen that looked at a changeset and recorded nothing, and a nonzero code for it would
// report a business refusal nobody made.
func (a *app) screenSubmission(ctx context.Context, s *session) func(model.Outcome) (reviewops.Result, error) {
	return func(outcome model.Outcome) (reviewops.Result, error) {
		var printed []string
		a.hookPresenter = func(r hooks.Result) { printed = append(printed, hookOutputLines(r)...) }
		defer func() { a.hookPresenter = nil }()

		result, _, _, err := a.submitReview(ctx, s, outcome, "", true, false)
		if err != nil {
			err = withHookText(err, printed)
		}
		return result, err
	}
}

// withHookText puts the refused submission's hook output into the message the screen shows. The screen wraps
// prose, so several lines are fine; the text is capped, because a lint run that dumped a megabyte would
// replace the review rather than inform it. The tail survives and the head is cut: for a test suite or a lint
// run, the part that says what failed is at the end.
func withHookText(err error, chunks []string) error {
	if len(chunks) == 0 {
		return err
	}
	const (
		maxLines = 15
		maxRunes = 1200
	)
	lines := strings.Split(strings.Join(chunks, "\n"), "\n")
	truncated := false
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
		truncated = true
	}
	text := strings.Join(lines, "\n")
	if len(text) > maxRunes {
		text = text[len(text)-maxRunes:]
		truncated = true
	}
	note := ""
	if truncated {
		note = "\n… the earlier output is cut; `git pair review submit` prints it in full."
	}
	return fmt.Errorf("%w\n\nthe hook printed:\n%s%s", err, text, note)
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
