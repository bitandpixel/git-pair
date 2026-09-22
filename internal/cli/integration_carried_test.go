package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
)

// The stacked landing. A child merges into the branch it was based on, that branch later lands on trunk,
// and a run measured against trunk names a descendant of what the record names. Before this, that run
// refused with "the record was written from the wrong commit" about a record that was correct — the case
// that happened for real when feat-publish-the-records (recorded at its merge into feat/two-frozen-refs)
// was re-recorded against main after the parent branch landed.
func TestRecordAcceptsACommitThatCarriesTheRecord(t *testing.T) {
	f, slug, source, interim := recordFixture(t)
	runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", interim,
		"--target", "release/2.x").mustSucceed(t, "integration", "record")
	before := durableRefs(t, f)

	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "release/2.x lands on trunk", "release/2.x")
	trunk := f.Head()

	res := runIn(t, f.Dir(), "integration", "record", "--changeset", slug, "--source", source,
		"--commit", trunk, "--target", "main")
	res.mustSucceed(t, "integration", "record")
	mustContain(t, res.stdout, "already recorded at "+shortOf(interim),
		"a carrying answer has to name what the record actually says")
	mustContain(t, res.stdout, shortOf(trunk)+" carries that commit into main",
		"and the commit that carries it, into the destination it was measured against")
	// The two ref lines show the record, not the asked pair: a line pointing at a commit no ref holds
	// would read as a second record.
	mustContain(t, res.stdout, "integration: "+reviewref.Integration(slug)+" -> "+shortOf(interim),
		"the ref line shows the record")
	mustNotContain(t, res.stdout, "integration: "+reviewref.Integration(slug)+" -> "+shortOf(trunk),
		"and not the commit that was asked for")

	if got := f.RefSHA(reviewref.Integration(slug)); got != interim {
		t.Errorf("the integration ref moved to %s, want %s", got, interim)
	}
	if got := durableRefs(t, f); len(got) != len(before) {
		t.Errorf("the durable refs changed shape: %v, want %v", got, before)
	}

	carried := runIn(t, f.Dir(), "integration", "record", "--json", "--changeset", slug,
		"--source", source, "--commit", trunk, "--target", "main")
	carried.mustSucceed(t, "integration", "record")
	j := carried.json(t)
	if got, ok := j["carried_by"].(string); !ok || !strings.HasPrefix(trunk, got) {
		t.Errorf("carried_by is %#v, want a prefix of %s", j["carried_by"], trunk)
	}
	if j["recorded"] != false || j["already_recorded"] != true {
		t.Errorf("a carrying run wrote: recorded=%v already_recorded=%v", j["recorded"], j["already_recorded"])
	}
	// The exact repeat still reports no carrier: `carried_by` distinguishes the two kinds of no-op, which
	// is the whole reason a pipeline should not have to compare SHAs to tell them apart.
	exact := runIn(t, f.Dir(), "integration", "record", "--json", "--changeset", slug,
		"--source", source, "--commit", interim, "--target", "release/2.x")
	exact.mustSucceed(t, "integration", "record")
	if _, ok := exact.json(t)["carried_by"]; ok {
		t.Errorf("an exact repeat reported carried_by: %v", exact.json(t))
	}
}

// The carrying rule is about a later position on the same chain. A different reviewed head is a different
// claim about what was approved, so an archive mismatch has to stay a conflict even when the integration
// half descends cleanly — otherwise a wrong `--source` would be swallowed by a correct-looking ancestry.
func TestRecordCarryingKeepsTheArchiveClaim(t *testing.T) {
	f, slug, source, interim := recordFixture(t)
	runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", interim,
		"--target", "release/2.x").mustSucceed(t, "integration", "record")

	f.SwitchTo("release/2.x")
	later := f.Commit("a commit on the interim branch, after the reviewed head", gittest.WithFile("drift.md", "drift\n"))
	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "release/2.x lands on trunk", "release/2.x")
	trunk := f.Head()

	res := runIn(t, f.Dir(), "integration", "record", "--changeset", slug, "--source", later,
		"--commit", trunk, "--target", "main")
	if res.code == exitOK {
		t.Fatalf("a different reviewed head was accepted as carried:\n%s", res.stdout)
	}
	joined := res.stdout + res.stderr
	mustContain(t, joined, "was asked for", "the conflict wording is the right answer here")
	mustNotContain(t, joined, "carries that commit", "and it is not a carrying case")
	if got := f.RefSHA(reviewref.Archive(slug)); got == later {
		t.Errorf("the archive ref moved to the later head %s", later)
	}
}

// "Carries it into the destination" is a claim, so a named --target that does not hold the asked commit
// refuses rather than answering with the record.
func TestRecordRefusesACarrierTheTargetDoesNotHold(t *testing.T) {
	f, slug, source, interim := recordFixture(t)
	runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", interim,
		"--target", "release/2.x").mustSucceed(t, "integration", "record")

	// A merge into a branch trunk never sees: it carries the record, so this is the carrying shape, and
	// `--target main` is the claim that fails.
	f.SwitchTo("main")
	f.Commit("work trunk did the merging with", gittest.WithFile("trunk.md", "trunk\n"))
	base := f.Head()
	f.CreateBranch("integration-area", "main")
	f.SwitchTo("integration-area")
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "release/2.x lands somewhere else", "release/2.x")
	elsewhere := f.Head()
	if base == "" {
		t.Fatal("fixture lost its base commit")
	}

	res := runIn(t, f.Dir(), "integration", "record", "--changeset", slug, "--source", source,
		"--commit", elsewhere, "--target", "main")
	if res.code == exitOK {
		t.Fatalf("a carrier trunk does not hold was accepted:\n%s", res.stdout)
	}
	joined := res.stdout + res.stderr
	mustContain(t, joined, "not reachable from main",
		"the refusal is the reachability one, because that is the claim that failed")
	mustNotContain(t, joined, "carries that commit", "so nothing is said about carrying")
	if got := f.RefSHA(reviewref.Integration(slug)); got != interim {
		t.Errorf("the integration ref moved to %s, want %s", got, interim)
	}
}
