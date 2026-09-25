package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
)

// `git pair integration configure` is the only place git-pair writes configuration, and the command *is*
// the consent: PRD §22 makes this CLI the agent surface, so the alternative — asking at the terminal —
// would make one command line mean two things depending on where it ran, and an unanswered prompt in CI
// reads exactly like a declined one. These tests are the consequences of that choice: nothing blocks,
// nothing asks, no other command writes config, and this one writes exactly two additive lines, once.
//
// It is a command rather than a flag on `record` because the clone that wants the configuration is not
// always a clone that is recording: `record` needs the two SHAs a landing produced, and a clone that
// arrived after the fact has neither.

// configOf reads the clone's own config file, because the assertions that matter are about the file: a
// message saying "already configured" is worth nothing unless the file did not change.
func configOf(t *testing.T, f *gittest.Fixture) string {
	t.Helper()
	path := filepath.Join(f.Dir(), ".git", "config")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(src)
}

// configValues reads one multi-valued key straight from git, so the assertions are about the repository
// rather than about anything git-pair printed. An unset key is git's exit 1, which is an answer and not a
// failure: the empty list is what "no line written" looks like in the file.
func configValues(t *testing.T, f *gittest.Fixture, key string) []string {
	t.Helper()
	out, err := f.Git("config", "--local", "--get-all", key)
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}
	return strings.Split(strings.TrimSpace(out), "\n")
}

// remoteWith points the fixture at a fresh bare remote under origin, which is the state the command needs
// and the state the record fixtures deliberately do not have.
func remoteWith(t *testing.T, f *gittest.Fixture) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	f.MustGit("init", "--bare", "-b", "main", remote)
	f.MustGit("remote", "add", "origin", remote)
	return remote
}

// A record writes refs and nothing else: the clone's config is untouched, and the answer names the command
// that would have changed it.
func TestRecordWritesNoConfigAndNamesTheCommand(t *testing.T) {
	f, _, source, landing := recordFixture(t)
	f.SwitchTo("main")
	remoteWith(t, f)
	before := configValues(t, f, "remote.origin.fetch")

	res := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing,
		"--target", "release/2.x").mustSucceed(t, "integration", "record")
	mustContain(t, res.stdout, "git pair integration configure", "the run names the command it did not run")
	mustContain(t, res.stdout, "remote.origin.fetch", "and the key it would have written")
	mustContain(t, res.stdout, "remote.origin.push", "and the other one")
	if got := configValues(t, f, "remote.origin.fetch"); len(got) != len(before) {
		t.Errorf("record changed remote.origin.fetch from %v to %v: configuration is not record's to write", before, got)
	}
	if got := configValues(t, f, "remote.origin.push"); len(got) != 0 {
		t.Errorf("record wrote remote.origin.push = %v, want the key untouched", got)
	}
	if durableRefs(t, f) == nil {
		t.Error("the record was not written: configuration is the optional half, not the gate")
	}

	// In JSON the absence is the answer: a pipeline that never asked about configuration must be able to
	// see that this run's answer has nothing to do with configuration.
	json := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing,
		"--target", "release/2.x", "--json").json(t)
	if json["fetch_config"] != nil {
		t.Errorf("fetch_config = %v on a record run, want the key gone with the flag it reported", json["fetch_config"])
	}
}

