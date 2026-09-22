package cli_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
)

// A record that exists only in one clone is the state where the paper trail is complete and still
// worthless: it is the memory of a landing, and it dies with the laptop. These tests walk one changeset
// through the three states that matter — nothing on the remote, half of it there, all of it there — and
// then check the two cases where nothing can be claimed: no remote at all, and a mirror namespace this
// clone has never fetched.
//
// The comparison is against this clone's mirrors, never against the remote, so one test pins that too:
// with the remote made unreachable, the finding is still reported. Asking the network is `--fetch`'s
// job, not the finding's.

// publishedFixture lands and records a changeset, points the repository at a bare remote, and leaves the
// publishing of the namespace to the test: which halves have reached the remote is what each test is
// about.
func publishedFixture(t *testing.T) (*gittest.Fixture, string, string) {
	t.Helper()
	fx, s, source, ln := recordFixture(t)
	fx.SwitchTo("main")
	runIn(t, fx.Dir(), "integration", "record", "--source", source, "--commit", ln,
		"--target", "release/2.x").mustSucceed(t, "integration", "record")
	remote := filepath.Join(t.TempDir(), "remote.git")
	fx.MustGit("init", "--bare", "-b", "main", remote)
	fx.MustGit("push", "--quiet", remote, "--all")
	fx.MustGit("remote", "add", "origin", remote)
	// Back on the changeset's branch: `status` there is the question, and on the destination it has no
	// changeset to report.
	fx.SwitchTo("booking")
	return fx, s, ln
}

// The three states, in the order a person actually passes through them.
func TestRecordedNotPublishedIsReportedAndPublishingClearsIt(t *testing.T) {
	f, slug, _ := publishedFixture(t)

	// Nothing of the record is on the remote, and this clone knows it because it just asked.
	out := runIn(t, f.Dir(), "status", "--fetch").mustSucceed(t, "status", "--fetch")
	mustContain(t, out.stdout, "RECORDED, NOT PUBLISHED", "a record that exists only here is the finding")
	mustContain(t, out.stdout, slug, "and it names the changeset")
	mustContain(t, out.stdout, "integration record: not on origin", "naming the half that is missing")
	mustContain(t, out.stdout, "archive: not on origin", "and the other")

	// Half of it published is the worse case and says so: the remote holds a hint that something happened
	// and no way to reconstruct what.
	f.MustGit("push", "--quiet", "origin", reviewref.Archive(slug)+":"+reviewref.Archive(slug))
	out = runIn(t, f.Dir(), "status", "--fetch").mustSucceed(t, "status", "--fetch")
	mustContain(t, out.stdout, "half published", "one ref of the pair on the remote is a distinct state")
	mustContain(t, out.stdout, "cannot be reconstructed", "and the report says why it is the worse one")
	mustNotContain(t, out.stdout, "archive: not on origin", "the half that arrived must not be listed as missing")

	found := runIn(t, f.Dir(), "status", "--fetch", "--json").jsonList(t, "unpublished")
	if len(found) != 1 {
		t.Fatalf("unpublished = %v, want the one changeset", found)
	}
	entry := found[0].(map[string]any)
	if entry["changeset"] != slug {
		t.Errorf("unpublished[0].changeset = %v, want %s", entry["changeset"], slug)
	}
	if missing := entry["missing"].([]any); len(missing) != 1 || missing[0] != "integration" {
		t.Errorf("unpublished[0].missing = %v, want exactly [integration] — the half-published shape is what the JSON is for", entry["missing"])
	}

	// Published: the finding goes quiet. A report that keeps complaining about work that is done trains
	// people to skim past the section.
	f.MustGit("push", "--quiet", "origin", reviewref.Integration(slug)+":"+reviewref.Integration(slug))
	out = runIn(t, f.Dir(), "status", "--fetch").mustSucceed(t, "status", "--fetch")
	mustNotContain(t, out.stdout, "RECORDED, NOT PUBLISHED", "once both refs are on the remote the section is noise")
}

// A clone that has never fetched the remote's copies cannot say anything about publication. The answer is
// one sentence about the clone and its remedy — never a per-changeset accusation, because an unfetched
// clone is one condition and forty changesets did not cause it.
func TestNeverFetchedSaysItCannotTell(t *testing.T) {
	f, _, _ := publishedFixture(t)

	out := runIn(t, f.Dir(), "status").mustSucceed(t, "status")
	mustContain(t, out.stdout, "never fetched", "the finding says what it could not compare")
	mustContain(t, out.stdout, "--fetch", "and names the remedy")
	mustNotContain(t, out.stdout, "RECORDED, NOT PUBLISHED", "it does not claim the finding without the comparison")
	mustNotContain(t, out.stdout, "not on origin", "and it does not blame a changeset for the state of the clone")

	json := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json").json(t)
	if note, ok := json["unpublished_note"].(string); !ok || !strings.Contains(note, "never fetched") {
		t.Errorf("unpublished_note = %v, want the same sentence --json readers get", json["unpublished_note"])
	}
	if found := runIn(t, f.Dir(), "status", "--json").jsonList(t, "unpublished"); len(found) != 0 {
		t.Errorf("unpublished = %v, want empty while nothing can be compared", found)
	}
}

