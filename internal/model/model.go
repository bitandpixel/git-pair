// Package model holds the review vocabulary shared by every other package.
package model

import "strings"

// State is the effective lifecycle state of a changeset, always derived from
// commit history rather than stored.
//
// There is no state for a landed changeset. Landing happens with ordinary git, and git-pair
// derives state from a changeset's own markers, so it does not derive the merge: the landing
// is reported by `status`, read out of the destination's history, beside the state rather than
// as another value of it. Markers are the only thing that moves state.
//
// INTEGRATING is the one state that is about the merge without pretending to be it. It is the
// author's statement that the approval is standing and the work is handed to whoever owns the
// destination branch — a request for a merge, never the merge, which stays ordinary git (PRD §26).
type State string

const (
	StateWorking  State = "WORKING"
	StateReady    State = "READY"
	StateBlocked  State = "BLOCKED"
	StateFeedback State = "FEEDBACK"
	StateApproved State = "APPROVED"
	// StateIntegrating is established by `git pair change integrate` and nothing else: the newest
	// marker is the author's declaration that this changeset may be merged. It sits above APPROVED in
	// the lifecycle rather than beside it, because the distinction is one an agent branches on —
	// "a reviewer owes this nothing, and the merge is licensed" — and a field beside `state` would
	// leave every consumer to remember to read it. A rewrite, a drift, or a moved parent still makes
	// `git pair check` refuse: the state says the author handed the work over, not that the gate passed.
	StateIntegrating State = "INTEGRATING"
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
	// TrailerParentHead is the tip of the branch this changeset is stacked on, at the moment a
	// review submission was made. It is written beside `Review-Head` for a stacked changeset and
	// nothing else: the parent branch moves under a child for reasons the child's own history
	// cannot show (PRD §21).
	TrailerParentHead = "Review-Parent-Head"
	// TrailerBaseHead is the commit the submitted diff was measured against, written beside
	// `Review-Head` by every submission. `Review-Parent-Head` names the parent's *branch* tip, which is
	// the measurement only while that branch is the base; once the parent has landed the base is a
	// commit derived from the destination, and the branch that named it may be deleted. This trailer is
	// what keeps the content question — is the diff under test the diff that was approved? — askable
	// after the branch is gone (PRD §21).
	TrailerBaseHead = "Review-Base-Head"
	// TrailerDiffId is the identity of the diff a review submission measured, written beside
	// `Review-Head` by every submission. `Review-Base-Head` names the commit the diff started at, which
	// is what makes the reviewer's diff reproducible and what lets a refusal say *which* side moved;
	// this names the content itself, so the question the gate asks — is this the diff that was
	// approved? — is one comparison rather than an inference from how far the world has moved since
	// (PRD §21). The value is `<version>:<raw>` or `<version>:<raw>+<patch>` — the identity of the content,
	// and the identity of the rendered hunks where this build could compute one — because the value is
	// defined by the rules that produced it (which flags, which paths excluded, which patch-id mode) and
	// changing one of them has to be a version bump rather than a silent edit that mismatches every
	// approval ever written.
	TrailerDiffId = "Review-Diff-Id"

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
	// StateValueIntegrating is written by `change integrate`, and unlike `working` and
	// `abandoned` it *is* a state: INTEGRATING is the answer to "has the author handed this
	// over?", which is the question a merge gate asks. The value is a declaration about the
	// next merge, not a claim that one happened — the record of a landing is the changeset
	// directory in the destination's history, written by the merge itself.
	StateValueIntegrating = "integrating"
)

// DiffIDVersion is the definition the value written by this build was computed under: the raw-diff flags
// `git.Repo.DiffIdentity` pins, the pinned `-U3` and `--default-prefix` on the patch half, `patch-id
// --verbatim`, and the exclusion of the changeset's own directory.
//
// It is a constant rather than a setting because the definition is not the user's to choose — but it is
// not permanent either, and a reader who finds a version it does not know should be told the value predates
// the rule rather than be refused for a mismatch nobody can explain.
const DiffIDVersion = "2"

// DiffID is what a review records about the diff it measured. Two identities of one diff, because they
// answer different questions and neither covers the other:
//
//   - Raw is the identity of the *content* — modes, blob OIDs, statuses, paths. It is the primary claim,
//     and the half that keeps working when the other cannot be computed.
//   - Patch is the identity of the *rendered* diff, and so of what changed and where. It is what lets an
//     approval stand when something else edited a file this branch also edits, on a line nowhere near
//     this branch's own, and what refuses when that edit lands inside the context the reviewer read.
//
// Patch is empty when it was not computed — an older git without `patch-id --verbatim`, or a diff with no
// patch in it. Absence is not a difference: the comparison falls back to Raw alone (§21).
type DiffID struct {
	Raw   string
	Patch string
}

// Empty says no identity was recorded at all.
func (d DiffID) Empty() bool { return d.Raw == "" && d.Patch == "" }

// FormatDiffID renders the pair as the value of a `Review-Diff-Id` trailer:
// `<version>:<raw>` for a content identity alone, `<version>:<raw>+<patch>` when both halves exist.
// One trailer rather than two, because the two halves are two readings of one measurement and a reader
// comparing them should not have to check that two lines describe the same diff.
func FormatDiffID(id DiffID) string {
	if id.Raw == "" {
		return ""
	}
	if id.Patch == "" {
		return DiffIDVersion + ":" + id.Raw
	}
	return DiffIDVersion + ":" + id.Raw + "+" + id.Patch
}

// AcceptsDiffIDVersion reports whether a reader knows what a recorded version means. `1:` carried the raw
// digest alone, which is still the primary claim, so those approvals keep being compared on that half;
// a version nobody recognises is read as no identity rather than as a mismatch.
func AcceptsDiffIDVersion(version string) bool {
	return version == "1" || version == DiffIDVersion
}

// ParseDiffID splits a `Review-Diff-Id` value into its version and its halves.
//
// It reports ok=false for anything that does not read as `<version>:<hex>[+<hex>]`, which is how a marker
// written by a hand-edited commit or a future version is recognised as unreadable rather than being
// compared as if it meant something. Callers treat that as "no digest recorded", which is a fact
// missing rather than a content difference — the direction that does not refuse an approval nobody
// moved (PRD §21).
func ParseDiffID(value string) (version string, id DiffID, ok bool) {
	i := strings.IndexByte(value, ':')
	if i <= 0 {
		return "", DiffID{}, false
	}
	version, rest := value[:i], value[i+1:]
	if rest == "" {
		return "", DiffID{}, false
	}
	raw, patch := rest, ""
	hasPatch := false
	if j := strings.IndexByte(rest, '+'); j >= 0 {
		raw, patch, hasPatch = rest[:j], rest[j+1:], true
	}
	if !isHex(raw) || (hasPatch && !isHex(patch)) {
		return "", DiffID{}, false
	}
	return version, DiffID{Raw: raw, Patch: patch}, true
}

func isHex(s string) bool {
	for j := 0; j < len(s); j++ {
		c := s[j]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			continue
		}
		return false
	}
	return s != ""
}
