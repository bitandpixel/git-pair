package reviewops_test

import (
	"context"
	"strings"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/gittest"
	"gitpair/internal/lifecycle"
	"gitpair/internal/model"
	"gitpair/internal/reviewops"
)

const slug = "booking-transaction"

type env struct {
	f    *gittest.Fixture
	repo *git.Repo
	cs   changeset.Changeset
}

func newEnv(t *testing.T) *env {
	t.Helper()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n\nfunc main() {}\n"))
	f.CreateBranch(slug)
	f.CommitChangeset(slug, "main")
	f.Commit("implement", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))

	e := &env{f: f, repo: &git.Repo{Dir: f.Dir()}}
	cs, err := changeset.Current(context.Background(), e.repo, "")
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	e.cs = cs
	if !cs.Exists {
		t.Fatal("fixture changeset is missing")
	}
	return e
}

func (e *env) summary(t *testing.T) lifecycle.Summary {
	t.Helper()
	summary, err := lifecycle.SummarizeHEAD(context.Background(), e.repo, e.cs.Slug, e.cs.Base)
	if err != nil {
		t.Fatalf("SummarizeHEAD: %v", err)
	}
	return summary
}

func (e *env) trunk(t *testing.T) changeset.DefaultBranchRef {
	t.Helper()
	db, err := changeset.DefaultBranch(context.Background(), e.repo, "")
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	return db
}

func (e *env) submit(t *testing.T, outcome model.Outcome, body string, stageAll bool) reviewops.Result {
	t.Helper()
	result, err := reviewops.Submit(context.Background(), e.repo, e.cs, outcome, body, stageAll, "", e.trunk(t))
	if err != nil {
		t.Fatalf("Submit(%s): %v", outcome, err)
	}
	return result
}

// PRD §10.4: every review submission is a standardized commit carrying
// machine-readable trailers, and PRD §13 requires the review ref to land on that
// exact HEAD.
func TestSubmitCreatesStandardizedReviewCommitAndMovesRef(t *testing.T) {
	e := newEnv(t)
	before := e.f.Head()
	e.f.Write("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n")

	result := e.submit(t, model.OutcomeBlock, "", true)

	if result.Outcome != model.OutcomeBlock {
		t.Errorf("Outcome = %q, want block", result.Outcome)
	}
	if result.Commit != e.f.Head() {
		t.Errorf("Commit = %s, want the new HEAD %s", result.Commit, e.f.Head())
	}
	if result.Commit == before {
		t.Error("no review commit was created")
	}
	if got := e.f.Subject(result.Commit); got != "review: block "+slug {
		t.Errorf("subject = %q, want %q (PRD §10.4)", got, "review: block "+slug)
	}
	trailers := e.f.Trailers(result.Commit)
	if trailers["Review-Outcome"] != "block" {
		t.Errorf("Review-Outcome = %q, want block", trailers["Review-Outcome"])
	}
	if trailers["Review-Changeset"] != slug {
		t.Errorf("Review-Changeset = %q, want %q", trailers["Review-Changeset"], slug)
	}
	// The submission is the commit and nothing else. PRD §10.4 used to add "immediately after a
	// successful submission, update the changeset's review archive ref to the resulting exact HEAD" —
	// a pointer four commands had to keep pointing at HEAD, and the durable refs are now written once,
	// at landing. There is no ref for a submission to report.
	if refs := e.f.RefNames("refs/git-pair"); len(refs) != 0 {
		t.Errorf("Submit wrote %v for %s; the review is the commit with the trailers", refs, slug)
	}
	// The reviewer's direct edit is part of the review (PRD §20).
	if len(result.Files) != 1 || result.Files[0] != "service.go" {
		t.Errorf("Files = %v, want [service.go]", result.Files)
	}
	if result.Empty() {
		t.Error("Empty() = true for a review that changed a file")
	}
	if !e.f.Clean() {
		t.Error("the review submission left the working tree dirty")
	}
	// A review submission is an ordinary single-parent commit on the branch
	// (plan assumption 2).
	if got := e.f.ParentCount(result.Commit); got != 1 {
		t.Errorf("review commit has %d parents, want 1", got)
	}
}