// The default: both lines, each the mirror or the record refspec only, and the answer names the key and
// the value for both.
func TestConfigureWritesBothRefspecs(t *testing.T) {
	f, _, _, _ := recordFixture(t)
	f.SwitchTo("main")
	remoteWith(t, f)
	before := configValues(t, f, "remote.origin.fetch")

	res := runIn(t, f.Dir(), "integration", "configure").mustSucceed(t, "integration", "configure")
	fetch, push := reviewref.MirrorRefspec("origin"), reviewref.PushRefspec
	mustContain(t, res.stdout, fetch, "the answer prints the fetch value it wrote")
	mustContain(t, res.stdout, push, "and the push value")
	mustContain(t, res.stdout, "added "+fetch+" to remote.origin.fetch", "naming the key and saying it wrote")
	mustContain(t, res.stdout, "added "+push+" to remote.origin.push", "for both halves")

	got := configValues(t, f, "remote.origin.fetch")
	if len(got) != len(before)+1 || got[len(got)-1] != fetch {
		t.Fatalf("remote.origin.fetch = %v, want the clone's own refspec plus exactly the mirror refspec", got)
	}
	// The clone's original fetch refspec survives. This is why the write is `--add`: a command that replaced
	// `remote.origin.fetch` would leave the clone unable to fetch its own branches, from an invocation
	// whose name says nothing about fetching.
	if strings.Join(got[:len(got)-1], "\n") != strings.Join(before, "\n") {
		t.Errorf("the clone's own fetch refspecs changed: %v, want %v untouched", got, before)
	}
	if pushed := configValues(t, f, "remote.origin.push"); len(pushed) != 1 || pushed[0] != push {
		t.Errorf("remote.origin.push = %v, want exactly %q", pushed, push)
	}
}

// The same write reported through `--json`, on its own fixture so the answer is the first run's: a
// pipeline needs to tell "written now" from "was already there", and `already_configured` is the only place
// that distinction survives the move away from a terminal.
func TestConfigureReportsWhatItDid(t *testing.T) {
	f, _, _, _ := recordFixture(t)
	f.SwitchTo("main")
	remoteWith(t, f)

	json := runIn(t, f.Dir(), "integration", "configure", "--json").json(t)
	if json["remote"] != "origin" {
		t.Errorf("remote = %v, want the remote the lines were written to", json["remote"])
	}
	for _, half := range []struct {
		key     string
		refspec string
	}{{"fetch", reviewref.MirrorRefspec("origin")}, {"push", reviewref.PushRefspec}} {
		entry, ok := json[half.key].(map[string]any)
		if !ok {
			t.Fatalf("%s = %v, want the key, value and outcome", half.key, json[half.key])
		}
		if entry["key"] != "remote.origin."+half.key || entry["refspec"] != half.refspec ||
			entry["already_configured"] != false {
			t.Errorf("%s = %v, want key, refspec and already_configured false", half.key, entry)
		}
	}
}

// Twice is the case the command has to survive living in a pipeline: the second run says it wrote nothing,
// and the config file — not the message — proves it.
func TestConfigureTwiceWritesNothing(t *testing.T) {
	f, _, _, _ := recordFixture(t)
	f.SwitchTo("main")
	remoteWith(t, f)

	first := runIn(t, f.Dir(), "integration", "configure", "--json").json(t)
	for _, key := range []string{"fetch", "push"} {
		if entry := first[key].(map[string]any); entry["already_configured"] != false {
			t.Fatalf("first run reports %s.already_configured = %v, want false", key, entry["already_configured"])
		}
	}
	before := configOf(t, f)

	second := runIn(t, f.Dir(), "integration", "configure", "--json").mustSucceed(t, "integration", "configure").json(t)
	for _, key := range []string{"fetch", "push"} {
		if entry := second[key].(map[string]any); entry["already_configured"] != true {
			t.Errorf("second run: %s = %v, want already_configured true", key, entry)
		}
	}
	if got := configOf(t, f); got != before {
		t.Errorf("the config file changed on a run that reported writing nothing:\n--- before ---\n%s\n--- after ---\n%s", before, got)
	}
	// And the human form of the same run says it plainly, because a log full of "configured" lines that
	// configured nothing is how people stop reading logs.
	human := runIn(t, f.Dir(), "integration", "configure").mustSucceed(t, "integration", "configure")
	mustContain(t, human.stdout, "already configured", "the human output says nothing was written")
	mustContain(t, human.stdout, "remote.origin.push already pushes", "and says which half it checked")
}

