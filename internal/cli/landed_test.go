package cli_test

import (
	"fmt"
	"strings"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
)

// The state these tests are about is the one the tool cannot see: the merge happens outside git-pair,
// and the step that writes the record is prose in an agent contract. A changeset directory in the
// destination branch with no integration ref is that step skipped — and because the tree rule makes a
// directory the destination carries stop being a claim, it is also the state in which the changeset
// vanishes from every report unless one of them goes looking for it.

// landedFixture is a reviewed changeset whose work is in `main`'s tree with nothing written to the
// durable namespace. The landing commit is squash-shaped — the directory copied onto a fresh commit —
// which is the shape that destroys the branch's chain, and the one a merged changeset leaves behind
// when the merge was a squash.
func landedFixture(t *testing.T) (*gittest.Fixture, string, string) {
	t.Helper()
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "approve")
	source := f.Head()
	f.SwitchTo("main")
	f.MustGit("checkout", source, "--", changeset.Root+"/"+slug)
	f.Commit(slug+": land the reviewed work", gittest.WithFile("landed.md", "landed\n"))
	return f, slug, f.Head()
}

// landExtra puts another changeset directory in `main` without going through the lifecycle, for tests
// about how many of these a report has to carry.
func landExtra(t *testing.T, f *gittest.Fixture, id string) {
	t.Helper()
	f.SwitchTo("main")
	f.Commit(id+": carry another changeset directory",
		gittest.WithFile(changeset.Root+"/"+id+"/CHANGESET.yaml", "id: "+id+"\nbase: main\n"))
}

func TestQueueAndStatusReportALandingNobodyRecorded(t *testing.T) {
	f, slug, _ := landedFixture(t)

	q := runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
	mustContain(t, q.stdout, "LANDED, UNRECORDED", "the finding is a section of its own, not a note")
	mustContain(t, q.stdout, slug, "and it names the changeset")
	mustContain(t, q.stdout, "git pair integration record --changeset "+slug,
		"and prints the invocation that closes the gap")
	mustContain(t, q.stdout, "nothing is ready", "the queue's own answer is unchanged")

	// `status` on the destination branch has nothing to say about work in progress, and the finding is
	// the half of its answer that is worth reading. Exit 2 stays: the branch really does not carry a
	// changeset, and that is what the code means.
	st := runIn(t, f.Dir(), "status")
	if st.code != exitUsage {
		t.Fatalf("status on the destination branch exited %d, want %d\n%s%s", st.code, exitUsage, st.stdout, st.stderr)
	}
	combined := st.stdout + st.stderr
	mustContain(t, combined, "no changeset for this branch", "the original answer is still there")
	mustContain(t, combined, "no integration record", "and the finding is attached to it")
	mustContain(t, combined, "git pair integration record --changeset "+slug, "with the way out")

	// Recording it retires the finding from both reports, because the finding is about the absence.
	runIn(t, f.Dir(), "integration", "record", "--changeset", slug).mustSucceed(t, "integration", "record")
	after := runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
	mustNotContain(t, after.stdout, "LANDED, UNRECORDED", "a recorded landing is not a finding")
	mustNotContain(t, after.stdout, slug, "and the changeset is not named in the queue at all")
	quiet := runIn(t, f.Dir(), "status")
	if quiet.stdout+quiet.stderr != st.stdout+st.stderr && strings.Contains(quiet.stdout+quiet.stderr, "no integration record") {
		t.Errorf("status still reports the finding after the record:\n%s%s", quiet.stdout, quiet.stderr)
	}
	if quiet.code != exitUsage {
		t.Errorf("status on a destination branch with nothing unrecorded exited %d, want %d", quiet.code, exitUsage)
	}
}

