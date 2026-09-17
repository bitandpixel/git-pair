package changeset_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
)

// The rule under test: the changesets on a revision are the `changesets/<id>/` directories in
// its tree that the integration branch's tree does not have. These fixtures come from the
// measurements in docs/plans/completed/identity-and-integration/research/, where each one is the
// answer a different formulation of the rule got wrong.

// repo is the package's convention for a handle on a fixture's repository.
func repo(f *gittest.Fixture) *git.Repo { return &git.Repo{Dir: f.Dir()} }

func resolveAt(t *testing.T, f *gittest.Fixture, rev string) changeset.Resolution {
	t.Helper()
	repo := repo(f)
	db, err := changeset.DefaultBranch(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	got, err := changeset.Resolve(context.Background(), repo, rev, db)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return got
}

func reviewRef(f *gittest.Fixture, id, sha string) {
	f.MustGit("update-ref", reviewref.Archive(id), sha)
}

func selectedID(got changeset.Resolution) string {
	if got.Selected == nil {
		return ""
	}
	return got.Selected.Changeset.Slug
}

func candidateIDs(got changeset.Resolution) []string {
	ids := make([]string, len(got.Candidates))
	for i, c := range got.Candidates {
		ids[i] = c.Changeset.Slug
	}
	return ids
}

func TestResolveStackedPicksTheNearest(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))

	f.CreateBranch("booking")
	parent := f.CommitChangeset("booking", "main")
	reviewRef(f, "booking", parent)

	f.CreateBranch("booking-tests")
	child := f.CommitChangeset("booking-tests", "refs/git-pair/changesets/booking/archive")
	reviewRef(f, "booking-tests", child)
	f.Commit("more tests", gittest.WithFile("booking_test.txt", "1\n"))

	if got := resolveAt(t, f, "HEAD"); selectedID(got) != "booking-tests" {
		t.Errorf("on the child branch selected %q, want booking-tests (nearest review ref)", selectedID(got))
	}

	f.SwitchTo("booking")
	if got := resolveAt(t, f, "HEAD"); selectedID(got) != "booking" {
		t.Errorf("on the parent branch selected %q, want booking", selectedID(got))
	}
}

// A branch created off an active changeset continues it, so both branches carry the same
// directory and neither has reached trunk. One movable ref cannot attribute two lines of
// development; the content test can, and does so without asking which branch is checked out.
func TestResolveDivergedBranchesShareTheChangeset(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))

	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.CreateBranch("booking-experiment")
	f.Commit("experiment", gittest.WithFile("experiment.txt", "x\n"))
	f.SwitchTo("booking")
	f.Commit("parent continues", gittest.WithFile("parent.txt", "p\n"))

	for _, branch := range []string{"booking", "booking-experiment"} {
		f.SwitchTo(branch)
		if got := resolveAt(t, f, "HEAD"); selectedID(got) != "booking" {
			t.Errorf("on %s selected %q, want booking on both branches", branch, selectedID(got))
		}
	}
}

// Landed work drops out because trunk holds the directory: no integration ref, no ancestry
// test, and no dependence on merge versus squash versus cherry-pick.
func TestResolveLandedChangesetDropsOut(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("work")
	f.CommitChangeset("work", "main")
	f.Commit("work", gittest.WithFile("work.txt", "1\n"))

	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land work", "work")

	f.SwitchTo("work")
	if got := resolveAt(t, f, "HEAD"); got.Selected != nil {
		t.Errorf("selected %q after landing, want nothing", selectedID(got))
	}
}

// The stacked case with the parent already landed: the parent's directory came down with the
// merge, so only the child is work in progress.
func TestResolveChildOfLandedParent(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("booking work", gittest.WithFile("booking.txt", "1\n"))
	f.CreateBranch("booking-tests")
	f.CommitChangeset("booking-tests", "refs/git-pair/changesets/booking/archive")
	f.Commit("test work", gittest.WithFile("booking_test.txt", "1\n"))

	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land booking", "booking")

	f.SwitchTo("booking-tests")
	got := resolveAt(t, f, "HEAD")
	if selectedID(got) != "booking-tests" {
		t.Errorf("selected %q from %v, want booking-tests", selectedID(got), candidateIDs(got))
	}
}

// Resolution reads trees, so a repository with no git-pair refs at all still answers. This is
// the CI shape: a checkout of the branch and of the integration branch, nothing else fetched.
func TestResolveWithoutAnyRefs(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("work", gittest.WithFile("booking.txt", "1\n"))

	if refs := f.RefNames(reviewref.NamespaceRoot()); len(refs) != 0 {
		t.Fatalf("fixture should hold no git-pair refs, got %v", refs)
	}
	if got := resolveAt(t, f, "HEAD"); selectedID(got) != "booking" {
		t.Errorf("selected %q, want booking", selectedID(got))
	}
}