// One half, because a clone that should be able to *compare* a record against the remote need not be the
// clone that decides the record is public.
func TestConfigureFetchOnlyWritesOneKey(t *testing.T) {
	f, _, _, _ := recordFixture(t)
	f.SwitchTo("main")
	remoteWith(t, f)

	res := runIn(t, f.Dir(), "integration", "configure", "--fetch-only").mustSucceed(t, "integration", "configure")
	mustContain(t, res.stdout, "added "+reviewref.MirrorRefspec("origin"), "the fetch half is written")
	mustNotContain(t, res.stdout, "remote.origin.push", "and the push half is not even mentioned")
	if got := configValues(t, f, "remote.origin.push"); len(got) != 0 {
		t.Errorf("--fetch-only wrote remote.origin.push = %v, want the key untouched", got)
	}
	if got := configValues(t, f, "remote.origin.fetch"); len(got) == 0 {
		t.Error("--fetch-only wrote no fetch refspec either")
	}

	// In JSON the half that was not offered is absent rather than false: "not asked" and "asked and already
	// there" are two answers, and a pipeline has to tell them apart.
	json := runIn(t, f.Dir(), "integration", "configure", "--fetch-only", "--json").json(t)
	if json["push"] != nil {
		t.Errorf("push = %v on a --fetch-only run, want the key absent", json["push"])
	}
	if entry, ok := json["fetch"].(map[string]any); !ok || entry["already_configured"] != true {
		t.Errorf("fetch = %v, want the half that was asked for and already present", json["fetch"])
	}
}

// A repository with more than one remote gets the line on the remote that was named, not on whichever
// sorted first — and a name that is not a remote is a refusal, not a fallback.
func TestConfigureNamesTheRemoteItWrites(t *testing.T) {
	f, _, _, _ := recordFixture(t)
	f.SwitchTo("main")
	remoteWith(t, f)
	up := filepath.Join(t.TempDir(), "upstream.git")
	f.MustGit("init", "--bare", "-b", "main", up)
	f.MustGit("remote", "add", "upstream", up)

	runIn(t, f.Dir(), "integration", "configure", "--remote", "upstream").mustSucceed(t, "integration", "configure")
	if got := configValues(t, f, "remote.upstream.push"); len(got) != 1 || got[0] != reviewref.PushRefspec {
		t.Errorf("remote.upstream.push = %v, want the durable push refspec", got)
	}
	if got := configValues(t, f, "remote.origin.push"); len(got) != 0 {
		t.Errorf("the remote that was not named still got a line: %v", got)
	}

	res := runIn(t, f.Dir(), "integration", "configure", "--remote", "nope")
	if res.code == 0 {
		t.Fatal("--remote nope succeeded")
	}
	mustContain(t, res.stdout+res.stderr, "no remote \"nope\"", "and the refusal says which name it could not find")
}

// Configuration in a repository with nowhere to fetch from or push to is refused rather than half-written.
func TestConfigureWithoutARemoteRefuses(t *testing.T) {
	f, _, _, _ := recordFixture(t)
	f.SwitchTo("main")

	res := runIn(t, f.Dir(), "integration", "configure")
	if res.code == 0 {
		t.Fatal("integration configure succeeded with no remote to configure")
	}
	combined := res.stdout + res.stderr
	mustContain(t, combined, "this repository has none", "and says what was missing")
	mustContain(t, combined, "git remote add", "naming the fix rather than the failure")
	for _, key := range []string{"remote.origin.fetch", "remote.origin.push"} {
		if got := configValues(t, f, key); len(got) != 0 {
			t.Errorf("the refusal still wrote %s: %v", key, got)
		}
	}
}

