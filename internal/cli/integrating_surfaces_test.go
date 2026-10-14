package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// INTEGRATING is a state with three readers, and each one has to answer in its own words: `status` for the
// author standing on the branch, `queue` for whoever watches the repository, and `change unready` for the
// author who changed their mind. A state that only `check` understands is a state that surprises people —
// the author sees a next step that no longer exists, and the watcher sees approved work sitting in a review
// queue.

// declaredChangeset is the fixture every test here needs: an approved changeset with the author's
// declaration on its tip, which is the state nothing else in the suite produces.
func declaredChangeset(t *testing.T) (*gittest.Fixture, string, string) {
	t.Helper()
	f, slug, _, _ := approvedChangeset(t)
	runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate")
	return f, slug, f.Head()
}

// `status` is where an author looks after declaring, and the answer has to say what is now true rather
// than repeat the landing contract: the author's step is over, and the branch has to reach the people who
// can act on it.
func TestStatusReportsADeclaration(t *testing.T) {
	f, _, declared := declaredChangeset(t)

	res := runIn(t, f.Dir(), "status").mustSucceed(t, "status")
	mustContain(t, res.stdout, "INTEGRATING", "the state is the derivation's, not a label this command adds")
	mustContain(t, res.stdout, "ready to integrate at "+f.Short(declared),
		"and the answer names the commit that made it so")
	mustContain(t, res.stdout, "git push origin booking-transaction",
		"the next step is the push that makes the request visible, which git-pair never runs itself")
	mustNotContain(t, res.stdout, "merge into main with ordinary git",
		"and the screen stops handing the author a merge they have already asked somebody else to perform")

	j := runIn(t, f.Dir(), "status", "--json").json(t)
	if j["state"] != "INTEGRATING" {
		t.Errorf("state = %v, want INTEGRATING", j["state"])
	}
	if j["integrating"] != true {
		t.Errorf("integrating = %v, want true", j["integrating"])
	}
	if j["integrate_commit"] != f.Short(declared) {
		t.Errorf("integrate_commit = %v, want %s", j["integrate_commit"], f.Short(declared))
	}
	next, _ := j["next_action"].(string)
	if !strings.Contains(next, "git push origin booking-transaction") {
		t.Errorf("next_action = %q, want the push that makes the request visible", next)
	}
	// The declaration does not end the author's obligations — it moves the merge to whoever owns the
	// destination branch — and the sentence says exactly that ("main merges what … reports ready and
	// integrating"). What it must not do is hand the author the merge command as their own next step: that is
	// the sentence this changeset exists to take away from the declared state.
	if strings.Contains(next, "merge into main with ordinary git") {
		t.Errorf("next_action = %q, still tells an author who has declared to merge it themselves", next)
	}
	if strings.Contains(next, "merged") {
		// Naming the push as a command the author runs is the rule; a log line in the past tense reads as an
		// action git-pair took (PRD §26).
		t.Errorf("next_action = %q, reads as a completed action", next)
	}
	if j["abandoned"] != false {
		t.Errorf("abandoned = %v on a declared changeset", j["abandoned"])
	}
}

// A declaration is about a commit, and `status` is where the author finds out that the commit they just
// made is not the one on record.
func TestStatusSaysWhenTheDeclaredHeadMoved(t *testing.T) {
	f, slug, _ := declaredChangeset(t)
	f.Commit("note the answer", gittest.WithFile("changesets/"+slug+"/ABOUT.md",
		"# "+slug+"\n\nConcurrency answered.\n"))

	j := runIn(t, f.Dir(), "status", "--json").json(t)
	next, _ := j["next_action"].(string)
	if !strings.Contains(next, "the head moved since the declaration") {
		t.Errorf("next_action = %q, want the moved-head answer", next)
	}
	if !strings.Contains(next, "git pair change integrate") {
		t.Errorf("next_action = %q, want it to name the command that declares this head", next)
	}
}

// The withdrawal. A declaration is the request a pipeline merges on, so stopping one has to be as easy as
// making one — a commit on the branch, with no rebase and no force-push, and visible to the gate the
// moment it lands.
func TestChangeUnreadyWithdrawsADeclaration(t *testing.T) {
	f, slug, _ := declaredChangeset(t)

	res := runIn(t, f.Dir(), "change", "unready").mustSucceed(t, "change", "unready")
	mustContain(t, res.stdout, "was:     INTEGRATING", "the answer says what it took back")
	mustContain(t, res.stdout, "the declaration is withdrawn",
		"and says so in the reader's terms, not only as a state change")

	j := runIn(t, f.Dir(), "check", "--json").mustSucceed(t, "check").json(t)
	if j["integrating"] != false {
		t.Errorf("integrating = %v after the withdrawal, want false: the gate stops offering the merge",
			j["integrating"])
	}
	if j["ready"] != false {
		t.Errorf("ready = %v after the withdrawal, want false", j["ready"])
	}
	q := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue").jsonList(t, "awaiting_integration")
	if len(q) != 0 {
		t.Errorf("awaiting_integration = %v after the withdrawal, want it empty", q)
	}

	// The offer is back on the queue, and re-declaring works: the withdrawal is a step, not a dead end.
	runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")
	runIn(t, f.Dir(), "review", "submit", "--approve").mustSucceed(t, "review", "submit")
	runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate")
	if got := f.Trailers(f.Head())["Review-State"]; got != "integrating" {
		t.Errorf("%s's newest marker is %q, want a fresh declaration", slug, got)
	}
}

