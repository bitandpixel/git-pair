package changeset_test

import (
	"testing"

	"gitpair/internal/gittest"
)

// The set operation is the primary rule for a stack: the candidates minus the changesets other candidates record
// as their base changeset. These eight shapes came from `probe/setop-selection`, where each was measured with the
// operation installed and with it removed, and they are kept because they are the cases where the tiers disagree -
// which is the only way to tell an ordering rule from a subtraction rule.

// stackedFixture is a trunk with one commit on it, which every shape below needs before a branch can point at it.
func stackedFixture(t *testing.T) *gittest.Fixture {
	t.Helper()
	f := gittest.New(t)
	f.Commit("trunk", gittest.WithFile("README.md", "# repo\n"))
	return f
}

// commitRecord commits a changeset whose CHANGESET.yaml is exactly the body the test wants, which is how a legacy
// file (a `base:` with no recorded id) and a current one (with the id) are built from the same helper.
func commitRecord(t *testing.T, f *gittest.Fixture, id, body string, opts ...gittest.CommitOpt) {
	t.Helper()
	f.StageChangeset(id, "main") // writes ABOUT.md too, so the directory counts as described
	f.WriteChangesetFile(id, "CHANGESET.yaml", body)
	f.Commit("Add changeset "+id, opts...)
}

// The shape the rule exists for: a linear chain, each level naming the one below. Every level's directory is on
// the tip revision, because branching carries the unlanded ancestry, so three candidates arrive here and one leaves.
func TestTheSetOperationDecidesALinearStack(t *testing.T) {
	f := stackedFixture(t)
	f.CreateBranch("feature/a")
	f.Commit("a work", gittest.WithFile("a.go", "package main\n"))
	commitRecord(t, f, "feature-a", "id: feature-a\nbase: main\n")

	f.CreateBranch("feature/b")
	commitRecord(t, f, "feature-b", "id: feature-b\nbase: feature/a\nbase-changeset: feature-a\n")
	f.Commit("b work", gittest.WithFile("b.go", "package main\n"))

	f.CreateBranch("feature/c")
	commitRecord(t, f, "feature-c", "id: feature-c\nbase: feature/b\nbase-changeset: feature-b\n")
	f.Commit("c work", gittest.WithFile("c.go", "package main\n"))

	got := resolveAt(t, f, "refs/heads/feature/c")
	if ids := candidateIDs(got); len(ids) != 1 || ids[0] != "feature-c" {
		t.Errorf("candidates %v, want [feature-c]: both levels below name their parent, so one pass over the "+
			"recorded ids removes them - the chain does not need a walk", ids)
	}
	if got.Ambiguous || selectedID(got) != "feature-c" {
		t.Errorf("selected %q ambiguous %v, want feature-c", selectedID(got), got.Ambiguous)
	}
}

// The branch works on the child and somebody commits to the parent afterwards. Subtraction answers before the
// ordering, so the edit cannot move the answer - which is the same defect M3 fixed in the measurement, closed one
// tier earlier so that the branch does not ask git at all.
func TestTheSetOperationIsNotMovedByALaterEditToTheParent(t *testing.T) {
	f := stackedFixture(t)
	f.CreateBranch("booking")
	f.Commit("booking work", gittest.WithFile("service.go", "package main\n"))
	commitRecord(t, f, "booking", "id: booking\nbase: main\n")

	f.CreateBranch("booking-tests")
	commitRecord(t, f, "booking-tests", "id: booking-tests\nbase: booking\nbase-changeset: booking\n")
	f.Commit("tests work", gittest.WithFile("tests.go", "package main\n"))

	f.Commit("more booking work, last", gittest.WithFiles(map[string]string{
		"changesets/booking/ABOUT.md": "# booking, revised\n",
		"service.go":                  "package main // revised\n",
	}))

	got := resolveAt(t, f, "refs/heads/booking-tests")
	if ids := candidateIDs(got); len(ids) != 1 || ids[0] != "booking-tests" {
		t.Errorf("candidates %v, want [booking-tests]: the parent is recorded below this one, so a commit to its "+
			"directory is housekeeping and not a reason to report it as the work", ids)
	}
	if id := selectedID(got); id != "booking-tests" {
		t.Errorf("selected %q, want booking-tests", id)
	}
}

