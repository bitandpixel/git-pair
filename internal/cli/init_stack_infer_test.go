package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The trap this rule closes fired twice on one stack: a changeset created on a stacked branch with
// `--base <parent>` rather than `--parent <parent>`, so the file recorded a measurement base naming a branch
// nobody had declared a parent. When that branch landed, the field named finished work, `check` told the
// author to merge into it, and `change integrate` would have asked CI to. `init` can see the relationship
// from the base it was handed, so it records the pair PRD §21 spells a stack with instead of leaving the
// inference to whoever reads the file next.

func TestInitRecordsTheStackTheBaseReveals(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("impl", gittest.WithFile("service.go", "package main\n"))
	f.CreateBranch("booking-tests", "booking")

	res := runIn(t, f.Dir(), "init", "--base", "booking").mustSucceed(t, "init")

	md := f.Read("changesets/booking-tests/CHANGESET.yaml")
	for _, want := range []string{"base: booking\n", "base-changeset: booking\n"} {
		if !strings.Contains(md, want) {
			t.Errorf("CHANGESET.yaml is missing %q:\n%s", want, md)
		}
	}
	// The older spelling is read and never written. A file that arrived with `parent:` keeps answering, but a
	// file written from here on records the stack as a base plus the changeset recorded on it.
	if strings.Contains(md, "parent:") {
		t.Errorf("no `parent:` belongs in a file written now:\n%s", md)
	}
	// The inference is said out loud. An author who typed `--base` and got a stack needs to learn that at
	// the command, not in a review round on a file they believe they already wrote.
	mustContain(t, res.stdout+res.stderr, "stacked on it", "the notice naming the inference")
}

// Two unlanded changesets on the base is a different situation: one of them may be the parent and the other
// a sibling that shares the branch, and nothing in the repository says which. The base stands as written and
// every candidate is named, because the author can make that decision and git-pair cannot.
func TestInitRefusesToGuessAmongTwoChangesetsOnTheBase(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.CommitChangeset("booking-extra", "main")
	f.CreateBranch("booking-tests", "booking")

	res := runIn(t, f.Dir(), "init", "--base", "booking").mustSucceed(t, "init")

	md := f.Read("changesets/booking-tests/CHANGESET.yaml")
	if !strings.Contains(md, "base: booking\n") {
		t.Errorf("the authored base did not stand:\n%s", md)
	}
	if strings.Contains(md, "parent") {
		t.Errorf("a stack was guessed from two candidates:\n%s", md)
	}
	mustContain(t, res.stdout+res.stderr, "booking-extra", "the candidate it declined to pick")
}

// The common case must not acquire a new reading: a base naming the integration branch is not a stack, and
// the trunk carries no unlanded changeset for anyone to be stacked on.
func TestInitStillRecordsAPlainBaseAgainstTheIntegrationBranch(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("bookings")

	runIn(t, f.Dir(), "init", "--base", "main").mustSucceed(t, "init")

	md := f.Read("changesets/bookings/CHANGESET.yaml")
	if !strings.Contains(md, "base: main\n") {
		t.Errorf("CHANGESET.yaml:\n%s\nwant the plain base", md)
	}
	if strings.Contains(md, "parent") {
		t.Errorf("a changeset measured against the integration branch is not stacked:\n%s", md)
	}
}

