package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The point of the whole feature, in one sentence: a repository can commit a script and have it run at the
// moment an author hands off, which is the moment nothing else verifies anything. The built-in checks can
// only ask what git already knows — a clean tree, an existing ABOUT.md, a standing approval. These tests are
// the contract that makes that useful rather than dangerous: a hook that fails must refuse with the exit
// code that means "the repository said no", must write no marker, and must keep its own noise out of the
// machine-readable verdict.
//
// Two fixture disciplines worth naming. A tracked hook is committed, because that is what tracked means and
// an uncommitted hook would fail the clean-tree check for the reason it exists. And a hook's side effects go
// to a scratch directory rather than into the work tree, because a hook that leaves files behind is exactly
// the case `change ready` has to refuse.

// putHook writes one hook, executable, and returns its path. A tracked hook is committed, so the tree is
// clean the way a repository holding hooks would be.
func putHook(t *testing.T, f *gittest.Fixture, root, point, name, script string) string {
	t.Helper()
	var dir string
	switch root {
	case "tracked":
		dir = filepath.Join(f.Dir(), ".git-pair", "hooks", point)
	case "clone":
		gitDir := strings.TrimSpace(f.MustGit("rev-parse", "--git-dir"))
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(f.Dir(), gitDir)
		}
		dir = filepath.Join(gitDir, "git-pair", "hooks", point)
	default:
		t.Fatalf("unknown hook root %q", root)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if root == "tracked" {
		f.Commit("add the repository hook " + name)
	}
	return path
}

// scratch is where hooks record that they ran. It is outside the work tree on purpose: a hook leaving files
// in the repository is the case the clean-tree check exists to refuse, so the happy path must not do it.
func scratch(t *testing.T) string { return filepath.ToSlash(t.TempDir()) }

// touch writes a marker file into scratch under the given name.
func touch(where, name string) string {
	return fmt.Sprintf("#!/bin/sh\ntouch %s\nexit 0\n", strconv.Quote(filepath.Join(where, name)))
}

const passHook = "#!/bin/sh\nexit 0\n"

func failHook(message string) string {
	return fmt.Sprintf("#!/bin/sh\necho %s >&2\nexit 1\n", strconv.Quote(message))
}

