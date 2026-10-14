package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// Where a stack's work has landed in stages, the record below a child names a branch that has outlived the
// work it carried. beta's `base:` says `alpha`, and `alpha` is where alpha's branch still points; alpha's
// work, and beta's, are both in the integration branch. A child that reads that value as a destination is
// told to merge into finished work — the same reading the parent walk already refuses one hop up, where a
// base read out of a landed tree has to be proved before it is believed.
//
// Reported from a stack whose middle changeset had landed: the child in the queue was told to merge into the
// branch under the parent, while `git pair change integrate` — the command the author then runs — put the
// integration branch on its `merge into:` line.

// stackedByBase is `stackedOff` with the record `init` writes today: a base and the changeset below it, in
// place of the older `parent:` pair. The base is what the destination walk starts from, so a fixture that
// means to ask that question has to write it.
func stackedByBase(t *testing.T, f *gittest.Fixture, slug, base, baseChangeset, work string) {
	t.Helper()
	f.CreateBranch(slug)
	f.Commit("changeset "+slug, gittest.WithFiles(map[string]string{
		"changesets/" + slug + "/CHANGESET.yaml": "id: " + slug +
			"\nbase: " + base + "\nbase-changeset: " + baseChangeset + "\n",
		"changesets/" + slug + "/ABOUT.md": "# " + slug + "\n",
	}), gittest.WithFile(work, "package main\n"))
}

// landedPairUnderAStack builds alpha, a beta cut from it, and a gamma cut from beta — all three offered
// while the stack was still standing, and then alpha and beta merged into the integration branch one after
// the other. gamma is left where the author left it: on beta's branch, with both branches under it stale,
// each carrying work that is already in the destination. That is the state a stack is in when its lower
// changesets are merged while the top one waits for review.
func landedPairUnderAStack(t *testing.T) *gittest.Fixture {
	t.Helper()
	f := stackOfThree(t)
	f.SwitchTo("alpha")
	landApproved(t, f, "alpha", "main")
	f.SwitchTo("beta")
	landApproved(t, f, "beta", "main")
	f.SwitchTo("gamma")
	return f
}

// stackOfThree builds the three-level stack with every changeset offered and the child approved, and leaves
// HEAD on gamma. Nothing has landed; the callers decide how much of it to merge.
func stackOfThree(t *testing.T) *gittest.Fixture {
	t.Helper()
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	ready(t, f)
	stackedByBase(t, f, "beta", "alpha", "alpha", "b.go")
	ready(t, f)
	stackedByBase(t, f, "gamma", "beta", "beta", "c.go")
	ready(t, f)
	submit(t, f, "approve")
	f.SwitchTo("gamma")
	return f
}

// TestChildOfALandedStackIsToldWhereItsOwnMergeGoes is the report: the child of a stack whose branches have
// all landed is told the destination, which is the branch that carries those landings, and not the branch its
// parent's record still names. It is read on every surface that prints the sentence, and against the
// declaration the author will actually run.
func TestChildOfALandedStackIsToldWhereItsOwnMergeGoes(t *testing.T) {
	f := landedPairUnderAStack(t)

	const want = "`git pair check`, then merge into main with ordinary git (where beta landed)"
	for name, line := range nextLines(t, f) {
		if !strings.HasSuffix(line, want) {
			t.Errorf("%s next step is %q, want it to end in %q", name, line, want)
		}
		if strings.Contains(line, "merge into alpha") || strings.Contains(line, "merge into beta") {
			t.Errorf("%s offers a branch whose work is already in the destination: %s", name, line)
		}
	}

	declared := runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change integrate").stdout
	if !strings.Contains(declared, "merge into:  main (where beta landed)") {
		t.Errorf("the declaration merges into something other than what the next step named:\n%s", declared)
	}
}

// The same reading with the parent still unlanded: the child waits on beta. The sentence names the
// integration branch once, for gamma's own merge, and leaves beta's step to beta's screen — beta's record
// naming alpha's branch is no more a destination here than it is above, and spelling beta's merge in gamma's
// sentence was a second copy of a contract that already had one home.
func TestChildOfAnUnlandedParentWhoseBaseHasLandedWaitsOnTheDestination(t *testing.T) {
	f := stackOfThree(t)
	f.SwitchTo("alpha")
	landApproved(t, f, "alpha", "main")
	f.SwitchTo("gamma")

	for name, line := range nextLines(t, f) {
		if !strings.Contains(line, "waiting on parent beta") {
			t.Errorf("%s does not name what is waiting: %s", name, line)
			continue
		}
		if strings.Contains(line, "into alpha") {
			t.Errorf("%s sends a merge to a branch whose work is in the destination: %s", name, line)
		}
		if n := strings.Count(line, "merge into main"); n != 1 {
			t.Errorf("%s names the destination %d times, want once — gamma's own merge, with beta's step left to "+
				"beta's screen:\n\t%s", name, n, line)
		}
		if !strings.Contains(line, "`git pair status --changeset beta`") {
			t.Errorf("%s does not send the reader to the parent's own advice:\n\t%s", name, line)
		}
	}
}
