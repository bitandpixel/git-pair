package cli_test

import (
	"testing"

	"gitpair/internal/gittest"
)

// A refusal used to state one thing: that the merge may not happen. The reader could not tell whether a
// reviewer had said no, or whether a reviewer had said yes and something else had moved underneath them —
// the two questions that decide whether the next visit is to `review open` or to the branch. `git pair
// status` answered it, and only by being a second command: `check --json` already carries `state` beside
// `ready` for exactly this reason ("the answer" and "why a reader may want to look further"), and the human
// screen was the one surface that dropped it.
//
// `declarable` is the same argument on the machine side. The three questions about a hand-off are "may this
// merge", "did the author ask", and "is a declaration wanted here"; the first two were in `check --json` and
// the third cost a `change integrate` and its exit code. It is the predicate the hand-off line is printed by,
// not a second reading, and these tests pin it against the command's own answer rather than against a copy of
// its rules.

func TestCheckNamesTheStateItRefusedBesideTheVerdict(t *testing.T) {
	t.Run("a reviewer answered with feedback", func(t *testing.T) {
		f, _ := newChangeset(t, "booking-transaction", "main")
		ready(t, f)
		submit(t, f, "feedback")

		out := runIn(t, f.Dir(), "check")
		if out.code != exitRefusal {
			t.Fatalf("check exited %d, want %d\n%s", out.code, exitRefusal, out.stdout)
		}
		mustContain(t, out.stdout, "\nstate: FEEDBACK\n",
			"a refusal has to say that the newest review is what is blocking, not merely that it is blocking")
		if got := runIn(t, f.Dir(), "check", "--json").json(t)["state"]; got != "FEEDBACK" {
			t.Errorf("`--json` state = %v, want the same word the human screen printed", got)
		}
	})

	t.Run("content moved after the approval", func(t *testing.T) {
		f, _ := newChangeset(t, "booking-transaction", "main")
		ready(t, f)
		submit(t, f, "approve")
		f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { transaction() }\n"))

		out := runIn(t, f.Dir(), "check")
		mustContain(t, out.stdout, "- content outside changesets/booking-transaction/ changed since",
			"the drift bullet is the reason, and it stays the reason")
		mustContain(t, out.stdout, "\nstate: WORKING\n",
			"the approval no longer speaks about this head, and the state line is what says so")

		// The two screens read the same marker differently and are allowed to: `status` reports what the
		// newest marker is, `check` reports what the tree can still be held to. The disagreement is
		// documented rather than accidental, so it is pinned here as well as described.
		status := runIn(t, f.Dir(), "status").mustSucceed(t, "status").stdout
		mustContain(t, status, "State: APPROVED",
			"`status` keeps reporting the approval, which is the reading `check`'s WORKING is qualified against")
	})

	t.Run("the changeset has ended", func(t *testing.T) {
		f, _ := newChangeset(t, "booking-transaction", "main")
		ready(t, f)
		runIn(t, f.Dir(), "change", "abandon").mustSucceed(t, "change abandon")

		out := runIn(t, f.Dir(), "check")
		mustContain(t, out.stdout, "- changeset was abandoned by", "the ending is the only reason")
		mustContain(t, out.stdout, "\nstate: WORKING\n",
			"an ended changeset reports WORKING beside the ending, the way status reports it")
	})

	t.Run("offered for review and never read", func(t *testing.T) {
		f, _ := newChangeset(t, "booking-transaction", "main")
		ready(t, f)

		out := runIn(t, f.Dir(), "check")
		mustContain(t, out.stdout, "\nstate: READY\n",
			"the difference between this and a feedback refusal is the whole next step, and it is not in the bullet")
	})
}

// The passing verdict already states everything the state line would add: the gate passed, so whatever the
// newest marker was, it permits the merge. Printing it there would make a field that exists to disambiguate a
// refusal into decoration on the screen people read most.
func TestCheckLeavesTheStateOffAPassingVerdict(t *testing.T) {
	f, _, _, _ := approvedChangeset(t)

	out := runIn(t, f.Dir(), "check").mustSucceed(t, "check")
	mustNotContain(t, out.stdout, "state:",
		"only the refusal is ambiguous about the review, so only the refusal names the state")
	mustContain(t, out.stdout, "OK: booking-transaction is integration-ready", "the verdict line stays the first line")
}

