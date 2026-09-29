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
	mustContain(t, res.stdout, "push the branch",
		"the next step is the push that makes the request visible, which git-pair never runs itself")

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
	if !strings.Contains(next, "push the branch") {
		t.Errorf("next_action = %q, want the push and then the landing contract", next)
	}
	// The landing contract is still in the sentence: the declaration does not end the author's obligations,
	// it moves the merge to whoever owns the destination branch.
	if !strings.Contains(next, "merge into main with ordinary git") {
		t.Errorf("next_action = %q, want it to keep naming the merge", next)
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
	f.CreateBranch("declared")
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

// One answer, printed by four commands. The landing sentence used to read the authored `base:` while
// `change integrate` asked `DestinationFor`, and the two disagreed on exactly the changeset where it mattered:
// a stack whose parent had merged. The author was told to merge into the parent's branch in the same breath in
// which the declaration was about to ask CI for the destination. These assertions are that disagreement's
// grave: the surfaces that tell a person what to run print the derived destination, notes included.
func TestTheLandingSentenceFollowsTheDestination(t *testing.T) {
	f, parent := newChangeset(t, "feature/x", "main")
	f.CreateBranch("ui", "feature/x")
	f.CommitChangeset("ui", "feature/x")
	f.Commit("ui work", gittest.WithFile("ui.go", "package main\n"))

	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land feature/x", "feature/x")
	f.SwitchTo("ui")
	ready(t, f)
	submitted := submit(t, f, "approve")

	want := "merge into main with ordinary git (base feature/x landed as feature-x)"
	if parent != "feature-x" {
		t.Fatalf("fixture slug = %q, want feature-x", parent)
	}

	check := runIn(t, f.Dir(), "check", "--json").mustSucceed(t, "check").json(t)
	if next, _ := check["next_action"].(string); !strings.Contains(next, want) {
		t.Errorf("check next_action = %q,\nwant it to contain %q", next, want)
	}
	status := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)
	if next, _ := status["next_action"].(string); !strings.Contains(next, want) {
		t.Errorf("status next_action = %q,\nwant it to contain %q", next, want)
	}
	if out := submitted.stdout + submitted.stderr; !strings.Contains(out, want) {
		t.Errorf("review submit printed:\n%s\nwant it to contain %q", out, want)
	}
	declared := runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate")
	mustContain(t, declared.stdout, "merge into:  main", "the declaration names the derived destination")
	mustContain(t, declared.stdout, "(base feature/x landed as feature-x)", "and the same note the sentences carry")

	// The trap's signature: no surface in this run offers the branch that already merged.
	for _, out := range []string{
		runIn(t, f.Dir(), "check").stdout,
		runIn(t, f.Dir(), "status").stdout,
		submitted.stdout + submitted.stderr,
		declared.stdout,
	} {
		if strings.Contains(out, "merge into feature/x") {
			t.Errorf("a surface still offers the merged parent branch:\n%s", out)
		}
	}
}
