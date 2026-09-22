package cli_test

import (
	"testing"

	"gitpair/internal/gittest"
)

// A landed child read after its branch has been tidied away is read from the durable refs, and the read has
// to stop reciting the span. After a merge landing the archived head sits *below* the base — the landing put
// the reviewed work inside the destination — so `base..head` is empty for exactly the changeset whose
// verdicts matter most, and the read used to answer "no reviews yet" about a head a reviewer approved while
// telling the reader to run the command whose evidence was on the screen.
func TestStatusOfARecordReadReportsTheArchivedVerdict(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")
	f.MustGit("branch", "-d", "alpha") // the tidy that leaves the record as the only read

	res := runIn(t, f.Dir(), "status", "--changeset", "alpha")
	res.mustSucceed(t, "status")
	mustContain(t, res.stdout, "Branch: none (read from the durable record)",
		"a record read says where it read from")
	mustContain(t, res.stdout, "outcome: approve",
		"and the verdict the archived chain carries")
	mustNotContain(t, res.stdout, "none yet",
		"it does not claim nobody looked at a head that was approved")
	mustContain(t, res.stdout, "read from the durable refs",
		"the reason says what the read was, not what the empty span contains")
	mustNotContain(t, res.stdout, "(`git pair integration record`)",
		"and a block that prints because the ref exists does not tell you to write it")

	// The same evidence from the other command, so the two cannot disagree about a commit neither of them
	// re-derived.
	hist := runIn(t, f.Dir(), "review", "history", "--changeset", "alpha")
	hist.mustSucceed(t, "review", "history")
	mustContain(t, hist.stdout, "approve", "`review history` reads the archived chain too")
}

// The archive is not a second source of state (PRD §13.4): walked in full, a landed child's history ends in
// an approval, and reporting that as `READY` would be a worse answer than `WORKING` beside an integration
// ref. `state` is what markers move, and no marker moved.
func TestStatusOfARecordReadKeepsStateWithTheSpan(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")
	f.MustGit("branch", "-d", "alpha")

	out := runIn(t, f.Dir(), "status", "--changeset", "alpha", "--json").json(t)
	if out["state"] != "WORKING" {
		t.Errorf("state is %v for a record read; the archive is not a second source of state", out["state"])
	}
	if out["integrated"] != true {
		t.Errorf("integrated is %v, want the record reported beside the state", out["integrated"])
	}
	if out["latest_review"] == nil {
		t.Errorf("latest_review is null; the archived verdict belongs in the machine surface too")
	}
}
