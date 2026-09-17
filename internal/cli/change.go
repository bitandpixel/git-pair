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
	cmd.AddCommand(newChangeInitCommand(a), newChangeReadyCommand(a),
		newChangeFeedbackCommand(a), newChangeWaitCommand(a))
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

  git pair change init --base main --about "$DESCRIPTION"
  git pair change init --base main --about - < about.md
  cat about.md | git pair change init --base main

Existing content is never overwritten silently: replacing a populated ABOUT.md
takes --set-about, the same way changing a base takes --set-base.`,
		Example: `  git pair change init --base main
  git pair change init --base booking-transaction   # stacked branch
  git pair change init --base main --about - < draft.md`,
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
		return &usageError{fmt.Errorf("%w: run `git switch -c <branch>` before `git pair change init`", changeset.ErrDetachedHead)}
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

// --- change feedback --------------------------------------------------------

type feedbackOptions struct {
	stat     bool
	nameOnly bool
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
  git pair change feedback --name-only`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChangeFeedback(cmd.Context(), a, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.stat, "stat", false, "show a diffstat instead of the full diff")
	cmd.Flags().BoolVar(&opts.nameOnly, "name-only", false, "list only the files the review changed")
	return cmd
}

func runChangeFeedback(ctx context.Context, a *app, opts *feedbackOptions) error {
	s, err := a.load(ctx)
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

Exits when the changeset stops being ready and becomes actionable — BLOCKED, FEEDBACK,
APPROVED or CLOSED — and prints what happened. Fully non-interactive.

  git pair change ready
  git pair change wait --fetch --interval 30s --json
  git pair change feedback

Without --fetch only local repository state is re-read, which is enough when the reviewer
works in the same clone. With --fetch every round runs ` + "`git fetch`" + ` against the
configured remote first and also evaluates the branch's remote-tracking ref, so reviews
submitted in another clone end the wait. This stays forge-agnostic: no forge APIs, only
ordinary refs that git fetch brings down.

Exits non-zero if the changeset is not ready to begin with, since then there is nothing to
wait for, and on timeout. Without --timeout it waits indefinitely.`,
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
	case model.StateBlocked, model.StateFeedback, model.StateApproved, model.StateClosed:
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
		return "`git pair change feedback`; feedback is non-blocking, `git pair review close` when integration is due"
	case model.StateApproved:
		return "`git pair review close` before squash/merge"
	case model.StateClosed:
		return "safe to squash/merge; review history is under refs/reviews/"
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
