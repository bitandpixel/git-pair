package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpr/internal/changeset"
	"gitpr/internal/cli"
	"gitpr/internal/gittest"
)

// Exit codes are the agent-facing contract (plan: "Stable for agents"). They are
// redeclared here on purpose: cli.Execute's return value is the contract, and a test
// that reused the implementation's constants could not notice a change in meaning.
const (
	exitOK      = 0
	exitRefusal = 1
	exitUsage   = 2
	exitGit     = 3
)

// stdio holds the process streams as the test binary started, so a CLI run can put
// files where an editor, pager or difftool would otherwise reach a terminal and
// block. Nothing in this suite may depend on a TTY.
var stdio = struct {
	stdout, stderr, stdin *os.File
}{stdout: os.Stdout, stderr: os.Stderr, stdin: os.Stdin}

// result is one `gitpr` invocation.
type result struct {
	code   int
	stdout string
	stderr string
}

// json decodes the command's --json output.
func (r result) json(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatalf("output is not a JSON object: %v\n%s", err, r.stdout)
	}
	return out
}

// jsonList decodes a named array field, accepting null as "empty".
func (r result) jsonList(t *testing.T, key string) []any {
	t.Helper()
	value, ok := r.json(t)[key]
	if !ok {
		t.Fatalf("JSON output has no %q key: %v", key, r.json(t))
	}
	if value == nil {
		return nil
	}
	list, ok := value.([]any)
	if !ok {
		t.Fatalf("JSON %q = %T, want an array", key, value)
	}
	return list
}

func (r result) mustSucceed(t *testing.T, args ...string) result {
	t.Helper()
	if r.code != exitOK {
		t.Fatalf("gitpr %s exited %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.code, r.stdout, r.stderr)
	}
	return r
}

// run executes `gitpr` with the given arguments in the current working directory.
//
// The output streams are regular files, and stdin is /dev/null: console.Interactive()
// is therefore false, so the editor/difftool-backed commands refuse instead of
// hanging, and git's own output (which `gitpr diff` delegates to) still lands in the
// captured stdout.
func run(t *testing.T, args ...string) result {
	t.Helper()

	dir := t.TempDir()
	outPath := filepath.Join(dir, "stdout")
	errPath := filepath.Join(dir, "stderr")
	stdout, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("create stdout capture: %v", err)
	}
	stderr, err := os.Create(errPath)
	if err != nil {
		t.Fatalf("create stderr capture: %v", err)
	}
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}

	os.Stdout, os.Stderr, os.Stdin = stdout, stderr, stdin
	defer func() {
		os.Stdout, os.Stderr, os.Stdin = stdio.stdout, stdio.stderr, stdio.stdin
	}()

	code := cli.Execute(args)

	stdout.Close()
	stderr.Close()
	stdin.Close()

	out, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read stdout capture: %v", err)
	}
	errs, err := os.ReadFile(errPath)
	if err != nil {
		t.Fatalf("read stderr capture: %v", err)
	}
	return result{code: code, stdout: string(out), stderr: string(errs)}
}

// runIn runs the command with dir as the process working directory.
func runIn(t *testing.T, dir string, args ...string) result {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	defer func() {
		if err := os.Chdir(old); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()
	return run(t, args...)
}

// --- scenario builders ------------------------------------------------------

// newRepo returns a repository with one commit on branch "main".
func newRepo(t *testing.T) *gittest.Fixture {
	t.Helper()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n\nfunc main() {}\n"))
	return f
}

// newChangeset builds a repository whose branch has a committed changeset directory
// and one implementation commit, ready for `gitpr change ready`.
//
// The scaffolding is written by the fixture rather than by `gitpr change init` so
// that a broken init only fails the tests that exercise it. change_test.go covers
// the product's own scaffolding.
func newChangeset(t *testing.T, branch, base string) (*gittest.Fixture, string) {
	t.Helper()
	f := newRepo(t)
	slug, err := changeset.SlugFromBranch(branch)
	if err != nil {
		t.Fatalf("SlugFromBranch(%q): %v", branch, err)
	}
	f.CreateBranch(branch)
	f.CommitChangeset(slug, base)
	// Two implementation files exist up front so that a review commit adding a
	// comment to one of them adds exactly one line.
	f.Commit("implement locking", gittest.WithFiles(map[string]string{
		"service.go": "package main\n\nfunc Lock() {}\n",
		"handler.go": "package main\n\nfunc Serve() {}\n",
	}))
	if !f.Clean() {
		t.Fatal("fixture changeset did not start with a clean working tree")
	}
	return f, slug
}

// ready marks the checked-out changeset ready through the CLI.
func ready(t *testing.T, f *gittest.Fixture) result {
	t.Helper()
	res := runIn(t, f.Dir(), "change", "ready")
	return res.mustSucceed(t, "change", "ready")
}

// submit records a review submission through the CLI.
func submit(t *testing.T, f *gittest.Fixture, outcome string) result {
	t.Helper()
	return runIn(t, f.Dir(), "review", "submit", "--"+outcome).mustSucceed(t, "review", "submit", "--"+outcome)
}

// reviewRef is the movable review ref for a changeset.
func reviewRef(slug string) string { return "refs/reviews/" + slug }

func archivePattern(slug string) string { return "refs/reviews/archive/" + slug }

func mustContain(t *testing.T, haystack, needle, what string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("%s does not contain %q:\n%s", what, needle, haystack)
	}
}

func mustNotContain(t *testing.T, haystack, needle, what string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("%s unexpectedly contains %q:\n%s", what, needle, haystack)
	}
}

func describe(t *testing.T, res result, args ...string) {
	t.Helper()
	t.Logf("gitpr %s -> %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), res.code, res.stdout, res.stderr)
}
