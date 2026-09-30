package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The invariant a branch has to satisfy before it is offered: it carries one changeset, plus the changesets it is
// stacked on (PRD 4). These are the entry points that enforce it - `change ready` on the way to the queue, and
// `check` as the reason the merge is gated - plus the two cases the rule must not get wrong in either direction:
// a changeset the destination already carries, which is landed work rather than a second changeset, and a child
// that records the parent it is stacked on, which is the ordinary shape of a stack.
//
// Each refusal is asserted as text, because the text is the thing a reader acts on: an id that is not named cannot
// be taken out of the branch, and a way out that is not printed is not found.

// twoChangesetsOnOneBranch is the refused shape as it is actually acquired: the second directory arrives in a
// commit of its own, so the ordering has an answer and the commands would otherwise pick one of the two and offer
// the branch as though the other were not there.
func twoChangesetsOnOneBranch(t *testing.T) *gittest.Fixture {
	t.Helper()
	f := newRepo(t)
	f.CreateBranch("two-things")
	f.CommitChangeset("first", "main")
	f.Commit("later work, with a second changeset added", gittest.WithFiles(map[string]string{
		"changesets/second/CHANGESET.yaml": "id: second\nbase: main\n",
		"changesets/second/ABOUT.md":       "# second\n",
		"x.go":                             "package main\n",
	}))
	return f
}

// The refusal on the way to the queue, and the evidence for keying it on the candidate set rather than on the
// selection: the resolver has an answer for this branch - the second directory joined the line later, so that is
// the changeset the branch is working on - and the refusal stands anyway. A rule written over the selection would
// have found a selected changeset, seen nothing wrong with it, and let the branch into the queue with the other
// directory riding along.
func TestChangeReadyRefusesABranchCarryingTwoChangesets(t *testing.T) {
	f := twoChangesetsOnOneBranch(t)

	r := runIn(t, f.Dir(), "change", "ready")
	if r.code != exitRefusal {
		t.Fatalf("marking a branch carrying two changesets ready exited %d, want %d\nstderr: %s", r.code, exitRefusal, r.stderr)
	}
	out := r.stdout + r.stderr
	for _, want := range []string{"first", "second", "one changeset"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, out)
		}
	}
	for _, want := range []string{"git pair change stack --base", "git pair change combine --into", "git restore --source=", "git fetch"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal gives no way out containing %q:\n%s", want, out)
		}
	}
}

// The same condition as a gate reason, which is the half that stops the CI job: the second changeset reaches the
// destination with no approval of its own, and no review of the first one covers it.
func TestCheckGatesABranchCarryingTwoChangesets(t *testing.T) {
	f := twoChangesetsOnOneBranch(t)

	out := runIn(t, f.Dir(), "check", "--json").json(t)
	if out["ready"].(bool) {
		t.Fatalf("check reported a branch carrying two changesets as ready: %v", out["reasons"])
	}
	var reason string
	for _, v := range out["reasons"].([]any) {
		if s, _ := v.(string); strings.Contains(s, "no approval of its own") {
			reason = s
		}
	}
	if reason == "" {
		t.Fatalf("no reason names the shape (reasons: %v)", out["reasons"])
	}
	for _, want := range []string{"first", "second"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the gate reason does not name %q: %s", want, reason)
		}
	}
}

// The negative case, and the one that would be most expensive to get wrong: a directory the destination already
// carries is landed work, not a second changeset. A rule that counted it would refuse every branch cut from a
// branch whose work landed after the branch point, which is most branches, and it would say so in the gate.
func TestChangeReadyAcceptsAChangesetTheDestinationCarriedAfterTheBranchPoint(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("other")
	f.CommitChangeset("other", "main")
	f.Commit("other work", gittest.WithFile("o.go", "package main\n"))

	f.CreateBranch("work", "other")
	f.Commit("work changeset", gittest.WithFiles(map[string]string{
		"changesets/work/CHANGESET.yaml": "id: work\nbase: other\n",
		"changesets/work/ABOUT.md":       "# work\n",
	}), gittest.WithFile("w.go", "package main\n"))

	// `other` lands on its own, so this branch now carries the directory of landed work beside its own.
	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "-m", "other: land the branch", "other")
	f.SwitchTo("work")

	ready(t, f).mustSucceed(t, "change ready")
}

// The other case the rule must allow, and the one the destination read buys. A middle level can land on its own -
// its changeset commit cherry-picked or squashed by itself, which is how a reviewer takes one level of a stack and
// not the work below it - and then the child records a parent that is landed while the level under that is still
// unlanded work. From the child's branch that is two directories nothing on the branch connects, because the link
// is in the landed parent's file. An ordinary merge of the middle branch would have landed the ancestry too and made
// the read unnecessary, which is why this fixture lands the middle level by itself rather than merging its branch.
func TestChangeReadyAcceptsAChildWhoseLandedParentLandedWithoutItsOwn(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("auth")
	f.CommitChangeset("auth", "main")
	f.Commit("auth work", gittest.WithFile("auth.go", "package main\n"))

	f.CreateBranch("auth-tests", "auth")
	f.Commit("auth-tests changeset", gittest.WithFiles(map[string]string{
		"changesets/auth-tests/CHANGESET.yaml": "id: auth-tests\nbase: auth\nbase-changeset: auth\n",
		"changesets/auth-tests/ABOUT.md":       "# auth-tests\n",
	}), gittest.WithFile("auth_test.go", "package main\n"))
	landing := f.Head() // the commit that adds changesets/auth-tests/, and nothing above it

	f.SwitchTo("main")
	f.MustGit("cherry-pick", landing)
	f.SwitchTo("auth-tests")

	f.CreateBranch("auth-cases", "auth-tests")
	f.Commit("cases changeset", gittest.WithFiles(map[string]string{
		"changesets/auth-cases/CHANGESET.yaml": "id: auth-cases\nbase: auth-tests\nbase-changeset: auth-tests\n",
		"changesets/auth-cases/ABOUT.md":       "# auth-cases\n",
	}), gittest.WithFile("case.go", "package main\n"))

	ready(t, f).mustSucceed(t, "change ready")
}

// The case the rule exists to allow, and the reason `init` judges the shape with the record it is about to write:
// a child stacked on the changeset its branch carries is one changeset plus its ancestry, not two.
func TestChangeReadyAcceptsAChildStackedOnTheChangesetItsBranchCarries(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	f.CreateBranch("booking-tests")

	runIn(t, f.Dir(), "init", "--parent", "booking").mustSucceed(t, "init")
	if md := f.Read("changesets/booking-tests/CHANGESET.yaml"); !strings.Contains(md, "base-changeset: booking\n") {
		t.Fatalf("the stack was not recorded, so this proves nothing about stacks:\n%s", md)
	}

	ready(t, f).mustSucceed(t, "change ready")
}