// PRD §10.4: "Approval must support a clean working tree and therefore may create
// an empty Git commit."
func TestSubmitApproveOnCleanTreeCreatesEmptyReviewCommit(t *testing.T) {
	e := newEnv(t)
	before := e.f.Head()

	result := e.submit(t, model.OutcomeApprove, "", true)

	if !result.Empty() {
		t.Errorf("Files = %v, want none: an approval with no edits is an empty review", result.Files)
	}
	if changed := e.f.ChangedFiles(before, result.Commit); len(changed) != 0 {
		t.Errorf("the approval changed %v, want an empty commit", changed)
	}
	if got := e.f.Subject(result.Commit); got != "review: approve "+slug {
		t.Errorf("subject = %q", got)
	}
	// The empty approval is a commit and nothing more — no ref accompanies it.
	if refs := e.f.RefNames("refs/git-pair"); refs != nil {
		t.Errorf("an empty approval wrote %v", refs)
	}
	// The empty approval is still a lifecycle marker: the effective state is APPROVED.
	if got := e.summary(t).State; got != model.StateApproved {
		t.Errorf("derived state = %s, want APPROVED", got)
	}
	// It is also a real commit that keeps the chain reachable.
	if !e.f.ReachableFrom(before, result.Commit) {
		t.Error("the approval is not a descendant of the reviewed HEAD")
	}
}

// A review body is prose for humans; the trailers remain the machine contract.
func TestSubmitRecordsBodyAboveTrailers(t *testing.T) {
	e := newEnv(t)
	body := "Two comments are blocking; the naming feedback is optional."

	result := e.submit(t, model.OutcomeFeedback, body, true)

	message := e.f.Message(result.Commit)
	if !strings.Contains(message, body) {
		t.Errorf("message = %q, want it to contain the body %q", message, body)
	}
	if !strings.Contains(message, "Review-Outcome: feedback") {
		t.Errorf("message = %q, want the outcome trailer", message)
	}
	if strings.Index(message, body) > strings.Index(message, "Review-Outcome:") {
		t.Errorf("message = %q, want the body before the trailer block", message)
	}
	if got := e.summary(t).State; got != model.StateFeedback {
		t.Errorf("derived state = %s, want FEEDBACK", got)
	}
}

func TestSubmitRejectsInvalidOutcome(t *testing.T) {
	e := newEnv(t)

	if _, err := reviewops.Submit(context.Background(), e.repo, e.cs,
		model.Outcome("approve-self"), "", true, "", changeset.DefaultBranchRef{}); err == nil {
		t.Error("Submit accepted an outcome that is not block/feedback/approve")
	}
}

// What a submission must never do is write a durable ref. The test this replaced asserted the
// submission advanced the archive ref it was handed; the property worth keeping from it is the one it
// was really protecting — that a submission does not lose history — and that falls out of the commit
// being an ordinary child of HEAD on the branch, asserted above.
func TestSubmitWritesNoRef(t *testing.T) {
	e := newEnv(t)
	e.f.Write("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n")
	e.submit(t, model.OutcomeBlock, "", true)

	if refs := e.f.RefNames("refs/git-pair"); refs != nil {
		t.Errorf("a review submission wrote %v; the refs belong to landing", refs)
	}
}

// `--no-stage` exists for reviewers who stage deliberately: with staging off, the
// reviewer's un-staged edits must stay out of the review commit.
func TestSubmitWithoutStagingCommitsOnlyTheIndex(t *testing.T) {
	e := newEnv(t)
	e.f.Write("service.go", "package main\n\n// not staged yet\nfunc Lock() {}\n")

	result := e.submit(t, model.OutcomeBlock, "", false)

	if !result.Empty() {
		t.Errorf("Files = %v, want none: nothing was staged", result.Files)
	}
	if e.f.Clean() {
		t.Error("the reviewer's unstaged edit was consumed by the submission")
	}
	if got := e.f.FileAt("HEAD", "service.go"); strings.Contains(got, "not staged yet") {
		t.Error("the unstaged edit leaked into the review commit")
	}
}

func TestSubmitRequiresCommits(t *testing.T) {
	f := gittest.New(t)
	f.CreateBranch(slug)
	repo := &git.Repo{Dir: f.Dir()}
	cs := changeset.Changeset{Slug: slug, Branch: slug, Dir: "changesets/" + slug, Base: "main", Exists: true}

	// No commits at all: there is nothing to review, and a marker commit would be
	// the repository's root commit with no base to compare against.
	if _, err := reviewops.Submit(context.Background(), repo, cs,
		model.OutcomeApprove, "", true, "", changeset.DefaultBranchRef{}); err == nil {
		t.Error("Submit succeeded with no commits in the repository")
	}
}

// Two submissions in a row must both stay reachable. They are commits on the branch, so each is an
// ancestor of the next; keeping the older one readable after the branch is gone is the record's job at
// landing, not a ref's job during review.
func TestSubmitKeepsEarlierReviewsReachable(t *testing.T) {
	e := newEnv(t)

	first := e.submit(t, model.OutcomeBlock, "", true)
	e.f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { transaction() }\n"))
	second := e.submit(t, model.OutcomeFeedback, "", true)

	if !e.f.ReachableFrom(first.Commit, second.Commit) {
		t.Error("the first review is not an ancestor of the second")
	}
	if refs := e.f.RefNames("refs/git-pair"); refs != nil {
		t.Errorf("two submissions wrote %v; the chain is the branch, until it is the record", refs)
	}
}
