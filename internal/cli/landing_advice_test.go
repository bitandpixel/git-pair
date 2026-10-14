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
		"status":  nextStepLine(t, runIn(t, f.Dir(), "status").stdout),
		"check":   nextStepLine(t, runIn(t, f.Dir(), "check").stdout),
		"wait":    nextStepLine(t, runIn(t, f.Dir(), "change", "wait").stdout),
		"submit":  nextStepLine(t, runIn(t, f.Dir(), "review", "submit", "--approve").stdout),
		"json":    stringField(t, runIn(t, f.Dir(), "check", "--json").json(t), "next_action"),
		"jsonst":  stringField(t, runIn(t, f.Dir(), "status", "--json").json(t), "next_action"),
		"jsonwt":  stringField(t, runIn(t, f.Dir(), "change", "wait", "--json").json(t), "next_action"),
		"jsonsub": stringField(t, runIn(t, f.Dir(), "review", "submit", "--approve", "--json").json(t), "next_action"),
	}
	return out
}

func nextStepLine(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if step, ok := strings.CutPrefix(trimmed, "Next: "); ok {
			return strings.TrimSpace(step)
		}
		if step, ok := strings.CutPrefix(trimmed, "next:"); ok {
			return strings.TrimSpace(step)
		}
	}
	t.Fatalf("output prints no next step:\n%s", out)
	return ""
}

// printedHandoff returns the hand-off line a screen printed under its next step, or "" when it printed none.
// The two screens indent it differently — under `Next: ` and under `next:  ` — so the test for it looks at
// what the line says rather than at how far in it sits.
func printedHandoff(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "or hand it to ") {
			return trimmed
		}
	}
	return ""
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

// The hand-off line, and the one property it has to hold: it appears exactly where `git pair change
// integrate` would record a declaration. A screen that offers the hand-off where the command refuses is the
// failure the design rejected — a `READY` changeset told the merge is available reads as permission to skip
// the review — and a screen that hides it where the command would accept leaves the second exit
// undiscoverable. So each row states both halves, read from the same fixture.
func TestHandoffLineAppearsExactlyWhereTheDeclarationWouldBeRecorded(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(t *testing.T, f *gittest.Fixture)
		want    bool
		exit    int
		records bool
	}{
		{"approved", func(t *testing.T, f *gittest.Fixture) {}, true, exitOK, true},
		{"never reviewed", func(t *testing.T, f *gittest.Fixture) {
			// Back to the offer: an approved changeset re-offered has no verdict covering the head.
			f.MustGit("reset", "--hard", "HEAD~1")
		}, false, exitRefusal, false},
		{"head moved since the approval", func(t *testing.T, f *gittest.Fixture) {
			f.Commit("a commit after the approval", gittest.WithFile("b.go", "package main\n"))
		}, false, exitRefusal, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := approvedUnstacked(t)
			tc.arrange(t, f)

			statusOut := runIn(t, f.Dir(), "status").stdout
			checkOut := runIn(t, f.Dir(), "check").stdout
			if got := printedHandoff(statusOut); (got != "") != tc.want {
				t.Errorf("status printed the hand-off %v, want %v:\n%s", got != "", tc.want, statusOut)
			}
			if got := printedHandoff(checkOut); (got != "") != tc.want {
				t.Errorf("check printed the hand-off %v, want %v:\n%s", got != "", tc.want, checkOut)
			}

			// The other half of the equivalence, from the command itself.
			declared := runIn(t, f.Dir(), "change", "integrate", "--json")
			if declared.code != tc.exit {
				t.Fatalf("change integrate exited %d, want %d\n%s%s", declared.code, tc.exit, declared.stdout, declared.stderr)
			}
			if tc.exit == exitOK {
				if got := declared.json(t)["recorded"]; got != tc.records {
					t.Errorf("recorded = %v, want %v", got, tc.records)
				}
			}
		})
	}
}

