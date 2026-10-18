package git_test

import (
	"context"
	"testing"

	"gitpair/internal/gittest"
)

// The two readings of "which paths changed" part company on a deletion paired with an unrelated addition.
// `git diff` finds renames by default, so dropping one file and writing a similar one in the same commit is
// reported as one path moving rather than one leaving and one arriving — and the path that left appears in no
// list at all. A caller asking what a branch still holds cannot afford that, because the content that left is
// content its reviewer may never have seen deleted; a caller asking what a diff would show can.
func TestContentPathsChangedKeepsADeletedPathVisible(t *testing.T) {
	f, repo := openFixture(t)
	body := "package main\n\nfunc Account() string { return \"reviewed\" }\n"
	reviewed := f.Commit("reviewed work", gittest.WithFile("account.go", body))
	// One commit does both halves, which is what makes git pair them as a rename: the file that leaves and
	// the file that arrives carry the same bytes.
	f.Remove("account.go")
	head := f.Commit("drop the reviewed file and write a similar one", gittest.WithFile("ledger.go", body))

	ctx := context.Background()

	// The defect, pinned: with rename detection on, the deleted path is not in the list at all, so a caller
	// that reads the list as "these paths differ" never learns that content left the tree.
	withRenames, err := repo.PathsChanged(ctx, reviewed, head)
	if err != nil {
		t.Fatalf("PathsChanged: %v", err)
	}
	if containsPath(withRenames, "account.go") {
		t.Fatalf("PathsChanged reported account.go (%v): rename detection is off by default in this git, so the"+
			" contrast between the two helpers needs re-thinking rather than a green test", withRenames)
	}
	if !containsPath(withRenames, "ledger.go") {
		t.Fatalf("PathsChanged = %v, want the added path listed", withRenames)
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
