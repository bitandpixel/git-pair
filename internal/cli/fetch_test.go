package cli_test

import (
	"path/filepath"
	"strings"
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

// The headline, and the shape every fetch test has to have: one clone, one command, two answers, and
// the difference is the flag.
func TestFetchMakesAPublishedRecordVisibleInAFreshClone(t *testing.T) {
	_, slug, clone := recordedAndPublished(t)

	before := runIn(t, clone, "status", "--json").mustSucceed(t, "status", "--json").json(t)
	if before["integrated"] == true {
		t.Fatalf("the clone reports %s integrated without the refs, so the fetch proves nothing", slug)
	}

	after := runIn(t, clone, "status", "--fetch", "--json").mustSucceed(t, "status", "--fetch", "--json").json(t)
	if after["integrated"] != true {
		t.Fatalf("after --fetch the clone still says not integrated: %v\nthe fetch did not bring the record home", after["integrated"])
	}
	if after["integration_ref"] != reviewref.Integration(slug) {
		t.Errorf("integration_ref = %v, want %s", after["integration_ref"], reviewref.Integration(slug))
	}
}

// The mirrors come along in the same fetch, because "has this travelled?" is asked in the same breath
// as "is this recorded?" and a clone holding one without the other would answer inconsistently.
func TestFetchBringsTheMirrorsToo(t *testing.T) {
	_, slug, clone := recordedAndPublished(t)
	runIn(t, clone, "status", "--fetch", "--json").mustSucceed(t, "status", "--fetch", "--json")

	mirrors := gitIn(t, clone, "for-each-ref", "--format=%(refname)", reviewref.MirrorRoot("origin")+"/")
	if mirrors == "" {
		t.Fatalf("the fetch brought no mirrors under %s: %q", reviewref.MirrorRoot("origin"), mirrors)
	}
	for _, want := range []string{reviewref.MirrorIntegration("origin", slug), reviewref.MirrorArchive("origin", slug)} {
		if !strings.Contains(mirrors+"\n", want+"\n") {
			t.Errorf("no mirror %s after --fetch, saw:\n%s", want, mirrors)
		}
	}
}

// The invariant the whole read side rests on: a mirror is somebody else's state seen from here, and
// nothing that answers "is this recorded" may read it. Fetched records land in the namespace proper and
// *do* answer that question — that is replication working. A mirror answering it would let a clone
// report a landing it has only ever glimpsed.
func TestMirrorsAreNeverRecords(t *testing.T) {
	_, slug, clone := recordedAndPublished(t)
	gitIn(t, clone, "fetch", "--quiet", "--prune", "origin", reviewref.MirrorRefspec("origin"))

	if got := gitIn(t, clone, "for-each-ref", "--format=%(refname)", reviewref.MirrorRoot("origin")+"/integrations/"); got == "" {
		t.Fatal("no mirror arrived, so the negative assertion below proves nothing")
	}
	if got := gitIn(t, clone, "for-each-ref", "--format=%(refname)", reviewref.NamespaceRoot); got != "" {
		t.Fatalf("the mirror fetch wrote into the record namespace, which is the failure this test exists for: %s", got)
	}

	out := runIn(t, clone, "status", "--json").mustSucceed(t, "status", "--json").json(t)
	if out["integrated"] == true {
		t.Fatalf("status read a mirror as a record for %s: %v", slug, out["integrated_ref"])
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

	// +2, not +1: one `git fetch` for both refspecs, plus the one call that decides which remote
	// to ask. Two negotiations would be +3, and the number a regression here produces is the point.
	if withFetch != plain+2 {
		t.Fatalf("`queue --fetch` cost %d git invocations against %d for the same queue: expected exactly +2 (one fetch carrying both refspecs, one remote lookup)",
			withFetch, plain)
	}
}
