package lifecycle

import (
	"strings"
	"testing"
	"time"

	"gitpair/internal/model"
)

// These tests drive state derivation over synthesised histories, which is where
// PRD §12's rules live. The integration tests in history_test.go replay the same
// rules against real commits.

func impl(sha string) Event {
	return Event{SHA: sha, Short: short(sha), Subject: "implementation"}
}

func ready(sha string) Event {
	e := Event{SHA: sha, Short: short(sha), Subject: "git-pair: ready booking", Kind: KindReady}
	return e
}

func review(sha string, outcome model.Outcome) Event {
	return Event{
		SHA: sha, Short: short(sha), Kind: KindReview, Outcome: outcome,
		Subject: "review: " + string(outcome) + " booking",
	}
}

func closed(sha string) Event {
	return Event{SHA: sha, Short: short(sha), Subject: "git-pair: close booking", Kind: KindClosed}
}

func malformed(sha string) Event {
	return Event{
		SHA: sha, Short: short(sha), Subject: "hand-written trailer",
		UnrecognisedMarker: true,
	}
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// TestDeriveStateIsPRD12Lifecycle walks the effective lifecycle PRD §12 spells
// out: initialized -> working -> ready -> review block -> working -> ready ->
// review feedback/approve -> close.
func TestDeriveStateIsPRD12Lifecycle(t *testing.T) {
	tests := []struct {
		name      string
		events    []Event
		want      model.State
		wantStale bool
		reviews   int
	}{
		{"no commits above the base yet", nil, model.StateWorking, false, 0},
		{"implementation only", []Event{impl("c1")}, model.StateWorking, false, 0},
		{"ready", []Event{impl("c1"), ready("c2")}, model.StateReady, false, 0},
		{
			"implementation after ready invalidates it",
			[]Event{impl("c1"), ready("c2"), impl("c3")}, model.StateWorking, true, 0,
		},
		{
			"review block",
			[]Event{impl("c1"), ready("c2"), review("c3", model.OutcomeBlock)},
			model.StateBlocked, false, 1,
		},
		{
			"author response after block",
			[]Event{impl("c1"), ready("c2"), review("c3", model.OutcomeBlock), impl("c4")},
			model.StateWorking, true, 1,
		},
		{
			"ready again",
			[]Event{impl("c1"), ready("c2"), review("c3", model.OutcomeBlock), impl("c4"), ready("c5")},
			model.StateReady, false, 1,
		},
		{
			"review feedback",
			[]Event{impl("c1"), ready("c2"), review("c3", model.OutcomeBlock), impl("c4"),
				ready("c5"), review("c6", model.OutcomeFeedback)},
			model.StateFeedback, false, 2,
		},
		{
			"review approve",
			[]Event{impl("c1"), ready("c2"), review("c3", model.OutcomeBlock), impl("c4"),
				ready("c5"), review("c6", model.OutcomeApprove)},
			model.StateApproved, false, 2,
		},
		{
			"close",
			[]Event{impl("c1"), ready("c2"), review("c3", model.OutcomeApprove), closed("c4")},
			model.StateClosed, false, 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := derive(tc.events)
			if got.State != tc.want {
				t.Errorf("derive().State = %s, want %s (reason: %s)", got.State, tc.want, got.Reason)
			}
			if got.Stale != tc.wantStale {
				t.Errorf("derive().Stale = %v, want %v", got.Stale, tc.wantStale)
			}
			if len(got.Reviews) != tc.reviews {
				t.Errorf("derive().Reviews = %d, want %d", len(got.Reviews), tc.reviews)
			}
			if got.Reason == "" {
				t.Error("derive() produced no reason; status and `status --json` report one")
			}
		})
	}
}

// PRD §12's named safety property, called out in the plan's M2 verification:
// "Any implementation commit after a ready/review/approval marker invalidates
// that marker for the current HEAD."
func TestDeriveImplementationCommitAfterApproveYieldsWorking(t *testing.T) {
	got := derive([]Event{
		impl("c1"), ready("c2"), review("c3", model.OutcomeApprove), impl("c4"),
	})
	if got.State != model.StateWorking {
		t.Errorf("state = %s, want WORKING: approval must not survive an implementation commit", got.State)
	}
	if !got.Stale {
		t.Error("Stale = false, want true")
	}
	// derive only counts commits; it cannot tell an implementation commit from a
	// changeset-only one, so it says "commit(s)" and lets ReconcileStaleness
	// decide whether they matter.
	if !strings.Contains(got.Reason, "commit(s) after review") {
		t.Errorf("Reason = %q, want it to count the commits after the approve", got.Reason)
	}
	// The approval is still history: `review history` must list it (PRD §10.5).
	if len(got.Reviews) != 1 || got.LatestReview == nil || got.LatestReview.Outcome != model.OutcomeApprove {
		t.Errorf("Reviews = %v, want the approve to stay listed", got.Reviews)
	}
}

