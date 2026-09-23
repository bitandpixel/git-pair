package cli_test

import (
	"path/filepath"
	"testing"

	"gitpair/internal/gittest"
)

// A parent that has landed is reported from its record, not from its branch. The branch is where an
// active parent lives, and it keeps living after the merge: a squash, a `--no-ff` merge or a
// fast-forward all leave it where it was, so "unchanged since the approval" is a true sentence about a
// parent whose work is already in the destination. Only the record says the work landed, and the child's
// next step — delete the branch, or rebase onto the landing — depends on the record plus its own head.
//
// The read is also for the child that has no approval yet. `status` is where an author looks before
// offering anything, and a child stacked on a landed parent wants to know that before it is offered.

// stackedOff starts a changeset on a new branch off the branch under the reader's feet, which is the
// shape `stackedChangeset` cannot build: it branches off the integration branch, and the case that needs
// separating is the child that branched off the parent and has not moved since.
func stackedOff(t *testing.T, f *gittest.Fixture, slug, parentSlug, parentBranch, work string) {
	t.Helper()
	f.CreateBranch(slug)
	f.Commit("changeset "+slug, gittest.WithFiles(map[string]string{
		"changesets/" + slug + "/CHANGESET.yaml": "id: " + slug +
			"\nparent: " + parentBranch + "\nparent-changeset: " + parentSlug + "\n",
		"changesets/" + slug + "/ABOUT.md": "# " + slug + "\n",
	}), gittest.WithFile(work, "package main\n"))
}

// landWithoutRecord is `landAndRecord` with the last step taken away, which is the state of a landing
// nobody recorded: the directory is in the destination, and git-pair has nothing durable to say about it.
func landWithoutRecord(t *testing.T, f *gittest.Fixture, slug, target string) string {
	t.Helper()
	ready(t, f)
	submit(t, f, "approve")
	f.SwitchTo(target)
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "land "+slug, slug)
	return f.Head()
}

func parentJSONOf(t *testing.T, f *gittest.Fixture, slug string) map[string]any {
	t.Helper()
	out := runIn(t, f.Dir(), "status", "--changeset", slug, "--json").json(t)
	p, ok := out["parent"].(map[string]any)
	if !ok {
		t.Fatalf("parent is %#v, want an object for a changeset stacked on %s", out["parent"], slug)
	}
	return p
}

// The reported case: the parent landed, its branch is still here, and the child's head is already on the
// landing commit. The child's own history shows nothing — no parent movement, no drift — so before this
// the author learned the parent had landed by trying to delete the branch and reading the error.
func TestStatusCallsALandedParentStaleWhileItsBranchIsPresent(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landing := landAndRecord(t, f, "alpha", "main")
	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")

	res := runIn(t, f.Dir(), "status", "--changeset", "beta")
	res.mustSucceed(t, "status")
	mustContain(t, res.stdout, "landed as "+shortOf(landing),
		"the parent's record is the fact its branch tip cannot give")
	mustContain(t, res.stdout, "alpha is stale",
		"and the branch is what is left of a landed parent")
	mustContain(t, res.stdout, "git branch -D alpha",
		"printed as the command, because an author translating \"delete it\" into arguments gets the order wrong")
	mustContain(t, res.stdout, "Base: alpha",
		"the read does not move the measurement base")
	mustNotContain(t, res.stdout, "stale:  ",
		"a note is not a refusal: this child has nothing the reviewer has to look at again")

	p := parentJSONOf(t, f, "beta")
	if p["landed"] != true {
		t.Errorf("parent.landed is %v, want true for a parent with an integration record", p["landed"])
	}
	if p["landed_commit"] != shortOf(landing) {
		t.Errorf("parent.landed_commit is %v, want %s", p["landed_commit"], shortOf(landing))
	}
	if p["landed_in_default_branch"] != true {
		t.Errorf("parent.landed_in_default_branch is %v, want true", p["landed_in_default_branch"])
	}
	if p["stale_branch"] != true {
		t.Errorf("parent.stale_branch is %v, want true: the head already carries the landing", p["stale_branch"])
	}
}

