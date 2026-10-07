package git_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gitpair/internal/git"
	"gitpair/internal/gittest"
)

// `git rev-parse --verify --quiet <rev>` reports an unresolvable revision by exiting 1
// with *no stderr at all*. That is why git.Error carries an exit code: a caller that
// only reads the message cannot tell "this ref does not exist" from "git failed", and
// would report an absent ref as a repository error.
//
// These tests pin that distinction at the layer that has to get it right, because every
// consumer above it — a base that does not resolve, `status` before the
// first review — depends on reading absence as absence.

// openFixture returns a fixture repository with one commit, plus a git.Repo on the same
// repository with the fixture's isolated environment.
func openFixture(t *testing.T) (*gittest.Fixture, *git.Repo) {
	t.Helper()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "1\n"))
	repo, err := git.Open(f.Dir())
	if err != nil {
		t.Fatalf("git.Open(%s): %v", f.Dir(), err)
	}
	repo.Env = f.Env()
	return f, repo
}

// TestUnknownRefReportsExit1WithNoStderr is the raw behaviour git.ExitCode exists for.
func TestUnknownRefReportsExit1WithNoStderr(t *testing.T) {
	_, repo := openFixture(t)
	ctx := context.Background()

	stdout, err := repo.Git(ctx, "rev-parse", "--verify", "--quiet", "refs/git-pair/changesets/absent/archive")
	if err == nil {
		t.Fatalf("rev-parse --verify --quiet on an unknown ref succeeded with stdout %q", stdout)
	}
	var ge *git.Error
	if !errors.As(err, &ge) {
		t.Fatalf("error is %T (%v), want *git.Error", err, err)
	}
	if ge.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", ge.ExitCode)
	}
	if ge.Stderr != "" {
		t.Errorf("stderr = %q, want empty: --quiet makes the exit code the only signal", ge.Stderr)
	}
	if git.ExitCode(err) != 1 {
		t.Errorf("git.ExitCode = %d, want 1", git.ExitCode(err))
	}
	// The message-based check alone cannot see this case, which is exactly why callers
	// must consult the exit code as well.
	if git.IsUnknownRevision(err) {
		t.Error("IsUnknownRevision matched an error with no stderr; the exit code is the signal here")
	}
}

// TestRevParseMapsUnknownRefToSentinel asserts the wrapper callers actually use.
func TestRevParseMapsUnknownRefToSentinel(t *testing.T) {
	_, repo := openFixture(t)
	ctx := context.Background()

	sha, err := repo.RevParse(ctx, "refs/git-pair/changesets/absent/archive")
	if sha != "" {
		t.Errorf("sha = %q, want empty", sha)
	}
	if !errors.Is(err, git.ErrUnknownRevision) {
		t.Fatalf("error = %v, want it to wrap git.ErrUnknownRevision", err)
	}
	// errors.Is is the contract RevParse documents. The wrapper builds a new error rather
	// than wrapping *git.Error, so git.ExitCode and git.IsUnknownRevision answer for
	// errors from Git itself, not for wrapped ones; callers must branch on the sentinel.

	// Same for a revision that is unresolvable because it is out of range, and for a
	// ref-shaped name that was never created.
	for _, rev := range []string{"HEAD~99", "refs/git-pair/changesets/nope/archive", "nope"} {
		if _, err := repo.RevParse(ctx, rev); !errors.Is(err, git.ErrUnknownRevision) {
			t.Errorf("RevParse(%q) = %v, want ErrUnknownRevision", rev, err)
		}
	}

	// A resolvable revision is unaffected.
	if _, err := repo.RevParse(ctx, "HEAD"); err != nil {
		t.Errorf("RevParse(HEAD) = %v, want no error", err)
	}
}

