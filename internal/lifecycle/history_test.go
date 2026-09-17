package lifecycle_test

import (
	"context"
	"strings"
	"testing"

	"gitpair/internal/git"
	"gitpair/internal/gittest"
	"gitpair/internal/lifecycle"
	"gitpair/internal/model"
)

func repo(f *gittest.Fixture) *git.Repo { return &git.Repo{Dir: f.Dir()} }

func summarize(t *testing.T, f *gittest.Fixture, slug, base, headRef string) lifecycle.Summary {
	t.Helper()
	summary, err := lifecycle.Summarize(context.Background(), repo(f), slug, base, headRef)
	if err != nil {
		t.Fatalf("Summarize(%s, base %s, head %s): %v", slug, base, headRef, err)
	}
	return summary
}

// TestSummarizePRD12GoldenHistory replays the PRD §12 lifecycle against real
// commits and checks the derived state after every step, which is the golden
// history the plan's M2 verification asks for.
func TestSummarizePRD12GoldenHistory(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking-transaction")
	slug, base := "booking-transaction", "main"

	steps := []struct {
		name      string
		act       func()
		want      model.State
		wantRev   int
		wantStale bool
	}{
		{
			name: "implementation only",
			act: func() {
				f.CommitChangeset(slug, base)
				f.Commit("implement locking", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))
			},
			want: model.StateWorking, wantRev: 0,
		},
		{
			name: "ready",
			act:  func() { f.CommitReadyMarker(slug) },
			want: model.StateReady, wantRev: 0,
		},
		{
			name: "review block",
			act: func() {
				f.CommitReviewMarker(slug, "block",
					gittest.WithFile("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n"))
			},
			want: model.StateBlocked, wantRev: 1,
		},
		{
			name:      "author response returns to working",
			act:       func() { f.Commit("address review", gittest.WithFile("other.go", "package main\n\nvar x = 1\n")) },
			want:      model.StateWorking,
			wantRev:   1,
			wantStale: true,
		},
		{
			name: "ready again",
			act:  func() { f.CommitReadyMarker(slug) },
			want: model.StateReady, wantRev: 1,
		},
		{
			name:      "review feedback",
			act:       func() { f.CommitReviewMarker(slug, "feedback", gittest.WithFile("notes.md", "note\n")) },
			want:      model.StateFeedback,
			wantRev:   2,
			wantStale: false,
		},
		{
			name:      "review approve",
			act:       func() { f.CommitReviewMarker(slug, "approve") },
			want:      model.StateApproved,
			wantRev:   3,
			wantStale: false,
		},
	}

	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			step.act()
			got := summarize(t, f, slug, base, "HEAD")
			if got.State != step.want {
				t.Errorf("state = %s, want %s (reason: %s)", got.State, step.want, got.Reason)
			}
			if len(got.Reviews) != step.wantRev {
				t.Errorf("reviews = %d, want %d", len(got.Reviews), step.wantRev)
			}
			if got.Stale != step.wantStale {
				t.Errorf("stale = %v, want %v (reason: %s)", got.Stale, step.wantStale, got.Reason)
			}
		})
	}
}

// TestSummarizeImplementationAfterApproveIsWorking is the PRD §12 safety property
// against real commits: "review: approve" followed by an agent implementation
// commit must not leave the branch looking approved.
func TestSummarizeImplementationAfterApproveIsWorking(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	slug, base := "booking", "main"

	f.CommitChangeset(slug, base)
	f.CommitReadyMarker(slug)
	approve := f.CommitReviewMarker(slug, "approve")

	if got := summarize(t, f, slug, base, "HEAD"); got.State != model.StateApproved {
		t.Fatalf("state before the agent commit = %s, want APPROVED", got.State)
	}

	after := f.Commit("agent: address feedback", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))
	got := summarize(t, f, slug, base, "HEAD")
	if got.State != model.StateWorking {
		t.Errorf("state = %s, want WORKING", got.State)
	}
	if !got.Stale {
		t.Error("stale = false, want true")
	}
	if got.Marker == nil || got.Marker.SHA != approve {
		t.Errorf("Marker = %+v, want the (now stale) approve %s", got.Marker, approve)
	}
	// The branch head is the implementation commit, not the approval.
	if f.Head() != after {
		t.Fatalf("HEAD = %s, want %s", f.Head(), after)
	}
}