// Each shape is asserted twice over: the field, and the command whose behaviour the field claims to describe.
// A `declarable` that disagrees with `change integrate` is worse than no field, because the reader follows the
// field and then trusts the command it pointed at.
func TestCheckReportsWhetherADeclarationIsWanted(t *testing.T) {
	t.Run("approved, clean, not yet handed over", func(t *testing.T) {
		f, _ := newChangeset(t, "booking-transaction", "main")
		ready(t, f)
		submit(t, f, "approve")

		verdict := runIn(t, f.Dir(), "check", "--json").json(t)
		if verdict["ready"] != true || verdict["integrating"] != false || verdict["declarable"] != true {
			t.Fatalf("verdict = %v, want ready, not integrating, declarable", verdict)
		}
		runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change integrate")
	})

	t.Run("already declared at this head", func(t *testing.T) {
		f, _ := newChangeset(t, "booking-transaction", "main")
		ready(t, f)
		submit(t, f, "approve")
		runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change integrate")

		verdict := runIn(t, f.Dir(), "check", "--json").json(t)
		if verdict["integrating"] != true || verdict["declarable"] != false {
			t.Fatalf("verdict = %v, want integrating and nothing further to declare", verdict)
		}
		// False here is not a refusal: the command accepts the request and records nothing, which is what
		// lets a script declare unconditionally. `integrating` is the field that says what is true instead.
		if code := runIn(t, f.Dir(), "change", "integrate").code; code != 0 {
			t.Errorf("`change integrate` exited %d on a head already declared, want the no-op success", code)
		}
	})

	t.Run("a child whose parent has not landed", func(t *testing.T) {
		f := approvedChildOnAnUnlandedParent(t)

		verdict := runIn(t, f.Dir(), "check", "--json").json(t)
		if verdict["ready"] != true || verdict["integrating"] != false || verdict["declarable"] != false {
			t.Fatalf("verdict = %v, want the merge permitted, no ask written, and no declaration wanted", verdict)
		}
		res := runIn(t, f.Dir(), "change", "integrate")
		if res.code != exitRefusal {
			t.Fatalf("`change integrate` exited %d, want the refusal the field predicted:\n%s", res.code, res.stdout)
		}
		mustContain(t, res.stdout, "is not landed on the integration branch",
			"the field and the refusal have one source, so a reader following one gets the other's reason")
	})

	t.Run("a reviewer answered with feedback", func(t *testing.T) {
		f, _ := newChangeset(t, "booking-transaction", "main")
		ready(t, f)
		submit(t, f, "feedback")

		verdict := runIn(t, f.Dir(), "check", "--json").json(t)
		if verdict["declarable"] != false {
			t.Errorf("declarable = true beside a feedback verdict, want false: the ask would be refused")
		}
		if code := runIn(t, f.Dir(), "change", "integrate").code; code != exitRefusal {
			t.Errorf("`change integrate` exited %d, want %d", code, exitRefusal)
		}
	})

	t.Run("content moved after the approval", func(t *testing.T) {
		f, _ := newChangeset(t, "booking-transaction", "main")
		ready(t, f)
		submit(t, f, "approve")
		f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { transaction() }\n"))

		verdict := runIn(t, f.Dir(), "check", "--json").json(t)
		if verdict["ready"] != false || verdict["declarable"] != false {
			t.Fatalf("verdict = %v, want the gate refused and no declaration wanted", verdict)
		}
		if code := runIn(t, f.Dir(), "change", "integrate").code; code != exitRefusal {
			t.Errorf("`change integrate` exited %d, want %d", code, exitRefusal)
		}
	})

	t.Run("the approved commit was rewritten out of the history", func(t *testing.T) {
		f, _, _, _ := approvedChangeset(t)
		// Every SHA changes and no file does, which is the shape only the ancestry test refuses — and the
		// shape the screens' cheaper predicate would call declarable, because the newest marker is still an
		// approval and the tree moved nowhere. The field is the gate's answer, so it does not.
		f.CreateBranch("trunk-advanced", "main")
		f.Commit("nothing to see", gittest.WithEmpty())
		f.SwitchTo("booking-transaction")
		f.MustGit("rebase", "trunk-advanced")

		verdict := runIn(t, f.Dir(), "check", "--json").json(t)
		if verdict["ready"] != false || verdict["declarable"] != false {
			t.Fatalf("verdict = %v, want the rewrite refused and no declaration offered", verdict)
		}
		mustContain(t, verdict["reasons"].([]any)[0].(string), "no longer in this history",
			"the reason is the rewritten history, so the two assertions above are about that case")
		if code := runIn(t, f.Dir(), "change", "integrate").code; code != exitRefusal {
			t.Errorf("`change integrate` exited %d on a rewritten branch, want %d", code, exitRefusal)
		}
	})

	t.Run("the work is already in the destination", func(t *testing.T) {
		f, slug := newChangeset(t, "booking-transaction", "main")
		ready(t, f)
		submit(t, f, "approve")
		landApproved(t, f, slug, "main")
		// The landing was performed from `main`, so the checkout is standing there and asks about a branch
		// that holds no work in progress.
		f.SwitchTo(slug)

		// The command does not answer this question at all, which is the strongest form of the field being
		// false: there is no verdict to read and no declaration to make, and the hint is the one that names
		// the landing. An agent that ran `check --json` here gets a refusal from the exit code, not a
		// `declarable` to follow into a doomed `change integrate`.
		res := runIn(t, f.Dir(), "check", "--json")
		if res.code == 0 || res.stdout != "" {
			t.Fatalf("check --json answered for landed work (exit %d):\n%s", res.code, res.stdout)
		}
		mustContain(t, res.stderr, "that changeset landed", "the refusal names where the work went")
		if code := runIn(t, f.Dir(), "change", "integrate").code; code == 0 {
			t.Errorf("`change integrate` accepted landed work, want the refusal the screen steers away from")
		}
	})

	t.Run("the changeset has ended", func(t *testing.T) {
		f, _ := newChangeset(t, "booking-transaction", "main")
		ready(t, f)
		submit(t, f, "approve")
		runIn(t, f.Dir(), "change", "abandon").mustSucceed(t, "change abandon")

		verdict := runIn(t, f.Dir(), "check", "--json").json(t)
		if verdict["declarable"] != false {
			t.Errorf("declarable = true on an abandoned changeset, want false")
		}
		if code := runIn(t, f.Dir(), "change", "integrate").code; code != exitRefusal {
			t.Errorf("`change integrate` exited %d, want %d", code, exitRefusal)
		}
	})
}
