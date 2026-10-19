package git_test

import (
	"context"
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The two readings of "which paths changed" part company on a deletion paired with an unrelated addition.
// `git diff` finds renames by default, so dropping one file and writing a similar one in the same commit is
// reported as one path moving rather than one leaving and one arriving — and the path that left appears in no list
// at all. Every list this package hands a caller is a content list for that reason: the callers ask what
// differs, and a path missing because git found a lookalike is a lie about a branch. The one reader that
// wants git's rename verdict is the review TUI's change signs, which asks `diff --name-status` for itself
// (see TestChangeStatusesSignsBothHalvesOfAMove).
func TestContentPathsChangedKeepsADeletedPathVisible(t *testing.T) {
	f, repo := openFixture(t)
	body := "package main\n\nfunc Account() string { return \"reviewed\" }\n"
	reviewed := f.Commit("reviewed work", gittest.WithFile("account.go", body))
	// One commit does both halves, which is what makes git pair them as a rename: the file that leaves and
	// the file that arrives carry the same bytes.
	f.Remove("account.go")
	head := f.Commit("drop the reviewed file and write a similar one", gittest.WithFile("ledger.go", body))

	ctx := context.Background()

	// The defect, pinned: with git's rename detection — on by default — the pair is one rename, and the path
	// that left the tree is in no list. This is what every caller of these lists used to read, so if a future
	// git or a future refactor makes the plain question answer like the content question, the reading below
	// stops meaning anything and this is the assertion that says so.
	withRenames, err := repo.Git(ctx, "diff", "--name-only", reviewed, head)
	if err != nil {
		t.Fatalf("git diff --name-only: %v", err)
	}
	if lineListed(withRenames, "account.go") {
		t.Fatalf("git diff --name-only reported account.go (%q): rename detection is off by default in this git, so"+
			" the contrast this test draws needs re-thinking rather than a green test", withRenames)
	}
	if !lineListed(withRenames, "ledger.go") {
		t.Fatalf("git diff --name-only = %q, want the added path listed", withRenames)
	}

	// The same comparison asked of content rather than of a diff lists both halves.
	noRenames, err := repo.ContentPathsChanged(ctx, reviewed, head)
	if err != nil {
		t.Fatalf("ContentPathsChanged: %v", err)
	}
	for _, want := range []string{"account.go", "ledger.go"} {
		if !containsPath(noRenames, want) {
			t.Errorf("ContentPathsChanged = %v, want %s listed: a path whose presence changed is a changed path",
				noRenames, want)
		}
	}
	if containsPath(noRenames, "a.txt") {
		t.Errorf("ContentPathsChanged = %v, want no untouched path listed", noRenames)
	}

	// The excluded-directory sibling answers the same question, because the drift rule reads that one.
	notes := f.Commit("changeset notes", gittest.WithFile("changesets/feature/ABOUT.md", "# feature\n"))
	notesAgain := f.Commit("notes again", gittest.WithFile("changesets/feature/ABOUT.md", "# feature\n\n## Summary\n\nNotes.\n"))
	outside, err := repo.ContentPathsChangedOutside(ctx, notes, notesAgain, "changesets/feature")
	if err != nil {
		t.Fatalf("ContentPathsChangedOutside: %v", err)
	}
	if len(outside) != 0 {
		t.Errorf("ContentPathsChangedOutside = %v, want nothing: the only change was inside the excluded directory",
			outside)
	}
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

func lineListed(out, want string) bool {
	for _, line := range strings.Split(out, "\n") {
		if line == want {
			return true
		}
	}
	return false
}