// Merging an unlanded sibling puts their directory in your tree, and both are absent from
// trunk, so nothing in the durable data orders them. Refusing is the honest answer, and the
// recorded decision (`ignores:`) is what resolves it.
func TestResolveSiblingMergeIsAmbiguousUntilRecorded(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))

	f.CreateBranch("theirs")
	f.CommitChangeset("theirs", "main")
	f.Commit("their work", gittest.WithFile("their.txt", "1\n"))

	f.SwitchTo("main")
	f.CreateBranch("mine")
	f.CommitChangeset("mine", "main")
	f.Commit("my work", gittest.WithFile("mine.txt", "1\n"))
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "take their branch too", "theirs")

	got := resolveAt(t, f, "HEAD")
	if !got.Ambiguous || got.Selected != nil {
		t.Fatalf("got %v ambiguous=%v selected=%q, want two ambiguous candidates", candidateIDs(got), got.Ambiguous, selectedID(got))
	}
	if strings.Join(candidateIDs(got), " ") != "mine theirs" {
		t.Errorf("candidates = %v, want [mine theirs]", candidateIDs(got))
	}

	f.WriteChangesetFile("mine", "CHANGESET.yaml", "id: mine\nbase: main\nignores: theirs\n")
	f.Commit("record which changeset this branch works on")
	if got = resolveAt(t, f, "HEAD"); got.Ambiguous || selectedID(got) != "mine" {
		t.Errorf("after recording: selected %q from %v ambiguous=%v, want mine", selectedID(got), candidateIDs(got), got.Ambiguous)
	}
}

// Two unarchived candidates have no distance between them, so the stack order comes from the
// metadata: the candidate named as another's base is the parent.
func TestResolveOrdersAStackWithoutRefs(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("booking work", gittest.WithFile("booking.txt", "1\n"))
	f.CreateBranch("booking-tests")
	f.CommitChangeset("booking-tests", "refs/git-pair/changesets/booking/archive")
	f.Commit("test work", gittest.WithFile("booking_test.txt", "1\n"))

	got := resolveAt(t, f, "HEAD")
	if got.Ambiguous || selectedID(got) != "booking-tests" {
		t.Errorf("selected %q from %v ambiguous=%v, want booking-tests from base alone", selectedID(got), candidateIDs(got), got.Ambiguous)
	}
}

// `change abandon` leaves the directory in the tree, and the branch really is that changeset's
// branch. Reporting the candidate with its terminal fact beats saying "no changeset here",
// which would send the author to `change init` against a directory that is still there.
func TestResolveReportsATerminalCandidate(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("booking")
	init := f.CommitChangeset("booking", "main")
	f.CommitReadyMarker("booking")
	end := f.CommitMessage("git-pair: abandon booking\n\nReview-State: abandoned\n", gittest.WithEmpty())
	reviewRef(f, "booking", end)

	got := resolveAt(t, f, "HEAD")
	if got.Selected == nil {
		t.Fatal("selected nothing, want the abandoned changeset reported")
	}
	if !got.Selected.Terminal {
		t.Errorf("Terminal = false for a changeset abandoned at %s, want true", f.Short(end))
	}
	if got.Selected.Review != f.RevParse(end) {
		t.Errorf("Review = %s, want %s", got.Selected.Review, f.RevParse(end))
	}
	_ = init
}

func TestResolveDeletedDirectoryHasNoCandidate(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("booking")
	init := f.CommitChangeset("booking", "main")
	reviewRef(f, "booking", init)
	f.Commit("work", gittest.WithFile("booking.txt", "1\n"))
	f.MustGit("rm", "-r", "-q", "--", "changesets/booking")
	f.Commit("drop the directory", gittest.WithNoStage())

	if got := resolveAt(t, f, "HEAD"); got.Selected != nil {
		t.Errorf("selected %q after the directory was deleted, want nothing", selectedID(got))
	}
}

