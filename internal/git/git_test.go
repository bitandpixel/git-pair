package git_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gitpr/internal/git"
	"gitpr/internal/gittest"
)

// `git rev-parse --verify --quiet <rev>` reports an unresolvable revision by exiting 1
// with *no stderr at all*. That is why git.Error carries an exit code: a caller that
// only reads the message cannot tell "this ref does not exist" from "git failed", and
// would report a missing review ref as a repository error.
//
// These tests pin that distinction at the layer that has to get it right, because every
// consumer above it — CreateRefIfAbsent, the review-ref lookup, `status` before the
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

	stdout, err := repo.Git(ctx, "rev-parse", "--verify", "--quiet", "refs/reviews/absent")
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

	sha, err := repo.RevParse(ctx, "refs/reviews/absent")
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
	for _, rev := range []string{"HEAD~99", "refs/reviews/archive/nope/abc1234", "nope"} {
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
	head := f.Head()
	const ref = "refs/reviews/booking-transaction"

	if _, err := repo.ResolveRef(ctx, ref); !errors.Is(err, git.ErrUnknownRevision) {
		t.Errorf("ResolveRef on an absent ref = %v, want ErrUnknownRevision", err)
	}

	// CreateRefIfAbsent must be able to tell "absent" from "git broke", because on the
	// latter it must not write.
	created, err := repo.CreateRefIfAbsent(ctx, ref, head)
	if err != nil {
		t.Fatalf("CreateRefIfAbsent = %v, want no error for an absent ref", err)
	}
	if !created {
		t.Error("CreateRefIfAbsent reported no creation for a ref that did not exist")
	}
	got, err := repo.ResolveRef(ctx, ref)
	if err != nil || got != head {
		t.Errorf("%s = %q (%v), want %s", ref, got, err, head)
	}

	// The second call must observe the existing ref and leave it alone.
	f.Commit("second", gittest.WithFile("b.txt", "2\n"))
	created, err = repo.CreateRefIfAbsent(ctx, ref, f.Head())
	if err != nil {
		t.Fatalf("second CreateRefIfAbsent = %v", err)
	}
	if created {
		t.Error("CreateRefIfAbsent reported creating a ref that already exists")
	}
	if got, err := repo.ResolveRef(ctx, ref); err != nil || got != head {
		t.Errorf("%s moved from %s to %q (%v)", ref, head, got, err)
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