// A repository with no remote has no published state to be missing. The key is still present, and still
// empty: `[]` means "there is nothing unpubished here as far as anyone knows", and a missing key would
// mean the build predates the question.
func TestNoRemoteSaysThereIsNothingToPublishAgainst(t *testing.T) {
	// A fixture with no remote at all: nothing to have published to.
	f, _, _, _ := recordFixture(t)
	f.SwitchTo("booking")

	json := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json").json(t)
	res := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json")
	if found, ok := res.json(t)["unpublished"]; !ok {
		t.Fatalf("`status --json` has no `unpublished` key at all:\n%s", res.stdout)
	} else if list, ok := found.([]any); !ok || len(list) != 0 {
		t.Errorf("`unpublished` = %v, want an empty list rather than null:\n%s", found, res.stdout)
	}
	note, ok := json["unpublished_note"].(string)
	if !ok || !strings.Contains(note, "no remote") {
		t.Errorf("unpublished_note = %v, want the sentence about having no remote", json["unpublished_note"])
	}
}

// The finding reads mirrors and never the remote: with `origin` pointing at a path that does not exist,
// the answer still arrives, because the answer was already in this clone. A detector that asked the
// network would turn an offline review into a report that says nothing.
func TestDetectionNeedsNoNetwork(t *testing.T) {
	f, slug, _ := publishedFixture(t)
	// Half the pair on the remote, so the mirrors exist and hold something to compare; then the remote
	// goes away, and the answer must not go away with it.
	f.MustGit("push", "--quiet", "origin", reviewref.Archive(slug)+":"+reviewref.Archive(slug))
	runIn(t, f.Dir(), "status", "--fetch").mustSucceed(t, "status", "--fetch")
	f.MustGit("remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))

	out := runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
	mustContain(t, out.stdout, "RECORDED, NOT PUBLISHED", "the queue's section answers with the remote unreachable")
	mustContain(t, out.stdout, slug, "and names the changeset")

	// The queue's own heading, styled like the one above it, and the counted sample rule.
	mustContain(t, out.stdout, "RECORDED, NOT PUBLISHED\n\n  "+slug, "the queue gets the section, not a line among the skips")
}

// `check` gates the verdict, not the publishing. A record that has not travelled is a durability risk,
// and refusing the push because of it would block the work to protect the archive — the same call §11.1
// `check` gates the verdict, not the publishing. The same changeset gets the same answer from `check`
// whether or not its record has travelled: publishing is a durability matter, and refusing the push
// because of it would block the work to protect the archive — the call §11.1 makes about unrecorded
// landings, for the same reason. Comparing the two answers is the honest form of this test: `check` has
// its own legitimate refusal for a changeset that is already recorded, and the publishing must not add a
// second reason to it.
func TestCheckAnswersTheSameWhicheverWayTheRecordTravelled(t *testing.T) {
	f, slug, _ := publishedFixture(t)
	runIn(t, f.Dir(), "status", "--fetch").mustSucceed(t, "status", "--fetch")

	unpublished := runIn(t, f.Dir(), "check")
	out := runIn(t, f.Dir(), "status", "--fetch").mustSucceed(t, "status", "--fetch")
	mustContain(t, out.stdout, "RECORDED, NOT PUBLISHED", "first prove the finding is live for this changeset")

	f.MustGit("push", "--quiet", "origin", reviewref.Archive(slug)+":"+reviewref.Archive(slug))
	f.MustGit("push", "--quiet", "origin", reviewref.Integration(slug)+":"+reviewref.Integration(slug))
	runIn(t, f.Dir(), "status", "--fetch").mustSucceed(t, "status", "--fetch")
	published := runIn(t, f.Dir(), "check")

	if unpublished.code != published.code {
		t.Fatalf("`check` exited %d unpublished and %d published: the gate moved with the publishing\n%s\n---\n%s",
			unpublished.code, published.code, unpublished.stdout+unpublished.stderr, published.stdout+published.stderr)
	}
	if unpublished.stdout != published.stdout {
		t.Fatalf("`check`'s answer changed with publishing:\nunpublished: %s\npublished: %s", unpublished.stdout, published.stdout)
	}
}

func TestQueueCarriesAnEmptyUnpublishedKey(t *testing.T) {
	f := newRepo(t)
	res := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	json := res.json(t)
	found, ok := json["unpublished"]
	if !ok {
		t.Fatalf("`queue --json` has no `unpublished` key at all:\n%s", res.stdout)
	}
	if list, ok := found.([]any); !ok || len(list) != 0 {
		t.Errorf("`unpublished` = %v, want an empty list rather than null:\n%s", found, res.stdout)
	}
}

// The finding is one `for-each-ref` of the mirror namespace and a map comparison, so its cost follows the
// command, not the history. A repository with two hundred changesets must not pay two hundred reads — the
// same property §10.6's index gave `LANDED, UNRECORDED`, re-asserted here because this finding was bolted
// onto a command that had already been made cheap once.
func TestDetectionCostDoesNotGrowWithTheNamespace(t *testing.T) {
	f, slug, _ := publishedFixture(t)
	f.MustGit("push", "--quiet", "origin", reviewref.Archive(slug)+":"+reviewref.Archive(slug))
	runIn(t, f.Dir(), "status", "--fetch").mustSucceed(t, "status", "--fetch")

	count := f.SpawnShim(t)
	before := count()
	runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	small := count() - before

	// Fifty more pairs, this clone only — the state the finding exists to notice.
	head := f.RevParse("HEAD")
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("bulk-%02d", i)
		f.MustGit("update-ref", reviewref.Archive(id), head)
		f.MustGit("update-ref", reviewref.Integration(id), head)
	}

	before = count()
	runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	big := count() - before

	if big != small {
		t.Fatalf("`queue` cost %d git invocations with 51 recorded changesets and %d with 1: the finding reads per changeset",
			big, small)
	}
	// And it found them: a cost test that compares two empty answers proves nothing.
	if got := runIn(t, f.Dir(), "queue", "--json").jsonList(t, "unpublished"); len(got) != 51 {
		t.Errorf("unpublished = %d entries, want all 51 local-only pairs; a constant cost that found nothing would pass the assertion above", len(got))
	}
}
