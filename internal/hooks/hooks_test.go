package hooks

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gitpair/internal/git"
	"gitpair/internal/gittest"
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

// The point set is the documentation's subject and the report's subject at the same time, so the two have to
// agree on one vocabulary: six actions, each with a side before and a side after.
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
	// The vocabulary is the six actions a changeset meets, each on both sides. A half with no partner is a
	// milestone that stopped halfway. That each of these is invoked by a command is not the hooks package's
	// to claim: the CLI tests run every point end to end, which is the assertion that a point exists because
	// something fires it.
	wantActions := []string{"ready", "integrate", "unready", "abandon", "check", "submit"}
	if len(all) != 2*len(wantActions) {
		t.Errorf("%d points configured, want %d (six actions, two sides)", len(all), 2*len(wantActions))
	}
	for _, action := range wantActions {
		for _, side := range []string{"pre-", "post-"} {
			if !seen[Point(side+action)] {
				t.Errorf("the %s transition has no %s half", action, side+action)
			}
		}
	}
	// A read-path command must not acquire a point by accident. A hook on a read turns a lookup into a
	// build, and a cost test that counts subprocesses would start measuring the customer's script.
	for _, read := range []string{"queue", "status", "diff", "feedback", "history", "wait"} {
		for _, side := range []string{"pre-", "post-"} {
			if seen[Point(side+read)] {
				t.Errorf("the read path %q has a lifecycle point; reads run no hooks", side+read)
			}
		}
	}
	// `check` and `submit` are points in their own right, not aliases for a transition: the gate answers
	// with conditions and the submission is a reviewer's act, so a hook written for one must not fire on the
	// other. An approve-named point would be that alias.
	if seen[Point("pre-approve")] || seen[Point("post-approve")] {
		t.Error("an approve point is configured; the reviewer's points are pre-submit and post-submit")
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

// The two reasons a tracked tree does not run are different facts, and the report has to tell them apart: a
// clone that has never accepted anything is a person who has not looked, and a clone whose accepted hash no
// longer matches is a person the repository moved a check under. The states are the difference between
// "read this" and "the team shipped a new version, and your work is running without it".
func TestTrustStates(t *testing.T) {
	ctx := context.Background()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	repo := &git.Repo{Dir: f.Dir()}

	// Nothing to accept is accepted by vacuity: an absent hooks directory must not read as a pending
	// approval, or every repository without hooks would be reported as waiting on one.
	state, accepted, current, err := Trust(ctx, repo)
	if err != nil {
		t.Fatalf("Trust with no hooks: %v", err)
	}
	if state != TrustAccepted || accepted != "" || current != "" {
		t.Errorf("no hooks: state=%s accepted=%q current=%q, want accepted with neither hash", state, accepted, current)
	}

	if err := os.MkdirAll(filepath.Join(f.Dir(), ".git-pair", "hooks", "pre-ready"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	hook := filepath.Join(f.Dir(), ".git-pair", "hooks", "pre-ready", "01-suite")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}

	state, accepted, current, err = Trust(ctx, repo)
	if err != nil {
		t.Fatalf("Trust before acceptance: %v", err)
	}
	if state != TrustUnrecorded || accepted != "" || current == "" {
		t.Errorf("never accepted: state=%s accepted=%q current=%q, want unrecorded with only the tree hash", state, accepted, current)
	}

	want, _, err := Accept(ctx, repo)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	state, accepted, current, err = Trust(ctx, repo)
	if err != nil {
		t.Fatalf("Trust after acceptance: %v", err)
	}
	if state != TrustAccepted || accepted != want || current != want {
		t.Errorf("accepted: state=%s accepted=%q current=%q want=%s", state, accepted, current, want)
	}

	// The case the boolean used to swallow. A line added after the approval — a check that now runs
	// something else too — is new content, and the answer has to name both hashes so the reader can see what
	// moved rather than being told only that something is wrong.
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nmisbehave\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("rewrite hook: %v", err)
	}
	state, accepted, current, err = Trust(ctx, repo)
	if err != nil {
		t.Fatalf("Trust after the edit: %v", err)
	}
	if state != TrustChanged {
		t.Errorf("after an edit: state=%s, want changed", state)
	}
	if accepted != want {
		t.Errorf("recorded hash = %q, want the accepted %q", accepted, want)
	}
	if current == want || current == "" {
		t.Errorf("current hash = %q, want a new hash beside the accepted %q", current, want)
	}

	// The two hashes a report shows are the same values `--trust` printed, so a person comparing them is
	// comparing the thing they approved against the thing that would run.
	if trusted, err := Trusted(ctx, repo); err != nil || trusted {
		t.Errorf("Trusted = %v (%v) after the edit, want false", trusted, err)
	}
}