// `queue` is the watcher's view, and a declared changeset belongs in a list of its own. It is not in the
// review queue — its newest marker is a declaration, so nothing is waiting for a reviewer — and printing it
// under "READY FOR REVIEW" would tell a reviewer to look at work that has already been approved.
func TestQueueListsDeclaredWorkSeparatelyFromReview(t *testing.T) {
	f := newRepo(t)
	// One changeset offered for review, one approved and declared: the two lists, one repository.
	f.CreateBranch("offered")
	f.CommitChangeset("offered", "main")
	f.Commit("offered work", gittest.WithFile("o.go", "package main\n"))
	f.SwitchTo("offered")
	ready(t, f)
	// Separate branches: one repository, two changesets, and no branch carrying work that is not its own.
	f.CreateBranch("declared", "main")
	f.CommitChangeset("declared", "main")
	f.Commit("declared work", gittest.WithFile("d.go", "package main\n"))
	f.SwitchTo("declared")
	ready(t, f)
	submit(t, f, "approve")
	runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate")
	declared := f.Head()

	out := runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
	mustContain(t, out.stdout, "AWAITING INTEGRATION", "the human surface has the second list")
	mustContain(t, out.stdout, "merge into: main", "and the branch somebody would merge into")
	if strings.Index(out.stdout, "declared") < strings.Index(out.stdout, "AWAITING INTEGRATION") {
		t.Errorf("the declared changeset is printed in the review section:\n%s", out.stdout)
	}

	j := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue")
	review := j.jsonList(t, "ready_for_review")
	if len(review) != 1 {
		t.Fatalf("ready_for_review = %v, want only the offered changeset", review)
	}
	if row, _ := review[0].(map[string]any); row["changeset"] != "offered" {
		t.Errorf("ready_for_review names %v, want offered", row["changeset"])
	}
	rows := j.jsonList(t, "awaiting_integration")
	if len(rows) != 1 {
		t.Fatalf("awaiting_integration = %v, want the declared changeset", rows)
	}
	row, _ := rows[0].(map[string]any)
	if row["changeset"] != "declared" {
		t.Errorf("changeset = %v, want declared", row["changeset"])
	}
	if row["state"] != "INTEGRATING" {
		t.Errorf("state = %v, want INTEGRATING", row["state"])
	}
	if row["destination"] != "main" {
		t.Errorf("destination = %v, want main", row["destination"])
	}
	if row["integrate_commit"] != declared {
		t.Errorf("integrate_commit = %v, want the declaration %s", row["integrate_commit"], declared)
	}
	if age, _ := row["declared_age"].(string); age == "" {
		t.Error("declared_age is empty: the list is ordered by waiting, so it has to say how long")
	}
}

// A declared changeset the destination already carries has landed, and the queue has nothing left to ask
// of it: neither of its lists is for finished work. The destination's tree is what says so — the merge is
// the record, so there is no second step for the row to be waiting on.
func TestQueueDoesNotListALandedChangesetAsAwaitingIntegration(t *testing.T) {
	f, slug, _ := declaredChangeset(t)
	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "land "+slug, slug)

	j := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue")
	if rows := j.jsonList(t, "awaiting_integration"); len(rows) != 0 {
		t.Errorf("awaiting_integration = %v after the landing, want it empty", rows)
	}
	if rows := j.jsonList(t, "ready_for_review"); len(rows) != 0 {
		t.Errorf("ready_for_review = %v after the landing, want it empty", rows)
	}
	mustNotContain(t, runIn(t, f.Dir(), "queue").mustSucceed(t, "queue").stdout, "AWAITING INTEGRATION",
		"and the human surface does not print an empty second list")
}

