package cli_test

import (
	"fmt"
	"strings"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/gittest"
)

// What these tests are about is the one finding that cannot come from the history of a branch: work that
// reached the integration branch without a review approving it. The merge happens outside git-pair, and
// the tree holds no trace of the branch it came from, so the only place a verdict can be found is the
// chain behind the directory — and a landing that carried no chain keeps nothing at all.
//
// This replaced `LANDED, UNRECORDED`, which reported that `integration record` had not been run. That
// finding closed the moment the command ran; nothing closes this one by writing a verdict, which is why the
// heading prints a read rather than a command. One act does end the report: `change tidy`, once the commit
// it wrote is in the destination. `TestATidiedLandingIsNotReportedUnreviewed` is that rule, and the test
// beside it is the half that keeps it honest — a tidy nobody has merged silences nothing.

// landMergeReviewing puts a reviewed changeset into `main` the way a merge does it, so the chain behind
// the directory is the branch that was reviewed.
func landMergeReviewing(t *testing.T, f *gittest.Fixture, slug string, reviewed bool) string {
	t.Helper()
	if reviewed {
		ready(t, f)
		submit(t, f, "approve")
	}
	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "-m", slug+": merge the branch", slug)
	return f.Head()
}

// landSquash puts a changeset's *tree* into `main` on one fresh commit, which is the shape a squash or a
// cherry-pick leaves: the directory arrives, none of the work's history does.
func landSquash(t *testing.T, f *gittest.Fixture, slug string) string {
	t.Helper()
	source := f.Head()
	f.SwitchTo("main")
	f.MustGit("checkout", source, "--", changeset.Root+"/"+slug)
	f.Commit(slug+": land the work", gittest.WithFile("landed.md", "landed\n"))
	return f.Head()
}

func TestQueueReportsWorkThatReachedTheDestinationWithoutApproval(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	// The author merged it themselves. Nobody reviewed it, and no marker exists anywhere.
	landMergeReviewing(t, f, slug, false)
	f.ForceDeleteBranch(slug)

	out := runIn(t, f.Dir(), "queue")
	mustContain(t, out.stdout, "LANDED UNREVIEWED", "work in the destination with no verdict is a finding")
	mustContain(t, out.stdout, slug, "naming the changeset")
	mustContain(t, out.stdout, "no review verdict", "saying what the chain said")
	mustContain(t, out.stdout, "status --changeset", "and the read that goes and looks")

	doc := runIn(t, f.Dir(), "queue", "--json").json(t)
	list, ok := doc["landed_unreviewed"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("landed_unreviewed = %#v, want one finding", doc["landed_unreviewed"])
	}
	item := list[0].(map[string]any)
	if item["changeset"] != slug || item["commit"] == "" || item["chain"] == "" {
		t.Errorf("finding = %#v, want the changeset, the landing commit and the range read", item)
	}
}

func TestAReviewedLandingIsNotAFinding(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	landMergeReviewing(t, f, slug, true)
	f.ForceDeleteBranch(slug)

	out := runIn(t, f.Dir(), "queue")
	mustNotContain(t, out.stdout, "LANDED UNREVIEWED", "a landing whose chain carries an approval")
	if list := runIn(t, f.Dir(), "queue", "--json").json(t)["landed_unreviewed"].([]any); len(list) != 0 {
		t.Errorf("landed_unreviewed = %#v, want the empty array", list)
	}
}

func TestASquashLandingSaysTheDestinationKeptNoHistory(t *testing.T) {
	// The review happened here, and the landing shape threw it away. The reason line has to tell those
	// two facts apart: an absent verdict and an unreadable chain are different questions to ask of the
	// history, and only one of them means nobody approved.
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "approve")
	landSquash(t, f, slug)
	f.ForceDeleteBranch(slug)

	out := runIn(t, f.Dir(), "queue")
	mustContain(t, out.stdout, "LANDED UNREVIEWED", "a squash landing of a reviewed changeset is still unreadable")
	mustContain(t, out.stdout, "one commit", "and says the history did not come with the directory")
	doc := runIn(t, f.Dir(), "queue", "--json").json(t)
	if item := doc["landed_unreviewed"].([]any)[0].(map[string]any); item["chain"] != "" {
		t.Errorf("chain = %v, want the empty string where there was no range to read", item["chain"])
	}
}