// The retired layout is retired. A landing written down at `refs/git-pair/changesets/<id>/integration` by
// the pre-two-ref code is not a record of anything this code reads, so the queue reports the landing as
// unrecorded and names the command that records it in a family that is read.
//
// What the retired ref *does* still answer is the other question — has this clone fetched the namespace at
// all — and that one must not be answered "no". A report telling the reader to fetch, in a repository that
// fetched and holds only the old spelling, sends them to a command that cannot change the answer.
func TestQueueReportsALandingTheRetiredLayoutMissed(t *testing.T) {
	f, slug, landing := landedFixture(t)
	f.MustGit("update-ref", "refs/git-pair/changesets/"+slug+"/integration", landing)

	q := runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
	mustContain(t, q.stdout, "LANDED, UNRECORDED", "a retired path records no landing")
	mustContain(t, q.stdout, slug, "so the changeset is reported")
	mustNotContain(t, q.stderr, "holds no refs/git-pair/* refs at all", "and the retired ref says this clone did fetch the namespace")
	mustContain(t, q.stderr, "means none *in this clone*", "so the hedge offered is the true one, not the fetch hint")

	// The current family is what settles it: the same queue, with the landing recorded where the code reads.
	f.MustGit("update-ref", reviewref.Integration(slug), landing)
	q = runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
	mustNotContain(t, q.stdout, "LANDED, UNRECORDED", "the record is what the report asks for")
	mustNotContain(t, q.stdout, slug, "and the changeset is left alone once it exists")
}

// An empty namespace is one condition about the clone, not one condition per changeset: §13.4's guidance
// is a fetch, and printing it three times would make three findings look like three problems.
func TestQueueSaysOnceThatTheNamespaceIsAbsent(t *testing.T) {
	f, _, _ := landedFixture(t)
	landExtra(t, f, "docs-cleanup")
	landExtra(t, f, "refactor-parser")

	q := runIn(t, f.Dir(), "queue")
	q.mustSucceed(t, "queue")
	if n := strings.Count(q.stderr, reviewref.FetchCommand); n != 1 {
		t.Errorf("the fetch guidance appears %d times, want once (three unrecorded landings):\n%s", n, q.stderr)
	}
	mustContain(t, q.stderr, "no refs/git-pair/* refs at all", "and it says what is actually missing")
}

// Once the namespace holds refs, "no record" has two readings and the report keeps both: nobody wrote
// it, or somebody did in the clone that ran the merge. The difference matters to the reader — one fix
// is a command, the other is a fetch.
func TestQueueHedgesWhenOtherRecordsExist(t *testing.T) {
	f, slug, _ := landedFixture(t)
	other := f.Head()
	// Another changeset's record, written the way the durable pair is written: the namespace is not
	// empty, so "no record" is a claim about this changeset rather than about the clone.
	f.MustGit("update-ref", reviewref.Integration("someone-elses-work"), other)

	q := runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
	mustContain(t, q.stdout, slug, "the unrecorded landing is still reported")
	mustNotContain(t, q.stderr, "no refs/git-pair/* refs at all",
		"but this clone does hold durable refs, so that claim would be false")
	mustContain(t, q.stderr, reviewref.FetchCommand, "and the other reading is named")
}

// The queue is also a notification surface, so it prints a counted sample and lets `--json` carry the
// whole answer. A repository with fifty unrecorded landings has a workflow problem, and a fifty-line
// queue is not the report that fixes it.
func TestQueueCountsLandingsItDoesNotPrint(t *testing.T) {
	f, _, _ := landedFixture(t)
	for i := 0; i < 11; i++ {
		landExtra(t, f, fmt.Sprintf("landing-%02d", i))
	}

	q := runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
	mustContain(t, q.stdout, "booking", "the first findings are printed")
	mustContain(t, q.stdout, "and 2 more", "the rest are counted")
	if n := strings.Count(q.stdout, "git pair integration record --changeset"); n != 10 {
		t.Errorf("the queue printed %d invocations, want the 10-item cap:\n%s", n, q.stdout)
	}
	rows := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue").jsonList(t, "landed_unrecorded")
	if len(rows) != 12 {
		t.Errorf("--json lists %d unrecorded landings, want all 12", len(rows))
	}
}

