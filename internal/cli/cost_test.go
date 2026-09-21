package cli_test

import (
	"fmt"
	"testing"

	"gitpair/internal/gittest"
)

// The queue resolves every branch, so its cost has to follow the branches and not the number
// of changesets the repository has ever had. The formulation this rule replaced asked a question
// per archive ref, which was invisible in a young repository and unusable in an old one, so the
// assertion is the scaling: the same queue over the same branches, with 300 review refs added,
// must not notice.
func TestReviewQueueCostFollowsBranchesNotExistingChangesets(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("work")
	f.CommitChangeset("work", "main")
	ready(t, f)

	count := f.SpawnShim(t)

	before := count()
	runIn(t, f.Dir(), "review", "queue", "--json").mustSucceed(t, "review", "queue", "--json")
	withoutRefs := count() - before

	base := f.Head()
	for i := 0; i < 300; i++ {
		f.MustGit("update-ref", fmt.Sprintf("refs/git-pair/changesets/cs-%03d/archive", i), base)
	}

	before = count()
	runIn(t, f.Dir(), "review", "queue", "--json").mustSucceed(t, "review", "queue", "--json")
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

// The unrecorded-landing report reads the destination's directories, which the branch scan had already
// listed, and one `for-each-ref` for the namespace — so a repository full of landings nobody recorded
// must cost the queue nothing per landing. The assertion is the same shape as the one above it: the same
// queue over the same branches, with three hundred directories added to the destination, must not notice.
//
// This is the second half of the reason the report reads the scan's trunk listing rather than asking for
// its own. The first is that it is the same answer.
func TestReviewQueueCostDoesNotGrowWithUnrecordedLandings(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("work")
	f.CommitChangeset("work", "main")
	ready(t, f)

	count := f.SpawnShim(t)

	before := count()
	runIn(t, f.Dir(), "review", "queue", "--json").mustSucceed(t, "review", "queue", "--json")
	empty := count() - before

	files := map[string]string{}
	for i := 0; i < 300; i++ {
		id := fmt.Sprintf("cs-%03d", i)
		files["changesets/"+id+"/CHANGESET.yaml"] = "id: " + id + "\nbase: main\n"
	}
	f.SwitchTo("main")
	f.Commit("land three hundred changesets", gittest.WithFiles(files))

	before = count()
	runIn(t, f.Dir(), "review", "queue", "--json").mustSucceed(t, "review", "queue", "--json")
	withDirs := count() - before

	if empty < 1 {
		t.Fatalf("the queue counted %d invocations: the shim measured nothing, so the bound below proves nothing", empty)
	}
	if withDirs > empty+1 {
		t.Errorf("queue cost %d git invocations with 300 unrecorded landings in the destination and %d without: the report is reading per landing", withDirs, empty)
	}
}
