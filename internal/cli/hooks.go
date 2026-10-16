package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"gitpair/internal/git"
	"gitpair/internal/hooks"
)

// --- running hooks ----------------------------------------------------------

// hookContext is what a hook is told about the event that fired. It travels as environment variables, in
// git's own idiom, because a script must not have to re-derive the changeset: the resolution rules are in
// this binary, and two answers to "which changeset is this?" is worse than one.
type hookContext struct {
	Point   hooks.Point
	Slug    string
	Branch  string
	Base    string
	Head    string
	Dest    string
	Outcome string // reviewer-side points; empty elsewhere
	// BodyFile is a file holding the `-m` text, for a reviewer's own policy hook. The body travels as a
	// path rather than as a variable because it is the reviewer's prose, of arbitrary length and with
	// newlines in it, and a script should read it the way it reads any other file.
	BodyFile string
}

func (c hookContext) env() map[string]string {
	env := map[string]string{
		// The marker this run is running hooks under. A hook that shells out to `git pair` inherits it,
		// which is the recursion guard: a hook that runs the gate must not fire the gate's hooks.
		hooks.EnvDisable:      "1",
		"GIT_PAIR_HOOK_POINT": c.Point.String(),
		"GIT_PAIR_CHANGESET":  c.Slug,
		"GIT_PAIR_BRANCH":     c.Branch,
		"GIT_PAIR_BASE":       c.Base,
		"GIT_PAIR_HEAD":       c.Head,
	}
	if c.Dest != "" {
		env["GIT_PAIR_DEFAULT_BRANCH"] = c.Dest
	}
	if c.Outcome != "" {
		env["GIT_PAIR_REVIEW_OUTCOME"] = c.Outcome
		// The head the submission names, spelled out beside the outcome: a policy hook that refuses to
		// approve unless a suite passed wants to say which commit its verdict was about, and `HEAD` at
		// the moment it runs is that commit only if nothing staged has been committed yet.
		env["GIT_PAIR_REVIEW_HEAD"] = c.Head
	}
	if c.BodyFile != "" {
		env["GIT_PAIR_REVIEW_BODY_FILE"] = c.BodyFile
	}
	return env
}

// runPre runs the point's hooks on the way into a transition and refuses the operation when one of them
// fails.
func (a *app) runPre(ctx context.Context, s *session, c hookContext) ([]hooks.Result, error) {
	results, err := a.runPoint(ctx, s, c, blockingMode)
	if err != nil {
		return results, err
	}
	return results, hookError(results)
}

// runPost runs the point's hooks on the way out of a transition and reports them.
//
// It returns no error, because there is nothing left to refuse: the marker is written, the changeset is in
// the queue or out of it, and git-pair does not unwind a lifecycle transition. What it can do is say so —
// the caller reports the failure and lets the exit code carry it, so automation learns that something after
// the operation went wrong without being told the operation itself did.
//
// A hook that could not be reached at all is a different case from a hook that ran and failed. Discovery
// trouble is printed and not fatal: a repository whose hooks directory has become unreadable should not turn
// into a claim that the transition, which happened, did not.
func (a *app) runPost(ctx context.Context, s *session, c hookContext) []hooks.Result {
	results, err := a.runPoint(ctx, s, c, reportingMode)
	if err != nil {
		a.warn("git-pair: the %s hooks could not be read: %v\n", c.Point, err)
	}
	return results
}

// firstFailure picks the result the exit code should be reported for. Post-hooks keep running after one
// fails — there is nothing to stop, and the remaining hooks may have side effects their owner still wants —
// so the first failure is the one named.
func firstFailure(results []hooks.Result) (hooks.Result, bool) {
	for _, r := range results {
		if r.Blocking() {
			return r, true
		}
	}
	return hooks.Result{}, false
}

// firstFailureAt is `firstFailure` restricted to one point, for a caller that ran both halves of a transition
// and needs to know whether the half that could not refuse anything is the one that failed.
func firstFailureAt(results []hooks.Result, point hooks.Point) (hooks.Result, bool) {
	for _, r := range results {
		if r.Hook.Point == point && r.Blocking() {
			return r, true
		}
	}
	return hooks.Result{}, false
}

