package cli_test

import (
	"fmt"
	"testing"
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