// The file written before ids were recorded: a `base:` naming a branch and nothing else. Subtraction cannot remove
// what is not written, so the parent stays a candidate and the ranking answers - correctly here, because the child's
// directory joined the line later, and visibly: the candidate list says both are on the branch.
func TestALegacyStackWithoutARecordedIdFallsToTheRanking(t *testing.T) {
	f := stackedFixture(t)
	f.CreateBranch("feature/a")
	f.Commit("a work", gittest.WithFile("a.go", "package main\n"))
	commitRecord(t, f, "feature-a", "id: feature-a\nbase: main\n")

	f.CreateBranch("feature/b")
	commitRecord(t, f, "feature-b", "id: feature-b\nbase: feature/a\n")
	f.Commit("b work, recorded before ids were kept", gittest.WithFile("b.go", "package main\n"))

	got := resolveAt(t, f, "refs/heads/feature/b")
	if got.Ambiguous || selectedID(got) != "feature-b" {
		t.Errorf("nothing answered a legacy child: selected %q ambiguous %v candidates %v",
			selectedID(got), got.Ambiguous, candidateIDs(got))
	}
	if ids := candidateIDs(got); len(ids) != 2 {
		t.Errorf("candidates %v, want both directories: with no recorded id there is nothing to subtract, and the "+
			"answer is a ranking the reader can see through rather than a subtraction the reader cannot question", ids)
	}
}

// Two hand-edited files naming each other subtract each other. An empty list would be reported as "no changeset
// here" for a branch that visibly has two, so the guard keeps both and the answer is the refusal.
func TestMutualBaseChangesetRecordsLeaveTheCandidatesStanding(t *testing.T) {
	f := stackedFixture(t)
	f.CreateBranch("cohabit")
	f.Commit("work", gittest.WithFile("x.go", "package main\n"))
	commitRecord(t, f, "one", "id: one\nbase: main\nbase-changeset: two\n")
	commitRecord(t, f, "two", "id: two\nbase: main\nbase-changeset: one\n")

	got := resolveAt(t, f, "HEAD")
	if !got.Ambiguous {
		t.Errorf("a mutual record selected %q, want the refusal: the two files contradict each other and creation "+
			"order is not the author's intent", selectedID(got))
	}
	if ids := candidateIDs(got); len(ids) != 2 {
		t.Errorf("candidates %v, want both: subtracting everything is the contradiction, not an empty branch", ids)
	}
}

// A candidate that names itself is a hand edit, not a stack. The self-edge has to be ignored or the file's author
// is told their own changeset is somebody else's parent.
func TestACandidateNamingItselfIsNotItsOwnParent(t *testing.T) {
	f := stackedFixture(t)
	f.CreateBranch("selfish")
	f.Commit("work", gittest.WithFile("x.go", "package main\n"))
	commitRecord(t, f, "selfish", "id: selfish\nbase: main\nbase-changeset: selfish\n")

	got := resolveAt(t, f, "refs/heads/selfish")
	if got.Ambiguous || selectedID(got) != "selfish" {
		t.Errorf("a self-record hid the branch's own changeset: selected %q ambiguous %v candidates %v",
			selectedID(got), got.Ambiguous, candidateIDs(got))
	}
	if ids := candidateIDs(got); len(ids) != 1 || ids[0] != "selfish" {
		t.Errorf("candidates %v, want [selfish]", ids)
	}
}

