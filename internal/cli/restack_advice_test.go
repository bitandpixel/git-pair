package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The withheld restack is withheld from the verdict, and from the verdict only. A surface that carries no
// verdict cannot invalidate an approval with what it prints, so `status` and `queue` keep the command: they
// are where a stale parent branch is the subject rather than the thing under test, and where a reader who
// wants the command for tidiness still finds it. A child with nothing standing behind it gets the step
// everywhere, because following it there costs nothing.

// TestUnapprovedChildKeepsTheRestackOnEverySurface is criterion four: the step is housekeeping wherever no
// approval is standing on it. The child was offered and never approved, its parent landed, and its branch is
// still present — the same geography that withholds the command from `check` for an approved child.
func TestUnapprovedChildKeepsTheRestackOnEverySurface(t *testing.T) {
	f := landedParentOfAnUnapprovedChild(t)
	step := "git rebase --onto " + shortOf(f.landing) + " alpha beta"

	st := runIn(t, f.Dir(), "status").mustSucceed(t, "status").stdout
	mustContain(t, st, step, "status is where an author reads the housekeeping, so it keeps the command")
	mustContain(t, st, "this head is still measured on alpha", "and states the fact beside it")

	q := runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
	mustContain(t, q.stderr, "rebase onto it", "and the queue's housekeeping voice keeps it too")

	p := parentJSONOf(t, f.Fixture, "beta")
	if p["step"] != nil && p["step"] == "" {
		t.Errorf("parent.step = %v, want the command or no key at all", p["step"])
	}
	if s, _ := p["step"].(string); !strings.Contains(s, step) {
		t.Errorf("parent.step = %q, want %q", s, step)
	}
	if n, _ := p["note"].(string); strings.Contains(n, "git rebase") {
		t.Errorf("parent.note = %q: the note states where the head sits, and the command is its own field", n)
	}
}

// landedParentOfAnUnapprovedChild cuts a child from its parent, offers it, and lands the parent with the
// branch left in place. No approval is anywhere in the child's history, so nothing the step rewrites is
// standing.
func landedParentOfAnUnapprovedChild(t *testing.T) landedChild {
	t.Helper()
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	ready(t, f)
	f.SwitchTo("alpha")
	landing := landApproved(t, f, "alpha", "main")
	f.SwitchTo("beta")
	return landedChild{Fixture: f, landing: landing}
}

// A feedback submission is not an approval, and the gate passes it only under `--allow-feedback`. It licenses
// a merge, so the step is the author's next act rather than a note — and it rewrites nothing that a reviewer
// certified, so the verdict prints the command it would withhold from an approved child.
func TestCheckAdvisesTheRestackToAChildWhoseVerdictIsFeedback(t *testing.T) {
	f := landedParentOfAnUnapprovedChild(t)
	submit(t, f.Fixture, "feedback")

	out := runIn(t, f.Dir(), "check", "--allow-feedback", "--json").mustSucceed(t, "check").json(t)
	if out["ready"] != true {
		t.Fatalf("ready = false, reasons %v: feedback permits integration under this policy", out["reasons"])
	}
	na, _ := out["next_action"].(string)
	if !strings.Contains(na, "git rebase --onto "+shortOf(f.landing)+" alpha beta") {
		t.Errorf("next_action = %q, want the restack the landing leaves: no approval is standing on this head", na)
	}
	if out["parent_approval_survives_landing"] != false {
		t.Error("parent_approval_survives_landing is true for a changeset with no approval in front of the gate")
	}
}

// Criterion five: where the content question cannot be answered, the step is still given — with the reason it
// is given without the comparison. An approval from before the measurement trailers existed is the ordinary
// way into this, and the direction of the failure is the one PRD §21 asks for: a step the reader can decline
// beats silence about a step, because silence is how a rewrite goes unwritten-about.
func TestCheckAdvisesTheRestackUnverifiedWhenTheApprovalRecordedNothing(t *testing.T) {
	f := approvedChildOnALandedParent(t)
	// Replace the recorded approval with one carrying neither `Review-Parent-Head` nor
	// `Review-Base-Head`, which is what an approval from before the trailers looks like.
	f.MustGit("reset", "--hard", "HEAD~1")
	f.CommitReviewMarker("beta", "approve")

	out := runIn(t, f.Dir(), "check", "--json").mustSucceed(t, "check").json(t)
	if out["ready"] != true {
		t.Fatalf("ready = false, reasons %v: an approval that named no starting point is not evidence the content moved",
			out["reasons"])
	}
	na, _ := out["next_action"].(string)
	if !strings.Contains(na, "git rebase --onto "+shortOf(f.landing)+" alpha beta") {
		t.Errorf("next_action = %q, want the step given rather than withheld on an unknown", na)
	}
	if out["parent_approval_survives_landing"] != false {
		t.Error("parent_approval_survives_landing is true without a comparison to stand on")
	}

	human := runIn(t, f.Dir(), "check").mustSucceed(t, "check").stdout
	mustContain(t, human, "no parent tip and no measured base",
		"and the verdict says the step is given without the comparison")
}

