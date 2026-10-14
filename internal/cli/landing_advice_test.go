package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The landing contract (PRD §29) told a stacked child to merge into its parent's branch, which is where
// `git pair change integrate` refused to declare it: a child lands where its parent landed, and until the
// parent has, that branch is one review can still rewrite. Four commands printed the sentence, so the fix is
// tested on all four, and the shape that was never wrong — work measured against the integration branch — is
// tested alongside so the fix can be seen to be about the stack and not about the sentence.
//
// Reported from the queue, where an approved child sat in INTEGRATING with a next step pointing at a branch
// its own declaration could not ask for (see `changesets/feat-stacked-landing-advice/ABOUT.md`).

// stackedApprovedPair builds alpha and a child beta cut from it, with beta approved and alpha not yet merged
// anywhere. It leaves HEAD on beta, which is where all four surfaces are then read.
func stackedApprovedPair(t *testing.T, f *gittest.Fixture) {
	t.Helper()
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	ready(t, f)
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	ready(t, f)
	submit(t, f, "approve")
}

// nextLines collects what each surface printed for its next step. They are collected together because the
// property that matters is that they agree, and a test that read one screen would pass on a fix that reached
// only that screen.
func nextLines(t *testing.T, f *gittest.Fixture) map[string]string {
	t.Helper()
	out := map[string]string{
		"status":  lastPrintedLine(t, runIn(t, f.Dir(), "status").stdout),
		"check":   lastPrintedLine(t, runIn(t, f.Dir(), "check").stdout),
		"wait":    lastPrintedLine(t, runIn(t, f.Dir(), "change", "wait").stdout),
		"submit":  lastPrintedLine(t, runIn(t, f.Dir(), "review", "submit", "--approve").stdout),
		"json":    stringField(t, runIn(t, f.Dir(), "check", "--json").json(t), "next_action"),
		"jsonst":  stringField(t, runIn(t, f.Dir(), "status", "--json").json(t), "next_action"),
		"jsonwt":  stringField(t, runIn(t, f.Dir(), "change", "wait", "--json").json(t), "next_action"),
		"jsonsub": stringField(t, runIn(t, f.Dir(), "review", "submit", "--approve", "--json").json(t), "next_action"),
	}
	return out
}

func lastPrintedLine(t *testing.T, out string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	t.Fatalf("output has no line to read: %q", out)
	return ""
}

func stringField(t *testing.T, doc map[string]any, key string) string {
	t.Helper()
	got, _ := doc[key].(string)
	return got
}

// The waiting sentence, in the words it has to have: it names the parent, sends the reader to the parent's
// own screen for the parent's step rather than spelling a second copy of the landing contract here, and then
// names where this changeset's own merge will go — the integration branch, not the parent's branch.
const waitingBeta = "waiting on parent alpha: its landing is owed first (`git pair status --changeset alpha` " +
	"names the step), then `git pair check` and merge into main"

func TestApprovedChildOfAnUnlandedParentIsToldTheParentLandsFirst(t *testing.T) {
	f := newRepo(t)
	stackedApprovedPair(t, f)

	for name, line := range nextLines(t, f) {
		if !strings.Contains(line, waitingBeta) {
			t.Errorf("%s next step is:\n\t%s\nwant the waiting sentence:\n\t%s", name, line, waitingBeta)
			continue
		}
		// The sentence this replaces, spelled as it was: the child's base, offered as a merge target.
		if strings.Contains(line, "merge into alpha") {
			t.Errorf("%s still offers the parent's branch as a merge target: %s", name, line)
		}
	}
}

// The control the change has to leave alone: an unstacked changeset is merged into its base, in the sentence
// that has always been right, with nothing waiting and nothing appended.
func TestUnstackedLandingAdviceIsTheBaseAndAMerge(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	ready(t, f)
	submit(t, f, "approve")

	const want = "`git pair check`, then merge into main with ordinary git"
	for name, line := range map[string]string{
		"status": lastPrintedLine(t, runIn(t, f.Dir(), "status").stdout),
		"check":  lastPrintedLine(t, runIn(t, f.Dir(), "check").stdout),
		"wait":   lastPrintedLine(t, runIn(t, f.Dir(), "change", "wait").stdout),
		"json":   stringField(t, runIn(t, f.Dir(), "check", "--json").json(t), "next_action"),
	} {
		if !strings.HasSuffix(line, want) {
			t.Errorf("%s next step is %q, want it to end in %q", name, line, want)
		}
		if strings.Contains(line, "waiting on parent") {
			t.Errorf("%s says a parent is waiting for work that has none: %s", name, line)
		}
	}
}

