package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// approvedChangeset drives a changeset to APPROVED on a clean tree and returns the
// fixture, the slug, the reviewed HEAD and the approval commit.
func approvedChangeset(t *testing.T) (*gittest.Fixture, string, string, string) {
	t.Helper()
	f, slug := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	reviewed := f.Head()
	submit(t, f, "approve")
	return f, slug, reviewed, f.Head()
}

// PRD §9.5 step 2: completing requires the latest effective review outcome to permit
// integration. A blocked review does not, and nothing may be archived.
func TestChangeCompleteRefusesNonPermittedOutcome(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	f.Write("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n")
	submit(t, f, "block")
	before := f.Head()

	res := runIn(t, f.Dir(), "change", "complete")
	if res.code != exitRefusal {
		t.Errorf("completing a blocked changeset exited %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "block", "the refusal must name the outcome that blocks integration")
	if f.Head() != before {
		t.Errorf("complete moved HEAD (%s -> %s) despite refusing", before, f.Head())
	}
	if refs := f.RefNames(archivePattern(slug)); len(refs) != 0 {
		t.Errorf("a refused completion wrote archive refs: %v", refs)
	}
	if got := runIn(t, f.Dir(), "status", "--json").json(t)["state"]; got != "BLOCKED" {
		t.Errorf("state = %v, want BLOCKED", got)
	}

	// Feedback and approve are the permitted outcomes (PRD §23): feedback is
	// non-blocking, so it lets the owner proceed without a second verdict.
	f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { transaction() }\n"))
	ready(t, f)
	submit(t, f, "feedback")
	runIn(t, f.Dir(), "change", "complete").mustSucceed(t, "change", "complete")
}

// PRD §9.5: completion anchors the history and writes the immutable archive ref at
// HEAD. It records no commit and establishes no state — the merge that finishes the
// changeset is ordinary git, and `status` keeps saying APPROVED until it happens.
func TestChangeCompleteArchivesHeadWithoutCommitting(t *testing.T) {
	f, slug, _, approve := approvedChangeset(t)
	before := f.RevListCount("HEAD")

	res := runIn(t, f.Dir(), "change", "complete").mustSucceed(t, "change", "complete")

	if f.Head() != approve {
		t.Errorf("HEAD = %s, want the approved head %s: completion records no commit", f.Head(), approve)
	}
	if got := f.RevListCount("HEAD"); got != before {
		t.Errorf("HEAD reaches %d commits, want the %d that were there before completion", got, before)
	}

	// The immutable archive ref names the archived commit and points at it exactly.
	wantArchive := archivePattern(slug) + "/" + f.Short(approve)
	if got := f.RefSHA(wantArchive); got != approve {
		t.Errorf("%s = %s, want the approved HEAD %s", wantArchive, got, approve)
	}
	mustContain(t, res.stdout, wantArchive, "complete must print the archive ref (PRD §9.5)")
	mustContain(t, res.stdout, "Safe to squash/merge", "complete must report integration readiness (PRD §9.5)")

	if got := runIn(t, f.Dir(), "status", "--json").json(t)["state"]; got != "APPROVED" {
		t.Errorf("state = %v, want APPROVED: completion is not a lifecycle state", got)
	}
	if queueListsChangeset(t, runIn(t, f.Dir(), "review", "queue", "--json"), slug) {
		t.Error("a completed changeset is still in the review queue")
	}
	if !f.Clean() {
		t.Error("complete left the working tree dirty")
	}
}

// The movable ref is the current review HEAD, and a changeset-only commit after the
// approval leaves it short of HEAD: the review submission is the last thing that moved
// it. Completing must anchor the head it archives.
func TestChangeCompleteAnchorsTheMovableRefAtTheCompletedHead(t *testing.T) {
	f, slug, _, approve := approvedChangeset(t)
	about := gittest.WithFile(filepath.Join("changesets", slug, "ABOUT.md"),
		"# Booking transaction\n\n## Validation\n\n`go test ./...` and a staging run.\n")
	f.Commit("author: record the validation run", about)
	head := f.Head()
	if got := f.RefSHA(reviewRef(slug)); got != approve {
		t.Fatalf("precondition: %s = %s, want the review commit %s", reviewRef(slug), got, approve)
	}

	runIn(t, f.Dir(), "change", "complete").mustSucceed(t, "change", "complete")

	if got := f.RefSHA(reviewRef(slug)); got != head {
		t.Errorf("%s = %s, want the completed head %s", reviewRef(slug), got, head)
	}
	if !f.ReachableFrom(approve, reviewRef(slug)) {
		t.Error("the approved chain is no longer reachable from the movable review ref")
	}
}

// PRD §13's invariant, and the archival promise the command exists to keep: after the
// branch is deleted the complete unsquashed chain must still be reachable from the
// archive ref.
func TestChangeCompleteKeepsFullChainReachableAfterBranchDeletion(t *testing.T) {
	f, slug, reviewed, approve := approvedChangeset(t)
	chain := f.RevList("HEAD")

	runIn(t, f.Dir(), "change", "complete").mustSucceed(t, "change", "complete")
	archiveRefs := f.RefNames(archivePattern(slug))
	if len(archiveRefs) != 1 {
		t.Fatalf("archive refs = %v, want exactly one", archiveRefs)
	}
	archive := archiveRefs[0]

	// Simulate the squash/merge finishing the branch: the branch is gone, the commits
	// are referenced by nothing else.
	f.SwitchTo("main")
	f.ForceDeleteBranch("booking-transaction")

	if branches := f.Branches(); len(branches) != 1 || branches[0] != "main" {
		t.Errorf("branches = %v, want only main", branches)
	}
	reachable := map[string]bool{}
	for _, sha := range f.RevList(archive) {
		reachable[sha] = true
	}
	for _, sha := range chain {
		if !reachable[sha] {
			t.Errorf("%s is not reachable from %s after `git branch -D`", sha, archive)
		}
	}
	// The reviewed implementation state and the approval are both in the archive.
	for _, want := range []string{reviewed, approve} {
		if !reachable[want] {
			t.Errorf("%s is missing from the archive", want)
		}
	}
	// And that chain is the full unsquashed history, not a single squashed commit.
	if count := f.RevListCount(archive); count < 5 {
		t.Errorf("archive reaches %d commits, want the whole implementation/review chain", count)
	}
	if got := f.RevList(archive); len(got) != len(chain) {
		t.Errorf("archive reaches %d commits, want the %d committed steps of this changeset", len(got), len(chain))
	}
}

// PRD §19.3: the same surviving-additions check must run before completing.
func TestChangeCompleteSurvivalGate(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)

	const comment = "// Please name this variable"
	f.Write("service.go", "package main\n\n"+comment+"\nfunc Lock() {}\n")
	submit(t, f, "feedback") // non-blocking outcome, so the survival gate is what refuses
	before := f.Head()

	res := runIn(t, f.Dir(), "change", "complete")
	if res.code != exitRefusal {
		t.Fatalf("completing with a surviving review addition exited %d, want %d\nstderr: %s",
			res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "service.go", "the report must name the file")
	mustContain(t, res.stderr, comment, "the report must print the surviving addition")
	mustContain(t, res.stderr, "git pair change complete --allow-surviving-review-additions",
		"PRD §19.3 names the override")
	if f.Head() != before {
		t.Error("a refused completion moved HEAD")
	}
	if refs := f.RefNames(archivePattern(slug)); len(refs) != 0 {
		t.Errorf("a refused completion wrote archive refs: %v", refs)
	}

	// The explicit acknowledgement lets the owner complete anyway.
	runIn(t, f.Dir(), "change", "complete", "--allow-surviving-review-additions").
		mustSucceed(t, "change", "complete", "--allow-surviving-review-additions")
	if f.Head() != before {
		t.Errorf("HEAD = %s, want the completed head %s: acknowledging survivors still adds no commit",
			f.Head(), before)
	}
	if got := f.RefSHA(archivePattern(slug) + "/" + f.Short(before)); got != before {
		t.Errorf("the acknowledged completion archived %v, want HEAD %s", got, before)
	}
}

// PRD §9.5 step 1: a clean working tree is required.
//
// The plan's exit-code table lists a dirty tree as a business-rule refusal (1): the
// command was used correctly and the repository state refused it.
func TestChangeCompleteRefusesDirtyWorkingTree(t *testing.T) {
	f, slug, _, _ := approvedChangeset(t)
	f.Write("service.go", "package main\n\n// uncommitted\nfunc Lock() {}\n")

	res := runIn(t, f.Dir(), "change", "complete")
	if res.code != exitRefusal {
		t.Errorf("dirty tree exited %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "clean", "the refusal must say the tree must be clean")
	if refs := f.RefNames(archivePattern(slug)); len(refs) != 0 {
		t.Errorf("a refused completion wrote archive refs: %v", refs)
	}
}

// An implementation commit after the approval leaves the approval as the reported state,
// but the head no longer carries what was reviewed, so completing refuses (PRD §9.5, §12).
func TestChangeCompleteRefusedAfterImplementationCommitFollowsApprove(t *testing.T) {
	f, slug, _, _ := approvedChangeset(t)
	f.Commit("agent: one more change", gittest.WithFile("service.go", "package main\n\nfunc Lock() { retry() }\n"))

	res := runIn(t, f.Dir(), "change", "complete")
	if res.code != exitRefusal {
		t.Errorf("completing after a post-approval commit exited %d, want %d\nstderr: %s",
			res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "code changed since review", "the refusal must name the drift, not just the state")
	if got := runIn(t, f.Dir(), "status", "--json").json(t)["state"]; got != "APPROVED" {
		t.Errorf("state = %v, want APPROVED: the commit moved the head, not the state", got)
	}
	if refs := f.RefNames(archivePattern(slug)); len(refs) != 0 {
		t.Errorf("the refused completion wrote archive refs: %v", refs)
	}
}

// PRD §9.5: "It never merges, pushes, or squashes." Observed in git state: no branch
// moves at all, no merge commit appears, and the only new refs are git-pair's own.
func TestChangeCompleteDoesNotMergePushOrSquash(t *testing.T) {
	f, slug, _, approve := approvedChangeset(t)
	branch := f.CurrentBranch()
	mainBefore := f.RevParse("main")
	headsBefore := f.Refs("refs/heads")

	runIn(t, f.Dir(), "change", "complete").mustSucceed(t, "change", "complete")

	if got := f.RevParse("main"); got != mainBefore {
		t.Errorf("complete moved main from %s to %s", mainBefore, got)
	}
	for _, before := range headsBefore {
		if got := f.RefSHA(before.Name); got != before.SHA {
			t.Errorf("complete moved %s from %s to %s", before.Name, before.SHA, got)
		}
	}
	if f.CurrentBranch() != branch {
		t.Errorf("complete left the repository on %s, want %s", f.CurrentBranch(), branch)
	}
	// Nothing was squashed: every commit in the archived chain is still a commit, and
	// none of them is a merge.
	for _, sha := range f.RevList(reviewRef(slug)) {
		if parents := f.ParentCount(sha); parents > 1 {
			t.Errorf("%s has %d parents: git-pair created a merge commit", sha, parents)
		}
	}
	if got := f.RevListCount(reviewRef(slug)); got < 5 {
		t.Errorf("the review ref reaches %d commits, want the unsquashed chain", got)
	}
	// The only refs git-pair created are the movable ref and the archive ref.
	for _, ref := range f.RefNames("refs/reviews") {
		if ref != reviewRef(slug) && ref != archivePattern(slug)+"/"+f.Short(approve) {
			t.Errorf("unexpected ref created by complete: %s", ref)
		}
	}
}

// Completing the same head twice is a no-op rather than a refusal: the operation is
// "this head's review history is archived", and archive refs are immutable, so there
// is nothing for a second run to do and nothing for it to undo (PRD §13).
func TestChangeCompleteIsIdempotent(t *testing.T) {
	f, slug, _, approve := approvedChangeset(t)

	runIn(t, f.Dir(), "change", "complete").mustSucceed(t, "change", "complete")
	archiveBefore := f.RefNames(archivePattern(slug))
	if len(archiveBefore) != 1 || archiveBefore[0] != archivePattern(slug)+"/"+f.Short(approve) {
		t.Fatalf("archive refs = %v, want %s/%s", archiveBefore, archivePattern(slug), f.Short(approve))
	}
	archiveSHA := f.RefSHA(archiveBefore[0])
	headBefore := f.Head()

	res := runIn(t, f.Dir(), "change", "complete").mustSucceed(t, "change", "complete")
	mustContain(t, res.stdout, "already points at",
		"the second completion must say the archive was already there")

	if got := f.RefNames(archivePattern(slug)); len(got) != len(archiveBefore) {
		t.Errorf("archive refs = %v, want the single original %v", got, archiveBefore)
	}
	if got := f.RefSHA(archiveBefore[0]); got != archiveSHA {
		t.Errorf("the archive ref moved from %s to %s; it is immutable", archiveSHA, got)
	}
	if f.Head() != headBefore {
		t.Errorf("the second completion moved HEAD from %s to %s", headBefore, f.Head())
	}

	out := runIn(t, f.Dir(), "change", "complete", "--json").json(t)
	if out["archive_created"] != false {
		t.Errorf("archive_created = %v, want false: the archive already existed", out["archive_created"])
	}
}

// The JSON contract an agent reads. `state` stays the derived state rather than naming
// completion, because completion is the archive ref this output reports.
func TestChangeCompleteJSONContract(t *testing.T) {
	f, slug, _, approve := approvedChangeset(t)

	out := runIn(t, f.Dir(), "change", "complete", "--json").
		mustSucceed(t, "change", "complete", "--json").json(t)

	want := map[string]any{
		"changeset":                     slug,
		"state":                         "APPROVED",
		"head":                          approve,
		"short":                         f.Short(approve),
		"base":                          "main",
		"review_ref":                    reviewRef(slug),
		"archive_ref":                   archivePattern(slug) + "/" + f.Short(approve),
		"archive_created":               true,
		"squash_safe":                   true,
		"acknowledged_survivors":        float64(0),
		"acknowledged_unreviewed_paths": float64(0),
		"surviving_review_artifacts":    float64(0),
	}
	for key, w := range want {
		if got := out[key]; got != w {
			t.Errorf("%s = %v, want %v", key, got, w)
		}
	}
	if len(out) != len(want) {
		t.Errorf("the contract has %d keys, want %d: %v", len(out), len(want), out)
	}
}

// Completion leaves no commit, so the archive ref is the only trace of it. `status`
// reports it while HEAD is that archived commit, and stops as soon as other work
// lands: an archive of an ancestor is not a statement that the work is finished.
func TestStatusReportsTheArchiveRefOnlyAtTheCompletedHead(t *testing.T) {
	f, slug, _, approve := approvedChangeset(t)
	wantArchive := archivePattern(slug) + "/" + f.Short(approve)

	if got := runIn(t, f.Dir(), "status", "--json").json(t)["archive_ref"]; got == wantArchive {
		t.Fatalf("archive_ref = %v before completion", got)
	}
	human := runIn(t, f.Dir(), "status").mustSucceed(t, "status")
	mustNotContain(t, human.stdout, wantArchive, "status before completion")

	runIn(t, f.Dir(), "change", "complete").mustSucceed(t, "change", "complete")

	got := runIn(t, f.Dir(), "status", "--json").json(t)
	if got["archive_ref"] != wantArchive {
		t.Errorf("archive_ref = %v, want %s", got["archive_ref"], wantArchive)
	}
	next, _ := got["next_action"].(string)
	mustContain(t, next, "safe to squash/merge", "the next step must change once HEAD is archived")
	mustContain(t, next, wantArchive, "the next step must name the archive it means")
	human = runIn(t, f.Dir(), "status").mustSucceed(t, "status")
	mustContain(t, human.stdout, wantArchive, "status must print the archive ref under Review archive")

	// New work makes the archive a statement about an ancestor.
	f.Commit("agent: one more change", gittest.WithFile("service.go", "package main\n\nfunc Lock() { retry() }\n"))
	after := runIn(t, f.Dir(), "status", "--json").json(t)
	if ref, _ := after["archive_ref"].(string); ref != "" {
		t.Errorf("archive_ref = %q after new work, want it empty", ref)
	}
	if !strings.Contains(after["next_action"].(string), "the head moved since the review") {
		t.Errorf("next_action = %v, want the drift named rather than a command that would refuse", after["next_action"])
	}
}

// The drift the flag exists for: content outside the changeset directory arrived on top of
// an approval, and the owner decides it is not worth a second review. The acknowledgement is
// printed and counted, because the archive still names this head.
func TestChangeCompleteAcknowledgesUnreviewedChanges(t *testing.T) {
	f, slug, _, _ := approvedChangeset(t)
	f.Commit("author: fix a typo in the readme", gittest.WithFile("README.md", "# booking\n\nFixed a typo.\n"))

	refused := runIn(t, f.Dir(), "change", "complete")
	if refused.code != exitRefusal {
		t.Fatalf("completing drifted work exited %d, want %d", refused.code, exitRefusal)
	}

	res := runIn(t, f.Dir(), "change", "complete", "--allow-unreviewed-changes", "--json")
	res.mustSucceed(t, "change", "complete", "--allow-unreviewed-changes")
	out := res.json(t)
	if out["acknowledged_unreviewed_paths"] != float64(1) {
		t.Errorf("acknowledged_unreviewed_paths = %v, want 1", out["acknowledged_unreviewed_paths"])
	}
	head := f.Head()
	if out["head"] != head {
		t.Errorf("head = %v, want the drifted head %s", out["head"], head)
	}
	if got := f.RefSHA(archivePattern(slug) + "/" + f.Short(head)); got != head {
		t.Errorf("the archive points at %s, want the head that was acknowledged", got)
	}

	human := runIn(t, f.Dir(), "change", "complete", "--allow-unreviewed-changes")
	human.mustSucceed(t, "change", "complete", "--allow-unreviewed-changes")
	mustContain(t, human.stdout, "Acknowledged 1 path(s) outside changesets/",
		"the human output must say what was completed over")
}

// `change unready` is a decision, not drift. With the newest marker being the withdrawal, no
// flag talks the changeset back into a state where completion applies, or the explicit act
// would be outranked by an implicit one.
func TestChangeCompleteDriftFlagCannotUndoAWithdrawal(t *testing.T) {
	f, _, _, _ := approvedChangeset(t)
	runIn(t, f.Dir(), "change", "unready").mustSucceed(t, "change", "unready")
	f.Commit("author: keep going", gittest.WithFile("service.go", "package main\n\nfunc Lock() { retry() }\n"))

	res := runIn(t, f.Dir(), "change", "complete", "--allow-unreviewed-changes")
	if res.code != exitRefusal {
		t.Errorf("completing a withdrawn changeset exited %d, want %d\nstderr: %s",
			res.code, exitRefusal, res.stderr)
	}
	// The reason names the withdrawal as the newest marker: "code changed since unready
	// <sha>" — the thing standing between this head and a reviewable state is the act.
	mustContain(t, res.stderr, "since unready", "the refusal must name the withdrawal as the newest marker")
}

// The flag covers drift over an approval, never a reviewer's block: that is a verdict to
// answer, not a detail to acknowledge.
func TestChangeCompleteDriftFlagDoesNotOverrideABlock(t *testing.T) {
	f, _ := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	f.Write("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n")
	submit(t, f, "block")
	f.Commit("author: unrelated work", gittest.WithFile("handler.go", "package main\n\nfunc Serve() { log() }\n"))

	res := runIn(t, f.Dir(), "change", "complete", "--allow-unreviewed-changes")
	if res.code != exitRefusal {
		t.Errorf("completing a blocked changeset exited %d, want %d\nstderr: %s",
			res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "block", "the refusal must name the outcome that withholds integration")
}
