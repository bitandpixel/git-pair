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
	cmd.AddCommand(newChangeReadyCommand(a), newChangeUnreadyCommand(a),
		newChangeAbandonCommand(a), newChangeFeedbackCommand(a), newChangeWaitCommand(a),
		newChangeStackCommand(a), newChangeCombineCommand(a),
		newChangeIntegrateCommand(a), newChangeTidyCommand(a))
	return cmd
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
	// The invariant, on the way to the queue. This is the half that catches propagation: a branch that
	// acquired the shape from a merge, a pull, or a parent that already carried two is refused whatever its
	// history looks like, which is where `init` cannot reach.
	if err := a.refuseBranchShape(ctx, s); err != nil {
		return err
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
		a.warn("warning: %s still has the empty `init` template; describe the change for the reviewer\n",
			s.cs.AboutPath())
	}

	sha, err := marker.Commit(ctx, s.repo, marker.ReadyMessage(s.cs.Slug), s.trunk)
	if err != nil {
		return fmt.Errorf("creating ready marker: %w", err)
	}
	// The marker commit is the whole operation: no ref is written, and nothing is anchored. The
	// branch holds the chain from here, and it is the only thing that does until landing — which is
	// why the merge happens before the branch goes away rather than after (PRD §13.3).
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
	a.printf("  queue: `git pair queue` now lists this changeset\n")
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
		Short: "Withdraw this changeset from review, and from the merge request",
		Long: `Record that this changeset is no longer offered for review, and take it out of the queue.

Use it when you want to keep implementing after ` + "`change ready`" + `. The marker says so on
purpose rather than leaving a reviewer to work it out from the diff, which is the difference between
an offer you withdrew and an offer you forgot to withdraw.

It also withdraws a declaration made with ` + "`git pair change integrate`" + `. That request is what a
pipeline merges on, so the way to stop one has to be a commit on the branch as well: the moment the
unready marker lands, ` + "`git pair check --json`" + ` reports ` + "`integrating`" + ` false again.

The changeset returns to WORKING, and a later ` + "`git pair change ready`" + ` puts it back in the
queue under the same gate as the first time: review additions that still survive unchanged have to be
resolved or acknowledged.

The marker is written only when the changeset is actually in review — READY, APPROVED, FEEDBACK, or
INTEGRATING. On a changeset that is WORKING or BLOCKED there is nothing to withdraw, so the command
succeeds without recording anything and a script can unready unconditionally.

It records a marker and nothing else. There is no ref to move: the retraction lives on the branch
beside the offer it withdraws. What makes a changeset's history permanent is the merge — landing
puts the directory, and the chain under it, into the destination.

Once the changeset has landed, this refuses. A changeset that has landed has no readiness
to withdraw, and "nothing to withdraw" would be an answer about the branch rather than about the work
being finished.`,
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
	if err := marker.RefuseIntegrated(ctx, s.repo, s.cs.Slug, s.trunk); err != nil {
		return err
	}
	if !inReview(s.summary.State) {
		return printUnready(a, s, "")
	}
	sha, err := marker.Commit(ctx, s.repo, marker.UnreadyMessage(s.cs.Slug), s.trunk)
	if err != nil {
		return fmt.Errorf("creating unready marker: %w", err)
	}
	return printUnready(a, s, sha)
}

