package changeset_test

import (
	"context"
	"strings"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/gittest"
)

// An archive under `.combined/` is inert to every reader: it answers no id, it is neither landed nor active, and it
// is not offered as a thread of the changeset that hosts it. Three rules, so three assertions - they have to stay in
// agreement, and the day one of them starts walking is the day a folded changeset comes back to life inside another
// one's directory. `change combine` writes these, and the shape is what a hand-edited file can produce too.

// archivedFixture is trunk plus a branch carrying one live changeset that hosts the archive of another.
func archivedFixture(t *testing.T, nested bool) *gittest.Fixture {
	t.Helper()
	f := gittest.New(t)
	f.Commit("trunk", gittest.WithFile("README.md", "# repo\n"))
	f.CreateBranch("work")
	files := map[string]string{
		"changesets/x/CHANGESET.yaml": "id: x\nbase: main\n",
		"changesets/x/ABOUT.md":       "# x\n",
		"changesets/x/scope.md":       "# scope\n\nx's own thread\n",
		// The archived file records a stack of its own, so a reader that walked the archive would have
		// something to say about it: inertness has to hold against a file with content, not an empty stub.
		"changesets/x/.combined/y/CHANGESET.yaml": "id: y\nbase: x\nbase-changeset: x\n",
		"changesets/x/.combined/y/ABOUT.md":       "# y\n",
		"work.go":                                 "package main\n",
	}
	if nested {
		files["changesets/x/.combined/y/.combined/z/CHANGESET.yaml"] = "id: z\nbase: main\n"
		files["changesets/x/.combined/y/.combined/z/ABOUT.md"] = "# z\n"
	}
	f.Commit("one changeset, one archive inside it", gittest.WithFiles(files))
	return f
}

func TestAnArchiveUnderCombinedAnswersNoID(t *testing.T) {
	f := archivedFixture(t, false)
	r := repo(f)
	db, err := changeset.DefaultBranch(context.Background(), r, "")
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}

	got := resolveAt(t, f, "HEAD")
	if ids := candidateIDs(got); len(ids) != 1 || ids[0] != "x" {
		t.Errorf("Resolve answered %v, want [x]: changesetDir accepts metadata only at exactly changesets/<id>/CHANGESET.yaml, "+
			"and an archive is deeper than that", ids)
	}

	active, err := changeset.ActiveIDs(context.Background(), r, "HEAD")
	if err != nil {
		t.Fatalf("ActiveIDs: %v", err)
	}
	if hasID(active, "y") {
		t.Errorf("ActiveIDs answered %v: an archived changeset is not active work on this branch", active)
	}
	landed, err := changeset.LandedIDs(context.Background(), r, db.Ref)
	if err != nil {
		t.Fatalf("LandedIDs: %v", err)
	}
	if hasID(landed, "y") {
		t.Errorf("LandedIDs answered %v: an archive on a feature branch is not landed work either", landed)
	}
}

// The host's thread list is the third reader. `Threads` reads the directory non-recursively and skips directories,
// so the archive stays out of the list a reviewer is shown as the changeset's own threads.
func TestAnArchiveIsNotAThreadOfItsHost(t *testing.T) {
	f := archivedFixture(t, false)
	threads, err := changeset.Changeset{Slug: "x", Dir: "changesets/x"}.Threads(repo(f))
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	joined := strings.Join(threads, " ")
	if strings.Contains(joined, "y") || strings.Contains(joined, ".combined") {
		t.Errorf("the host's threads are %v, want only its own scope.md", threads)
	}
	if !strings.Contains(joined, "scope.md") {
		t.Errorf("the host's own thread is missing from %v", threads)
	}
}

// Nesting happens when a survivor that already holds an archive is itself folded into a third changeset, and it is
// the case a reader that walked one level deep would get wrong.
func TestArchivesNestedInAnArchiveStayInert(t *testing.T) {
	f := archivedFixture(t, true)
	got := resolveAt(t, f, "HEAD")
	if ids := candidateIDs(got); len(ids) != 1 || ids[0] != "x" {
		t.Errorf("a second level of archive answered %v, want [x]", ids)
	}
}

func hasID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