// A changeset initialised on the integration branch is inert: its directory is in trunk's
// tree, so it is not work in progress on trunk or on anything branched off it.
func TestResolveOnTrunkIsUnresolved(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CommitChangeset("scratch", "main")
	f.CreateBranch("off-trunk")
	f.Commit("real work", gittest.WithFile("b.txt", "b\n"))

	if got := resolveAt(t, f, "HEAD"); got.Selected != nil || len(got.Candidates) != 0 {
		t.Errorf("off trunk got %v, want nothing", candidateIDs(got))
	}
	f.SwitchTo("main")
	if got := resolveAt(t, f, "HEAD"); got.Selected != nil || len(got.Candidates) != 0 {
		t.Errorf("on trunk got %v, want nothing", candidateIDs(got))
	}
}

// Pruning the default branch removes the evidence the landed test reads, so a branch that has
// not merged the prune starts looking active again. Pinned as documented behaviour: a
// repository that tidies trunk may only prune changesets with a terminal record.
func TestResolvePruningTheIntegrationBranch(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("work")
	f.CommitChangeset("work", "main")
	f.Commit("work", gittest.WithFile("work.txt", "1\n"))
	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land work", "work")
	f.MustGit("rm", "-r", "-q", "--", "changesets/work")
	f.Commit("prune landed changeset directories", gittest.WithNoStage())

	f.SwitchTo("work")
	if got := resolveAt(t, f, "HEAD"); selectedID(got) != "work" {
		t.Errorf("after pruning trunk selected %q, want work: this is the documented resurrection", selectedID(got))
	}

	// Merging the prune is its own cure: the directory leaves the branch too.
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "catch up", "main")
	if got := resolveAt(t, f, "HEAD"); got.Selected != nil {
		t.Errorf("after merging the prune selected %q, want nothing", selectedID(got))
	}
}

// A base recorded as the parent's archive ref and one recorded as a branch name mean the same
// parent, so dropping the parent from the candidate set must not depend on the spelling.
func TestResolveBaseSpelling(t *testing.T) {
	for _, base := range []string{"booking", "refs/heads/booking", "refs/git-pair/changesets/booking/archive"} {
		t.Run(base, func(t *testing.T) {
			f := gittest.New(t)
			f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
			f.CreateBranch("booking")
			f.CommitChangeset("booking", "main")
			f.CreateBranch("child")
			f.CommitChangeset("child", base)
			if got := resolveAt(t, f, "HEAD"); selectedID(got) != "child" {
				t.Errorf("base %q: selected %q from %v, want child", base, selectedID(got), candidateIDs(got))
			}
		})
	}
}

// `ignores:` is a claim one changeset makes about another, and a hand-edited file can make two
// of them point at each other. Dropping both would report "no changeset here" for a branch that
// visibly has two, so the filter refuses to empty the set.
func TestResolveMutualIgnoresKeepsBothCandidates(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("one")
	f.CommitChangeset("one", "main")
	f.CreateBranch("two")
	f.CommitChangeset("two", "main")
	f.WriteChangesetFile("one", "CHANGESET.yaml", "id: one\nbase: main\nignores: two\n")
	f.WriteChangesetFile("two", "CHANGESET.yaml", "id: two\nbase: main\nignores: one\n")
	f.Commit("point them at each other")

	got := resolveAt(t, f, "HEAD")
	if !got.Ambiguous || len(got.Candidates) != 2 {
		t.Fatalf("got %v ambiguous=%v, want both candidates still standing", candidateIDs(got), got.Ambiguous)
	}
}

// Nearest review ref wins. The stacked test above is decided by the base naming its parent, so
// this one deliberately stacks on trunk: with nothing in the metadata ordering the two, the
// distances have to, and the child is the nearer.
func TestResolveNearestRefDecidesWhenNothingElseDoes(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("work")
	f.CommitChangeset("work", "main")
	f.Commit("work", gittest.WithFile("work.txt", "1\n"))
	reviewRef(f, "work", f.Head())

	f.CreateBranch("child")
	f.CommitChangeset("child", "main")
	f.Commit("child work", gittest.WithFile("child.txt", "1\n"))
	reviewRef(f, "child", f.Head())

	got := resolveAt(t, f, "HEAD")
	if got.Selected == nil {
		t.Fatal("selected nothing, want the nearer changeset")
	}
	if got.Selected.Changeset.Slug != "child" {
		t.Errorf("selected %q from %v, want child: it owns the nearer review ref", got.Selected.Changeset.Slug, candidateIDs(got))
	}
	if got.Selected.Distance >= got.Candidates[len(got.Candidates)-1].Distance {
		t.Errorf("candidates are not ordered near to far: %v/%v", candidateIDs(got), distances(got))
	}
}