// What the configuration buys, and what it deliberately does not: after an ordinary `git fetch` the clone
// can tell published from unpublished on its own, and it still has to ask to be given records. A record is
// a claim about a landing; a clone should acquire claims by asking, not because a config line written weeks
// earlier keeps delivering them.
func TestConfiguredCloneComparesWithoutTheFetchFlag(t *testing.T) {
	f, slug, _ := publishedFixture(t)
	// Half the pair on the remote: the comparison has something to report once the clone can make it.
	f.MustGit("push", "--quiet", "origin", reviewref.Archive(slug)+":"+reviewref.Archive(slug))

	f.MustGit("fetch", "--quiet", "origin")
	before := runIn(t, f.Dir(), "status").mustSucceed(t, "status")
	mustContain(t, before.stdout, "never fetched", "before configuring, a plain fetch brings nothing to compare")

	runIn(t, f.Dir(), "integration", "configure", "--fetch-only").mustSucceed(t, "integration", "configure")
	f.MustGit("fetch", "--quiet", "--prune", "origin")
	after := runIn(t, f.Dir(), "status").mustSucceed(t, "status")
	mustNotContain(t, after.stdout, "never fetched", "configured, the same plain fetch leaves the clone able to compare")
	mustContain(t, after.stdout, "RECORDED, NOT PUBLISHED", "and it uses that to answer the question it could not before")
	mustContain(t, after.stdout, slug, "naming the changeset whose pair has not travelled")

	// And the records themselves are still opt-in: the mirror refspec brought mirrors, not facts.
	if got := gitIn(t, f.Dir(), "for-each-ref", "--format=%(refname)", reviewref.NamespaceRoot+"/"); got == "" {
		t.Fatal("the fixture lost its own records, so the next assertion proves nothing")
	}
	clone := filepath.Join(t.TempDir(), "reader")
	f.MustGit("clone", "--quiet", remoteOf(t, f), clone)
	gitIn(t, clone, "config", "--local", "--add", "remote.origin.fetch", reviewref.MirrorRefspec("origin"))
	gitIn(t, clone, "fetch", "--quiet", "--prune", "origin")
	if got := gitIn(t, clone, "for-each-ref", "--format=%(refname)", reviewref.MirrorRoot("origin")+"/"); got == "" {
		t.Error("the configured fetch brought no mirrors, so it configured nothing worth having")
	}
	if got := gitIn(t, clone, "for-each-ref", "--format=%(refname)", reviewref.NamespaceRoot); got != "" {
		t.Errorf("the configured fetch delivered records into %s — records stay opt-in:\n%s", reviewref.NamespaceRoot, got)
	}
}

// What the push half buys, and the one thing it must never buy: an ordinary `git push` carries the pair to
// the remote, and a remote that holds a different value rejects it instead of being overwritten. The second
// assertion is the reason the refspec carries no `+` — a repository can opt into publishing on every push,
// and even then no push can move a record somebody else wrote.
func TestConfiguredPushSendsTheRecordsAndNeverMovesOne(t *testing.T) {
	f, slug, _ := publishedFixture(t)
	f.SwitchTo("main")
	runIn(t, f.Dir(), "integration", "configure").mustSucceed(t, "integration", "configure")

	// A plain `git push`, with no refspec on the command line: the configured one is the whole point.
	f.MustGit("push", "origin")
	remote := remoteOf(t, f)
	for _, ref := range []string{reviewref.Archive(slug), reviewref.Integration(slug)} {
		if got := remoteRef(t, remote, ref); got == "" {
			t.Errorf("%s never reached the remote through an ordinary push", ref)
		}
	}
	if got := remoteRef(t, remote, reviewref.Archive(slug)); got != f.RefSHA(reviewref.Archive(slug)) {
		t.Errorf("the remote holds %s for the archive, want %s", got, f.RefSHA(reviewref.Archive(slug)))
	}

	// Now a disagreement. `--force` is not on the table: git refuses a non-fast-forward update to an
	// existing ref unless the refspec forces it, and this refspec does not.
	before := remoteRef(t, remote, reviewref.Archive(slug))
	moved := unrelatedCommit(t, f)
	f.MustGit("update-ref", reviewref.Archive(slug), moved)
	if out, err := f.Git("push", "origin"); err == nil {
		t.Errorf("the ordinary push accepted a record that disagrees with the remote's:\n%s", out)
	}
	if got := remoteRef(t, remote, reviewref.Archive(slug)); got != before {
		t.Errorf("the refused push still moved the remote's record to %s, want %s", got, before)
	}
}

