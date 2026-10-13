// Package hooks runs repository-configured scripts at git-pair's lifecycle points.
//
// The mechanism is client control. git-pair publishes an event — "this changeset is being marked ready" —
// and the client decides what that event triggers: a test suite, a lint pass, a validation, a notification.
// git-pair supplies the moment and the context, and owns nothing about what the script does. That division is
// what keeps the feature inside PRD §26: every git verb a hook runs is invoked by the client's own script,
// and git-pair itself still invokes none.
//
// Two roots are searched, in this order:
//
//	<worktree>/.git-pair/hooks/<point>   tracked, shared, must be trusted
//	<gitdir>/git-pair/hooks/<point>      per-clone, never shared, trusted by construction
//
// The second root is what makes this a client-control feature rather than repository policy: a reviewer
// attaches their own checks to a repository they may not edit. It follows git's own division, where
// `.git/hooks` is yours and `core.hooksPath` is somebody's choice.
//
// At each root `<point>` is either an executable file or a directory whose executable entries run in
// lexicographic order. One rule covers the single-script case and the several-scripts case without a config
// format, and `parseMetadata` (`internal/changeset`) is the precedent for not adding one.
package hooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gitpair/internal/git"
)

// Point names a lifecycle event a hook can answer. The names are the existing lifecycle vocabulary — the
// marker kinds in `internal/lifecycle` plus `check` and the reviewer's submission — because a hook's name
// should be the event it answers, the way git names `pre-commit` for the commit it precedes.
type Point string

const (
	PreReady      Point = "pre-ready"
	PostReady     Point = "post-ready"
	PreIntegrate  Point = "pre-integrate"
	PostIntegrate Point = "post-integrate"
	PreUnready    Point = "pre-unready"
	PostUnready   Point = "post-unready"
	PreAbandon    Point = "pre-abandon"
	PostAbandon   Point = "post-abandon"
	PreCheck      Point = "pre-check"
	PostCheck     Point = "post-check"
	PreSubmit     Point = "pre-submit"
	PostSubmit    Point = "post-submit"
)

// allPoints is the whole vocabulary, including points no command invokes yet. `git pair hooks` reports a hook
// configured under an inactive point rather than ignoring it: a typo'd hook is otherwise
// indistinguishable from an absent one, which is how a team ends up believing it has validation it does not run.
var allPoints = []Point{
	PreReady, PostReady,
	PreIntegrate, PostIntegrate,
	PreUnready, PostUnready,
	PreAbandon, PostAbandon,
	PreCheck, PostCheck,
	PreSubmit, PostSubmit,
}

// AllPoints is the vocabulary in the order the lifecycle walks it.
func AllPoints() []Point { return append([]Point(nil), allPoints...) }

// String lets a Point be formatted into messages and environment values.
func (p Point) String() string { return string(p) }

// active is the set of points some command in this build invokes. It grows as the milestones land; it is
// kept here rather than inferred from call sites so that `git pair hooks` can say, in one place, which
// configured hooks this build will actually run.
var active = map[Point]bool{PreReady: true}

// Active reports whether this build invokes point.
func Active(p Point) bool { return active[p] }

// RootKind says which of the two roots a hook came from, and travels to the client as GIT_PAIR_HOOK_ROOT
// and as the `root` field of the JSON report.
type RootKind string

const (
	// RootTracked is `<worktree>/.git-pair/hooks`: shared by committing it, and therefore code from a
	// stranger, so it runs only once a person has recorded acceptance.
	RootTracked RootKind = "worktree"
	// RootClone is `<gitdir>/git-pair/hooks`: the person's own file under their own git directory, trusted
	// by construction. Requiring consent for it would be consent to oneself.
	RootClone RootKind = "clone"
)

// Hook is one configured script.
type Hook struct {
	Point Point    `json:"point"`
	Name  string   `json:"name"`
	Path  string   `json:"path"`
	Root  RootKind `json:"root"`
}

// Status is what became of one hook. Every value is a distinct answer a reviewer or an agent branches on,
// including the two that mean "nothing ran": a hook that was skipped for trust is not a hook that passed.
type Status string