// executedHooks reports whether any of these results came from a hook that actually ran. It is what makes
// the clean-tree re-check cost nothing where there are no hooks: a repository with none configured does the
// work it always did, and one extra `git status` per command is a price nobody agreed to. A hook that was
// skipped for trust, or refused before starting for want of the executable bit, changed no file.
func executedHooks(results []hooks.Result) bool {
	for _, r := range results {
		if r.Ran() {
			return true
		}
	}
	return false
}

// refuseIfHooksDirtiedTree refuses a marker commit when a hook that ran left changes in the working tree.
//
// The marker is written only over a clean tree, so files a hook generated and nobody staged would otherwise
// travel into the span a reviewer is about to read. The alternative — committing them on the author's behalf
// — is a thing git-pair does not do, so refusing is the only honest answer, and the refusal names the point
// that produced them rather than reporting a dirty tree the author did not create.
func (a *app) refuseIfHooksDirtiedTree(ctx context.Context, s *session, results []hooks.Result, point hooks.Point, slug string) error {
	if !executedHooks(results) {
		return nil
	}
	clean, err := s.repo.IsClean(ctx)
	if err != nil {
		return err
	}
	if !clean {
		return fmt.Errorf("a %s hook left changes in the working tree; commit or stash them, then mark %s ready again",
			point, slug)
	}
	return nil
}

// runMode says which half of the contract a point is being run under. It is a named type rather than a bool
// because the two halves differ in three places at once — whether execution stops at a failure, whether a
// failure becomes a refusal, and what the caller owes the reader — and a `true` at a call site says none of
// them.
type runMode int

const (
	// blockingMode is the way into a transition: the first failure stops the rest, and the caller refuses.
	blockingMode runMode = iota
	// reportingMode is the way out: every hook runs, because there is nothing left to save, and the caller
	// reports what happened without reversing it.
	reportingMode
)

// runPoint is the shared half: discovery, execution, and the rule that hook text never reaches stdout.
func (a *app) runPoint(ctx context.Context, s *session, c hookContext, mode runMode) ([]hooks.Result, error) {
	if s == nil {
		return nil, nil
	}
	run := a.hookRun(s.repo)
	if off, why := run.Disabled(ctx); off {
		// A deliberate off switch needs no commentary; the person or the job set it knowing why.
		_ = why
		return []hooks.Result{}, nil
	}
	plan, err := run.Discover(ctx, c.Point)
	if err != nil {
		return nil, err
	}
	if len(plan.Hooks) == 0 {
		return []hooks.Result{}, nil
	}
	env := c.env()
	var results []hooks.Result
	runnable := plan.Hooks
	if !plan.TrackedTrusted {
		results = append(results, plan.SkippedUntrusted()...)
		runnable = nil
		for _, h := range plan.Hooks {
			if h.Root != hooks.RootTracked {
				runnable = append(runnable, h)
			}
		}
		// The sentence depends on why they did not run, because the reader's next act does. An unrecorded tree
		// is content nobody has read; a declined one is content somebody read and chose not to run, and telling
		// that person to accept it would be telling them to revisit a decision they already made — beside
		// losing the hash that tells them whether the tree they declined is the tree that is here. Both come
		// from the plan, so the warning describes the same read that decided to skip.
		if plan.TrustState == hooks.TrustDeclined {
			// The recorded hash, not the one the state machine compared: this sentence is read by the person who
			// declined, and the number they need is the one they chose.
			a.warn("git-pair: %d tracked hook(s) at %s did not run — the tree under %s is declined for this clone (declined %s); `git pair hooks --trust` runs it after you read it\n",
				len(plan.SkippedUntrusted()), c.Point, hooks.TrackedRootDir(s.repo.Dir), plan.Declined)
		} else {
			a.warn("git-pair: %d tracked hook(s) at %s did not run — the tree under %s has not been accepted; "+
				"review it and run `git pair hooks --trust`\n",
				len(plan.SkippedUntrusted()), c.Point, hooks.TrackedRootDir(s.repo.Dir))
		}
	}
	if len(runnable) > 0 {
		if mode == blockingMode {
			results = append(results, hooks.Run(ctx, s.repo, runnable, env)...)
		} else {
			results = append(results, hooks.RunAll(ctx, s.repo, runnable, env)...)
		}
	}
	for _, r := range results {
		if a.hookPresenter != nil {
			a.hookPresenter(r)
			continue
		}
		a.replayHookOutput(r)
	}
	return results, nil
}

