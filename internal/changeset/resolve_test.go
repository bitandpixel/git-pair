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

// Two changesets on one branch, and nothing in the metadata ordering them: the branch's own
// history decides, by the newest commit that touched each changeset's directory. This is the
// reading the archive-ref distance used to provide, and it is the only one that works in a fresh
// clone — where there are no refs to measure against, and never any more will be while the work is
// in flight.
func TestResolveOrdersTwoChangesetsByWhatTheBranchLastTouched(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))

	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("booking work", gittest.WithFile("booking.txt", "1\n"))
	// A second changeset claimed on the same branch, and then worked on. Neither names the other,
	// so only the branch's own history can say which is live.
	f.CommitChangeset("booking-tests", "main")
	f.Commit("test work", gittest.WithFile("booking_test.txt", "1\n"))

	if got := resolveAt(t, f, "HEAD"); selectedID(got) != "booking-tests" {
		t.Errorf("on the branch selected %q from %v, want booking-tests: its directory is what this branch touched last",
			selectedID(got), candidateIDs(got))
	}
	if got := resolveAt(t, f, "HEAD"); got.Selected.Distance >= got.Candidates[1].Distance {
		t.Errorf("distances %v do not put booking-tests first", distances(got))
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
	f.CommitChangeset("booking-tests", "booking")
	f.Commit("test work", gittest.WithFile("booking_test.txt", "1\n"))

	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land booking", "booking")

	f.SwitchTo("booking-tests")
	got := resolveAt(t, f, "HEAD")
	if selectedID(got) != "booking-tests" {
		t.Errorf("selected %q from %v, want booking-tests", selectedID(got), candidateIDs(got))
	}
}