// The section is an answer to a question, so it always has one: `[]` says "asked, and none", where null
// would be indistinguishable from a build that never asked. (`skipped` stays a nullable note list.)
func TestQueueJSONCarriesTheSectionAsAnArray(t *testing.T) {
	fresh := newRepo(t)
	out := runIn(t, fresh.Dir(), "queue", "--json").mustSucceed(t, "queue").json(t)
	value, ok := out["landed_unrecorded"]
	if !ok {
		t.Fatalf("the queue's JSON has no %q key: %v", "landed_unrecorded", out)
	}
	if rows, isList := value.([]any); !isList || len(rows) != 0 {
		t.Errorf("landed_unrecorded = %#v, want an empty array", value)
	}

	f, slug, _ := landedFixture(t)
	rows := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue").jsonList(t, "landed_unrecorded")
	if len(rows) != 1 {
		t.Fatalf("landed_unrecorded = %v, want one entry", rows)
	}
	entry, isObject := rows[0].(map[string]any)
	if !isObject {
		t.Fatalf("landed_unrecorded[0] = %T, want an object", rows[0])
	}
	if entry["changeset"] != slug {
		t.Errorf("changeset = %v, want %q", entry["changeset"], slug)
	}
	if entry["command"] != "git pair integration record --changeset "+slug {
		t.Errorf("command = %v, want the invocation that closes the gap", entry["command"])
	}
}

// Live work is the opposite finding, and a report that confused them would be worse than either. A
// changeset whose branch is checked out, unlanded, and READY is queue material; it is not a landing, so
// it must not appear under this heading, and `status` on it has nothing to annotate.
func TestQueueDoesNotReportLiveWorkAsALanding(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)

	q := runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
	mustContain(t, q.stdout, "READY FOR REVIEW", "the changeset is in the queue")
	mustContain(t, q.stdout, slug, "under its own heading")
	mustNotContain(t, q.stdout, "LANDED, UNRECORDED", "and nowhere else")
	mustNotContain(t, q.stderr, reviewref.FetchCommand, "an empty namespace is not a finding on its own")

	st := runIn(t, f.Dir(), "status").mustSucceed(t, "status")
	mustNotContain(t, st.stdout, "no integration record", "status on live work says nothing about landings")
}

// A changeset that landed on a branch nobody calls trunk is not this finding either: its branch, its
// markers and its own refusals are all still there, which is why `integration record` can be told where
// it went. The heading belongs to the case where the destination swallowed the work silently.
func TestQueueDoesNotReportALandingOnAnotherBranch(t *testing.T) {
	f, slug, _, _ := recordFixture(t) // lands on release/2.x, unrecorded by design

	q := runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
	mustNotContain(t, q.stdout, "LANDED, UNRECORDED", "a landing outside the destination is not this finding")
	mustNotContain(t, q.stdout, slug, "and the changeset is not named")
}

// A merged branch is where a reader often still stands when the merge happened, and `status` there is the
// ordinary way to meet this answer. The hint used to say "run `git pair init`", which starts a second
// changeset over work that already has a record.
func TestStatusOnALandedChangesetsOwnBranchSaysItLanded(t *testing.T) {
	f, slug, _ := landedFixture(t)
	f.SwitchTo(slug) // the changeset branch, still carrying its own directory

	res := runIn(t, f.Dir(), "status")
	if res.code != exitUsage {
		t.Fatalf("status exited %d, want %d\nstderr: %s", res.code, exitUsage, res.stderr)
	}
	combined := res.stdout + res.stderr
	mustContain(t, combined, "no changeset for this branch", "the branch holds no work in progress")
	mustContain(t, combined, "that changeset landed", "and the answer says what happened")
	mustContain(t, combined, "git pair status --changeset "+slug, "and names the read that answers")
	if strings.Contains(combined, "run `git pair init") {
		t.Errorf("the answer sends the reader to init over work with a record\n%s", combined)
	}
}
