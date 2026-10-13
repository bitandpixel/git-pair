package hooks

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The discovery rules are the interface a team writes against, and they are small enough to enumerate
// completely here. Each case is a name a hook file could plausibly have; the interesting ones are the names
// an editor or a reviewer leaves behind, which must not become code that runs on someone else's clone.
func TestIgnored(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ignored bool
	}{
		{"01-test", false},
		{"run-checks", false},
		{".01-test", true},          // editor backup, and the dot form of a name that would run
		{".gitkeep", true},          // the file that keeps an empty directory in git
		{"01-test.md", true},        // notes beside the hook
		{"01-test.sample", true},    // the template a team commits for people to copy
		{"01-test~", true},          // backup from an editor that does not use dots
		{"README", false},           // no suffix, so it is a script and will refuse with exit 126
		{"01-test.sample.md", true}, // both suffixes at once
	} {
		if got := ignored(tc.name); got != tc.ignored {
			t.Errorf("ignored(%q) = %v, want %v", tc.name, got, tc.ignored)
		}
	}
}

// UnknownPointsIn is the only thing that makes a misspelled point visible. A directory nobody runs is
// otherwise indistinguishable from no directory, and the cost of staying quiet is a team believing its check
// is running.
func TestUnknownPointsIn(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"pre-ready", "pre-redy", "01-test", "notes.md", ".git"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := UnknownPointsIn(dir)
	if !slices.Equal(got, []string{"01-test", "pre-redy"}) {
		t.Errorf("unknown = %v, want [01-test pre-redy]: a real point, a dot-entry, and a note are not misspellings, "+
			"and a script dropped at the root instead of under a point directory is the same class of mistake", got)
	}
	// A missing root is not an error and not a report either. It comes back empty rather than nil
	// because the same slice is what `--json` prints, and an absent array there reads as a broken
	// command rather than as an unconfigured repository.
	if got := UnknownPointsIn(filepath.Join(dir, "no-such-root")); len(got) != 0 {
		t.Errorf("unknown for a missing root = %v, want nothing to report", got)
	}
}

// The point set is the documentation's subject and the switch's subject at the same time, so the two have to
// agree: every point is named, ordered, and known to `Active`.
func TestPointSet(t *testing.T) {
	all := AllPoints()
	if len(all) == 0 {
		t.Fatal("no lifecycle points")
	}
	seen := map[Point]bool{}
	for _, p := range all {
		if seen[p] {
			t.Errorf("point %s listed twice", p)
		}
		seen[p] = true
		if !strings.Contains(p.String(), "-") {
			t.Errorf("point %q is not in <pre|post>-<action> form", p)
		}
		if !strings.HasPrefix(p.String(), "pre-") && !strings.HasPrefix(p.String(), "post-") {
			t.Errorf("point %q does not say which side of the transition it runs on", p)
		}
	}
	// The runner is wired for one point at a time, so this build must claim exactly that one. A point
	// marked active that nothing invokes is the failure `git pair hooks` exists to expose.
	for _, p := range all {
		want := p == PreReady
		if got := Active(p); got != want {
			t.Errorf("Active(%s) = %v, want %v", p, got, want)
		}
	}
}

// render is the environment contract. It sorts so a run is reproducible, and the per-hook value is appended
// last so a caller's map cannot overwrite which root produced a hook.
func TestRender(t *testing.T) {
	env := map[string]string{
		"GIT_PAIR_HOOK_ROOT":  "lied-to",
		"GIT_PAIR_CHANGESET":  "checkout-flow",
		"GIT_PAIR_HOOK_POINT": "pre-ready",
	}
	got := render(env, "GIT_PAIR_HOOK_ROOT", string(RootTracked))
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "GIT_PAIR_HOOK_ROOT=worktree") {
		t.Errorf("render = %q, want the root the caller passed", joined)
	}
	if strings.Contains(joined, "lied-to") {
		t.Errorf("render = %q, want the caller's value for the per-hook key not to survive", joined)
	}
	// Everything but the per-hook value keeps the sorted order the rest of the contract is written
	// against; that value goes last so a caller's map cannot overwrite it.
	if head := got[:len(got)-1]; !slices.IsSorted(head) {
		t.Errorf("render is not sorted apart from its last entry: %q", got)
	}
	if !strings.HasPrefix(got[len(got)-1], "GIT_PAIR_HOOK_ROOT=") {
		t.Errorf("render put %q last, want the per-hook value", got[len(got)-1])
	}
}

// SkippedUntrusted is what a refusal and a report name, so an untrusted hook has to be findable in the plan
// rather than only implied by its absence. The clone's own hooks are never listed: they are the reader's own
// file, and telling them to review it would be nonsense.
func TestPlanSkippedUntrusted(t *testing.T) {
	plan := Plan{Hooks: []Hook{
		{Name: "01-repo", Root: RootTracked},
		{Name: "03-clone", Root: RootClone},
	}}
	var got []string
	for _, r := range plan.SkippedUntrusted() {
		if r.Status != StatusSkippedUntrusted {
			t.Errorf("%s: status = %s, want skipped-untrusted", r.Hook.Name, r.Status)
		}
		if r.Blocking() {
			t.Errorf("%s: a hook that never ran must not refuse the operation on its own", r.Hook.Name)
		}
		got = append(got, r.Hook.Name)
	}
	if !slices.Equal(got, []string{"01-repo"}) {
		t.Errorf("skipped = %v, want [01-repo]", got)
	}
}