// PRD §10.5: "Only commits explicitly identified as review marker commits
// count as reviews. Ordinary Git commits do not."
func TestSummarizeIgnoresOrdinaryCommitsAsReviews(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	slug, base := "booking", "main"

	f.CommitChangeset(slug, base)
	f.Commit("review: looks fine to me", gittest.WithFile("a.txt", "a\n"))
	f.Commit("review: block booking", gittest.WithFile("b.txt", "b\n"))
	f.Commit("Review-Outcome: block", gittest.WithFile("c.txt", "c\n"))

	got := summarize(t, f, slug, base, "HEAD")
	if len(got.Reviews) != 0 {
		t.Errorf("reviews = %v, want none: a commit subject or message body is not a review submission", got.Reviews)
	}
	if got.State != model.StateWorking {
		t.Errorf("state = %s, want WORKING", got.State)
	}
}

// The plan's risk table: "Accept a marker only with both expected trailers
// (Review-Changeset matching); malformed ⇒ warning line, not a state change."
// Each of these commits carries Review-* trailers that must not be honoured.
func TestSummarizeMalformedAndMismatchedTrailersAreImplementation(t *testing.T) {
	tests := []struct {
		name    string
		message string
		// wantWarn is whether git-pair can see the malformed trailer at all. When a
		// hand-written trailer block is not a valid trailer block, git's own
		// `%(trailers)` expansion reports nothing, so git-pair cannot warn about it
		// either; what matters is that no state is established.
		wantWarn bool
	}{
		{
			name:     "ready marker for a different changeset",
			message:  "git-pair: ready other-changeset\n\nReview-State: ready\nReview-Changeset: other-changeset\n",
			wantWarn: true,
		},
		{
			name:     "ready marker with no changeset trailer",
			message:  "git-pair: ready booking\n\nReview-State: ready\n",
			wantWarn: true,
		},
		{
			name:     "unknown state value",
			message:  "git-pair: ready booking\n\nReview-State: READY\nReview-Changeset: booking\n",
			wantWarn: true,
		},
		{
			name:     "unknown outcome value",
			message:  "review: approve booking\n\nReview-Outcome: approved\nReview-Changeset: booking\n",
			wantWarn: true,
		},
		{
			name:     "review marker with no changeset trailer",
			message:  "review: block booking\n\nReview-Outcome: block\n",
			wantWarn: true,
		},
		{
			name:     "changeset trailer line without a colon",
			message:  "review: block booking\n\nReview-Outcome: block\nReview-Changeset booking\n",
			wantWarn: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := gittest.New(t)
			f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
			f.CreateBranch("booking")
			slug, base := "booking", "main"
			f.CommitChangeset(slug, base)

			f.CommitMessage(tc.message, gittest.WithEmpty())

			got := summarize(t, f, slug, base, "HEAD")
			if got.State != model.StateWorking {
				t.Errorf("state = %s, want WORKING: %s must not establish a lifecycle state", got.State, tc.name)
			}
			if len(got.Reviews) != 0 {
				t.Errorf("reviews = %v, want none", got.Reviews)
			}
			if got.Marker != nil {
				t.Errorf("Marker = %+v, want nil", got.Marker)
			}
			if len(got.Unrecognised) == 0 && tc.wantWarn {
				t.Error("Unrecognised is empty: the plan requires a warning line for an unreadable marker")
			}
			if !tc.wantWarn && len(got.Unrecognised) != 0 {
				t.Errorf("Unrecognised = %v, want empty: git reports no trailer block, so there is nothing to warn about", got.Unrecognised)
			}
		})
	}
}

// PRD §21: stacked branches each have their own review history. A sibling
// changeset is the base, and only the branch's own commits are walked.
func TestSummarizeStackedChangesetsHaveIndependentHistories(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))

	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("booking impl", gittest.WithFile("service.go", "package main\n"))
	f.CommitReadyMarker("booking")
	f.CommitReviewMarker("booking", "block", gittest.WithFile("service.go", "package main\n\n// comment\n"))

	f.CreateBranch("booking-tests", "booking")
	f.CommitChangeset("booking-tests", "booking")
	f.Commit("tests impl", gittest.WithFile("service_test.go", "package main\n\nfunc TestLock() {}\n"))
	f.CommitReadyMarker("booking-tests")

	below := summarize(t, f, "booking", "main", "booking")
	if len(below.Reviews) != 1 || below.Reviews[0].Outcome != model.OutcomeBlock {
		t.Errorf("lower changeset reviews = %v, want its own single block review", below.Reviews)
	}
	if below.State != model.StateBlocked {
		t.Errorf("lower changeset state = %s, want BLOCKED", below.State)
	}

	top := summarize(t, f, "booking-tests", "booking", "HEAD")
	if len(top.Reviews) != 0 {
		t.Errorf("upper changeset reviews = %v, want none: it has never been reviewed", top.Reviews)
	}
	if top.State != model.StateReady {
		t.Errorf("upper changeset state = %s, want READY", top.State)
	}
	// Its history is only its own commits, measured against the sibling branch.
	if len(top.Events) != 3 {
		t.Errorf("upper changeset events = %d (%v), want 3: changeset dir, implementation, ready marker",
			len(top.Events), subjects(top.Events))
	}
}

