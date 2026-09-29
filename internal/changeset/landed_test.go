package changeset_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/gittest"
)

// The landed namespace: `changesets/.landed/<id>/` keeps a landed changeset's metadata out of the
// way without erasing the tree's statement that the changeset existed. It fails in two ways when a
// reader does not know about it, so it is tested twice: the namespace becomes a changeset id in a
// listing, and then every branch that carries it is reported as broken rather than answered.

func activeIDsAt(t *testing.T, f *gittest.Fixture, rev string) []string {
	t.Helper()
	got, err := changeset.ActiveIDs(context.Background(), repo(f), rev)
	if err != nil {
		t.Fatalf("ActiveIDs(%s): %v", rev, err)
	}
	return got
}

func landedIDsAt(t *testing.T, f *gittest.Fixture, rev string) []string {
	t.Helper()
	got, err := changeset.LandedIDs(context.Background(), repo(f), rev)
	if err != nil {
		t.Fatalf("LandedIDs(%s): %v", rev, err)
	}
	return got
}

// A destination holding one landed changeset, tidied, and a branch carrying one still in progress.
// The stray `CHANGESET.yaml` written directly under the namespace is the sharpest form of the trap: its
// directory name is `changesets/.landed`, it parses, and its `id:` matches, so a reader that names a
// changeset from a metadata path would accept a changeset called `.landed` — and the branch below would
// then carry two of them.
func TestLandedNamespaceIsNotMistakenForAnActiveChangeset(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("done")
	f.CommitChangeset("done", "main")
	f.Commit("done work", gittest.WithFile("done.txt", "1\n"))

	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land done", "done")
	// The namespace needs a file in it before anything can move into it: git does not track empty
	// directories, and `git mv` refuses a destination whose parent is not there. The same stray
	// `CHANGESET.yaml` is the sharpest form of the trap — its directory name is `changesets/.landed`,
	// it parses, and its `id:` matches, so a reader that names a changeset from a metadata path would
	// accept a changeset called `.landed`, and the branch below would then carry two of them.
	f.Write(filepath.Join("changesets", ".landed", "CHANGESET.yaml"), "id: .landed\nbase: main\n")
	f.Commit("the landed namespace exists")
	f.MustGit("mv", "changesets/done", filepath.Join("changesets", ".landed", "done"))
	f.Commit("tidy done under the namespace")

	f.CreateBranch("work")
	f.CommitChangeset("active", "main")
	f.Commit("active work", gittest.WithFile("work.txt", "1\n"))

	if got := activeIDsAt(t, f, "HEAD"); !reflect.DeepEqual(got, []string{"active"}) {
		t.Errorf("ActiveIDs on the branch = %v, want only active: the landed namespace is not a changeset", got)
	}
	if got := activeIDsAt(t, f, "main"); len(got) != 0 {
		t.Errorf("ActiveIDs on main = %v, want none: its one changeset is tidied", got)
	}
	if got := landedIDsAt(t, f, "main"); !reflect.DeepEqual(got, []string{"done"}) {
		t.Errorf("LandedIDs on main = %v, want done: the moved directory is landed work", got)
	}

	// The branch answers rather than erroring. Before the exclusion the namespace was a candidate, so
	// this call had two candidates to choose between and the branch carried an ambiguity instead of
	// the one changeset it is working on.
	if got := resolveAt(t, f, "HEAD"); !reflect.DeepEqual(candidateIDs(got), []string{"active"}) {
		t.Errorf("candidates = %v, want only active", candidateIDs(got))
	}
}

// A destination holding both spellings at once — the state between a landing and the tidying that
// follows it, and the state a partial tidy leaves behind — reports the changeset once.
func TestLandedIDsCountAChangesetOnceInEitherSpelling(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("booking work", gittest.WithFile("booking.txt", "1\n"))
	f.CreateBranch("ui")
	f.CommitChangeset("ui", "main")
	f.Commit("ui work", gittest.WithFile("ui.txt", "1\n"))

	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land booking and ui", "booking", "ui")
	// Move one and leave the other: `changesets/ui/` and `changesets/.landed/booking/` on one tree.
	f.Write(filepath.Join("changesets", ".landed", ".keep"), "the namespace, tracked\n")
	f.MustGit("mv", "changesets/booking", filepath.Join("changesets", ".landed", "booking"))
	f.Commit("tidy booking")

	if got := landedIDsAt(t, f, "main"); !reflect.DeepEqual(got, []string{"booking", "ui"}) {
		t.Errorf("LandedIDs = %v, want booking and ui", got)
	}
	if got := activeIDsAt(t, f, "main"); !reflect.DeepEqual(got, []string{"ui"}) {
		t.Errorf("ActiveIDs = %v, want ui: the tidied directory is not listed as work", got)
	}
	// A changeset is never listed twice because both of its paths exist.
	for _, id := range []string{"booking", "ui"} {
		if n := countOf(landedIDsAt(t, f, "main"), id); n != 1 {
			t.Errorf("LandedIDs lists %s %d times, want once", id, n)
		}
	}
}

// Nothing to report is an empty answer, not a failure, for a revision whose tree has no
// `changesets/` directory: the first commit of a repository, and a release branch cut before changesets
// were invented. `ActiveIDs` also tolerates a revision git cannot resolve, because that is what a
// destination this clone has not fetched looks like — but `ls-tree` reports one as "Not a valid object
// name", which `git.IsUnknownRevision` does not match, so that half of the tolerance is not reachable
// through this path and is not asserted here. It is asserted where the revision is parsed
// (`internal/git`), and the two messages are worth lining up before a caller depends on it.
func TestActiveIDsAndLandedIDsOfRevisionsWithNothing(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))

	if got := activeIDsAt(t, f, "HEAD"); len(got) != 0 {
		t.Errorf("ActiveIDs with no changesets/ = %v, want none", got)
	}
	if got := landedIDsAt(t, f, "HEAD"); len(got) != 0 {
		t.Errorf("LandedIDs with no changesets/ = %v, want none", got)
	}
}

// The namespace is a name no changeset may take, so `git pair init --id .landed` cannot write a
// working changeset over the directories of changesets that have landed.
func TestValidateIDRefusesTheLandedNamespace(t *testing.T) {
	if err := changeset.ValidateID(changeset.LandedDir); err == nil {
		t.Error("ValidateID accepted the landed namespace as a changeset id")
	}
	for _, id := range []string{"done", "dots.in.name", "snake_case"} {
		if err := changeset.ValidateID(id); err != nil {
			t.Errorf("ValidateID(%s): %v, want it accepted", id, err)
		}
	}
}

// A walk over one changeset's history has to ask for both spellings, because the tidying move puts
// the original path in the history of a directory that now lives at the other.
func TestDirPathspecsNameBothSpellings(t *testing.T) {
	want := []string{
		filepath.Join("changesets", "booking") + "/",
		filepath.Join("changesets", ".landed", "booking") + "/",
	}
	got := changeset.DirPathspecs("booking")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DirPathspecs = %v, want %v", got, want)
	}
	if !changeset.Reserved(".landed") || changeset.Reserved("booking") {
		t.Error("Reserved does not name the landed namespace alone")
	}
}

func countOf(ids []string, want string) int {
	n := 0
	for _, id := range ids {
		if id == want {
			n++
		}
	}
	return n
}
