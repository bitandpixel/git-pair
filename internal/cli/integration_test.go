package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
)

// recordFixture builds the shape `integration record` exists for, and returns the fixture, the
// changeset id, the archived head (A) and the landing commit (B).
//
// The landing is on `release/2.x` rather than on `main`, which is deliberate twice over. First,
// it is the case only this command can answer for: the rule that finds a changeset asks whether its
// directory is absent from trunk, so a landing on the default branch retires the changeset by
// itself, while a landing elsewhere leaves the branch looking like live work until someone records
// where it went. Second, B is built by copying the committed changeset directory onto a commit with
// no ancestry to A, which is what a squash or a cherry-pick leaves behind — and a record built on
// that shape proves the command never reaches for ancestry to do its job.
func recordFixture(t *testing.T) (*gittest.Fixture, string, string, string) {
	t.Helper()
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "approve")
	source := f.Head()
	f.CreateBranch("release/2.x", "main")
	f.MustGit("checkout", source, "--", changeset.Root+"/"+slug)
	f.Commit("booking: land the reviewed work", gittest.WithFile("landed.md", "landed\n"))
	return f, slug, source, f.Head()
}

// The happy path, and the two properties the command's whole design rests on: no ancestry has to
// exist between what was archived and what landed, and the command needs no checkout of the
// changeset — it is addressed by SHA and ref, because the person running it is a pipeline.
func TestIntegrationRecordLinksAnArchivedHeadToALanding(t *testing.T) {
	f, slug, source, landing := recordFixture(t)
	f.SwitchTo("main") // not the changeset's branch, and not the landing's either

	res := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing, "--target", "release/2.x")
	res.mustSucceed(t, "integration", "record")
	mustContain(t, res.stdout, "recorded "+shortOf(landing), "the answer must say what it recorded")
	mustContain(t, res.stdout, reviewref.Integration(slug), "the answer must name the ref it wrote")
	mustContain(t, res.stdout, "reachable from release/2.x", "and the check it made")

	if got := f.RefSHA(reviewref.Integration(slug)); got != landing {
		t.Errorf("the record is at %s, want %s", got, landing)
	}
	// The archive is the other half of the mapping and is not this command's to touch.
	if got := f.RefSHA(reviewref.Archive(slug)); got != source {
		t.Errorf("the archive moved to %s, want the archived head %s", got, source)
	}
}

func TestIntegrationRecordJSON(t *testing.T) {
	f, slug, source, landing := recordFixture(t)

	res := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing,
		"--target", "release/2.x", "--json")
	res.mustSucceed(t, "integration", "record")
	got := res.json(t)
	// Full SHAs, not the short forms the text surface prints: what a pipeline does with these is
	// compare them against the SHAs it already holds.
	for key, want := range map[string]string{
		"changeset":       slug,
		"source":          source,
		"commit":          landing,
		"target":          "release/2.x",
		"integration_ref": reviewref.Integration(slug),
	} {
		if got[key] != want {
			t.Errorf("%s = %v, want %s", key, got[key], want)
		}
	}
	if got["recorded"] != true {
		t.Errorf("recorded = %v, want true", got["recorded"])
	}
}

// An abbreviated SHA from a CI log has to resolve before discovery, or `--points-at` matching finds
// nothing and the pipeline is told no changeset exists.
func TestIntegrationRecordAcceptsAbbreviatedSHAs(t *testing.T) {
	f, _, source, landing := recordFixture(t)

	res := runIn(t, f.Dir(), "integration", "record",
		"--source", f.Short(source), "--commit", f.Short(landing))
	res.mustSucceed(t, "integration", "record")
	if got := f.RefSHA(reviewref.Integration("booking")); got != landing {
		t.Errorf("the record is at %s, want %s", got, landing)
	}
}

// §22: one record per changeset. The refusal names the record that exists rather than the one being
// attempted, and it survives being asked twice with different answers — which is the backport case,
// the one most likely to arrive as a second CI run against a release branch.
func TestIntegrationRecordIsCreatedOnce(t *testing.T) {
	f, _, source, landing := recordFixture(t)
	first := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing)
	first.mustSucceed(t, "integration", "record")

	// A re-run of the same command must not be a success that did nothing: a pipeline that
	// records twice has a problem worth failing on.
	again := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing)
	if again.code != 1 {
		t.Fatalf("re-recording exited %d, want 1\n%s%s", again.code, again.stdout, again.stderr)
	}
	mustContain(t, again.stderr, "already recorded", "the refusal must say a record exists")
	mustContain(t, again.stderr, shortOf(source), "and name the source it holds")
	mustContain(t, again.stderr, shortOf(landing), "and the commit it holds")

	f.SwitchTo("release/2.x")
	f.Commit("backport the same work", gittest.WithFile("backport.md", "again\n"))
	backport := f.Head()
	second := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", backport,
		"--target", "release/2.x")
	if second.code != 1 {
		t.Fatalf("a second landing exited %d, want 1\n%s%s", second.code, second.stdout, second.stderr)
	}
	mustContain(t, second.stderr, "one integration record", "the refusal must explain why a backport is not a second record")
	mustContain(t, second.stderr, "release/2.x", "and may name what the caller asked about")
	if got := f.RefSHA(reviewref.Integration("booking")); got != landing {
		t.Errorf("the record moved to %s; §22 says it stays at %s", got, landing)
	}
}

