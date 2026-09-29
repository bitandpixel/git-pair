package changeset_test

import (
	"context"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
)

// The rule under test: where a changeset's work lands is not always the base it is measured against. For
// a stack whose parent has landed, the base is the parent's integration ref — a commit, which nothing can
// merge into — and the destination has to be read further up the stack instead. These fixtures are the
// shapes where the two answers differ, plus the ones where the walk has nothing to walk and has to say so
// rather than invent a branch.

func destinationOf(t *testing.T, f *gittest.Fixture, slug string) changeset.Destination {
	t.Helper()
	ctx := context.Background()
	r := repo(f)
	db, err := changeset.DefaultBranch(ctx, r, "")
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	// The changeset comes from the resolver rather than being typed by hand, because the relink that
	// makes this question interesting happens there: `DestinationFor` is asked about the base the rest of
	// the product measures against, not the string a yaml file happens to carry.
	res := resolveAt(t, f, "HEAD")
	if res.Selected == nil || res.Selected.Changeset.Slug != slug {
		t.Fatalf("resolved %q, want %q (candidates %v)", selectedID(res), slug, candidateIDs(res))
	}
	got, err := changeset.DestinationFor(ctx, r, res.Selected.Changeset, db)
	if err != nil {
		t.Fatalf("DestinationFor: %v", err)
	}
	return got
}

// recordLanding writes the pair `integration record` would have written for slug at commit, which is what
// a destination walk has to read. The archive half is the branch's own tip, so the fixture says what the
// recorder would have said instead of the test inventing a ref path.
// stageStacked writes a changeset directory stacked on a parent branch. `parent:` is the base, and the
// parent's changeset is recorded beside it — the pair that still names the relationship once the parent
// has landed and its branch has been tidied away.
func stageStacked(t *testing.T, f *gittest.Fixture, slug, parentBranch, parentSlug string) {
	t.Helper()
	f.StageChangeset(slug, parentBranch)
	f.Write(f.ChangesetPath(slug, "CHANGESET.yaml"),
		"id: "+slug+"\nparent: "+parentBranch+"\nparent-changeset: "+parentSlug+"\n")
}

func recordLanding(t *testing.T, f *gittest.Fixture, slug, commit, source string) {
	t.Helper()
	if _, err := reviewref.CreatePair(context.Background(), repo(f),
		reviewref.Pair{ID: slug, Archive: source, Integration: commit}); err != nil {
		t.Fatalf("record %s: %v", slug, err)
	}
}

// A changeset measured against a branch has the simplest answer, and it is worth pinning because every
// other rule here is an exception to it: the destination is the base, and the walk never runs.
func TestDestinationIsTheBaseForUnstackedWork(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("work", gittest.WithFile("booking.txt", "1\n"))

	got := destinationOf(t, f, "booking")
	if got.Ref != "main" || got.Why != "base" || len(got.Via) != 0 {
		t.Errorf("destination = %s via %v, want main (base)", got, got.Via)
	}
}

// A child stacked on a parent that has not landed still has the branch as its base, and the branch as its
// destination: this is the stacked landing that stays a human decision (git-pair records a child landed on
// its parent branch — see `integration record`'s carried answer — it only declines to queue one).
func TestDestinationIsTheParentBranchWhileTheParentIsUnlanded(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("booking work", gittest.WithFile("booking.txt", "1\n"))
	f.CreateBranch("booking-tests")
	stageStacked(t, f, "booking-tests", "booking", "booking")
	f.Commit("test work", gittest.WithFile("booking_test.txt", "1\n"))

	got := destinationOf(t, f, "booking-tests")
	if got.Ref != "booking" || got.Why != "base" {
		t.Errorf("destination = %s, want booking (base): the parent has no record, so nothing has moved", got)
	}
}

