// Package model holds the review vocabulary shared by every other package.
package model

// State is the effective lifecycle state of a changeset, always derived from
// commit history rather than stored.
type State string

const (
	StateWorking  State = "WORKING"
	StateReady    State = "READY"
	StateBlocked  State = "BLOCKED"
	StateFeedback State = "FEEDBACK"
	StateApproved State = "APPROVED"
	StateClosed   State = "CLOSED"
)

// Outcome is the verdict recorded by a review submission.
type Outcome string

const (
	OutcomeBlock    Outcome = "block"
	OutcomeFeedback Outcome = "feedback"
	OutcomeApprove  Outcome = "approve"
)

// ParseOutcome converts a CLI flag value into an Outcome.
func ParseOutcome(s string) (Outcome, bool) {
	switch Outcome(s) {
	case OutcomeBlock:
		return OutcomeBlock, true
	case OutcomeFeedback:
		return OutcomeFeedback, true
	case OutcomeApprove:
		return OutcomeApprove, true
	}
	return "", false
}

// Valid reports whether o is one of the three review outcomes.
func (o Outcome) Valid() bool {
	switch o {
	case OutcomeBlock, OutcomeFeedback, OutcomeApprove:
		return true
	}
	return false
}

// PermitsIntegration reports whether a review with this outcome lets the
// changeset proceed to close. Block does not; feedback and approve do.
func (o Outcome) PermitsIntegration() bool {
	return o == OutcomeFeedback || o == OutcomeApprove
}

// State maps a review outcome onto the effective state it establishes.
func (o Outcome) State() State {
	switch o {
	case OutcomeBlock:
		return StateBlocked
	case OutcomeFeedback:
		return StateFeedback
	case OutcomeApprove:
		return StateApproved
	}
	return StateWorking
}

// Trailer keys written into lifecycle commits. A commit is only treated as a
// git-pair lifecycle marker when the keys below appear together with a changeset
// value matching the changeset being inspected.
const (
	TrailerOutcome   = "Review-Outcome"
	TrailerState     = "Review-State"
	TrailerChangeset = "Review-Changeset"

	StateValueReady  = "ready"
	StateValueClosed = "closed"
)
