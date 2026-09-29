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
// finding closed the moment the command ran; nothing closes this one, which is why the heading prints a
// read rather than a command.

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

// A rebase-merge lands the branch's commits themselves, so the markers arrive with them and the verdict
// survives. `LANDED UNREVIEWED` is for the landing that carries no marker commit at all — a squash, a
// cherry-pick — not for every landing that was not a `--no-ff` merge. The reviewer asked which of the two a
// rebase is; the derivation-level answer is `TestLandedChainOfARebaseLanding`, and this is the same answer on
// the command surface, beside the squash case above that says the opposite.
func TestARebaseLandingKeepsTheReviewItCarried(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "approve")

	// The shape a "rebase and merge" button produces: trunk moves, the branch rebases onto it, and then
	// lands by fast-forward — every commit on the branch is a new commit with an old message.
	f.SwitchTo("main")
	f.Commit("trunk moves on", gittest.WithFile("trunk.md", "moved\n"))
	f.SwitchTo(slug)
	f.MustGit("rebase", "--quiet", "main")
	reviewed := f.Head()
	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--ff-only", slug)
	f.ForceDeleteBranch(slug)

	out := runIn(t, f.Dir(), "status", "--changeset", slug, "--json").mustSucceed(t, "status").json(t)
	if out["landed"] != true {
		t.Fatalf("landed = %v: the destination carries the directory", out["landed"])
	}
	if out["reviewed"] != true {
		t.Errorf("reviewed = %v, want true: the rebase replayed the approve marker as a commit and its "+
			"trailer came with it (%v..%v)", out["reviewed"], out["chain_base"], out["chain_head"])
	}
	if out["chain_head"] != f.Short(reviewed) {
		t.Errorf("chain_head = %v, want the rebased review commit %s", out["chain_head"], f.Short(reviewed))
	}
	if out["state"] != "APPROVED" {
		t.Errorf("state = %v, want APPROVED: the chain's own markers derive it", out["state"])
	}

	q := runIn(t, f.Dir(), "queue", "--json").json(t)
	if got := q["landed_unreviewed"]; len(got.([]any)) != 0 {
		t.Errorf("landed_unreviewed = %v, want none: a landing that carried its verdict is not a finding", got)
	}
}
