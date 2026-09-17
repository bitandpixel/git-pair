package gittest_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitpair/internal/gittest"
)

// The fixture is test infrastructure: if it silently inherits the developer's
// git config, every integration test below becomes unreliable. These tests check
// the properties the rest of the suite depends on.

func TestFixtureStartsOnMainWithNoCommits(t *testing.T) {
	f := gittest.New(t)

	if got := f.CurrentBranch(); got != "main" {
		t.Errorf("CurrentBranch() = %q, want %q", got, "main")
	}
	if _, err := f.Git("rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		t.Error("new fixture has a commit; tests need to choose the root commit")
	}
	if !f.Clean() {
		t.Error("new fixture is not clean")
	}
}

func TestFixtureSetsIdentityLocally(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))

	author := strings.TrimSpace(f.MustGit("log", "-1", "--format=%an <%ae>"))
	want := gittest.AuthorName + " <" + gittest.AuthorEmail + ">"
	if author != want {
		t.Errorf("commit author = %q, want %q", author, want)
	}
	if got := f.MustGit("config", "--local", "commit.gpgsign"); strings.TrimSpace(got) != "false" {
		t.Errorf("local commit.gpgsign = %q, want false", got)
	}
}

// TestFixtureIgnoresGlobalConfig proves the isolation that keeps the suite from
// depending on the developer's machine: a global config that would normally
// rewrite history (user identity, commit hooks via alias) must have no effect.
func TestFixtureIgnoresGlobalConfig(t *testing.T) {
	f := gittest.New(t)

	global := os.Getenv("GIT_CONFIG_GLOBAL")
	if global == "" {
		t.Fatal("GIT_CONFIG_GLOBAL is not set; the fixture is not isolating config")
	}
	if home := os.Getenv("HOME"); home == "" || home == filepath.Dir(global) {
		t.Errorf("HOME = %q, want a private directory", home)
	}
	// A global config written after New must not be read by fixture git calls.
	if err := os.WriteFile(global, []byte("[user]\n\tname = Global Override\n\temail = override@example.invalid\n"), 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}
	sha := f.Commit("seed", gittest.WithFile("a.txt", "a\n"))

	if got := f.Trailers(sha); len(got) != 0 {
		t.Errorf("unexpected trailers on a plain commit: %v", got)
	}
	if author := f.MustGit("log", "-1", "--format=%an"); strings.TrimSpace(author) != gittest.AuthorName {
		t.Errorf("commit author = %q, want the fixture identity, not the global config", author)
	}
}

func TestFixtureCommitHelpers(t *testing.T) {
	f := gittest.New(t)

	seed := f.Commit("seed", gittest.WithFiles(map[string]string{
		"a.txt":        "a\n",
		"nested/b.txt": "b\n",
	}))
	if got := f.FileAt(seed, "nested/b.txt"); got != "b\n" {
		t.Errorf("nested/b.txt at seed = %q", got)
	}
	if got := f.ParentCount(seed); got != 0 {
		t.Errorf("root commit parent count = %d, want 0", got)
	}

	empty := f.EmptyCommit("empty")
	if got := f.ChangedFiles(seed, empty); len(got) != 0 {
		t.Errorf("empty commit changed %v, want no files", got)
	}
	if f.Head() != empty {
		t.Errorf("Head() = %s, want %s", f.Head(), empty)
	}

	withChange := f.Commit("change", gittest.WithFile("a.txt", "a\nb\n"))
	if got := f.ChangedFiles(empty, withChange); len(got) != 1 || got[0] != "a.txt" {
		t.Errorf("ChangedFiles = %v, want [a.txt]", got)
	}
}

func TestFixtureBranchHelpers(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))

	f.CreateBranch("feature/two")
	if got := f.CurrentBranch(); got != "feature/two" {
		t.Fatalf("CurrentBranch() = %q after CreateBranch", got)
	}
	f.Commit("on feature", gittest.WithFile("b.txt", "b\n"))

	f.CreateBranch("stacked", "main")
	if got := f.CurrentBranch(); got != "stacked" {
		t.Fatalf("CurrentBranch() = %q after CreateBranch from main", got)
	}
	if f.HasFile(f.Head(), "b.txt") {
		t.Error("branch created from main can see a commit made on feature/two")
	}

	f.SwitchTo("feature/two")
	if got := f.CurrentBranch(); got != "feature/two" {
		t.Errorf("CurrentBranch() = %q after SwitchTo", got)
	}
	f.Detach()
	if got := f.CurrentBranch(); got != "" {
		t.Errorf("CurrentBranch() = %q on a detached HEAD, want \"\"", got)
	}
	f.ForceDeleteBranch("stacked")
	for _, b := range f.Branches() {
		if b == "stacked" {
			t.Error("ForceDeleteBranch left the branch in place")
		}
	}
}

