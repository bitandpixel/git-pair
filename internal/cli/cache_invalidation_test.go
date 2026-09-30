package cli_test

import (
	"encoding/json"
	"testing"

	"gitpair/internal/gittest"
)

// The caches change how a command gets its answers, not what the answers are. Everything below is one
// claim stated four ways: a run that read its answers from disk says exactly what a run that asked git
// says, and it still says so after the repository moves under it.
//
// The four ways are the four things that could go wrong. An entry could outlive the commit it described
// (a destination that moved). An entry for a branch could outlive that branch (a new commit, or a rewind).
// A cached answer could go stale inside one run (a marker recorded by the command itself). And a cached
// "nothing to report" could be served as if it were a row.
//
// Each test ends by asserting that the state it built actually changed the output. A comparison between
// two runs of the same thing passes whether or not anything moved, so the inert version of these tests
// would be green forever — which is the failure mode a cache is uniquely good at hiding.

// queueSnapshot is `queue --json` in the form worth comparing: the wall-clock ages are dropped, because
// they are the one part of the output that is a fact about the moment it was printed rather than about the
// repository. Everything else must match byte for byte.
func queueSnapshot(t *testing.T, f *gittest.Fixture, extra ...string) string {
	t.Helper()
	args := append([]string{"queue", "--json"}, extra...)
	res := runIn(t, f.Dir(), args...).mustSucceed(t, args...)
	doc := res.json(t)
	for _, key := range []string{"ready_for_review", "awaiting_integration"} {
		list, ok := doc[key].([]any)
		if !ok {
			continue
		}
		for _, item := range list {
			row, ok := item.(map[string]any)
			if !ok {
				continue
			}
			delete(row, "ready_age")
			delete(row, "declared_age")
		}
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("re-encode the queue: %v", err)
	}
	return string(out)
}

// statusSnapshot is the same for `status --changeset <slug> --json`, with the ages it prints dropped.
func statusSnapshot(t *testing.T, f *gittest.Fixture, slug string, extra ...string) string {
	t.Helper()
	args := append([]string{"status", "--changeset", slug, "--json"}, extra...)
	res := runIn(t, f.Dir(), args...).mustSucceed(t, args...)
	doc := res.json(t)
	delete(doc, "reason") // "marked ready by <sha>" names a commit, but the reason is the surface under test elsewhere
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("re-encode the status: %v", err)
	}
	return string(out)
}

// cacheFixture builds a repository with one offered changeset, one still being written, and one landed with
// no review of any kind — the three kinds of row the queue and the landings report read.
//
// Each branch is cut from `main` by name. `CreateBranch` defaults to the checked-out branch, and a beta cut
// from alpha carries alpha's directory as well as its own, which turns every later question into one about
// a branch carrying three changesets.
func cacheFixture(t *testing.T) *gittest.Fixture {
	t.Helper()
	f := newRepo(t)
	f.CreateBranch("alpha", "main")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	ready(t, f)

	f.CreateBranch("beta", "main")
	f.CommitChangeset("beta", "main")
	f.Commit("beta work", gittest.WithFile("b.go", "package main\n"))

	// gamma reaches main by an ordinary merge, with no marker in front of it. That is the shape the
	// landings report exists for: the directory is on the destination and nothing in its chain approved it.
	f.CreateBranch("gamma", "main")
	f.CommitChangeset("gamma", "main")
	f.Commit("gamma work", gittest.WithFile("c.go", "package main\n"))
	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "-m", "gamma: merge the branch", "gamma")
	return f
}

func TestQueueAnswersTheSameWarmAndCold(t *testing.T) {
	f := cacheFixture(t)

	// The first run is asked to ignore the caches, so it neither reads nor writes them and the comparison
	// below does not depend on which run happened first.
	cold := queueSnapshot(t, f, "--no-cache")
	warm := queueSnapshot(t, f)  // fills the caches
	again := queueSnapshot(t, f) // reads them back

	if cold != warm {
		t.Errorf("the run that filled the caches disagreed with the run that ignored them:\n%s\n----\n%s", cold, warm)
	}
	if cold != again {
		t.Errorf("the run that read the caches disagreed with the run that ignored them:\n%s\n----\n%s", cold, again)
	}
	// Guard against the fixture producing a queue nobody could tell apart from an empty one.
	if len(queueRows(t, cold)) == 0 {
		t.Fatal("the fixture queued nothing, so the comparison above proves nothing")
	}
}

// The destination branch is the commit every landed answer is derived from. This is the case the cache is
// keyed on, and the one where getting it wrong would print a finding about a trunk that no longer exists.
func TestQueueRewarmsWhenTheDestinationMoves(t *testing.T) {
	f := cacheFixture(t)
	before := queueSnapshot(t, f, "--no-cache")
	queueSnapshot(t, f) // warm the caches against the destination as it stands

	// Land alpha. The destination gains its directory, alpha's branch stops being work in progress, and
	// every answer derived from the old destination commit is now about a commit that is not the tip.
	f.SwitchTo("alpha")
	landAndRecord(t, f, "alpha", "main")
	f.SwitchTo("main")

	want := queueSnapshot(t, f, "--no-cache")
	got := queueSnapshot(t, f)
	if want != got {
		t.Errorf("a cached answer about the old destination was served:\n%s\n----\n%s", want, got)
	}
	if before == want {
		t.Error("landing alpha changed nothing in the queue, so this test could not have caught a stale answer")
	}
}