// TestUnknownRefIsAbsenceForEveryReader covers the three reads that must treat an
// unknown ref as "not there" rather than as a failure.
func TestUnknownRefIsAbsenceForEveryReader(t *testing.T) {
	f, repo := openFixture(t)
	ctx := context.Background()
	const ref = "refs/heads/nope-not-here"

	if _, err := repo.ResolveRef(ctx, ref); !errors.Is(err, git.ErrUnknownRevision) {
		t.Errorf("ResolveRef on an absent ref = %v, want ErrUnknownRevision", err)
	}

	// A missing path at a real revision is absence too.
	if !repo.PathExistsAt(ctx, "HEAD", "a.txt") {
		t.Error("PathExistsAt(a.txt) = false, want true")
	}
	if repo.PathExistsAt(ctx, "HEAD", "missing.txt") {
		t.Error("PathExistsAt(missing.txt) = true, want false")
	}

	// `symbolic-ref --quiet` fails the same way on a detached HEAD, and the wrapper must
	// read it as "no branch" rather than as an error.
	f.Detach()
	branch, err := repo.CurrentBranch(ctx)
	if err != nil {
		t.Fatalf("CurrentBranch on a detached HEAD = %v, want no error", err)
	}
	if branch != "" {
		t.Errorf("CurrentBranch = %q, want empty on a detached HEAD", branch)
	}
	f.SwitchTo("main")
	if branch, err := repo.CurrentBranch(ctx); err != nil || branch != "main" {
		t.Errorf("CurrentBranch = %q (%v), want main", branch, err)
	}
}

// TestRealGitFailureStaysARealFailure is the other side of the rule: exit 1 with no
// stderr is absence, but a genuine git failure must keep a distinguishable error so the
// CLI can report it as a git failure (exit 3) instead of an empty result.
func TestRealGitFailureStaysARealFailure(t *testing.T) {
	_, repo := openFixture(t)
	ctx := context.Background()

	_, err := repo.Git(ctx, "not-a-git-command")
	if err == nil {
		t.Fatal("running a non-existent git subcommand succeeded")
	}
	if code := git.ExitCode(err); code == 0 {
		t.Errorf("git.ExitCode = 0, want the git subprocess status")
	}
	if errors.Is(err, git.ErrUnknownRevision) || git.IsUnknownRevision(err) {
		t.Errorf("%v was classified as an unknown revision; it is a real git failure", err)
	}

	// A not-a-repository directory is its own sentinel, not a git failure.
	if _, err := git.Open(t.TempDir()); !errors.Is(err, git.ErrNotRepository) {
		t.Errorf("Open on a non-repository = %v, want ErrNotRepository", err)
	}
}

// The span picker lists commits and refs, and both have to come back in the form a
// checkpoint needs: a full id to pin, and for a tag the commit behind the tag object.
func TestRecentCommitsAndRefTipsCarryWhatAPickerNeeds(t *testing.T) {
	f, repo := openFixture(t)
	second := f.Commit("second thing", gittest.WithFile("b.txt", "2\n"))
	f.MustGit("branch", "feature/x", second)
	f.MustGit("tag", "-a", "v1", "-m", "annotated", second)

	tips, err := repo.RecentCommits(context.Background(), 10, "HEAD")
	if err != nil {
		t.Fatalf("RecentCommits: %v", err)
	}
	if len(tips) != 2 {
		t.Fatalf("RecentCommits returned %d commits, want 2", len(tips))
	}
	if tips[0].SHA != second {
		t.Errorf("newest is %s, want %s: the picker lists history newest first", tips[0].SHA, second)
	}
	if tips[0].Subject != "second thing" || tips[0].Short != second[:7] || tips[0].When.IsZero() {
		t.Errorf("newest entry = %+v, want sha, short sha, subject and a date", tips[0])
	}

	// The limit is a window, not a rule, and it is the window that gets respected.
	one, err := repo.RecentCommits(context.Background(), 1, "HEAD")
	if err != nil || len(one) != 1 {
		t.Errorf("RecentCommits(limit 1) = %d commits (%v), want 1", len(one), err)
	}

	refs, err := repo.RefTips(context.Background(), "refs")
	if err != nil {
		t.Fatalf("RefTips: %v", err)
	}
	pointing := map[string]string{}
	for _, r := range refs {
		pointing[r.Name] = r.Commit
	}
	if pointing["refs/heads/feature/x"] != second {
		t.Errorf("refs/heads/feature/x = %s, want %s", pointing["refs/heads/feature/x"], second)
	}
	tagged := strings.TrimSpace(f.MustGit("rev-parse", "v1^{commit}"))
	if pointing["refs/tags/v1"] != tagged {
		t.Errorf("refs/tags/v1 = %s, want the commit it tags (%s): a reviewer picking a tag means the code",
			pointing["refs/tags/v1"], tagged)
	}
	if asObject := strings.TrimSpace(f.MustGit("rev-parse", "v1")); asObject != tagged && pointing["refs/tags/v1"] == asObject {
		t.Error("RefTip carried the tag object rather than the commit")
	}
}