// printedFindings mirrors unreviewedDisplayCap in landed.go: the cap is part of the printed contract, so
// a test that counts the counted tail has to know it, and a change there should fail here.
const printedFindings = 10

func TestQueueCountsUnreviewedLandingsItDoesNotPrint(t *testing.T) {
	f := newRepo(t)
	for i := 0; i < printedFindings+2; i++ {
		id := fmt.Sprintf("bulk%02d", i)
		f.CreateBranch(id)
		f.CommitChangeset(id, "main")
		f.Commit(id+": work", gittest.WithFile(id+".go", "package main\n"))
		f.SwitchTo("main")
		f.MustGit("merge", "--no-ff", "-m", id+": merge", id)
		f.ForceDeleteBranch(id)
	}

	out := runIn(t, f.Dir(), "queue")
	mustContain(t, out.stdout, fmt.Sprintf("and 2 more (`git pair queue --json` lists every one)"),
		"the counted tail says how many were left out and where the whole list is")
	if n := len(runIn(t, f.Dir(), "queue", "--json").json(t)["landed_unreviewed"].([]any)); n != printedFindings+2 {
		t.Errorf("json carries %d findings, want every one of the %d", n, printedFindings+2)
	}
}

func TestStatusOnTheDestinationNamesWorkNobodyApproved(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	landMergeReviewing(t, f, slug, false)
	f.ForceDeleteBranch(slug)
	f.SwitchTo("main")

	// On the destination branch there is no changeset to report, and exit 2 stays right — what the reader
	// needs is the finding that explains why the branch is empty of work in progress.
	res := runIn(t, f.Dir(), "status")
	if res.code != 2 {
		t.Fatalf("status on the destination exited %d, want 2\n%s", res.code, res.stdout+res.stderr)
	}
	// The finding rides the error, because the error is what the command returns and exit 2 is what the
	// reader acts on; the reason and the finding have to arrive together or one of them is lost.
	mustContain(t, res.stderr, "LANDED UNREVIEWED", "the same finding `queue` prints")
	mustContain(t, res.stderr, slug, "naming the changeset")

	doc := runIn(t, f.Dir(), "status", "--json").json(t)
	if list, ok := doc["landed_unreviewed"].([]any); !ok || len(list) != 1 {
		t.Errorf("landed_unreviewed = %#v, want one finding on the error path too", doc["landed_unreviewed"])
	}
}

func TestUnreviewedLandingsSurviveEveryBranchBeingGone(t *testing.T) {
	// The point of reading the destination rather than the refs: a clone with no branches left still
	// knows what it merged without a review.
	f, slug := newChangeset(t, "booking", "main")
	landMergeReviewing(t, f, slug, false)
	f.ForceDeleteBranch(slug)

	if len(durableRefs(t, f)) != 0 {
		t.Fatal("the fixture wrote durable refs, which would test nothing")
	}
	if !strings.Contains(runIn(t, f.Dir(), "queue").stdout, "LANDED UNREVIEWED") {
		t.Error("the finding is gone with the refs")
	}
}

// The durable refs are still in the repository until milestone M5 deletes them, and this is the test that
// says they are inert already: point both families at commits that tell a different story, and every answer
// above stays the one the destination's tree gives. A later change that reads a ref again fails here, in a
// repository where the refs disagree, rather than in a clone where they happen to agree.
func TestDurableRefsDisagreeWithTheTreeAndChangeNothing(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	landMergeReviewing(t, f, slug, true)
	f.ForceDeleteBranch(slug)

	clean := runIn(t, f.Dir(), "status", "--changeset", slug, "--json").json(t)
	want := len(runIn(t, f.Dir(), "queue", "--json").json(t)["landed_unreviewed"].([]any))

	// Two refs, both lying: an integration ref naming a commit that carries nothing, and an archive ref
	// naming the destination's own tip.
	bogus := f.Commit("an unrelated commit the refs will name", gittest.WithFile("elsewhere.go", "package main\n"))
	f.MustGit("update-ref", "refs/git-pair/integrations/"+slug, bogus)
	f.MustGit("update-ref", "refs/git-pair/archive/"+slug, "main")

	if len(durableRefs(t, f)) != 2 {
		t.Fatal("the lying refs were not written, so the assertions below prove nothing")
	}
	after := runIn(t, f.Dir(), "status", "--changeset", slug, "--json").json(t)
	for _, key := range []string{"landed", "landed_commit", "landed_branch", "chain_base", "chain_head",
		"reviewed", "state"} {
		if after[key] != clean[key] {
			t.Errorf("%s = %v with the refs lying, want %v: that answer came from a ref", key, after[key], clean[key])
		}
	}
	if got := len(runIn(t, f.Dir(), "queue", "--json").json(t)["landed_unreviewed"].([]any)); got != want {
		t.Errorf("landed_unreviewed has %d findings where it had %d: a ref is on the queue's path", got, want)
	}
}