const (
	StatusPassed           Status = "passed"
	StatusFailed           Status = "failed"
	StatusTimeout          Status = "timeout"
	StatusSkippedUntrusted Status = "skipped-untrusted"
	StatusNotExecutable    Status = "not-executable"
)

// Result is one hook's outcome. Stdout and Stderr are captured rather than inherited, because a hook's
// output must never reach git-pair's stdout: `--json` is a machine contract and it has to parse end to end.
// The captured text is replayed to stderr when the hook failed.
type Result struct {
	Hook       Hook   `json:"hook"`
	Status     Status `json:"status"`
	Exit       int    `json:"exit"`
	DurationMS int64  `json:"duration_ms"`
	Stdout     string `json:"-"`
	Stderr     string `json:"-"`
}

// Blocking reports whether this outcome refuses the operation. Only a passed hook is not a refusal — except
// StatusSkippedUntrusted, which is not a refusal either: the operation proceeds in a clone nobody has
// approved yet, and the run says so on stderr. That asymmetry is the same one `--configure-fetch` uses for
// consent.
func (r Result) Blocking() bool {
	switch r.Status {
	case StatusPassed, StatusSkippedUntrusted:
		return false
	}
	return true
}

// TrackedRootDir is the shared, committed hooks directory. It sits beside `.git-pair/about-template.md`,
// which is the repository's other piece of tracked configuration.
func TrackedRootDir(workTree string) string {
	return filepath.Join(workTree, ".git-pair", "hooks")
}

// CloneRootDir is the per-clone hooks directory, under the git directory beside the other two stores
// (`<gitdir>/git-pair/cache`, `<gitdir>/git-pair/marks/<slug>`).
func CloneRootDir(gitDir string) string {
	return filepath.Join(gitDir, "git-pair", "hooks")
}

// Plan is what discovery found for one point.
type Plan struct {
	Hooks []Hook
	// TrackedTrusted is false when the tracked hooks tree has changed since it was accepted, or was never
	// accepted. The tracked hooks are then reported as skipped rather than run.
	TrackedTrusted bool
	// UnknownPoints names entries under `.git-pair/hooks/` that are not part of the vocabulary — the report
	// surface for a typo.
	UnknownPoints []string
}

// Discover finds the hooks configured for point, in run order: the tracked root first, because the
// repository's rules are the shared baseline, then the clone root, where the client's own checks add to them.
func Discover(ctx context.Context, repo *git.Repo, point Point) (Plan, error) {
	var plan Plan
	plan.Hooks = []Hook{}
	plan.UnknownPoints = []string{}

	tracked := TrackedRootDir(repo.Dir)
	trusted, err := Trusted(ctx, repo)
	if err != nil {
		return plan, err
	}
	plan.TrackedTrusted = trusted
	// A clone with no tracked hooks tree needs no acceptance at all, so an absent directory is not an
	// untrusted one: reporting `skipped-untrusted` there would tell a reader to approve something that
	// does not exist.
	if _, err := os.Stat(tracked); errors.Is(err, os.ErrNotExist) {
		plan.TrackedTrusted = true
	} else if err != nil {
		return plan, fmt.Errorf("reading %s: %w", tracked, err)
	}

	hooks, err := collect(tracked, point, RootTracked)
	if err != nil {
		return plan, err
	}
	plan.Hooks = append(plan.Hooks, hooks...)
	plan.UnknownPoints = append(plan.UnknownPoints, UnknownPointsIn(tracked)...)

	gitDir, err := repo.GitDir(ctx)
	if err != nil {
		// Without the git directory there is no per-clone root to search, and no reason to fail a command
		// for it: the tracked root was already read, and it is the one that carries risk.
		return plan, nil
	}
	hooks, err = collect(CloneRootDir(gitDir), point, RootClone)
	if err != nil {
		return plan, err
	}
	plan.Hooks = append(plan.Hooks, hooks...)
	plan.UnknownPoints = append(plan.UnknownPoints, UnknownPointsIn(CloneRootDir(gitDir))...)
	return plan, nil
}

