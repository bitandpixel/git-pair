package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"gitpair/internal/lifecycle"
	"gitpair/internal/model"
	"gitpair/internal/reviewref"
)

// --- check ------------------------------------------------------------------

func newCheckCommand(a *app) *cobra.Command {
	var allowFeedback bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Assert that this changeset is integration-ready",
		Long: `Assert that the checked-out changeset satisfies git-pair's integration policy,
and exit non-zero when it does not.

This is the assertion half of the pair with ` + "`git pair status`" + `. ` + "`status`" + ` reports
what the history says; ` + "`check`" + ` answers the one question a merge gate has, in its exit code,
so nothing has to parse prose. CI needs ` + "`$?`" + ` and nothing else.

Every failed condition is listed, not the first: a gate that reports one problem per run turns a
two-minute fix into two round trips, and a log that explains itself once is the difference between
a check people read and one they re-run.

The conditions are that the changeset has not ended and has not been recorded as landed; that the
newest marker is a review whose outcome permits integration (` + "`approve`" + `, or ` + "`feedback`" + ` with
` + "`--allow-feedback`" + `); and that the content that review looked at is still what ` + "`HEAD`" + ` carries.

` + "`check`" + ` refuses feedback on its own unless you say otherwise. Non-blocking feedback is still a
review of a head, so it permits integration in the tool's own terms — but the repository decides at
the gate whether feedback alone is enough to land, and the default is that it is not.

The assertion is about the commit, not the checkout: uncommitted changes are not in ` + "`HEAD`" + ` and
cannot invalidate a review of it, so a dirty working tree does not change the answer.

Without ` + "`--changeset`" + `, on purpose: this is the check a forge runs *on* a revision, and naming a
second changeset would make the verdict ambiguous about what was gated.

Passing the gate is the first of three steps: the gate, then an ordinary git merge or squash into the
base branch performed by whoever owns it, then ` + "`git pair integration record`" + ` to write the durable
record. git-pair does the gate and the record and nothing in between.

Exit codes: 0 integration-ready, 1 not ready, 2 usage, 3 git failed.`,
		Example: `  git pair check
  git pair check --allow-feedback
  git pair check --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(cmd.Context(), a, allowFeedback)
		},
	}
	cmd.Flags().BoolVar(&allowFeedback, "allow-feedback", false,
		"accept non-blocking feedback as sufficient for integration")
	return cmd
}

// The feedback policy, named in the output so a CI log explains its own verdict without the
// command line next to it. `--allow-feedback` is the only policy switch git-pair has, and a
// verdict recorded without its policy is a number nobody can re-derive.
const (
	policyApproveOnly       = "approve-only"
	policyApproveOrFeedback = "approve-or-feedback"
)

type checkJSON struct {
	Changeset string `json:"changeset"`
	Ready     bool   `json:"ready"`
	// State is the derived state, reported beside the verdict rather than as part of it:
	// `ready` is the answer, `state` is why a reader may want to look further.
	State string `json:"state"`
	// Head is the full SHA, not the short form the human output prints, because the consumer of
	// this command compares it against the revision it built.
	Head    string   `json:"head"`
	Reasons []string `json:"reasons"`
	Policy  string   `json:"policy"`
	// Integrated says an integration record exists, and IntegratedAt where its commit sits. Like
	// status's integration fields, they sit beside the verdict rather than changing what `ready`
	// means: a changeset that has landed is not integration-ready again.
	Integrated       bool    `json:"integrated"`
	IntegratedCommit string  `json:"integrated_commit,omitempty"`
	IntegratedAt     landing `json:"-"`
}

func runCheck(ctx context.Context, a *app, allowFeedback bool) error {
	s, err := a.load(ctx)
	if err != nil {
		return err
	}
	// The tree question — is the reviewed content still what HEAD carries? — is the one
	// `status` asks observationally and this command has to answer as a verdict. Asking it
	// through the same derivation is what keeps the two from disagreeing about what drift is.
	reviewed, err := lifecycle.SummarizeAgainstTreeHEAD(ctx, s.repo, s.cs.Slug, s.cs.Base)
	if err != nil {
		return err
	}
	terminal, err := terminalRecord(ctx, s.repo, s.cs.Slug, s.cs.Base, reviewed)
	if err != nil {
		return err
	}
	// Reported beside the verdict rather than folded into it: "already integrated" is not the same
	// fact as "not ready", and a pipeline re-running this gate after its own landing needs to tell
	// the two apart without matching on the wording of a reason.
	landed, err := reviewref.ResolveIntegration(ctx, s.repo, s.cs.Slug)
	if err != nil && !errors.Is(err, reviewref.ErrNotIntegrated) {
		return err
	}
	var where landing
	if landed != "" {
		if where, err = a.describeLanding(ctx, s.repo, landed); err != nil {
			return err
		}
	}

	policy := policyApproveOnly
	if allowFeedback {
		policy = policyApproveOrFeedback
	}
	out := checkJSON{
		Changeset:        s.cs.Slug,
		State:            string(reviewed.State),
		Head:             s.head,
		Policy:           policy,
		Integrated:       landed != "",
		IntegratedCommit: short(landed),
		IntegratedAt:     where,
		Reasons:          integrationReasons(s.cs.Slug, terminal, reviewed, s.head, allowFeedback, where),
	}
	out.Ready = len(out.Reasons) == 0
	if out.Reasons == nil {
		// `reasons` is an array in both verdicts. `null` would make every consumer
		// handle two shapes for the same fact, and the fact it is checking — whether the
		// gate passed — is already in `ready`.
		out.Reasons = []string{}
	}

	if a.json {
		return a.emitJSON(out)
	}
	if !out.Ready {
		a.printf("NOT READY:\n")
		for _, r := range out.Reasons {
			a.printf("- %s\n", r)
		}
		// The verdict is the output, so it is not also an error message: `git pair check`
		// failing is this command working, and CI reads the exit code rather than a
		// `git-pair:` line prefixed over its own answer.
		return errSilent
	}
	a.printf("OK: %s is integration-ready\n", s.cs.Slug)
	// The commit the gate cleared, named on the passing line as well as in --json: a log that says
	// "ready" without saying what it looked at cannot be re-read after the branch has moved.
	a.printf("head:  %s\n", short(s.head))
	a.printf("next:  %s\n", landingNextAction(s.cs.Base))
	return nil
}

// integrationReasons lists every reason this changeset is not integration-ready, in the order a
// reader can act on them: what the reviewers decided, then what moved since they decided it.
//
// It reads the derivation rather than recomputing anything. `status` and this command ask the same
// questions of the same commits, and a second implementation of "is the reviewed content still
// here" would be a second answer waiting to disagree with the first.
//
// The surviving-review-additions diagnostic is deliberately absent. Requirements §26 lists it
// among the readiness conditions, but `change ready` is where that decision is made and
// acknowledged, and the outcome and drift conditions here already guarantee that nothing has
// moved since the marker — re-deriving it would re-litigate a decision the author already took,
// in a command with no override flag to take it again.
func integrationReasons(slug string, terminal *lifecycle.Event, s lifecycle.Summary,
	head string, allowFeedback bool, landed landing) []string {
	if landed.Commit != "" {
		// The other early return, and it comes first. Once the record exists the review is over:
		// the drift question below it describes a changeset still being worked on, which this one
		// is not. It outranks the abandoned check because an integration record is a record that
		// was written, and git-pair writes no marker for a changeset that has landed, so an
		// abandonment cannot have been recorded after it.
		return []string{fmt.Sprintf("changeset is already integrated at %s%s", short(landed.Commit), landed.reach())}
	}
	if terminal != nil {
		// The one condition that stops the list. Everything below it is fixable, and
		// nothing is fixable about an ending: an abandoned changeset will not be taken
		// forward whatever else happens to its branch (PRD §9.7).
		return []string{fmt.Sprintf("changeset was abandoned by %s and will not be taken forward", terminal.Short)}
	}

	var reasons []string

	m := s.Marker
	switch {
	case m == nil:
		reasons = append(reasons, "the changeset has no lifecycle marker yet: run `git pair change ready`")
	case m.Kind == lifecycle.KindUnready:
		reasons = append(reasons, fmt.Sprintf(
			"the author took the changeset out of review (%s): run `git pair change ready` when it is offered again", m.Short))
	case m.Kind == lifecycle.KindReady:
		reasons = append(reasons, fmt.Sprintf("the changeset is marked ready and has not been reviewed since (%s)", m.Short))
	case m.Kind == lifecycle.KindReview:
		switch m.Outcome {
		case model.OutcomeApprove:
			// Integration is permitted at this head.
		case model.OutcomeFeedback:
			if !allowFeedback {
				reasons = append(reasons, fmt.Sprintf(
					"the newest review is feedback, which is non-blocking and is not sufficient without --allow-feedback (%s)", m.Short))
			}
		default:
			// `block`, and anything git-pair could not read as an outcome: the gate fails
			// closed on a marker whose verdict it cannot name.
			reasons = append(reasons, "latest review outcome is blocking")
		}
	default:
		reasons = append(reasons, fmt.Sprintf("the newest lifecycle marker (%s) is not one git-pair can classify", m.Short))
	}

	if s.TrailingUnrecognised > 0 {
		// Commits after the marker carrying Review-* trailers git-pair cannot read. The
		// tree cannot speak for them, so the state falls back to WORKING and no
		// comparison of file contents can say what they meant.
		reasons = append(reasons, fmt.Sprintf(
			"%d review marker(s) after %s carry trailers git-pair cannot read", s.TrailingUnrecognised, markerOr(s)))
	}
	if len(s.Drifted) > 0 {
		reasons = append(reasons, fmt.Sprintf("content outside changesets/%s/ changed since %s: %s",
			slug, markerOr(s), strings.Join(s.Drifted, ", ")))
	}

	return reasons
}

// markerOr names the newest marker for a reason line, which is only reached where a marker
// exists — drift and unreadable markers are both measured against it.
func markerOr(s lifecycle.Summary) string {
	if s.Marker == nil {
		return "the last review"
	}
	return s.Marker.Short
}