// A child that has not moved since it branched is not on the landing commit, so its parent branch is not
// dead weight yet — rebasing is. The two cases share the fact and differ in the step, which is why the
// step is a separate field from the landing.
func TestStatusNotesALandedParentTheChildHasNotRebasedOnto(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	f.SwitchTo("alpha")
	landing := landAndRecord(t, f, "alpha", "main")

	res := runIn(t, f.Dir(), "status", "--changeset", "beta")
	res.mustSucceed(t, "status")
	mustContain(t, res.stdout, "landed as "+shortOf(landing), "the parent's record still answers")
	mustContain(t, res.stdout, "rebase onto it", "and the step is the rebase, not the deletion")
	mustContain(t, res.stdout, "git rebase --onto "+shortOf(landing)+" alpha beta",
		"with the landing commit as --onto, the parent branch as the upstream, and the child as the branch")
	mustNotContain(t, res.stdout, "is stale", "the branch still carries the base this child is measured on")

	p := parentJSONOf(t, f, "beta")
	if p["landed"] != true || p["landed_commit"] != shortOf(landing) {
		t.Errorf("parent is %v, want landed at %s", p, shortOf(landing))
	}
	if p["stale_branch"] != false {
		t.Errorf("parent.stale_branch is %v, want false: this head is not on the landing", p["stale_branch"])
	}
}

// A landing into a release branch is a landing, and it is not a landing on the integration branch. The
// two claims travel separately for the reason §13 gives the integration ref: it stores an object id and
// no branch name, so what reached where is read from the history rather than remembered.
func TestStatusSaysWhenTheLandingDidNotReachTheDefaultBranch(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("release/2.x")
	f.SwitchTo("main")
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landing := landAndRecord(t, f, "alpha", "release/2.x")
	f.SwitchTo("main")
	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")

	res := runIn(t, f.Dir(), "status", "--changeset", "beta")
	res.mustSucceed(t, "status")
	mustContain(t, res.stdout, "landed as "+shortOf(landing), "the record is the record")
	mustContain(t, res.stdout, "not reachable from main",
		"and the branch it did not reach is named rather than assumed")

	p := parentJSONOf(t, f, "beta")
	if p["landed"] != true {
		t.Errorf("parent.landed is %v, want true: a release-branch landing has a record too", p["landed"])
	}
	if p["landed_in_default_branch"] != false {
		t.Errorf("parent.landed_in_default_branch is %v, want false", p["landed_in_default_branch"])
	}
}

// Work in the destination with no record behind it is the finding §22 exists to make detectable, and a
// child stacked under it is the reader most likely to notice. The hedge stays: "no record" is a statement
// about this clone until the namespace has been fetched.
func TestStatusSaysALandedParentHasNoRecordHere(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	f.SwitchTo("alpha")
	landWithoutRecord(t, f, "alpha", "main")

	res := runIn(t, f.Dir(), "status", "--changeset", "beta")
	res.mustSucceed(t, "status")
	mustContain(t, res.stdout, "no integration record",
		"the parent's work is in main and git-pair has nothing durable about it")
	mustContain(t, res.stdout, "git pair integration record --changeset alpha",
		"naming the command that closes the gap, with this changeset in it")
	mustContain(t, res.stdout, "none in this clone", "and the hedge that keeps the other reading open")

	p := parentJSONOf(t, f, "beta")
	if p["landed"] != false {
		t.Errorf("parent.landed is %v without a record: landed is a record's claim, not a merge's", p["landed"])
	}
}

