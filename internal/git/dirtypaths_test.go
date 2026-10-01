package git_test

import (
	"context"
	"testing"

	"gitpair/internal/gittest"
)

// DirtyPaths is the one call behind the TUI's "this file has an uncommitted change" mark, so it is
// pinned here against real git output rather than against a hand-written record: the format has a
// wrinkle in it — a rename or a copy record is followed by a second NUL-separated field that is the
// path the file came from and has no status of its own — and reading that field as its own record is
// how a path silently disappears from the answer.

// TestDirtyPathsReportsEveryWayAPathCanBeUncommitted covers the three states that make a path dirty —
// staged, unstaged, untracked — the two paths of a staged rename, and a name git would otherwise quote.
func TestDirtyPathsReportsEveryWayAPathCanBeUncommitted(t *testing.T) {
	f, repo := openFixture(t)
	f.Commit("more", gittest.WithFiles(map[string]string{
		"src/edited.txt":    "edited\n",
		"src/staged.txt":    "staged\n",
		"src/moved.txt":     "moved\n",
		"src/untouched.txt": "untouched\n",
	}))
	ctx := context.Background()

	// A tree with nothing in it but commits has no answer to give, and says so with an empty set
	// rather than a nil one the caller has to guard.
	got, err := repo.DirtyPaths(ctx)
	if err != nil {
		t.Fatalf("DirtyPaths on a clean tree: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("clean tree reports %v, want nothing", keys(got))
	}

	f.Write("src/edited.txt", "edited\nwritten in the editor\n") // unstaged
	f.Write("src/staged.txt", "staged\nwritten and staged\n")    // staged
	f.MustGit("add", "src/staged.txt")
	f.Rename("src/moved.txt", "src/moved-into.txt") // a rename, once it is staged
	f.MustGit("add", "-A")
	f.Write("src/untracked.txt", "new file\n")
	f.Write("src/épaulé.txt", "non-ascii name\n") // untracked, and quoted by git by default

	got, err = repo.DirtyPaths(ctx)
	if err != nil {
		t.Fatalf("DirtyPaths: %v", err)
	}
	for _, want := range []string{
		"src/edited.txt",     // unstaged
		"src/staged.txt",     // staged
		"src/moved.txt",      // the path the rename left
		"src/moved-into.txt", // the path the rename arrived at
		"src/untracked.txt",
		"src/épaulé.txt",
	} {
		if !got[want] {
			t.Errorf("%s is not reported dirty; the answer is %v", want, keys(got))
		}
	}
	if got["src/untouched.txt"] {
		t.Errorf("src/untouched.txt is reported dirty; nothing changed there: %v", keys(got))
	}
	if len(got) != 6 {
		t.Errorf("DirtyPaths reported %d paths, want 6: %v", len(got), keys(got))
	}
}

// TestDirtyPathsKeepsPathsRelativeToTheRepository is the property the TUI compares against: a path out
// of `git status` has to be the same string as the path out of `git diff --name-status`, whatever
// directory the caller stands in and whatever `status.relativePaths` says.
func TestDirtyPathsKeepsPathsRelativeToTheRepository(t *testing.T) {
	f, repo := openFixture(t)
	f.Commit("nested", gittest.WithFile("deep/down/inside/file.txt", "1\n"))
	f.Write("deep/down/inside/file.txt", "2\n")
	f.Config("status.relativePaths", "true")

	got, err := repo.DirtyPaths(context.Background())
	if err != nil {
		t.Fatalf("DirtyPaths: %v", err)
	}
	if !got["deep/down/inside/file.txt"] {
		t.Errorf("the change is not reported at the repository-relative path; the answer is %v", keys(got))
	}
}
