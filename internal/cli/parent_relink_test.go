package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The base a stacked child is measured against is a branch, and a branch is the half of a landed parent
// that stops being the truth. The branch is usually still there — landing deletes nothing — and a branch that
// outlives its work is the case that produces the wrong diff, not the case that produces an error: diffing a
// child against a branch the landing never moved reads the landed work as still sitting under the child.
//
// `changeset.BaseFor` answers the question from the destination: the parent branch while it stands and its
// work has not landed, the run the child shares with the destination once it has, and the destination itself
// when neither resolves. The rule travels with the value (`Base:` prints both, `--json` as `base_ref` and
// `base_why`) because a SHA alone cannot be read.
//
// What the base moving must not do is invalidate a review for free. The rule settled in PRD §21 is content,
// not bookkeeping: an approval stands while `base...head` names the same files under the old base and the new
// one, and falls when it does not.

func TestADerivedBaseReplacesTheParentBranchWhileTheBranchIsHere(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landing := landAndRecord(t, f, "alpha", "main")
	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")
	f.SwitchTo("beta")

	res := runIn(t, f.Dir(), "status")
	res.mustSucceed(t, "status")
	mustContain(t, res.stdout, "Base: main — the parent alpha landed",
		"the base names the destination the child is now measured against, and the rule is printed with it")
	mustContain(t, res.stdout, "landed as "+shortOf(landing),
		"and the stack line names the commit the parent became, which is where a reader reads the commit")
	mustContain(t, res.stdout, "Span: main...current",
		"and the span is drawn from that base rather than from a branch tip that no longer carries the work")
	// The label is the destination; the measurement is still the child's own run. Naming a branch the child
	// sits behind would put the parent's landed work into its diff, so the diff is what settles it.
	diff := runIn(t, f.Dir(), "diff", "--stat")
	diff.mustSucceed(t, "diff", "--stat")
	mustContain(t, diff.stdout, "b.go", "the span carries the child's own work")
	mustNotContain(t, diff.stdout, "a.go", "and not the parent's, which the landing already carried across")
	if _, err := f.Git("rev-parse", "--verify", "refs/heads/alpha"); err != nil {
		t.Fatalf("the fixture deleted the parent branch, which is the easy case")
	}
	mustNotContain(t, res.stdout, "refs/git-pair/", "and no durable ref is named anywhere in the answer")
}

// The rebased-and-trunk-moved case, which is the one a ref-named base gets wrong. Trunk advanced after the
// parent branched; the child is rebased onto the landing, so a base naming anything older counts the trunk
// commits it inherited as its own work.
func TestRelinkAfterARebaseOntoTrunkShowsOnlyTheChildsOwnWork(t *testing.T) {
	f := newRepo(t)
	f.Commit("trunk note", gittest.WithFile("d.go", "package main\n"))
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	ready(t, f)
	submit(t, f, "approve")
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	f.SwitchTo("main")
	f.Commit("trunk moves on", gittest.WithFile("e.go", "package main\n"))
	f.SwitchTo("alpha")
	landing := landApproved(t, f, "alpha", "main")
	f.SwitchTo("beta")
	// The command the note prints, run by the test: this is the rebase the child has to do, and the span
	// afterwards is the thing being measured.
	f.MustGit("rebase", "--onto", landing, "alpha", "beta")

	res := runIn(t, f.Dir(), "status")
	res.mustSucceed(t, "status")
	mustContain(t, res.stdout, "Span: main...current",
		"the span is drawn from the destination, so the trunk commits the child inherited are not the child's")
	mustNotContain(t, res.stdout, landing, "and no commit id stands in for that sentence in the header")
	own := f.SubjectsAbove(landing, f.Head())
	if len(own) == 0 || !strings.Contains(strings.Join(own, "\n"), "changeset beta") {
		t.Errorf("above the landing the child carries %v, want its own commits only", own)
	}
	for _, s := range own {
		if strings.Contains(s, "trunk moves on") || strings.Contains(s, "alpha work") {
			t.Errorf("the span counts %q as this child's work, which is the noise the derived base removes", s)
		}
	}
}