// A branch tip is a separate key from the destination's. Offering one changeset must not freeze the others
// at whatever they were when the destination last moved.
func TestQueueRewarmsWhenABranchTipMoves(t *testing.T) {
	f := cacheFixture(t)
	before := queueSnapshot(t, f, "--no-cache")
	queueSnapshot(t, f)

	// beta is being written while the caches fill, then it is offered. The destination never moved; only
	// beta's branch did, and the row it gains can only come from a derivation that noticed the new tip.
	f.SwitchTo("beta")
	ready(t, f)
	f.SwitchTo("main")

	want := queueSnapshot(t, f, "--no-cache")
	got := queueSnapshot(t, f)
	if want != got {
		t.Errorf("a cached answer about the old branch tip was served:\n%s\n----\n%s", want, got)
	}
	if !contains(queueRows(t, want), "beta") {
		t.Fatal("offering beta did not put it in the queue, so this test compared the wrong thing")
	}
	if contains(queueRows(t, before), "beta") {
		t.Fatal("beta was already queued before it was offered, so this test compared the wrong thing")
	}
}

// The state a queue row reports comes from the markers on the branch. A submission that answers an offer
// removes the row, and it is the kind of change a cache could hold silently: same changeset, same
// directory, same destination, one new commit the cached derivation never saw.
func TestQueueRewarmsWhenAReviewArrives(t *testing.T) {
	f := cacheFixture(t)
	f.SwitchTo("alpha")
	before := queueSnapshot(t, f, "--no-cache")
	queueSnapshot(t, f)

	submit(t, f, "approve")
	f.SwitchTo("main")

	want := queueSnapshot(t, f, "--no-cache")
	got := queueSnapshot(t, f)
	if want != got {
		t.Errorf("a cached lifecycle answer was served after a review submission:\n%s\n----\n%s", want, got)
	}
	if contains(queueRows(t, before), "alpha") == contains(queueRows(t, want), "alpha") {
		t.Error("the approval did not change whether alpha is queued, so this test could not have caught a stale answer")
	}
}

// A write command must not read an answer from before its own commit. `change ready` derives state, writes
// a marker, and reports what it did; a memo that survived the commit would have it report the state it
// replaced.
func TestRecordingAMarkerIsNotServedFromBeforeTheCommit(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))

	// Derive the state first, the way a command does before it decides what to write.
	if got := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)["state"]; got != "WORKING" {
		t.Fatalf("state before the offer is %v, want WORKING", got)
	}
	runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")

	got := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)["state"]
	if got != "READY" {
		t.Errorf("after `change ready` the state is %v, want READY: the answer came from before the commit", got)
	}
}

// A cached "there is nothing to report" is an answer, and it must not arrive as a row. The landings report
// caches one record per landed changeset, and a record whose finding is absent has to stay absent rather
// than render as an entry with no changeset, no commit and no reason.
func TestCachedAbsenceDoesNotBecomeARow(t *testing.T) {
	f := cacheFixture(t)
	// delta lands through the full loop, approval included, so it is landed and there is nothing to report.
	f.CreateBranch("delta", "main")
	f.CommitChangeset("delta", "main")
	f.Commit("delta work", gittest.WithFile("d.go", "package main\n"))
	landAndRecord(t, f, "delta", "main")
	f.SwitchTo("main")

	// Two runs: one to fill the record for delta, one to read it back.
	first := queueSnapshot(t, f, "--no-cache")
	second := queueSnapshot(t, f)
	third := queueSnapshot(t, f)
	if first != second || first != third {
		t.Errorf("the landings report changed between runs:\n%s\n----\n%s", first, third)
	}
	for _, doc := range []string{first, second, third} {
		for _, row := range queueRows(t, doc) {
			if row == "" {
				t.Error("a landed-unreviewed row arrived with no changeset in it")
			}
		}
	}
	if !contains(queueRows(t, first), "gamma") {
		t.Error("gamma lost its unreviewed landing, so the fixture stopped exercising the list it is testing")
	}
	if contains(queueRows(t, first), "delta") {
		t.Error("delta landed with an approval and was still reported as unreviewed")
	}
}

// queueRows is every name the queue printed under either heading, so a test can say "alpha is in there".
func queueRows(t *testing.T, doc string) []string {
	t.Helper()
	var parsed struct {
		Ready []struct {
			Changeset string `json:"changeset"`
		} `json:"ready_for_review"`
		Landed []struct {
			Changeset string `json:"changeset"`
		} `json:"landed_unreviewed"`
	}
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("parse the snapshot: %v\n%s", err, doc)
	}
	var out []string
	for _, e := range parsed.Ready {
		out = append(out, e.Changeset)
	}
	for _, e := range parsed.Landed {
		out = append(out, e.Changeset)
	}
	return out
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
