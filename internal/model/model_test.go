package model_test

import (
	"testing"

	"gitpair/internal/model"
)

// PRD §23 defines which outcomes let a changeset proceed to integration. Close
// relies on exactly this predicate, so the table is the contract.
func TestOutcomePermitsIntegration(t *testing.T) {
	tests := []struct {
		outcome model.Outcome
		want    bool
		reason  string
	}{
		{model.OutcomeBlock, false, "PRD §23: changes are required before integration"},
		{model.OutcomeFeedback, true, "PRD §23: integration is permitted once other requirements pass"},
		{model.OutcomeApprove, true, "PRD §23: integration is permitted once CI/policy passes"},
		{model.Outcome(""), false, "an empty outcome is not a review verdict"},
		{model.Outcome("approved"), false, "an unrecognised outcome must not permit integration"},
	}
	for _, tc := range tests {
		t.Run(string(tc.outcome)+"/"+tc.reason, func(t *testing.T) {
			if got := tc.outcome.PermitsIntegration(); got != tc.want {
				t.Errorf("%q.PermitsIntegration() = %v, want %v (%s)", tc.outcome, got, tc.want, tc.reason)
			}
		})
	}
}

// PRD §12 lists the effective states; PRD §23 maps each outcome onto one.
func TestOutcomeState(t *testing.T) {
	tests := []struct {
		outcome model.Outcome
		want    model.State
	}{
		{model.OutcomeBlock, model.StateBlocked},
		{model.OutcomeFeedback, model.StateFeedback},
		{model.OutcomeApprove, model.StateApproved},
		{model.Outcome("nonsense"), model.StateWorking},
	}
	for _, tc := range tests {
		if got := tc.outcome.State(); got != tc.want {
			t.Errorf("%q.State() = %s, want %s", tc.outcome, got, tc.want)
		}
	}
}

func TestParseOutcome(t *testing.T) {
	for _, valid := range []string{"block", "feedback", "approve"} {
		got, ok := model.ParseOutcome(valid)
		if !ok || got != model.Outcome(valid) {
			t.Errorf("ParseOutcome(%q) = (%q, %v), want (%q, true)", valid, got, ok, valid)
		}
	}
	for _, invalid := range []string{"", "Block", "APPROVE", "approved", "block ", "reject"} {
		if got, ok := model.ParseOutcome(invalid); ok {
			t.Errorf("ParseOutcome(%q) = (%q, true), want it rejected", invalid, got)
		}
	}
}

func TestOutcomeValid(t *testing.T) {
	for _, tc := range []struct {
		outcome model.Outcome
		want    bool
	}{
		{model.OutcomeBlock, true},
		{model.OutcomeFeedback, true},
		{model.OutcomeApprove, true},
		{model.Outcome(""), false},
		{model.Outcome("closed"), false},
	} {
		if got := tc.outcome.Valid(); got != tc.want {
			t.Errorf("%q.Valid() = %v, want %v", tc.outcome, got, tc.want)
		}
	}
}

// The trailer keys below are the whole machine-readable contract between git-pair's
// commits and any agent reading them (PRD §9.2, §10.4, §23), so their exact
// spelling is pinned here.
func TestTrailerVocabulary(t *testing.T) {
	tests := []struct{ got, want string }{
		{model.TrailerOutcome, "Review-Outcome"},
		{model.TrailerState, "Review-State"},
		{model.TrailerChangeset, "Review-Changeset"},
		{model.StateValueReady, "ready"},
		{model.StateValueClosed, "closed"},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("trailer vocabulary = %q, want %q", tc.got, tc.want)
		}
	}
}

// PRD §12 names the six effective states verbatim, and `status --json`/agents
// compare them by string, so the spelling is part of the contract.
func TestStateVocabulary(t *testing.T) {
	tests := []struct {
		got  model.State
		want string
	}{
		{model.StateWorking, "WORKING"},
		{model.StateReady, "READY"},
		{model.StateBlocked, "BLOCKED"},
		{model.StateFeedback, "FEEDBACK"},
		{model.StateApproved, "APPROVED"},
		{model.StateClosed, "CLOSED"},
	}
	for _, tc := range tests {
		if string(tc.got) != tc.want {
			t.Errorf("state = %q, want %q", tc.got, tc.want)
		}
	}
}