// The delete the stale note advises fails in the repository layout this project actually uses: the parent
// branch is checked out in another worktree, and git refuses with "used by worktree at ...". git-pair does
// not remove worktrees (PRD §26), so the blocker and its remedy are named as text.
func TestStatusNamesTheWorktreeBlockingAParentDelete(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")
	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")
	wt := filepath.Join(t.TempDir(), "alpha-worktree")
	f.MustGit("worktree", "add", wt, "alpha")

	res := runIn(t, f.Dir(), "status", "--changeset", "beta")
	res.mustSucceed(t, "status")
	mustContain(t, res.stdout, "git branch -D alpha", "the step is still the delete")
	mustContain(t, res.stdout, "checked out in "+wt, "and the reason it would fail is named")
	mustContain(t, res.stdout, "git worktree remove "+wt,
		"with the remedy spelled as text rather than run: removing a worktree is not git-pair's to do")
}

// The parent's branch being gone is the case `relinkStacks` already measures a child against the parent's
// integration ref for. It keeps its own answer, refusal included: a child that has not been reconciled
// over a deleted parent has an author's decision outstanding, not a note.
func TestStatusKeepsTheDeletedParentBranchAnswer(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landing := landAndRecord(t, f, "alpha", "main")
	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")
	f.MustGit("branch", "-d", "alpha")

	res := runIn(t, f.Dir(), "status", "--changeset", "beta")
	res.mustSucceed(t, "status")
	mustContain(t, res.stdout, "the branch is gone", "the branch half of the answer is unchanged")
	mustContain(t, res.stdout, "landed as "+shortOf(landing),
		"and the landing is named by the record, which is where the deleted branch's tip used to live")

	p := parentJSONOf(t, f, "beta")
	if p["landed"] != true || p["landed_commit"] != shortOf(landing) {
		t.Errorf("parent is %v, want landed at %s", p, shortOf(landing))
	}
	if p["stale_branch"] != false {
		t.Errorf("parent.stale_branch is %v when the branch is gone: stale is about a branch that is here",
			p["stale_branch"])
	}
}

// Without `parent-changeset:` there is no record to look for: the yaml names a branch, and a branch is not
// a changeset id. Silence is the honest answer, and `parent:` alone is also the shape a hand-written stack
// arrives in.
func TestStatusStaysQuietAboutAParentWithNoChangesetID(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")
	f.CreateBranch("beta")
	f.Commit("changeset beta", gittest.WithFiles(map[string]string{
		"changesets/beta/CHANGESET.yaml": "id: beta\nparent: alpha\n",
		"changesets/beta/ABOUT.md":       "# beta\n",
	}), gittest.WithFile("b.go", "package main\n"))

	res := runIn(t, f.Dir(), "status", "--changeset", "beta")
	res.mustSucceed(t, "status")
	mustNotContain(t, res.stdout, "landed", "no id means no record to read, so nothing is claimed")

	out := runIn(t, f.Dir(), "status", "--changeset", "beta", "--json").json(t)
	if p, ok := out["parent"].(map[string]any); ok && p["landed"] == true {
		t.Errorf("parent.landed is true for a parent git-pair cannot name a record for: %v", p)
	}
}

// An unapproved child gets the same note and none of the refusal. The approval is what a moved parent can
// invalidate; a child that has not been offered yet has nothing to invalidate, and the landing is still
// worth knowing before the author runs `change ready`.
func TestStatusReportsALandedParentWithoutAnApproval(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landing := landAndRecord(t, f, "alpha", "main")
	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")

	out := runIn(t, f.Dir(), "status", "--changeset", "beta", "--json").json(t)
	if out["state"] != "WORKING" {
		t.Fatalf("state is %v, want WORKING so the test is about an unapproved child", out["state"])
	}
	p := out["parent"].(map[string]any)
	if p["landed"] != true || p["landed_commit"] != shortOf(landing) {
		t.Errorf("parent is %v, want the landing reported for a child with no approval", p)
	}
	if reason, ok := p["reason"].(string); ok && reason != "" {
		t.Errorf("parent.reason is %q for an unapproved child: a landing is a note here, not a refusal", reason)
	}
}