// TestPreReadyHookRunsAndBlocksTheHandoff is the pair that defines the feature: a passing hook lets the
// handoff through and is reported, and a failing one refuses it, writes nothing, and is refused with the
// exit code that means the repository said no rather than the one that means git broke.
func TestPreReadyHookRunsAndBlocksTheHandoff(t *testing.T) {
	t.Run("a passing hook lets the handoff through and is reported", func(t *testing.T) {
		f, slug := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-ready", "01-suite", passHook)
		trustHooks(t, f)

		res := runIn(t, f.Dir(), "change", "ready", "--json").mustSucceed(t, "change", "ready")

		if !strings.Contains(res.stdout, `"state": "READY"`) {
			t.Errorf("state is not READY with a passing hook: %s", res.stdout)
		}
		if subject := f.Subject(f.Head()); subject != "git-pair: ready "+slug {
			t.Errorf("the marker was not written with a passing hook: head is %q", subject)
		}
		entries := res.jsonList(t, "hooks")
		if len(entries) != 1 {
			t.Fatalf("hooks = %v, want one entry", entries)
		}
		entry := entries[0].(map[string]any)
		if entry["status"] != "passed" || entry["name"] != "01-suite" || entry["point"] != "pre-ready" {
			t.Errorf("hook entry = %v, want a passed pre-ready entry named 01-suite", entry)
		}
		if entry["root"] != "worktree" {
			t.Errorf("root = %v, want worktree", entry["root"])
		}
	})

	t.Run("a failing hook refuses and writes no marker", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-ready", "01-suite", failHook("3 tests failed"))
		trustHooks(t, f)

		head := f.Head()
		commits := f.RevListCount("HEAD")
		res := runIn(t, f.Dir(), "change", "ready")

		// Exit 1, not 3. The distinction is the whole agent-facing contract: 1 is "the repository
		// refused, fix the state", 3 is "git itself failed" — and a hook refusal travelling through
		// `git commit`, as a native git hook's would, comes back as 3.
		if res.code != exitRefusal {
			t.Errorf("exit = %d, want %d (a hook refusal is a business refusal, not a git failure)\nstderr: %s",
				res.code, exitRefusal, res.stderr)
		}
		if f.Head() != head {
			t.Error("a refused handoff wrote a commit")
		}
		if got := f.RevListCount("HEAD"); got != commits {
			t.Errorf("HEAD reaches %d commits, want %d", got, commits)
		}
		mustContain(t, res.stderr, "01-suite", "the refusal must name the hook that refused")
		mustContain(t, res.stderr, "pre-ready", "the refusal must name the point")
		mustContain(t, res.stderr, "3 tests failed", "a failing hook's output must reach the author")

		if queueListsChangeset(t, runIn(t, f.Dir(), "queue", "--json"), "checkout-flow") {
			t.Error("a refused changeset is in the queue")
		}
	})

	t.Run("hook output never reaches stdout, where --json lives", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-ready", "01-suite", "#!/bin/sh\necho 'BUILD OK 41 tests'\nexit 0\n")
		trustHooks(t, f)

		res := runIn(t, f.Dir(), "change", "ready", "--json").mustSucceed(t, "change", "ready")
		// The proof is that stdout parses as JSON at all: hook text on stdout would make it not
		// parse, and a CI job reading the verdict would read nothing.
		var probe map[string]any
		if err := json.Unmarshal([]byte(res.stdout), &probe); err != nil {
			t.Fatalf("stdout is not JSON: %v\n%s", err, res.stdout)
		}
		if strings.Contains(res.stdout, "BUILD OK") {
			t.Error("hook output reached stdout, where --json lives")
		}
		// A passing hook's output is shown only when asked for: a suite that prints 400 lines
		// would make every hand-off unreadable.
		if strings.Contains(res.stderr, "BUILD OK") {
			t.Error("a passing hook echoed its output although nothing asked for it")
		}

		t.Setenv("GIT_PAIR_HOOK_OUTPUT", "1")
		again := runIn(t, f.Dir(), "change", "ready", "--json").mustSucceed(t, "change", "ready")
		mustContain(t, again.stderr, "BUILD OK", "GIT_PAIR_HOOK_OUTPUT asks for a passing hook's output too")
		if strings.Contains(again.stdout, "BUILD OK") {
			t.Error("echoed output reached stdout even when asked for")
		}
	})
}

// TestPreReadyHookFailureModes covers the ways a hook can go wrong that are not a nonzero exit. Each has to
// be visible: a hook that is silently not executable, or that hangs, is a repository believing it has a
// check it does not run.
func TestPreReadyHookFailureModes(t *testing.T) {
	t.Run("a hook without the executable bit is refused, not skipped", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		path := putHook(t, f, "tracked", "pre-ready", "01-suite", passHook)
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		f.Commit("drop the executable bit")
		trustHooks(t, f)

		res := runIn(t, f.Dir(), "change", "ready")
		if res.code != exitRefusal {
			t.Fatalf("exit = %d, want %d", res.code, exitRefusal)
		}
		mustContain(t, res.stderr, "not executable", "the refusal must say what is wrong")
		mustContain(t, res.stderr, "chmod +x", "and how to fix it")
	})

	t.Run("a hook that outlives its deadline is refused", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-ready", "01-slow", "#!/bin/sh\nsleep 30\nexit 0\n")
		trustHooks(t, f)

		t.Setenv("GIT_PAIR_HOOK_TIMEOUT", "1")
		res := runIn(t, f.Dir(), "change", "ready")
		if res.code != exitRefusal {
			t.Fatalf("exit = %d, want %d", res.code, exitRefusal)
		}
		mustContain(t, res.stderr, "deadline", "the refusal must say it timed out rather than that it failed")
	})

	t.Run("a hook that dirties the tree is refused", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		// A formatting or code-generating hook is the ordinary way this happens.
		putHook(t, f, "tracked", "pre-ready", "01-fmt", "#!/bin/sh\necho generated > generated.go\nexit 0\n")
		trustHooks(t, f)

		res := runIn(t, f.Dir(), "change", "ready")
		if res.code != exitRefusal {
			t.Fatalf("exit = %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
		}
		mustContain(t, res.stderr, "working tree", "the refusal must say the hook changed the tree")
		if subject := f.Subject(f.Head()); strings.HasPrefix(subject, "git-pair: ready") {
			t.Error("a marker was written over files the hook generated and nobody staged")
		}
	})
}