// TestAReplayedLandingIsUnreviewedBecauseTheApprovedCommitsDidNotArrive is the strict half of the landing
// verdict. An approval is a statement about a commit. A landing that replayed the run — the "rebase and
// merge" button, or an amend or rebase the author made after the approval — brings the statement across and
// leaves the commits it was about behind, so the destination holds no approval of what the destination
// holds. The record and the licence are different questions, and only one of them survives a rewrite.
func TestAReplayedLandingIsUnreviewedBecauseTheApprovedCommitsDidNotArrive(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "approve")

	f.SwitchTo("main")
	f.Commit("trunk moves on", gittest.WithFile("trunk.md", "moved\n"))
	f.SwitchTo(slug)
	f.MustGit("rebase", "--quiet", "main")
	replayed := f.Head()
	named := reviewHeadOf(t, f.MustGit("log", "-1", "--format=%B"))
	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--ff-only", slug)
	f.ForceDeleteBranch(slug)

	out := runIn(t, f.Dir(), "status", "--changeset", slug, "--json").mustSucceed(t, "status").json(t)
	if out["landed"] != true {
		t.Fatalf("landed = %v: the destination carries the directory", out["landed"])
	}
	if out["reviewed"] != false {
		t.Errorf("reviewed = %v, want false: the replay carried the approve marker and not the commit it "+
			"names (%s)", out["reviewed"], named)
	}
	if out["chain_head"] != f.Short(replayed) {
		t.Errorf("chain_head = %v, want the replayed review commit %s", out["chain_head"], f.Short(replayed))
	}
	if out["state"] != "APPROVED" {
		t.Errorf("state = %v, want APPROVED: the markers are in the chain, and what they recorded is a "+
			"different question from what they license", out["state"])
	}

	q := runIn(t, f.Dir(), "queue", "--json").json(t)
	list, ok := q["landed_unreviewed"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("landed_unreviewed = %v, want this landing alone", q["landed_unreviewed"])
	}
	entry, ok := list[0].(map[string]any)
	if !ok || entry["changeset"] != slug {
		t.Fatalf("landed_unreviewed[0] = %v, want %s", list[0], slug)
	}
	reason, _ := entry["reason"].(string)
	for _, want := range []string{named[:7], "does not carry", "replayed the run"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q does not say %q: a reader has to be able to tell this from a squash, and "+
				"to be able to go and look at what is missing", reason, want)
		}
	}
}

// reviewHeadOf reads the commit an approval names out of the marker's own message. A test that argues about
// Review-Head quotes the trailer rather than a SHA it guessed at, which is the difference between testing
// the rule and testing the fixture.
func reviewHeadOf(t *testing.T, marker string) string {
	t.Helper()
	for _, line := range strings.Split(marker, "\n") {
		if v, ok := strings.CutPrefix(line, "Review-Head:"); ok {
			return strings.TrimSpace(v)
		}
	}
	t.Fatalf("the marker carries no Review-Head trailer, so the fixture is not what the test claims:\n%s", marker)
	return ""
}

