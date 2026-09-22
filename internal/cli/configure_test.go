package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
)

// `--configure-fetch` is the only place git-pair writes configuration, and the flag *is* the consent: PRD
// §22 makes this CLI the agent surface, so the alternative — asking at the terminal — would make one
// command line mean two things depending on where it ran, and an unanswered prompt in CI reads exactly like
// a declined one. These tests are the consequences of that choice: nothing blocks, nothing asks, the
// default writes no config, and the flag writes exactly one additive line, once.

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

func fetchValues(t *testing.T, f *gittest.Fixture) []string {
	t.Helper()
	out := strings.TrimSpace(f.MustGit("config", "--local", "--get-all", "remote.origin.fetch"))
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// The default: refs written, config untouched, and one line naming the flag that would have configured it.
func TestRecordWithoutConfigureFetchWritesNoConfig(t *testing.T) {
	f, _, source, landing := recordFixture(t)
	f.SwitchTo("main")
	remote := filepath.Join(t.TempDir(), "remote.git")
	f.MustGit("init", "--bare", "-b", "main", remote)
	f.MustGit("remote", "add", "origin", remote)
	before := fetchValues(t, f)

	res := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing,
		"--target", "release/2.x").mustSucceed(t, "integration", "record")
	mustContain(t, res.stdout, "--configure-fetch", "the run names the option it did not take")
	mustContain(t, res.stdout, "remote.origin.fetch", "and the key it would have written")
	if got := fetchValues(t, f); len(got) != len(before) {
		t.Errorf("`--configure-fetch` was not passed, yet remote.origin.fetch went from %v to %v", before, got)
	}
	if durableRefs(t, f) == nil {
		t.Error("the record was not written: configuration is the optional half, not the gate")
	}

	// In JSON the absence is the answer: a pipeline that did not ask must be able to tell "not asked" from
	// "asked and nothing needed writing", which is what `fetch_config` being absent versus present means.
	json := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing,
		"--target", "release/2.x", "--json").json(t)
	if json["fetch_config"] != nil {
		t.Errorf("fetch_config = %v on a run that did not ask, want absent", json["fetch_config"])
	}
}

// With the flag: one line, the mirror refspec only, and the answer names the key and the value.
func TestConfigureFetchWritesTheMirrorRefspec(t *testing.T) {
	f, _, source, landing := recordFixture(t)
	f.SwitchTo("main")
	remote := filepath.Join(t.TempDir(), "remote.git")
	f.MustGit("init", "--bare", "-b", "main", remote)
	f.MustGit("remote", "add", "origin", remote)
	before := fetchValues(t, f)

	res := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing,
		"--target", "release/2.x", "--configure-fetch").mustSucceed(t, "integration", "record")
	spec := reviewref.MirrorRefspec("origin")
	mustContain(t, res.stdout, spec, "the answer prints the value it wrote")
	mustContain(t, res.stdout, "remote.origin.fetch", "and the key it wrote it to")
	mustContain(t, res.stdout, "added "+spec, "and says it wrote rather than confirmed")

	got := fetchValues(t, f)
	if len(got) != len(before)+1 {
		t.Fatalf("remote.origin.fetch = %v, want the clone's own refspec plus exactly the mirror refspec", got)
	}
	if got[len(got)-1] != spec {
		t.Errorf("appended %q, want %q", got[len(got)-1], spec)
	}
	// The clone's original fetch refspec survives. This is why the write is `--add`: a record command that
	// replaced `remote.origin.fetch` would leave the clone unable to fetch branches, from a command whose
	// name says nothing about fetching.
	if strings.Join(got[:len(got)-1], "\n") != strings.Join(before, "\n") {
		t.Errorf("the clone's own fetch refspecs changed: %v, want %v untouched", got, before)
	}
}

// The same write reported through `--json`, on its own fixture so the answer is the first run's: a
// pipeline needs to tell "written now" from "was already there", and `already_configured` is the only place
// that distinction survives the move away from a terminal.
func TestConfigureFetchReportsWhatItDid(t *testing.T) {
	f, _, source, landing := recordFixture(t)
	f.SwitchTo("main")
	f.MustGit("init", "--bare", "-b", "main", filepath.Join(t.TempDir(), "remote.git"))
	f.MustGit("remote", "add", "origin", filepath.Join(t.TempDir(), "remote.git"))

	fc := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing,
		"--target", "release/2.x", "--configure-fetch", "--json").json(t)["fetch_config"]
	entry, ok := fc.(map[string]any)
	if !ok {
		t.Fatalf("fetch_config = %v, want the key, value and outcome", fc)
	}
	if entry["key"] != "remote.origin.fetch" || entry["refspec"] != reviewref.MirrorRefspec("origin") ||
		entry["already_configured"] != false {
		t.Errorf("fetch_config = %v, want key, refspec and already_configured false", entry)
	}
}

// Twice is the case the flag has to survive living in a pipeline: the second run says it wrote nothing, and
// the config file — not the message — proves it.
func TestConfigureFetchTwiceWritesNothing(t *testing.T) {
	f, _, source, landing := recordFixture(t)
	f.SwitchTo("main")
	remote := filepath.Join(t.TempDir(), "remote.git")
	f.MustGit("init", "--bare", "-b", "main", remote)
	f.MustGit("remote", "add", "origin", remote)

	record := []string{"integration", "record", "--source", source, "--commit", landing,
		"--target", "release/2.x", "--configure-fetch", "--json"}
	first := runIn(t, f.Dir(), record...).mustSucceed(t, record...).json(t)
	if fc := first["fetch_config"].(map[string]any); fc["already_configured"] != false {
		t.Fatalf("first run reports already_configured = %v, want false", fc["already_configured"])
	}
	before := configOf(t, f)

	// The retry arrives with the same SHAs, which is also the already-recorded path: a pipeline that
	// re-runs its record step must still be able to configure a clone it did not configure the first time.
	second := runIn(t, f.Dir(), record...).mustSucceed(t, record...).json(t)
	if fc := second["fetch_config"].(map[string]any); fc["already_configured"] != true {
		t.Errorf("second run: fetch_config = %v, want already_configured true", fc)
	}
	if got := configOf(t, f); got != before {
		t.Errorf("the config file changed on a run that reported writing nothing:\n--- before ---\n%s\n--- after ---\n%s", before, got)
	}
	// And the human form of the same run says it plainly, because a log full of "configured" lines that
	// configured nothing is how people stop reading logs.
	human := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing,
		"--target", "release/2.x", "--configure-fetch").mustSucceed(t, "integration", "record")
	mustContain(t, human.stdout, "already fetches the durable mirrors", "the human output says nothing was written")
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

	f.MustGit("config", "--local", "--add", "remote.origin.fetch", reviewref.MirrorRefspec("origin"))
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

// A flag that asks for configuration in a repository with nowhere to fetch from is refused, and the refusal
// leaves no half-written record behind: configuration is checked before the refs, because it is the half
// that can be undone.
func TestConfigureFetchWithoutARemoteRefuses(t *testing.T) {
	f, _, source, landing := recordFixture(t)
	f.SwitchTo("main")

	res := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing,
		"--target", "release/2.x", "--configure-fetch")
	if res.code == 0 {
		t.Fatal("--configure-fetch succeeded with no remote to configure")
	}
	combined := res.stdout + res.stderr
	mustContain(t, combined, "no remote", "and says what was missing")
	mustContain(t, combined, "git remote add", "naming the fix rather than the failure")
	if got := durableRefs(t, f); len(got) != 0 {
		t.Errorf("the refusal still wrote durable refs: %v", got)
	}
}