// hookOutputLines is the text of one hook run, in the shape the human surfaces print: a failing hook always
// shows its own text, because that is the reason the operation stopped, and a passing hook shows it only when
// $GIT_PAIR_HOOK_OUTPUT asks for it, because a suite that prints 400 lines on success would make every handoff
// unreadable.
func hookOutputLines(r hooks.Result) []string {
	if r.Status == hooks.StatusSkippedUntrusted {
		return nil
	}
	if !r.Blocking() && !hooks.EchoOutput() {
		return nil
	}
	var lines []string
	for _, chunk := range []string{r.Stdout, r.Stderr} {
		if trimmed := strings.TrimRight(chunk, "\n"); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

// replayHookOutput keeps hook text off stdout, where `--json` lives, by putting it on stderr.
func (a *app) replayHookOutput(r hooks.Result) {
	for _, lines := range hookOutputLines(r) {
		a.warn("%s\n", lines)
	}
}

// hookError turns a blocking result into the refusal the operation should return.
//
// The exit code is 1, the business-refusal half of the contract in `root.go`: the invocation was right and
// the repository said no. A hook's nonzero exit must not arrive as exit 3, which the same table documents as
// "git itself failed" — an agent told 3 goes looking for a broken git rather than a failing suite, and the
// whole value of the table is that 1 and 3 mean different things.
func hookError(results []hooks.Result) error {
	for _, r := range results {
		if !r.Blocking() {
			continue
		}
		switch r.Status {
		case hooks.StatusNotExecutable:
			return fmt.Errorf("hook %q at %s is not executable; make it one with `chmod +x %s`",
				r.Hook.Name, r.Hook.Point, displayPath(r.Hook.Path))
		case hooks.StatusTimeout:
			return fmt.Errorf("hook %q at %s exceeded its deadline; raise it with $%s or `git config git-pair.hookTimeout`",
				r.Hook.Name, r.Hook.Point, hooks.TimeoutEnv)
		default:
			return fmt.Errorf("hook %q at %s refused the operation with exit %d",
				r.Hook.Name, r.Hook.Point, r.Exit)
		}
	}
	return nil
}

// hookReasons turns the hook outcomes that are not a pass into gate reasons.
//
// The wording names the point and the script rather than the condition, because a reader who cannot tell
// git-pair's own conditions apart from the repository's own scripts will go looking for a marker that is not
// missing. Every one of these says "a file you wrote said no", which is a different fix from any condition
// the gate derives from history.
func hookReasons(results []hooks.Result) []string {
	var out []string
	for _, r := range results {
		if !r.Blocking() {
			continue
		}
		switch r.Status {
		case hooks.StatusTimeout:
			out = append(out, fmt.Sprintf("the %s hook %q exceeded its deadline (raise it with $%s)",
				r.Hook.Point, r.Hook.Name, hooks.TimeoutEnv))
		case hooks.StatusNotExecutable:
			out = append(out, fmt.Sprintf("the %s hook %q is not executable", r.Hook.Point, r.Hook.Name))
		default:
			out = append(out, fmt.Sprintf("the %s hook %q failed with exit %d", r.Hook.Point, r.Hook.Name, r.Exit))
		}
	}
	return out
}

// hooksForJSON renders the results for a `--json` verdict. The array is `[]` when nothing is configured,
// following the rule in `orEmpty`: an array answers "asked, and none", and null answers "not asked".
func hooksForJSON(results []hooks.Result) []map[string]any {
	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		out = append(out, map[string]any{
			"point":       r.Hook.Point.String(),
			"name":        r.Hook.Name,
			"root":        string(r.Hook.Root),
			"status":      string(r.Status),
			"exit":        r.Exit,
			"duration_ms": r.DurationMS,
		})
	}
	return out
}

// displayPath shortens an absolute path under the work tree to the form a person would type.
func displayPath(path string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return path
	}
	if rel, err := filepath.Rel(cwd, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

// --- git pair hooks ---------------------------------------------------------

func newHooksCommand(a *app) *cobra.Command {
	var trust, decline bool
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Report the lifecycle hooks this repository and this clone configure",
		Long: `List the lifecycle hooks configured under the two hook roots, and whether the tracked one is trusted.

Hooks are the repository's and the client's own scripts, run at git-pair's lifecycle points: a test suite
before a hand-off, a validation before a declaration, a notification after a submission. git-pair supplies
the moment and the context; the client supplies the command and owns everything it does.

The roots, searched in this order:

  .git-pair/hooks/<point>    committed with the repository, so shared — and so code from a stranger, which
                             runs only after a person records acceptance with ` + "`--trust`" + `
  <clone gitdir>/            this clone only, never shared, trusted by construction: one directory per clone, so a
  git-pair/hooks/            check you attach to your machine is attached once, not once per worktree

At each root a point is either one executable file or a directory whose executable entries run in
lexicographic order. Entries a reader would not call a hook — a dot-file, git's own ` + "`*.sample`" + `, a ` + "`*.md`" + ` note,
a ` + "`~`" + ` backup, a nested directory — are passed over.

The tracked tree is trusted by content, not by a flag: any edit to any hook invalidates the acceptance,
because the thing that was approved was that content. The acceptance covers exactly the entries that run — the list
above and nothing else — so an editor's swap file beside a hook cannot revoke a position, and a script that would
run cannot stay outside one. Four states describe a clone's relation to it —
` + "`accepted`" + `, ` + "`declined`" + `, ` + "`changed`" + `, ` + "`unrecorded`" + `. Only ` + "`accepted`" + ` runs the scripts.

The record lives with the clone rather than with one working directory, so reading the scripts once covers every
worktree cut from the repository — including ones cut later — and it holds one entry per version, so the branch
that edits the suite does not invalidate what trunk's checkout accepted.

The commands that start or advance work refuse while the tree is ` + "`changed`" + ` or ` + "`unrecorded`" + `: ` + "`init`" + ` before a
changeset exists, ` + "`change ready`" + ` before the hand-off, ` + "`review submit`" + ` before the reviewer's content is
staged, and the gate behind ` + "`check`" + ` and ` + "`change integrate`" + ` before a merge. A hand-off, a verdict and a
merge should not rest on checks that never ran. What still works with nothing recorded is this command, and
the two that take work out of the queue — ` + "`change unready`" + ` and ` + "`change abandon`" + `: reading the scripts and
putting the work down are what you do because you have not decided yet.

` + "`--decline`" + ` is the other position a person can take: read the scripts, choose not to run them here. It is
recorded against the same content hash as an acceptance, so the next version the repository ships is
unrecorded again rather than quietly inherited — the difference between a team that opted out and a team that
never heard is a fact the report can state, and a fact a gate can ask for without pushing anyone into
approving a script they do not want.

Nothing here runs a hook. This command reports configuration; the lifecycle commands run.

Exit codes: 0 reported, 2 usage, 3 git failed.`,
		Example: `  git pair hooks
  git pair hooks --json
  git pair hooks --trust
  git pair hooks --decline`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if trust && decline {
				return &usageError{fmt.Errorf("--trust and --decline record opposite positions about the same content; say which one this clone is taking")}
			}
			return runHooksReport(cmd.Context(), a, trust, decline)
		},
	}
	cmd.Flags().BoolVar(&trust, "trust", false,
		"record the tracked hooks tree as accepted so its hooks may run; prints what is being approved first")
	cmd.Flags().BoolVar(&decline, "decline", false,
		"record the tracked hooks tree as read and chosen not to run; prints what is being declined first")
	return cmd
}