// The report that started this: a declared head, `git pair check` still spelling the author's own merge. The
// gate passes, the screen says `declared:`, and the next step reads as if nothing had been asked for.
func TestCheckNamesThePushForAnUnpublishedDeclaration(t *testing.T) {
	f, _, _ := declaredChangeset(t)

	res := runIn(t, f.Dir(), "check").mustSucceed(t, "check")
	mustContain(t, res.stdout, "declared:", "the verdict carries the declaration beside it")
	mustContain(t, res.stdout, "git push origin booking-transaction",
		"so the next step is the push that makes that request visible")
	mustNotContain(t, res.stdout, "merge into main with ordinary git",
		"and the merge sentence is gone from a head whose merge has already been asked for")
	mustNotContain(t, res.stdout, "or hand it to",
		"nor is the hand-off offered again: a second declaration records nothing")

	j := runIn(t, f.Dir(), "check", "--json").json(t)
	next, _ := j["next_action"].(string)
	if !strings.Contains(next, "git push origin booking-transaction") {
		t.Errorf("check --json next_action = %q, want the push", next)
	}
	if strings.Contains(next, "\n") || strings.Contains(next, "or take it yourself") {
		t.Errorf("check --json next_action = %q, want one instruction and no human alternative", next)
	}
}

// Once the request is on the remote the author's part is done, and the screen says so in the shape the
// `READY` state already uses for waiting: what you are waiting for, then the command that reads whether it
// happened. The author's own merge stays on the screen as the other exit, so waiting is never the only thing
// offered — and it stays off `next_action`, which is one instruction.
func TestDeclaredScreensWaitOnceTheBranchIsPushed(t *testing.T) {
	f, slug, declared := declaredChangeset(t)
	f.MustGit("update-ref", "refs/remotes/origin/booking-transaction", declared)

	for _, name := range []string{"status", "check"} {
		out := runIn(t, f.Dir(), name).mustSucceed(t, name).stdout
		mustContain(t, out, "waiting on main's owner to merge it", name+" names the wait")
		mustContain(t, out, "git pair status --changeset "+slug,
			name+" names the read that reports the landing, not a loop to poll")
		mustContain(t, out, "or take it yourself: `git pair check`, then merge into main with ordinary git",
			name+" keeps the author's own merge offered beside the wait")
		mustNotContain(t, out, "git push origin", name+" still asks for a push that has happened")
		mustNotContain(t, out, "or hand it to", name+" offers a declaration that would record nothing")

		j := runIn(t, f.Dir(), name, "--json").json(t)
		next, _ := j["next_action"].(string)
		if !strings.HasPrefix(next, "waiting on main's owner to merge it") {
			t.Errorf("%s --json next_action = %q, want the wait on its own", name, next)
		}
		if strings.Contains(next, "or take it yourself") || strings.Contains(next, "\n") {
			t.Errorf("%s --json next_action = %q, want one instruction with no human alternative", name, next)
		}
	}
}

// `change integrate` on a head that is already declared wrote nothing, printed "nothing recorded", and
// dropped the step it had just computed. The author was left with no next line at all.
func TestChangeIntegrateRerunNamesTheStepItLeftOut(t *testing.T) {
	t.Run("unpublished", func(t *testing.T) {
		f, _, _ := declaredChangeset(t)
		out := runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate").stdout
		mustContain(t, out, "nothing recorded", "the re-run still writes nothing")
		mustContain(t, out, "git push origin booking-transaction",
			"and now says what is left to do")
		mustNotContain(t, out, "or take it yourself", "the push is the step, so no alternative is offered yet")
	})

	t.Run("pushed", func(t *testing.T) {
		f, slug, declared := declaredChangeset(t)
		f.MustGit("update-ref", "refs/remotes/origin/booking-transaction", declared)
		out := runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate").stdout
		mustContain(t, out, "waiting on main's owner to merge it", "the re-run reports the wait")
		mustContain(t, out, "git pair status --changeset "+slug, "with the read that settles it")
		mustContain(t, out, "or take it yourself:", "and the other exit")
	})
}

// A declaration is about a commit. When a commit comes under it, neither the push nor the wait is the step:
// the request on the branch names a commit nobody is going to merge, and `check` has to say so in the words
// `status` already uses rather than inventing a second sentence for the same fact.
func TestCheckSaysWhenTheDeclaredHeadMoved(t *testing.T) {
	f, slug, _ := declaredChangeset(t)
	f.Commit("note the answer", gittest.WithFile("changesets/"+slug+"/ABOUT.md",
		"# "+slug+"\n\nConcurrency answered.\n"))

	res := runIn(t, f.Dir(), "check").mustSucceed(t, "check")
	mustContain(t, res.stdout, "the head moved since the declaration: `git pair change integrate` to declare this head",
		"check names the declaration that covers this head")
	mustNotContain(t, res.stdout, "git push origin", "and does not ask for a push of a request that is out of date")

	j := runIn(t, f.Dir(), "check", "--json").json(t)
	if next, _ := j["next_action"].(string); !strings.Contains(next, "git pair change integrate") {
		t.Errorf("check --json next_action = %q, want the re-declaration", next)
	}
}
