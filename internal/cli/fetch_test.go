package cli_test

import (
	"path/filepath"
	"testing"

	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
)

// `--fetch` is the read side of the durable refs. The records are written in whichever clone did the
// landing, so every other clone has to ask for them, and `--fetch` asks for two kinds of thing in one
// trip: the records themselves — which are records wherever they are read, because a paper trail that
// replicates is the point — and mirrors under `refs/remotes/<remote>/`, which exist only to be compared
// against. The tests below check that both arrive, that one fetch buys both, and that a mirror can
// never pass for the fact.

// recordedAndPublished lands a changeset, pushes the branch and the namespace to a bare remote, and
// returns a clone of that remote which has not been given the namespace refspec. That clone is the
// state a fresh CI checkout is usually in: the landing is real and published, and this clone cannot
// see it.
func recordedAndPublished(t *testing.T) (*gittest.Fixture, string, string) {
	t.Helper()
	f, slug, source, landing := recordFixture(t)
	f.SwitchTo("main")
	runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing,
		"--target", "release/2.x").mustSucceed(t, "integration", "record")
	remote := filepath.Join(t.TempDir(), "remote.git")
	f.MustGit("init", "--bare", "-b", "main", remote)
	f.MustGit("push", "--quiet", remote, "--all")
	f.MustGit("push", "--quiet", remote, reviewref.FetchRefspec)
	clone := filepath.Join(t.TempDir(), "reader")
	f.MustGit("clone", "--quiet", remote, clone)
	if got := gitIn(t, clone, "for-each-ref", "--format=%(refname)", reviewref.NamespaceRoot); got != "" {
		t.Fatalf("the clone already holds the namespace, so it proves nothing: %s", got)
	}
	if got := gitIn(t, remote, "for-each-ref", "--format=%(refname)", reviewref.NamespaceRoot); got == "" {
		t.Fatalf("the remote holds no durable refs, so no fetch can be the fix: %s", got)
	}
	gitIn(t, clone, "checkout", "--quiet", "-b", "booking", "origin/booking")
	return f, slug, clone
}

// The fetch brings the records home and brings no landing, and the two are now different questions. This
// fixture's record names a commit on `release/2.x`, so the integration branch carries no directory for the
// changeset and the honest answer is "not landed" -- a record is a fact about a command somebody ran, and
// under the tree model it claims nothing about the destination. That is the flexibility the plan decided to
// keep: work merged only into a release line is still work in progress.
func TestFetchBringsTheRecordsHomeAndNoLanding(t *testing.T) {
	_, slug, clone := recordedAndPublished(t)

	before := runIn(t, clone, "status", "--fetch", "--json").mustSucceed(t, "status", "--fetch", "--json").json(t)
	if before["landed"] != false {
		t.Fatalf("landed = %v for %s: nothing in the integration branch carries it", before["landed"], slug)
	}
	if before["unpublished_note"] == "" && len(before["unpublished"].([]any)) == 0 {
		t.Errorf("neither the list nor its note answered: %v", before)
	}
	if got := gitIn(t, clone, "for-each-ref", "--format=%(refname)", reviewref.NamespaceRoot); got == "" {
		t.Fatal("the fetch brought no durable refs home, so the rest of this file proves nothing")
	}
}

// A fetch failure is a warning and the stale answer, never a refusal: "I could not ask" is not a
// verdict about the work, and a command that fails when a remote is unreachable turns a review
// question into an outage.
func TestFetchFailureWarnsAndAnswersAnyway(t *testing.T) {
	_, _, clone := recordedAndPublished(t)
	gitIn(t, clone, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))

	res := runIn(t, clone, "status", "--fetch")
	if res.code != 0 {
		t.Fatalf("status with an unreachable remote exited %d, want the stale answer\n%s%s", res.code, res.stdout, res.stderr)
	}
	mustContain(t, res.stderr, "failed", "the warning says the fetch did not happen")
	mustContain(t, res.stderr, "answering from this clone", "and says what the answer is about")
}

// The two refspecs are one negotiation, not two: the flag buys both at the cost of one git call, which
// is what keeps `queue --fetch` in a pipeline from doubling its network for one question.
func TestFetchIsOneGitInvocation(t *testing.T) {
	f, _, _ := recordedAndPublished(t)
	remote := filepath.Join(t.TempDir(), "second.git")
	f.MustGit("init", "--bare", "-b", "main", remote)
	f.MustGit("push", "--quiet", remote, "--all")
	f.MustGit("push", "--quiet", remote, reviewref.FetchRefspec)
	f.MustGit("remote", "add", "origin", remote)

	count := f.SpawnShim(t)
	before := count()
	runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	plain := count() - before

	before = count()
	runIn(t, f.Dir(), "queue", "--fetch", "--json").mustSucceed(t, "queue", "--fetch", "--json")
	withFetch := count() - before

	// +3, and each unit is accounted for: one `git fetch` for the records, one for the mirrors, and the
	// one call that decides which remote to ask (cached, so the second fetch does not repeat it). The
	// records and the mirrors are two fetches because `--prune` applies to every destination in a command
	// and pruning the record namespace would delete this clone's own unpublished records — see
	// Repo.FetchRefs. The assertion is on the exact number so a third negotiation for the same answer, or
	// a lookup per fetch, shows up as a failure.
	if withFetch != plain+3 {
		t.Fatalf("`queue --fetch` cost %d git invocations against %d for the same queue: expected exactly +2 (one fetch carrying both refspecs, one remote lookup)",
			withFetch, plain)
	}
}