// The relationship is authored, not derived. A second changeset appearing on the parent branch later is
// somebody else's decision to start work, and it must not move a field in a file a reviewer has already read
// or shift the base a diff was measured from.
func TestTheRecordedStackDoesNotMoveWhenTheParentGainsAChangeset(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("impl", gittest.WithFile("service.go", "package main\n"))
	f.CreateBranch("booking-tests", "booking")
	runIn(t, f.Dir(), "init", "--base", "booking").mustSucceed(t, "init")
	before := f.Read("changesets/booking-tests/CHANGESET.yaml")

	f.SwitchTo("booking")
	f.CommitChangeset("booking-extra", "main")
	f.SwitchTo("booking-tests")

	if after := f.Read("changesets/booking-tests/CHANGESET.yaml"); after != before {
		t.Errorf("the child's record moved when the parent gained a second changeset.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	status := runIn(t, f.Dir(), "status", "--changeset", "booking-tests", "--json").mustSucceed(t, "status").json(t)
	if status["base"] != "booking" {
		t.Errorf("base = %v, want booking: the parent branch still carries the work below this one", status["base"])
	}
}

// The advice for the files that already exist. This is a recommendation and not a reason: a changeset written
// before `init` recorded the link must not be held by it, and which key a file chose to name its parent with
// is not a condition a merge should depend on.
func TestCheckRecommendsTheStackLinkInitWouldHaveRecorded(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("impl", gittest.WithFile("service.go", "package main\n"))
	f.CreateBranch("booking-tests", "booking")
	// The shape `init` no longer produces, written the way an older `init` wrote it: the fixture commits
	// the metadata directly, so no inference runs.
	f.CommitChangeset("booking-tests", "booking")

	res := runIn(t, f.Dir(), "check", "--json")
	out := res.json(t)
	recs, ok := out["recommendations"].([]any)
	if !ok {
		t.Fatalf("recommendations is %T, want an array:\n%s", out["recommendations"], res.stdout)
	}
	if len(recs) != 1 {
		t.Fatalf("recommendations = %v, want the one stack link", recs)
	}
	mustContain(t, recs[0].(string), "git pair init --parent booking", "the command that fixes it")

	// The advice must not arrive as a reason, and must not change what the gate said.
	if reasons, _ := out["reasons"].([]any); len(reasons) == 0 {
		t.Errorf("this changeset is unreviewed, so the gate should have refused it regardless:\n%s", res.stdout)
	}
	for _, r := range out["reasons"].([]any) {
		if strings.Contains(r.(string), "record the stack") {
			t.Errorf("the advice was written as a reason, which would gate the merge:\n%s", res.stdout)
		}
	}

	human := runIn(t, f.Dir(), "check")
	mustContain(t, human.stdout, "recommend: base booking carries changeset booking", "the prefix that separates advice from a reason")
	if strings.Contains(human.stdout, "- base booking carries") {
		t.Errorf("the advice printed with the reason bullet on the human surface:\n%s", human.stdout)
	}
}

// The recorded id is what the ancestor drop and the base derivation both read, so a file whose id and whose base
// name different changesets is read two ways at once: the branch says one changeset is below this one, the id says
// another. Which is stale is the author's fact - the parent may have been renamed, or the file may have been
// restacked by hand - so this arrives as advice, and a reviewer cannot settle it from the diff any better.
func TestCheckRecommendsWhenTheRecordedIdAndItsBaseDisagree(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("feature/booking")
	f.CommitChangeset("booking", "main")
	f.Commit("impl", gittest.WithFile("service.go", "package main\n"))
	f.CreateBranch("feature/booking-tests", "feature/booking")
	f.WriteChangesetFile("booking-tests", "CHANGESET.yaml",
		"id: booking-tests\nbase: feature/booking\nbase-changeset: booking-renamed\n")
	f.Commit("tests work", gittest.WithFile("tests.go", "package main\n"))

	out := runIn(t, f.Dir(), "check", "--json").json(t)
	recs, ok := out["recommendations"].([]any)
	if !ok || len(recs) != 1 {
		t.Fatalf("recommendations = %v, want the one disagreement:\n%s", out["recommendations"], out["reasons"])
	}
	for _, want := range []string{"carries changeset booking,", "records booking-renamed", "--set-parent"} {
		if !strings.Contains(recs[0].(string), want) {
			t.Errorf("the advice omits %q:\n%s", want, recs[0])
		}
	}
	for _, r := range out["reasons"].([]any) {
		if strings.Contains(r.(string), "stale") {
			t.Errorf("the disagreement was written as a reason, which would gate the merge:\n%s", r)
		}
	}

	human := runIn(t, f.Dir(), "check")
	mustContain(t, human.stdout,
		"recommend: base feature/booking carries changeset booking, while this file records booking-renamed",
		"the prefix that separates advice from a reason")
	if strings.Contains(human.stdout, "- base feature/booking carries") {
		t.Errorf("the advice printed with the reason bullets on the human surface:\n%s", human.stdout)
	}
}

// The other half of the same claim: a changeset whose parent is recorded needs no advice, and a changeset
// measured against trunk needs none either. Silence is the ordinary case.
func TestCheckIsSilentWhenTheStackIsRecordedOrThereIsNoStack(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	f.CreateBranch("booking-tests")
	runIn(t, f.Dir(), "init", "--parent", "booking").mustSucceed(t, "init")

	out := runIn(t, f.Dir(), "check", "--json").json(t)
	if recs := out["recommendations"].([]any); len(recs) != 0 {
		t.Errorf("recommendations = %v for a changeset that recorded its parent", recs)
	}

	f.CreateBranch("plain")
	runIn(t, f.Dir(), "init").mustSucceed(t, "init")
	out = runIn(t, f.Dir(), "check", "--json").json(t)
	if recs := out["recommendations"].([]any); len(recs) != 0 {
		t.Errorf("recommendations = %v for a changeset measured against trunk", recs)
	}
}

// Three levels, each branch created from the branch under it, which is how a stack gets written and how this
// rule was found not to work: B off A leaves A's changeset directory in B's tree, so the base carried two
// unlanded changesets and `init` refused to pick. The one that names the other is the parent, and the answer
// is read from the chain the parent records - the same evidence `--parent` already uses.
func TestInitLooksPastTheAncestorsAStackedBaseCarries(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("feature/auth")
	f.CommitChangeset("feature-auth", "main")
	f.Commit("auth work", gittest.WithFile("auth.go", "package main\n"))
	f.CreateBranch("feature/auth-tests")
	f.Commit("auth-tests changeset", gittest.WithFiles(map[string]string{
		"changesets/feature-auth-tests/CHANGESET.yaml": "id: feature-auth-tests\nparent: feature/auth\nparent-changeset: feature-auth\n",
		"changesets/feature-auth-tests/ABOUT.md":       "# feature-auth-tests\n",
	}), gittest.WithFile("auth_test.go", "package main\n"))
	f.CreateBranch("feature/auth-cases", "feature/auth-tests")

	res := runIn(t, f.Dir(), "init", "--base", "feature/auth-tests").mustSucceed(t, "init")

	md := f.Read("changesets/feature-auth-cases/CHANGESET.yaml")
	// The level below is still written the older way in this fixture on purpose: a legacy file under a new one
	// is the state trunk is in, and the drop that finds the top of the chain has to read both spellings.
	for _, want := range []string{"base: feature/auth-tests\n", "base-changeset: feature-auth-tests\n"} {
		if !strings.Contains(md, want) {
			t.Errorf("CHANGESET.yaml is missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "parent:") {
		t.Errorf("no `parent:` belongs in a file written now:\n%s", md)
	}
	mustContain(t, res.stdout+res.stderr, "stacked on it", "the notice naming the inference")
}

// The limit of that filter, and it is a real one: with nothing recorded below the base, an ancestor and a
// sibling are the same shape - two directories, neither landed - and nothing in the repository orders them.
// The base stands as authored and both are named, so the author picks.
func TestInitStillRefusesWhenTheLevelBelowRecordsNoChain(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("feature/auth")
	f.CommitChangeset("feature-auth", "main")
	f.Commit("auth work", gittest.WithFile("auth.go", "package main\n"))
	f.CreateBranch("feature/auth-tests")
	f.Commit("auth-tests changeset", gittest.WithFiles(map[string]string{
		"changesets/feature-auth-tests/CHANGESET.yaml": "id: feature-auth-tests\nbase: feature/auth\n",
		"changesets/feature-auth-tests/ABOUT.md":       "# feature-auth-tests\n",
	}), gittest.WithFile("auth_test.go", "package main\n"))
	f.CreateBranch("feature/auth-cases", "feature/auth-tests")

	res := runIn(t, f.Dir(), "init", "--base", "feature/auth-tests").mustSucceed(t, "init")

	md := f.Read("changesets/feature-auth-cases/CHANGESET.yaml")
	if !strings.Contains(md, "base: feature/auth-tests\n") {
		t.Errorf("the authored base did not stand:\n%s", md)
	}
	if strings.Contains(md, "parent") {
		t.Errorf("a stack was guessed from an unrecorded chain:\n%s", md)
	}
	mustContain(t, res.stdout+res.stderr, "feature-auth", "the candidate it declined to pick")
}