// inReview reports whether the changeset is currently offered to a reviewer, and so
// has a readiness that `change unready` can withdraw. BLOCKED is excluded because the
// author is already expected to act, and the block marker stays the newest marker until
// they ready the changeset again.
//
// INTEGRATING belongs here for the reason the state exists: a declaration is the author's own statement,
// made on the branch, and the way to take it back has to be a commit on that branch too. `check` stops
// reporting the request as soon as the unready marker lands, so the withdrawal is real from the moment it
// is written — and an author who has just approved their own work should not have to rebase a marker away
// to stop a pipeline from merging it.
func inReview(state model.State) bool {
	switch state {
	case model.StateReady, model.StateApproved, model.StateFeedback, model.StateIntegrating:
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
	a.printf("  queue:   `git pair queue` no longer lists this changeset\n")
	if s.summary.State == model.StateIntegrating {
		// The withdrawal is bigger than the review queue for this one: the request a pipeline merges on is
		// gone, and the reader who ran `change integrate` should see that named rather than infer it.
		a.printf("  merge:   the declaration is withdrawn — `git pair check --json` no longer reports it integrating\n")
	}
	a.printf("  next:    `git pair change ready` puts it back once the work is done\n")
	return nil
}

// --- change abandon ---------------------------------------------------------

func newChangeAbandonCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "abandon",
		Short: "End the current changeset",
		Long: `Record that this changeset will not be taken forward.

Abandoning is a decision about work that is still here, which is why the command needs the branch: the
terminal marker is an ordinary commit on it, carrying ` + "`Review-State: abandoned" + `. That commit is
the record — ` + "`git log`" + ` shows it, ` + "`git pair status`" + ` reports it as abandoned, and
` + "`git pair queue`" + ` stops listing the changeset because the newest marker says the work
ended.

Nothing is anchored and no ref is written. The chain stays on the branch, so deleting the branch loses
it. Landing is what makes a changeset's history permanent, by putting the directory in the destination, and
abandoning is the case where that does not happen — an ending nobody integrated has no landing to point at
(PRD §13.3).

Unlike ` + "`change unready`" + `, which withdraws an offer for now, this one closes the changeset:
` + "`change ready`" + `, ` + "`change unready`" + ` and ` + "`review submit`" + ` refuse against it
afterwards. That is also what stops a new branch reusing the name of a changeset that ended.

The state stays WORKING and no state value is added: the ending is reported as
` + "`abandoned_commit`" + ` in ` + "`status --json`" + `, beside the state rather than inside it.

Re-running the command changes nothing and succeeds. Abandoning work whose branch is already gone
refuses: post-landing bookkeeping is not a lifecycle act.`,
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
	if err := marker.RefuseIntegrated(ctx, s.repo, s.cs.Slug, s.trunk); err != nil {
		return err
	}
	at, err := terminalRecord(s.summary)
	if err != nil {
		return err
	}
	if at != nil {
		// Already ended. Like `change unready` on a changeset that was never offered,
		// the wanted state already holds, so a script can abandon unconditionally.
		return printAbandoned(a, s, at, "")
	}

	sha, err := marker.Commit(ctx, s.repo, marker.AbandonedMessage(s.cs.Slug), s.trunk)
	if err != nil {
		return fmt.Errorf("creating abandon marker: %w", err)
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
		})
	}
	if sha == "" {
		a.printf("Changeset %s is already abandoned by %s.\n", s.cs.Slug, at.Short)
		return nil
	}
	a.printf("Abandoned changeset %s\n\n", s.cs.Slug)
	a.printf("Terminal marker: %s\n", sha)
	a.printf("Nothing further is recorded for it; the history stays on %s\n", branchOrHead(s))
	return nil
}

// branchOrHead names where a changeset's history currently lives, for a message that has to say
// which ref is holding the chain: the branch while it exists, and HEAD when the caller is detached.
func branchOrHead(s *session) string {
	if s.cs.Branch == "" {
		return "HEAD"
	}
	return s.cs.Branch
}

// terminalRecord returns the newest abandon marker for a changeset from the chain summary the caller
// already has. It used to fall back to the archive ref, which held the unsquashed chain after the branch
// was deleted, so a slug recreated after `git branch -D` could not be reopened. The durable refs are gone,
// so the branch (or, for a landed changeset, the destination's history) is the only place an ending is
// read from. The limit is stated in PRD §13.3: an abandon marker whose chain no clone can walk is not a
// finding git-pair can report.
func terminalRecord(derived lifecycle.Summary) (*lifecycle.Event, error) {
	return derived.Abandoned, nil
}

// refuseIfAbandoned is the write gate: nothing records a marker onto a changeset that
// has ended. Exit 1 rather than 2 — the repository says no, and re-running after undoing
// the abandonment (there is no such command; the marker is history) is not a retry.
func (a *app) refuseIfAbandoned(ctx context.Context, s *session) error {
	at, err := terminalRecord(s.summary)
	if err != nil {
		return err
	}
	if at != nil {
		return fmt.Errorf("changeset %s was abandoned by %s; git-pair records nothing further for it",
			s.cs.Slug, at.Short)
	}
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
	cmd.Flags().BoolVar(&opts.fetch, "fetch", false, "run git fetch before each check")
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
		// Each round is a fresh observation, which is the whole point of the loop: a review submitted in
		// another clone has to end the wait. The memo's "one run" contract does not stretch across a poll,
		// so it is dropped before the round rather than held for the minutes the author is waiting.
		s.repo.ResetMemo()
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
	return reportWait(a, s.cs.Slug, s.cs.Base, seen)
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

func reportWait(a *app, slug, base string, r waitInput) error {
	out := waitResult{
		Changeset: slug, PreviousState: r.PreviousState, State: stateName(r.State),
		Ref: r.Ref, Fetches: r.Fetches, WaitedSeconds: r.Waited, TimedOut: r.TimedOut,
	}
	if r.Review != nil {
		out.ReviewCommit = r.Review.Short
		out.ReviewCommitFull = r.Review.SHA
	}
	out.NextAction = waitNextAction(r.State, r.Ref, base)

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

func waitNextAction(s model.State, ref, base string) string {
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
		return "`git pair change feedback`; feedback is non-blocking, " + landingNextAction(base)
	case model.StateApproved:
		return landingNextAction(base)
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