func TestDeriveClosedBeatsEverythingElse(t *testing.T) {
	got := derive([]Event{
		impl("c1"), review("c2", model.OutcomeApprove), closed("c3"), impl("c4"),
	})
	if got.State != model.StateWorking {
		t.Errorf("state = %s, want WORKING: an implementation commit after close also invalidates it", got.State)
	}
	got = derive([]Event{impl("c1"), closed("c2")})
	if got.State != model.StateClosed {
		t.Errorf("state = %s, want CLOSED", got.State)
	}
}

func TestDeriveNewestMarkerWins(t *testing.T) {
	got := derive([]Event{
		impl("c1"), ready("c2"), review("c3", model.OutcomeBlock), ready("c4"),
	})
	if got.State != model.StateReady {
		t.Errorf("state = %s, want READY: the newest marker decides", got.State)
	}
	if got.Marker == nil || got.Marker.SHA != "c4" {
		t.Errorf("Marker = %+v, want c4", got.Marker)
	}
}

func TestDeriveMalformedMarkerIsImplementation(t *testing.T) {
	// The plan's risk table: a commit carrying Review-* trailers that git-pair
	// cannot interpret must be read as an implementation commit, never as a
	// marker, and never silently honoured.
	got := derive([]Event{impl("c1"), ready("c2"), malformed("c3")})
	if got.State != model.StateWorking {
		t.Errorf("state = %s, want WORKING: a malformed marker must invalidate the ready marker", got.State)
	}
	if len(got.Unrecognised) != 1 || got.Unrecognised[0].SHA != "c3" {
		t.Errorf("Unrecognised = %v, want c3 listed for the warning line", got.Unrecognised)
	}

	got = derive([]Event{impl("c1"), malformed("c2")})
	if got.State != model.StateWorking {
		t.Errorf("state = %s, want WORKING", got.State)
	}
	if got.Marker != nil {
		t.Errorf("Marker = %+v, want nil: a malformed marker establishes no state", got.Marker)
	}
	if len(got.Reviews) != 0 {
		t.Errorf("Reviews = %v, want none", got.Reviews)
	}
}

func TestDeriveOnlyReviewSubmissionsCountAsReviews(t *testing.T) {
	got := derive([]Event{
		impl("c1"), ready("c2"), review("c3", model.OutcomeBlock), impl("c4"),
		review("c5", model.OutcomeFeedback), impl("c6"), review("c7", model.OutcomeApprove),
	})
	want := []string{"c3", "c5", "c7"}
	if len(got.Reviews) != len(want) {
		t.Fatalf("Reviews = %v, want %v", got.Reviews, want)
	}
	for i, sha := range want {
		if got.Reviews[i].SHA != sha {
			t.Errorf("Reviews[%d] = %s, want %s (PRD §10.5: chronological index from 0)", i, got.Reviews[i].SHA, sha)
		}
	}
	if got.LatestReview == nil || got.LatestReview.SHA != "c7" {
		t.Errorf("LatestReview = %+v, want c7", got.LatestReview)
	}
}

func TestReviewIndex(t *testing.T) {
	s := derive([]Event{review("r0", model.OutcomeBlock), impl("c"), review("r1", model.OutcomeFeedback), review("r2", model.OutcomeApprove)})
	tests := []struct {
		index   int
		wantSHA string
		wantOK  bool
	}{
		{0, "r0", true},
		{1, "r1", true},
		{2, "r2", true},
		{-1, "r2", true},
		{-2, "r1", true},
		{-3, "r0", true},
		{3, "", false},
		{-4, "", false},
	}
	for _, tc := range tests {
		got, ok := s.ReviewIndex(tc.index)
		if ok != tc.wantOK || (ok && got.SHA != tc.wantSHA) {
			t.Errorf("ReviewIndex(%d) = (%s, %v), want (%s, %v)", tc.index, got.SHA, ok, tc.wantSHA, tc.wantOK)
		}
	}
	empty := derive(nil)
	if _, ok := empty.ReviewIndex(-1); ok {
		t.Error("ReviewIndex(-1) succeeded with no reviews")
	}
}

