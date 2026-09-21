package cli_test

import (
	"fmt"
	"testing"

	"gitpair/internal/gittest"
)

// The command's whole purpose: an author who readied a changeset and wants to keep
// implementing withdraws the offer with a command, and the queue drops it then rather
// than when a diff happens to make the readiness untenable.
func TestChangeUnreadyTakesAReadyChangesetOutOfTheQueue(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	if !queueListsChangeset(t, runIn(t, f.Dir(), "review", "queue", "--json"), slug) {
		t.Fatal("the fixture is not in the queue before unready")
	}
	readyCommit := f.Head()
	commits := f.RevListCount("HEAD")

	res := runIn(t, f.Dir(), "change", "unready").mustSucceed(t, "change", "unready")

	if queueListsChangeset(t, runIn(t, f.Dir(), "review", "queue", "--json"), slug) {
		t.Error("the changeset is still in the review queue after unready")
	}
	marker := f.Head()
	if marker == readyCommit {
		t.Fatal("unready recorded no marker: the withdrawal has to be a fact in history")
	}
	if got := f.RevListCount("HEAD"); got != commits+1 {
		t.Errorf("HEAD reaches %d commits, want %d: unready writes exactly one marker", got, commits+1)
	}
	if got := f.Subject(marker); got != "git-pair: unready "+slug {
		t.Errorf("marker subject = %q, want %q", got, "git-pair: unready "+slug)
	}
	trailers := f.Trailers(marker)
	if trailers["Review-State"] != "working" {
		t.Errorf("Review-State = %q, want working", trailers["Review-State"])
	}
	if trailers["Review-Changeset"] != slug {
		t.Errorf("Review-Changeset = %q, want %s", trailers["Review-Changeset"], slug)
	}
	mustContain(t, res.stdout, "Unready: "+slug, "unready must confirm the changeset it withdrew")

	// The withdrawal is the marker and nothing else. It used to move the archive ref too, so that a
	// reader of the anchor would not report work as offered after its author took it back; the queue
	// and `check` read the marker itself now, and the durable refs wait for a landing.
	if got := durableRefs(t, f); len(got) != 0 {
		t.Errorf("`change unready` wrote %v for %s", got, slug)
	}

	status := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json").json(t)
	if status["state"] != "WORKING" {
		t.Errorf("state = %v, want WORKING", status["state"])
	}
	// The value has to be recognised as a marker. An unrecognised `Review-State: working`
	// would read as an implementation commit, which is the same answer by accident and
	// hides the withdrawal from anyone reading the reason.
	mustContain(t, fmt.Sprint(status["reason"]), "marked unready by", "status must name the retraction as the newest marker")
	if got, ok := status["unrecognised_markers"]; ok {
		t.Errorf("the working marker was not recognised: %v", got)
	}
	if !f.Clean() {
		t.Error("unready left the working tree dirty")
	}
}

// An author who never readied the changeset has nothing to withdraw. Refusing would
// make every script that unready-before-working handle an error for the common case,
// so the command succeeds and records nothing.
func TestChangeUnreadyWithNothingToWithdrawRecordsNothing(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	before := f.Head()
	commits := f.RevListCount("HEAD")

	res := runIn(t, f.Dir(), "change", "unready", "--json").mustSucceed(t, "change", "unready")
	out := res.json(t)

	if out["recorded"] != false {
		t.Errorf("recorded = %v, want false", out["recorded"])
	}
	if out["state"] != "WORKING" || out["was"] != "WORKING" {
		t.Errorf("state/was = %v/%v, want WORKING/WORKING", out["state"], out["was"])
	}
	if f.Head() != before || f.RevListCount("HEAD") != commits {
		t.Errorf("unready wrote a marker (%s -> %s) when there was nothing to withdraw", before, f.Head())
	}
	// Moving nothing also means creating nothing. The ref is written when a changeset is offered; a
	// withdrawal with nothing to withdraw must not fabricate an archive for work nobody has seen.
	if f.HasRef(archiveRef(slug)) {
		t.Error("a no-op withdrawal created the archive ref")
	}
	mustContain(t, runIn(t, f.Dir(), "change", "unready").stdout, "nothing to withdraw",
		"the human output must say why it did nothing")
}

// The consequence of the withdrawal being a marker rather than a marker plus a ref: the branch that
// carries it is what makes it readable. While the branch stands, `status` names the retraction as the
// newest marker; once the branch is deleted there is nothing left to read, because the durable refs are
// written for work that landed and not for work that was taken back.
//
// The old version of this test asserted the opposite — that the anchor held the withdrawal — which was
// one of the reasons four commands had to maintain a ref. What the queue guarantees is unchanged either
// way: a withdrawn changeset is not in it, with a branch or without one.
func TestChangeUnreadyWithdrawalIsReadableWhileTheBranchStands(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	runIn(t, f.Dir(), "change", "unready").mustSucceed(t, "change", "unready")

	out := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)
	if out["state"] != "WORKING" {
		t.Errorf("state = %v, want WORKING: the withdrawal supersedes the offer", out["state"])
	}
	mustContain(t, fmt.Sprint(out["reason"]), "marked unready by",
		"and names the retraction as the newest marker rather than an offer")
	if out["archive_ref"] != "" {
		t.Errorf("archive_ref = %v, want empty: the marker is the whole record", out["archive_ref"])
	}
	if queueListsChangeset(t, runIn(t, f.Dir(), "review", "queue", "--json"), slug) {
		t.Error("the withdrawn changeset is in the queue with its branch standing")
	}

	f.SwitchTo("main")
	f.ForceDeleteBranch("booking-transaction")
	if queueListsChangeset(t, runIn(t, f.Dir(), "review", "queue", "--json"), slug) {
		t.Error("the withdrawn changeset is in the queue with its branch deleted")
	}
	res := runIn(t, f.Dir(), "status", "--changeset", slug)
	if res.code == 0 {
		t.Errorf("status read a withdrawn changeset whose branch is gone; the marker was in it:\n%s", res.stdout)
	}
	mustContain(t, res.stderr, slug, "the refusal names the changeset it could not resolve")
}