// Two changesets both readied on their own branches and then merged into a third are the same
// distance from it, and nothing orders them. This is the tie the rule refuses rather than
// breaking: either answer silently picks a diff base.
func TestResolveEqualDistanceIsAmbiguous(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("one")
	f.CommitChangeset("one", "main")
	f.Commit("work one", gittest.WithFile("one.txt", "1\n"))
	reviewRef(f, "one", f.Head())

	f.SwitchTo("main")
	f.CreateBranch("two")
	f.CommitChangeset("two", "main")
	f.Commit("work two", gittest.WithFile("two.txt", "2\n"))
	reviewRef(f, "two", f.Head())

	f.SwitchTo("main")
	f.CreateBranch("both")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "take one", "one")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "take two", "two")

	got := resolveAt(t, f, "HEAD")
	if !got.Ambiguous || got.Selected != nil {
		t.Fatalf("got %v at distances %v ambiguous=%v selected=%q, want an undecided tie",
			candidateIDs(got), distances(got), got.Ambiguous, selectedID(got))
	}
	if got.Candidates[0].Distance < 0 {
		t.Errorf("distances = %v, want real distances so the tie is a tie and not an absence", distances(got))
	}
}

// A candidate with no review ref has no distance, and absence is not evidence of being the
// changeset in progress. Without the sentinel it would compete at distance zero and outrank a
// changeset with real review history on the same branch.
func TestResolveUnarchivedCandidateSortsLast(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("work")
	f.CommitChangeset("sent", "main")
	f.Commit("sent work", gittest.WithFile("sent.txt", "1\n"))
	reviewRef(f, "sent", f.Head())

	f.CommitChangeset("not-yet", "main")
	f.Commit("later work", gittest.WithFile("later.txt", "1\n"))

	got := resolveAt(t, f, "HEAD")
	if got.Selected == nil {
		t.Fatal("selected nothing, want the changeset that has a review ref")
	}
	if got.Selected.Changeset.Slug != "sent" {
		t.Errorf("selected %q at distances %v, want sent: the other has never been archived",
			got.Selected.Changeset.Slug, distances(got))
	}
}

// A directory under changesets/ with no CHANGESET.yaml is not a changeset. Scratch directories
// and whatever a squashed merge leaves behind must not become candidates the author has to
// explain away.
func TestResolveIgnoresADirectoryWithoutMetadata(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("work")
	f.Write("changesets/scratch/notes.txt", "not a changeset\n")
	f.CommitChangeset("work", "main")

	got := resolveAt(t, f, "HEAD")
	if selectedID(got) != "work" || len(got.Candidates) != 1 {
		t.Errorf("selected %q from %v, want only work", selectedID(got), candidateIDs(got))
	}
}

// A directory that has landed stays landed. Copying one out of the integration branch into a
// working tree — a conflict resolution, a `git checkout main -- changesets/x/` — must not make
// it work in progress on this branch, so the uncommitted additions are checked against trunk
// too.
func TestResolveWorktreeAdditionsAreStillCheckedAgainstTrunk(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("landed")
	f.CommitChangeset("landed", "main")
	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land it", "landed")

	f.CreateBranch("work")
	// Left uncommitted on purpose: it is the working-tree addition the test is about.
	f.Write("changesets/landed/CHANGESET.yaml", "id: landed\nbase: main\n")

	res, err := changeset.ResolveCurrent(context.Background(), repo(f), "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Selected != nil || len(res.Candidates) != 0 {
		t.Errorf("candidates %v selected %q, want nothing: the directory is on trunk", candidateIDs(res), selectedID(res))
	}
}

func distances(got changeset.Resolution) []int {
	out := make([]int, len(got.Candidates))
	for i, c := range got.Candidates {
		out[i] = c.Distance
	}
	return out
}

