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
	// Measured the way the product measures it, so a test of what a marker records is a test of the
	// measurement and not of a fixture's guess.
	result, err := reviewops.Submit(context.Background(), e.repo, e.cs, outcome, body, stageAll,
		changeset.MeasureSubmission(context.Background(), e.repo, e.cs, e.trunk(t), ""), e.trunk(t))
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
		model.Outcome("approve-self"), "", true, changeset.Measurement{}, changeset.DefaultBranchRef{}); err == nil {
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
		model.OutcomeApprove, "", true, changeset.Measurement{}, changeset.DefaultBranchRef{}); err == nil {
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

// --- the recorded diff identity ---------------------------------------------------------------

// A submission records the identity of the diff it reviewed, so the gate can ask the content question as
// one comparison rather than an inference from how far the world has moved since (PRD §21). It is read
// back here the way a reader reads it: recomputed with git from the base the same marker recorded, and
// compared against what the marker claims. A value nobody can reproduce from the marker that carries it
// is worse than no value, because it reads as a comparison that was made.
func TestSubmitRecordsTheIdentityOfTheDiffItReviewed(t *testing.T) {
	e := newEnv(t)
	head := e.headSHA(t)

	res := e.submit(t, model.OutcomeApprove, "the content is right", true)
	message := e.f.MustGit("log", "-1", "--format=%B", res.Commit)

	version, digest, ok := model.ParseDiffID(trailerValue(t, message, "Review-Diff-Id"))
	if !ok || version != model.DiffIDVersion {
		t.Fatalf("Review-Diff-Id = %q, want %q:<hex>", trailerValue(t, message, "Review-Diff-Id"), model.DiffIDVersion)
	}
	base := trailerValue(t, message, "Review-Base-Head")
	if base == "" {
		t.Fatal("the marker recorded no base: the digest would name a diff nobody can reproduce")
	}

	want, err := e.repo.DiffRawDigest(context.Background(), base, head,
		"changesets/"+slug, "changesets/.landed/"+slug)
	if err != nil {
		t.Fatalf("DiffRawDigest(%s, %s): %v", base, head, err)
	}
	if digest != want {
		t.Errorf("recorded digest %s, want %s: the value a marker names must be reproducible from the base it names",
			digest, want)
	}
}

// Each submission records the diff it spoke about, and an earlier marker keeps the identity it made: the
// newest marker describes the newest head, and a reader asking what an older review looked at asks it.
func TestEachSubmissionRecordsItsOwnDiff(t *testing.T) {
	e := newEnv(t)

	first := e.submit(t, model.OutcomeApprove, "", true)
	e.f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { transaction() }\n"))
	second := e.submit(t, model.OutcomeFeedback, "", true)

	a := trailerValue(t, e.f.MustGit("log", "-1", "--format=%B", first.Commit), "Review-Diff-Id")
	b := trailerValue(t, e.f.MustGit("log", "-1", "--format=%B", second.Commit), "Review-Diff-Id")
	if a == "" || b == "" {
		t.Fatalf("a submission recorded no diff identity (%q, %q)", a, b)
	}
	if a == b {
		t.Error("two submissions over different content recorded the same diff identity")
	}
}

// The changeset's own directory is outside the digest, because that is where the review record lives.
// Without the exclusion, replying to a review thread would change the identity of the approval the reply
// is written into and invalidate it — the reader's own words turning against the review they were part of.
func TestAReplyToTheReviewThreadLeavesTheRecordedDiffAlone(t *testing.T) {
	e := newEnv(t)

	first := e.submit(t, model.OutcomeFeedback, "needs one change", true)
	e.f.Append("changesets/"+slug+"/ABOUT.md", "\n### comment (author)\n\nfixed\n")
	e.f.Commit("reply to the review thread")
	second := e.submit(t, model.OutcomeApprove, "", true)

	a := trailerValue(t, e.f.MustGit("log", "-1", "--format=%B", first.Commit), "Review-Diff-Id")
	b := trailerValue(t, e.f.MustGit("log", "-1", "--format=%B", second.Commit), "Review-Diff-Id")
	if a == "" || b == "" {
		t.Fatalf("a submission recorded no diff identity (%q, %q)", a, b)
	}
	if a != b {
		t.Errorf("the recorded identity moved from %s to %s across a reply to the review thread: the changeset's own directory must not be part of it", a, b)
	}
}

func (e *env) headSHA(t *testing.T) string {
	t.Helper()
	head, err := e.repo.Head(context.Background())
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	return head
}

// trailerValue is one trailer of a commit message, and "" when the message does not carry it — the
// absence a reader has to be able to tell from a value, because an absent record means "this was not
// said" and never means a difference.
func trailerValue(t *testing.T, message, key string) string {
	t.Helper()
	prefix := key + ": "
	for _, line := range strings.Split(message, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}