// An approval is a claim about content. The base moving under it does not change the content, so the
// approval stands and `check` still passes — the diff `base...head` is the same files under the branch tip
// and under the landing commit.
func TestRelinkKeepsAnApprovalWhoseDiffIsIdentical(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	f.SwitchTo("alpha")
	ready(t, f)
	submit(t, f, "approve")
	f.SwitchTo("beta")
	ready(t, f)
	submit(t, f, "approve")
	f.SwitchTo("alpha")
	landApproved(t, f, "alpha", "main")
	f.SwitchTo("beta")

	out := runIn(t, f.Dir(), "status", "--json").json(t)
	if out["base_why"] == nil || out["base_why"] == "" {
		t.Fatalf("base = %v with no base_why: the test needs the base to have been derived, to prove the approval survives it", out["base"])
	}
	res := runIn(t, f.Dir(), "check", "--json")
	res.mustSucceed(t, "check", "--json")
	if res.json(t)["ready"] != true {
		t.Errorf("check = %v: a base that moved onto the same content is not new work to review", res.json(t))
	}
}

// The other side of the same rule, and the reason it is a rule rather than an exemption: the content is
// the same, and the approval still falls. `git rebase --onto` replays the child's commits onto the landing,
// so the contribution above the ground it now sits on is byte for byte the contribution the reviewer read —
// and the approval is dropped anyway, because the commits the review looked at are no longer in this
// history. That is the `Review-Head` lineage rule, not the content rule: a rebase costs a review whatever
// it changes, which is what keeps "re-review the content" and "rebase the branch" from being alternatives.
//
// The content rule's own refusal — the same head, different content — is in `contribution_gate_test.go`.
func TestRelinkOntoTheLandingCostsAReviewForTheRewrite(t *testing.T) {
	f := newRepo(t)
	f.Commit("trunk note", gittest.WithFile("d.go", "package main\n"))
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	// The parent's verdict first, then the child's: a parent that takes its ready and approve commits
	// after the child's approval is the "parent branch moved" case, and this test is about the base
	// moving onto a record while the branch stays where the approval put it.
	ready(t, f)
	submit(t, f, "approve")
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	f.SwitchTo("beta")
	ready(t, f)
	submit(t, f, "approve")
	f.SwitchTo("main")
	f.Commit("trunk moves on", gittest.WithFile("e.go", "package main\n"))
	f.SwitchTo("alpha")
	landing := landApproved(t, f, "alpha", "main")
	f.SwitchTo("beta")
	f.MustGit("rebase", "--onto", landing, "alpha", "beta")

	res := runIn(t, f.Dir(), "check", "--json")
	if res.code == 0 && res.json(t)["ready"] == true {
		t.Fatalf("check passed: %v, after the reviewed commits left this history", res.json(t))
	}
	mustContain(t, res.stdout+res.stderr, "no longer in this history",
		"the reason names the rewrite, which is what the rebase did")
	mustContain(t, res.stdout+res.stderr, "does not license integration",
		"and says what that means for the approval")
}

// The derivation needs nothing from this clone's refs: with the parent's record deleted from the repository
// the base is the same commit, because the answer comes out of the destination's history and not out of
// git-pair's paper trail. The failure this replaces is an `unknown revision` from a base another clone wrote
// down, and the failure before that is the same error from a base naming a branch that has gone.
func TestADerivedBaseNeedsNoRecordInThisClone(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landing := landAndRecord(t, f, "alpha", "main")
	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")
	f.MustGit("update-ref", "-d", "refs/git-pair/integrations/alpha")
	f.MustGit("update-ref", "-d", "refs/git-pair/archive/alpha")

	res := runIn(t, f.Dir(), "status", "--changeset", "beta")
	res.mustSucceed(t, "status")
	mustContain(t, res.stdout, "Base: main — the parent alpha landed",
		"the same base the record used to name, derived rather than read, and named by the destination rather than by a commit")
	mustContain(t, res.stdout, "landed as "+shortOf(landing),
		"and the parent is reported landed from the destination, with no ref left in the repository")
	mustNotContain(t, res.stdout, "git pair integration record",
		"with no invocation offered: the landing needs no record to be known")
}

// `Review-Parent-Head` is the value two clones have to agree on when they submit the same review, so it
// names the branch while the branch exists. The landing is a separate recorded fact; making it the parent
// head would have a clone that fetched the namespace record a different trailer from one that has not.
func TestSubmissionRecordsTheParentBranchTipAfterTheParentLanded(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")
	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")
	f.SwitchTo("beta")
	tip := f.RevParse("refs/heads/alpha")

	ready(t, f)
	submit(t, f, "approve")
	// A review submission is a commit on this branch, so the marker is the head it just moved to.
	trailers := f.Trailers(f.Head())
	if trailers["Review-Parent-Head"] == "" {
		t.Fatalf("the submission recorded no parent head: %v", trailers)
	}
	if got := f.RevParse(trailers["Review-Parent-Head"]); got != tip {
		t.Errorf("Review-Parent-Head resolves to %s, want the parent branch tip %s", got, tip)
	}
}
