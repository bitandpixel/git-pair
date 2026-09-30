package changeset_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The tie-break for candidates that the records do not separate is the commit that added each
// directory, not the commit that last edited one. These fixtures keep the ancestor drop out of the
// way - a `base:` naming a branch is not a value the drop can match against a slug - so the ordering
// is the only thing that can decide, which is where it belongs.

func twoCandidatesUnordered(t *testing.T, recorded bool) *gittest.Fixture {
	t.Helper()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("README.md", "# repo\n"))

	f.CreateBranch("feature/booking")
	f.Commit("booking work", gittest.WithFile("service.go", "package main\n"))
	f.CommitChangeset("booking", "main")

	f.CreateBranch("booking-tests")
	child := "id: booking-tests\nbase: feature/booking\n"
	if recorded {
		child += "base-changeset: booking\n"
	}
	f.WriteChangesetFile("booking-tests", "CHANGESET.yaml", child)
	f.Commit("the tests changeset, stacked on booking", gittest.WithFile("tests.go", "package main\n"))
	return f
}

// Editing a changeset the branch is not working on is the case that makes the last edit useless as
// evidence: the branch carries its parent's directory, and housekeeping there is not a decision to
// start working on the parent.
func TestTheChangesetAddedLastIsTheWorkInTheBranch(t *testing.T) {
	// Both spellings of the child's file have to answer the same way. With the recorded id the answer may
	// come from the record; without it, the commit that added each directory is the only evidence left, and
	// it has to be enough on its own.
	for _, tt := range []struct {
		name     string
		recorded bool
	}{
		{"with the recorded id", true},
		{"with the record removed", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := twoCandidatesUnordered(t, tt.recorded)
			f.Commit("housekeeping on the parent, last", gittest.WithFile("changesets/booking/ABOUT.md", "# booking, revised\n"))

			got := resolveAt(t, f, "refs/heads/booking-tests")
			if got.Ambiguous {
				t.Fatalf("the two directories were added by different commits, so the branch is decided: %v", candidateIDs(got))
			}
			if id := selectedID(got); id != "booking-tests" {
				t.Errorf("selected %q, want booking-tests: it is the directory added last, and the newest edit is "+
					"on the parent (candidates %v)", id, candidateIDs(got))
			}
		})
	}
}

// Two directories created by one commit are a tie, and it stays a tie when one of them is edited
// afterwards. Under the last edit the later edit broke the tie, which answered a question about the
// author's intent with a fact about housekeeping.
func TestChangesetsAddedByOneCommitStayAmbiguousWhenOneIsEditedAfterwards(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("README.md", "# repo\n"))
	f.CreateBranch("two-things")

	f.StageChangeset("first", "main")
	f.StageChangeset("second", "main")
	f.Commit("both changesets, one commit")

	f.Commit("later work on the second", gittest.WithFile("changesets/second/ABOUT.md", "# second, revised\n"))

	got := resolveAt(t, f, "HEAD")
	if !got.Ambiguous {
		t.Errorf("selected %q, want the branch undecided: both directories joined on the same commit, so "+
			"a later edit to one of them says nothing about which the branch is about (candidates %v)",
			selectedID(got), candidateIDs(got))
	}
}

// The ranking is the only part of the decision that asks git for history, so it must not happen when
// the records already decided. Counting invocations is not enough here: the assertion is about which
// command ran.
func TestTheRankingReadHappensOnlyWhenCandidatesRemain(t *testing.T) {
	single := gittest.New(t)
	single.Commit("seed", gittest.WithFile("README.md", "# repo\n"))
	single.CreateBranch("solo")
	single.CommitChangeset("solo", "main")
	single.Commit("solo work", gittest.WithFile("solo.go", "package main\n"))

	_, singleLines := single.SpawnShimLines(t)
	if got := resolveAt(t, single, "refs/heads/solo"); selectedID(got) != "solo" {
		t.Fatalf("a branch with one changeset should answer it, got %q", selectedID(got))
	}
	if n := rankingCalls(singleLines()); n != 0 {
		t.Errorf("a single candidate cost %d history reads, want none: nothing had to be ordered (invocations: %v)",
			n, matchingAny(singleLines(), rankingReads))
	}

	// A stack is the case that used to pay for a walk it did not need: the revision carried two
	// directories, the recorded link answered which one was the work, and the ordering still walked
	// both before the filter removed one.
	stacked := gittest.New(t)
	stacked.Commit("seed", gittest.WithFile("README.md", "# repo\n"))
	stacked.CreateBranch("booking")
	stacked.Commit("booking work", gittest.WithFile("service.go", "package main\n"))
	stacked.CommitChangeset("booking", "main")
	stacked.CreateBranch("booking-tests")
	// An id-shaped base: this is the spelling the ancestor drop can match, so one candidate remains.
	stacked.WriteChangesetFile("booking-tests", "CHANGESET.yaml",
		"id: booking-tests\nbase: booking\nbase-changeset: booking\n")
	stacked.Commit("the tests changeset", gittest.WithFile("tests.go", "package main\n"))

	_, stackedLines := stacked.SpawnShimLines(t)
	if got := resolveAt(t, stacked, "refs/heads/booking-tests"); selectedID(got) != "booking-tests" {
		t.Fatalf("the recorded link decides this branch, got %q (%v)", selectedID(got), candidateIDs(got))
	}
	if n := rankingCalls(stackedLines()); n != 0 {
		t.Errorf("a branch decided by its records cost %d history reads, want none: the walk ran before the "+
			"filter could reduce it to one (invocations: %v)", n, matchingAny(stackedLines(), rankingReads))
	}

	multiple := twoCandidatesUnordered(t, true)
	multiple.Commit("later work", gittest.WithFile("notes.md", "notes\n"))
	_, multipleLines := multiple.SpawnShimLines(t)
	if got := resolveAt(t, multiple, "refs/heads/booking-tests"); got.Ambiguous {
		t.Fatalf("the two directories were added by different commits, so the branch is decided: %v", candidateIDs(got))
	}
	if n, want := rankingCalls(multipleLines()), 2*len(rankingReads); n != want {
		t.Errorf("two candidates cost %d history reads, want %d: one add commit and one walk per candidate "+
			"(invocations: %v)", n, want, matchingAny(multipleLines(), rankingReads))
	}
}

// rankingReads are the invocations ranking one candidate makes: the commit that added its directory,
// and the walk from that commit to the revision. A path that does not rank makes neither of them, which
// is why both are counted - a stray history read under either shape fails the assertion.
var rankingReads = []string{"--diff-filter=A", "rev-list --count"}

func rankingCalls(lines []string) int {
	n := 0
	for _, needle := range rankingReads {
		n += countLinesWith(lines, needle)
	}
	return n
}

func matchingAny(lines []string, needles []string) []string {
	var out []string
	for _, needle := range needles {
		out = append(out, matching(lines, needle)...)
	}
	return out
}

func countLinesWith(lines []string, needle string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, needle) {
			n++
		}
	}
	return n
}

func matching(lines []string, needle string) []string {
	var out []string
	for _, l := range lines {
		if strings.Contains(l, needle) {
			out = append(out, l)
		}
	}
	return out
}
