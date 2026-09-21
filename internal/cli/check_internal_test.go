package cli

import (
	"strings"
	"testing"

	"gitpair/internal/lifecycle"
	"gitpair/internal/model"
)

// The predicate is the product here: `git pair check` exists to turn the derivation into an
// exit code, so the conditions are tested as a function of what the derivation said, with git
// out of the picture. check_test.go covers the surface — exit codes, output, `--json`.
//
// Each case names the condition it pins, because the point of listing every failure is that
// each one is reported independently: a case that only counts reasons would pass if two
// conditions were merged into one message.
//
// There is no archive condition any more. The gate used to ask whether a ref pointed at HEAD; it now
// asks the derivation the same question directly — is the content the newest review spoke about still
// what HEAD carries — which is one reading rather than two that could disagree.
func TestIntegrationReasons(t *testing.T) {
	const (
		slug      = "booking"
		head      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		landedSHA = "ffffffffffffffffffffffffffffffffffffffff"
	)
	approve := &lifecycle.Event{SHA: head, Short: "aaaaaaa", Kind: lifecycle.KindReview, Outcome: model.OutcomeApprove}
	feedback := &lifecycle.Event{SHA: head, Short: "aaaaaaa", Kind: lifecycle.KindReview, Outcome: model.OutcomeFeedback}
	block := &lifecycle.Event{SHA: head, Short: "aaaaaaa", Kind: lifecycle.KindReview, Outcome: model.OutcomeBlock}
	ready := &lifecycle.Event{SHA: "cccccccccccccccccccccccccccccccccccccccc", Short: "ccccccc", Kind: lifecycle.KindReady}
	unready := &lifecycle.Event{SHA: "dddddddddddddddddddddddddddddddddddddddd", Short: "ddddddd", Kind: lifecycle.KindUnready}
	abandon := &lifecycle.Event{SHA: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", Short: "eeeeeee", Kind: lifecycle.KindAbandoned}

	tests := []struct {
		name string
		// terminal, summary and head are the inputs the command reads; policy is the only one a
		// caller may choose.
		terminal *lifecycle.Event
		summary  lifecycle.Summary
		head     string
		feedback bool
		// landed is the integration record, if one exists — not a policy a caller may choose, but
		// an input the command reads like the others.
		landed landing
		// n is how many reasons the case must produce, so a merged or dropped condition
		// shows up even where the wording matches; `contains` names text the reasons must
		// carry, and one reason may satisfy more than one entry.
		n        int
		contains []string
		not      []string
	}{
		{
			name:    "an approved head passes",
			summary: lifecycle.Summary{Marker: approve, LatestReview: approve, State: model.StateApproved},
			head:    head,
			n:       0,
		},
		{
			name:     "feedback is not enough under the default policy",
			summary:  lifecycle.Summary{Marker: feedback, LatestReview: feedback, State: model.StateFeedback},
			head:     head,
			n:        1,
			contains: []string{"the newest review is feedback", "--allow-feedback"},
		},
		{
			name:     "feedback is enough when the policy allows it",
			summary:  lifecycle.Summary{Marker: feedback, LatestReview: feedback, State: model.StateFeedback},
			head:     head,
			feedback: true,
			n:        0,
		},
		{
			name:     "a block fails",
			summary:  lifecycle.Summary{Marker: block, LatestReview: block, State: model.StateBlocked},
			head:     head,
			n:        1,
			contains: []string{"latest review outcome is blocking"},
		},
		{
			// A review whose outcome git-pair cannot name fails closed: the gate cannot
			// infer permission from a trailer it could not read.
			name:     "an outcome git-pair cannot read fails",
			summary:  lifecycle.Summary{Marker: &lifecycle.Event{Short: "aaaaaaa", Kind: lifecycle.KindReview}, State: model.StateWorking},
			head:     head,
			n:        1,
			contains: []string{"latest review outcome is blocking"},
		},
		{
			name:     "a changeset with no marker fails",
			summary:  lifecycle.Summary{State: model.StateWorking},
			head:     head,
			n:        1,
			contains: []string{"no lifecycle marker"},
		},
		{
			name:     "marked ready and never reviewed fails",
			summary:  lifecycle.Summary{Marker: ready, State: model.StateReady},
			head:     head,
			n:        1,
			contains: []string{"marked ready and has not been reviewed since (ccccccc)"},
		},
		{
			name:     "a withdrawn changeset fails",
			summary:  lifecycle.Summary{Marker: unready, LatestReview: approve, State: model.StateWorking},
			head:     head,
			n:        1,
			contains: []string{"took the changeset out of review (ddddddd)"},
		},
		{
			// The ending is the one condition that stops the list. A check without the terminal rule
			// would say nothing about the changeset being finished, and one that carried on would
			// report fixable problems on work that will never land.
			name:     "an abandoned changeset reports only the ending",
			terminal: abandon,
			summary:  lifecycle.Summary{Marker: abandon, State: model.StateWorking},
			head:     head,
			n:        1,
			contains: []string{"abandoned by eeeeeee"},
		},
		{
			name: "drift over the reviewed content fails, and names the paths",
			summary: lifecycle.Summary{Marker: approve, LatestReview: approve, State: model.StateWorking,
				Drifted: []string{"service.go", "handler.go"}},
			head: head,
			n:    1,
			contains: []string{
				"content outside changesets/booking/ changed since aaaaaaa: service.go, handler.go",
			},
		},
		{
			name: "an unreadable marker after the newest marker fails",
			summary: lifecycle.Summary{Marker: approve, LatestReview: approve, State: model.StateWorking,
				TrailingUnrecognised: 2},
			head:     head,
			n:        1,
			contains: []string{"2 review marker(s) after aaaaaaa carry trailers git-pair cannot read"},
		},
		{
			// The newest marker is a marker only by the derivation's definition. Anything else
			// fails closed rather than passing an event the tool cannot interpret.
			name:     "an unclassifiable marker fails closed",
			summary:  lifecycle.Summary{Marker: &lifecycle.Event{Short: "aaaaaaa", Kind: lifecycle.KindImplementation}, State: model.StateWorking},
			head:     head,
			n:        1,
			contains: []string{"not one git-pair can classify"},
		},
		{
			// Every other condition says pass, and the record still says no. A pipeline that runs
			// this gate before integrating gets a clear answer when it re-runs after integrating.
			name:    "an integrated changeset is not integration-ready again",
			summary: lifecycle.Summary{Marker: approve, LatestReview: approve, State: model.StateApproved},
			head:    head,
			landed:  landing{Commit: landedSHA, BranchKnown: true, DefaultBranch: "main", InDefaultBranch: true},
			n:       1,
			contains: []string{
				"changeset is already integrated at " + short(landedSHA),
				"reachable from main",
			},
			// The record outranks everything else: the drift question below it describes work
			// still in progress.
			not: []string{"changed since"},
		},
		{
			name:     "a landing outside the default branch says so",
			summary:  lifecycle.Summary{Marker: approve, LatestReview: approve, State: model.StateApproved},
			head:     head,
			landed:   landing{Commit: landedSHA, BranchKnown: true, DefaultBranch: "main", InDefaultBranch: false},
			n:        1,
			contains: []string{"already integrated at", "not reachable from main"},
		},
		{
			// "I cannot tell which branch is the integration branch" must not come out as "it is
			// not in main": that would turn a fetching problem into a claim about the work.
			name:     "an unknown default branch is not reported as an absence",
			summary:  lifecycle.Summary{Marker: approve, LatestReview: approve, State: model.StateApproved},
			head:     head,
			landed:   landing{Commit: landedSHA},
			n:        1,
			contains: []string{"already integrated at " + short(landedSHA)},
			not:      []string{"reachable from"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := integrationReasons(slug, tc.terminal, tc.summary, tc.head, tc.feedback, tc.landed)
			if len(got) != tc.n {
				t.Fatalf("reasons = %q, want %d", got, tc.n)
			}
			joined := strings.Join(got, "\n")
			for _, want := range tc.contains {
				if !strings.Contains(joined, want) {
					t.Errorf("reasons = %q, want one containing %q", got, want)
				}
			}
			for _, unwanted := range tc.not {
				if strings.Contains(joined, unwanted) {
					t.Errorf("reasons = %q, must not claim %q", got, unwanted)
				}
			}
		})
	}
}

// The whole point of the multi-reason output is that one run names every problem. Two cases
// that pin the shape rather than the wording: an approved head whose content both drifted and
// picked up an unreadable marker reports both, and the order is what a reader can act on — what the
// reviewers decided, then what moved since.
func TestIntegrationReasonsReportsEveryFailureAtOnce(t *testing.T) {
	const head = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	approve := &lifecycle.Event{SHA: head, Short: "aaaaaaa", Kind: lifecycle.KindReview, Outcome: model.OutcomeApprove}

	got := integrationReasons("booking", nil, lifecycle.Summary{
		Marker: approve, LatestReview: approve, State: model.StateWorking,
		Drifted: []string{"service.go"}, TrailingUnrecognised: 1,
	}, head, false, landing{})

	if len(got) != 2 {
		t.Fatalf("reasons = %q, want two: unreadable marker, drift", got)
	}
	for i, want := range []string{"cannot read", "content outside"} {
		if !strings.Contains(got[i], want) {
			t.Errorf("reasons[%d] = %q, want it to be the one about %q", i, got[i], want)
		}
	}
}
