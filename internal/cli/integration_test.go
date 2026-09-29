package cli_test

import (
	"context"
	"strings"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
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

// The happy path, and the properties the command's whole design rests on: it writes both durable
// refs and nothing else writes them; no ancestry has to exist between what was reviewed and what
// landed; and the command needs no checkout of the changeset — it is addressed by SHA and ref,
// because the person running it is a pipeline.
func TestIntegrationRecordLinksAnArchivedHeadToALanding(t *testing.T) {
	f, slug, source, landing := recordFixture(t)
	f.SwitchTo("main") // not the changeset's branch, and not the landing's either

	// Nothing durable exists for this changeset before the record: no command writes a ref while
	// work is in flight, which is the property the whole milestone is about.
	if got := durableRefs(t, f); len(got) != 0 {
		t.Fatalf("durable refs before the record: %v", got)
	}

	res := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing, "--target", "release/2.x")
	res.mustSucceed(t, "integration", "record")
	mustContain(t, res.stdout, "recorded "+shortOf(landing), "the answer must say what it recorded")
	mustContain(t, res.stdout, reviewref.Integration(slug), "the answer must name the ref it wrote")
	mustContain(t, res.stdout, reviewref.Archive(slug), "and the other one")
	mustContain(t, res.stdout, "reachable from release/2.x", "and the check it made")

	if got := f.RefSHA(reviewref.Integration(slug)); got != landing {
		t.Errorf("the integration record is at %s, want %s", got, landing)
	}
	// The archive half is the unsquashed head, which is the half no other ref in the repository
	// reaches: it is what survives the branch deletion, and it is what a squash destroys.
	if got := f.RefSHA(reviewref.Archive(slug)); got != source {
		t.Errorf("the archive is at %s, want the reviewed head %s", got, source)
	}
	if got := durableRefs(t, f); len(got) != 2 {
		t.Errorf("durable refs after the record = %v, want exactly the pair", got)
	}
	// The chain the archive names is reachable from the archive alone, which is the property the
	// landing is allowed to destroy on the branch.
	f.ForceDeleteBranch("booking")
	for _, want := range []string{source, f.RevParse(source + "~1")} {
		if !f.ReachableFrom(want, reviewref.Archive(slug)) {
			t.Errorf("%s is not reachable from the archive after the branch was deleted", shortOf(want))
		}
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
		"archive_ref":     reviewref.Archive(slug),
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
		"--source", f.Short(source), "--commit", f.Short(landing), "--target", "release/2.x")
	res.mustSucceed(t, "integration", "record")
	if got := f.RefSHA(reviewref.Integration("booking")); got != landing {
		t.Errorf("the record is at %s, want %s", got, landing)
	}
}

// §22: one record per changeset, and a retry is not a failure. The pair of behaviours is the point:
// the same command run twice is a success that changed nothing, and a *different* pair for a
// changeset that already has one is a refusal that names what is already recorded — including in the
// backport case, the one most likely to arrive as a second CI run against a release branch.
func TestIntegrationRecordIsCreatedOnce(t *testing.T) {
	f, slug, source, landing := recordFixture(t)
	first := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing, "--target", "release/2.x")
	first.mustSucceed(t, "integration", "record")

	again := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing, "--target", "release/2.x")
	again.mustSucceed(t, "integration", "record")
	mustContain(t, again.stdout, "already recorded", "the retry says what it found")
	mustContain(t, again.stdout, shortOf(source), "naming the source it holds")
	mustContain(t, again.stdout, shortOf(landing), "and the commit it holds")
	out := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing, "--target", "release/2.x", "--json").json(t)
	if out["recorded"] != false || out["already_recorded"] != true {
		t.Errorf("recorded = %v, already_recorded = %v; a retry that wrote nothing must say so",
			out["recorded"], out["already_recorded"])
	}
	if got := f.RefSHA(reviewref.Archive(slug)); got != source {
		t.Errorf("the retry moved the archive to %s", got)
	}

	// The same *source* recorded against a different commit is a different claim about the same
	// changeset, and there is no answer that is both safe and automatic.
	f.SwitchTo("release/2.x")
	f.Commit("backport the same work", gittest.WithFile("backport.md", "again\n"))
	backport := f.Head()
	second := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", backport,
		"--target", "release/2.x")
	if second.code != 1 {
		t.Fatalf("a second landing exited %d, want 1\n%s%s", second.code, second.stdout, second.stderr)
	}
	mustContain(t, second.stderr, reviewref.Integration(slug)+" records", "the refusal names the record that exists")
	mustContain(t, second.stderr, shortOf(landing), "with its commit, so the reader can see which claim is on the record")
	mustContain(t, second.stderr, reviewref.Integration(slug), "and the ref to go and look at")
	mustContain(t, second.stderr, "never moves", "and says plainly that there is no flag for this")
	if got := f.RefSHA(reviewref.Integration(slug)); got != landing {
		t.Errorf("the record moved to %s; §22 says it stays at %s", got, landing)
	}

	// A half record — the crash case — is completed by the same command, not refused.
	f2, slug2, source2, landing2 := recordFixture(t)
	if _, err := reviewref.CreateOnly(context.Background(), &git.Repo{Dir: f2.Dir()}, reviewref.Archive(slug2), source2); err != nil {
		t.Fatalf("seed the half pair: %v", err)
	}
	half := runIn(t, f2.Dir(), "integration", "record", "--source", source2, "--commit", landing2, "--target", "release/2.x")
	half.mustSucceed(t, "integration", "record")
	mustContain(t, half.stdout, "completed the record", "a half-written pair is finished, not refused")
	if got := f2.RefSHA(reviewref.Integration(slug2)); got != landing2 {
		t.Errorf("the completion left the record at %s, want %s", got, landing2)
	}
}