// TestAMergeCommitLandingKeepsTheReviewItCarried is the counterweight that keeps the rule from meaning
// "flag every landing". This repository lands by merge commit, which carries the run without rewriting it,
// so the commit the approval names is in the destination and the approval covers what landed. A verdict that
// refused this shape too would fire on every landing in the repository and say nothing about any of them.
func TestAMergeCommitLandingKeepsTheReviewItCarried(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "approve")

	f.SwitchTo("main")
	f.Commit("trunk moves on", gittest.WithFile("trunk.md", "moved\n"))
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "Merge booking into main", slug)
	f.ForceDeleteBranch(slug)

	out := runIn(t, f.Dir(), "status", "--changeset", slug, "--json").mustSucceed(t, "status").json(t)
	if out["reviewed"] != true {
		t.Errorf("reviewed = %v, want true: the merge carried the run without rewriting it, so the commit "+
			"the approval names is here (chain %v..%v)", out["reviewed"], out["chain_base"], out["chain_head"])
	}
	q := runIn(t, f.Dir(), "queue", "--json").json(t)
	if got := q["landed_unreviewed"]; len(got.([]any)) != 0 {
		t.Errorf("landed_unreviewed = %v, want none: a landing that brought the approved commits is not a "+
			"finding", got)
	}
}

// TestAnApprovalThatNamesNoCommitLicensesNothing is the strict edge chosen deliberately rather than arrived
// at: what cannot be checked cannot license a merge either. The record survives — the chain still carries
// the approve marker, so `state` is still APPROVED — and the answer is still no, because there is no commit
// to compare the destination against. Two changesets in this repository's own trunk are in that shape.
func TestAnApprovalThatNamesNoCommitLicensesNothing(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "approve")

	named := reviewHeadOf(t, f.MustGit("log", "-1", "--format=%B"))
	unnames := strings.TrimRight(strings.Replace(f.MustGit("log", "-1", "--format=%B"),
		"Review-Head: "+named+"\n", "", 1), "\n")
	// A marker commit changes no files, so amending it needs `--allow-empty`: git is refusing to make an
	// empty commit empty, not refusing the test.
	f.MustGit("commit", "--amend", "--quiet", "--allow-empty", "-m", unnames)
	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--ff-only", slug)
	f.ForceDeleteBranch(slug)

	out := runIn(t, f.Dir(), "status", "--changeset", slug, "--json").mustSucceed(t, "status").json(t)
	if out["reviewed"] != false {
		t.Errorf("reviewed = %v, want false: an approval with no commit named cannot be checked against "+
			"the destination", out["reviewed"])
	}
	if out["state"] != "APPROVED" {
		t.Errorf("state = %v, want APPROVED: the verdict is still recorded, it just proves nothing here", out["state"])
	}
	q := runIn(t, f.Dir(), "queue", "--json").json(t)
	list, ok := q["landed_unreviewed"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("landed_unreviewed = %v, want this landing alone", q["landed_unreviewed"])
	}
	entry, _ := list[0].(map[string]any)
	reason, _ := entry["reason"].(string)
	if !strings.Contains(reason, "names no commit") {
		t.Errorf("reason %q, want it to say the approval names no commit rather than that no review happened", reason)
	}
}

// A landing is the destination carrying the directory, and the destination is the branch the changeset
// names, which is not always the default one. The trunk scan cannot see such a landing: the directory is
// absent from trunk and the branch is still here, so the changeset would be an ordinary review row. The
// queue reads the destination itself and says where the work went, rather than dropping the row in
// silence — a changeset that leaves the queue without a word is a mystery to the person reading it.
func TestQueueNamesALandingOnABranchThatIsNotTheDefault(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("release/2.x")
	f.Commit("the release line", gittest.WithFile("release.md", "2.x\n"))
	// The changeset names the release branch as its base, so that branch is its destination.
	f.CreateBranch("booking", "release/2.x")
	f.CommitChangeset("booking", "release/2.x")
	f.Commit("booking work", gittest.WithFile("b.go", "package main\n"))
	ready(t, f)
	f.SwitchTo("release/2.x")
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "booking: land on the release line", "booking")
	landing := f.Head()
	f.SwitchTo("booking")

	res := runIn(t, f.Dir(), "queue")
	res.mustSucceed(t, "queue")
	mustContain(t, res.stderr, "booking (landed on release/2.x at "+f.Short(landing)+")",
		"the skip note names the landing the trunk scan cannot see")

	doc := runIn(t, f.Dir(), "queue", "--json").json(t)
	for _, row := range doc["ready_for_review"].([]any) {
		entry, ok := row.(map[string]any)
		if ok && entry["changeset"] == "booking" {
			t.Errorf("the queue offers a changeset the destination already carries for review: %v", entry)
		}
	}
	if got := len(doc["landed_unreviewed"].([]any)); got != 0 {
		t.Errorf("landed_unreviewed = %v: a landing on a branch that is not the integration branch is not that finding",
			doc["landed_unreviewed"])
	}
}

