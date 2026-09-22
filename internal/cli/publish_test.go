package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
)

// `integration publish` is where the paper trail leaves the clone that wrote it. These tests are all
// about the same three things: that what the command claims is what the remote ends up holding, that a
// disagreement stops it rather than steering it, and that the half-state a push can leave — one ref of a
// pair accepted, the other refused — is reported as the distinct problem it is instead of collapsing into
// "failed".
//
// The remote is a real repository in every case, because the properties being asserted are the server's
// as much as the client's: an unforced push refusing a non-fast-forward update is git's behaviour, not
// git-pair's, and the conflict policy rests on it.

// remoteOf reads the fixture's origin URL, which the fixtures make a local path so hooks can be installed.
func remoteOf(t *testing.T, f *gittest.Fixture) string {
	t.Helper()
	return strings.TrimSpace(f.MustGit("remote", "get-url", "origin"))
}

// remoteRef reads one ref off the bare remote, so the assertions are about the remote rather than about
// anything git-pair said.
func remoteRef(t *testing.T, remote, ref string) string {
	t.Helper()
	out := gitIn(t, remote, "for-each-ref", "--format=%(objectname)", ref)
	return strings.TrimSpace(out)
}

// unrelatedCommit is a commit with no ancestry to anything in the repository — what a second recorder's
// landing looks like from the remote's point of view.
func unrelatedCommit(t *testing.T, f *gittest.Fixture) string {
	t.Helper()
	tree := strings.TrimSpace(f.MustGit("mktree"))
	return strings.TrimSpace(f.MustGit("commit-tree", tree, "-m", "a different landing"))
}

// The whole point, asserted end to end: publish from the clone that recorded, and a clone that has never
// seen it can read the record.
func TestPublishSendsThePairAndAnotherCloneReadsIt(t *testing.T) {
	f, slug, _ := publishedFixture(t)
	res := runIn(t, f.Dir(), "integration", "publish").mustSucceed(t, "integration", "publish")
	mustContain(t, res.stdout, slug+": published to origin", "the command says what it published")
	mustContain(t, res.stdout, "archive + integration", "and which halves")

	json := runIn(t, f.Dir(), "integration", "publish", "--json").json(t)
	refs := map[string]bool{}
	for _, entry := range json["already_published"].([]any) {
		for _, r := range entry.(map[string]any)["refs"].([]any) {
			refs[r.(string)] = true
		}
	}
	for _, want := range []string{reviewref.Integration(slug), reviewref.Archive(slug)} {
		if !refs[want] {
			t.Errorf("the JSON does not name %s: %v", want, refs)
		}
	}

	remote := remoteOf(t, f)
	for _, ref := range []string{reviewref.Integration(slug), reviewref.Archive(slug)} {
		if remoteRef(t, remote, ref) == "" {
			t.Errorf("%s never reached the remote", ref)
		}
	}

	clone := filepath.Join(t.TempDir(), "reader")
	f.MustGit("clone", "--quiet", remote, clone)
	gitIn(t, clone, "checkout", "--quiet", "-b", "booking", "origin/booking")
	out := runIn(t, clone, "status", "--fetch", "--json").mustSucceed(t, "status", "--fetch", "--json").json(t)
	if out["integrated"] != true {
		t.Fatalf("a fresh clone still cannot see the record after publish + --fetch: %v", out["integrated"])
	}
}

// Idempotence, because the command is meant to run in CI on every build: the second publish confirms
// rather than writes, and the remote's values are byte-identical.
func TestPublishTwiceWritesNothingTheSecondTime(t *testing.T) {
	f, slug, _ := publishedFixture(t)
	runIn(t, f.Dir(), "integration", "publish").mustSucceed(t, "integration", "publish")

	remote := remoteOf(t, f)
	before := map[string]string{}
	for _, ref := range []string{reviewref.Integration(slug), reviewref.Archive(slug)} {
		before[ref] = remoteRef(t, remote, ref)
	}

	first := runIn(t, f.Dir(), "integration", "publish", "--json").mustSucceed(t, "integration", "publish", "--json").json(t)
	if got := len(first["published"].([]any)); got != 0 {
		t.Errorf("published = %v on the second run, want nothing newly published", first["published"])
	}
	already := first["already_published"].([]any)
	if len(already) != 1 {
		t.Fatalf("already_published = %v, want the one pair", already)
	}
	if entry := already[0].(map[string]any); entry["changeset"] != slug {
		t.Errorf("already_published[0] = %v, want %s", entry, slug)
	}

	second := runIn(t, f.Dir(), "integration", "publish").mustSucceed(t, "integration", "publish")
	mustContain(t, second.stdout, "already published", "and the human output says so too")
	for ref, sha := range before {
		if got := remoteRef(t, remote, ref); got != sha {
			t.Errorf("%s moved from %s to %s on a publish that wrote nothing", ref, sha, got)
		}
	}
}

