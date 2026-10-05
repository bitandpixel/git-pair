package cli_test

import (
	"fmt"
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The landing is one fact with three readers. `status` is where an author looks, `check` is the gate that
// has to stay quiet about it, and `queue` is where a reviewer sees a child sitting on a base that is no
// longer the work. None of them may turn the landing into a reason: a child whose diff is unchanged is
// still integration-ready with a landed parent, and a gate that refused it would be asking for a re-review
// of content nobody has changed.

// TestCheckPassesAChildOnALandedParent is the case the whole milestone exists for: the child is approved,
// its own content has not moved, and the only new fact is that the branch underneath it has landed.
func TestCheckPassesAChildOnALandedParentAndNamesTheStep(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	// The parent's own ready and approve commits have to arrive before the child's approval records the
	// parent tip, or the fixture is the `parent moved since review` case rather than the landing case.
	f.SwitchTo("alpha")
	ready(t, f)
	submit(t, f, "approve")
	f.SwitchTo("beta")
	ready(t, f)
	submit(t, f, "approve")
	f.SwitchTo("alpha")
	landing := landApproved(t, f, "alpha", "main")
	f.SwitchTo("beta")

	res := runIn(t, f.Dir(), "check")
	res.mustSucceed(t, "check")
	mustContain(t, res.stdout, "landed as "+shortOf(landing),
		"a passing verdict still says what is underneath the work")
	mustContain(t, res.stdout, "rebase onto it", "and which step that leaves")

	out := runIn(t, f.Dir(), "check", "--json").json(t)
	if out["ready"] != true {
		t.Fatalf("ready is %v with reasons %v: a landed parent changes no content this child owns",
			out["ready"], out["reasons"])
	}
	if reasons := out["reasons"].([]any); len(reasons) != 0 {
		t.Errorf("reasons = %v, want none", reasons)
	}
	if na, ok := out["next_action"].(string); !ok || !strings.Contains(na, "rebase onto it") {
		t.Errorf("next_action = %v, want it to name the rebase the landing leaves", out["next_action"])
	}
	if out["parent_landed"] != true || out["parent_landed_commit"] != shortOf(landing) {
		t.Errorf("parent_landed = %v / %v, want true and %s", out["parent_landed"], out["parent_landed_commit"], shortOf(landing))
	}
}

// The other half of the choice: the child is already on the landing commit, so what is left is a branch to
// delete. `check` says the same sentence `status` says, because a passing gate is where the author reads
// what to do next.
func TestCheckNamesAStaleParentBranchInNextAction(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landing := landAndRecord(t, f, "alpha", "main")
	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")
	f.SwitchTo("beta")
	ready(t, f)
	submit(t, f, "approve")

	out := runIn(t, f.Dir(), "check", "--json").json(t)
	if out["ready"] != true {
		t.Fatalf("ready is %v with reasons %v", out["ready"], out["reasons"])
	}
	na, ok := out["next_action"].(string)
	if !ok || !strings.Contains(na, "alpha is stale") {
		t.Errorf("next_action = %v, want it to call the parent branch stale", out["next_action"])
	}
	if out["parent_stale_branch"] != true {
		t.Errorf("parent_stale_branch is %v, want true", out["parent_stale_branch"])
	}
	if out["parent_landed_commit"] != shortOf(landing) {
		t.Errorf("parent_landed_commit is %v, want %s", out["parent_landed_commit"], shortOf(landing))
	}
}

// The parent moving after the approval is a different fact from the content moving, and it is a note
// rather than a refusal. Without this row the passing verdict above is indistinguishable from a gate that
// has stopped asking the question at all: the note is the evidence that the comparison was made, and the
// test of the comparison refusing is in `contribution_gate_test.go`, where the content actually moved.
func TestCheckNotesAParentThatMovedAfterTheApproval(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	ready(t, f)
	submit(t, f, "approve")
	f.SwitchTo("alpha")
	f.Commit("alpha extra work", gittest.WithFile("c.go", "package main\n"))
	f.SwitchTo("beta")

	res := runIn(t, f.Dir(), "check").mustSucceed(t, "check")
	mustContain(t, res.stderr+res.stdout, "moved since review",
		"the parent's own movement is reported, with what it was")
	mustContain(t, res.stdout, "none of them touch files this branch changes",
		"and whether that movement reaches this branch's files")
}

// A reviewer reading the queue wants to know a row is being read against a base that has already landed.
// `behindParent` counts parent commits the branch does not have, which is zero for a parent whose branch
// was left where it was — so this note comes from the record instead.
func TestQueueNotesAReadyChildSittingOnALandedParent(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landing := landAndRecord(t, f, "alpha", "main")
	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")
	ready(t, f)

	res := runIn(t, f.Dir(), "queue")
	res.mustSucceed(t, "queue")
	mustContain(t, res.stderr, "parent alpha landed as "+shortOf(landing),
		"the note says the base is a landing, not a moving branch")
	mustContain(t, res.stderr, "alpha is stale", "and what is left of it")

	out := runIn(t, f.Dir(), "queue", "--json").json(t)
	rows := out["ready_for_review"].([]any)
	if len(rows) != 1 {
		t.Fatalf("ready_for_review = %v, want the one READY child unchanged: a note is not a row", rows)
	}
	notes := out["parent_notes"].([]any)
	if len(notes) != 1 || !strings.Contains(notes[0].(string), "landed as") {
		t.Errorf("parent_notes = %v, want the one landed-parent note", out["parent_notes"])
	}
}

// The note asks the destination one question per READY child that has a landed parent. The assertion is
// the shape the other queue cost tests use: the same repository with three hundred retired refs added
// must not notice, because nothing reads that namespace any more.
func TestQueueLandedParentNoteCostsNothingPerRef(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")
	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")
	ready(t, f)

	count := f.SpawnShim(t)
	before := count()
	runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	without := count() - before

	base := f.Head()
	for i := 0; i < 300; i++ {
		f.MustGit("update-ref", "refs/git-pair/archive/"+fmt.Sprintf("cs-%03d", i), base)
	}
	count = f.SpawnShim(t)
	before = count()
	runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	withRefs := count() - before

	if without < 1 {
		t.Fatalf("the queue counted %d invocations: the shim measured nothing", without)
	}
	if withRefs > without+2 {
		t.Errorf("queue cost %d invocations with 300 extra refs and %d without: the landed-parent note reads the namespace per changeset",
			withRefs, without)
	}
}

// landApproved lands the checked-out branch — already approved, which is why `landAndRecord` cannot be
// used here: it adds the ready and review commits itself, and a parent that moves after the child's
// approval is a different finding from a parent that lands.
func landApproved(t *testing.T, f *gittest.Fixture, slug, target string) string {
	t.Helper()
	f.SwitchTo(target)
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "land "+slug, slug)
	return f.Head()
}