// TestHookTrustGatesTheTrackedRoot is the safety half. A tracked file is code that runs on a clone, which is
// why git keeps its own hooks under `.git/` and refuses to clone them. Trust is by content, so an edit
// after the approval is a new approval; and the per-clone root needs no such step, because it is the
// person's own file under their own git directory.
func TestHookTrustGatesTheTrackedRoot(t *testing.T) {
	t.Run("an unaccepted tracked hook stops the hand-off, and the refusal names both ways out", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		dir := scratch(t)
		putHook(t, f, "tracked", "pre-ready", "01-suite", touch(dir, "RAN"))

		res := runIn(t, f.Dir(), "change", "ready")
		if res.code != exitRefusal {
			t.Fatalf("`change ready` exited %d, want %d\nstdout: %s\nstderr: %s",
				res.code, exitRefusal, res.stdout, res.stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, "RAN")); err == nil {
			t.Error("an unaccepted hook ran: a clone would execute a stranger's script")
		}
		// The hand-off is where a reviewer is asked to spend attention, so it asks here rather than leaving the
		// queue to fill with work whose suite never ran. Both ways out are named because a refusal that names
		// only `--trust` reads as a demand to approve.
		have := res.stdout + res.stderr
		mustContain(t, have, "have not been recorded in this clone", "the refusal says what is missing")
		mustContain(t, have, "git pair hooks --trust", "and the way that runs the checks")
		mustContain(t, have, "git pair hooks --decline", "and the way that does not")
		marker := filepath.Join(f.Dir(), ".git", "git-pair", "changesets", "checkout-flow", "ready")
		if _, err := os.Stat(marker); err == nil {
			t.Error("a refused hand-off still wrote a ready marker")
		}
	})

	t.Run("accepting lets it run, and prints what is being approved", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		dir := scratch(t)
		putHook(t, f, "tracked", "pre-ready", "01-suite", touch(dir, "RAN"))

		res := runIn(t, f.Dir(), "hooks", "--trust").mustSucceed(t, "hooks", "--trust")
		mustContain(t, res.stdout, "01-suite", "--trust must print the tree it is approving")

		runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")
		if _, err := os.Stat(filepath.Join(dir, "RAN")); err != nil {
			t.Error("the accepted hook did not run")
		}
	})

	t.Run("editing a hook invalidates the acceptance, and says it changed rather than never ran", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		dir := scratch(t)
		putHook(t, f, "tracked", "pre-ready", "01-suite", touch(dir, "RAN"))
		trustHooks(t, f)

		// The line that matters: the approved thing was that content, so a line added afterwards
		// is not approved. A flag would approve the directory forever.
		f.Append(filepath.Join(".git-pair", "hooks", "pre-ready", "01-suite"), "echo escalated\n")
		f.Commit("escalate the hook")

		report := runIn(t, f.Dir(), "hooks", "--json").mustSucceed(t, "hooks", "--json").json(t)
		if report["trusted"] != false {
			t.Errorf("trusted = %v after editing a hook, want false", report["trusted"])
		}
		// `trusted: false` alone cannot tell this clone from one where nobody has run `--trust`, which is
		// how a team shipping a new version of a check ends up with the check not running for anybody who
		// pulled, and no signal saying so.
		if report["trust_state"] != "changed" {
			t.Errorf("trust_state = %v, want changed", report["trust_state"])
		}
		accepted, _ := report["accepted"].(string)
		tree, _ := report["tree_hash"].(string)
		if accepted == "" || tree == "" || accepted == tree {
			t.Errorf("accepted=%q tree_hash=%q, want both hashes and different, so a reader can see what moved", accepted, tree)
		}

		human := runIn(t, f.Dir(), "hooks").mustSucceed(t, "hooks")
		mustContain(t, human.stderr, "CHANGED since you accepted them", "the note has to name the move, not just the absence")
		mustContain(t, human.stderr, accepted, "and show the hash that was approved beside the one that would run")
		mustContain(t, human.stderr, tree, "along with the tree's current hash")
	})

	t.Run("a clone that never accepted anything does not get the changed note", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		dir := scratch(t)
		putHook(t, f, "tracked", "pre-ready", "01-suite", touch(dir, "RAN"))

		report := runIn(t, f.Dir(), "hooks", "--json").mustSucceed(t, "hooks", "--json").json(t)
		if report["trust_state"] != "unrecorded" {
			t.Errorf("trust_state = %v, want unrecorded", report["trust_state"])
		}
		if _, ok := report["accepted"]; ok {
			t.Errorf("accepted = %v, want the field absent when nothing was ever approved", report["accepted"])
		}
		human := runIn(t, f.Dir(), "hooks").mustSucceed(t, "hooks")
		mustContain(t, human.stderr, "NOT trusted", "the first-time sentence stays the plain one")
		if strings.Contains(human.stderr, "CHANGED") {
			t.Errorf("a clone that never accepted anything was told its hooks changed:\n%s", human.stderr)
		}
	})

	t.Run("a per-clone hook runs with no acceptance", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		dir := scratch(t)
		putHook(t, f, "clone", "pre-ready", "mine", touch(dir, "RAN_CLONE"))

		runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")

		if _, err := os.Stat(filepath.Join(dir, "RAN_CLONE")); err != nil {
			t.Error("the per-clone hook did not run: consent to one's own file is not consent")
		}
	})

	t.Run("tracked hooks run before the clone's own", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		dir := scratch(t)
		order := filepath.Join(dir, "ORDER")
		putHook(t, f, "tracked", "pre-ready", "01-repo",
			fmt.Sprintf("#!/bin/sh\necho repo >> %s\nexit 0\n", strconv.Quote(order)))
		putHook(t, f, "clone", "pre-ready", "02-mine",
			fmt.Sprintf("#!/bin/sh\necho clone >> %s\nexit 0\n", strconv.Quote(order)))
		trustHooks(t, f)

		runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")

		got := strings.Fields(strings.TrimSpace(readFile(t, order)))
		if len(got) != 2 || got[0] != "repo" || got[1] != "clone" {
			t.Errorf("order = %v, want [repo clone]: the repository's rules are the baseline, the client adds to them", got)
		}
	})

	t.Run("several hooks at one point run in lexicographic order and stop at the first failure", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		dir := scratch(t)
		order := filepath.Join(dir, "ORDER")
		putHook(t, f, "tracked", "pre-ready", "01-first",
			fmt.Sprintf("#!/bin/sh\necho first >> %s\nexit 0\n", strconv.Quote(order)))
		putHook(t, f, "tracked", "pre-ready", "02-second", failHook("stopped here"))
		putHook(t, f, "tracked", "pre-ready", "03-third",
			fmt.Sprintf("#!/bin/sh\necho third >> %s\nexit 0\n", strconv.Quote(order)))
		trustHooks(t, f)

		res := runIn(t, f.Dir(), "change", "ready")
		if res.code != exitRefusal {
			t.Fatalf("exit = %d, want %d", res.code, exitRefusal)
		}
		got := strings.Fields(strings.TrimSpace(readFile(t, order)))
		if len(got) != 1 || got[0] != "first" {
			t.Errorf("order = %v, want [first]: a suite that fails at minute one must not be followed by the next", got)
		}
	})
}