// Once the parent has landed, the child's next step is its own merge — and it names the same branch
// `git pair change integrate` puts on its `merge into:` line, which is the pair that has to agree: the
// sentence an author is told and the declaration they will run.
func TestChildOfALandedParentIsToldWhereItsOwnMergeGoes(t *testing.T) {
	f := newRepo(t)
	stackedApprovedPair(t, f)
	f.SwitchTo("alpha")
	landApproved(t, f, "alpha", "main")
	f.SwitchTo("beta")

	const want = "`git pair check`, then merge into main with ordinary git (where alpha landed)"
	for name, line := range map[string]string{
		"status": lastPrintedLine(t, runIn(t, f.Dir(), "status").stdout),
		"check":  lastPrintedLine(t, runIn(t, f.Dir(), "check").stdout),
		"wait":   lastPrintedLine(t, runIn(t, f.Dir(), "change", "wait").stdout),
		"json":   stringField(t, runIn(t, f.Dir(), "check", "--json").json(t), "next_action"),
	} {
		if !strings.HasSuffix(line, want) {
			t.Errorf("%s next step is %q, want it to end in %q", name, line, want)
		}
		if strings.Contains(line, "waiting on parent") {
			t.Errorf("%s still waits on a parent whose work is in the destination: %s", name, line)
		}
	}

	// The declaration and the sentence are read from one rule: if these two lines ever disagree, an author
	// is told one destination and handed another.
	declared := runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change integrate").stdout
	if !strings.Contains(declared, "merge into:  main (where alpha landed)") {
		t.Errorf("the declaration merges into something other than what the next step named:\n%s", declared)
	}
}

// A record that names a parent branch and no parent changeset is the shape `init` no longer writes and a
// hand-edited `CHANGESET.yaml` still makes. git-pair cannot show that branch has landed, so the advice keeps
// the waiting form and names the branch it can see — the same direction `change integrate` refuses in, rather
// than a sentence that guesses the parent is done.
func TestChildOfAParentWithNoRecordedChangesetWaitsOnTheBranch(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	ready(t, f)
	f.CreateBranch("beta")
	f.Commit("changeset beta", gittest.WithFiles(map[string]string{
		"changesets/beta/CHANGESET.yaml": "id: beta\nparent: alpha\n",
		"changesets/beta/ABOUT.md":       "# beta\n",
	}), gittest.WithFile("b.go", "package main\n"))
	ready(t, f)
	submit(t, f, "approve")

	line := lastPrintedLine(t, runIn(t, f.Dir(), "status").stdout)
	const want = "waiting on parent alpha: its landing is owed first (`git pair status --changeset alpha` names the step)"
	if !strings.Contains(line, want) {
		t.Errorf("status next step is %q, want it to contain %q", line, want)
	}
	if strings.Contains(line, "merge into alpha") {
		t.Errorf("status offers the parent's branch as a merge target: %s", line)
	}
}

// Feedback is the other state that ends in the landing sentence, and it is the state most of a stack's life
// is spent in. The waiting step has to survive the "optionally address feedback" prefix rather than be
// replaced by it.
func TestChildWithFeedbackIsStillToldWhichLandingIsNext(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	ready(t, f)
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	ready(t, f)
	submit(t, f, "feedback")

	line := lastPrintedLine(t, runIn(t, f.Dir(), "status").stdout)
	if !strings.HasPrefix(line, "Next: optionally address feedback") {
		t.Errorf("status next step is %q, want the feedback prefix kept", line)
	}
	if !strings.Contains(line, waitingBeta) {
		t.Errorf("status next step is %q, want it to carry the waiting landing step", line)
	}
	if strings.Contains(line, "merge into alpha") {
		t.Errorf("status offers the parent's branch as a merge target: %s", line)
	}
}

// A stack two deep, where the middle changeset has not landed either: the child's sentence names the parent
// it waits on and the branch the parent's own record says it is going to. It is the parent's turn, and the
// parent's turn is named — which is the point of the waiting form even when the queue below it is longer.
func TestDeepStackWaitsOnTheParentDirectlyBelow(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	ready(t, f)
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	ready(t, f)
	stackedChangeset(t, f, "gamma", "beta", "beta", "c.go")
	ready(t, f)
	submit(t, f, "approve")

	line := lastPrintedLine(t, runIn(t, f.Dir(), "status").stdout)
	if !strings.Contains(line, "waiting on parent beta:") {
		t.Errorf("status next step is %q, want it to name the parent directly below", line)
	}
	if strings.Contains(line, "merge into beta") || strings.Contains(line, "merge into alpha") {
		t.Errorf("status offers a stack branch as a merge target: %s", line)
	}
}

// The advice is advice: a parent whose branch is gone before its work reached the destination leaves the
// screen with a parent it cannot name a commit for, and it says that rather than inventing a place to merge
// to. The readings that refuse a merge are `check`'s and `change integrate`'s; this one keeps talking.
func TestChildOfADeletedUnlandedParentStillGetsAStep(t *testing.T) {
	f := newRepo(t)
	stackedApprovedPair(t, f)
	f.SwitchTo("main")
	f.MustGit("branch", "-D", "alpha")
	f.SwitchTo("beta")

	res := runIn(t, f.Dir(), "status")
	res.mustSucceed(t, "status")
	line := lastPrintedLine(t, res.stdout)
	if !strings.Contains(line, "Next:") {
		t.Errorf("status printed no next step: %q", res.stdout)
	}
	if !strings.Contains(line, "waiting on parent alpha") {
		t.Errorf("status lost the parent it can still see: %q", line)
	}
	if strings.Contains(line, "merge into alpha") {
		t.Errorf("status offers a branch that no longer exists as a merge target: %s", line)
	}
}
