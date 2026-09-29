package changeset_test

import (
	"context"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/gittest"
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
	// The changeset comes from the resolver rather than being typed by hand, because that is the shape the
	// product asks the question about: `DestinationFor` is asked about the base the rest of the product
	// measures against - what the resolver put in `Base`, derived where the stack needs it derived - and not
	// about the string a yaml file happens to carry.
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

// stageStacked writes a changeset directory stacked on a parent branch. `parent:` is the base, and the
// parent's changeset is recorded beside it — the pair that still names the relationship once the parent
// has landed and its branch has been tidied away.
func stageStacked(t *testing.T, f *gittest.Fixture, slug, parentBranch, parentSlug string) {
	t.Helper()
	f.StageChangeset(slug, parentBranch)
	f.Write(f.ChangesetPath(slug, "CHANGESET.yaml"),
		"id: "+slug+"\nparent: "+parentBranch+"\nparent-changeset: "+parentSlug+"\n")
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
// its parent branch, and `change tidy` is what moves such a directory once the branch is gone).
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
// branch, and a merge target has to be a branch. The answer comes from the parent's own record, which
// landed with the parent's directory.
func TestDestinationFollowsALandedParentToItsBase(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("booking work", gittest.WithFile("booking.txt", "1\n"))
	f.CreateBranch("booking-tests")
	stageStacked(t, f, "booking-tests", "booking", "booking")
	f.Commit("test work", gittest.WithFile("booking_test.txt", "1\n"))

	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land booking", "booking")

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
	f.Commit("alpha work", gittest.WithFile("alpha.txt", "1\n"))
	f.CreateBranch("beta")
	stageStacked(t, f, "beta", "alpha", "alpha")
	f.Commit("beta work", gittest.WithFile("beta.txt", "1\n"))
	f.CreateBranch("gamma")
	stageStacked(t, f, "gamma", "beta", "beta")
	f.Commit("gamma work", gittest.WithFile("gamma.txt", "1\n"))

	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land beta", "beta")
	f.SwitchTo("alpha")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land alpha", "alpha")

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
	f.Commit("booking work", gittest.WithFile("booking.txt", "1\n"))
	f.CreateBranch("booking-tests")
	stageStacked(t, f, "booking-tests", "booking", "booking")
	f.Commit("test work", gittest.WithFile("booking_test.txt", "1\n"))

	f.SwitchTo("release/2.x")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "merge booking into the release line", "booking")
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

// The five shapes of the hop-0 rule. `DestinationFor` used to prove only the values it walked, which left
// the authored `base:` returned unchecked - so a stack whose parent merged kept being told to merge into the
// parent's branch, by a command that reads the file rather than the destination. Each shape asserts the answer
// and the reason for it together, because "main" on its own says nothing about which rule produced it.

// Shape 1, and the one the rule exists for: the base names a branch whose changeset the destination already
// carries. The authored field is refused, the walk continues through the landed record, and the refusal is
// reported rather than applied in silence.
func TestDestinationRefusesABaseThatNamesALandedChangeset(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("feature/x")
	f.CommitChangeset("feature-x", "main")
	f.Commit("x work", gittest.WithFile("x.txt", "1\n"))
	f.CreateBranch("ui", "feature/x")
	f.StageChangeset("ui", "feature/x")
	f.Write(f.ChangesetPath("ui", "CHANGESET.yaml"), "id: ui\nbase: feature/x\n")
	f.Commit("ui work", gittest.WithFile("ui.txt", "1\n"))

	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land feature/x", "feature/x")

	f.SwitchTo("ui")
	got := destinationOf(t, f, "ui")
	if got.Ref != "main" || got.Why != "base-landed" {
		t.Errorf("destination = %s, want main (base-landed)", got)
	}
	if len(got.Via) != 1 || got.Via[0] != "feature-x" {
		t.Errorf("via = %v, want [feature-x]: the report names the changeset it crossed", got.Via)
	}
	if got.Overrode != "feature/x" {
		t.Errorf("overrode = %q, want feature/x: the field that was refused is named, in the spelling it was written", got.Overrode)
	}
	if got.Unreachable != "" {
		t.Errorf("unreachable = %q, want empty: the base resolved, it was just not a destination", got.Unreachable)
	}
}

// Shape 2: the base names nothing this clone can resolve. The old code handed that back as the destination,
// so the sentence told somebody to merge into a branch that does not exist. The answer falls back to the
// integration branch and says which branch was missing - and the measurement base is untouched, because that
// is `BaseFor`'s question and it has its own fallback.
func TestDestinationFallsBackWhenTheOwnBaseDoesNotResolve(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("solo")
	f.StageChangeset("solo", "feature/gone")
	f.Write(f.ChangesetPath("solo", "CHANGESET.yaml"), "id: solo\nbase: feature/gone\n")
	f.Commit("solo work", gittest.WithFile("solo.txt", "1\n"))

	db, err := changeset.DefaultBranch(context.Background(), repo(f), "")
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	got := destinationOf(t, f, "solo")
	if got.Ref != db.Ref || got.Why != "default" {
		t.Errorf("destination = %s, want %s (default)", got, db.Ref)
	}
	if got.Unreachable != "feature/gone" || got.Overrode != "feature/gone" {
		t.Errorf("unreachable = %q, overrode = %q, want feature/gone in both", got.Unreachable, got.Overrode)
	}

	base, err := changeset.BaseFor(context.Background(), repo(f), resolveAt(t, f, "HEAD").Selected.Changeset, f.Head(), db)
	if err != nil {
		t.Fatalf("BaseFor: %v", err)
	}
	if base.Ref != "feature/gone" {
		t.Errorf("measurement base = %q, want feature/gone: a destination fallback is not a re-measurement", base.Ref)
	}
}

// Shape 3, the one this rule could break silently: a base naming a branch that is live work. Nothing is
// landed, so nothing is overridden, and the answer is the authored base exactly as written.
func TestDestinationKeepsALiveParentsBranchAsTheBase(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("feature/x")
	f.CommitChangeset("feature-x", "main")
	f.Commit("x work", gittest.WithFile("x.txt", "1\n"))
	f.CreateBranch("ui", "feature/x")
	f.StageChangeset("ui", "feature/x")
	f.Write(f.ChangesetPath("ui", "CHANGESET.yaml"), "id: ui\nbase: feature/x\n")
	f.Commit("ui work", gittest.WithFile("ui.txt", "1\n"))

	got := destinationOf(t, f, "ui")
	if got.Ref != "feature/x" || got.Why != "base" {
		t.Errorf("destination = %s, want feature/x (base): live work is a destination", got)
	}
	if got.Overrode != "" || len(got.Via) != 0 || got.Unreachable != "" {
		t.Errorf("destination = %s via %v overrode %q unreachable %q, want an unremarked answer", got, got.Via, got.Overrode, got.Unreachable)
	}
}

// Shape 4: the same fact written two ways. A branch name and the changeset id it normalises to are the same
// base, and the answer prints the id it matched - `SlugFromBranch` is many-to-one, so the author's spelling
// cannot be echoed back as if it were the id.
func TestDestinationRecognisesTheLandedChangesetByBranchAndByID(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("feature/x")
	f.CommitChangeset("feature-x", "main")
	f.Commit("x work", gittest.WithFile("x.txt", "1\n"))
	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land feature/x", "feature/x")

	for _, tc := range []struct{ child, base, spelling string }{
		{"ui-branch", "feature/x", "the branch name"},
		{"ui-id", "feature-x", "the changeset id"},
	} {
		f.CreateBranch(tc.child, "main")
		f.StageChangeset(tc.child, tc.base)
		f.Write(f.ChangesetPath(tc.child, "CHANGESET.yaml"), "id: "+tc.child+"\nbase: "+tc.base+"\n")
		f.Commit(tc.child+" work", gittest.WithFile(tc.child+".txt", "1\n"))

		f.SwitchTo(tc.child)
		got := destinationOf(t, f, tc.child)
		if got.Ref != "main" || got.Why != "base-landed" {
			t.Errorf("%s (%s): destination = %s, want main (base-landed)", tc.child, tc.spelling, got)
		}
		if len(got.Via) != 1 || got.Via[0] != "feature-x" {
			t.Errorf("%s (%s): via = %v, want [feature-x]: the matched id, not the spelling", tc.child, tc.spelling, got.Via)
		}
		if got.Overrode != tc.base {
			t.Errorf("%s (%s): overrode = %q, want %q", tc.child, tc.spelling, got.Overrode, tc.base)
		}
		f.SwitchTo("main")
	}
}

// Shape 5: the walk continues rather than jumping to the default branch, and stops at the first hop that is
// still live work. `beta` landed with `alpha`'s directory retired, so trunk carries `beta` and not `alpha`,
// and `alpha` is a live branch below it. A rule that gave up after one override would answer main here.
func TestDestinationStopsAtTheLiveParentAboveALandedOne(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("alpha.txt", "1\n"))
	f.CreateBranch("beta", "alpha")
	stageStacked(t, f, "beta", "alpha", "alpha")
	f.Commit("beta work", gittest.WithFile("beta.txt", "1\n"))
	f.MustGit("rm", "-r", "--quiet", "changesets/alpha")
	f.MustGit("commit", "-q", "-m", "retire alpha's directory")
	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land beta", "beta")

	f.CreateBranch("gamma", "main")
	f.StageChangeset("gamma", "beta")
	f.Write(f.ChangesetPath("gamma", "CHANGESET.yaml"), "id: gamma\nbase: beta\n")
	f.Commit("gamma work", gittest.WithFile("gamma.txt", "1\n"))

	got := destinationOf(t, f, "gamma")
	if got.Ref != "alpha" || got.Why != "base-landed" {
		t.Errorf("destination = %s, want alpha (base-landed): the walk stops at the hop that is live work", got)
	}
	if len(got.Via) != 1 || got.Via[0] != "beta" {
		t.Errorf("via = %v, want [beta]", got.Via)
	}
}
