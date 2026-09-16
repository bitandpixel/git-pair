package span_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gitpr/internal/git"
	"gitpr/internal/gittest"
	"gitpr/internal/lifecycle"
	"gitpr/internal/span"
)

const slug = "booking-transaction"

// reviews builds a changeset with three review submissions R1, R2, R3 and the
// implementation commits between them, matching PRD §18's `A -- R1 -- B -- C --
// R2 -- D` shape.
type scenario struct {
	f       *gittest.Fixture
	repo    *git.Repo
	base    string
	reviews []string
	between []string
	head    string
	offBase string // a commit made on the base branch after the fork
}

func newScenario(t *testing.T, reviewed bool) *scenario {
	t.Helper()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch(slug)
	f.CommitChangeset(slug, "main")
	f.Commit("implement", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))

	s := &scenario{f: f, repo: &git.Repo{Dir: f.Dir()}, base: "main"}
	if !reviewed {
		s.head = f.Head()
		return s
	}

	s.reviews = append(s.reviews, f.CommitReviewMarker(slug, "block",
		gittest.WithFile("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n")))
	s.between = append(s.between, f.Commit("response 1", gittest.WithFile("service.go",
		"package main\n\nfunc Lock() { transaction() }\n")))
	s.reviews = append(s.reviews, f.CommitReviewMarker(slug, "feedback",
		gittest.WithFile("docs.md", "note\n")))
	s.between = append(s.between, f.Commit("response 2", gittest.WithFile("docs.md", "note\nresolved\n")))
	s.reviews = append(s.reviews, f.CommitReviewMarker(slug, "approve"))
	s.head = f.Head()

	// Advance the base branch: the full span must still be measured from the
	// merge base, so main's later work cannot leak into the changeset diff.
	f.SwitchTo("main")
	s.offBase = f.Commit("unrelated work on main", gittest.WithFile("unrelated.go", "package main\n"))
	f.SwitchTo(slug)
	return s
}

func (s *scenario) summary(t *testing.T) lifecycle.Summary {
	t.Helper()
	summary, err := lifecycle.SummarizeHEAD(context.Background(), s.repo, slug, s.base)
	if err != nil {
		t.Fatalf("SummarizeHEAD: %v", err)
	}
	return summary
}

func (s *scenario) resolve(t *testing.T, sel span.Selector) (span.Span, error) {
	t.Helper()
	return span.Resolve(context.Background(), s.repo, s.base, s.summary(t), sel)
}

// PRD §17.1: the default span is `changeset.base ... HEAD`.
func TestResolveFullChangesetSpan(t *testing.T) {
	s := newScenario(t, true)

	got, err := s.resolve(t, span.Full())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	wantFrom := s.f.MergeBase("main", "HEAD")
	if got.From != wantFrom {
		t.Errorf("From = %s, want merge-base(main,HEAD) %s", got.From, wantFrom)
	}
	if got.To != s.f.Head() {
		t.Errorf("To = %s, want HEAD %s", got.To, s.f.Head())
	}
	if got.Base.Kind != span.KindChangesetBase || got.Head.Kind != span.KindWorkingTree {
		t.Errorf("span = %s → %s, want changeset base → working tree", got.Base.Kind, got.Head.Kind)
	}
	if !got.Live() {
		t.Error("a span ending at the working tree is a live review")
	}
	if !strings.Contains(got.Label, "main") || !strings.Contains(got.Label, "HEAD") {
		t.Errorf("Label = %q, want it to name the resolved range", got.Label)
	}
	// The span must not contain the commit made on main after the fork.
	changed := s.f.ChangedFiles(got.From, got.To)
	for _, path := range changed {
		if path == "unrelated.go" {
			t.Error("the full span includes a commit made on the base branch after the fork")
		}
	}
}

// PRD §17.3 spells out the index semantics, and PRD §18 requires the span to
// start *at* the review commit (the review commit is the boundary, so its own
// changes are the thing being responded to).
func TestResolveSinceReviewIndexes(t *testing.T) {
	s := newScenario(t, true)
	if len(s.reviews) != 3 {
		t.Fatalf("scenario has %d reviews, want 3", len(s.reviews))
	}
	tests := []struct {
		index      int
		wantFrom   string
		wantRevIdx int
	}{
		{0, s.reviews[0], 0},  // first review
		{1, s.reviews[1], 1},  // second review
		{2, s.reviews[2], 2},  // most recent, spelled positively
		{-1, s.reviews[2], 2}, // most recent review
		{-2, s.reviews[1], 1}, // second-most-recent
		{-3, s.reviews[0], 0}, // first review, spelled negatively
	}
	for _, tc := range tests {
		index := tc.index
		got, err := s.resolve(t, span.SinceReview(index))
		if err != nil {
			t.Fatalf("Resolve(--since-review=%d): %v", index, err)
		}
		if got.From != tc.wantFrom {
			t.Errorf("--since-review=%d resolved From = %s, want %s", index, got.From, tc.wantFrom)
		}
		if got.To != s.f.Head() {
			t.Errorf("--since-review=%d resolved To = %s, want HEAD %s", index, got.To, s.f.Head())
		}
		if got.Base.Kind != span.KindReview || got.Head.Kind != span.KindWorkingTree {
			t.Errorf("--since-review=%d span = %s → %s, want review → working tree",
				index, got.Base.Kind, got.Head.Kind)
		}
		if got.Base.ResolvedIndex != tc.wantRevIdx {
			t.Errorf("--since-review=%d resolved index = %d, want %d",
				index, got.Base.ResolvedIndex, tc.wantRevIdx)
		}
	}
}