// fileAway tidies `slug` on its own branch and merges that branch into main, which is how a tidy reaches
// the destination: the move is a changeset of renames and rides the normal flow. Tidying in the checked-out
// working tree of trunk would be a different state, and a less honest one to test.
func fileAway(t *testing.T, f *gittest.Fixture, slug string) {
	t.Helper()
	f.CreateBranch("file-"+slug+"-away", "main")
	runIn(t, f.Dir(), "change", "tidy", slug).mustSucceed(t, "change tidy")
	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "file "+slug+" away", "file-"+slug+"-away")
}

// A landed directory the destination has filed away under `changesets/.landed/` is a record somebody moved
// out of the way in a commit that reached the destination through review. The report stops there, on both
// surfaces and in both output modes, because a notification with no end is a notification that gets ignored.
//
// What does not stop is the answer. The chain is still there and `status --changeset` still reads it, so
// filing is a way to stop being told, not a way to make the finding untrue — which is the property that makes
// it safe to have.
func TestATidiedLandingIsNotReportedUnreviewed(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	landMergeReviewing(t, f, slug, false)
	f.ForceDeleteBranch(slug)
	mustContain(t, runIn(t, f.Dir(), "queue").stdout, "LANDED UNREVIEWED", "before the tidy, the finding is there")

	fileAway(t, f, slug)

	out := runIn(t, f.Dir(), "queue")
	mustNotContain(t, out.stdout, "LANDED UNREVIEWED", "a filing that reached the destination ends the heading")
	mustNotContain(t, out.stdout, slug, "and does not name the changeset it filed away")
	if list := runIn(t, f.Dir(), "queue", "--json").json(t)["landed_unreviewed"].([]any); len(list) != 0 {
		t.Errorf("landed_unreviewed = %#v, want the empty list: the same rule on the machine surface", list)
	}

	// The destination branch has no changeset of its own, so this is the other surface that printed the
	// heading. Exit 2 is about work in progress and is not the finding's to change either way.
	res := runIn(t, f.Dir(), "status")
	if res.code != 2 {
		t.Fatalf("status on the destination exited %d, want 2\n%s", res.code, res.stdout+res.stderr)
	}
	mustNotContain(t, res.stderr, "LANDED UNREVIEWED", "and status says the same")

	// The finding itself, read by name: the same chain, the same answer, unchanged by the move. This is the
	// half that makes the rule above defensible.
	doc := runIn(t, f.Dir(), "status", "--changeset", slug, "--json").mustSucceed(t, "status").json(t)
	if doc["landed"] != true {
		t.Fatalf("landed = %v, want true: a filed directory is still landed work", doc["landed"])
	}
	if doc["reviewed"] != false {
		t.Errorf("reviewed = %v, want false: filing a record does not make an approval appear", doc["reviewed"])
	}
}

// The rule is about the destination's tree, so a tidy that sits on an open branch changes nothing. This is
// the difference between a filing and an intention, and the reason the rule cannot be used to silence a
// finding from an unreviewed commit: the move has to be merged, and merging is where the review happens.
func TestATidyThatHasNotReachedTheDestinationIsStillAFinding(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	landMergeReviewing(t, f, slug, false)
	f.ForceDeleteBranch(slug)

	f.CreateBranch("tidy-up", "main")
	runIn(t, f.Dir(), "change", "tidy", slug).mustSucceed(t, "change tidy")
	f.SwitchTo("main")

	out := runIn(t, f.Dir(), "queue")
	mustContain(t, out.stdout, "LANDED UNREVIEWED", "the destination still carries the directory in place")
	mustContain(t, out.stdout, slug, "and names it")
	if list := runIn(t, f.Dir(), "queue", "--json").json(t)["landed_unreviewed"].([]any); len(list) != 1 {
		t.Errorf("landed_unreviewed = %#v, want the finding until the tidy is merged", list)
	}
}
