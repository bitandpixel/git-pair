package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/git"
)

// SpawnRepo returns a repo handle whose git subprocesses are counted, and a function
// reporting how many have been counted so far.
//
// Some costs are properties of the implementation rather than of its answers: a resolver
// that asks one question per changeset ref behaves identically on every fixture and takes
// minutes in a repository with a year of landed changesets. Counting invocations is the
// only way to assert that, and the count comes from a shim earlier on PATH than git, so the
// code under test is not modified to be observable.
//
// Only calls made through the returned handle are counted; the fixture's own setup calls run
// outside it on purpose, so a test measures the code and not its scaffolding.
func (f *Fixture) SpawnRepo(t *testing.T) (*git.Repo, func() int) {
	t.Helper()

	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("gittest: no git on PATH to count around: %v", err)
	}
	sim := t.TempDir()
	log := filepath.Join(sim, "spawns")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >>\"" + log + "\"\nexec \"" + real + "\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(sim, "git"), []byte(script), 0o755); err != nil {
		t.Fatalf("gittest: write spawn shim: %v", err)
	}

	env := append([]string{}, f.Env()...)
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			env[i] = "PATH=" + sim + string(os.PathListSeparator) + strings.TrimPrefix(kv, "PATH=")
		}
	}
	env = append(env, "GITPAIR_SPAWN_LOG="+log)

	repo := &git.Repo{Dir: f.Dir(), Env: env}
	count := func() int {
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
	return repo, count
}