// TestHooksSwitches covers the two ways to run nothing: the environment, which is what stops a hook calling
// `git pair` from re-entering, and git config, which is the per-clone switch for a machine that cannot run
// the repository's hooks today.
func TestHooksSwitches(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(t *testing.T, f *gittest.Fixture)
	}{
		{"GIT_PAIR_HOOKS=0 disables", func(t *testing.T, f *gittest.Fixture) { t.Setenv("GIT_PAIR_HOOKS", "0") }},
		{"git-pair.hooks=false disables", func(t *testing.T, f *gittest.Fixture) {
			f.Config("git-pair.hooks", "false")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := newChangeset(t, "checkout-flow", "main")
			dir := scratch(t)
			putHook(t, f, "tracked", "pre-ready", "01-suite", touch(dir, "RAN"))
			trustHooks(t, f)
			tc.set(t, f)

			runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")

			if _, err := os.Stat(filepath.Join(dir, "RAN")); err == nil {
				t.Error("the hook ran although hooks were switched off")
			}
		})
	}

	t.Run("a hook inherits the guard so it cannot re-enter", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		dir := scratch(t)
		// A hook that shells out to `git pair` is the recursion the guard exists for, and the child
		// has to see the variable the parent set.
		putHook(t, f, "tracked", "pre-ready", "01-env",
			fmt.Sprintf("#!/bin/sh\nprintf '%%s' \"${GIT_PAIR_HOOKS:-unset}\" > %s\nexit 0\n",
				strconv.Quote(filepath.Join(dir, "GUARD"))))
		trustHooks(t, f)

		runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")

		if got := readFile(t, filepath.Join(dir, "GUARD")); got != "1" {
			t.Errorf("GIT_PAIR_HOOKS inside the hook = %q, want 1", got)
		}
	})
}