// Where the command gets named without being run. A configuration a caller has never heard of is a
// configuration nobody has: the surfaces that meet the absence of it — writing a record, publishing one,
// and reading a clone that cannot compare — each say the remedy exists, and none of them writes it.
func TestTheSurfacesNudgeTowardConfiguring(t *testing.T) {
	f, slug, _ := publishedFixture(t)

	// The read path. Before the mirrors exist the finding is that nothing can be compared, and the
	// sentence that says so names both remedies: the flag that asks once, and the command that stops the
	// asking. It is the same string in the human report and in `--json`, because a pipeline reading the
	// note deserves the same remedy a person is shown.
	out := runIn(t, f.Dir(), "status", "--changeset", slug).mustSucceed(t, "status", "--changeset", slug)
	mustContain(t, out.stdout, "never fetched", "the finding is still the finding")
	mustContain(t, out.stdout, "git pair integration configure", "and it names the durable remedy")
	note := runIn(t, f.Dir(), "status", "--changeset", slug, "--json").json(t)["unpublished_note"]
	if s, ok := note.(string); !ok || !strings.Contains(s, "git pair integration configure") {
		t.Errorf("unpublished_note = %v, want the same remedy --json readers are shown", note)
	}
	q := runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
	mustContain(t, q.stdout, "git pair integration configure", "and the queue says it too, since it cannot compare either")

	// The publish path: a clone that sent a pair by hand is a clone an ordinary push could have served.
	// The nudge names the push key only — the fetch half is not what this run just did by hand.
	pub := runIn(t, f.Dir(), "integration", "publish").mustSucceed(t, "integration", "publish")
	mustContain(t, pub.stdout, slug+": published to origin", "the publish happened")
	mustContain(t, pub.stdout, "git pair integration configure", "and the run names the line that would end the handwork")
	mustContain(t, pub.stdout, "remote.origin.push", "naming the key it would write")
	mustNotContain(t, pub.stdout, "remote.origin.fetch", "and not offering the half this command does not need")

	// Same command, already configured: the line is gone rather than reworded. A reminder of a thing the
	// clone already did is how a hint becomes noise, and this is the run a pipeline repeats.
	f.MustGit("config", "--local", "--add", "remote.origin.push", reviewref.PushRefspec)
	again := runIn(t, f.Dir(), "integration", "publish").mustSucceed(t, "integration", "publish")
	mustContain(t, again.stdout, slug+": already published to origin", "the publish still answers")
	mustNotContain(t, again.stdout, "git pair integration configure", "and says nothing about configuring")

	// The machine's answer carries findings, not prose: a pipeline that asked for JSON gets the same
	// verdict with no nudge to parse.
	js := runIn(t, f.Dir(), "integration", "publish", "--json").json(t)
	for _, key := range []string{"published", "already_published", "failed"} {
		if _, ok := js[key].([]any); !ok {
			t.Errorf("%s = %v, want the array this command always answers with", key, js[key])
		}
	}
}

// A run that publishes nothing has no refs to advertise about, so it prints no nudge: the line is about the
// pair that just travelled, and here there is none.
func TestPublishWithoutAnythingToSendDoesNotNudge(t *testing.T) {
	f, _, _ := publishedFixture(t)

	// A fresh clone of the remote holds no records at all — a clone maps `refs/heads/*` and nothing else —
	// so this is the invocation that finds nothing to send.
	clone := filepath.Join(t.TempDir(), "empty")
	f.MustGit("clone", "--quiet", remoteOf(t, f), clone)
	out := runIn(t, clone, "integration", "publish").mustSucceed(t, "integration", "publish")
	mustContain(t, out.stdout, "nothing to publish", "the clone holds no record, and says so")
	mustNotContain(t, out.stdout, "git pair integration configure", "and does not advertise publishing to it")
}
