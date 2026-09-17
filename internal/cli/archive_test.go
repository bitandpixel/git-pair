package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// approvedChangeset drives a changeset to APPROVED on a clean tree and returns the
// fixture, the slug, the reviewed HEAD and the approval commit. The approval leaves the
// archive ref pointing at HEAD, which is the normal state §11 of the requirements describes.
func approvedChangeset(t *testing.T) (*gittest.Fixture, string, string, string) {
	t.Helper()
	f, slug := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	reviewed := f.Head()
	submit(t, f, "approve")
	return f, slug, reviewed, f.Head()
}

// archivedAt is where the changeset's one durable ref points, which is the whole observable
// effect of `change archive`.
func archivedAt(f *gittest.Fixture, slug string) string { return f.RefSHA(archiveRef(slug)) }

// PRD §9.5 step 2: archiving requires the latest effective review outcome to permit
// integration. A blocked review does not, and the archive must not move.
func TestChangeArchiveRefusesNonPermittedOutcome(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	f.Write("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n")
	submit(t, f, "block")
	before := f.Head()
	beforeArchive := archivedAt(f, slug)

	res := runIn(t, f.Dir(), "change", "archive")
	if res.code != exitRefusal {
		t.Errorf("archiving a blocked changeset exited %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "block", "the refusal must name the outcome that blocks integration")
	if f.Head() != before {
		t.Errorf("archive moved HEAD (%s -> %s) despite refusing", before, f.Head())
	}
	if got := archivedAt(f, slug); got != beforeArchive {
		t.Errorf("%s moved from %s to %s despite refusing", archiveRef(slug), beforeArchive, got)
	}
	if got := runIn(t, f.Dir(), "status", "--json").json(t)["state"]; got != "BLOCKED" {
		t.Errorf("state = %v, want BLOCKED", got)
	}

	// Feedback and approve are the permitted outcomes (PRD §23): feedback is
	// non-blocking, so it lets the owner proceed without a second verdict.
	f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { transaction() }\n"))
	ready(t, f)
	submit(t, f, "feedback")
	runIn(t, f.Dir(), "change", "archive").mustSucceed(t, "change", "archive")
}

// PRD §9.5: archiving moves one ref and nothing else. It records no commit and establishes no
// state — the merge that finishes the changeset is ordinary git, and `status` keeps saying
// APPROVED until it happens.
func TestChangeArchiveAdvancesTheRefWithoutCommitting(t *testing.T) {
	f, slug, _, approve := approvedChangeset(t)
	before := f.RevListCount("HEAD")

	res := runIn(t, f.Dir(), "change", "archive").mustSucceed(t, "change", "archive")

	if f.Head() != approve {
		t.Errorf("HEAD = %s, want the approved head %s: archiving records no commit", f.Head(), approve)
	}
	if got := f.RevListCount("HEAD"); got != before {
		t.Errorf("HEAD reaches %d commits, want the %d that were there before archiving", got, before)
	}
	if got := archivedAt(f, slug); got != approve {
		t.Errorf("%s = %s, want the approved HEAD %s", archiveRef(slug), got, approve)
	}
	mustContain(t, res.stdout, archiveRef(slug), "archive must print the ref it moved (PRD §9.5)")
	mustContain(t, res.stdout, "Safe to squash/merge", "archive must report integration readiness (PRD §9.5)")

	if got := runIn(t, f.Dir(), "status", "--json").json(t)["state"]; got != "APPROVED" {
		t.Errorf("state = %v, want APPROVED: archiving is not a lifecycle state", got)
	}
	if queueListsChangeset(t, runIn(t, f.Dir(), "review", "queue", "--json"), slug) {
		t.Error("an archived changeset is still in the review queue")
	}
	if !f.Clean() {
		t.Error("archive left the working tree dirty")
	}
}

// The case the command exists for: a review-artifact commit after the approval. The ref was
// last moved by the review submission, so it stops short of HEAD, and what sits between them is
// a ABOUT.md edit — a review artifact, not implementation. Advancing over it is the whole point;
// the archived chain still contains the reviewed head.
func TestChangeArchiveAdvancesOverChangesetOnlyCommits(t *testing.T) {
	f, slug, _, approve := approvedChangeset(t)
	about := gittest.WithFile(filepath.Join("changesets", slug, "ABOUT.md"),
		"# Booking transaction\n\n## Validation\n\n`go test ./...` and a staging run.\n")
	f.Commit("author: record the validation run", about)
	head := f.Head()
	if got := archivedAt(f, slug); got != approve {
		t.Fatalf("precondition: %s = %s, want the review commit %s", archiveRef(slug), got, approve)
	}

	runIn(t, f.Dir(), "change", "archive").mustSucceed(t, "change", "archive")

	if got := archivedAt(f, slug); got != head {
		t.Errorf("%s = %s, want the archived head %s", archiveRef(slug), got, head)
	}
	if !f.ReachableFrom(approve, archiveRef(slug)) {
		t.Error("the approved chain is no longer reachable from the archive ref")
	}
}

// PRD §13's invariant, and the archival promise the command exists to keep: after the
// branch is deleted the complete unsquashed chain must still be reachable from the archive ref.
func TestChangeArchiveKeepsFullChainReachableAfterBranchDeletion(t *testing.T) {
	f, slug, reviewed, approve := approvedChangeset(t)
	chain := f.RevList("HEAD")

	runIn(t, f.Dir(), "change", "archive").mustSucceed(t, "change", "archive")

	// Simulate the squash/merge finishing the branch: the branch is gone, the commits
	// are referenced by nothing else.
	f.SwitchTo("main")
	f.ForceDeleteBranch("booking-transaction")

	if branches := f.Branches(); len(branches) != 1 || branches[0] != "main" {
		t.Errorf("branches = %v, want only main", branches)
	}
	reachable := map[string]bool{}
	for _, sha := range f.RevList(archiveRef(slug)) {
		reachable[sha] = true
	}
	for _, sha := range chain {
		if !reachable[sha] {
			t.Errorf("%s is not reachable from %s after `git branch -D`", sha, archiveRef(slug))
		}
	}
	// The reviewed implementation state and the approval are both in the archive.
	for _, want := range []string{reviewed, approve} {
		if !reachable[want] {
			t.Errorf("%s is missing from the archive", want)
		}
	}
	// And that chain is the full unsquashed history, not a single squashed commit.
	if count := f.RevListCount(archiveRef(slug)); count < 5 {
		t.Errorf("archive reaches %d commits, want the whole implementation/review chain", count)
	}
	if got := f.RevList(archiveRef(slug)); len(got) != len(chain) {
		t.Errorf("archive reaches %d commits, want the %d committed steps of this changeset", len(got), len(chain))
	}
}

// PRD §19.3: the same surviving-additions check must run before archiving.
func TestChangeArchiveSurvivalGate(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)

	const comment = "// Please name this variable"
	f.Write("service.go", "package main\n\n"+comment+"\nfunc Lock() {}\n")
	submit(t, f, "feedback") // non-blocking outcome, so the survival gate is what refuses
	before := f.Head()
	beforeArchive := archivedAt(f, slug)

	res := runIn(t, f.Dir(), "change", "archive")
	if res.code != exitRefusal {
		t.Fatalf("archiving with a surviving review addition exited %d, want %d\nstderr: %s",
			res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "service.go", "the report must name the file")
	mustContain(t, res.stderr, comment, "the report must print the surviving addition")
	mustContain(t, res.stderr, "git pair change archive --allow-surviving-review-additions",
		"PRD §19.3 names the override")
	if f.Head() != before {
		t.Error("a refused archive moved HEAD")
	}
	if got := archivedAt(f, slug); got != beforeArchive {
		t.Errorf("%s moved from %s to %s despite refusing", archiveRef(slug), beforeArchive, got)
	}

	// The explicit acknowledgement lets the owner archive anyway.
	runIn(t, f.Dir(), "change", "archive", "--allow-surviving-review-additions").
		mustSucceed(t, "change", "archive", "--allow-surviving-review-additions")
	if f.Head() != before {
		t.Errorf("HEAD = %s, want the archived head %s: acknowledging survivors still adds no commit",
			f.Head(), before)
	}
	if got := archivedAt(f, slug); got != before {
		t.Errorf("%s = %s, want HEAD %s", archiveRef(slug), got, before)
	}
}

// PRD §9.5 step 1: a clean working tree is required.
//
// The plan's exit-code table lists a dirty tree as a business-rule refusal (1): the
// command was used correctly and the repository state refused it.
func TestChangeArchiveRefusesDirtyWorkingTree(t *testing.T) {
	f, slug, _, _ := approvedChangeset(t)
	beforeArchive := archivedAt(f, slug)
	f.Write("service.go", "package main\n\n// uncommitted\nfunc Lock() {}\n")

	res := runIn(t, f.Dir(), "change", "archive")
	if res.code != exitRefusal {
		t.Errorf("dirty tree exited %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "clean", "the refusal must say the tree must be clean")
	if got := archivedAt(f, slug); got != beforeArchive {
		t.Errorf("%s moved from %s to %s despite refusing", archiveRef(slug), beforeArchive, got)
	}
}

// An implementation commit after the approval leaves the approval as the reported state,
// but the head no longer carries what was reviewed, so archiving refuses (PRD §9.5, §12).
func TestChangeArchiveRefusedAfterImplementationCommitFollowsApprove(t *testing.T) {
	f, slug, _, _ := approvedChangeset(t)
	beforeArchive := archivedAt(f, slug)
	f.Commit("agent: one more change", gittest.WithFile("service.go", "package main\n\nfunc Lock() { retry() }\n"))

	res := runIn(t, f.Dir(), "change", "archive")
	if res.code != exitRefusal {
		t.Errorf("archiving after a post-approval commit exited %d, want %d\nstderr: %s",
			res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "code changed since review", "the refusal must name the drift, not just the state")
	if got := runIn(t, f.Dir(), "status", "--json").json(t)["state"]; got != "APPROVED" {
		t.Errorf("state = %v, want APPROVED: the commit moved the head, not the state", got)
	}
	if got := archivedAt(f, slug); got != beforeArchive {
		t.Errorf("%s moved from %s to %s: unreviewed implementation must not be archived",
			archiveRef(slug), beforeArchive, got)
	}
}

// PRD §9.5: "It never merges, pushes, or squashes." Observed in git state: no branch
// moves at all, no merge commit appears, and the only new ref is git-pair's own.
func TestChangeArchiveDoesNotMergePushOrSquash(t *testing.T) {
	f, slug, _, _ := approvedChangeset(t)
	// An artifact commit to advance over, so the command has work to do. Everything
	// below is captured after it: what must not move is what the archive refuses to move.
	f.Commit("author: record the validation run",
		gittest.WithFile(filepath.Join("changesets", slug, "ABOUT.md"), "# booking\n\nValidated.\n"))
	branch := f.CurrentBranch()
	mainBefore := f.RevParse("main")
	headsBefore := f.Refs("refs/heads")

	runIn(t, f.Dir(), "change", "archive").mustSucceed(t, "change", "archive")

	if got := f.RevParse("main"); got != mainBefore {
		t.Errorf("archive moved main from %s to %s", mainBefore, got)
	}
	for _, before := range headsBefore {
		if got := f.RefSHA(before.Name); got != before.SHA {
			t.Errorf("archive moved %s from %s to %s", before.Name, before.SHA, got)
		}
	}
	if f.CurrentBranch() != branch {
		t.Errorf("archive left the repository on %s, want %s", f.CurrentBranch(), branch)
	}
	// Nothing was squashed: every commit in the archived chain is still a commit, and
	// none of them is a merge.
	for _, sha := range f.RevList(archiveRef(slug)) {
		if parents := f.ParentCount(sha); parents > 1 {
			t.Errorf("%s has %d parents: git-pair created a merge commit", sha, parents)
		}
	}
	if got := f.RevListCount(archiveRef(slug)); got < 5 {
		t.Errorf("the archive ref reaches %d commits, want the unsquashed chain", got)
	}
	// One durable ref per changeset: nothing else was written under it.
	for _, ref := range f.RefNames("refs/reviews") {
		if ref != archiveRef(slug) {
			t.Errorf("unexpected ref created by change archive: %s", ref)
		}
	}
}

// Archiving the head the archive already names is a no-op rather than a refusal, and says
// so: there is nothing for a second run to do and nothing for it to undo (PRD §13).
func TestChangeArchiveIsIdempotent(t *testing.T) {
	f, slug, _, approve := approvedChangeset(t)

	runIn(t, f.Dir(), "change", "archive").mustSucceed(t, "change", "archive")
	headBefore := f.Head()

	res := runIn(t, f.Dir(), "change", "archive").mustSucceed(t, "change", "archive")
	mustContain(t, res.stdout, "already points there",
		"the second archive must say the ref was already where it wanted it")

	if got := archivedAt(f, slug); got != approve {
		t.Errorf("%s = %s, want it unmoved at %s", archiveRef(slug), got, approve)
	}
	if f.Head() != headBefore {
		t.Errorf("the second archive moved HEAD from %s to %s", headBefore, f.Head())
	}
	out := runIn(t, f.Dir(), "change", "archive", "--json").json(t)
	if out["archive_advanced"] != false {
		t.Errorf("archive_advanced = %v, want false: the ref was already at HEAD", out["archive_advanced"])
	}
	if out["archive_was"] != approve {
		t.Errorf("archive_was = %v, want the commit it already pointed at (%s)", out["archive_was"], approve)
	}
}

// The archive only moves forward. A head behind the current tip — an old checkout, or a
// branch wound back — would drop the archived chain from the one ref keeping it reachable, so
// the command refuses rather than quietly un-archiving work.
func TestChangeArchiveRefusesToMoveBackwards(t *testing.T) {
	f, slug, _, approve := approvedChangeset(t)
	// Advance the archive past the approval with a review-artifact commit.
	f.Commit("author: record the validation run",
		gittest.WithFile(filepath.Join("changesets", slug, "ABOUT.md"), "# booking\n\nValidated.\n"))
	runIn(t, f.Dir(), "change", "archive").mustSucceed(t, "change", "archive")
	ahead := archivedAt(f, slug)

	// Stand where the branch was before that commit: HEAD is now behind the archive.
	f.CreateBranch("older", approve)

	res := runIn(t, f.Dir(), "change", "archive")
	if res.code != exitRefusal {
		t.Fatalf("archiving a head behind the archive exited %d, want %d\nstderr: %s",
			res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "only moves forward", "the refusal must say which way the ref may go")
	mustContain(t, res.stderr, f.Short(ahead), "the refusal must name the commit it would have dropped")
	if got := archivedAt(f, slug); got != ahead {
		t.Errorf("%s = %s, want it unmoved at %s: a refused archive moves nothing", archiveRef(slug), got, ahead)
	}
}

// A rebase is not a move backwards. Rewritten history is neither ahead nor behind, and its
// markers moved with it, so refusing here would strand the archive on commits the branch no
// longer has — the opposite of what keeping it current is for.
func TestChangeArchiveFollowsRewrittenHistory(t *testing.T) {
	f, slug, _, approve := approvedChangeset(t)
	f.Commit("author: record the validation run",
		gittest.WithFile(filepath.Join("changesets", slug, "ABOUT.md"), "# booking\n\nValidated.\n"))
	runIn(t, f.Dir(), "change", "archive").mustSucceed(t, "change", "archive")
	before := archivedAt(f, slug)

	// Rewrite the approval onto the commit before it, as a rebase would. The message
	// differs by a word so the rewritten marker is a distinct commit rather than the
	// byte-identical original; it stays empty, so it is the same verdict on new history.
	f.CreateBranch("rewritten", f.Parent(approve))
	rewritten := f.CommitMessage(
		"review: approve "+slug+" (after rebase)\n\nReview-Outcome: approve\nReview-Changeset: "+slug+"\n",
		gittest.WithEmpty())

	res := runIn(t, f.Dir(), "change", "archive").mustSucceed(t, "change", "archive")
	if got := archivedAt(f, slug); got != rewritten {
		t.Errorf("%s = %s, want the rewritten approval %s (was %s)",
			archiveRef(slug), got, rewritten, before)
	}
	mustContain(t, res.stdout, "→", "the output must show the move it made")
}

// The JSON contract an agent reads. `state` stays the derived state rather than naming
// archiving, because archiving is the ref move this output reports.
func TestChangeArchiveJSONContract(t *testing.T) {
	f, slug, _, approve := approvedChangeset(t)
	f.Commit("author: record the validation run",
		gittest.WithFile(filepath.Join("changesets", slug, "ABOUT.md"), "# booking\n\nValidated.\n"))
	head := f.Head()

	out := runIn(t, f.Dir(), "change", "archive", "--json").
		mustSucceed(t, "change", "archive", "--json").json(t)

	want := map[string]any{
		"changeset":                     slug,
		"state":                         "APPROVED",
		"head":                          head,
		"short":                         f.Short(head),
		"base":                          "main",
		"archive_ref":                   archiveRef(slug),
		"archive_was":                   approve,
		"archive_advanced":              true,
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

// The archive ref is reported whenever it exists, with the commit it names — which is the
// reader's own test for "is what is here what was archived?". The archived next step appears
// only when the answer is yes: an archive of an ancestor is not a statement that work is done.
func TestStatusReportsTheArchiveRefAndWhetherItNamesHead(t *testing.T) {
	f, slug, _, approve := approvedChangeset(t)

	before := runIn(t, f.Dir(), "status", "--json").json(t)
	if before["archive_ref"] != archiveRef(slug) {
		t.Errorf("archive_ref = %v, want %s", before["archive_ref"], archiveRef(slug))
	}
	if before["archive_commit"] != f.Short(approve) {
		t.Errorf("archive_commit = %v, want the approved head %s", before["archive_commit"], f.Short(approve))
	}
	human := runIn(t, f.Dir(), "status").mustSucceed(t, "status")
	mustContain(t, human.stdout, archiveRef(slug), "status must print the archive ref")

	// A review-artifact commit puts HEAD ahead of the archive.
	f.Commit("author: record the validation run",
		gittest.WithFile(filepath.Join("changesets", slug, "ABOUT.md"), "# booking\n\nValidated.\n"))
	moved := runIn(t, f.Dir(), "status", "--json").json(t)
	if moved["archive_commit"] != f.Short(approve) {
		t.Errorf("archive_commit = %v, want the ref's own commit %s, not HEAD", moved["archive_commit"], f.Short(approve))
	}
	mustNotContain(t, moved["next_action"].(string), "safe to squash/merge",
		"the next step must not claim HEAD is archived while the ref is behind it")

	runIn(t, f.Dir(), "change", "archive").mustSucceed(t, "change", "archive")
	archived := f.Head()
	got := runIn(t, f.Dir(), "status", "--json").json(t)
	if got["archive_commit"] != f.Short(archived) {
		t.Errorf("archive_commit = %v, want %s", got["archive_commit"], f.Short(archived))
	}
	next, _ := got["next_action"].(string)
	mustContain(t, next, "safe to squash/merge", "the next step must change once HEAD is archived")
	mustContain(t, next, archiveRef(slug), "the next step must name the archive it means")

	// New work makes the archive a statement about an ancestor again.
	f.Commit("agent: one more change", gittest.WithFile("service.go", "package main\n\nfunc Lock() { retry() }\n"))
	after := runIn(t, f.Dir(), "status", "--json").json(t)
	if after["archive_commit"] != f.Short(archived) {
		t.Errorf("archive_commit = %v, want it to keep naming the archived commit %s",
			after["archive_commit"], f.Short(archived))
	}
	if !strings.Contains(after["next_action"].(string), "the head moved since the review") {
		t.Errorf("next_action = %v, want the drift named rather than a command that would refuse", after["next_action"])
	}
}

// The drift the flag exists for: content outside the changeset directory arrived on top of
// an approval, and the owner decides it is not worth a second review. The acknowledgement is
// printed and counted, because the archive still names this head.
func TestChangeArchiveAcknowledgesUnreviewedChanges(t *testing.T) {
	f, slug, _, _ := approvedChangeset(t)
	f.Commit("author: fix a typo in the readme", gittest.WithFile("README.md", "# booking\n\nFixed a typo.\n"))

	refused := runIn(t, f.Dir(), "change", "archive")
	if refused.code != exitRefusal {
		t.Fatalf("archiving drifted work exited %d, want %d", refused.code, exitRefusal)
	}

	res := runIn(t, f.Dir(), "change", "archive", "--allow-unreviewed-changes", "--json")
	res.mustSucceed(t, "change", "archive", "--allow-unreviewed-changes")
	out := res.json(t)
	if out["acknowledged_unreviewed_paths"] != float64(1) {
		t.Errorf("acknowledged_unreviewed_paths = %v, want 1", out["acknowledged_unreviewed_paths"])
	}
	head := f.Head()
	if out["head"] != head {
		t.Errorf("head = %v, want the drifted head %s", out["head"], head)
	}
	if got := archivedAt(f, slug); got != head {
		t.Errorf("%s = %s, want the head that was acknowledged", archiveRef(slug), got)
	}

	human := runIn(t, f.Dir(), "change", "archive", "--allow-unreviewed-changes")
	human.mustSucceed(t, "change", "archive", "--allow-unreviewed-changes")
	mustContain(t, human.stdout, "Acknowledged 1 path(s) outside changesets/",
		"the human output must say what was archived over")
}

// `change unready` is a decision, not drift. With the newest marker being the withdrawal, no
// flag talks the changeset back into a state where archiving applies, or the explicit act
// would be outranked by an implicit one.
func TestChangeArchiveDriftFlagCannotUndoAWithdrawal(t *testing.T) {
	f, _, _, _ := approvedChangeset(t)
	runIn(t, f.Dir(), "change", "unready").mustSucceed(t, "change", "unready")
	f.Commit("author: keep going", gittest.WithFile("service.go", "package main\n\nfunc Lock() { retry() }\n"))

	res := runIn(t, f.Dir(), "change", "archive", "--allow-unreviewed-changes")
	if res.code != exitRefusal {
		t.Errorf("archiving a withdrawn changeset exited %d, want %d\nstderr: %s",
			res.code, exitRefusal, res.stderr)
	}
	// The reason names the withdrawal as the newest marker: "code changed since unready
	// <sha>" — the thing standing between this head and a reviewable state is the act.
	mustContain(t, res.stderr, "since unready", "the refusal must name the withdrawal as the newest marker")
}

// The flag covers drift over an approval, never a reviewer's block: that is a verdict to
// answer, not a detail to acknowledge.
func TestChangeArchiveDriftFlagDoesNotOverrideABlock(t *testing.T) {
	f, _ := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	f.Write("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n")
	submit(t, f, "block")
	f.Commit("author: unrelated work", gittest.WithFile("handler.go", "package main\n\nfunc Serve() { log() }\n"))

	res := runIn(t, f.Dir(), "change", "archive", "--allow-unreviewed-changes")
	if res.code != exitRefusal {
		t.Errorf("archiving a blocked changeset exited %d, want %d\nstderr: %s",
			res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "block", "the refusal must name the outcome that withholds integration")
}

// A review submission may carry the reviewer's own edits. The reviewed content is the
// marker's tree, not its parent, so those files are not drift the author has to explain —
// and a reply in ABOUT.md afterwards must not turn them into drift.
func TestChangeArchiveDoesNotCountTheReviewersOwnEditsAsDrift(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	f.CommitReviewMarker(slug, "feedback",
		gittest.WithFile("docs/reviewer-note.md", "Please use a transaction here.\n"))
	// The author answers in the changeset directory, which is the one place a commit is
	// always allowed.
	f.Commit("author: addressed the note in ABOUT.md",
		gittest.WithFile(f.ChangesetPath(slug, "ABOUT.md"), "# booking\n\nFixed.\n"))

	res := runIn(t, f.Dir(), "change", "archive", "--allow-surviving-review-additions", "--json")
	res.mustSucceed(t, "change", "archive", "--allow-surviving-review-additions")
	if got := res.json(t)["acknowledged_unreviewed_paths"]; got != float64(0) {
		t.Errorf("acknowledged_unreviewed_paths = %v, want 0: the reviewer's edits are the reviewed content, not drift\nstderr: %s",
			got, res.stderr)
	}
}

// The hatch is for drift and nothing else. A commit whose `Review-State:` git-pair cannot
// honour leaves the tree unable to speak for it, and that refusal survives
// `--allow-unreviewed-changes` — otherwise the flag would archive vocabulary the tool cannot
// interpret, which is what the unreadable-marker rule exists to prevent.
func TestChangeArchiveDriftFlagDoesNotCoverAnUnreadableMarker(t *testing.T) {
	f, _, _, _ := approvedChangeset(t)
	// A commit that announces itself with `Review-State:` in vocabulary this build has
	// retired is the realistic case: git-pair refuses to honour it, and the tree cannot
	// speak for it either.
	f.CommitMessage("git-pair: close booking-transaction\n\nReview-State: closed\nReview-Changeset: booking-transaction\n",
		gittest.WithEmpty())

	res := runIn(t, f.Dir(), "change", "archive", "--allow-unreviewed-changes")
	if res.code != exitRefusal {
		t.Fatalf("archiving over an unreadable marker exited %d, want %d\nstdout: %s",
			res.code, exitRefusal, res.stdout)
	}
	mustContain(t, res.stderr, "unrecognised review marker", "the refusal must name the unreadable marker")
}
