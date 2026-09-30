package changeset_test

import (
	"testing"

	"gitpair/internal/gittest"
)

// `ignores:` is the author's own statement of which changeset the branch is working on. It has to decide before
// any inference removes the changeset that wrote it, because a dropped candidate cannot make a declaration that
// anyone then reads. These two fixtures are the same branch in the two spellings of a stack link, which take
// different paths through `choose`.

func declaredBranch(t *testing.T, branchBase string) *gittest.Fixture {
	t.Helper()
	f := gittest.New(t)
	f.Commit("trunk", gittest.WithFile("README.md", "# repo\n"))

	f.CreateBranch("booking")
	f.Commit("booking work", gittest.WithFile("service.go", "package main\n"))
	f.CommitChangeset("booking", "main")
	f.WriteChangesetFile("booking", "CHANGESET.yaml", "id: booking\nbase: main\nignores: booking-tests\n")
	f.Commit("booking records that the tests changeset merely shares this branch")

	f.CreateBranch("booking-tests")
	f.WriteChangesetFile("booking-tests", "CHANGESET.yaml",
		"id: booking-tests\nbase: "+branchBase+"\nbase-changeset: booking\n")
	f.Commit("the tests changeset, stacked on booking", gittest.WithFile("tests.go", "package main\n"))

	// Work on booking, last. If anything still ranks by recency, booking-tests is the answer it would give.
	f.Commit("more booking work", gittest.WithFiles(map[string]string{
		"service.go":                        "package main // revised\n",
		"changesets/booking-tests/ABOUT.md": "# tests, touched last\n",
	}))
	return f
}

// The recorded link names the parent as a changeset id, which is the spelling the ancestor drop can match. Today
// that drop runs first, so the changeset that wrote `ignores:` leaves the list before its declaration is consulted.
func TestTheDeclarationOutranksARecordedStackID(t *testing.T) {
	f := declaredBranch(t, "booking")

	got := resolveAt(t, f, "refs/heads/booking-tests")
	if got.Ambiguous {
		t.Fatalf("a declaration answered the branch, so nothing is ambiguous: candidates %v", candidateIDs(got))
	}
	if id := selectedID(got); id != "booking" {
		t.Errorf("selected %q, want booking: the author recorded `ignores: booking-tests` on booking, so the "+
			"branch is about booking (candidates %v)", id, candidateIDs(got))
	}
}

// The recorded link names the parent branch instead, which the ancestor drop cannot match at all. The declaration
// is the only evidence that decides this branch, so it has to hold with the id spelling as it does here.
func TestTheDeclarationOutranksARecordedParentBranch(t *testing.T) {
	f := declaredBranch(t, "feature/booking")

	got := resolveAt(t, f, "refs/heads/booking-tests")
	if got.Ambiguous {
		t.Fatalf("a declaration answered the branch, so nothing is ambiguous: candidates %v", candidateIDs(got))
	}
	if id := selectedID(got); id != "booking" {
		t.Errorf("selected %q, want booking (candidates %v)", id, candidateIDs(got))
	}
}