// TestHookReceivesTheResolution is the reason the context is handed over rather than derived: the rules for
// "which changeset is this" live in this binary, and a script that re-implements them gets a second,
// different answer.
func TestHookReceivesTheResolution(t *testing.T) {
	f, slug := newChangeset(t, "checkout-flow", "main")
	head := f.Head()
	dir := scratch(t)
	putHook(t, f, "clone", "pre-ready", "01-env", strings.Join([]string{
		"#!/bin/sh",
		"{",
		`printf 'point=%s\n' "$GIT_PAIR_HOOK_POINT"`,
		`printf 'slug=%s\n' "$GIT_PAIR_CHANGESET"`,
		`printf 'branch=%s\n' "$GIT_PAIR_BRANCH"`,
		`printf 'base=%s\n' "$GIT_PAIR_BASE"`,
		`printf 'head=%s\n' "$GIT_PAIR_HEAD"`,
		`printf 'root=%s\n' "$GIT_PAIR_HOOK_ROOT"`,
		fmt.Sprintf("} > %s", strconv.Quote(filepath.Join(dir, "ENV"))),
		`exit 0`,
	}, "\n"))

	runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")

	env := readFile(t, filepath.Join(dir, "ENV"))
	for _, want := range []string{
		"point=pre-ready", "slug=" + slug, "branch=checkout-flow",
		"base=main", "head=" + head, "root=clone",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("hook environment is missing %q, got:\n%s", want, env)
		}
	}
}

// TestHooksCommandReportsConfiguration covers the surface a person uses to find out what is configured —
// which is also the surface that makes a typo visible, because an ignored hook is indistinguishable from an
// absent one.
func TestHooksCommandReportsConfiguration(t *testing.T) {
	t.Run("nothing configured answers with empty arrays, not null", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		res := runIn(t, f.Dir(), "hooks", "--json").mustSucceed(t, "hooks", "--json")
		if got := len(res.jsonList(t, "hooks")); got != 0 {
			t.Errorf("hooks = %d entries, want 0", got)
		}
		res.jsonList(t, "unknown")
		if res.json(t)["trusted"] != true {
			t.Error("a repository with no tracked hooks tree must read as trusted, not as something to approve")
		}
	})

	t.Run("an entry that is not a lifecycle point is reported, not ignored", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-redy", "01-typo", passHook)

		res := runIn(t, f.Dir(), "hooks", "--json").mustSucceed(t, "hooks", "--json")
		unknown := res.jsonList(t, "unknown")
		if len(unknown) != 1 || unknown[0] != "pre-redy" {
			t.Errorf("unknown = %v, want [pre-redy]", unknown)
		}
		human := runIn(t, f.Dir(), "hooks").mustSucceed(t, "hooks")
		mustContain(t, human.stderr, "pre-redy", "the human form must name the misspelled point")
	})

	t.Run("a non-executable hook is reported as one", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		path := putHook(t, f, "tracked", "pre-ready", "01-suite", passHook)
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		f.Commit("drop the executable bit")
		res := runIn(t, f.Dir(), "hooks", "--json").mustSucceed(t, "hooks", "--json")
		entries := res.jsonList(t, "hooks")
		if len(entries) != 1 || entries[0].(map[string]any)["executable"] != false {
			t.Errorf("hooks = %v, want one entry with executable=false", entries)
		}
	})
}

