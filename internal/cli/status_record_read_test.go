package cli_test

import (
	"testing"

	"gitpair/internal/gittest"
)

// A landed child read after its branch has been tidied away is read from the destination's history: the run
// of commits on its first-parent line that carries the changeset's directory. That run is the span the read
// measures, so the markers, the verdict and the thread files are where the branch left them, and the answer
// is a marker sentence rather than the empty-range apology the ref-driven read used to print — after a merge
// landing the chain sits *below* the recorded base, and `base..head` there is empty for exactly the changeset
// whose verdicts matter most.
func TestStatusOfARecordReadReportsTheArchivedVerdict(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")
	f.MustGit("branch", "-d", "alpha") // the tidy that leaves the record as the only read

	res := runIn(t, f.Dir(), "status", "--changeset", "alpha")
	res.mustSucceed(t, "status")
	mustContain(t, res.stdout, "Branch: none (read from the landed chain)",
		"a landed read says where it read from")
	mustContain(t, res.stdout, "outcome: approve",
		"and the verdict the chain carries")
	mustNotContain(t, res.stdout, "none yet",
		"it does not claim nobody looked at a head that was approved")
	mustNotContain(t, res.stdout, "no commits above the base",
		"the chain is the span now, so the reason is what the markers say rather than an empty range")
	mustNotContain(t, res.stdout, "(`git pair integration record`)",
		"and a block that prints because the ref exists does not tell you to write it")

	// The same evidence from the other command, so the two cannot disagree about a commit neither of them
	// re-derived.
	hist := runIn(t, f.Dir(), "review", "history", "--changeset", "alpha")
	hist.mustSucceed(t, "review", "history")
	mustContain(t, hist.stdout, "approve", "`review history` reads the same chain")
}

// The chain the destination carries *is* the span the read measures, so a landed read reports the state the
// chain's own markers derive. PRD §13.4's rule that the archive is not a second source of state was about
// walking a ref outside the span and reporting what it found as if it were live work; with the chain as the
// span there is one source — the destination's history — and the thing that keeps a landed read from
// licensing a merge is the gate, which refuses on the landing itself before it asks the drift question.
func TestStatusOfARecordReadReportsTheChainAsState(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")
	f.MustGit("branch", "-d", "alpha")

	out := runIn(t, f.Dir(), "status", "--changeset", "alpha", "--json").json(t)
	if out["state"] != "APPROVED" {
		t.Errorf("state is %v for a chain read; the chain carries the approval the branch left behind", out["state"])
	}
	if out["latest_review"] == nil {
		t.Errorf("latest_review is null; the verdict the chain carries belongs in the machine surface too")
	}

	// What keeps the printed state safe is not the state itself: `check` on this branch refuses because
	// the resolver no longer offers a landed directory as work in progress, and the gate's own landing
	// clause (asserted in TestIntegrationReasons) is the backstop behind it.
	if got := runIn(t, f.Dir(), "check"); got.code == 0 {
		t.Errorf("check passed on a branch whose changeset has landed: %s", got.stdout)
	}
}
