package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
)

// `integration record` answers "where did this land" when nobody named a destination, and for a child of a
// landed parent the obvious answer is not a branch at all. The child's base is the parent's integration ref
// — the measurement moved there when the parent landed, so the child's diff stays the child's own work —
// and a durable ref names a commit that nothing merges into. The destination has to come from the parent's
// record instead, which is the branch the parent was measured against.
//
// This is the case a pipeline hits without noticing: it merges the child into trunk, runs the recorder with
// no arguments, and either gets a refusal about a ref or, worse, a record verified against something other
// than the branch it merged into.

// landedStack builds the shape under test twice, because the first run of the recorder is the one that
// verifies a destination and the second only reports the record it wrote: a fixture per assertion, with the
// history built by the same lines.
//
// The parent lands on trunk and is recorded; the child is cut from the parent branch, approved, and merged
// into trunk by hand. It returns the fixture, the reviewed head to pass as --source, and the landing commit
// to pass as --commit.
func landedStack(t *testing.T) (*gittest.Fixture, string, string) {
	t.Helper()
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")

	// The child is built the way a stack is built: a branch off the parent, and the parent's changeset
	// recorded in its own yaml.
	f.SwitchTo("alpha")
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	ready(t, f)
	submit(t, f, "approve")
	source := f.Head()

	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "land beta", "beta")
	return f, source, f.Head()
}

// TestIntegrationRecordOfAStackedChildVerifiesAgainstTheParentDestination is the whole point: the recorder
// is told nothing about the destination and works out that the child of a parent landed on trunk landed on
// trunk too.
func TestIntegrationRecordOfAStackedChildVerifiesAgainstTheParentDestination(t *testing.T) {
	f, source, landing := landedStack(t)

	res := runIn(t, f.Dir(), "integration", "record", "--changeset", "beta",
		"--source", source, "--commit", landing)
	res.mustSucceed(t, "integration", "record")
	mustContain(t, res.stdout, "verified reachable from main",
		"the child lands where its parent landed")
	mustContain(t, res.stdout, "verified reachable from main (the branch its landed parent was based on)",
		"and the answer says where that came from, because the child's own yaml does not say it")
	mustNotContain(t, res.stdout, "verified reachable from alpha",
		"the parent branch is where the child was cut from, not where it landed")
	if got := f.RefSHA(reviewref.Integration("beta")); got != landing {
		t.Errorf("integration ref holds %s, want the landing %s", got, landing)
	}
}

// The machine answer has to carry the same fact, and must never carry one of git-pair's own refs as a
// destination: a record "verified against" a durable ref is a record verified against nothing.
func TestIntegrationRecordOfAStackedChildReportsABranchAsItsDerivedTarget(t *testing.T) {
	f, source, landing := landedStack(t)

	j := runIn(t, f.Dir(), "integration", "record", "--json", "--changeset", "beta",
		"--source", source, "--commit", landing).json(t)
	target, _ := j["target"].(string)
	if target != "main" {
		t.Errorf("target is %q, want main", target)
	}
	if _, ok := reviewref.IntegrationID(target); ok {
		t.Errorf("target %q is one of git-pair's own refs: a record verified against a durable ref says "+
			"nothing about a branch", target)
	}
	if j["target_derived"] != true {
		t.Errorf("target_derived is %v, want true: git-pair chose this destination, so it has to say so",
			j["target_derived"])
	}
	if j["recorded"] != true {
		t.Errorf("recorded is %v, want true", j["recorded"])
	}
	if got := f.RefSHA(reviewref.Integration("beta")); got != landing {
		t.Errorf("integration ref holds %s, want the landing %s", got, landing)
	}
}

// The rule is about a parent that has landed. A parent that has not keeps the answer it always had — the
// branch it is based on, which is a real branch a child can land on — so this change does not quietly
// reinterpret the stacked landing that stays a human decision.
func TestIntegrationRecordOfAChildOnAnUnlandedParentKeepsTheBase(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	f.SwitchTo("alpha")
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	ready(t, f)
	submit(t, f, "approve")
	source := f.Head()

	f.SwitchTo("alpha")
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "land beta on its parent", "beta")
	landing := f.Head()

	res := runIn(t, f.Dir(), "integration", "record", "--changeset", "beta",
		"--source", source, "--commit", landing)
	res.mustSucceed(t, "integration", "record")
	mustContain(t, res.stdout, "verified reachable from alpha (the changeset's base branch)",
		"nothing has landed, so the base is still the destination and the answer says so")
}

// A landing nobody recorded leaves the child measured against a ref whose commit this clone may not even
// hold, and the recorder still has to answer with a branch: the default branch is the fallback, and the
// output says it was a fallback rather than a fact about the work.
func TestIntegrationRecordFallsBackToTheDefaultBranchForAStackWithNoRecord(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	// Landed, and not recorded: the directory is in the destination and git-pair has nothing durable to
	// say about it, so the child's base names a ref that no ref holds.
	landWithoutRecord(t, f, "alpha", "main")
	f.SwitchTo("alpha")
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	ready(t, f)
	submit(t, f, "approve")
	source := f.Head()

	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "land beta", "beta")
	landing := f.Head()

	res := runIn(t, f.Dir(), "integration", "record", "--changeset", "beta",
		"--source", source, "--commit", landing)
	res.mustSucceed(t, "integration", "record")
	if !strings.Contains(res.stdout, "verified reachable from main") {
		t.Errorf("record verified against another branch:\n%s", res.stdout)
	}
	mustContain(t, res.stdout, "(the default branch)",
		"with no parent record to read, main is a fallback and has to be named as one")
	if got := f.RefSHA(reviewref.Integration("beta")); got != landing {
		t.Errorf("integration ref holds %s, want the landing %s", got, landing)
	}
}
