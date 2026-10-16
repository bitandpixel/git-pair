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

// A decline is the position that was missing: content read, and chosen not to run here. Two properties make it
// worth a state of its own rather than a boolean beside the acceptance. It has to be distinguishable from never
// having looked — that is its whole use to a report, and what lets the gate ask for a position without pushing
// anyone into approving a script — and it must not become a permanent exemption, which is why it is keyed by the
// same content hash acceptance is.
func TestDeclineStates(t *testing.T) {
	ctx := context.Background()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	repo := &git.Repo{Dir: f.Dir()}
	dir := filepath.Join(f.Dir(), ".git-pair", "hooks", "pre-ready")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	hook := filepath.Join(dir, "01-suite")
	writeHook := func(body string) {
		t.Helper()
		if err := os.WriteFile(hook, []byte(body), 0o755); err != nil {
			t.Fatalf("write hook: %v", err)
		}
	}
	writeHook("#!/bin/sh\nexit 0\n")

	// The list comes back for the same reason `Accept` prints one: a position taken about content nobody has
	// been shown is not a position.
	want, names, err := Decline(ctx, repo)
	if err != nil {
		t.Fatalf("Decline: %v", err)
	}
	if want == "" || len(names) != 1 || !strings.Contains(names[0], "01-suite") {
		t.Errorf("Decline = %q, %v, want a hash and the one file it covers", want, names)
	}
	state, accepted, current, err := Trust(ctx, repo)
	if err != nil {
		t.Fatalf("Trust after the decline: %v", err)
	}
	if state != TrustDeclined || current != want {
		t.Errorf("declined: state=%s current=%q, want declined at %q", state, current, want)
	}
	// Declining runs nothing. The distinction from an unrecorded tree is who decided, not what happened.
	if trusted, err := Trusted(ctx, repo); err != nil || trusted {
		t.Errorf("Trusted = %v (%v) for declined content, want false — a decline is not a way to get a script run",
			trusted, err)
	}
	if got, err := Declined(ctx, repo); err != nil || got != want {
		t.Errorf("Declined = %q (%v), want %q", got, err, want)
	}

	// The hash rule. A new version of a declined script is new content, and the decline says nothing about it:
	// it comes back unrecorded rather than quietly inherited, which is the difference between a position on a
	// script and an exemption for a directory.
	writeHook("#!/bin/sh\nmisbehave\nexit 0\n")
	state, accepted, current, err = Trust(ctx, repo)
	if err != nil {
		t.Fatalf("Trust after the new version: %v", err)
	}
	if state != TrustUnrecorded {
		t.Errorf("after a new version of declined content: state=%s, want unrecorded", state)
	}
	if accepted != "" {
		t.Errorf("accepted = %q for a clone that never accepted anything", accepted)
	}
	if current == want || current == "" {
		t.Errorf("current = %q, want a new hash beside the declined %q", current, want)
	}
	// The stale decline stays reportable, because "declined 3f1a…, the tree is now 82b0…" is the sentence that
	// tells a person the script they refused has a new version sitting unrecorded behind it.
	if got, err := Declined(ctx, repo); err != nil || got != want {
		t.Errorf("Declined = %q (%v) after the new version, want the stale %q to stay reportable", got, err, want)
	}

	// The two positions still cancel each other, in both directions — but they cancel the hash being answered
	// for, not the whole record. Accepting content you had declined, or declining content you had accepted, is one
	// person changing their mind about the same bytes, and the other record must not go on saying otherwise.
	//
	// The direction that changed is the older acceptance. It used to be cleared, because with a single slot the
	// mere presence of an acceptance made the content read as `changed`, which would tell a person to re-read what
	// they had just read. Membership replaced that reading — a hash answers for the content it names and for no
	// other — so the state below is decided by whether this tree is in the declined set, and the older acceptance
	// is a fact about different content rather than a claim about this one. Clearing the file is not free any more
	// either: the record is shared by every worktree of the clone, and a person declining the branch they are
	// standing on would otherwise switch off the check a colleague's checkout had accepted on trunk.
	writeHook("#!/bin/sh\nexit 0\n")
	acceptedHash, _, err := Accept(ctx, repo)
	if err != nil {
		t.Fatalf("Accept after the decline: %v", err)
	}
	if got, err := Declined(ctx, repo); err != nil || got != "" {
		t.Errorf("Declined = %q (%v) after accepting, want the cleared record", got, err)
	}
	if state, _, _, err := Trust(ctx, repo); err != nil || state != TrustAccepted {
		t.Errorf("state = %s (%v) after accepting, want accepted", state, err)
	}
	writeHook("#!/bin/sh\nexit 1\n")
	if _, _, err := Decline(ctx, repo); err != nil {
		t.Fatalf("Decline after the acceptance: %v", err)
	}
	state, accepted, _, err = Trust(ctx, repo)
	if err != nil {
		t.Fatalf("Trust after declining: %v", err)
	}
	if state != TrustDeclined {
		t.Errorf("state = %s after declining, want declined", state)
	}
	if accepted != acceptedHash {
		t.Errorf("accepted = %q after declining, want the older acceptance %q to survive: it names content somebody did read, and another worktree may be standing on it",
			accepted, acceptedHash)
	}
}
