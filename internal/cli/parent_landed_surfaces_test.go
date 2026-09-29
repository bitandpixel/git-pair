package cli_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
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

// The parent moving after the approval is a different fact and keeps its refusal. Without this row the
// passing verdict above is indistinguishable from a gate that has stopped asking the question.
func TestCheckStillRefusesAParentThatMovedAfterTheApproval(t *testing.T) {
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

	res := runIn(t, f.Dir(), "check")
	if res.code == 0 {
		t.Fatalf("check passed with stdout:\n%s", res.stdout)
	}
	mustContain(t, res.stderr+res.stdout, "moved since review",
		"the parent's own movement still refuses, with today's wording")
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

// The note reads the namespace index the queue already built and costs one containment question per READY
// child that has a recorded parent. The assertion is the shape the other queue cost tests use: the same
// repository with three hundred refs added must not notice.
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
		f.MustGit("update-ref", reviewref.Archive(fmt.Sprintf("cs-%03d", i)), base)
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
	source := f.Head()
	f.SwitchTo(target)
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "land "+slug, slug)
	landing := f.Head()
	runIn(t, f.Dir(), "integration", "record", "--changeset", slug, "--source", source,
		"--commit", landing, "--target", target).mustSucceed(t, "integration", "record")
	return landing
}

// trunkFixture lands and records two changesets on main, points the repository at a bare remote, and
// publishes only one of the two pairs — the state where a record exists only in the clone that wrote it.
func trunkFixture(t *testing.T) *gittest.Fixture {
	t.Helper()
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")
	f.CreateBranch("beta")
	f.CommitChangeset("beta", "main")
	f.Commit("beta work", gittest.WithFile("b.go", "package main\n"))
	landAndRecord(t, f, "beta", "main")

	remote := filepath.Join(t.TempDir(), "remote.git")
	f.MustGit("init", "--bare", "-b", "main", remote)
	f.MustGit("push", "--quiet", remote, "--all")
	f.MustGit("remote", "add", "origin", remote)
	for _, family := range []func(string) string{reviewref.Archive, reviewref.Integration} {
		f.MustGit("push", "--quiet", "origin", family("alpha")+":"+family("alpha"))
	}
	return f
}

// The destination branch is the branch every changeset eventually lands on, and the one branch where
// `status` asked nothing about publication: it failed on "no changeset for this branch" before the
// comparison ran. Two records here, one of them never published, and the answer was one line.
func TestStatusOnTheDestinationBranchReportsUnpublishedRecords(t *testing.T) {
	f := trunkFixture(t)
	f.SwitchTo("main")

	res := runIn(t, f.Dir(), "status", "--fetch")
	if res.code != 2 {
		t.Fatalf("status on the destination branch exited %d, want 2: the branch really holds no work in progress\n%s\n%s",
			res.code, res.stdout, res.stderr)
	}
	mustContain(t, res.stderr, "no changeset for this branch",
		"the first half of the answer is unchanged")
	mustContain(t, res.stdout, "RECORDED, NOT PUBLISHED", "and the second half now runs")
	mustContain(t, res.stdout, "beta", "naming the record that never left the clone")
	mustNotContain(t, res.stdout, "alpha\n", "the published one is not a finding")
}

// `--json` on that failure answers with a document, because the caller is a machine that has to tell
// "nothing is waiting" from "this build could not look". Both keys are present and empty when there is
// nothing to report, which is the convention `status` already commits to on its success path.
func TestStatusOnTheDestinationBranchJSONCarriesBothLists(t *testing.T) {
	f := trunkFixture(t)
	f.SwitchTo("main")

	res := runIn(t, f.Dir(), "status", "--fetch", "--json")
	if res.code != 2 {
		t.Fatalf("exit %d, want 2", res.code)
	}
	out := res.json(t)
	unpub := out["unpublished"].([]any)
	if len(unpub) != 1 {
		t.Fatalf("unpublished = %v, want the one record on this clone and not on origin", out["unpublished"])
	}
	if pair := unpub[0].(map[string]any); pair["changeset"] != "beta" {
		t.Errorf("unpublished[0] = %v, want beta", unpub[0])
	}
	if rec := out["landed_unreviewed"].([]any); len(rec) != 0 {
		t.Errorf("landed_unreviewed = %v, want the empty list: both directories here arrived through a review",
			out["landed_unreviewed"])
	}
}

// With no remote there is no comparison to make, and the answer is the sentence that says so rather than
// an empty list, which a reader would take for "all published".
func TestStatusOnTheDestinationBranchSaysWhenNothingCanBeClaimed(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")
	f.SwitchTo("main")

	res := runIn(t, f.Dir(), "status")
	if res.code != 2 {
		t.Fatalf("exit %d, want 2", res.code)
	}
	mustContain(t, res.stdout+res.stderr, "no remote to compare against",
		"the question cannot be asked here, and that is what gets said")
}