// A BLOCKED changeset is not in the queue either, and retracting a reviewer's block is
// not what unready means. The state has to stay BLOCKED: flattening it to WORKING would
// hide a request the author has not answered.
func TestChangeUnreadyLeavesABlockedChangesetBlocked(t *testing.T) {
	f, _ := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	submit(t, f, "block")
	before := f.Head()

	out := runIn(t, f.Dir(), "change", "unready", "--json").mustSucceed(t, "change", "unready").json(t)

	if out["recorded"] != false {
		t.Error("unready recorded a marker against a blocked changeset")
	}
	if out["state"] != "BLOCKED" || out["was"] != "BLOCKED" {
		t.Errorf("state/was = %v/%v, want BLOCKED/BLOCKED", out["state"], out["was"])
	}
	if f.Head() != before {
		t.Errorf("HEAD moved %s -> %s without recording anything", before, f.Head())
	}
}

// Withdrawing an approved changeset is the author saying the reviewed state is not what
// they are taking forward. The approval stays in history, but it no longer permits
// completion, and a fresh review is needed.
func TestChangeUnreadyFromApprovedRequiresAFreshReview(t *testing.T) {
	f, _, _, _ := approvedChangeset(t)
	runIn(t, f.Dir(), "change", "unready").mustSucceed(t, "change", "unready")

	if got := runIn(t, f.Dir(), "status", "--json").json(t)["state"]; got != "WORKING" {
		t.Fatalf("state = %v, want WORKING after withdrawing an approved changeset", got)
	}
	res := runIn(t, f.Dir(), "check")
	if res.code != exitRefusal {
		t.Errorf("checking a withdrawn changeset exited %d, want %d\nstdout: %s\nstderr: %s",
			res.code, exitRefusal, res.stdout, res.stderr)
	}
	mustContain(t, res.stdout, "took the changeset out of review",
		"the gate must say the withdrawal is why, and name the marker that did it")
	if got := durableRefs(t, f); len(got) != 0 {
		t.Errorf("a withdrawn changeset has nothing to record, and yet: %v", got)
	}

	// The approval is history, not a fact to be edited: unready adds a marker, it does
	// not rewrite the review.
	if got := runIn(t, f.Dir(), "status", "--json").json(t)["latest_review"]; got == nil {
		t.Error("unready erased the approval from status; a withdrawal supersedes, it does not delete")
	}
	// A fresh review is what the withdrawal asked for, and the gate says so: the approval describes
	// HEAD again, so the changeset is integrable and nothing about the earlier withdrawal still
	// applies. There is no ref to advance afterwards, because the record is written at landing.
	submit(t, f, "approve")
	runIn(t, f.Dir(), "check").mustSucceed(t, "check")
}

// Taking a changeset out of the queue is not a way past the gate on the way back in: a
// review addition that still survives unchanged has to be resolved or acknowledged, the
// same as for the first ready. This also records that a reviewer may still submit
// against an unready changeset, which is what makes the gate reachable at all.
func TestChangeUnreadyDoesNotSkipTheReviewGate(t *testing.T) {
	f, _ := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	runIn(t, f.Dir(), "change", "unready").mustSucceed(t, "change", "unready")

	f.Write("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n")
	submit(t, f, "block")

	res := runIn(t, f.Dir(), "change", "ready")
	if res.code != exitRefusal {
		t.Fatalf("readying with a surviving review addition exited %d, want %d\nstderr: %s",
			res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "survive", "the refusal must name the surviving additions")

	f.Commit("use a transaction", gittest.WithFile("service.go", "package main\n\nfunc Lock() { transaction() }\n"))
	ready(t, f)
}

// The marker is a commit, so a dirty tree is refused the same way `change ready` refuses
// it: repository state is exit 1, not a usage error.
func TestChangeUnreadyRefusesADirtyTree(t *testing.T) {
	f, _ := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	before := f.Head()
	f.Write("scratch.go", "package main\n\nvar scratch = true\n")

	res := runIn(t, f.Dir(), "change", "unready")
	if res.code != exitRefusal {
		t.Errorf("unready on a dirty tree exited %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "clean", "the refusal must say what to fix")
	if f.Head() != before {
		t.Errorf("unready committed onto a dirty tree: %s -> %s", before, f.Head())
	}
}

// A second unready has nothing left to withdraw, so it must not leave a second marker
// behind. Marker subjects are stable strings, and consecutive copies of one say nothing.
func TestChangeUnreadyTwiceRecordsOneMarker(t *testing.T) {
	f, _ := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	runIn(t, f.Dir(), "change", "unready").mustSucceed(t, "change", "unready")
	after := f.Head()
	commits := f.RevListCount("HEAD")

	out := runIn(t, f.Dir(), "change", "unready", "--json").mustSucceed(t, "change", "unready").json(t)

	if out["recorded"] != false {
		t.Errorf("the second unready recorded a marker: %v", out)
	}
	if f.Head() != after || f.RevListCount("HEAD") != commits {
		t.Errorf("repeating unready wrote another marker: %s -> %s", after, f.Head())
	}
}
