package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// landAndRecord offers, approves, lands the checked-out branch on target with a merge, and records the
// pair — the whole §29 loop, so a chain test can talk about the chain instead of about setup.
func landAndRecord(t *testing.T, f *gittest.Fixture, slug, target string) string {
	t.Helper()
	ready(t, f)
	submit(t, f, "approve")
	source := f.Head()
	f.SwitchTo(target)
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "land "+slug, slug)
	landing := f.Head()
	// `--changeset` because a stacked child's source carries its parents' directories too, which is the
	// case the flag exists for.
	runIn(t, f.Dir(), "integration", "record", "--changeset", slug, "--source", source, "--commit", landing,
		"--target", target).mustSucceed(t, "integration", "record")
	return landing
}

// stackedChangeset starts a changeset on a new branch off main and says which changeset it sits on. The
// yaml is written by hand rather than by CommitChangeset because `parent:` replaces `base:` — the two are
// two answers to the same question, and the tool refuses a file that gives both.
func stackedChangeset(t *testing.T, f *gittest.Fixture, slug, parentSlug, parentBranch, work string) {
	t.Helper()
	f.CreateBranch(slug, "main")
	yaml := "id: " + slug + "\n"
	if parentSlug != "" {
		yaml += "parent: " + parentBranch + "\nparent-changeset: " + parentSlug + "\n"
	} else {
		yaml += "base: main\n"
	}
	f.Commit("changeset "+slug, gittest.WithFiles(map[string]string{
		"changesets/" + slug + "/CHANGESET.yaml": yaml,
		"changesets/" + slug + "/ABOUT.md":       "# " + slug + "\n",
	}), gittest.WithFile(work, "package main\n"))
}

// A landed child's own two refs say what it became and where its reviewed chain is. They do not say
// whether any of it reached the integration branch: that is a fact about its parent's record, and before
// this the reader had to find the parent's id in CHANGESET.yaml and go ask.
func TestStatusPrintsTheRecordedStackChain(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	la := landAndRecord(t, f, "alpha", "main")

	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")
	lb := landAndRecord(t, f, "beta", "main")
	stackedChangeset(t, f, "gamma", "beta", "beta", "g.go")
	landAndRecord(t, f, "gamma", "main")

	res := runIn(t, f.Dir(), "status", "--changeset", "gamma")
	res.mustSucceed(t, "status")
	mustContain(t, res.stdout, "Stack:", "a landed child has a stack to report")
	mustContain(t, res.stdout, "record: beta (branch beta) -> "+shortOf(lb)+", reachable from main",
		"the nearest ancestor first, with the commit its own record names")
	mustContain(t, res.stdout, "record: alpha (branch alpha) -> "+shortOf(la)+", reachable from main",
		"and the chain continues through the ancestor's own yaml, one step per changeset")

	steps := runIn(t, f.Dir(), "status", "--changeset", "gamma", "--json").jsonList(t, "stack")
	if len(steps) != 2 {
		t.Fatalf("stack has %d steps, want the two ancestors: %v", len(steps), steps)
	}
	for i, want := range []struct {
		id, commit string
	}{{"beta", lb}, {"alpha", la}} {
		step, ok := steps[i].(map[string]any)
		if !ok {
			t.Fatalf("stack step %d is not an object: %v", i, steps[i])
		}
		if step["changeset"] != want.id {
			t.Errorf("stack step %d is %v, want %s", i, step["changeset"], want.id)
		}
		if step["integration_commit"] != want.commit {
			t.Errorf("stack step %d records %v, want %s", i, step["integration_commit"], want.commit)
		}
		if step["branch_exists"] != true {
			t.Errorf("stack step %d says the branch is gone: %v", i, step)
		}
		if step["in_default_branch"] != true {
			t.Errorf("stack step %d says the record does not reach trunk: %v", i, step)
		}
	}
}