// Every refusal the command owes, with the exit code and the phrase that makes it actionable. The
// table is the contract: an absent changeset directory is a repository fact (exit 1), an ambiguous
// one is an argument problem (exit 2, the same rule every other command applies to ambiguity), and the
// rest are the verifications §21 lists.
//
// Note what is not here: no case about an archive ref. The command used to require one to exist at the
// source commit, which was the same fact — "this commit was the reviewed head of a changeset" — read
// out of a ref that only existed because someone ran a command. It is read from the commit's own tree
// now.
func TestIntegrationRecordRefusesWhatItMustRefuse(t *testing.T) {
	tests := []struct {
		name string
		// setup mutates the fixture and returns the args to run with.
		setup func(t *testing.T, f *gittest.Fixture, slug, source, landing string) []string
		code  int
		want  []string
	}{
		{
			// Nothing named and nothing derivable. The command asks the repository before it asks the
			// caller — the landing is a first-parent transition and the reviewed head is a branch — so
			// this is the refusal that fires when the repository cannot answer, and it says which
			// branches it looked at and what to name instead.
			name: "nothing named, and nothing on the destination to derive from",
			setup: func(_ *testing.T, _ *gittest.Fixture, _, _, _ string) []string {
				return nil
			},
			code: 2,
			want: []string{"no changeset directory on main, so there is nothing here to record", "--source and --commit"},
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
			name: "a source carrying no changeset directory",
			// Real work, never claimed by a changeset: there is nothing to record, and saying so is
			// better than inventing an id from the branch the commit happens to sit on.
			setup: func(t *testing.T, f *gittest.Fixture, _, _, landing string) []string {
				t.Helper()
				f.SwitchTo("main")
				unrelated := f.Commit("work with no changeset directory", gittest.WithFile("more.go", "package main\n"))
				return []string{"--source", unrelated, "--commit", landing}
			},
			code: 1,
			want: []string{"no changesets/<id>/ directory exists in"},
		},
		{
			name: "a source carrying two changeset directories is ambiguous",
			// The stacked case: a child carries its parent's directory, so two ids are true of the
			// commit at once. Ambiguity is a usage error — the repository is not broken, the caller
			// has to say which one they mean.
			setup: func(t *testing.T, f *gittest.Fixture, _, _, landing string) []string {
				t.Helper()
				f.SwitchTo("booking")
				f.Commit("claim a second changeset too", gittest.WithFile(changeset.Root+"/other/CHANGESET.yaml", "id: other\nbase: main\n"))
				return []string{"--source", f.Head(), "--commit", landing}
			},
			code: 2,
			want: []string{"more than one changeset directory exists", "booking", "other", "--changeset"},
		},
		{
			// `--changeset` picks between the changesets a source actually carries. It is not a way
			// to name a changeset the commit knows nothing about, and the refusal says which of the
			// two the caller has done.
			name: "the named changeset is not one the source carries",
			setup: func(t *testing.T, f *gittest.Fixture, _, source, landing string) []string {
				t.Helper()
				return []string{"--source", source, "--commit", landing, "--changeset", "someone-elses-work"}
			},
			code: 1,
			want: []string{"not among the changesets", "booking", "--changeset picks between them"},
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

// Once the destination carries the changeset's directory, no in-flight command has anything left to
// write for it. The commands refuse because the destination says so — not because a record was written,
// which is a fact about a command somebody ran — and none of them may leave a marker behind: a marker on
// landed work is a claim about a review that cannot happen.
func TestLandedChangesetRefusesFurtherWork(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "approve")
	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land booking", "booking")
	f.SwitchTo("booking")

	// Work on the branch after the landing: exactly what the moving ref used to chase.
	f.Commit("note an edge case after the landing",
		gittest.WithFile("changesets/"+slug+"/ABOUT.md", "# booking\n\n## Summary\n\nnoted\n"))
	branchHead := f.Head()

	for _, args := range [][]string{
		{"change", "ready"},
		{"change", "unready"},
		{"review", "submit", "--approve"},
		{"change", "abandon"},
		{"change", "integrate"},
	} {
		res := runIn(t, f.Dir(), args...)
		if res.code != exitUsage {
			t.Errorf("git-pair %v exited %d, want %d\n%s%s", args, res.code, exitUsage, res.stdout, res.stderr)
			continue
		}
		mustContain(t, res.stderr, "that changeset landed", "the answer must say the work is already on the destination")
		mustContain(t, res.stderr, slug, "and name the changeset it belongs to")
	}

	// No command may leave a marker behind either. A marker commit on landed work is the half-write the
	// write gate exists to prevent.
	if got := f.Head(); got != branchHead {
		t.Errorf("a refused command committed: HEAD is %s (%s), was %s (%s)",
			shortOf(got), f.Subject(got), shortOf(branchHead), f.Subject(branchHead))
	}
	// And nothing durable was written: the landing is a commit in the destination, and no command in
	// git-pair moves a ref.
	if got := durableRefs(t, f); len(got) != 0 {
		t.Errorf("the landing left durable refs behind: %v", got)
	}
}

// Landing means the destination. Work merged into some other branch — a release line the author keeps
// separately — has not reached where it was headed, so the branch is still work in progress and the author
// can still ask for the merge. This is what milestone M1 of docs/plans/simplify-architecture/plan.md buys
// and what it pays: the durable record used to refuse here, and it refused on a fact about a command that
// was run rather than about the destination's tree.
func TestLandingOnAnotherBranchLeavesTheWorkInProgress(t *testing.T) {
	f, slug, source, interim := recordFixture(t)
	runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", interim,
		"--target", "release/2.x").mustSucceed(t, "integration", "record")
	f.SwitchTo(slug)

	view := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)
	if view["state"] != "APPROVED" || view["changeset"] != slug {
		t.Fatalf("status = %v/%v, want %s APPROVED: a landing on another branch is not a landing",
			view["changeset"], view["state"], slug)
	}
	declared := runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate")
	mustContain(t, declared.stdout, "merge into:  main", "the declaration names the destination the work has not reached")
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

	runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing, "--target", "release/2.x").mustSucceed(t, "integration", "record")
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

	// No --target: the changeset's own `base:` is main and the landing is in main, so the destination
	// git-pair derives is the one the record should be verified against.
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