// The dangling ancestor: the middle level landed on its own, so it is not a candidate, and the child records that
// landed id as its parent. One hop of subtraction removes nothing, because the name does not appear among the
// candidates - which is the right behaviour (the grandparent is not this child's parent) and leaves the ranking to
// answer with the grandparent still visible.
func TestAStackOnALandedParentLosesNothing(t *testing.T) {
	f := stackedFixture(t)
	f.CreateBranch("feature/a")
	f.Commit("a work", gittest.WithFile("a.go", "package main\n"))
	commitRecord(t, f, "feature-a", "id: feature-a\nbase: main\n")

	f.CreateBranch("feature/b")
	commitRecord(t, f, "feature-b", "id: feature-b\nbase: feature/a\nbase-changeset: feature-a\n")
	f.Commit("b work", gittest.WithFile("b.go", "package main\n"))

	f.SwitchTo("main")
	commitRecord(t, f, "feature-b", "id: feature-b\nbase: feature/a\nbase-changeset: feature-a\n")

	f.CreateBranch("feature/c", "feature/b")
	commitRecord(t, f, "feature-c", "id: feature-c\nbase: feature/b\nbase-changeset: feature-b\n")
	f.Commit("c work", gittest.WithFile("c.go", "package main\n"))

	got := resolveAt(t, f, "refs/heads/feature/c")
	if got.Ambiguous || selectedID(got) != "feature-c" {
		t.Errorf("selected %q ambiguous %v, want feature-c: the recorded parent landed and is not a candidate, so "+
			"the ranking has to answer with what is left (candidates %v)", selectedID(got), got.Ambiguous, candidateIDs(got))
	}
	if ids := candidateIDs(got); len(ids) != 2 {
		t.Errorf("candidates %v, want feature-c and feature-a: the landed parent is never a candidate, and the "+
			"grandparent is not named by this child's record", ids)
	}
}

// The one shape where a subtraction could overrule an author, and it does not: `booking` declares it merely shares
// the branch with `booking-tests` while both live on this branch, and `booking-tests` records `booking` below it.
// The declaration is consulted first, so the branch reports the changeset the author named.
func TestTheDeclarationStillOutranksTheSetOperation(t *testing.T) {
	f := stackedFixture(t)
	f.CreateBranch("booking-tests")
	f.Commit("booking work", gittest.WithFile("service.go", "package main\n"))
	commitRecord(t, f, "booking", "id: booking\nbase: main\nignores: booking-tests\n")
	commitRecord(t, f, "booking-tests", "id: booking-tests\nbase: booking\nbase-changeset: booking\n")

	got := resolveAt(t, f, "refs/heads/booking-tests")
	if got.Ambiguous || selectedID(got) != "booking" {
		t.Errorf("selected %q ambiguous %v, want booking: the author recorded that this branch works on booking, and "+
			"a subtraction that runs first would answer for them (candidates %v)",
			selectedID(got), got.Ambiguous, candidateIDs(got))
	}
	if ids := candidateIDs(got); len(ids) != 1 || ids[0] != "booking" {
		t.Errorf("candidates %v, want [booking] - the declared cohabitant leaves the list first", ids)
	}
}

// Two independent changesets on one branch, neither naming the other: no record decides, so the ranking does, and
// it reports both. This is the shape M6 refuses at offer time, and it is also why the ranking stays - `status` and
// `diff` still have to answer something on a branch in it.
func TestSiblingsWithNoRecordsAreOrderedByCreation(t *testing.T) {
	f := stackedFixture(t)
	f.CreateBranch("two-things")
	f.Commit("work", gittest.WithFile("x.go", "package main\n"))
	commitRecord(t, f, "first", "id: first\nbase: main\n")
	f.Commit("later work, with the second changeset added", gittest.WithFiles(map[string]string{
		"changesets/second/CHANGESET.yaml": "id: second\nbase: main\n",
		"changesets/second/ABOUT.md":       "# second\n",
		"y.go":                             "package main\n",
	}))

	got := resolveAt(t, f, "refs/heads/two-things")
	if got.Ambiguous || selectedID(got) != "second" {
		t.Errorf("selected %q ambiguous %v, want second: neither records the other, so the directory that joined "+
			"this line last is the answer (candidates %v)", selectedID(got), got.Ambiguous, candidateIDs(got))
	}
	if ids := candidateIDs(got); len(ids) != 2 {
		t.Errorf("candidates %v, want both siblings: nothing here says either is below the other", ids)
	}
}