// The branch name in the child's yaml outlives the branch. The chain has to say which of its two names for
// a parent — the branch and the record — is still there, because "the branch is gone" and "this clone has
// never seen the branch" look the same from a ref lookup and mean different things to the reader.
func TestStatusStackChainReportsAGoneParentBranch(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")
	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")
	landAndRecord(t, f, "beta", "main")

	f.MustGit("branch", "-d", "beta", "alpha")

	res := runIn(t, f.Dir(), "status", "--changeset", "beta")
	res.mustSucceed(t, "status")
	mustContain(t, res.stdout, "record: alpha (branch alpha is gone)",
		"the branch the child was stacked on has been tidied away")
	mustContain(t, res.stdout, "reachable from main",
		"and the record still says where the work reached")

	step := runIn(t, f.Dir(), "status", "--changeset", "beta", "--json").jsonList(t, "stack")[0].(map[string]any)
	if step["branch_exists"] != false {
		t.Errorf("branch_exists is %v after the branch was deleted", step["branch_exists"])
	}
}

// An ancestor with no record here is a finding, not a gap to hide: the child's chain stops being provable
// at that step, and `--fetch` is what closes it.
func TestStatusStackChainNamesAnAncestorWithNoRecord(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")
	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")
	landAndRecord(t, f, "beta", "main")
	f.MustGit("update-ref", "-d", "refs/git-pair/integrations/alpha")

	res := runIn(t, f.Dir(), "status", "--changeset", "beta")
	res.mustSucceed(t, "status")
	mustContain(t, res.stdout, "record: alpha", "the step is still there")
	mustContain(t, res.stdout, "no record in this clone", "and says what is missing")
	mustContain(t, res.stdout, "--fetch", "and the command that fixes it")
}

// CHANGESET.yaml is committed content, so `parent-changeset` can be edited into a loop. The walk is
// bounded and says so, rather than either hanging or printing a short list that reads as the whole chain.
func TestStatusStackChainStopsOnACycle(t *testing.T) {
	f := newRepo(t)
	stackedChangeset(t, f, "ouroboros", "ouroboros", "main", "o.go")
	landAndRecord(t, f, "ouroboros", "main")

	res := runIn(t, f.Dir(), "status", "--changeset", "ouroboros")
	res.mustSucceed(t, "status")
	mustContain(t, res.stdout, "named twice", "a stack that names its own ancestor says so")
	if strings.Contains(res.stdout, "record: ouroboros ->") {
		t.Errorf("the cycle was walked once as a real step:\n%s", res.stdout)
	}
}

// An unstacked changeset has no chain, and `stack` answers `[]` rather than null or nothing: the empty list
// is the answer "this sat on the integration branch", and a missing key would be a different answer.
func TestStatusStackChainIsEmptyForAnUnstackedChangeset(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")

	res := runIn(t, f.Dir(), "status", "--changeset", "alpha")
	res.mustSucceed(t, "status")
	mustNotContain(t, res.stdout, "record: ", "nothing to walk, nothing to print")
	if got := runIn(t, f.Dir(), "status", "--changeset", "alpha", "--json").jsonList(t, "stack"); len(got) != 0 {
		t.Errorf("stack is %v for an unstacked changeset, want the empty list", got)
	}
}

// The chain is a read on top of a read, so its cost belongs to it: one listing of the branch names for the
// whole walk, then one `CHANGESET.yaml` read and one `merge-base` per ancestor. Measured at four invocations
// for a third ancestor, against the nine the two-ancestor chain costs over an unstacked changeset in the
// same repository — the remainder is the deeper changeset's own history, which the walk does not touch.
// Bounding the marginal is the point: a walk that re-listed the namespace per step, or followed a chain
// twice, would blow through this on a stack of any depth.
func TestStatusStackChainCostsABoundedReadPerStep(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")
	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")
	landAndRecord(t, f, "beta", "main")
	stackedChangeset(t, f, "gamma", "beta", "beta", "g.go")
	landAndRecord(t, f, "gamma", "main")

	count := f.SpawnShim(t)
	runIn(t, f.Dir(), "status", "--changeset", "gamma").mustSucceed(t, "status")
	two := count()

	stackedChangeset(t, f, "delta", "gamma", "gamma", "d.go")
	landAndRecord(t, f, "delta", "main")
	count = f.SpawnShim(t)
	runIn(t, f.Dir(), "status", "--changeset", "delta").mustSucceed(t, "status")
	if marginal := count() - two; marginal > 5 {
		t.Errorf("one more ancestor cost %d git invocations, want at most 5 (one yaml read, one merge-base, and the deeper changeset's own work)", marginal)
	}
}
