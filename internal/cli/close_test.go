package cli_test

import (
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

// PRD §10.7 step 2: closing requires the latest effective review outcome to permit
// integration. A blocked review does not, and nothing may be archived.
func TestReviewCloseRefusesNonPermittedOutcome(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	f.Write("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n")
	submit(t, f, "block")
	before := f.Head()

	res := runIn(t, f.Dir(), "review", "close")
	if res.code != exitRefusal {
		t.Errorf("closing a blocked changeset exited %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "block", "the refusal must name the outcome that blocks integration")
	if f.Head() != before {
		t.Errorf("close created a commit (%s -> %s) despite refusing", before, f.Head())
	}
	if refs := f.RefNames(archivePattern(slug)); len(refs) != 0 {
		t.Errorf("a refused close wrote archive refs: %v", refs)
	}
	if got := runIn(t, f.Dir(), "status", "--json").json(t)["state"]; got != "BLOCKED" {
		t.Errorf("state = %v, want BLOCKED", got)
	}

	// Feedback and approve are the permitted outcomes (PRD §23).
	f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { transaction() }\n"))
	ready(t, f)
	submit(t, f, "feedback")
	runIn(t, f.Dir(), "review", "close").mustSucceed(t, "review", "close")
}

// PRD §10.7: close anchors the history, writes a durable final review ref, marks the
// changeset closed, and prints the archive ref plus integration readiness.
func TestReviewCloseArchivesAndMarksClosed(t *testing.T) {
	f, slug, _, approve := approvedChangeset(t)

	res := runIn(t, f.Dir(), "review", "close").mustSucceed(t, "review", "close")

	head := f.Head()
	if head == approve {
		t.Fatal("close created no commit")
	}
	if got := f.Subject(head); got != "git-pair: close "+slug {
		t.Errorf("subject = %q, want %q", got, "git-pair: close "+slug)
	}
	trailers := f.Trailers(head)
	if trailers["GitPR-State"] != "closed" {
		t.Errorf("GitPR-State = %q, want closed", trailers["GitPR-State"])
	}
	if trailers["GitPR-Changeset"] != slug {
		t.Errorf("GitPR-Changeset = %q, want %q", trailers["GitPR-Changeset"], slug)
	}

	// The immutable archive ref names the archived commit and points at it exactly.
	wantArchive := archivePattern(slug) + "/" + f.Short(approve)
	if got := f.RefSHA(wantArchive); got != approve {
		t.Errorf("%s = %s, want the approved HEAD %s", wantArchive, got, approve)
	}
	mustContain(t, res.stdout, wantArchive, "close must print the archive ref (PRD §10.7)")
	mustContain(t, res.stdout, "Safe to squash/merge", "close must report integration readiness (PRD §10.7)")

	// The movable ref keeps the close marker itself reachable.
	if got := f.RefSHA(reviewRef(slug)); got != head {
		t.Errorf("%s = %s, want the close marker %s", reviewRef(slug), got, head)
	}
	if !f.ReachableFrom(approve, reviewRef(slug)) {
		t.Error("the approved chain is no longer reachable from the movable review ref")
	}

	// The close marker is the commit that closes the changeset (plan M4).
	if got := f.Trailers(head)["GitPR-Changeset"]; got != slug {
		t.Errorf("close marker changeset = %q", got)
	}
	if got := runIn(t, f.Dir(), "status", "--json").json(t); got["state"] != "CLOSED" {
		t.Errorf("state = %v, want CLOSED", got["state"])
	}
	if queueListsChangeset(t, runIn(t, f.Dir(), "review", "queue", "--json"), slug) {
		t.Error("a closed changeset is still in the review queue")
	}
	if !f.Clean() {
		t.Error("close left the working tree dirty")
	}
}

// PRD §13's invariant, and the plan's M4 verification: after the branch is deleted the
// complete unsquashed chain must still be reachable from the archive ref.
func TestReviewCloseKeepsFullChainReachableAfterBranchDeletion(t *testing.T) {
	f, slug, reviewed, approve := approvedChangeset(t)
	chain := f.RevList("HEAD")

	runIn(t, f.Dir(), "review", "close").mustSucceed(t, "review", "close")
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

// PRD §19.3: the same surviving-additions check must run before closing.
func TestReviewCloseSurvivalGate(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)

	const comment = "// Please name this variable"
	f.Write("service.go", "package main\n\n"+comment+"\nfunc Lock() {}\n")
	submit(t, f, "feedback") // non-blocking outcome, so the survival gate is what refuses
	before := f.Head()

	res := runIn(t, f.Dir(), "review", "close")
	if res.code != exitRefusal {
		t.Fatalf("closing with a surviving review addition exited %d, want %d\nstderr: %s",
			res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "service.go", "the report must name the file")
	mustContain(t, res.stderr, comment, "the report must print the surviving addition")
	mustContain(t, res.stderr, "git pair review close --allow-surviving-review-additions",
		"PRD §19.3 names the override")
	if f.Head() != before {
		t.Error("a refused close created a commit")
	}
	if refs := f.RefNames(archivePattern(slug)); len(refs) != 0 {
		t.Errorf("a refused close wrote archive refs: %v", refs)
	}

	// The explicit acknowledgement lets the reviewer close anyway.
	runIn(t, f.Dir(), "review", "close", "--allow-surviving-review-additions").
		mustSucceed(t, "review", "close", "--allow-surviving-review-additions")
	if got := f.Subject(f.Head()); got != "git-pair: close booking" {
		t.Errorf("HEAD = %q, want the close marker", got)
	}
}

// PRD §10.7 step 1: a clean working tree is required.
//
// The plan's exit-code table lists a dirty tree as a business-rule refusal (1): the
// command was used correctly and the repository state refused it.
func TestReviewCloseRefusesDirtyWorkingTree(t *testing.T) {
	f, slug, _, _ := approvedChangeset(t)
	f.Write("service.go", "package main\n\n// uncommitted\nfunc Lock() {}\n")

	res := runIn(t, f.Dir(), "review", "close")
	if res.code != exitRefusal {
		t.Errorf("dirty tree exited %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "clean", "the refusal must say the tree must be clean")
	if refs := f.RefNames(archivePattern(slug)); len(refs) != 0 {
		t.Errorf("a refused close wrote archive refs: %v", refs)
	}
}

// An implementation commit after the approval invalidates it, so closing must refuse
// until the changeset is reviewed again (PRD §12, §23).
func TestReviewCloseRefusedAfterImplementationCommitFollowsApprove(t *testing.T) {
	f, slug, _, _ := approvedChangeset(t)
	f.Commit("agent: one more change", gittest.WithFile("service.go", "package main\n\nfunc Lock() { retry() }\n"))

	res := runIn(t, f.Dir(), "review", "close")
	if res.code != exitRefusal {
		t.Errorf("closing after a post-approval commit exited %d, want %d\nstderr: %s",
			res.code, exitRefusal, res.stderr)
	}
	if got := runIn(t, f.Dir(), "status", "--json").json(t)["state"]; got != "WORKING" {
		t.Errorf("state = %v, want WORKING", got)
	}
	if refs := f.RefNames(archivePattern(slug)); len(refs) != 0 {
		t.Errorf("the refused close wrote archive refs: %v", refs)
	}
}

// PRD §10.7: "It must not merge, push, or squash by default." Observed in git state:
// no other branch moves, no merge commit appears, and the only new refs are git-pair's
// own review refs.
func TestReviewCloseDoesNotMergePushOrSquash(t *testing.T) {
	f, slug, _, approve := approvedChangeset(t)
	mainBefore := f.RevParse("main")
	headsBefore := f.Refs("refs/heads")

	runIn(t, f.Dir(), "review", "close").mustSucceed(t, "review", "close")

	if got := f.RevParse("main"); got != mainBefore {
		t.Errorf("close moved main from %s to %s", mainBefore, got)
	}
	for _, before := range headsBefore {
		if before.Name == "refs/heads/"+f.CurrentBranch() {
			continue
		}
		if got := f.RefSHA(before.Name); got != before.SHA {
			t.Errorf("close moved %s from %s to %s", before.Name, before.SHA, got)
		}
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
			t.Errorf("unexpected ref created by close: %s", ref)
		}
	}
}

// Closing twice must not rewrite or duplicate the archive (PRD §13's immutability).
func TestReviewCloseIsIdempotentAboutTheArchiveRef(t *testing.T) {
	f, slug, _, approve := approvedChangeset(t)

	runIn(t, f.Dir(), "review", "close").mustSucceed(t, "review", "close")
	archiveBefore := f.RefNames(archivePattern(slug))
	if len(archiveBefore) != 1 || archiveBefore[0] != archivePattern(slug)+"/"+f.Short(approve) {
		t.Fatalf("archive refs = %v, want %s/%s", archiveBefore, archivePattern(slug), f.Short(approve))
	}
	archiveSHA := f.RefSHA(archiveBefore[0])

	res := runIn(t, f.Dir(), "review", "close")
	if res.code == exitOK {
		t.Error("closing an already-closed changeset reported success")
	}
	if got := f.RefNames(archivePattern(slug)); len(got) != len(archiveBefore) {
		t.Errorf("archive refs = %v, want the single original %v", got, archiveBefore)
	}
	if got := f.RefSHA(archiveBefore[0]); got != archiveSHA {
		t.Errorf("the archive ref moved from %s to %s; it is immutable", archiveSHA, got)
	}
	if !strings.Contains(res.stderr, "closed") {
		t.Logf("note: second close said: %s", res.stderr)
	}
}