// Every refusal the command owes, with the exit code and the phrase that makes it actionable. The
// table is the contract: §18's nothing-matched is a repository fact (exit 1), §19's ambiguity is an
// argument problem (exit 2, the same rule every other command applies to ambiguity), and the rest
// are the verifications §21 lists.
func TestIntegrationRecordRefusesWhatItMustRefuse(t *testing.T) {
	tests := []struct {
		name string
		// setup mutates the fixture and returns the args to run with.
		setup func(t *testing.T, f *gittest.Fixture, slug, source, landing string) []string
		code  int
		want  []string
	}{
		{
			name: "a missing flag",
			setup: func(_ *testing.T, _ *gittest.Fixture, _, _, landing string) []string {
				return []string{"--commit", landing}
			},
			code: 2,
			want: []string{"both required"},
		},
		{
			name: "a source this repository does not have",
			setup: func(_ *testing.T, _ *gittest.Fixture, _, _, landing string) []string {
				return []string{"--source", strings.Repeat("a", 40), "--commit", landing}
			},
			code: 1,
			want: []string{"--source", "is not a commit this repository has"},
		},
		{
			name: "no archive points at the source (§18)",
			// The implementation commit is real work; it was simply never archived.
			setup: func(t *testing.T, f *gittest.Fixture, _, _, landing string) []string {
				t.Helper()
				f.SwitchTo("booking")
				f.Commit("work nobody offered for review", gittest.WithFile("more.go", "package main\n"))
				return []string{"--source", f.Head(), "--commit", landing}
			},
			code: 1,
			want: []string{"no changeset archive points at", "never fetched", "never archived", "wrong commit"},
		},
		{
			name: "the named changeset still needs an archive there",
			setup: func(t *testing.T, f *gittest.Fixture, slug, _, landing string) []string {
				t.Helper()
				return []string{"--source", f.RevParse("main"), "--commit", landing, "--changeset", slug}
			},
			code: 1,
			want: []string{"must still be at that commit"},
		},
		{
			name: "two archives at one commit (§19)",
			setup: func(t *testing.T, f *gittest.Fixture, _, source, landing string) []string {
				t.Helper()
				// git-pair cannot create this state — one archive per id, forward-only — so it is
				// built by hand, which is also why the command must refuse rather than pick.
				f.MustGit("update-ref", reviewref.Archive("booking-v2"), source)
				return []string{"--source", source, "--commit", landing}
			},
			code: 2,
			want: []string{"more than one changeset archive points at", "booking", "booking-v2", "--changeset"},
		},
		{
			name: "a named changeset that is not one of the matches",
			setup: func(t *testing.T, f *gittest.Fixture, _, source, landing string) []string {
				t.Helper()
				f.MustGit("update-ref", reviewref.Archive("booking-v2"), source)
				return []string{"--source", source, "--commit", landing, "--changeset", "someone-elses-work"}
			},
			code: 1,
			want: []string{"does not replace the archive", "booking"},
		},
		{
			name: "an abandoned changeset has nothing to integrate",
			setup: func(t *testing.T, f *gittest.Fixture, _, _, landing string) []string {
				t.Helper()
				f.SwitchTo("booking")
				runIn(t, f.Dir(), "change", "abandon").mustSucceed(t, "change", "abandon")
				return []string{"--source", f.Head(), "--commit", landing}
			},
			code: 1,
			want: []string{"was abandoned by"},
		},
		{
			name: "a landing that does not carry the changeset",
			setup: func(t *testing.T, f *gittest.Fixture, _, source, _ string) []string {
				t.Helper()
				// `main` never received the changeset directory — the landing went to release/2.x —
				// so a commit there is a landing commit that cannot be this work's.
				f.SwitchTo("main")
				f.Commit("unrelated work on the default branch", gittest.WithFile("notes.md", "nothing to do with booking\n"))
				return []string{"--source", source, "--commit", f.Head()}
			},
			code: 1,
			want: []string{"does not exist in", "does not carry this changeset"},
		},
		{
			name: "a landing outside the target it was verified against",
			setup: func(t *testing.T, f *gittest.Fixture, _, source, landing string) []string {
				t.Helper()
				return []string{"--source", source, "--commit", landing, "--target", "booking"}
			},
			code: 1,
			want: []string{"is not reachable from booking"},
		},
		{
			name: "a target that does not resolve",
			setup: func(_ *testing.T, _ *gittest.Fixture, _, source, landing string) []string {
				return []string{"--source", source, "--commit", landing, "--target", "origin/nope"}
			},
			code: 1,
			want: []string{"--target origin/nope", "is not a commit this repository has"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, slug, source, landing := recordFixture(t)
			args := tc.setup(t, f, slug, source, landing)
			res := runIn(t, f.Dir(), append([]string{"integration", "record"}, args...)...)
			if res.code != tc.code {
				t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", res.code, tc.code, res.stdout, res.stderr)
			}
			for _, want := range tc.want {
				mustContain(t, res.stderr, want, "the refusal must be actionable")
			}
		})
	}
}