// A remote that holds a different value is a disagreement between two people who recorded the same
// changeset, and the command's answer is to stop. This is the case a `--force` would erase: the SHAs are
// named on both sides, and the word "force" does not appear.
func TestPublishRefusesWhereTheRemoteDisagrees(t *testing.T) {
	f, slug, _ := publishedFixture(t)
	runIn(t, f.Dir(), "integration", "publish").mustSucceed(t, "integration", "publish")

	// Somebody else's record arrives at the remote, moving the integration ref off ours.
	other := unrelatedCommit(t, f)
	f.MustGit("push", "--quiet", "--force", "origin", other+":"+reviewref.Integration(slug))
	ours := strings.TrimSpace(f.MustGit("rev-parse", reviewref.Integration(slug)))

	res := runIn(t, f.Dir(), "integration", "publish")
	if res.code == 0 {
		t.Fatalf("publish succeeded over a remote record it does not hold:\n%s%s", res.stdout, res.stderr)
	}
	combined := res.stdout + res.stderr
	mustContain(t, combined, "NOT published", "the refusal is explicit")
	mustContain(t, combined, shortOf(other), "naming what the remote holds")
	mustContain(t, combined, shortOf(ours), "and what this clone holds")
	mustNotContain(t, combined, "--force", "and never suggests overwriting the other recorder")

	if got := remoteRef(t, remoteOf(t, f), reviewref.Integration(slug)); got != other {
		t.Errorf("the remote's record moved from %s to %s: the refusal pushed anyway", other, got)
	}
}

// The half-state, which is not hypothetical: git applies the refspecs a server accepts and exits non-zero
// for the ones it rejects. A command that reported this as a single failure would hide that the remote now
// holds one half of a record it cannot reconstruct.
func TestPublishReportsAHalfPublishedPair(t *testing.T) {
	f, slug, _ := publishedFixture(t)
	remote := remoteOf(t, f)
	hook := filepath.Join(remote, "hooks", "update")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ncase \"$1\" in\n"+
		"refs/git-pair/integrations/*) echo refused by policy >&2; exit 1;;\nesac\n"), 0o755); err != nil {
		t.Fatalf("install hook: %v", err)
	}

	res := runIn(t, f.Dir(), "integration", "publish")
	if res.code == 0 {
		t.Fatalf("publish reported success with half the pair refused:\n%s", res.stdout)
	}
	mustContain(t, res.stdout+res.stderr, "NOT published", "the pair is refused")
	if remoteRef(t, remote, reviewref.Archive(slug)) == "" {
		t.Error("the accepted half did not reach the remote, so the half-state this test is about cannot occur")
	}

	json := runIn(t, f.Dir(), "integration", "publish", "--json").json(t)
	failed, ok := json["failed"].([]any)
	if !ok || len(failed) != 1 {
		t.Fatalf("failed = %v, want the one pair", json["failed"])
	}
	entry := failed[0].(map[string]any)
	if entry["changeset"] != slug {
		t.Errorf("failed[0].changeset = %v, want %s", entry["changeset"], slug)
	}
	if entry["half_state"] != true {
		t.Errorf("failed[0] = %v, want half_state: true — one ref of the pair arrived", entry)
	}
	if entry["family"] != familyIntegration {
		t.Errorf("failed[0].family = %v, want %q", entry["family"], "integration")
	}
	mustNotContain(t, fmt.Sprint(json), "--force", "and the JSON does not suggest forcing either")
}

// The remote is named, not guessed, and the two refusals are different sentences: no remote at all is a
// repository that has never been published from, and a name that does not exist is a typo.
func TestPublishRefusesWhenThereIsNowhereToPublish(t *testing.T) {
	f, _, _ := publishedFixture(t)
	remote := remoteOf(t, f)
	f.MustGit("remote", "remove", "origin")

	res := runIn(t, f.Dir(), "integration", "publish")
	if res.code == 0 {
		t.Fatal("publish succeeded with no remote")
	}
	mustContain(t, res.stdout+res.stderr, "no remote", "and says what it looked for")

	f.MustGit("remote", "add", "origin", remote)
	res = runIn(t, f.Dir(), "integration", "publish", "--remote", "nowhere")
	if res.code == 0 {
		t.Fatal("publish succeeded to a remote that does not exist")
	}
	mustContain(t, res.stdout+res.stderr, `"nowhere"`, "naming the remote that was asked for")
}

// A repository with nothing to publish gets three empty arrays, not three absences: the command answered
// the question, and a consumer should not have to know which build introduced it.
func TestPublishJSONCarriesEveryArrayWhenThereIsNothingToPublish(t *testing.T) {
	f, _, _, _ := recordFixture(t)
	f.SwitchTo("main")
	remote := filepath.Join(t.TempDir(), "remote.git")
	f.MustGit("init", "--bare", "-b", "main", remote)
	f.MustGit("remote", "add", "origin", remote)

	res := runIn(t, f.Dir(), "integration", "publish", "--json").mustSucceed(t, "integration", "publish", "--json")
	for _, key := range []string{"published", "already_published", "failed"} {
		list, ok := res.json(t)[key].([]any)
		if !ok {
			t.Errorf("`%s` is %v (%T), want an array", key, res.json(t)[key], res.json(t)[key])
			continue
		}
		if len(list) != 0 {
			t.Errorf("`%s` = %v, want empty", key, list)
		}
	}
}

// Publishing a named changeset that has no record here is a refusal rather than a successful no-op: the
// command's whole purpose is that nothing goes silently unpublished, and the likeliest reason a name has
// no record is that it is spelled wrong or was recorded elsewhere.
func TestPublishNamedWithoutARecordRefuses(t *testing.T) {
	f, _, _ := publishedFixture(t)
	res := runIn(t, f.Dir(), "integration", "publish", "bookingg")
	if res.code == 0 {
		t.Fatal("publish accepted a name with no record behind it")
	}
	mustContain(t, res.stdout+res.stderr, "no integration record for bookingg", "and says which name it could not place")
}

const familyIntegration = "integration"
