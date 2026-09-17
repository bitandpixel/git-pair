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
func (f *Fixture) SpawnShim(t *testing.T) (count func() int) {
	t.Helper()

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

	return func() int {
		data, err := os.ReadFile(log)
		if err != nil {
			if os.IsNotExist(err) {
				return 0
			}
			t.Fatalf("gittest: read spawn log: %v", err)
		}
		n := 0
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			if line != "" {
				n++
			}
		}
		return n
	}
}

// SpawnRepo returns a repo handle whose git subprocesses are counted. The fixture's own calls
// are counted as well once this is called, so measure around the code, not across the test.
func (f *Fixture) SpawnRepo(t *testing.T) (*git.Repo, func() int) {
	t.Helper()
	return &git.Repo{Dir: f.Dir(), Env: f.Env()}, f.SpawnShim(t)
}