// The `V` columns interleave a changeset's own commits with its review submissions, and they order
// them by the walk rather than by the dates, because a rebase leaves every commit it replayed
// carrying the same committer date. So the walk has to name each commit's place in the order it
// happened and say which commits changed files: a marker holds no content to review and is already
// listed by alias, yet it still sits between the rows a reviewer reads, so it has to be counted. A
// merge is kept, because the first-parent diff reports the files it brought in -- that merge is where
// the changeset caught up with something, and a reviewer needs the row to put what arrived outside
// the span.
func TestRangeCommitsGiveTheRangeAnOrderDatesCannot(t *testing.T) {
	f, repo := openFixture(t)
	base, one, marker, two, side, merge := rebasedStack(t, f)

	walk, err := repo.RangeCommits(context.Background(), base+"..HEAD")
	if err != nil {
		t.Fatalf("RangeCommits: %v", err)
	}
	bySHA := map[string]git.RangeCommit{}
	for i, c := range walk {
		bySHA[c.SHA] = c
		if c.Position != i {
			t.Errorf("%s carries position %d at row %d: the positions are the walk, oldest first, and the "+
				"callers compare them with each other", c.SHA, c.Position, i)
		}
		if c.Short != c.SHA[:7] || c.Subject == "" || c.When.IsZero() {
			t.Errorf("entry = %+v, want a short id, a subject and a date beside the sha", c)
		}
	}
	// The whole range, markers included: the window is the caller's, and a position is worth nothing
	// if the commits a caller leaves out of its rows stop taking up room in the order.
	if len(walk) != 5 {
		t.Errorf("walk = %d commits, want the five of the range including the empty marker", len(walk))
	}
	if _, ok := bySHA[base]; ok {
		t.Errorf("%s is in the walk: the base already holds it, so it is not this changeset's own history", base)
	}

	// Which commit came first is what the dates can no longer answer, so it is what the walk has to.
	// git walks a commit after its parents; siblings are git's to order, so only these pairs are named.
	for _, pair := range [][2]string{{one, marker}, {marker, two}, {one, side}, {side, merge}, {two, merge}} {
		before, after := bySHA[pair[0]], bySHA[pair[1]]
		if before.SHA == "" || after.SHA == "" {
			t.Fatalf("the walk is missing %q or %q: %v", pair[0], pair[1], walk)
		}
		if before.Position >= after.Position {
			t.Errorf("%s reads as position %d and its child %s as %d: the columns merge by this order, so a "+
				"commit has to come out below the one it follows", pair[0], before.Position, pair[1], after.Position)
		}
	}

	if c, ok := bySHA[marker]; !ok || c.ChangesFiles {
		t.Errorf("the empty marker = %+v, want a commit that changes no file: it holds no content to "+
			"review and the row a reviewer uses for it is the alias", bySHA[marker])
	}
	for _, sha := range []string{one, two, side, merge} {
		if !bySHA[sha].ChangesFiles {
			t.Errorf("%s reads as changing no file", sha)
		}
	}
}

// rebasedStack builds the history both walks above are asked about: two commits on main with an empty
// marker between them, a side branch off the first, and a merge bringing it back. Every commit carries
// one committer date and an author date of its own, because that is what `git rebase` leaves behind:
// it replays each commit it touches, so the minute of the rebase becomes the committer date of all of
// them and the days the work was written survive only in the author dates. git compares dates at
// one-second granularity, so with the committer dates tied nothing in the dates says which commit came
// first -- which is the case the walks have to answer from the history itself.
func rebasedStack(t *testing.T, f *gittest.Fixture) (base, one, marker, two, side, merge string) {
	t.Helper()
	rebased := time.Date(2026, 9, 9, 9, 0, 0, 0, time.UTC)
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	stamped := func(opts ...gittest.CommitOpt) []gittest.CommitOpt {
		day = day.Add(24 * time.Hour)
		return append(opts, gittest.WithAuthorDate(day), gittest.WithCommitterDate(rebased))
	}
	base = f.Head()
	one = f.Commit("first work", stamped(gittest.WithFile("b.txt", "2\n"))...)
	marker = f.EmptyCommit("review: approve something", stamped()...)
	two = f.Commit("second work", stamped(gittest.WithFile("c.txt", "3\n"))...)
	f.CreateBranch("side", one)
	f.SwitchTo("side")
	side = f.Commit("side work", stamped(gittest.WithFile("d.txt", "4\n"))...)
	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "-m", "merge side", "side")
	merge = f.Head()
	return base, one, marker, two, side, merge
}

