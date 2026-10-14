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
		a.warn("git-pair: %d tracked hook(s) at %s did not run — the tree under %s has not been accepted; "+
			"review it and run `git pair hooks --trust`\n",
			len(plan.SkippedUntrusted()), c.Point, hooks.TrackedRootDir(s.repo.Dir))
	}
	if len(runnable) > 0 {
		if mode == blockingMode {
			results = append(results, hooks.Run(ctx, s.repo, runnable, env)...)
		} else {
			results = append(results, hooks.RunAll(ctx, s.repo, runnable, env)...)
		}
	}
	for _, r := range results {
		a.replayHookOutput(r)
	}
	return results, nil
}

// replayHookOutput keeps hook text off stdout, where `--json` lives. A failing hook's output is what the
// author needs, so it always goes to stderr; a passing hook's output goes there only when asked for, because
// a suite that prints 400 lines on success would make every handoff unreadable.
func (a *app) replayHookOutput(r hooks.Result) {
	if r.Status == hooks.StatusSkippedUntrusted {
		return
	}
	if r.Blocking() || hooks.EchoOutput() {
		if out := strings.TrimRight(r.Stdout, "\n"); out != "" {
			a.warn("%s\n", out)
		}
		if errOut := strings.TrimRight(r.Stderr, "\n"); errOut != "" {
			a.warn("%s\n", errOut)
		}
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
	var trust bool
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
  <gitdir>/git-pair/hooks/   this clone only, never shared, trusted by construction

At each root a point is either one executable file or a directory whose executable entries run in
lexicographic order.

The tracked tree is trusted by content, not by a flag: any edit to any hook invalidates the acceptance,
because the thing that was approved was that content. An unaccepted tree does not run and the command says so
on stderr — the operation itself still proceeds, so a clone nobody has approved is still a clone you can
work in.

Nothing here runs a hook. This command reports configuration; the lifecycle commands run.

Exit codes: 0 reported, 2 usage, 3 git failed.`,
		Example: `  git pair hooks
  git pair hooks --json
  git pair hooks --trust`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHooksReport(cmd.Context(), a, trust)
		},
	}
	cmd.Flags().BoolVar(&trust, "trust", false,
		"record the tracked hooks tree as accepted so its hooks may run; prints what is being approved first")
	return cmd
}

// unknownEntry is an entry under a hooks root whose name is not a lifecycle point, kept with its root
// because the fix differs: a misspelled directory under the tracked root is a file to correct and commit,
// and one under the clone root is a file the reader wrote themselves and can just rename.
type unknownEntry struct {
	name string
	dir  string
}

// hookEntry is one configured hook as this command reports it. `active` says whether this build invokes the
// point at all, which is what makes a hook under a point nobody runs visible rather than silently inert.
type hookEntry struct {
	Point      string `json:"point"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Root       string `json:"root"`
	Executable bool   `json:"executable"`
	Active     bool   `json:"active"`
}

// hooksReport is `git pair hooks --json`.
type hooksReport struct {
	TrackedRoot string `json:"tracked_root"`
	CloneRoot   string `json:"clone_root"`
	Trusted     bool   `json:"trusted"`
	// TrustState says which of the two reasons `trusted: false` carries: `accepted`, `changed`, or
	// `unrecorded`. It is a state rather than a second boolean because the reader's next action differs — one
	// is "read these", the other is "the repository moved these, read what changed".
	TrustState string `json:"trust_state"`
	Accepted   string `json:"accepted,omitempty"`
	// TreeHash is the tracked tree's current hash, carried only when it differs from `Accepted`: a reader
	// comparing the two needs both, and a reader whose hooks are trusted needs neither.
	TreeHash string      `json:"tree_hash,omitempty"`
	Hooks    []hookEntry `json:"hooks"`
	Unknown  []string    `json:"unknown"`
	Disabled string      `json:"disabled_by,omitempty"`
}

func runHooksReport(ctx context.Context, a *app, trust bool) error {
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
	if state == hooks.TrustChanged {
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
				Active:     hooks.Active(point),
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
		// Two states, two sentences. "Never accepted" is a setting nobody has touched; "changed" is the
		// repository shipping a new version of a check this clone had already approved, and it is the case
		// where work goes on with the check not running and nobody having decided that. On stderr, beside the
		// note a run makes when it skips these same hooks: the table is the answer, this is the warning about
		// it, and a script that captures the report should not have the warning inside it.
		if hooks.TrustState(rep.TrustState) == hooks.TrustChanged {
			a.warn("tracked hooks CHANGED since you accepted them (accepted %s, the tree is now %s): they will not run until `git pair hooks --trust` records them\n", rep.Accepted, rep.TreeHash)
		} else {
			a.warn("tracked hooks are NOT trusted: they will not run until `git pair hooks --trust` records them\n")
		}
	}
	for _, h := range rep.Hooks {
		notes := []string{}
		if !h.Executable {
			notes = append(notes, "not executable")
		}
		if !h.Active {
			notes = append(notes, "point not run by this build")
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
	a.printf("recorded %s at %s\n", hash, displayPath(filepath.Join(gitDir, "git-pair", "trusted-hooks")))
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