// collect reads one root for one point. A directory holds one hook per entry, in lexicographic order; a file
// is the single-hook form. Entries a reader would not call a hook — a directory, a dot-file, git's own
// `*.sample` convention — are passed over; everything else has to be executable, because a hook nobody made
// executable is the most likely way a repository ends up believing it runs a check it never runs.
func collect(rootDir string, point Point, kind RootKind) ([]Hook, error) {
	target := filepath.Join(rootDir, point.String())
	info, err := os.Stat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", target, err)
	}
	if !info.IsDir() {
		// The single-script form. The name is the point rather than the path, so a report reads the same for
		// both forms: one event, one hook, named for the event it answers.
		return []Hook{{Point: point, Name: point.String(), Path: target, Root: kind}}, nil
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", target, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ignored(e.Name()) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	hooks := make([]Hook, 0, len(names))
	for _, n := range names {
		p := filepath.Join(target, n)
		hooks = append(hooks, Hook{Point: point, Name: n, Path: p, Root: kind})
	}
	return hooks, nil
}

// ignored names the entries a hooks directory may hold that are not themselves hooks.
func ignored(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".sample") ||
		strings.HasSuffix(name, ".md") || strings.HasSuffix(name, "~")
}

// UnknownPointsIn lists entries directly under a root whose name is not one of the points. `git pair hooks`
// reports them; discovery ignores them, because a point nobody runs must not fail a command by existing.
func UnknownPointsIn(rootDir string) []string {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return []string{}
	}
	known := map[Point]bool{}
	for _, p := range allPoints {
		known[Point(p)] = true
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		// The rule that decides what is not a hook decides what is not a misspelling either, so a
		// note or a template beside the point directories is silent in both places rather than
		// noisy in one.
		if ignored(name) || known[Point(name)] {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Disabled reports whether this run must run no hook at all, and why. Two switches: the environment, which a
// hook's own children inherit so that a hook calling `git pair` cannot re-enter, and git config, which is
// per-clone and is the switch for a person whose machine cannot run the repository's hooks today.
func Disabled(ctx context.Context, repo *git.Repo) (bool, string) {
	if v := os.Getenv(EnvDisable); v != "" && v != "1" && strings.ToLower(v) != "true" {
		return true, "$" + EnvDisable
	}
	if repo != nil {
		if v, err := repo.Git(ctx, "config", "--bool", "git-pair.hooks"); err == nil {
			if strings.EqualFold(strings.TrimSpace(v), "false") {
				return true, "git-pair.hooks"
			}
		}
	}
	return false, ""
}

// EnvDisable is the environment form of the off switch. A hook that shells out to `git pair` inherits it,
// which is the recursion guard: a `pre-check` hook running the gate must not fire the gate's hooks.
const EnvDisable = "GIT_PAIR_HOOKS"

// EchoOutputEnv asks for every hook's output to be replayed to stderr, not only a failing hook's. It exists
// because a passing suite that printed the interesting warning is the one whose output people want.
const EchoOutputEnv = "GIT_PAIR_HOOK_OUTPUT"

// TimeoutEnv overrides the per-hook deadline in seconds, for a machine where the default is wrong.
const TimeoutEnv = "GIT_PAIR_HOOK_TIMEOUT"

// DefaultTimeout bounds one hook. It has to be generous enough for a full suite and finite so that a script
// that waits on something never returns leaves an author unable to hand off.
const DefaultTimeout = 10 * time.Minute

// timeout resolves the deadline from the environment, then git config, then the default. An unparsable value
// falls back rather than refusing, because the value is a bound on a script and not a fact about the
// repository — the worst a wrong value can do is wait too long or too briefly.
func timeout(ctx context.Context, repo *git.Repo) time.Duration {
	if v := os.Getenv(TimeoutEnv); v != "" {
		if d, err := parseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	if repo != nil {
		if v, err := repo.Git(ctx, "config", "--get", "git-pair.hookTimeout"); err == nil {
			if d, err2 := parseDuration(strings.TrimSpace(v)); err2 == nil && d > 0 {
				return d
			}
		}
	}
	return DefaultTimeout
}

func parseDuration(v string) (time.Duration, error) {
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	return time.ParseDuration(v)
}

// Run executes hooks in order and stops at the first that does not pass. A suite that fails at minute one
// should not be followed by three more suites, and the first failure is the one worth reading.
//
// The environment is the process environment plus the GIT_PAIR_* context the script cannot derive for
// itself: which event fired, and what the changeset's resolution was. Values arrive in env so the script
// never has to re-derive the changeset through the ambiguity rules this repository already implements.
func Run(ctx context.Context, repo *git.Repo, hooks []Hook, env map[string]string) []Result {
	results := make([]Result, 0, len(hooks))
	limit := timeout(ctx, repo)
	for _, h := range hooks {
		r := runOne(ctx, repo, h, env, limit)
		results = append(results, r)
		if r.Blocking() {
			break
		}
	}
	return results
}

// runOne spawns one hook.
//
// The spawn is argv-exact, with no shell in between: the file is execved as configured, and any shell a
// script wants is its own shebang. `internal/console` carries the cautionary tale for the alternative — its
// editor path expands a user-supplied value through `eval`, and its comment records how a reviewed path
// containing `$`, a backtick, or a space is read as shell rather than passed through.
func runOne(ctx context.Context, repo *git.Repo, h Hook, env map[string]string, limit time.Duration) Result {
	info, err := os.Stat(h.Path)
	if err != nil {
		return Result{Hook: h, Status: StatusFailed, Exit: -1, Stderr: fmt.Sprintf("stat: %v", err)}
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return Result{Hook: h, Status: StatusNotExecutable}
	}

	hctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	cmd := exec.CommandContext(hctx, h.Path)
	cmd.Dir = repo.Dir
	// The root travels with the hook: a reviewer who set a check in their own clone, and a teammate reading
	// the same failure, need to know which of the two roots produced it.
	cmd.Env = append(os.Environ(), render(env, "GIT_PAIR_HOOK_ROOT", string(h.Root))...)
	// A hook gets no terminal on stdin. Agents run the commands that reach here, and a script that reads
	// stdin would otherwise wait for a person who is not watching that terminal.
	cmd.Stdin = nil
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	started := time.Now()
	err = cmd.Run()
	elapsed := time.Since(started).Milliseconds()

	r := Result{Hook: h, Stdout: stdout.String(), Stderr: stderr.String(), DurationMS: elapsed}
	switch {
	case hctx.Err() == context.DeadlineExceeded:
		r.Status, r.Exit = StatusTimeout, -1
		if r.Stderr == "" {
			r.Stderr = fmt.Sprintf("hook exceeded %s", limit)
		}
	case err == nil:
		r.Status, r.Exit = StatusPassed, 0
	default:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			r.Exit = ee.ExitCode()
		} else {
			// Not a nonzero exit: the exec itself failed, which the message has to carry.
			r.Exit = -1
			if r.Stderr == "" {
				r.Stderr = err.Error()
			}
		}
		r.Status = StatusFailed
	}
	return r
}

// render turns the context map into KEY=VALUE pairs for one hook: the caller's values sorted for a
// reproducible run, then the per-hook value last so the caller's map cannot overwrite which root produced it.
func render(env map[string]string, extraKey, extraValue string) []string {
	out := RenderEnv(env)
	for i, kv := range out {
		if strings.HasPrefix(kv, extraKey+"=") {
			out = append(out[:i], out[i+1:]...)
			break
		}
	}
	return append(out, extraKey+"="+extraValue)
}

// RenderEnv is the environment contract as a subprocess would receive it: sorted, so a run is reproducible
// in a test and comparable between two runs. It is exported for the one caller that is not running a hook —
// the marker commit, which carries the same variables so git's own hooks can read them.
func RenderEnv(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

// EchoOutput reports whether every hook's captured output should be replayed, not just a failing one's.
func EchoOutput() bool { return os.Getenv(EchoOutputEnv) != "" }

// SkippedUntrusted reports the plan's tracked hooks as outcomes that stopped them running, so a refusal and
// a report can name what did not happen rather than imply it from an absence. Call it only where the tracked
// tree is known to be unaccepted: the clone's own hooks are never a consent problem, and listing them here
// would tell a person to review a file they wrote themselves.
func (p Plan) SkippedUntrusted() []Result {
	out := []Result{}
	for _, h := range p.Hooks {
		if h.Root == RootTracked {
			out = append(out, Result{Hook: h, Status: StatusSkippedUntrusted})
		}
	}
	return out
}
