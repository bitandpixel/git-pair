package git_test

import (
	"context"
	"testing"

	"gitpair/internal/gittest"
)

// DirtyPaths and UntrackedPaths are the two calls behind the TUI's marks on the working tree, so they are
// pinned here against real git output rather than against a hand-written record. The status format has a
// wrinkle in it — a rename or a copy record is followed by a second NUL-separated field that is the
// path the file came from and has no status of its own — and reading that field as its own record is
// how a path silently disappears from the answer. What both answers must agree on is being the same string
// `diff --name-status` gives, because the review screen compares the three of them.

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

// UntrackedPaths is the other half of the same question, and the half a bool cannot carry: the file a reviewer
// created has no commit and no index entry behind it, which is why `git diff <rev> -- <path>` says nothing
// about it and why the review screen has to know the difference before it previews one. This pins what the
// listing says *in*: git's own notion of untracked, which is not "not in HEAD".
func TestUntrackedPathsListsOnlyFilesGitHasNeverSeen(t *testing.T) {
	f, repo := openFixture(t)
	f.Commit("ignored", gittest.WithFiles(map[string]string{
		".gitignore":  "build/\n",
		"tracked.txt": "tracked\n",
	}))
	ctx := context.Background()

	// A tree with nothing new in it has no answer to give, and says so with an empty set rather than a nil
	// one the caller has to guard.
	got, err := repo.UntrackedPaths(ctx)
	if err != nil {
		t.Fatalf("UntrackedPaths on a clean tree: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("clean tree reports %v, want nothing", keys(got))
	}

	f.Write("notes/scratch.md", "# reviewer's note\n") // untracked, in a directory git has never seen
	f.Write("notes/épaulé.md", "non-ascii name\n")     // untracked, and quoted by git by default
	f.Write("tracked.txt", "tracked\nwritten over\n")  // tracked, and modified
	f.Write("staged.txt", "staged\n")
	f.MustGit("add", "staged.txt")            // in the index, so git has seen it
	f.Write("build/output.js", "generated\n") // untracked, and ignored

	got, err = repo.UntrackedPaths(ctx)
	if err != nil {
		t.Fatalf("UntrackedPaths: %v", err)
	}
	for _, want := range []string{"notes/scratch.md", "notes/épaulé.md"} {
		if !got[want] {
			t.Errorf("%s is not listed; the answer is %v", want, keys(got))
		}
	}
	for _, unwanted := range []string{
		".gitignore",      // committed and untouched
		"tracked.txt",     // tracked, and modified: dirty, but not untracked
		"staged.txt",      // in the index, which is where git first learns a path exists
		"build/output.js", // git was told to ignore it
	} {
		if got[unwanted] {
			t.Errorf("%s is listed; it is not a file git has never seen: %v", unwanted, keys(got))
		}
	}
	if len(got) != 2 {
		t.Errorf("UntrackedPaths listed %d paths, want 2: %v", len(got), keys(got))
	}
}

// The two answers are compared against each other and against `diff --name-status` by the review screen, so
// they have to be the same kind of string: repository-relative and unquoted, whatever the caller's rules say
// about the other one.
func TestUntrackedPathsKeepsPathsRelativeToTheRepository(t *testing.T) {
	f, repo := openFixture(t)
	f.Config("status.relativePaths", "true")
	f.Write("deep/down/inside/new.txt", "new\n")

	got, err := repo.UntrackedPaths(context.Background())
	if err != nil {
		t.Fatalf("UntrackedPaths: %v", err)
	}
	if !got["deep/down/inside/new.txt"] {
		t.Errorf("the new file is not listed at the repository-relative path; the answer is %v", keys(got))
	}
}