// The comparison still refuses where it should. This is criterion two on the side where the parent's branch
// survived: the parent reached the destination as a squash, so its commits never became ancestors of anything,
// and the child that took the landing in is now measured above content the reviewer never saw. The step is
// the re-offer, because what would land is a diff nobody read — and the sentence must not read as naming a
// branch deletion followed by a review.
func TestCheckRefusesAChildRewrittenOntoASquashedParentLanding(t *testing.T) {
	f := newRepo(t)
	f.Commit("seed the shared file", gittest.WithFile("shared.txt", "header\nline a\nline b\nthe end\n"))
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha writes the bottom", gittest.WithFile("shared.txt", "header\nline a\nline b\nthe end\nparent note\n"))
	f.CreateBranch("beta")
	f.Commit("changeset beta", gittest.WithFiles(map[string]string{
		"changesets/beta/CHANGESET.yaml": "id: beta\nparent: alpha\nparent-changeset: alpha\n",
		"changesets/beta/ABOUT.md":       "# beta\n",
	}))
	f.Commit("beta writes the top", gittest.WithFile("shared.txt", "the child's line\nheader\nline a\nline b\nthe end\nparent note\n"))
	ready(t, f)
	submit(t, f, "approve")

	// The parent is reviewed after its child, and then reaches the destination as a squash: the content the
	// review read arrives in commits the child never had.
	f.SwitchTo("alpha")
	ready(t, f)
	submit(t, f, "approve")
	f.SwitchTo("main")
	f.MustGit("merge", "--squash", "alpha")
	f.MustGit("commit", "--no-edit", "-m", "land alpha (squashed)")
	landing := f.Head()

	f.SwitchTo("beta")
	f.MustGit("rebase", "--onto", landing, "alpha", "beta")

	res := runIn(t, f.Dir(), "check")
	if res.code == 0 {
		t.Fatalf("check passed a child whose measured content is not what the approval read:\n%s", res.stdout)
	}
	mustContain(t, res.stdout, "differs from what review", "the reason names the comparison that failed")
	mustContain(t, res.stdout, "have the result reviewed again", "and the step it leaves asks for a review of the result")
	mustContain(t, res.stdout, "git pair change ready --changeset beta",
		"and the step is the re-offer, since this head is already on the landing")
	mustNotContain(t, res.stdout, "git branch -D alpha and", "not a branch deletion glued to a review request")

	out := runIn(t, f.Dir(), "check", "--json").json(t)
	if out["parent_landed"] != true || out["parent_landed_commit"] != shortOf(landing) {
		t.Errorf("parent_landed = %v / %v, want true and %s", out["parent_landed"], out["parent_landed_commit"], shortOf(landing))
	}
	if out["parent_approval_survives_landing"] != false {
		t.Error("parent_approval_survives_landing is true while the comparison says the content differs")
	}
}

// Criterion three, and the case where the merge the withheld step was standing in for is the problem: the
// branch does not merge cleanly into the destination, so there is no patch to compare at all and the
// resolution is content no reviewer saw. The step is the merge, and the review comes after it.
func TestCheckNamesTheMergeWhenALandedParentWouldConflictWithTheChild(t *testing.T) {
	f := newRepo(t)
	f.Commit("seed the shared file", gittest.WithFile("shared.txt", "header\nline a\nline b\nthe end\n"))
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	f.CreateBranch("beta")
	f.Commit("changeset beta", gittest.WithFiles(map[string]string{
		"changesets/beta/CHANGESET.yaml": "id: beta\nparent: alpha\nparent-changeset: alpha\n",
		"changesets/beta/ABOUT.md":       "# beta\n",
	}))
	// The child edits the middle of the file above the ground it was cut from.
	f.Commit("beta rewrites the middle", gittest.WithFile("shared.txt", "header\nthe child's line\nline b\nthe end\n"))
	ready(t, f)
	submit(t, f, "approve")

	// The parent then rewrites the same line, after its child branched, and lands. The merge base of the two
	// branches is the commit the child was cut from, so both sides changed that line and the merge conflicts
	// — while the child's own contribution above its ground is exactly what was read, which is why the
	// content comparison alone cannot see it and the cleanliness question is asked separately.
	f.SwitchTo("alpha")
	f.Commit("alpha rewrites the same line", gittest.WithFile("shared.txt", "header\nthe parent's line\nline b\nthe end\n"))
	ready(t, f)
	submit(t, f, "approve")
	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "land alpha", "alpha")

	f.SwitchTo("beta")
	res := runIn(t, f.Dir(), "check")
	if res.code == 0 {
		t.Fatalf("check passed a branch that conflicts with the destination:\n%s", res.stdout)
	}
	mustContain(t, res.stdout, "would conflict with main", "the reason names the condition and the destination")
	mustContain(t, res.stdout, "shared.txt", "and the file that would conflict")
	mustContain(t, res.stdout, "git merge main && git pair change ready --changeset beta",
		"and the step is the merge followed by a re-offer, not a rewrite onto the landing")
}