// Resolution reads trees, so a repository with no git-pair refs at all still answers. That is the
// ordinary shape now rather than a degenerate one: nothing writes a ref until a changeset lands, so
// every branch in a young repository resolves with the namespace empty.
func TestResolveWithoutAnyRefs(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("work", gittest.WithFile("booking.txt", "1\n"))

	if refs := f.RefNames(reviewref.NamespaceRoot); len(refs) != 0 {
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

// The stack order comes from the metadata before it comes from any measurement: the candidate named
// as another's base is its parent, and a parent's directory sitting in a child's tree is not the work
// in hand.
func TestResolveOrdersAStackWithoutRefs(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("booking work", gittest.WithFile("booking.txt", "1\n"))
	f.CreateBranch("booking-tests")
	f.CommitChangeset("booking-tests", "booking")
	f.Commit("test work", gittest.WithFile("booking_test.txt", "1\n"))

	got := resolveAt(t, f, "HEAD")
	if got.Ambiguous || selectedID(got) != "booking-tests" {
		t.Errorf("selected %q from %v ambiguous=%v, want booking-tests from base alone", selectedID(got), candidateIDs(got), got.Ambiguous)
	}
}

func TestResolveDeletedDirectoryHasNoCandidate(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
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

// A base recorded as the parent's changeset id and one recorded as the branch carrying it mean the
// same parent, so dropping the parent from the candidate set must not depend on the spelling. A ref
// path is not a base spelling: it named a pointer that no longer exists while the work is in flight.
func TestResolveBaseSpelling(t *testing.T) {
	for _, base := range []string{"booking", "refs/heads/booking"} {
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

// The branch's own history decides when nothing else does. The stacked test above is decided by the
// base naming its parent, so this one deliberately stacks on trunk: with nothing in the metadata
// ordering the two, the distances have to, and the child — whose directory this branch created last —
// is the nearer.
func TestResolveBranchHistoryDecidesWhenNothingElseDoes(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("work")
	f.CommitChangeset("work", "main")
	f.Commit("work", gittest.WithFile("work.txt", "1\n"))

	f.CreateBranch("child")
	f.CommitChangeset("child", "main")
	f.Commit("child work", gittest.WithFile("child.txt", "1\n"))

	got := resolveAt(t, f, "HEAD")
	if got.Selected == nil {
		t.Fatal("selected nothing, want the nearer changeset")
	}
	if got.Selected.Changeset.Slug != "child" {
		t.Errorf("selected %q from %v, want child: it owns the directory this branch touched last",
			got.Selected.Changeset.Slug, candidateIDs(got))
	}
	if got.Selected.Distance >= got.Candidates[len(got.Candidates)-1].Distance {
		t.Errorf("candidates are not ordered near to far: %v/%v", candidateIDs(got), distances(got))
	}
}

// Two changeset directories arriving in one commit are the same distance from it, and nothing orders
// them. This is the tie the rule refuses rather than breaking: either answer silently picks a diff
// base, and the reader cannot see that a coin was flipped. A merge that brings two unlanded
// directories together is how a repository gets here.
func TestResolveEqualDistanceIsAmbiguous(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("both")
	f.Commit("claim two changesets in one commit", gittest.WithFiles(map[string]string{
		"changesets/one/CHANGESET.yaml": "id: one\nbase: main\n",
		"changesets/two/CHANGESET.yaml": "id: two\nbase: main\n",
	}))

	got := resolveAt(t, f, "HEAD")
	if !got.Ambiguous || got.Selected != nil {
		t.Fatalf("got %v at distances %v ambiguous=%v selected=%q, want an undecided tie",
			candidateIDs(got), distances(got), got.Ambiguous, selectedID(got))
	}
	for _, d := range distances(got) {
		if d != 0 {
			t.Errorf("distances = %v, want both at 0: the tie is a tie, not an ordering that failed", distances(got))
		}
	}
	// The refusal carries both names and the way out, because the reader has nothing else to go on.
	if err := changeset.AmbiguityError(got); !strings.Contains(err.Error(), "one and two") ||
		!strings.Contains(err.Error(), "--changeset") {
		t.Errorf("AmbiguityError = %q, want both ids and the flag that decides", err)
	}
}

// A directory with no commit on this line to measure from has no distance at all, and absence is not
// evidence of being the work in hand: it sorts after every real distance rather than competing at
// zero. The case that produces it is `init` — a scaffolded directory sitting uncommitted beside
// the changeset the branch has actually been working on — and the committed one wins.
func TestResolveWorktreeOnlyCandidateSortsLast(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("work")
	f.CommitChangeset("sent", "main")
	f.Commit("sent work", gittest.WithFile("sent.txt", "1\n"))
	// The scaffold: on disk, not in any commit.
	f.WriteChangesetFile("scaffold", "CHANGESET.yaml", "id: scaffold\nbase: main\n")

	repo := repo(f)
	got, err := changeset.ResolveCurrent(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("ResolveCurrent: %v", err)
	}
	if got.Selected == nil {
		t.Fatal("selected nothing, want the changeset this branch has worked on")
	}
	if got.Selected.Changeset.Slug != "sent" {
		t.Errorf("selected %q at distances %v, want sent: the other has no history on this line",
			got.Selected.Changeset.Slug, distances(got))
	}
	if got.Ambiguous {
		t.Errorf("ambiguous = true at distances %v, want the absence to lose rather than tie", distances(got))
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

// The cost is a property of the implementation. This repository holds three hundred changesets that
// landed — their directories are on trunk, and each has a record in the namespace — and a rule that
// asked a question per durable ref would scale with them.
func TestResolveCostDoesNotGrowWithExistingChangesets(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	files := map[string]string{}
	for i := 0; i < 300; i++ {
		id := "cs-" + itoa(i)
		files["changesets/"+id+"/CHANGESET.yaml"] = "id: " + id + "\nbase: main\n"
	}
	f.Commit("land three hundred changesets", gittest.WithFiles(files))
	landed := f.Head()
	for i := 0; i < 300; i++ {
		f.MustGit("update-ref", reviewref.Integration("cs-"+itoa(i)), landed)
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
	// One revision's tree listing, one batch read of its metadata, the trunk listing it compares
	// against, and the two calls that order more than one candidate — with slack. Nothing here is
	// charged per changeset that has ever existed: the formulation this replaced measured 1,802 git
	// invocations on this shape, because it asked one ancestry question per archive ref.
	if spawns < 1 {
		t.Fatalf("Resolve cost %d invocations: the counter measured nothing, so the bound below proves nothing", spawns)
	}
	if spawns > 10 {
		t.Errorf("Resolve cost %d git invocations with 300 landed changesets and 300 records present, want a handful", spawns)
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

// TestDefaultBranchIgnoresADanglingRemoteHead pins the case that makes the listing non-trivial.
// `git remote set-head` records a symbolic ref, and deleting the branch it points at leaves the
// pointer behind. Returning it would name an integration branch that resolves to no commit, so
// the search has to fall through to what does resolve.
func TestDefaultBranchIgnoresADanglingRemoteHead(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.MustGit("update-ref", "refs/remotes/origin/main", f.Head())
	f.MustGit("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")

	got, err := changeset.DefaultBranch(context.Background(), repo(f), "")
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got.Ref != "refs/remotes/origin/main" || got.Source != changeset.DefaultBranchSoleCandidate {
		t.Errorf("got %+v, want refs/remotes/origin/main", got)
	}
}

// TestParentOfTreatsEverySpellingOfTrunkAsTrunk is the comparison behind a refusal. `base:` is written as a
// name and `DefaultBranch` answers with a ref, and in a clone that has fetched and not branched off trunk
// the two spellings of one branch differ: `main`, and `refs/remotes/origin/main`. Read as two branches,
// every changeset in such a clone is stacked on a branch called main — which `status` prints as a parent
// line, and which `change integrate` refuses over, because an unlanded parent is a refusal and trunk never
// has an integration record. A real stack has to stay a stack, or the refusal loses its meaning.
func TestParentOfTreatsEverySpellingOfTrunkAsTrunk(t *testing.T) {
	ctx := context.Background()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("feature/booking")
	f.MustGit("update-ref", "refs/remotes/origin/main", f.Head())
	f.MustGit("branch", "-D", "main")

	repo := repo(f)
	db, err := changeset.DefaultBranch(ctx, repo, "")
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if db.Ref != "refs/remotes/origin/main" {
		t.Fatalf("db.Ref = %q, want the fetch root: the case under test is a trunk with no local branch", db.Ref)
	}
	for _, name := range []string{"main", "refs/heads/main", "refs/remotes/origin/main"} {
		if !db.IsBranch(name) {
			t.Errorf("IsBranch(%q) = false, want true: the same branch read through another root", name)
		}
	}
	if db.IsBranch("feature/booking") {
		t.Error("IsBranch reported a feature branch as the integration branch")
	}

	for _, base := range []string{"main", "refs/heads/main", "refs/remotes/origin/main"} {
		p, err := changeset.ParentOf(ctx, repo,
			changeset.Changeset{Slug: "booking", Branch: "feature/booking", Base: base}, db)
		if err != nil {
			t.Fatalf("ParentOf(base %q): %v", base, err)
		}
		if p.Branch != "" {
			t.Errorf("base %q read as stacked on %q, want unstacked", base, p.Branch)
		}
		// The `parent:` key spells the same branch the same way, and answering "stacked on trunk" there is
		// what makes `init --parent main` look like a stack.
		p, err = changeset.ParentOf(ctx, repo,
			changeset.Changeset{Slug: "booking", Branch: "feature/booking", Base: base, ParentBranch: "main"}, db)
		if err != nil {
			t.Fatalf("ParentOf(parent %q): %v", base, err)
		}
		if p.Branch != "" {
			t.Errorf("parent %q read as a stack on %q, want no parent", base, p.Branch)
		}
	}

	p, err := changeset.ParentOf(ctx, repo,
		changeset.Changeset{Slug: "child", Branch: "feature/child", Base: "feature/booking"}, db)
	if err != nil {
		t.Fatalf("ParentOf(a real parent): %v", err)
	}
	if p.Branch != "feature/booking" || p.Tip == "" {
		t.Errorf("a stack on a feature branch read as %+v, want the branch and its tip", p)
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