// PRD §17.2: `--unreviewed` is `latest-review.commit .. HEAD`, and it is the same
// span as `--since-review=-1`.
func TestResolveUnreviewedIsLatestReviewSpan(t *testing.T) {
	s := newScenario(t, true)

	unreviewed, err := s.resolve(t, span.SinceReview(-1))
	if err != nil {
		t.Fatalf("Resolve(--unreviewed): %v", err)
	}
	// -1 has to mean the same submission as the positive index of the newest one.
	sinceLatest, err := s.resolve(t, span.SinceReview(len(s.reviews)-1))
	if err != nil {
		t.Fatalf("Resolve(--since-review=-1): %v", err)
	}
	if unreviewed.From != s.reviews[2] || unreviewed.From != sinceLatest.From {
		t.Errorf("--unreviewed From = %s, want %s (same as --since-review=-1 = %s)",
			unreviewed.From, s.reviews[2], sinceLatest.From)
	}
	if unreviewed.Base.ResolvedIndex != 2 {
		t.Errorf("--unreviewed resolved index = %d, want 2", unreviewed.Base.ResolvedIndex)
	}
	// The span answers "what happened after I reviewed": it must include the
	// author's response commit and exclude anything before the review.
	changed := s.f.ChangedFiles(unreviewed.From, unreviewed.To)
	if len(changed) != 0 {
		t.Errorf("changed files after the approve = %v, want none (the approve was the last commit)", changed)
	}

	// A span measured from R2 shows the work done since then, including the
	// deletion/edit of reviewer-added lines (PRD §17.2 makes resolution visible).
	fromR2, err := s.resolve(t, span.SinceReview(1))
	if err != nil {
		t.Fatalf("Resolve(--since-review=1): %v", err)
	}
	if got := s.f.ChangedFiles(fromR2.From, fromR2.To); len(got) != 1 || got[0] != "docs.md" {
		t.Errorf("R2..HEAD changed %v, want [docs.md]", got)
	}
}

func TestResolveOutOfRangeIndex(t *testing.T) {
	s := newScenario(t, true)
	for _, index := range []int{3, -4, 99, -99} {
		_, err := s.resolve(t, span.SinceReview(index))
		if err == nil {
			t.Fatalf("Resolve(--since-review=%d) succeeded, want an error", index)
		}
		if errors.Is(err, span.ErrNoReviews) {
			t.Errorf("Resolve(--since-review=%d) = %v, want an out-of-range error, not ErrNoReviews", index, err)
		}
		if !strings.Contains(err.Error(), "3") {
			t.Errorf("error = %v, want it to say how many reviews exist so an agent can recover", err)
		}
	}
}

// PRD §17: a review-relative span cannot exist before the first review. The plan
// asks for a clear "no reviews yet" error rather than an empty diff.
func TestResolveWithoutReviews(t *testing.T) {
	s := newScenario(t, false)

	for _, index := range []int{0, -1} {
		if _, err := s.resolve(t, span.SinceReview(index)); !errors.Is(err, span.ErrNoReviews) {
			t.Errorf("Resolve(review %d) with no reviews = %v, want ErrNoReviews", index, err)
		}
	}
	// The full changeset span still works without any review.
	if _, err := s.resolve(t, span.Full()); err != nil {
		t.Errorf("Resolve(full) with no reviews = %v, want success", err)
	}
}

// Only GitPR review submissions may anchor a review-relative span: an ordinary
// commit whose subject looks like a review must not become a span boundary
// (PRD §10.5).
func TestResolveIgnoresOrdinaryCommits(t *testing.T) {
	s := newScenario(t, true)
	lookalike := s.f.Commit("review: block booking-transaction", gittest.WithFile("lookalike.go", "package main\n"))

	got, err := s.resolve(t, span.SinceReview(-1))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.From == lookalike {
		t.Error("an ordinary commit was used as a review boundary")
	}
	if got.From != s.reviews[2] {
		t.Errorf("From = %s, want the real approve commit %s", got.From, s.reviews[2])
	}
}