// unknownEntry is an entry under a hooks root whose name is not a lifecycle point, kept with its root
// because the fix differs: a misspelled directory under the tracked root is a file to correct and commit,
// and one under the clone root is a file the reader wrote themselves and can just rename.
type unknownEntry struct {
	name string
	dir  string
}

// hookEntry is one configured hook as this command reports it. Every point in the vocabulary is invoked, so a
// configured hook is a hook that runs; what a reader has to notice is `executable`, and the `unknown` list
// under the hooks, which is where a typo'd point name lands.
type hookEntry struct {
	Point      string `json:"point"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Root       string `json:"root"`
	Executable bool   `json:"executable"`
}

// hooksReport is `git pair hooks --json`.
type hooksReport struct {
	TrackedRoot string `json:"tracked_root"`
	CloneRoot   string `json:"clone_root"`
	Trusted     bool   `json:"trusted"`
	// TrustState says which of the reasons `trusted: false` carries: `accepted`, `declined`, `changed`, or
	// `unrecorded`. It is a state rather than a second boolean because the reader's next action differs — one
	// is "read these", another is "the repository moved these, read what changed", and a decline is a position
	// nobody needs to revisit until the content does.
	TrustState string `json:"trust_state"`
	Accepted   string `json:"accepted,omitempty"`
	// Declined is the recorded decline, carried even when it no longer matches the tree: "declined at 3f1a…,
	// the tree is now 82b0…" is the sentence that tells a person the script they refused has a new version
	// sitting unrecorded behind it.
	Declined string `json:"declined,omitempty"`
	// TreeHash is the tracked tree's current hash, carried whenever the tree is not the accepted content: a
	// reader comparing the two needs both, and a reader whose hooks are trusted needs neither.
	TreeHash string      `json:"tree_hash,omitempty"`
	Hooks    []hookEntry `json:"hooks"`
	Unknown  []string    `json:"unknown"`
	Disabled string      `json:"disabled_by,omitempty"`
}

func runHooksReport(ctx context.Context, a *app, trust, decline bool) error {
	repo, err := a.loadRepo(ctx)
	if err != nil {
		return err
	}
	gitDir, err := repo.GitDir(ctx)
	if err != nil {
		return err
	}
	if trust {
		return acceptHooks(ctx, a, repo, gitDir)
	}
	if decline {
		return declineHooks(ctx, a, repo, gitDir)
	}

	rep := hooksReport{
		TrackedRoot: hooks.TrackedRootDir(repo.Dir),
		CloneRoot:   hooks.CloneRootDir(gitDir),
		Hooks:       []hookEntry{},
		Unknown:     []string{},
	}
	if off, why := a.hookRun(repo).Disabled(ctx); off {
		rep.Disabled = why
	}
	state, accepted, current, err := hooks.Trust(ctx, repo)
	if err != nil {
		return err
	}
	rep.Trusted = state == hooks.TrustAccepted
	rep.TrustState = string(state)
	rep.Accepted = accepted
	rep.Declined, err = hooks.Declined(ctx, repo)
	if err != nil {
		return err
	}
	if state != hooks.TrustAccepted {
		rep.TreeHash = current
	}
	// Unknown entries are reported per root, because the fix differs: a misspelled directory under the
	// tracked root is a file to correct and commit, and one under the clone root is a file the reader wrote
	// themselves and can just rename.
	var found []unknownEntry
	for _, u := range hooks.UnknownPointsIn(rep.TrackedRoot) {
		found = append(found, unknownEntry{u, rep.TrackedRoot})
	}
	for _, u := range hooks.UnknownPointsIn(rep.CloneRoot) {
		found = append(found, unknownEntry{u, rep.CloneRoot})
	}
	for _, point := range hooks.AllPoints() {
		plan, err := a.hookRun(repo).Discover(ctx, point)
		if err != nil {
			return err
		}
		for _, h := range plan.Hooks {
			info, err := os.Stat(h.Path)
			rep.Hooks = append(rep.Hooks, hookEntry{
				Point: point.String(), Name: h.Name, Path: h.Path, Root: string(h.Root),
				Executable: err == nil && info.Mode()&0o111 != 0,
			})
		}
	}
	sort.SliceStable(rep.Hooks, func(i, j int) bool {
		a, b := rep.Hooks[i], rep.Hooks[j]
		if a.Point != b.Point {
			return a.Point < b.Point
		}
		if a.Root != b.Root {
			return a.Root == string(hooks.RootTracked)
		}
		return a.Name < b.Name
	})
	for _, u := range found {
		rep.Unknown = append(rep.Unknown, u.name)
	}
	rep.Unknown = dedupe(rep.Unknown)

	if a.json {
		return a.emitJSON(rep)
	}
	if rep.Disabled != "" {
		a.printf("hooks are switched off for this run by %s\n", rep.Disabled)
	}
	if len(rep.Hooks) == 0 {
		a.printf("no hooks configured\n")
		a.printf("  tracked: %s\n  clone:   %s\n", displayPath(rep.TrackedRoot), displayPath(rep.CloneRoot))
		// The misspelled point still has to be said, because "nothing is configured" beside a
		// directory that clearly holds a script is exactly the message that sends a team away
		// believing their check is running.
		reportUnknown(a, rep, found)
		return nil
	}
	if !rep.Trusted {
		// Three states, three sentences. "Never recorded" is a setting nobody has touched; "changed" is the
		// repository shipping a new version of a check this clone had already approved, and it is the case where
		// work goes on with the check not running and nobody having decided that; "declined" is a decision
		// somebody did make, and the only thing it needs is that the hash beside it is the one they chose, so a
		// new version is visible as new. On stderr, beside the note a run makes when it skips these same hooks:
		// the table is the answer, this is the warning about it, and a script that captures the report should
		// not have the warning inside it.
		switch hooks.TrustState(rep.TrustState) {
		case hooks.TrustChanged:
			// "this clone recorded", not "you accepted": the record is the clone's, so the reading beside that hash
			// may have been taken in another worktree. The two hashes are still both printed, which is what lets a
			// reader see that content moved rather than being told something is missing.
			a.warn("tracked hooks CHANGED since this clone recorded them (recorded %s, the tree is now %s): they will not run until `git pair hooks --trust` records them\n", rep.Accepted, rep.TreeHash)
		case hooks.TrustDeclined:
			a.warn("tracked hooks DECLINED for this clone (declined %s): they will not run. `git pair hooks --trust` runs them after you read them\n", rep.Declined)
		default:
			a.warn("tracked hooks are NOT trusted: they will not run until `git pair hooks --trust` records them, or `--decline` records that you read them and chose not to run them\n")
		}
	}
	for _, h := range rep.Hooks {
		notes := []string{}
		if !h.Executable {
			notes = append(notes, "not executable")
		}
		suffix := ""
		if len(notes) > 0 {
			suffix = "  (" + strings.Join(notes, "; ") + ")"
		}
		a.printf("%-14s %-7s %s%s\n", h.Point, h.Root, displayPath(h.Path), suffix)
	}
	reportUnknown(a, rep, found)
	return nil
}

// reportUnknown says which entries under a hooks root are not lifecycle points. An entry nobody runs is
// otherwise indistinguishable from no entry, and a team that believes it has a pre-ready suite is the cost
// of staying quiet about it.
func reportUnknown(a *app, rep hooksReport, found []unknownEntry) {
	for _, u := range found {
		a.warn("git-pair: %s is not a lifecycle point; expected one of: %s\n",
			displayPath(filepath.Join(u.dir, u.name)), strings.Join(pointNames(), ", "))
	}
}

// hooksPositionReason is what the commands that create or advance a changeset ask about the repository's own
// scripts: a tracked hooks tree this clone has recorded no position on carries checks that have not run, and
// git-pair does not build on top of that silence.
//
// The reviewer asked for it as early as it can be asked — "it should be obvious as early as possible that there
// are hooks that repo owner wants run" — so it is met at `init`, before a directory exists, at `change ready`,
// before the hand-off, at `review submit`, before the reviewer's content is staged, and at the gate, where
// `check` and `change integrate` refuse the merge. One sentence on every surface, because a person who meets it
// twice should read the same words and the same two ways out, not a different refusal each time.
//
// `accepted` answers it by running the scripts. `declined` answers it differently: a person read them and chose
// not to run them here, which is a position rather than an absence, and is what lets the tool ask for one without
// pushing anyone into approving a script they do not want. What neither is, is silence — so `unrecorded` and
// `changed` both refuse. `changed` refuses because content that moved under an acceptance is content nobody has
// looked at since; letting the older record answer for it would make one acceptance cover the whole history of the
// directory, which is the reading the state machine in `hooks.TrustAt` refuses.
//
// Two commands deliberately do not ask: `change unready` and `change abandon`. Both take work out of the queue
// rather than putting it in, and a person who cannot run the checks is exactly the person who needs to put the
// work down. Refusing them would turn the safety mechanism into a trap.
//
// The off switch is not an answer either. `GIT_PAIR_HOOKS=0` and `git-pair.hooks=false` say a machine does not
// want to run scripts right now, which says nothing about what those scripts do; the position is
// `git pair hooks --decline`, written down where the next reader can see it was taken.
func (a *app) hooksPositionReason(ctx context.Context, repo *git.Repo) (string, error) {
	state, accepted, current, err := a.hookRun(repo).Trust(ctx)
	if err != nil {
		return "", err
	}
	switch state {
	case hooks.TrustAccepted, hooks.TrustDeclined:
		return "", nil
	}
	root := displayPath(hooks.TrackedRootDir(repo.Dir))
	if state == hooks.TrustChanged {
		return fmt.Sprintf("the repository's tracked hooks under %s changed since this clone recorded them (recorded %s, the tree is now %s): the checks they carry have not run on this content — read what moved, then `git pair hooks --trust` to run it or `git pair hooks --decline` to record that you read it and chose not to",
			root, short(accepted), short(current)), nil
	}
	return fmt.Sprintf("the repository's tracked hooks under %s have not been recorded in this clone: the checks they carry have not run — read them, then `git pair hooks --trust` to run them or `git pair hooks --decline` to record that you read them and chose not to",
		root), nil
}

// hooksPositionRefusal is the same question as an error, for the commands that refuse rather than report. It is a
// business refusal (exit 1) like a dirty tree or a failing pre-hook: the invocation was right, and the clone has
// a step to take. Nothing is written before it, so a refusal leaves the repository as it was found.
func (a *app) hooksPositionRefusal(ctx context.Context, repo *git.Repo) error {
	reason, err := a.hooksPositionReason(ctx, repo)
	if err != nil {
		return err
	}
	if reason == "" {
		return nil
	}
	return fmt.Errorf("%s", reason)
}

func acceptHooks(ctx context.Context, a *app, repo *git.Repo, gitDir string) error {
	hash, names, err := hooks.Accept(ctx, repo)
	if err != nil {
		return err
	}
	if a.json {
		return a.emitJSON(map[string]any{"accepted": hash, "files": orEmpty(names)})
	}
	a.printf("Accepting the tracked hooks under %s:\n", displayPath(hooks.TrackedRootDir(repo.Dir)))
	for _, n := range names {
		a.printf("  %s\n", n)
	}
	// The path is printed because it is the file a reader deletes to undo this, and the answer belongs to the
	// clone rather than the worktree: one reading here covers every worktree cut from this repository.
	a.printf("recorded %s for this clone at %s\n", hash, displayPath(hooks.TrustedRecordPath(gitDir)))
	return nil
}

// declineHooks records the position `--trust` does not: read, and not run here. It prints the same list first,
// because a decline about content nobody has looked at is the same formality as an acceptance about it, and a
// formality is how a team ends up declining the check it wanted.
func declineHooks(ctx context.Context, a *app, repo *git.Repo, gitDir string) error {
	hash, names, err := hooks.Decline(ctx, repo)
	if err != nil {
		return err
	}
	if a.json {
		return a.emitJSON(map[string]any{"declined": hash, "files": orEmpty(names)})
	}
	a.printf("Declining the tracked hooks under %s for this clone:\n", displayPath(hooks.TrackedRootDir(repo.Dir)))
	for _, n := range names {
		a.printf("  %s\n", n)
	}
	a.printf("recorded %s for this clone at %s\n", hash, displayPath(hooks.DeclinedRecordPath(gitDir)))
	a.printf("they will not run; a new version the repository commits is unrecorded again, not declined\n")
	return nil
}

func pointNames() []string {
	names := make([]string, 0)
	for _, p := range hooks.AllPoints() {
		names = append(names, p.String())
	}
	return names
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