// TestNativeGitHookSeesTheResolution covers why the resolution travels to the marker commit itself and not
// only to git-pair's own hooks. git gives `pre-commit` no arguments at all, and the environment of the commit
// is the only thing it inherits, so a repository that already runs git's own hooks — or a pre-commit framework
// driven by them — would otherwise have to re-derive which changeset it is inside, from rules that live in
// this binary.
func TestNativeGitHookSeesTheResolution(t *testing.T) {
	f, slug := newChangeset(t, "checkout-flow", "main")
	dir := scratch(t)
	f.InstallHook("pre-commit", strings.Join([]string{
		"#!/bin/sh",
		"{",
		`printf 'slug=%s\n' "$GIT_PAIR_CHANGESET"`,
		`printf 'branch=%s\n' "$GIT_PAIR_BRANCH"`,
		`printf 'base=%s\n' "$GIT_PAIR_BASE"`,
		`} > "$OUT"`,
		`exit 0`,
	}, "\n")+"\n")
	// The path travels through the environment rather than being written into the script, so the fixture's
	// hook stays a fixed file and the scratch directory stays the test's business.
	t.Setenv("OUT", filepath.Join(dir, "ENV"))

	runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")

	env := readFile(t, filepath.Join(dir, "ENV"))
	for _, want := range []string{"slug=" + slug, "branch=checkout-flow", "base=main"} {
		if !strings.Contains(env, want) {
			t.Errorf("git's own pre-commit is missing %q, got:\n%s", want, env)
		}
	}
}

// jsonListNamed returns the entries of a `hooks` array whose point or name matches, so a test that puts one
// hook down does not have to assume no other hook directory exists in the fixture.
func jsonListNamed(t *testing.T, res result, key, name string) []any {
	t.Helper()
	var out []any
	for _, e := range res.jsonList(t, key) {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if entry["point"] == name || entry["name"] == name {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s has no entry for %q: %v", key, name, res.jsonList(t, key))
	}
	return out
}

// trustHooks records the tracked hooks tree, the way a person would after reading it.
func trustHooks(t *testing.T, f *gittest.Fixture) {
	t.Helper()
	runIn(t, f.Dir(), "hooks", "--trust").mustSucceed(t, "hooks", "--trust")
}

// declineHooks records the other position a person can take on tracked content: read, and chosen not to run.
func declineHooks(t *testing.T, f *gittest.Fixture) {
	t.Helper()
	runIn(t, f.Dir(), "hooks", "--decline").mustSucceed(t, "hooks", "--decline")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// The behaviour this exists for, at the surface a person with several worktrees actually stands at: read the
// repository's checks once, and every worktree of the clone — including ones cut afterwards — is quiet.
//
// Before the record lived with the clone, each worktree asked the same question about the same bytes, and every
// command that creates or advances work refused until somebody read the same dozen lines again. The tenth reading
// is not a safety check, so the tenth worktree does not get to demand one.
func TestHookTrustIsRecordedForTheWholeClone(t *testing.T) {
	f := newRepo(t)
	putHook(t, f, "tracked", "pre-ready", "01-suite", "#!/bin/sh\nexit 0\n")
	first := filepath.Join(t.TempDir(), "first")
	f.MustGit("worktree", "add", "-q", "-b", "hooks-first", first, "HEAD")

	res := runIn(t, first, "hooks", "--trust").mustSucceed(t, "hooks --trust in a worktree")
	mustContain(t, res.stdout, "for this clone", "the acceptance has to say whose answer it is, not this directory's")

	// The checkout that was never asked. This is the one that used to answer `unrecorded` and refuse.
	main := runIn(t, f.Dir(), "hooks", "--json").mustSucceed(t, "hooks --json in the main checkout")
	mustContain(t, main.stdout, `"trust_state": "accepted"`, "the main checkout sees the position taken in a worktree")
	mustContain(t, main.stdout, `"trusted": true`, "and runs the scripts on the strength of it")

	// A worktree cut after the reading, which is the ordinary shape of the complaint: nobody asks about the
	// first worktree, and every new one used to ask.
	second := filepath.Join(t.TempDir(), "second")
	f.MustGit("worktree", "add", "-q", "-b", "hooks-second", second, "HEAD")
	next := runIn(t, second, "hooks", "--json").mustSucceed(t, "hooks --json in a later worktree")
	mustContain(t, next.stdout, `"trust_state": "accepted"`, "a worktree created after the reading inherits it")

	// The record is git-directory data, so no tree gets dirtied by recording it — and a record that landed in a
	// working tree would be refused by the clean-tree check on the way to a hand-off.
	if out := strings.TrimSpace(f.MustGit("status", "--porcelain")); out != "" {
		t.Errorf("recording the position dirtied the working tree:\n%s", out)
	}
}
