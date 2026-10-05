package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The gate asks one question of an approval: is the content under test still the content that was
// reviewed. These are the cases that question decides, in both directions, that the older reading — "has
// the parent's branch moved" — could not tell apart.
//
// The shape to keep in mind is that a contribution is measured above the ground its work sits on, and the
// ground is the newest commit that both this branch's history and the work underneath it share. Merging the
// destination in moves the ground with the branch, so the contribution is unchanged. Merging anything else
// in puts that work inside the contribution, where a reviewer has not looked at it.

// Where the unreviewed-content rule now stops: taking the destination in is not content the review never
// saw. The branch did not write it, the integration branch did, and the reviewer had no reason to read
// trunk. What the rule keeps refusing is content the destination does not carry — the next test is that one.
func TestMergingTheDestinationInIsNotUnreviewedWork(t *testing.T) {
	f, _, _ := stackedPair(t)
	f.SwitchTo("main")
	f.Commit("someone else lands unrelated work", gittest.WithFile("other.go", "package main\n"))
	f.SwitchTo("booking-tests")
	f.MustGit("merge", "--no-edit", "main")

	res := runIn(t, f.Dir(), "check", "--json")
	out := res.json(t)
	if out["ready"] != true {
		t.Fatalf("check refused with the destination merged in: %v", out["reasons"])
	}
	credited, _ := out["drift_credited"].([]any)
	if len(credited) == 0 {
		t.Fatalf("drift_credited is empty: the gate let the merge through without saying it credited it\n%s", res.stdout)
	}
	if !strings.Contains(res.stdout, "other.go") {
		t.Errorf("drift_credited does not name other.go:\n%s", res.stdout)
	}
	mustNotContain(t, res.stdout, "the parent branch",
		"and the stack contributes nothing: the parent did not move")
}

// The other side of the same comparison: work that is not the destination's lands inside the contribution,
// and the approval is a claim about the contribution. This is the case the parent-movement rule got exactly
// backwards — it refused a branch for taking the destination in, and stayed silent about one taking a
// stranger's branch in.
func TestMergingInWorkThatIsNotTheDestinationsIsRefused(t *testing.T) {
	f := newRepo(t)
	// Long enough that two branches editing different parts of it merge cleanly. Two edits three lines
	// apart are one hunk to git, and one hunk is a conflict rather than the clean merge this needs.
	f.Commit("a file two branches will both edit", gittest.WithFile("shared.txt",
		"one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n"))
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	ready(t, f)
	submit(t, f, "approve")
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	f.Commit("beta edits the second line", gittest.WithFile("shared.txt",
		"one\nBETA\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n"))
	ready(t, f)
	submit(t, f, "approve")

	// A third branch, off the destination, editing a different part: the merge is clean, which is the
	// point. Nothing about it forces the author to notice that the branch now carries their work too.
	f.SwitchTo("main")
	f.CreateBranch("side-work")
	f.Commit("side edits the last line", gittest.WithFile("shared.txt",
		"one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nSIDE\n"))
	f.SwitchTo("beta")
	f.MustGit("merge", "--no-edit", "side-work")

	res := runIn(t, f.Dir(), "check", "--json")
	if res.code == 0 && res.json(t)["ready"] == true {
		t.Fatalf("check passed after the branch took in somebody else's work: %v", res.json(t))
	}
	mustContain(t, res.stdout+res.stderr, "changed since",
		"the reason says the branch carries content the review did not see")
	mustContain(t, res.stdout+res.stderr, "shared.txt", "and names the file it arrived in")
}

// A child whose new commit is its own is refused too. Which rule says so is the interesting part: the
// branch has not been rewritten, so this is the content question rather than the lineage question, and it
// stays true whichever rule wins the sentence.
func TestNewWorkOnAnApprovedChildIsRefused(t *testing.T) {
	f, _, _ := stackedPair(t)
	f.Commit("one more test", gittest.WithFile("extra_test.go", "package main\n\nfunc TestExtra() {}\n"))

	res := runIn(t, f.Dir(), "check")
	if res.code == 0 {
		t.Fatalf("check passed with new work on the branch:\n%s", res.stdout)
	}
}