func TestParseTrailers(t *testing.T) {
	block := strings.Join([]string{
		"Review-Outcome: block",
		"Review-Changeset: booking",
		"Review-Outcome: approve",
		"Reviewed-by: someone",
		"not a trailer",
		"",
	}, "\n")

	got := parseTrailers(block)
	if got["Review-Outcome"] != "block" {
		t.Errorf("Review-Outcome = %q, want the first value block: a duplicated key must not be smuggled past a check", got["Review-Outcome"])
	}
	if got["Review-Changeset"] != "booking" {
		t.Errorf("Review-Changeset = %q", got["Review-Changeset"])
	}
	if _, ok := got["Reviewed-by"]; ok {
		t.Errorf("non-review trailer leaked in: %v", got)
	}
	if len(got) != 2 {
		t.Errorf("parseTrailers = %v, want only the two Review-* keys", got)
	}
}

func TestParseEventClassifiesMarkers(t *testing.T) {
	tests := []struct {
		name        string
		rec         []string
		wantKind    Kind
		wantOutcome model.Outcome
		wantUnrecog bool
	}{
		{"plain commit", []string{"c1", "c1", "0", "author", "implement stuff", ""}, KindImplementation, "", false},
		{"ready marker", []string{"c1", "c1", "0", "author", "git-pair: ready booking", "Review-State: ready\nReview-Changeset: booking\n"}, KindReady, "", false},
		{"close marker", []string{"c1", "c1", "0", "author", "git-pair: close booking", "Review-State: closed\nReview-Changeset: booking\n"}, KindClosed, "", false},
		{"review block", []string{"c1", "c1", "0", "author", "review: block booking", "Review-Outcome: block\nReview-Changeset: booking\n"}, KindReview, model.OutcomeBlock, false},
		{"wrong changeset", []string{"c1", "c1", "0", "author", "git-pair: ready other", "Review-State: ready\nReview-Changeset: other\n"}, KindImplementation, "", true},
		{"missing changeset trailer", []string{"c1", "c1", "0", "author", "git-pair: ready booking", "Review-State: ready\n"}, KindImplementation, "", true},
		{"unknown state", []string{"c1", "c1", "0", "author", "git-pair: ready booking", "Review-State: READY\nReview-Changeset: booking\n"}, KindImplementation, "", true},
		{"unknown outcome", []string{"c1", "c1", "0", "author", "review: approve booking", "Review-Outcome: approved\nReview-Changeset: booking\n"}, KindImplementation, "", true},
		{"changeset scaffold commit", []string{"c1", "c1", "0", "author", "git-pair: initialize changeset booking", "Review-Changeset: booking\n"}, KindImplementation, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseEvent("booking", tc.rec)
			if got.Kind != tc.wantKind {
				t.Errorf("Kind = %s, want %s", got.Kind, tc.wantKind)
			}
			if got.Outcome != tc.wantOutcome {
				t.Errorf("Outcome = %q, want %q", got.Outcome, tc.wantOutcome)
			}
			if got.UnrecognisedMarker != tc.wantUnrecog {
				t.Errorf("UnrecognisedMarker = %v, want %v", got.UnrecognisedMarker, tc.wantUnrecog)
			}
			if got.Marker() != (tc.wantKind != KindImplementation) {
				t.Errorf("Marker() = %v for kind %s", got.Marker(), got.Kind)
			}
		})
	}
}

func TestEventStateAndKindString(t *testing.T) {
	if got := ready("c1").State(); got != model.StateReady {
		t.Errorf("ready marker state = %s", got)
	}
	if got := closed("c1").State(); got != model.StateClosed {
		t.Errorf("close marker state = %s", got)
	}
	if got := review("c1", model.OutcomeFeedback).State(); got != model.StateFeedback {
		t.Errorf("feedback review state = %s", got)
	}
	if got := impl("c1").State(); got != model.StateWorking {
		t.Errorf("implementation state = %s", got)
	}
	for kind, want := range map[Kind]string{
		KindImplementation: "implementation", KindReady: "ready", KindReview: "review", KindClosed: "closed",
	} {
		if got := kind.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", kind, got, want)
		}
	}
}

func TestAge(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		age  time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m"},
		{18 * time.Minute, "18m"},
		{3 * time.Hour, "3h"},
		{2 * 24 * time.Hour, "2d"},
		{-time.Hour, "0s"},
	}
	for _, tc := range tests {
		if got := Age(now.Add(-tc.age), now); got != tc.want {
			t.Errorf("Age(-%v) = %q, want %q", tc.age, got, tc.want)
		}
	}
}

func TestRangeForHead(t *testing.T) {
	if got := RangeForHead("main", "HEAD"); got != "main..HEAD" {
		t.Errorf("RangeForHead = %q, want main..HEAD: two-dot so only this branch's commits are walked", got)
	}
	if got := RangeForHead("booking-transaction", "booking-transaction-tests"); got != "booking-transaction..booking-transaction-tests" {
		t.Errorf("RangeForHead for a stacked branch = %q", got)
	}
}
