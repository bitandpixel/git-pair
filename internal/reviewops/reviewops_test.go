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
	cs, err := changeset.ForBranch(e.repo, slug)
	if err != nil {
		t.Fatalf("ForBranch: %v", err)
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

func (e *env) submit(t *testing.T, outcome model.Outcome, body string, stageAll bool) reviewops.Result {
	t.Helper()
	result, err := reviewops.Submit(context.Background(), e.repo, e.cs, e.summary(t), outcome, body, stageAll)
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
	if trailers["GitPR-Outcome"] != "block" {
		t.Errorf("GitPR-Outcome = %q, want block", trailers["GitPR-Outcome"])
	}
	if trailers["GitPR-Changeset"] != slug {
		t.Errorf("GitPR-Changeset = %q, want %q", trailers["GitPR-Changeset"], slug)
	}
	if got := e.f.RefSHA("refs/reviews/" + slug); got != result.Commit {
		t.Errorf("refs/reviews/%s points at %s, want the review commit %s", slug, got, result.Commit)
	}
	if result.Ref != "refs/reviews/"+slug {
		t.Errorf("Ref = %q, want refs/reviews/%s", result.Ref, slug)
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
	if got := e.f.RefSHA("refs/reviews/" + slug); got != result.Commit {
		t.Errorf("review ref = %s, want the empty approval %s", got, result.Commit)
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
	if !strings.Contains(message, "GitPR-Outcome: feedback") {
		t.Errorf("message = %q, want the outcome trailer", message)
	}
	if strings.Index(message, body) > strings.Index(message, "GitPR-Outcome:") {
		t.Errorf("message = %q, want the body before the trailer block", message)
	}
	if got := e.summary(t).State; got != model.StateFeedback {
		t.Errorf("derived state = %s, want FEEDBACK", got)
	}
}

func TestSubmitRejectsInvalidOutcome(t *testing.T) {
	e := newEnv(t)

	if _, err := reviewops.Submit(context.Background(), e.repo, e.cs, e.summary(t),
		model.Outcome("approve-self"), "", true); err == nil {
		t.Error("Submit accepted an outcome that is not block/feedback/approve")
	}
}

// An agent must not be able to keep reviewing a closed changeset (PRD §10.7, §22).
func TestSubmitRefusesClosedChangeset(t *testing.T) {
	e := newEnv(t)
	e.submit(t, model.OutcomeApprove, "", true)
	e.f.CommitCloseMarker(slug)

	if got := e.summary(t).State; got != model.StateClosed {
		t.Fatalf("derived state = %s, want CLOSED", got)
	}
	if _, err := reviewops.Submit(context.Background(), e.repo, e.cs, e.summary(t),
		model.OutcomeApprove, "", true); err == nil {
		t.Error("Submit succeeded on a closed changeset")
	} else if !strings.Contains(err.Error(), "closed") {
		t.Errorf("error = %v, want it to say the changeset is closed", err)
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
	if _, err := reviewops.Submit(context.Background(), repo, cs, lifecycle.Summary{},
		model.OutcomeApprove, "", true); err == nil {
		t.Error("Submit succeeded with no commits in the repository")
	}
}

// Two submissions in a row must both stay reachable: each moves the ref, so the
// first review's commit is an ancestor of the second rather than garbage.
func TestSubmitKeepsEarlierReviewsReachable(t *testing.T) {
	e := newEnv(t)

	first := e.submit(t, model.OutcomeBlock, "", true)
	e.f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { transaction() }\n"))
	second := e.submit(t, model.OutcomeFeedback, "", true)

	if !e.f.ReachableFrom(first.Commit, second.Commit) {
		t.Error("the first review is not an ancestor of the second")
	}
	if got := e.f.RefSHA("refs/reviews/" + slug); got != second.Commit {
		t.Errorf("review ref = %s, want the newest review %s", got, second.Commit)
	}
	if !e.f.ReachableFrom(first.Commit, "refs/reviews/"+slug) {
		t.Error("the first review is not reachable from the review ref (PRD §13)")
	}
}