// The states the design excluded, asserted one at a time rather than as a leftover: each is a case where the
// command refuses, and the screen has to stay quiet.
func TestHandoffLineStaysQuietWhereTheDeclarationIsRefused(t *testing.T) {
	t.Run("child of an unlanded parent", func(t *testing.T) {
		f := newRepo(t)
		stackedApprovedPair(t, f)

		if got := printedHandoff(runIn(t, f.Dir(), "status").stdout); got != "" {
			t.Errorf("status offers the hand-off while the parent is unlanded: %q", got)
		}
		if got := printedHandoff(runIn(t, f.Dir(), "check").stdout); got != "" {
			t.Errorf("check offers the hand-off while the parent is unlanded: %q", got)
		}
		res := runIn(t, f.Dir(), "change", "integrate")
		if res.code != exitRefusal {
			t.Errorf("change integrate exited %d on an unlanded parent, want %d:\n%s", res.code, exitRefusal, res.stdout)
		}
	})

	t.Run("feedback instead of an approval", func(t *testing.T) {
		f := newRepo(t)
		f.CreateBranch("alpha")
		f.CommitChangeset("alpha", "main")
		f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
		ready(t, f)
		submit(t, f, "feedback")

		if got := printedHandoff(runIn(t, f.Dir(), "status").stdout); got != "" {
			t.Errorf("status offers the hand-off on a changeset the default policy cannot declare: %q", got)
		}
		if res := runIn(t, f.Dir(), "change", "integrate"); res.code != exitRefusal {
			t.Errorf("change integrate exited %d on feedback, want %d", res.code, exitRefusal)
		}
	})

	t.Run("head already declared", func(t *testing.T) {
		f := approvedUnstacked(t)
		runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change integrate")

		if got := printedHandoff(runIn(t, f.Dir(), "status").stdout); got != "" {
			t.Errorf("status offers the hand-off again after the hand-off: %q", got)
		}
		again := runIn(t, f.Dir(), "change", "integrate", "--json").json(t)
		if again["recorded"] != false {
			t.Errorf("the second declaration recorded a marker: %v", again)
		}
	})
}

// The machine contract the human line must not touch: `next_action` stays one instruction, so a consumer
// parsing it never has to decide between two exits.
func TestHandoffLineNeverEntersTheMachineAnswer(t *testing.T) {
	f := approvedUnstacked(t)

	for name, doc := range map[string]map[string]any{
		"status": runIn(t, f.Dir(), "status", "--json").json(t),
		"check":  runIn(t, f.Dir(), "check", "--json").json(t),
	} {
		action, _ := doc["next_action"].(string)
		if action == "" {
			t.Fatalf("%s --json printed no next_action: %v", name, doc)
		}
		if strings.Contains(action, "or hand it to") {
			t.Errorf("%s --json carries the human hand-off in next_action: %q", name, action)
		}
		if !strings.Contains(action, "merge into main with ordinary git") {
			t.Errorf("%s --json next_action is %q, want the landing sentence on its own", name, action)
		}
	}

	// The two lines name one destination, read from one contract: an author who reads both must not be
	// handed two branches.
	statusOut := runIn(t, f.Dir(), "status").stdout
	step, handoff := nextStepLine(t, statusOut), printedHandoff(statusOut)
	if handoff == "" {
		t.Fatal("status printed no hand-off line for an approved, declarable changeset")
	}
	if !strings.Contains(step, "merge into main") || !strings.Contains(handoff, "main's owner") {
		t.Errorf("the step and the hand-off name different destinations:\n  %s\n  %s", step, handoff)
	}
	if !strings.Contains(handoff, "git push origin alpha") {
		t.Errorf("the hand-off leaves out the push the other side has to be able to see the request: %q", handoff)
	}
}

// approvedUnstacked is a changeset approved on its own branch against main — the shape where the merge is
// the author's step and the hand-off is the alternative to it.
func approvedUnstacked(t *testing.T) *gittest.Fixture {
	t.Helper()
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	ready(t, f)
	submit(t, f, "approve")
	return f
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
		"status": nextStepLine(t, runIn(t, f.Dir(), "status").stdout),
		"check":  nextStepLine(t, runIn(t, f.Dir(), "check").stdout),
		"wait":   nextStepLine(t, runIn(t, f.Dir(), "change", "wait").stdout),
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
		"status": nextStepLine(t, runIn(t, f.Dir(), "status").stdout),
		"check":  nextStepLine(t, runIn(t, f.Dir(), "check").stdout),
		"wait":   nextStepLine(t, runIn(t, f.Dir(), "change", "wait").stdout),
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

	line := nextStepLine(t, runIn(t, f.Dir(), "status").stdout)
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

	line := nextStepLine(t, runIn(t, f.Dir(), "status").stdout)
	if !strings.HasPrefix(line, "optionally address feedback") {
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

	line := nextStepLine(t, runIn(t, f.Dir(), "status").stdout)
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
	line := nextStepLine(t, res.stdout)
	if line == "" {
		t.Errorf("status printed no next step: %q", res.stdout)
	}
	if !strings.Contains(line, "waiting on parent alpha") {
		t.Errorf("status lost the parent it can still see: %q", line)
	}
	if strings.Contains(line, "merge into alpha") {
		t.Errorf("status offers a branch that no longer exists as a merge target: %s", line)
	}
}