// A changeset whose base cannot be resolved must fail loudly rather than guess a
// trunk (plan risk table: "never guess; test covers missing base").
func TestSummarizeUnresolvableBaseFails(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "does-not-exist")

	_, err := lifecycle.Summarize(context.Background(), repo(f), "booking", "does-not-exist", "HEAD")
	if err == nil {
		t.Fatal("Summarize succeeded with an unresolvable base")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error = %v, want it to name the unresolvable base", err)
	}
	if _, err := lifecycle.Summarize(context.Background(), repo(f), "booking", "", "HEAD"); err == nil {
		t.Error("Summarize succeeded with an empty base")
	}
}

func TestSummarizeOtherHeadRef(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.CommitReadyMarker("booking")
	ready := f.Head()

	// Inspecting another branch must not depend on it being checked out.
	f.CreateBranch("other", "main")
	got := summarize(t, f, "booking", "main", "booking")
	if got.State != model.StateReady {
		t.Errorf("state of the non-checked-out branch = %s, want READY", got.State)
	}
	if got.Marker == nil || got.Marker.SHA != ready {
		t.Errorf("Marker = %+v, want the ready marker %s", got.Marker, ready)
	}
}

func subjects(events []lifecycle.Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Subject)
	}
	return out
}

// PRD §421 invalidates a ready marker on a later *implementation* commit. A commit
// that touches nothing but changesets/<slug>/ is not one, and the reviewer reads
// ABOUT.md and threads from HEAD anyway, so the marker must stand.
func TestSummarizeChangesetOnlyCommitDoesNotInvalidateReady(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	slug, base := "booking", "main"
	f.CommitChangeset(slug, base)
	f.CommitReadyMarker(slug)

	f.WriteChangesetFile(slug, "ABOUT.md", "# booking\n\n## Summary\n\nExpanded for the reviewer.\n")
	f.Commit("describe booking")

	got := summarize(t, f, slug, base, "HEAD")
	if got.State != model.StateReady {
		t.Errorf("state = %s, want READY: no implementation code changed (reason: %s)", got.State, got.Reason)
	}
	if got.Stale {
		t.Error("Stale = true, want false")
	}
	if !strings.Contains(got.Reason, "changeset-only") {
		t.Errorf("reason = %q, want it to name the commits it looked past", got.Reason)
	}
}

// The boundary the tree comparison has to get right: one commit touching both the
// changeset directory and code is an implementation commit, because the code moved.
func TestSummarizeMixedCommitInvalidatesReady(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	slug, base := "booking", "main"
	f.CommitChangeset(slug, base)
	f.CommitReadyMarker(slug)

	f.WriteChangesetFile(slug, "ABOUT.md", "# booking\n\n## Summary\n\nAlso touched the code.\n")
	f.Commit("address feedback", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))

	got := summarize(t, f, slug, base, "HEAD")
	if got.State != model.StateWorking {
		t.Errorf("state = %s, want WORKING: code changed above the marker", got.State)
	}
	if !got.Stale {
		t.Error("Stale = false, want true")
	}
	if !strings.Contains(got.Reason, "code changed since ready") {
		t.Errorf("reason = %q, want it to say the code changed since the marker", got.Reason)
	}
}

// Counting commits gets merges and rebases wrong; comparing trees does not. Here
// the changeset is unchanged and merged into an advanced trunk, so the reviewed
// code is still exactly what was approved.
func TestSummarizeBaseMovingUnderAReadyChangesetKeepsReady(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	slug, base := "booking", "main"
	f.CommitChangeset(slug, base)
	f.CommitReadyMarker(slug)

	f.SwitchTo("main")
	f.Commit("trunk work", gittest.WithFile("other.go", "package main\n"))
	f.SwitchTo("booking")

	if got := summarize(t, f, slug, base, "HEAD"); got.State != model.StateReady {
		t.Errorf("state = %s, want READY: the reviewed code is untouched (reason: %s)", got.State, got.Reason)
	}
}
