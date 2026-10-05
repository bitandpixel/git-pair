package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// landAndRecord offers, approves, and lands the checked-out branch on target with a merge — the whole
// §29 loop, so a chain test can talk about the chain instead of about setup. The name keeps "record"
// because the loop it sets up is the one that used to end in `integration record`; what ends it now is
// the merge, since the directory in the destination is the record.
func landAndRecord(t *testing.T, f *gittest.Fixture, slug, target string) string {
	t.Helper()
	ready(t, f)
	submit(t, f, "approve")
	f.SwitchTo(target)
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "land "+slug, slug)
	landing := f.Head()
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
	mustContain(t, res.stdout, "landed: beta (branch beta) -> "+shortOf(lb)+", reachable from main",
		"the nearest ancestor first, with the commit its own record names")
	mustContain(t, res.stdout, "landed: alpha (branch alpha) -> "+shortOf(la)+", reachable from main",
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
		if step["landed_commit"] != want.commit {
			t.Errorf("stack step %d landed %v, want %s", i, step["landed_commit"], want.commit)
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
	mustContain(t, res.stdout, "landed: alpha (branch alpha is gone)",
		"the branch the child was stacked on has been tidied away")
	mustContain(t, res.stdout, "reachable from main",
		"and the record still says where the work reached")

	step := runIn(t, f.Dir(), "status", "--changeset", "beta", "--json").jsonList(t, "stack")[0].(map[string]any)
	if step["branch_exists"] != false {
		t.Errorf("branch_exists is %v after the branch was deleted", step["branch_exists"])
	}
}

// Deleting the durable record of an ancestor changes nothing about its chain. The landing is a fact of the
// integration branch's tree, so the step still reports it and the walk still continues from the destination.
func TestStatusStackChainIgnoresADeletedRecord(t *testing.T) {
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
	mustContain(t, res.stdout, "landed: alpha (branch alpha) -> ", "the landing is still reported with its commit")
	mustContain(t, res.stdout, "reachable from main", "and still placed in the integration branch")
	mustNotContain(t, res.stdout, "--fetch", "nothing is fetched to answer this; the destination is read as it is")
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
	if strings.Contains(res.stdout, "landed: ouroboros ->") {
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

// The chain is a read on top of a read, so its cost belongs to it: one listing of the branch names for
// the whole walk, then two tree reads (does the destination carry the ancestor, and which commit brought
// it in) and one `CHANGESET.yaml` read per ancestor. Measured at 10 invocations per step once the chain is
// three deep, and flat beyond that — 90, 97, 104 for depths 3, 4 and 5 in this repository.
//
// Ten rather than the five this test was written against: the content comparison a stacked child is now
// asked for needs each ancestor's directory (so the review record can be left out of the digest), which is
// the same two tree reads per ancestor the chain walk already pays, plus a fixed handful for the merge
// bases, the ancestry comparisons between them, the digest itself and the merge probe. The marginal is
// still per-ancestor and constant, which is the property this test bounds.
//
// The assertion is the marginal at steady state, not the cost of a repository that grew: a run measured
// straight after one more landing has been added also pays for the extra commits the status walk now
// reads over, which is the changeset's own history and not the chain's. A walk that re-derived a chain per
// step, listed the namespace per step, or followed the chain twice would not be flat.
func TestStatusStackChainCostsABoundedReadPerStep(t *testing.T) {
	ids := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	f := newRepo(t)
	for i, id := range ids {
		if i == 0 {
			f.CreateBranch(id)
			f.CommitChangeset(id, "main")
			f.Commit(id+" work", gittest.WithFile(id+".go", "package main\n"))
		} else {
			stackedChangeset(t, f, id, ids[i-1], ids[i-1], id+".go")
		}
		landAndRecord(t, f, id, "main")
	}

	const budget = 10
	measured := map[string]int{}
	for _, id := range []string{"gamma", "delta", "epsilon"} {
		count := f.SpawnShim(t)
		runIn(t, f.Dir(), "status", "--changeset", id).mustSucceed(t, "status")
		measured[id] = count()
		if got := measured[id] - measured["gamma"]; id != "gamma" && got > budget*(indexIn(ids, id)-indexIn(ids, "gamma")) {
			t.Errorf("%s costs %d invocations over %s, more than %d per extra ancestor: the walk is not one bounded read per step",
				id, measured[id], "gamma", budget)
		}
	}
	if measured["delta"] <= measured["gamma"] {
		t.Errorf("delta cost %d and gamma %d: the counter measured nothing, so the bound above proves nothing",
			measured["delta"], measured["gamma"])
	}
}

func indexIn(xs []string, want string) int {
	for i, x := range xs {
		if x == want {
			return i
		}
	}
	return -1
}

func TestZZCostProfile(t *testing.T) {
	ids := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	f := newRepo(t)
	for i, id := range ids {
		if i == 0 {
			f.CreateBranch(id)
			f.CommitChangeset(id, "main")
			f.Commit(id+" work", gittest.WithFile(id+".go", "package main\n"))
		} else {
			stackedChangeset(t, f, id, ids[i-1], ids[i-1], id+".go")
		}
		landAndRecord(t, f, id, "main")
	}
	for i, id := range ids {
		count := f.SpawnShim(t)
		runIn(t, f.Dir(), "status", "--changeset", id).mustSucceed(t, "status")
		t.Logf("depth %d (%s): %d invocations", i+1, id, count())
	}
}