// The answer every in-flight command gives for a changeset the destination already holds. `check` gets
// there through the resolver, which drops a landed directory from what a branch is working on, so the
// command refuses before it reaches the gate: the branch is not working on that changeset any more. The
// gate keeps its own landing clause for the path that reads a landed changeset by name, asserted in
// TestIntegrationReasons and end-to-end once the trunk walk replaces the archive-ref fallback.
func TestCheckRefusesAChangesetTheDestinationAlreadyHolds(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "approve")
	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land booking", "booking")
	landing := f.RevParse("main")
	f.SwitchTo("booking")

	res := runIn(t, f.Dir(), "check")
	if res.code != exitUsage {
		t.Fatalf("check on a landed changeset exited %d, want %d\n%s", res.code, exitUsage, res.stdout+res.stderr)
	}
	mustContain(t, res.stderr, "that changeset landed", "the answer says the destination holds the work")
	mustContain(t, res.stderr, slug, "and names the changeset")
	// The landing is a commit in the destination and nothing else: no command wrote a ref.
	if got := durableRefs(t, f); len(got) != 0 {
		t.Errorf("the landing left durable refs behind: %v", got)
	}
	if f.RevParse("main") != landing {
		t.Errorf("main moved from %s", shortOf(landing))
	}
}

// The queue is where the release-branch case was unknowable before this command: the directory is
// still absent from trunk, so the tree rule keeps reporting live work. The record is what lets the
// queue say "asked and answered", and it says so rather than dropping the row in silence — this
// branch is still here, and its disappearance would otherwise read as a bug.
func TestQueueSkipsAnIntegratedChangeset(t *testing.T) {
	f, slug, source, landing := recordFixture(t)
	// The queue lists what is offered, and an approval is not an offer — so the changeset is re-offered
	// to see it there. The record still names the approved head: a head whose newest marker is `ready`
	// has no verdict on it, and the recorder refuses it for that reason (§11.4).
	f.SwitchTo("booking")
	runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")
	f.SwitchTo("main")

	before := runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
	mustContain(t, before.stdout, slug, "the offered changeset is in the queue before it lands")

	runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing, "--target", "release/2.x").mustSucceed(t, "integration", "record")
	after := runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
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