// §23: the record freezes the archive. The commands that move it are the ones that have to refuse,
// and they have to refuse before writing, or the branch keeps a marker with nothing pointing at it.
func TestArchiveIsFrozenOnceIntegrationIsRecorded(t *testing.T) {
	f, slug, source, landing := recordFixture(t)
	runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing).mustSucceed(t, "integration", "record")
	f.SwitchTo("booking")

	// A review artifact on the branch: exactly what `change archive` exists to move the ref over.
	f.Commit("note an edge case after the landing", gittest.WithFile("changesets/booking/ABOUT.md", "# booking\n\n## Summary\n\nnoted\n"))
	before := f.RefSHA(reviewref.Archive(slug))
	branchHead := f.Head()

	for _, args := range [][]string{
		{"change", "archive"},
		{"change", "ready"},
		{"change", "unready"},
		{"review", "submit", "--approve"},
		{"change", "abandon"},
	} {
		res := runIn(t, f.Dir(), args...)
		if res.code != 1 {
			t.Errorf("git-pair %v exited %d, want 1\n%s%s", args, res.code, res.stdout, res.stderr)
			continue
		}
		mustContain(t, res.stderr, "integrated at", "the refusal must name the record that freezes the archive")
		mustContain(t, res.stderr, slug, "and the changeset it belongs to")
	}
	if got := f.RefSHA(reviewref.Archive(slug)); got != before {
		t.Errorf("the archive moved to %s; the frozen record says %s", got, before)
	}
	// No command may leave a marker behind either. A marker commit with no ref pointing at it is
	// the half-write the write gate exists to prevent, and it is invisible to every later
	// derivation that reads state from the ref's history.
	if got := f.Head(); got != branchHead {
		t.Errorf("a refused command committed: HEAD is %s (%s), was %s (%s)",
			shortOf(got), f.Subject(got), shortOf(branchHead), f.Subject(branchHead))
	}
}

// status reports the record beside the state, and says which branch it measured the landing against
// — the distinction between landing on trunk and retiring into a release branch.
func TestStatusReportsIntegration(t *testing.T) {
	f, _, source, landing := recordFixture(t)
	f.SwitchTo("booking")

	before := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)
	if before["integrated"] != false {
		t.Fatalf("integrated = %v before the record; the branch alone must not claim a landing", before["integrated"])
	}

	runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing).mustSucceed(t, "integration", "record")
	after := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)
	if after["integrated"] != true {
		t.Fatalf("integrated = %v after the record", after["integrated"])
	}
	if after["integrated_commit"] != shortOf(landing) {
		t.Errorf("integrated_commit = %v, want %s", after["integrated_commit"], shortOf(landing))
	}
	// The state is untouched: landing is not a marker, and a state value for it would put a
	// derived fact inside the machine that markers move.
	if after["state"] != before["state"] {
		t.Errorf("state moved from %v to %v; integration is reported beside state, not inside it", before["state"], after["state"])
	}
	// B is on release/2.x, so it is not in the branch git-pair calls the default one. This is the
	// distinction the fields exist for: work that retired into a release branch and never reached
	// the default branch must not read like a default-branch landing.
	if after["integrated_in_default_branch"] != false {
		t.Errorf("integrated_in_default_branch = %v; the landing is on release/2.x", after["integrated_in_default_branch"])
	}
	if after["integrated_default_branch"] != "main" {
		t.Errorf("integrated_default_branch = %v, want main", after["integrated_default_branch"])
	}
	mustContain(t, str(t, after, "next_action"), "integrated at "+shortOf(landing), "the next action must stop promising work")

	text := runIn(t, f.Dir(), "status").mustSucceed(t, "status").stdout
	mustContain(t, text, "Integrated:", "the text surface says it too")
	mustContain(t, text, "not reachable from main", "and names where it did land")
}