// The case the contribution comparison exists for: the parent's work reached the destination by a squash,
// which leaves no ancestry for a merge base to find. Measuring the child from a merge base with that
// landing reports the parent's content a second time inside the child's patch, and the approval dies on a
// shape the child had no part in choosing. Measured from the ground it actually sits on, the child
// contributes what the reviewer read, and the only thing left is the rebase the landing always left it.
func TestSquashLandedParentKeepsTheChildsApprovalAndNamesTheComparison(t *testing.T) {
	f, _, _ := stackedPair(t)
	f.SwitchTo("main")
	f.MustGit("merge", "--squash", "booking")
	f.MustGit("commit", "-m", "landed booking")
	landing := f.RevParse("HEAD")
	f.ForceDeleteBranch("booking")
	f.SwitchTo("booking-tests")

	out := runIn(t, f.Dir(), "check", "--json").mustSucceed(t, "check").json(t)
	if out["ready"] != true {
		t.Fatalf("ready is %v with reasons %v", out["ready"], out["reasons"])
	}
	if out["parent_comparison"] != "contribution" {
		t.Errorf("parent_comparison = %v, want the contribution comparison to be the reading that passed it",
			out["parent_comparison"])
	}
	if out["parent_landed_commit"] != shortOf(landing) {
		t.Errorf("parent_landed_commit = %v, want %s", out["parent_landed_commit"], shortOf(landing))
	}
}

// The guard that keeps that pass honest. A branch can carry exactly the approved content and still not
// merge: something else landed underneath it, on the same lines. There is then no clean patch to compare
// at all — what would land is whatever resolution somebody writes afterwards, over content no reviewer saw
// — and the merge job, after the gate said ready and CI said green, is the worst place to find that out.
func TestChildThatWouldConflictWithTheDestinationIsRefused(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	ready(t, f)
	submit(t, f, "approve")
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	f.Commit("beta adds the notes file", gittest.WithFile("notes.md", "beta's notes\n"))
	ready(t, f)
	submit(t, f, "approve")

	// The destination gains its own `notes.md` and the parent's landing: the child's content is untouched
	// and the merge would conflict.
	f.SwitchTo("main")
	f.Commit("trunk wrote the same file", gittest.WithFile("notes.md", "trunk's notes\n"))
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "land alpha", "alpha")
	f.SwitchTo("beta")

	res := runIn(t, f.Dir(), "check")
	if res.code == 0 {
		t.Fatalf("check passed a branch that would conflict with the destination:\n%s", res.stdout)
	}
	mustContain(t, res.stdout, "would conflict with", "the reason names the condition")
	mustContain(t, res.stdout, "notes.md", "and the file that conflicts")
}

// An approval that recorded no diff identity is every approval written before the trailer existed, and it
// must keep the reading it had. The fallback compares the two bases a landing puts in front of the child by
// the trees they carry; the sentence it produces is different on purpose, so a reader can tell which
// comparison was made rather than being shown a verdict with no reasoning attached.
func TestApprovalWithoutADiffIdFallsBackToTheBaseComparison(t *testing.T) {
	f, _, _ := stackedPair(t)
	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "land booking", "booking")
	f.SwitchTo("booking-tests")
	// Replace the approval with one that recorded its bases and no identity, as an older build wrote.
	f.CommitReviewMarkerRecorded("booking-tests", "approve", f.MustGit("rev-parse", "booking"), "", "")

	out := runIn(t, f.Dir(), "check", "--json").mustSucceed(t, "check").json(t)
	if out["ready"] != true {
		t.Fatalf("ready is %v with reasons %v: the older reading still answers this case", out["ready"], out["reasons"])
	}
	if out["parent_comparison"] != "merge-base" {
		t.Errorf("parent_comparison = %v, want the base comparison to be the reading that answered", out["parent_comparison"])
	}
}
