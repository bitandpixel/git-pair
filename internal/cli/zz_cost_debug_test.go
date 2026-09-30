package cli_test

import (
	"os"
	"gitpair/internal/gittest"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestZZCostTrace(t *testing.T) {
	ids := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	f := newRepo(t)
	for i, id := range ids {
		if i == 0 {
			f.CreateBranch(id)
			f.CommitChangeset(id, "main")
			f.Commit(id+" work", gittest.WithFile(id+".go", "package main\n"))
		} else {
			stackedChangeset(t, f, id, ids[i-1], ids[i-1], id+".go")
		}
		landAndRecord(t, f, id, "main")
	}
	real, _ := exec.LookPath("git")
	dir := t.TempDir()
	log := filepath.Join(dir, "spawns")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >>\"" + log + "\"\nexec \"" + real + "\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	runIn(t, f.Dir(), "status", "--changeset", "epsilon").mustSucceed(t, "status")
	data, _ := os.ReadFile(log)
	counts := map[string]int{}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		key := strings.Join(fields[:min(3, len(fields))], " ")
		counts[key]++
	}
	type kv struct {
		k string
		n int
	}
	var out []kv
	for k, n := range counts {
		out = append(out, kv{k, n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].n > out[j].n })
	t.Logf("total=%d", len(strings.Split(strings.TrimRight(string(data), "\n"), "\n")))
	for i, e := range out {
		if i > 14 {
			break
		}
		t.Logf("%4d  %s", e.n, e.k)
	}
}

func minzz(a, b int) int { if a < b { return a }; return b }