// The other half of the containment fact, and the shape a landed changeset really has: the branch
// is gone, the work is in the default branch, and the only thing left to read is the anchor beside
// the record. Asking with `--default-branch release/2.x` instead would not work — with the landing
// inside what git-pair calls trunk, the branch's changeset directory is no longer a claim on
// anything, and there is nothing left for `status` to resolve.
func TestStatusReportsALandingOnTrunk(t *testing.T) {
	f, slug, source, _ := recordFixture(t)
	f.SwitchTo("main")
	f.MustGit("checkout", source, "--", changeset.Root+"/"+slug)
	f.Commit("booking: squash-merge the reviewed work")
	landing := f.Head()
	f.ForceDeleteBranch("booking")

	runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing).mustSucceed(t, "integration", "record")
	got := runIn(t, f.Dir(), "status", "--changeset", slug, "--json").mustSucceed(t, "status").json(t)
	if got["integrated"] != true {
		t.Fatalf("integrated = %v, want true", got["integrated"])
	}
	if got["integrated_in_default_branch"] != true {
		t.Errorf("integrated_in_default_branch = %v, want true: the landing is in main", got["integrated_in_default_branch"])
	}
	if got["integrated_default_branch"] != "main" {
		t.Errorf("integrated_default_branch = %v, want main", got["integrated_default_branch"])
	}
	if got["branch"] != "" {
		t.Errorf("branch = %v, want the empty string: the branch is gone and the record is what remains", got["branch"])
	}
}

// The gate's answer for a changeset that has landed. A pipeline that runs `check` before integrating
// re-runs it after, and needs the exit code and the reason to say what already happened.
func TestCheckFailsAnIntegratedChangeset(t *testing.T) {
	f, _, source, landing := recordFixture(t)
	f.SwitchTo("booking")
	runIn(t, f.Dir(), "check").mustSucceed(t, "check")

	runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing).mustSucceed(t, "integration", "record")
	res := runIn(t, f.Dir(), "check")
	if res.code != 1 {
		t.Fatalf("check on an integrated changeset exited %d, want 1\n%s", res.code, res.stdout+res.stderr)
	}
	mustContain(t, res.stdout, "NOT READY", "the verdict is the output")
	mustContain(t, res.stdout, "already integrated at "+shortOf(landing), "and it names the record")
	mustContain(t, res.stdout, "not reachable from main", "including where the landing sits")

	out := runIn(t, f.Dir(), "check", "--json").json(t)
	if out["integrated"] != true {
		t.Errorf("integrated = %v, want true; a pipeline must not parse prose to learn the work landed", out["integrated"])
	}
	if out["integrated_commit"] != shortOf(landing) {
		t.Errorf("integrated_commit = %v, want %s", out["integrated_commit"], shortOf(landing))
	}
}

// The queue is where the release-branch case was unknowable before this command: the directory is
// still absent from trunk, so the tree rule keeps reporting live work. The record is what lets the
// queue say "asked and answered", and it says so rather than dropping the row in silence — this
// branch is still here, and its disappearance would otherwise read as a bug.
func TestQueueSkipsAnIntegratedChangeset(t *testing.T) {
	f, slug, source, landing := recordFixture(t)
	// The queue lists what is offered, and the fixture's last act was an approval. Re-offering
	// moves the archive, so the source is re-read rather than assumed.
	f.SwitchTo("booking")
	runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")
	source = f.Head()
	f.SwitchTo("main")

	before := runIn(t, f.Dir(), "review", "queue").mustSucceed(t, "review", "queue")
	mustContain(t, before.stdout, slug, "the offered changeset is in the queue before it lands")

	runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing).mustSucceed(t, "integration", "record")
	after := runIn(t, f.Dir(), "review", "queue").mustSucceed(t, "review", "queue")
	if strings.Contains(after.stdout, slug) {
		t.Errorf("the queue still lists %s after the record:\n%s", slug, after.stdout)
	}
	mustContain(t, after.stderr, slug+" (integrated at "+shortOf(landing)+")", "and says why it stopped")
}

// `integration` is a command group, so an unknown subcommand is a usage error rather than a help
// screen that exits 0 — the same rule `change` and `review` follow.
func TestIntegrationGroupRefusesUnknownSubcommand(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	res := runIn(t, f.Dir(), "integration", "recordd")
	if res.code != 2 {
		t.Errorf("git-pair integration recordd exited %d, want 2\n%s%s", res.code, res.stdout, res.stderr)
	}
	mustContain(t, res.stderr, "unknown integration command", "and names the group it did not understand")
}

// str reads a string field out of a decoded JSON object. The CLI tests assert against decoded
// maps rather than structs, so the shape a consumer sees is the shape under test.
func str(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	s, ok := m[key].(string)
	if !ok {
		t.Fatalf("%s = %#v, want a string", key, m[key])
	}
	return s
}
