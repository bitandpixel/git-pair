package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/git"
)

// SpawnShim puts a logging `git` ahead of the real one on PATH and returns a function
// reporting how many invocations have been counted since it was called.
//
// Some costs are properties of the implementation rather than of its answers: a resolver that
// asks one question per changeset ref behaves identically on every fixture and takes minutes in
// a repository with a year of landed changesets. Counting invocations is the only way to assert
// that, and the count comes from outside the code under test, so nothing is instrumented to be
// measured.
//
// PATH is rewritten for the whole test process, because that is how a program finds `git`; the
// test's own fixture calls go through the shim too, so a measurement is a delta taken around the
// thing being measured rather than a total. Everything created before the shim is called is
// outside the count by construction.
//
// Calling it also clears the repository's derived-fact cache, so a measurement is a cold one. A warm
// cache answers those questions from disk without spawning git at all, and a counter that reports a
// handful of invocations for a run that did the work would read as a bound having been met when it was
// only been hidden. The guarantee lives with the instrument, because a test that forgot it would fail in
// the wrong direction: it would pass.
func (f *Fixture) SpawnShim(t *testing.T) (count func() int) {
	t.Helper()
	count, _ = f.spawnShim(t)
	return count
}

// SpawnShimLines counts git invocations and returns them in the order they ran, for the assertions
// that are about which reads happened rather than how many. Counting alone cannot tell a command
// that skipped a read from one that made it and did not need it.
func (f *Fixture) SpawnShimLines(t *testing.T) (count func() int, lines func() []string) {
	t.Helper()
	return f.spawnShim(t)
}

func (f *Fixture) spawnShim(t *testing.T) (count func() int, lines func() []string) {
	f.clearFactCache()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("gittest: no git on PATH to count around: %v", err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "spawns")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >>\"" + log + "\"\nexec \"" + real + "\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatalf("gittest: write spawn shim: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	read := func() (string, error) {
		data, err := os.ReadFile(log)
		if err != nil {
			if os.IsNotExist(err) {
				return "", nil
			}
			t.Fatalf("gittest: read spawn log: %v", err)
		}
		return string(data), nil
	}
	count = func() int {
		data, err := read()
		if err != nil {
			t.Fatalf("gittest: read spawn log: %v", err)
		}
		n := 0
		for _, line := range strings.Split(strings.TrimRight(data, "\n"), "\n") {
			if line != "" {
				n++
			}
		}
		return n
	}
	lines = func() []string {
		data, err := read()
		if err != nil {
			t.Fatalf("gittest: read spawn log: %v", err)
		}
		var out []string
		for _, line := range strings.Split(strings.TrimRight(data, "\n"), "\n") {
			if line != "" {
				out = append(out, line)
			}
		}
		return out
	}
	return count, lines
}

// SpawnRepo returns a repo handle whose git subprocesses are counted. The fixture's own calls
// are counted as well once this is called, so measure around the code, not across the test.
func (f *Fixture) SpawnRepo(t *testing.T) (*git.Repo, func() int) {
	t.Helper()
	return &git.Repo{Dir: f.Dir(), Env: f.Env()}, f.SpawnShim(t)
}

// clearFactCache removes the derived-fact cache from this repository's git directory.
//
// It is best-effort by design: a fixture with no git directory, or no cache yet, has nothing to clear,
// and a test that measures cost should not fail because the thing it is clearing was never written.
func (f *Fixture) clearFactCache() {
	f.t.Helper()
	out, err := f.Git("rev-parse", "--absolute-git-dir")
	if err != nil {
		return
	}
	gitDir := strings.TrimSpace(out)
	if gitDir == "" {
		return
	}
	if err := os.RemoveAll(filepath.Join(gitDir, "git-pair", "cache")); err != nil {
		f.t.Fatalf("gittest: clear fact cache: %v", err)
	}
}