// landedAndUnreviewed builds the shape the destination-branch report exists for: one changeset merged
// into main whose chain carries no verdict at all, and one merged through a review, and the checkout is
// left standing on main — the branch that holds no work in progress.
func landedAndUnreviewed(t *testing.T) *gittest.Fixture {
	t.Helper()
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.StageChangeset("alpha", "main")
	f.Write(f.ChangesetPath("alpha", "CHANGESET.yaml"), "id: alpha\nbase: main\n")
	f.Write(f.ChangesetPath("alpha", "ABOUT.md"), "# alpha\n")
	f.Commit("changeset alpha", gittest.WithFile("a.go", "package main\n"))
	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "land alpha", "alpha")
	f.CreateBranch("beta")
	f.CommitChangeset("beta", "main")
	f.Commit("beta work", gittest.WithFile("b.go", "package main\n"))
	landAndRecord(t, f, "beta", "main")
	f.SwitchTo("main")
	return f
}

// The destination branch is the branch every changeset eventually lands on, and the branch where a
// landing that no review permitted is easiest to miss: it holds no work in progress, so `status` there
// fails on "no changeset for this branch" and the finding has to ride along with that answer.
func TestStatusOnTheDestinationBranchReportsUnreviewedLandings(t *testing.T) {
	f := landedAndUnreviewed(t)

	res := runIn(t, f.Dir(), "status")
	if res.code != 2 {
		t.Fatalf("status on the destination branch exited %d, want 2: the branch really holds no work in progress\n%s\n%s",
			res.code, res.stdout, res.stderr)
	}
	mustContain(t, res.stderr, "no changeset for this branch",
		"the first half of the answer is unchanged")
	mustContain(t, res.stderr, "LANDED UNREVIEWED", "and the second half rides on the same error")
	mustContain(t, res.stderr, "alpha", "naming the landing whose chain carries no verdict")
}

// `--json` on that failure answers with a document, because the caller is a machine that has to tell
// "nothing is waiting" from "this build could not look". The key is present and empty when there is
// nothing to report, which is the convention `status` already commits to on its success path.
func TestStatusOnTheDestinationBranchJSONCarriesTheList(t *testing.T) {
	f := landedAndUnreviewed(t)

	res := runIn(t, f.Dir(), "status", "--json")
	if res.code != 2 {
		t.Fatalf("exit %d, want 2", res.code)
	}
	out := res.json(t)
	unreviewed := out["landed_unreviewed"].([]any)
	if len(unreviewed) != 1 {
		t.Fatalf("landed_unreviewed = %v, want the one landing whose chain carries no verdict", out["landed_unreviewed"])
	}
	if row := unreviewed[0].(map[string]any); row["changeset"] != "alpha" {
		t.Errorf("landed_unreviewed[0] = %v, want alpha", unreviewed[0])
	}
}