// A changeset directory whose recorded id is not its own directory name is a broken fixture,
// not a thing to guess about: the id and the directory are the same string by rule.
func TestResolveRefusesAMismatchedID(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.WriteChangesetFile("booking", "CHANGESET.yaml", "id: other\nbase: main\n")
	f.Commit("break it")

	repo := repo(f)
	db, err := changeset.DefaultBranch(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = changeset.Resolve(context.Background(), repo, "HEAD", db)
	if !errors.Is(err, changeset.ErrIDMismatch) {
		t.Fatalf("error = %v, want ErrIDMismatch", err)
	}
}

// The cost is a property of the implementation. Each of these refs is a changeset that landed
// and left its directory on trunk; a rule that asked a question per ref would scale with them,
// and this repository has 300 of them.
func TestResolveCostDoesNotGrowWithExistingChangesets(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	base := f.Head()
	for i := 0; i < 300; i++ {
		reviewRef(f, "cs-"+strings.Repeat("x", 1)+itoa(i), base)
	}
	f.CreateBranch("work")
	f.CommitChangeset("work", "main")
	f.Commit("work", gittest.WithFile("work.txt", "1\n"))

	repo, count := f.SpawnRepo(t)
	db, err := changeset.DefaultBranch(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	before := count()
	got, err := changeset.Resolve(context.Background(), repo, "HEAD", db)
	if err != nil {
		t.Fatal(err)
	}
	spawns := count() - before
	if selectedID(got) != "work" {
		t.Fatalf("selected %q, want work", selectedID(got))
	}
	// Two listings, one batch read, one ref listing, one distance, one terminal check, with
	// slack. The formulation this replaced measured 1,802 on the same shape.
	if spawns < 1 {
		t.Fatalf("Resolve cost %d invocations: the counter measured nothing, so the bound below proves nothing", spawns)
	}
	if spawns > 10 {
		t.Errorf("Resolve cost %d git invocations with 300 review refs present, want a handful", spawns)
	}
}

func itoa(n int) string {
	digits := []byte{}
	for {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		if n < 10 {
			break
		}
		n /= 10
	}
	return string(digits)
}

func TestDefaultBranchPrefersTheCaller(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("integration")

	repo := repo(f)
	got, err := changeset.DefaultBranch(context.Background(), repo, "integration")
	if err != nil {
		t.Fatal(err)
	}
	if got.Ref != "integration" || got.Source != changeset.DefaultBranchFlag {
		t.Errorf("got %+v, want the caller's ref flagged as such", got)
	}

	if _, err := changeset.DefaultBranch(context.Background(), repo, "no-such-ref"); err == nil {
		t.Error("an unresolvable override was accepted, want an error naming it")
	}
}

func TestDefaultBranchReadsWhatGitRecords(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	sha := f.Head()
	f.MustGit("update-ref", "refs/remotes/origin/trunk", sha)
	f.MustGit("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")

	got, err := changeset.DefaultBranch(context.Background(), repo(f), "")
	if err != nil {
		t.Fatal(err)
	}
	// The remote's own answer beats the local main sitting next to it, because what counts as
	// landed is what has been published.
	if got.Ref != "refs/remotes/origin/trunk" || got.Source != changeset.DefaultBranchRemoteHead {
		t.Errorf("got %+v, want the remote's default branch", got)
	}
}

func TestDefaultBranchFallsBackAndRefuses(t *testing.T) {
	t.Run("sole origin branch", func(t *testing.T) {
		f := gittest.New(t)
		f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
		f.MustGit("update-ref", "refs/remotes/origin/main", f.Head())
		f.CreateBranch("elsewhere")
		f.MustGit("branch", "-D", "main")

		got, err := changeset.DefaultBranch(context.Background(), repo(f), "")
		if err != nil {
			t.Fatal(err)
		}
		if got.Ref != "refs/remotes/origin/main" || got.Source != changeset.DefaultBranchSoleCandidate {
			t.Errorf("got %+v, want refs/remotes/origin/main", got)
		}
	})

	t.Run("two origin branches is a coin flip", func(t *testing.T) {
		f := gittest.New(t)
		f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
		f.MustGit("update-ref", "refs/remotes/origin/main", f.Head())
		f.MustGit("update-ref", "refs/remotes/origin/master", f.Head())

		if _, err := changeset.DefaultBranch(context.Background(), repo(f), ""); !errors.Is(err, changeset.ErrNoDefaultBranch) {
			t.Fatalf("error = %v, want ErrNoDefaultBranch: refusing beats guessing the trunk", err)
		}
	})

	t.Run("local main when there is no remote", func(t *testing.T) {
		f := gittest.New(t)
		f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
		got, err := changeset.DefaultBranch(context.Background(), repo(f), "")
		if err != nil {
			t.Fatal(err)
		}
		if got.Ref != "refs/heads/main" {
			t.Errorf("got %+v, want refs/heads/main", got)
		}
	})

	t.Run("nothing to compare against", func(t *testing.T) {
		f := gittest.New(t)
		f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
		f.CreateBranch("only-work")
		f.MustGit("branch", "-D", "main")

		_, err := changeset.DefaultBranch(context.Background(), repo(f), "")
		if !errors.Is(err, changeset.ErrNoDefaultBranch) {
			t.Fatalf("error = %v, want ErrNoDefaultBranch", err)
		}
		if !strings.Contains(err.Error(), "--default-branch") {
			t.Errorf("error %q should name the flag that fixes it", err)
		}
	})
}