// The case the rule exists for. The parent landed on trunk, the child's base became the parent's
// integration ref, and a destination of `refs/git-pair/integrations/booking` would name a commit and no
// branch — `integration record` refuses one as `--target` for exactly that reason. The answer comes from
// the parent's own record, which landed with the parent's directory.
func TestDestinationFollowsALandedParentToItsBase(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	parentTip := f.Commit("booking work", gittest.WithFile("booking.txt", "1\n"))
	f.CreateBranch("booking-tests")
	stageStacked(t, f, "booking-tests", "booking", "booking")
	f.Commit("test work", gittest.WithFile("booking_test.txt", "1\n"))

	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land booking", "booking")
	recordLanding(t, f, "booking", f.Head(), parentTip)

	f.SwitchTo("booking-tests")
	got := destinationOf(t, f, "booking-tests")
	if got.Ref != "main" || got.Why != "parent" {
		t.Errorf("destination = %s, want main (parent)", got)
	}
	if len(got.Via) != 1 || got.Via[0] != "booking" {
		t.Errorf("via = %v, want [booking]: the report has to name the chain it crossed", got.Via)
	}
	if got.Unreachable != "" {
		t.Errorf("unreachable = %q, want empty", got.Unreachable)
	}
}

// A stack three deep, where both ancestors landed: the walk crosses two records and lands on the branch
// under all of it. This is where a rule that gave up after one hop would return a durable ref.
func TestDestinationWalksAParentChain(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	alphaTip := f.Commit("alpha work", gittest.WithFile("alpha.txt", "1\n"))
	f.CreateBranch("beta")
	stageStacked(t, f, "beta", "alpha", "alpha")
	betaTip := f.Commit("beta work", gittest.WithFile("beta.txt", "1\n"))
	f.CreateBranch("gamma")
	stageStacked(t, f, "gamma", "beta", "beta")
	f.Commit("gamma work", gittest.WithFile("gamma.txt", "1\n"))

	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land beta", "beta")
	recordLanding(t, f, "beta", f.Head(), betaTip)
	f.SwitchTo("alpha")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land alpha", "alpha")
	recordLanding(t, f, "alpha", f.Head(), alphaTip)

	f.SwitchTo("gamma")
	got := destinationOf(t, f, "gamma")
	if got.Ref != "main" || got.Why != "parent" {
		t.Errorf("destination = %s, want main (parent)", got)
	}
	if len(got.Via) != 2 || got.Via[0] != "beta" || got.Via[1] != "alpha" {
		t.Errorf("via = %v, want [beta alpha] nearest first", got.Via)
	}
}

// A parent merged only into a release branch is not landed (§12: the integration branch holds the directory
// or it does not), so the child's stack still stands on the parent branch — and that branch is where the
// child's work is asked to land. The record used to answer this by reading the parent's own base out of the
// landing commit and, when that branch had been deleted, falling back to the integration branch and naming
// it unreachable. With the refs gone there is no such reading: the tree says the parent is live work, and the
// destination is the branch the stack sits on.
func TestDestinationIsTheLiveParentBranchWhenTheParentReachedOnlyAReleaseLine(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("release/2.x")
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "release/2.x")
	tip := f.Commit("booking work", gittest.WithFile("booking.txt", "1\n"))
	f.CreateBranch("booking-tests")
	stageStacked(t, f, "booking-tests", "booking", "booking")
	f.Commit("test work", gittest.WithFile("booking_test.txt", "1\n"))

	f.SwitchTo("release/2.x")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "merge booking into the release line", "booking")
	recordLanding(t, f, "booking", f.Head(), tip)
	f.SwitchTo("main")
	f.ForceDeleteBranch("release/2.x")

	f.SwitchTo("booking-tests")
	got := destinationOf(t, f, "booking-tests")
	if got.Ref != "booking" || got.Why != "base" {
		t.Errorf("destination = %s, want booking (base): the parent is live work and the stack sits on it", got)
	}
	if got.Unreachable != "" {
		t.Errorf("unreachable = %q, want empty: nothing in the tree names a branch that went away", got.Unreachable)
	}
	if len(got.Via) != 0 {
		t.Errorf("via = %v, want none: nothing was crossed", got.Via)
	}
}

// A base under `refs/git-pair/` that is not an integration ref — an archive ref, a stray, a retired
