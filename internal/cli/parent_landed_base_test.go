package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// A landed parent usually loses its branch, and the child left behind is measured from a commit the
// destination derives rather than from a branch tip anybody recorded. The gate's question has not changed:
// is the diff under test the diff that was approved? It can only be asked of an approval that wrote down
// where it measured. `Review-Base-Head` is that record, and `Review-Parent-Head` is its older spelling —
// the same commit while the parent's branch was the base, and a different one after a rebase onto the
// landing.
//
// The scenarios below differ only in what the approval managed to write down, and they answer differently
// on purpose: an approval that named its starting point is compared, an approval whose recorded starting
// point predates the landing is refused, and an approval that named nothing is a note rather than a refusal
// (PRD §21).

// landedParentWithADeletedBranch builds the shape a stack arrives at in the ordinary course: trunk moves
// on, `alpha` lands carrying that work with it, `beta` is rebased onto the landing the way the note tells
// it to, and the parent's branch is deleted afterwards. It returns the landing and the parent's old tip —
// the two commits whose difference decides every case here — with `beta` checked out and unreviewed.
func landedParentWithADeletedBranch(t *testing.T) (f *gittest.Fixture, landing, parentTip string) {
	t.Helper()
	f = newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	parentTip = f.RevParse("alpha")
	f.SwitchTo("main")
	f.Commit("trunk moves on", gittest.WithFile("e.go", "package main\n"))
	f.SwitchTo("alpha")
	landing = landAndRecord(t, f, "alpha", "main")
	f.SwitchTo("beta")
	// The command the landed-parent note prints, run by the fixture: the child cannot be measured from a
	// branch that is about to be deleted.
	f.MustGit("rebase", "--onto", landing, "alpha", "beta")
	f.ForceDeleteBranch("alpha")
	f.SwitchTo("beta")
	return f, landing, parentTip
}

// The case the record exists for. The parent landed and its branch was deleted before anyone read the
// child, so the submission could record no parent tip — and the base it measured from is the landing, the
// commit the child still sits on. Nothing has moved under the approval, and the gate says so.
func TestCheckPassesAChildApprovedAfterItsParentLandedAndItsBranchWasDeleted(t *testing.T) {
	f, landing, _ := landedParentWithADeletedBranch(t)
	ready(t, f)
	submit(t, f, "approve")

	out := runIn(t, f.Dir(), "check", "--json").json(t)
	if out["ready"] != true {
		t.Fatalf("check = %v: this approval measured from %s, and the base has not moved since",
			out["reasons"], shortOf(landing))
	}
	if out["parent_landed"] != true || out["parent_landed_commit"] != shortOf(landing) {
		t.Errorf("parent_landed = %v at %v, want the landing %s",
			out["parent_landed"], out["parent_landed_commit"], shortOf(landing))
	}
	if out["parent_head_carries_landing"] != true {
		t.Errorf("parent_head_carries_landing is false with this head on %s", shortOf(landing))
	}
	if out["parent_stale_branch"] != false {
		t.Error("parent_stale_branch is true for a branch that has been deleted: stale is about a branch that is here")
	}
	// The step a landed parent leaves is a rebase onto the parent's branch. Here that branch is gone and
	// its landing is already under this head, so the command would name something that does not exist.
	if na, _ := out["next_action"].(string); strings.Contains(na, "git rebase --onto") {
		t.Errorf("next_action = %q, which advises a rebase this head has already done onto a branch that is deleted", na)
	}
}

// The boundary the record draws, and the reason the rule is a rule rather than an exemption: an approval
// that recorded the parent's branch tip measured a diff that still carried the trunk work the landing
// merge brought in. The diff under test today is narrower than the one that was reviewed, so the approval
// is not about this diff, and `check` says which of the two it is talking about.
func TestCheckStillRefusesAnApprovalWhoseRecordedBasePredatesTheLanding(t *testing.T) {
	f, _, parentTip := landedParentWithADeletedBranch(t)
	f.CommitReadyMarker("beta")
	f.CommitReviewMarkerOnParent("beta", "approve", parentTip)

	res := runIn(t, f.Dir(), "check")
	if res.code == 0 {
		t.Fatalf("check passed after the base moved onto content the approval never measured:\n%s%s", res.stdout, res.stderr)
	}
	mustContain(t, res.stdout+res.stderr, "differs", "the reason names the comparison that failed")
	mustContain(t, res.stdout+res.stderr, "have the result reviewed again", "and the step it leaves")
}

