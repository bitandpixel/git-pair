// Package model holds the review vocabulary shared by every other package.
package model

// State is the effective lifecycle state of a changeset, always derived from
// commit history rather than stored.
//
// There is no state for a landed changeset. Landing happens with ordinary git, and git-pair
// derives state from a changeset's own markers, so it does not derive the merge: the record
// written by `git pair integration record` is reported by `status`, beside the state rather than
// as another value of it. Markers are the only thing that moves state.
type State string

const (
	StateWorking  State = "WORKING"
	StateReady    State = "READY"
	StateBlocked  State = "BLOCKED"
	StateFeedback State = "FEEDBACK"
	StateApproved State = "APPROVED"
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

// PermitsIntegration reports whether a review with this outcome lets the owner
// take the changeset forward. Block does not; feedback and approve do.
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
	// TrailerHead is the commit a review submission spoke about, written on review
	// commits only. It is what makes an approval about a piece of history rather than
	// about a tree: a rebase rewrites the review commit but preserves its message, so
	// the rewritten marker still names a head that is no longer in this line, and the
	// ancestry test refuses it (PRD §11.3, §12).
	TrailerHead = "Review-Head"

	StateValueReady = "ready"
	// StateValueWorking is written by `change unready`. It is not a new state: WORKING
	// is what a changeset with no marker derives anyway, so the value exists to let an
	// author say so on purpose rather than leaving a reviewer to infer it from a diff.
	StateValueWorking = "working"
	// StateValueAbandoned is written by `change abandon`. Like `working` it is not a
	// sixth lifecycle state: `abandoned` is a fact recorded beside the state
	// (Summary.Abandoned), not a value an agent branches `state` on. A changeset that
	// can never move again has to be recognisable without learning a new state name,
	// and every consumer that switches on `state` would have to learn one.
	StateValueAbandoned = "abandoned"
)