func TestFixtureTrailersAndMarkers(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))

	ready := f.CommitReadyMarker("booking")
	if got := f.Subject(ready); got != "git-pair: ready booking" {
		t.Errorf("ready subject = %q", got)
	}
	if got := f.Trailers(ready)["Review-State"]; got != "ready" {
		t.Errorf("ready marker Review-State = %q, want ready", got)
	}
	review := f.CommitReviewMarker("booking", "approve")
	trailers := f.Trailers(review)
	if trailers["Review-Outcome"] != "approve" || trailers["Review-Changeset"] != "booking" {
		t.Errorf("review trailers = %v", trailers)
	}
	if got := f.Subject(review); got != "review: approve booking" {
		t.Errorf("review subject = %q", got)
	}
}

func TestFixtureRefsAndReachability(t *testing.T) {
	f := gittest.New(t)
	seed := f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.MustGit("update-ref", "refs/reviews/booking", seed)

	if !f.HasRef("refs/reviews/booking") {
		t.Fatal("ref created by update-ref is not visible")
	}
	if got := f.RefSHA("refs/reviews/booking"); got != seed {
		t.Errorf("RefSHA = %s, want %s", got, seed)
	}
	if got := f.RefNames("refs/reviews"); len(got) != 1 || got[0] != "refs/reviews/booking" {
		t.Errorf("RefNames = %v", got)
	}
	if !f.ReachableFrom(seed, seed) {
		t.Error("a commit should be reachable from itself")
	}
	if got := f.RevListCount(seed); got != 1 {
		t.Errorf("RevListCount(seed) = %d, want 1", got)
	}
}

func TestFixtureFixedCommitDate(t *testing.T) {
	f := gittest.New(t)
	when := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	sha := f.Commit("dated", gittest.WithFile("a.txt", "a\n"), gittest.WithDate(when))

	got := strings.TrimSpace(f.MustGit("log", "-1", "--format=%ct", sha))
	if want := strconv.FormatInt(when.Unix(), 10); got != want {
		t.Errorf("commit committer date = %s, want %s (the requested fixed date)", got, want)
	}
}

func TestFixtureWorkingTreeHelpers(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "one\n"))

	f.Append("a.txt", "two\n")
	f.Write("new/c.txt", "c\n")
	if !strings.Contains(f.Read("a.txt"), "two") {
		t.Errorf("Append did not extend a.txt: %q", f.Read("a.txt"))
	}
	if f.Clean() {
		t.Error("fixture reports a clean tree with unstaged changes")
	}

	f.Rename("new/c.txt", "moved/c.txt")
	f.Remove("new")
	if f.HasWorktreeFile("new/c.txt") {
		t.Error("Remove left new/c.txt behind")
	}
	if _, err := os.Stat(filepath.Join(f.Dir(), "moved", "c.txt")); err != nil {
		t.Errorf("Rename did not move the file: %v", err)
	}
}

func TestFixtureChangesetHelpers(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))

	f.StageChangeset("booking", "main")
	if got := f.MetadataBase("booking"); got != "main" {
		t.Errorf("MetadataBase = %q, want main", got)
	}
	if !f.HasWorktreeFile(f.ChangesetPath("booking", "ABOUT.md")) {
		t.Error("StageChangeset did not write ABOUT.md")
	}
	sha := f.Commit("impl", gittest.WithFile("src/service.go", "package service\n"))
	if !f.HasFile(sha, f.ChangesetPath("booking", "CHANGESET.yaml")) {
		t.Error("StageChangeset files were not committed by the next commit")
	}

	f.WriteChangesetFile("booking", "concurrency-tests.md", "# Concurrency tests\n\nQuestion.\n")
	if got := f.Threads("booking"); len(got) != 1 || got[0] != "concurrency-tests.md" {
		t.Errorf("Threads = %v, want [concurrency-tests.md]", got)
	}
}

func TestFixtureInstallHookCanMakeGitFail(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))

	hook := f.InstallHook("pre-commit", "#!/bin/sh\nexit 9\n")
	defer f.RemoveHook(hook)

	if _, err := f.Git("commit", "--allow-empty", "-m", "should fail"); err == nil {
		t.Error("commit succeeded despite a failing pre-commit hook")
	}
}
