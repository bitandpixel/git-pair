package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// `ignores:` is read and never written. The command that wrote it is gone, because the branch-shape invariant and the
// two exits cover every shape the key could decide; the record itself keeps its meaning, because a decision somebody
// recorded is evidence about why a branch is the way it is, and deleting that reading would rewrite history rather
// than stop repeating it. These two fixtures are what `use_test.go` proved, minus the write.

func TestADeclaredCohabitantStillSelectsTheDeclaringChangeset(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("cohabit")
	f.Commit("work", gittest.WithFile("x.go", "package main\n"))
	// The record a file written before the writer was removed carries: booking is this branch's work,
	// booking-tests is merely sharing the branch with it.
	f.Commit("two changesets, one of them declaring the other", gittest.WithFiles(map[string]string{
		"changesets/booking/CHANGESET.yaml":       "id: booking\nbase: main\nignores: booking-tests\n",
		"changesets/booking/ABOUT.md":             "# booking\n",
		"changesets/booking-tests/CHANGESET.yaml": "id: booking-tests\nbase: main\n",
		"changesets/booking-tests/ABOUT.md":       "# booking-tests\n",
		"booking.go":                              "package main\n",
	}))

	// Marking ready acts on the resolved changeset, so the marker names the answer the reader gave.
	ready(t, f).mustSucceed(t, "change ready")
	marker := f.MustGit("show", "-s", "--format=%B", "HEAD")
	if !strings.Contains(marker, "Review-Changeset: booking") {
		t.Errorf("the declaration did not decide which changeset the branch is working on:\n%s", marker)
	}
	if strings.Contains(marker, "Review-Changeset: booking-tests") {
		t.Error("the declared cohabitant was chosen instead")
	}
}

// The other half of the removal: the refusal that used to point at the deleted command points at the two exits,
// because a refusal with no way out is the dead end this plan exists to close.
func TestTheAmbiguityRefusalNamesTheTwoExits(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("tied")
	f.Commit("work", gittest.WithFile("x.go", "package main\n"))
	// One commit adding both directories: neither records the other, and the add-commit ordering has nothing to
	// order, so the resolver is genuinely undecided rather than merely undecided to this branch's shape.
	f.Commit("two changesets in one commit", gittest.WithFiles(map[string]string{
		"changesets/aaa/CHANGESET.yaml": "id: aaa\nbase: main\n",
		"changesets/aaa/ABOUT.md":       "# aaa\n",
		"changesets/bbb/CHANGESET.yaml": "id: bbb\nbase: main\n",
		"changesets/bbb/ABOUT.md":       "# bbb\n",
		"y.go":                          "package main\n",
	}))

	r := runIn(t, f.Dir(), "status")
	if r.code == 0 {
		t.Fatalf("status answered an undecided branch with success:\n%s", r.stdout)
	}
	out := r.stdout + r.stderr
	for _, want := range []string{"aaa", "bbb", "--changeset", "git pair change stack --base", "git pair change combine --into"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, out)
		}
	}
}
