package cli_test

import (
	"testing"

	"gitpair/internal/gittest"
)

// `integration record` answers "which commits" from the graph, and "which changeset" from the directories
// the destination carries. Both halves had a second answer that only appeared once a repository had landed
// something: the discovery passes disagreed, and the weaker one won; and the source came from a branch,
// which is the half a tidy-up removes. The rows below are the shapes the session that found this recorded
// by hand, one invocation at a time.

// TestRecordFastForwardNeedsNoFlagsAndNoBranch is the shape where the destination's own line carries the
// reviewed chain: a fast-forward leaves the approved commits in trunk, so the archive and the integration
// are the same commit, and there is no branch left to name it.
func TestRecordFastForwardNeedsNoFlagsAndNoBranch(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	ready(t, f)
	submit(t, f, "approve")
	tip := f.Head()
	f.SwitchTo("main")
	f.MustGit("merge", "--ff", "alpha")
	f.MustGit("branch", "-D", "alpha")

	res := runIn(t, f.Dir(), "integration", "record")
	res.mustSucceed(t, "integration", "record")
	mustContain(t, res.stdout, "fast-forward",
		"a pair naming one commit is a derivation worth stating, not an accident to be explained later")
	if got := f.RefSHA(integrationRef("alpha")); got != tip {
		t.Errorf("the integration ref is at %s, want the commit the reviewed chain ends at (%s)", got, tip)
	}
	if got := f.RefSHA(archiveRef("alpha")); got != tip {
		t.Errorf("the archive is at %s, want the same commit: the destination carried the chain itself", got)
	}
}

// A merge landing introduces the directory from a side, and that side is the reviewed chain. This is the
// case the archive ref exists for (§13.1): after `git branch -D` nothing but the record holds the chain —
// except that the merge commit does, as its second parent, and reading it is what removes the race between
// recording and tidying.
func TestRecordMergeLandingAfterTheBranchIsGone(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	ready(t, f)
	submit(t, f, "approve")
	reviewed := f.Head()
	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "land alpha", "alpha")
	landing := f.Head()
	f.MustGit("branch", "-D", "alpha")

	res := runIn(t, f.Dir(), "integration", "record")
	res.mustSucceed(t, "integration", "record")
	if got := f.RefSHA(archiveRef("alpha")); got != reviewed {
		t.Errorf("the archive is at %s, want the introduced side's tip %s", got, reviewed)
	}
	if got := f.RefSHA(integrationRef("alpha")); got != landing {
		t.Errorf("the integration ref is at %s, want the merge %s", got, landing)
	}
	if f.RefSHA(archiveRef("alpha")) == f.RefSHA(integrationRef("alpha")) {
		t.Errorf("a merge landing recorded both halves at %s: the archive never names the merge commit",
			f.RefSHA(archiveRef("alpha")))
	}
}

// Everything on the destinations already recorded, two directories deep: the command has nothing to write,
// and it says that rather than naming the finished records as if they were gaps. A record is create-only, so
// "record it again" is never the finding — and a run that cannot say which changeset it meant is not a
// result a pipeline should read as green, because the state where a landing sits unrecorded looks identical
// from the exit code.
func TestRecordFlaglessNamesNothingWhenEverythingIsRecorded(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")
	f.CreateBranch("beta")
	f.CommitChangeset("beta", "main")
	f.Commit("beta work", gittest.WithFile("b.go", "package main\n"))
	landAndRecord(t, f, "beta", "main")

	res := runIn(t, f.Dir(), "integration", "record")
	if res.code != 2 {
		t.Fatalf("record exited %d, want 2: nothing is left to write here\n%s", res.code, res.stdout)
	}
	mustContain(t, res.stderr, "nothing to record", "and it says so in those words")
	mustContain(t, res.stderr, "already has", "the reason being that the refs are already there")
	mustNotContain(t, res.stderr, "alpha\n", "a finished record is not a candidate list")
	mustNotContain(t, res.stderr, "beta\n", "nor is the other one")
}

// The same repository with one directory still needing its pair: that is the flagless run's answer, named
// and written with no flags at all. Two of the three are in trunk and recorded, which is the state that made
// the flagless command stop working entirely before the discovery passes were made to agree.
func TestRecordFlaglessRecordsTheOneUnrecordedOfThree(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")
	f.CreateBranch("beta")
	f.CommitChangeset("beta", "main")
	f.Commit("beta work", gittest.WithFile("b.go", "package main\n"))
	landAndRecord(t, f, "beta", "main")
	f.CreateBranch("gamma")
	f.CommitChangeset("gamma", "main")
	f.Commit("gamma work", gittest.WithFile("c.go", "package main\n"))
	landing := landWithoutRecord(t, f, "gamma", "main")

	res := runIn(t, f.Dir(), "integration", "record")
	res.mustSucceed(t, "integration", "record")
	mustContain(t, res.stdout, "gamma:", "the one directory without a pair is the answer")
	if got := f.RefSHA(integrationRef("gamma")); got != landing {
		t.Errorf("gamma's record is at %s, want the landing %s", got, landing)
	}
	for _, id := range []string{"alpha", "beta"} {
		if !f.HasRef(integrationRef(id)) {
			t.Errorf("%s lost its record on the way", id)
		}
	}
}

// A retry against a repository with more than one finished record is still the idempotent answer, because
// `--changeset` names the one it means. The refusal above is for a run that could not say which changeset it
// was about; this one can.
func TestRecordRetryWithTwoFinishedRecordsIsStillANoOp(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")
	f.CreateBranch("beta")
	f.CommitChangeset("beta", "main")
	f.Commit("beta work", gittest.WithFile("b.go", "package main\n"))
	landAndRecord(t, f, "beta", "main")

	res := runIn(t, f.Dir(), "integration", "record", "--changeset", "beta")
	res.mustSucceed(t, "integration", "record")
	mustContain(t, res.stdout, "already recorded", "the directed retry is the run the contract promises")
	out := runIn(t, f.Dir(), "integration", "record", "--changeset", "beta", "--json").json(t)
	if out["recorded"] != false || out["already_recorded"] != true {
		t.Errorf("recorded = %v, already_recorded = %v; a retry that wrote nothing must say so",
			out["recorded"], out["already_recorded"])
	}
}

// After a merge the destination carries the directory, and every branch cut from the destination afterwards
// inherits it. Without the descendant rule a landed changeset turns each of those branches into a claim
// about where its reviewed head is — and a squash, whose chain really is only on a branch, would be recorded
// from a branch that never carried the review.
func TestRecordDoesNotTakeABranchDownstreamOfTheLandingAsTheSource(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	ready(t, f)
	submit(t, f, "approve")
	f.SwitchTo("main")
	f.MustGit("merge", "--squash", "alpha")
	f.MustGit("commit", "-m", "squash alpha")
	landing := f.Head()
	f.ForceDeleteBranch("alpha")
	f.CreateBranch("later-work")
	f.Commit("unrelated trunk work", gittest.WithFile("z.go", "package main\n"))

	res := runIn(t, f.Dir(), "integration", "record", "--changeset", "alpha", "--commit", landing)
	if res.code == 0 {
		t.Fatalf("record wrote a pair from a branch that never carried the review:\n%s", res.stdout)
	}
	mustNotContain(t, res.stderr, "later-work",
		"a branch downstream of the landing is not offered as the reviewed head")
	mustContain(t, res.stderr, "--source", "and the answer it is given is the flag that names the head")
}