// Neither trailer, which is a submission from before either existed or one written by hand. The absence
// says the trailer was not written; it is not evidence that the content moved, and refusing on it would
// refuse every child approved before the records existed. What `status` prints instead is the two facts
// this path can still establish: where the parent went, and where this head is.
func TestAnApprovalThatRecordedNoBaseIsANoteAndNotARefusal(t *testing.T) {
	f, _, _ := landedParentWithADeletedBranch(t)
	f.CommitReadyMarker("beta")
	f.CommitReviewMarker("beta", "approve")

	out := runIn(t, f.Dir(), "check", "--json").json(t)
	if out["ready"] != true {
		t.Fatalf("check = %v: an approval that named no starting point is not evidence that the content moved",
			out["reasons"])
	}

	st := runIn(t, f.Dir(), "status", "--json").json(t)
	parent, ok := st["parent"].(map[string]any)
	if !ok {
		t.Fatalf("parent is %#v, want an object for a stacked changeset", st["parent"])
	}
	note, _ := parent["note"].(string)
	mustContain(t, note, "no parent tip and no measured base", "the missing record is said rather than guessed past")
	mustContain(t, note, "this head is already on", "and the reading this path can still take is printed")
	if parent["head_carries_landing"] != true {
		t.Error("head_carries_landing is false with this head on the landing commit")
	}
}

// The two trailers answer two questions, and while the parent's branch is the base they agree by accident
// of history rather than by design. Once the parent has landed they name different commits: the branch tip
// is where the branch stands, and the base is the run the child shares with the destination. A submission
// that recorded only the first could no longer be asked the content question after the branch was deleted,
// which is the gap `Review-Base-Head` closes.
func TestSubmissionRecordsWhereItMeasuredWhenThatIsNotTheParentTip(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landing := landAndRecord(t, f, "alpha", "main")
	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")
	f.SwitchTo("beta")
	tip := f.RevParse("refs/heads/alpha")

	ready(t, f)
	submit(t, f, "approve")

	trailers := f.Trailers(f.Head())
	if trailers["Review-Parent-Head"] == "" {
		t.Fatalf("the submission recorded no parent head: %v", trailers)
	}
	if got := f.RevParse(trailers["Review-Parent-Head"]); got != tip {
		t.Errorf("Review-Parent-Head resolves to %s, want the parent branch tip %s", got, tip)
	}
	if trailers["Review-Base-Head"] == "" {
		t.Fatalf("the submission recorded no measured base: %v", trailers)
	}
	if got := f.RevParse(trailers["Review-Base-Head"]); got != landing {
		t.Errorf("Review-Base-Head resolves to %s, want the landing %s it measured from", got, landing)
	}
	if trailers["Review-Parent-Head"] == trailers["Review-Base-Head"] {
		t.Errorf("both trailers name %s: the parent's branch tip and the derived base are different commits here",
			trailers["Review-Parent-Head"])
	}
}

// An unstacked changeset records it too. Nothing gates on the value today — the parent rule is what asked
// for it — and a submission that recorded its starting point only when a stack existed would leave every
// later question about a diff's measurement back where this one started.
func TestSubmissionRecordsTheBaseForAnUnstackedChangeset(t *testing.T) {
	f, _, reviewed, _ := approvedChangeset(t)

	trailers := f.Trailers(f.Head())
	if trailers["Review-Base-Head"] == "" {
		t.Fatalf("an unstacked submission recorded no measured base: %v", trailers)
	}
	if got, want := f.RevParse(trailers["Review-Base-Head"]), f.MergeBase("main", reviewed); got != want {
		t.Errorf("Review-Base-Head resolves to %s, want the run %s shares with main (%s)", got, shortOf(reviewed), want)
	}
}
