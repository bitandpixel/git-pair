package cli_test

import (
	"testing"

	"gitpair/internal/gittest"
)

// PRD §21: "Each stacked branch has its own independent changeset" — its own
// ABOUT.md, threads, review history, review outcome and review archive ref. Stack
// relationships only influence the configured base ref.
func TestStackedChangesetsResolveBaseToSiblingWithIndependentState(t *testing.T) {
	f := newRepo(t)

	// Lower changeset.
	f.CreateBranch("booking-transaction")
	f.CommitChangeset("booking-transaction", "main")
	f.Commit("implement locking", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))
	ready(t, f)
	f.Write("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n")
	submit(t, f, "block")
	lowerReview := f.Head()
	lowerRef := f.RefSHA(reviewRef("booking-transaction"))

	// Upper changeset, stacked on the lower branch.
	f.CreateBranch("booking-transaction-tests", "booking-transaction")
	runIn(t, f.Dir(), "change", "init", "--base", "booking-transaction").
		mustSucceed(t, "change", "init")
	f.Commit("add concurrent final-seat test", gittest.WithFile("service_test.go",
		"package main\n\nfunc TestFinalSeat() {}\n"))
	ready(t, f)

	status := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json").json(t)
	if status["changeset"] != "booking-transaction-tests" {
		t.Errorf("changeset = %v, want booking-transaction-tests", status["changeset"])
	}
	if status["base"] != "booking-transaction" {
		t.Errorf("base = %v, want the sibling changeset booking-transaction", status["base"])
	}
	if status["state"] != "READY" {
		t.Errorf("state = %v, want READY", status["state"])
	}
	if status["review_ref"] != reviewRef("booking-transaction-tests") {
		t.Errorf("review_ref = %v, want its own ref", status["review_ref"])
	}

	// Independent review history: the upper changeset has never been reviewed, even
	// though its base branch has a block review in its own history.
	// Independent review history: the upper changeset has never been reviewed, even
	// though its base branch has a block review in its own history.
	upperHistory := runIn(t, f.Dir(), "review", "history", "--json")
	if reviews := upperHistory.jsonList(t, "reviews"); len(reviews) != 0 {
		t.Errorf("upper reviews = %v, want none: the base branch's review is not this changeset's history", reviews)
	}

	// Only the upper changeset is queued; the lower one is blocked.
	queue := runIn(t, f.Dir(), "review", "queue", "--json")
	rows := queue.jsonList(t, "ready_for_review")
	if len(rows) != 1 {
		t.Fatalf("queue = %v, want only the ready upper changeset", rows)
	}
	entry := rows[0].(map[string]any)
	if entry["changeset"] != "booking-transaction-tests" || entry["base"] != "booking-transaction" {
		t.Errorf("queue entry = %v, want booking-transaction-tests on base booking-transaction", entry)
	}

	// The upper changeset's diff is measured against its sibling, not against main:
	// the lower changeset's files must not appear.
	from := f.MergeBase("booking-transaction", "HEAD")
	want, err := f.Git("diff", from, "HEAD")
	if err != nil {
		t.Fatalf("git diff: %v", err)
	}
	got := runIn(t, f.Dir(), "diff").mustSucceed(t, "diff")
	if got.stdout != want {
		t.Errorf("`git pair diff` differs from `git diff %s HEAD`", from)
	}
	mustNotContain(t, got.stdout, "+func Lock()", "the lower changeset's implementation must not be in the upper span")

	// Independent artifacts: each changeset directory has its own ABOUT.md and threads.
	f.WriteChangesetFile("booking-transaction-tests", "flaky-test.md", "# Flaky test\n\nWhy is this one flaky?\n")
	f.WriteChangesetFile("booking-transaction", "ABOUT.md", "# booking-transaction\n\nLower description.\n")
	if got := f.ChangesetFile("booking-transaction-tests", "ABOUT.md"); got == f.ChangesetFile("booking-transaction", "ABOUT.md") {
		t.Error("stacked changesets share one ABOUT.md")
	}
	if threads := f.Threads("booking-transaction-tests"); len(threads) != 1 {
		t.Errorf("upper threads = %v, want its own thread file", threads)
	}

	// Independent refs: reviewing the upper changeset must not move the lower one.
	submit(t, f, "approve")
	upperHead := f.Head()
	if got := f.RefSHA(reviewRef("booking-transaction-tests")); got != upperHead {
		t.Errorf("%s = %s, want the upper review commit %s", reviewRef("booking-transaction-tests"), got, upperHead)
	}
	if got := f.RefSHA(reviewRef("booking-transaction")); got != lowerRef {
		t.Errorf("the lower changeset's ref moved from %s to %s", lowerRef, got)
	}
	// Its history is now its own single approve.
	upperHistory = runIn(t, f.Dir(), "review", "history", "--json")
	reviews := upperHistory.jsonList(t, "reviews")
	if len(reviews) != 1 || reviews[0].(map[string]any)["outcome"] != "approve" {
		t.Errorf("upper reviews = %v, want its own approve", reviews)
	}

	// Archival is per changeset too: closing the upper changeset writes its own
	// archive ref and leaves the lower one's refs alone.
	runIn(t, f.Dir(), "review", "close").mustSucceed(t, "review", "close")
	upperArchives := f.RefNames(archivePattern("booking-transaction-tests"))
	if len(upperArchives) != 1 {
		t.Errorf("upper archive refs = %v, want exactly one", upperArchives)
	}
	if got := f.RefSHA(reviewRef("booking-transaction")); got != lowerRef {
		t.Errorf("closing the upper changeset moved the lower ref to %s, want %s", got, lowerRef)
	}
	if got := f.ReachableFrom(lowerReview, upperArchives[0]); !got {
		t.Logf("note: the upper archive does not include the lower changeset's review commit (expected)")
	}
}

// A stacked branch whose base sibling has advanced must still resolve its span from
// the common ancestor, so the reviewer sees only this changeset's work (plan risk
// table: "main advancing under a changeset shifts merge-base").
func TestStackedChangesetSpanAfterBaseAdvances(t *testing.T) {
	f := newRepo(t)

	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("implement locking", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))
	f.CreateBranch("booking-tests", "booking")
	f.CommitChangeset("booking-tests", "booking")
	f.Commit("add tests", gittest.WithFile("service_test.go", "package main\n\nfunc TestLock() {}\n"))
	own := f.MergeBase("booking", "HEAD")

	// The base branch advances with unrelated work.
	f.SwitchTo("booking")
	f.Commit("more lower work", gittest.WithFile("extra.go", "package main\n\nfunc Extra() {}\n"))

	f.SwitchTo("booking-tests")
	want, err := f.Git("diff", own, "HEAD")
	if err != nil {
		t.Fatalf("git diff: %v", err)
	}
	got := runIn(t, f.Dir(), "diff").mustSucceed(t, "diff")
	if got.stdout != want {
		t.Errorf("`git pair diff` differs from `git diff %s HEAD` after the base advanced", own)
	}
	mustNotContain(t, got.stdout, "extra.go", "work on the base branch must not appear in this changeset's diff")
}
