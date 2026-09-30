package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// A changeset records `base: main` because that is the spelling a file other machines read should carry,
// and a bare name resolves under `refs/heads/` first. In the clone `git clone` leaves behind that branch is
// where trunk stood the day this one was cut, and nobody moves it again — `git fetch` moves
// `refs/remotes/origin/main` and leaves the local branch alone. Measuring from the local copy then puts
// every commit the destination gained in between into this changeset's diff, and a reviewer reads someone
// else's merged work as the author's. The three tests below are the same repository measured three ways.

// fetchedTrunk moves the remote's trunk past this clone's and leaves `refs/heads/main` where it was, which
// is the ordinary state of a person who fetches and never updates their local trunk.
func fetchedTrunk(t *testing.T, f *gittest.Fixture, file string) string {
	t.Helper()
	f.SwitchTo("main")
	f.CreateBranch("fetched")
	there := f.Commit("main: someone else's merged work", gittest.WithFile(file, "package main\n"))
	f.MustGit("update-ref", "refs/remotes/origin/main", there)
	f.SwitchTo("main")
	f.ForceDeleteBranch("fetched")
	return there
}

// The diff the report is about: after a rebase onto the fetched trunk, the destination's own work is not
// part of this changeset, and the span says which base it was measured from.
func TestDiffMeasuresAgainstTheFetchedTrunk(t *testing.T) {
	f, _ := newChangeset(t, "feature/booking-transaction", "main")
	fetchedTrunk(t, f, "unrelated.go")
	f.SwitchTo("feature/booking-transaction")
	f.MustGit("rebase", "refs/remotes/origin/main")

	res := runIn(t, f.Dir(), "diff").mustSucceed(t, "diff")
	mustContain(t, res.stderr, "origin/main...current", "the span names the base it measured against")
	mustNotContain(t, res.stdout, "unrelated.go", "work the destination already has is not this changeset's diff")
	mustContain(t, res.stdout, "service.go", "the changeset's own work stays in the diff")
}

// `status` reports the base it measured against beside the base the changeset recorded, so the screen and
// the span on it are one answer. The record itself does not move: `base:` is committed content other
// machines read, and a fetch ref written there reads as a destination this clone can name and nobody else
// can.
func TestStatusNamesTheBaseItMeasuredAgainst(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	fetchedTrunk(t, f, "unrelated.go")
	f.SwitchTo("booking")
	f.MustGit("rebase", "refs/remotes/origin/main")

	res := runIn(t, f.Dir(), "status").mustSucceed(t, "status")
	mustContain(t, res.stdout, "Base: origin/main — the base names the integration branch",
		"the base line names the copy it measured against, not just a branch")
	mustContain(t, res.stdout, "Span: origin/main...current", "and the span agrees with it")

	out := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json").json(t)
	if out["base"] != "main" {
		t.Errorf("base = %v, want the recorded value: CHANGESET.yaml still says main", out["base"])
	}
	if out["base_ref"] != "refs/remotes/origin/main" {
		t.Errorf("base_ref = %v, want the ref every diff here was measured against", out["base_ref"])
	}
	if why, _ := out["base_why"].(string); !strings.Contains(why, "fetched") {
		t.Errorf("base_why = %q, want the sentence naming the copy it chose", why)
	}
	if body := f.Read("changesets/booking/CHANGESET.yaml"); strings.Contains(body, "refs/remotes") {
		t.Errorf("CHANGESET.yaml carries a fetch ref:\n%s\nwhich is a per-clone fact written into committed content", body)
	}
}

// A clone holding only a local trunk answers the way it always has: the recorded name is the whole answer,
// because there is no second copy of the branch to choose between.
func TestBaseStaysTheRecordedNameWithoutAFetchRef(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")

	res := runIn(t, f.Dir(), "status").mustSucceed(t, "status")
	mustContain(t, res.stdout, "Base: main\n", "no fetch ref, no second answer")
	mustNotContain(t, res.stdout, "base names the integration branch",
		"a clone with one copy of trunk has nothing to explain about which one it used")

	out := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json").json(t)
	if _, ok := out["base_ref"]; ok {
		t.Errorf("base_ref = %v, want it absent: the recorded base is the measured base here", out["base_ref"])
	}
}