// `lifecycle.derive` reads the last record of the walk as the newest marker, so the walk has to keep a
// commit below the commit it marks even when their dates tie -- and a rebased stack ties all at once.
// git's default order promises only reverse chronology, so it is not the order this reads as; the
// walk asks for it topologically.
func TestLogFieldsKeepsAMarkerBelowTheCommitItFollows(t *testing.T) {
	f, repo := openFixture(t)
	base, one, marker, two, side, merge := rebasedStack(t, f)

	records, err := repo.LogFields(context.Background(), base+"..HEAD", "%H")
	if err != nil {
		t.Fatalf("LogFields: %v", err)
	}
	at := map[string]int{}
	for i, rec := range records {
		at[rec[0]] = i
	}
	for _, pair := range [][2]string{{one, marker}, {marker, two}, {one, side}, {side, merge}, {two, merge}} {
		before, after := pair[0], pair[1]
		if _, ok := at[before]; !ok {
			t.Fatalf("%s is missing from the walk: %v", before, records)
		}
		if _, ok := at[after]; !ok {
			t.Fatalf("%s is missing from the walk: %v", after, records)
		}
		if at[before] >= at[after] {
			t.Errorf("%s reads as record %d and its child %s as %d: the last record of this walk is the "+
				"newest marker, so a commit has to come after the one it follows even when their dates "+
				"are the same second", before, at[before], after, at[after])
		}
	}
}

// `cat-file --batch` frames many objects into one stream: `<oid> <type> <size>`, content,
// then the next object. Parsing that by length rather than by scanning for a terminator is
// the difference between reading a metadata file and reading a metadata file plus whatever
// the next object happened to start with, so the framing cases are pinned here.
func TestCatFileBlobsReadsManyObjectsInOneCall(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFiles(map[string]string{
		"one.txt":        "single line\n",
		"two.txt":        "id: booking\nbase: main\nbranch: booking\n",
		"empty.txt":      "",
		"twin-a.txt":     "same content\n",
		"twin-b.txt":     "same content\n",
		"no-newline.txt": "ends without a newline",
	}))
	repo := &git.Repo{Dir: f.Dir()}
	ctx := context.Background()

	entries, err := repo.TreeEntries(ctx, "main", "")
	if err != nil {
		t.Fatalf("TreeEntries: %v", err)
	}
	oids := map[string]string{}
	for _, e := range entries {
		oids[e.Path] = e.OID
	}
	if len(oids) != 6 {
		t.Fatalf("TreeEntries found %d files, want 6: %v", len(oids), oids)
	}
	if oids["twin-a.txt"] != oids["twin-b.txt"] {
		t.Fatal("identical contents should share one object; the batch dedupe depends on it")
	}

	// Duplicates asked for twice, and an oid that does not exist.
	requested := []string{
		oids["one.txt"], oids["two.txt"], oids["empty.txt"],
		oids["twin-a.txt"], oids["twin-a.txt"], oids["no-newline.txt"],
		"0000000000000000000000000000000000000000",
	}
	blobs, err := repo.CatFileBlobs(ctx, requested)
	if err != nil {
		t.Fatalf("CatFileBlobs: %v", err)
	}
	for path, want := range map[string]string{
		"one.txt":        "single line\n",
		"two.txt":        "id: booking\nbase: main\nbranch: booking\n",
		"empty.txt":      "",
		"twin-a.txt":     "same content\n",
		"no-newline.txt": "ends without a newline",
	} {
		if got := blobs[oids[path]]; got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if _, ok := blobs["0000000000000000000000000000000000000000"]; ok {
		t.Error("a missing object should be absent, not an entry and not an error")
	}
}

// An empty request must not spawn git at all: a scan that finds no changeset directories is
// the common case, not an edge worth paying a process for.
func TestCatFileBlobsOnNothingAsksNothing(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	repo := &git.Repo{Dir: f.Dir()}

	blobs, err := repo.CatFileBlobs(context.Background(), nil)
	if err != nil || len(blobs) != 0 {
		t.Errorf("CatFileBlobs(nil) = %v, %v; want an empty result", blobs, err)
	}
	blobs, err = repo.CatFileBlobs(context.Background(), []string{"", ""})
	if err != nil || len(blobs) != 0 {
		t.Errorf("CatFileBlobs([\"\"]) = %v, %v; want an empty result", blobs, err)
	}
}
