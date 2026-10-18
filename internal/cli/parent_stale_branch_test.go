package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// "Your parent's branch is dead weight — delete it" is advice that destroys work when it is wrong, and one
// reading made it wrong. The landed-parent path asked one containment question, whether the child's own head
// already carries the landing commit, and printed the deletion whenever it held. That question says where the
// child sits; it says nothing about the branch, and a parent that took another round of work after landing has
// commits the destination has never held. Measured on `main` before this change, with `alpha` landed and then
// given one unlanded commit, `beta` reported:
//
//	parent_stale_branch: true
//	next_action: … the branch alpha is stale — it holds nothing the destination does not: `git branch -D alpha`
//
// while `git log main..alpha` listed a commit nobody had landed. `status` printed the same step and `queue`
// printed it in the row, so all three surfaces are pinned here, together with the case where the deletion is
// genuinely what is left.

// staleBranchChildOnLandedParent builds the shape: `alpha` approved and landed into `main`, then a child
// created off the destination so its head carries the landing commit. `extraParentWork` puts a commit on
// `alpha` after the landing, which is the state the deletion must not be advised in.
func staleBranchChildOnLandedParent(t *testing.T, extraParentWork bool) *gittest.Fixture {
	t.Helper()
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")
	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")
	f.SwitchTo("beta")
	ready(t, f)
	submit(t, f, "approve")
	if extraParentWork {
		f.SwitchTo("alpha")
		f.Commit("alpha round two, never landed", gittest.WithFile("roundtwo.go", "package main\n\nfunc RoundTwo() {}\n"))
		f.SwitchTo("beta")
	}
	return f
}

// The child sits above the landing, so the child owes nothing; and the branch is not the destination's
// finished chain, so the command the stale reading left would drop `roundtwo.go`. `git branch -D` answers to
// nothing else in the tool, so the refusal to print it is the whole of the fix and is what these three tests
// ask of the three surfaces that printed it.
func TestCheckWithholdsTheParentBranchDeletionWhileTheParentHoldsUnlandedWork(t *testing.T) {
	f := staleBranchChildOnLandedParent(t, true)

	out := runIn(t, f.Dir(), "check", "--json").mustSucceed(t, "check").json(t)
	if out["ready"] != true {
		t.Fatalf("ready is %v with reasons %v: the parent's new commit is the parent's business", out["ready"], out["reasons"])
	}
	if out["parent_landed"] != true {
		t.Fatalf("parent_landed is %v, want true — the fixture landed alpha", out["parent_landed"])
	}
	if out["parent_stale_branch"] != false {
		t.Errorf("parent_stale_branch is %v, want false: alpha holds a commit the destination never had",
			out["parent_stale_branch"])
	}
	if out["parent_head_carries_landing"] != true {
		t.Errorf("parent_head_carries_landing is %v, want true: this is the case the two fields exist to separate",
			out["parent_head_carries_landing"])
	}
	if na, _ := out["next_action"].(string); strings.Contains(na, "git branch -D") {
		t.Errorf("next_action = %q: the deletion would drop the parent's unlanded work", na)
	} else if !strings.Contains(na, "still holds") {
		t.Errorf("next_action = %q, want it to say the branch still holds work", na)
	}
}

// The same verdict on the surface an author looks at before touching anything.
func TestStatusWithholdsTheSameDeletionAndNamesWhereTheWorkBelongs(t *testing.T) {
	f := staleBranchChildOnLandedParent(t, true)

	out := runIn(t, f.Dir(), "status", "--changeset", "beta", "--json").json(t)
	p, ok := out["parent"].(map[string]any)
	if !ok {
		t.Fatalf("parent is %#v, want an object", out["parent"])
	}
	if p["stale_branch"] != false {
		t.Errorf("stale_branch is %v, want false", p["stale_branch"])
	}
	step, _ := p["step"].(string)
	if strings.Contains(step, "git branch -D") {
		t.Errorf("step = %q: the deletion would drop the parent's unlanded work", step)
	}
	mustContain(t, step, "alpha", "the sentence has to say which branch and whose work it means")
	mustContain(t, step, "land or drop", "and where that work has to go before the branch can go")
}

// The queue is a reviewer's surface and it spells the step with the same helper, so it printed the same
// command. Its row is a READY child, which is also why the path pays one chain derivation rather than a
// marker walk.
func TestQueueNamesTheUnlandedParentWorkInsteadOfTheDeletion(t *testing.T) {
	f := staleBranchChildOnLandedParent(t, false)
	stackedChangeset(t, f, "gamma", "alpha", "alpha", "g.go")
	f.SwitchTo("gamma")
	ready(t, f)
	f.SwitchTo("alpha")
	f.Commit("alpha round two, never landed", gittest.WithFile("roundtwo.go", "package main\n\nfunc RoundTwo() {}\n"))
	f.SwitchTo("gamma")

	joined := queueParentNotes(t, f)
	if strings.Contains(joined, "git branch -D") {
		t.Errorf("parent_notes = %q: the deletion would drop the parent's unlanded work", joined)
	}
	mustContain(t, joined, "still holds", "the reviewer reads the same fact the author does")
}

// The third state the one read answers: the parent's branch is not in this clone at all. The queue named it
// in a `git branch -D` before, and now has a sentence of its own — there is nothing to run against a branch
// that is gone, and it must not claim the branch holds work either.
func TestQueueSaysADeletedParentBranchIsGoneRatherThanStaleOrHoldingWork(t *testing.T) {
	f := staleBranchChildOnLandedParent(t, false)
	stackedChangeset(t, f, "gamma", "alpha", "alpha", "g.go")
	f.SwitchTo("gamma")
	ready(t, f)
	f.MustGit("branch", "-D", "alpha")

	joined := queueParentNotes(t, f)
	mustContain(t, joined, "nothing to rebase", "a gone branch leaves no command to run")
	if strings.Contains(joined, "git branch -D") {
		t.Errorf("parent note = %q: no deletion may be advised for a branch that is not here", joined)
	}
	if strings.Contains(joined, "still holds") {
		t.Errorf("parent note = %q, want the gone sentence rather than the holds-work one", joined)
	}
}

// queueParentNotes runs the listing and joins the parent notes it printed, so a test asks one question of the
// whole listing rather than indexing a row.
func queueParentNotes(t *testing.T, f *gittest.Fixture) string {
	t.Helper()
	runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
	out := runIn(t, f.Dir(), "queue", "--json").json(t)
	joined := ""
	for _, n := range out["parent_notes"].([]any) {
		if s, ok := n.(string); ok {
			joined += s + "\n"
		}
	}
	if joined == "" {
		t.Fatalf("parent_notes is empty, want a note for the child sitting on a landed parent")
	}
	return joined
}

// And the case the sentence exists for, so the fix cannot become a blanket refusal to call a landed branch
// stale: nothing above the landing, and the deletion is what is left.
func TestTheDeletionIsStillAdvisedWhenTheParentBranchHoldsNothingNew(t *testing.T) {
	f := staleBranchChildOnLandedParent(t, false)

	out := runIn(t, f.Dir(), "check", "--json").mustSucceed(t, "check").json(t)
	if out["parent_stale_branch"] != true {
		t.Fatalf("parent_stale_branch is %v, want true: alpha holds nothing main lacks", out["parent_stale_branch"])
	}
	na, _ := out["next_action"].(string)
	mustContain(t, na, "git branch -D alpha", "a branch that is genuinely spent is worth saying so")
	if strings.Contains(na, "still holds") {
		t.Errorf("next_action = %q, want the deletion rather than the new sentence", na)
	}
}
