package cli_test

import (
	"fmt"
	"testing"

	"gitpair/internal/gittest"
)

// The queue resolves every branch, so its cost has to follow the branches and not the number
// of changesets the repository has ever had. The formulation this rule replaced asked a question
// per archive ref, which was invisible in a young repository and unusable in an old one, so the
// assertion is the scaling: the same queue over the same branches, with 300 changesets' worth of
// noise added, must not notice.
//
// The noise here is refs under git-pair's retired namespace — what a repository that used the old
// layout still holds. Nothing reads them any more, so they are worth the one `for-each-ref` that
// lists branches and cannot name a changeset.
func TestReviewQueueCostFollowsBranchesNotExistingChangesets(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("work")
	f.CommitChangeset("work", "main")
	ready(t, f)

	count := f.SpawnShim(t)

	before := count()
	runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	withoutRefs := count() - before

	base := f.Head()
	for i := 0; i < 300; i++ {
		f.MustGit("update-ref", "refs/git-pair/archive/"+fmt.Sprintf("cs-%03d", i), base)
	}

	before = count()
	runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	withRefs := count() - before

	if withoutRefs < 1 {
		t.Fatalf("the queue counted %d invocations: the shim measured nothing, so the bound below proves nothing", withoutRefs)
	}

	// One for-each-ref is all the extra refs are worth: they are listed, and only the ones
	// naming a changeset this revision carries are asked about further.
	if withRefs > withoutRefs+2 {
		t.Errorf("queue cost %d git invocations with 300 review refs present and %d without: the refs are being read per changeset", withRefs, withoutRefs)
	}
}

// The finding that landed-but-unreviewed work is a fact about the destination, so the queue pays for it
// per landing: one chain derivation and one lifecycle walk each. That is the price of the answer being
// true in a clone that has fetched nothing but the destination — the reason the report reads the tree at
// all — and this test is what keeps the price bounded rather than quietly quadratic in the size of the
// repository. It is the same queue over the same branches, with three hundred landed directories added to
// the destination, measured against the queue that has none.
//
// The bound is per landing, and the number is measured rather than guessed: four git invocations is the
// chain (the boundary walk, two revisions, one parent count) plus the walk over the range the chain names.
// A landing whose chain is short costs its own length, which is why the assertion allows slack.
func TestReviewQueueCostPerLandedChangesetIsBounded(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("work")
	f.CommitChangeset("work", "main")
	ready(t, f)

	count := f.SpawnShim(t)

	before := count()
	runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	empty := count() - before

	files := map[string]string{}
	for i := 0; i < 300; i++ {
		id := fmt.Sprintf("cs-%03d", i)
		files["changesets/"+id+"/CHANGESET.yaml"] = "id: " + id + "\nbase: main\n"
	}
	f.SwitchTo("main")
	f.Commit("land three hundred changesets", gittest.WithFiles(files))

	before = count()
	runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	withDirs := count() - before

	// The three hundred above came in through one commit, so every one of them is a squash: the landing
	// verdict short-circuits on "no markers came with it" and never asks the destination for an ancestor.
	// A destination whose landings carry markers does ask, and that is the shape the queue meets in a real
	// repository, so measure it too — twenty runs, each brought in by a merge commit with its ready marker
	// and its approval behind it. Without this, the bound below would have been silently lifted by the read
	// `landingLicence` adds, because the fixture could not reach it.
	marked := 20
	for i := 0; i < marked; i++ {
		id := fmt.Sprintf("marked-%02d", i)
		f.CreateBranch(id)
		f.CommitChangeset(id, "main")
		ready(t, f)
		submit(t, f, "approve")
		f.SwitchTo("main")
		f.MustGit("merge", "--quiet", "--no-ff", "-m", "Merge "+id+" into main", id)
	}
	before = count()
	runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	markedCost := count() - before

	if empty < 1 {
		t.Fatalf("the queue counted %d invocations: the shim measured nothing, so the bound below proves nothing", empty)
	}
	// Measured here: 16 invocations per landing whose chain carries markers, against the 11 above. The extra
	// five are what a real chain costs — the boundary walk a squash never takes, the lifecycle walk over the
	// run, and the one ancestor question the verdict asks. The bound is a third above, which still leaves
	// room for one more read per landing and no room for walking a branch per landing.
	if per := float64(markedCost-withDirs) / float64(marked); per > 21 {
		t.Errorf("queue costs %.1f git invocations per marked landing (%d with %d marked landings, %d without): "+
			"a landing whose chain carries an approval asks the destination one ancestor question, and nothing "+
			"else should grow with it", per, markedCost, marked, withDirs)
	}
	const landings = 300
	// Measured on this repository: 11 invocations per directory the destination carries. They are the chain
	// (the boundary walk, the tip revision, two containment reads, a parent count), the lifecycle walk over
	// the range the chain names, and the orphan classification — which now has to ask the destination whether
	// it carries the directory before it can stay silent about it. The bound is a third above that, so a
	// change that adds one read per landing still passes and one that walks the branch per landing does not.
	if per := float64(withDirs-empty) / landings; per > 14 {
		t.Errorf("queue costs %.1f git invocations per landed changeset (empty queue %d, %d with %d landings): "+
			"the finding is meant to cost a bounded few reads each, not a walk of the destination",
			per, empty, withDirs, landings)
	}
}
