package changeset_test

import (
	"context"
	"errors"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/gittest"
)

// A stack is recorded as `base:` together with `base-changeset:`, always both. The id is the half that has to
// survive its branch's deletion, and it is the half the resolver could not use while `stackOf` discarded it from
// a file with no `parent:` key.

const stackTop = "refs/heads/feature/booking-cases"

// threeLevelStack builds trunk, `booking`, `booking-tests` on it, and `booking-cases` on that - each on a branch
// whose name is *not* its changeset slug. That difference is the fixture. The ancestor drop compares what a
// candidate names against a slug, so a base reading `feature/booking-tests` matches nothing until the recorded id
// is read, and a branch named after its changeset - which is what most of this repository's own branches look
// like - would let the old code pass by accident. Every branch carries the whole unlanded chain in its tree,
// because that is what branching does, so the tip revision is a three-candidate revision and only the records can
// order it.
func threeLevelStack(t *testing.T, legacy bool) *gittest.Fixture {
	t.Helper()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("README.md", "# repo\n"))

	f.CreateBranch("feature/booking")
	f.CommitChangeset("booking", "main")
	f.Commit("booking work", gittest.WithFile("service.go", "package main\n"))

	idKey, baseKey := changeset.BaseChangesetKey, "base"
	if legacy {
		baseKey, idKey = changeset.ParentKey, changeset.ParentChangesetKey
	}

	f.CreateBranch("feature/booking-tests")
	f.WriteChangesetFile("booking-tests", "CHANGESET.yaml",
		"id: booking-tests\n"+baseKey+": feature/booking\n"+idKey+": booking\n")
	f.Commit("tests work", gittest.WithFile("tests.go", "package main\n"))

	f.CreateBranch("feature/booking-cases")
	f.WriteChangesetFile("booking-cases", "CHANGESET.yaml",
		"id: booking-cases\n"+baseKey+": feature/booking-tests\n"+idKey+": booking-tests\n")
	f.Commit("cases work", gittest.WithFile("cases.go", "package main\n"))
	return f
}

func TestARecordedStackShrinksToTheChangesetOnTop(t *testing.T) {
	// Either key may carry the id, because `parent-changeset:` is read and not written: which one a file uses is
	// a fact about when it was written, and a drop that understood only the newer key would leave a legacy stack's
	// ancestors standing.
	for _, tt := range []struct {
		name   string
		legacy bool
	}{
		{"written as base: with base-changeset:", false},
		{"written as parent: with parent-changeset:", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := threeLevelStack(t, tt.legacy)
			f.MustGit("rev-parse", "--verify", stackTop) // a mistyped rev would resolve the working tree instead
			got := resolveAt(t, f, stackTop)

			if ids := candidateIDs(got); len(ids) != 1 || ids[0] != "booking-cases" {
				t.Errorf("candidates %v, want [booking-cases]: the two below it are recorded as its ancestors, so "+
					"they say where this branch's work starts and are not the work itself", ids)
			}
			if got.Ambiguous {
				t.Errorf("the branch is undecided, want booking-cases: %v", candidateIDs(got))
			}
			if id := selectedID(got); id != "booking-cases" {
				t.Errorf("selected %q, want booking-cases", id)
			}
		})
	}
}

// Reading the older spelling has to reach further than the drop. A file naming its parent with `parent:` has to
// measure against the same place, for the same reason, as the same stack written today - otherwise trunk's landed
// history and a branch off it are measured by two rules, and this repository has no migration to fix that.
func TestALegacyParentFileAnswersWhatTheNewSpellingMeans(t *testing.T) {
	fresh, legacy := threeLevelStack(t, false), threeLevelStack(t, true)
	fresh.MustGit("rev-parse", "--verify", stackTop)
	legacy.MustGit("rev-parse", "--verify", stackTop)

	newSel := selected(t, fresh, stackTop)
	oldSel := selected(t, legacy, stackTop)
	if newSel == nil || oldSel == nil {
		t.Fatalf("one side did not answer: new %v, legacy %v",
			candidateIDs(resolveAt(t, fresh, stackTop)), candidateIDs(resolveAt(t, legacy, stackTop)))
	}
	if newSel.Changeset.Slug != oldSel.Changeset.Slug {
		t.Errorf("the new spelling selects %q, the legacy one selects %q", newSel.Changeset.Slug, oldSel.Changeset.Slug)
	}
	if newSel.Changeset.Base != oldSel.Changeset.Base {
		t.Errorf("the new spelling measures against %q, the legacy one against %q",
			newSel.Changeset.Base, oldSel.Changeset.Base)
	}
	if newSel.Changeset.BaseWhy != oldSel.Changeset.BaseWhy {
		t.Errorf("the new spelling explains the base as %q, the legacy one as %q",
			newSel.Changeset.BaseWhy, oldSel.Changeset.BaseWhy)
	}
}

// A file that sets both spellings of the base answers with an error rather than a choice, and it answers where the
// file is read, so no command gets to run on whichever half it happened to look at first.
func TestAFileNamingBothBaseAndParentIsRefused(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("README.md", "# repo\n"))
	f.CreateBranch("both")
	f.WriteChangesetFile("both", "CHANGESET.yaml", "id: both\nbase: main\nparent: booking\n")
	f.Commit("a file that means two things")

	_, err := changeset.StackAt(context.Background(), repo(f), "refs/heads/both", "both")
	if !errors.Is(err, changeset.ErrParentWithBase) {
		t.Errorf("StackAt answered %v, want ErrParentWithBase", err)
	}
}

func selected(t *testing.T, f *gittest.Fixture, rev string) *changeset.Candidate {
	t.Helper()
	res := resolveAt(t, f, rev)
	if res.Selected == nil {
		return nil
	}
	for i, c := range res.Candidates {
		if c.Changeset.Slug == res.Selected.Changeset.Slug {
			return &res.Candidates[i]
		}
	}
	return nil
}
