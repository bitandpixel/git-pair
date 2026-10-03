package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/lifecycle"
	"gitpair/internal/model"
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
` + "`--allow-feedback`" + `); that the commit that review spoke about (` + "`Review-Head`" + `) is still in this
line of history; and that the content it looked at is still what ` + "`HEAD`" + ` carries.

The lineage condition and the content condition are different questions, and a rewrite separates them.
A rebase that resolves no conflict changes no file, so the tree still matches and only the ancestry
test refuses — which is the point: an approval is about a commit, and git-pair does not read an
approval of one commit as approval of the rewritten version of it. Merging the base in, or committing
on top, rewrites nothing and passes.

` + "`check`" + ` refuses feedback on its own unless you say otherwise. Non-blocking feedback is still a
review of a head, so it permits integration in the tool's own terms — but the repository decides at
the gate whether feedback alone is enough to land, and the default is that it is not.

The assertion is about the commit, not the checkout: uncommitted changes are not in ` + "`HEAD`" + ` and
cannot invalidate a review of it, so a dirty working tree does not change the answer.

Without ` + "`--changeset`" + `, on purpose: this is the check a forge runs *on* a revision, and naming a
second changeset would make the verdict ambiguous about what was gated.

Passing the gate is the first of two steps: the gate, then an ordinary git merge or squash into the
destination branch performed by whoever owns it. git-pair does the gate and nothing in between, and the
landing itself is what git-pair reads afterwards — the directory in the destination's tree is the record.

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
	// ReviewedHead is the commit the newest permitting review spoke about, from its
	// `Review-Head` trailer, resolved to a full SHA where this clone can. It is reported
	// whether or not the verdict is ready: when the gate refuses for lineage, the log has to
	// name both ends of the comparison (PRD §11.3).
	ReviewedHead string `json:"reviewed_head,omitempty"`
	// Landed says the destination's tree carries this changeset's directory, and LandedCommit names the
	// commit that brought it there. Like status's landing fields, they sit beside the verdict rather than
	// changing what `ready` means: a changeset that has landed is not integration-ready again. They are
	// read from the destination's tree, so they answer the same way in a clone that has never fetched a
	// ref — the answer is a fact about the destination, not about what this machine was shown.
	Landed       bool    `json:"landed"`
	LandedCommit string  `json:"landed_commit,omitempty"`
	LandedAt     landing `json:"-"`
	// Integrating says the newest declaration from `git pair change integrate` is on this branch, and
	// IntegrateCommit names the commit that wrote it. They sit beside `ready` for the same reason
	// `integrated` does: "may this merge" and "did the author ask for one" are two questions, and CI's
	// gate is the conjunction of the two answers — `jq -e '.ready and .integrating'`. Note that this is
	// the marker, not the tree: an implementation commit placed after the declaration turns `state` back
	// into WORKING and `ready` off, and the declaration is still the newest marker on the branch.
	Integrating     bool   `json:"integrating"`
	IntegrateCommit string `json:"integrate_commit,omitempty"`
	// ParentLanded says the branch this changeset is stacked on has landed: the base is
	// finished work. It sits beside the verdict and never inside `reasons`, because a parent that landed
	// changes nothing this child owns — the diff the reviewer approved is the diff still under test.
	// Refusing it would ask for a re-review of unchanged content.
	ParentLanded       bool   `json:"parent_landed"`
	ParentLandedCommit string `json:"parent_landed_commit,omitempty"`
	ParentStaleBranch  bool   `json:"parent_stale_branch"`
	// ParentMeasuredBase is the commit the approval recorded measuring its diff from, and
	// ParentComparison names the reading that answered the landed-parent question — "contribution" when
	// this branch's contribution was compared with the diff identity the approval recorded, "merge-base"
	// when the older base comparison decided it, empty when neither was asked. They are the two facts
	// behind a parent verdict, and a reader who disagrees with the verdict needs them to say which side
	// moved.
	ParentMeasuredBase string `json:"parent_measured_base,omitempty"`
	ParentComparison   string `json:"parent_comparison,omitempty"`
	// ParentHeadCarriesLanding says this changeset's own head already carries the parent's landing commit.
	// `parent_stale_branch` is the same reading where the parent's branch is still in this clone, and false
	// where it has been deleted; this one answers in both cases, and a gate that advises a rebase should
	// read it before it does.
	ParentHeadCarriesLanding bool `json:"parent_head_carries_landing"`
	// NextAction is the step a passing verdict licenses — the merge someone else performs, then the
	// record — spelled the same way `status` spells it. It appears only when the gate passed: when it
	// did not, `reasons` is the next step, and a consumer should never have to decide which of two
	// fields to believe. An agent reading this verdict rather than the exit code should not have to
	// parse a sentence to learn what comes next.
	NextAction string `json:"next_action,omitempty"`
	// Recommendations is advice a person can act on that the gate neither requires nor refuses: what would
	// make the next round easier about the file rather than about the code. It is an array in both verdicts
	// for the reason `reasons` is one, and empty in the ordinary case, so a consumer that ignores it loses
	// nothing.
	Recommendations []string `json:"recommendations"`
}

func runCheck(ctx context.Context, a *app, allowFeedback bool) error {
	s, err := a.load(ctx)
	if err != nil {
		return err
	}
	g, err := a.integrationGate(ctx, s, allowFeedback)
	if err != nil {
		return err
	}

	policy := policyApproveOnly
	if allowFeedback {
		policy = policyApproveOrFeedback
	}
	out := checkJSON{
		Changeset:    s.cs.Slug,
		State:        string(g.Summary.State),
		Head:         s.head,
		Policy:       policy,
		ReviewedHead: g.ReviewedHead,
		Landed:       g.Landed != "",
		LandedCommit: short(g.Landed),
		LandedAt:     g.Where,
		Reasons:      g.Reasons,
		// Advice the verdict does not depend on. It is computed after the gate, never inside it: a reason
		// refuses a merge, and this does not.
		Recommendations: stackLinkRecommendations(ctx, a, s),
		// The CI half of the gate. `ready` answers "may this merge"; `integrating` answers "did the
		// author ask for one", and a pipeline that merges on the first alone takes the decision out of
		// the author's hands. Both in one command is the point: a CI job should not have to run a second
		// command and join two verdicts that were computed from two different reads of the repository.
		Integrating:     g.Declared() != nil,
		IntegrateCommit: eventSHA(g.Declared()),
	}
	out.Ready = len(out.Reasons) == 0
	if out.Ready {
		out.NextAction = landingNextAction(s.cs.Base)
		if g.Parent.Landed != "" && g.Parent.owesLandingStep() {
			// Spelled beside the landing contract rather than inside it: `landingNextAction` is one string
			// shared by `status`, `check`, `change ready` and `review`, it takes only a base, and teaching it
			// about parents would make the same sentence mean two things in four commands.
			out.NextAction += fmt.Sprintf("; parent %s landed as %s — %s", g.Parent.parentName(), g.Parent.Landed,
				landedParentStep(s.cs, g.Parent))
		}
	}
	out.ParentLanded = g.Parent.Landed != ""
	out.ParentLandedCommit = g.Parent.Landed
	out.ParentStaleBranch = g.Parent.StaleBranch
	out.ParentMeasuredBase = short(g.Parent.Measured)
	out.ParentComparison = g.Parent.Comparison
	out.ParentHeadCarriesLanding = g.Parent.LandingUnderHead
	if out.Reasons == nil {
		// `reasons` is an array in both verdicts. `null` would make every consumer
		// handle two shapes for the same fact, and the fact it is checking — whether the
		// gate passed — is already in `ready`.
		out.Reasons = []string{}
	}
	if out.Recommendations == nil {
		// The same argument applies to the advice: one shape, in both verdicts.
		out.Recommendations = []string{}
	}

	if a.json {
		return a.emitJSON(out)
	}
	if !out.Ready {
		a.printf("NOT READY:\n")
		for _, r := range out.Reasons {
			a.printf("- %s\n", r)
		}
		a.printRecommendations(out.Recommendations)
		// The verdict is the output, so it is not also an error message: `git pair check`
		// failing is this command working, and CI reads the exit code rather than a
		// `git-pair:` line prefixed over its own answer.
		return errSilent
	}
	a.printf("OK: %s is integration-ready\n", s.cs.Slug)
	// The commit the gate cleared, named on the passing line as well as in --json: a log that says
	// "ready" without saying what it looked at cannot be re-read after the branch has moved.
	a.printf("head:  %s\n", short(s.head))
	if d := g.Declared(); d != nil {
		// Beside the verdict, because it is a different question: the gate says the merge may happen, the
		// declaration says somebody asked for it.
		a.printf("declared: %s (`git pair change integrate`)\n", short(d.SHA))
	}
	if g.Parent.Landed != "" {
		// Beside the verdict, not inside it: the gate passed, and the reader still needs to know the base
		// underneath is finished work with a step attached to it.
		a.printf("parent: %s landed as %s — %s\n", g.Parent.parentName(), g.Parent.Landed,
			landedParentStep(s.cs, g.Parent))
	} else if g.Parent.Note != "" {
		// The stack observation the verdict did not act on: the parent moved, or its record says too little
		// to compare. Printing it on the passing verdict is the point — the gate is where an author reads
		// before merging, and a note that only appears in `status` is read after the merge.
		a.printf("parent: %s\n", g.Parent.Note)
	}
	a.printRecommendations(out.Recommendations)
	a.printf("next:  %s\n", out.NextAction)
	return nil
}

// printRecommendations writes the advice that is not part of the verdict. `recommend:` rather than the `- `
// of a reason, because a reader who cannot tell the two apart will start treating advice as a refusal - and
// the same prefix in both verdicts, since the advice does not change when the gate falls.
func (a *app) printRecommendations(recs []string) {
	for _, r := range recs {
		a.printf("recommend: %s\n", r)
	}
}

// stackLinkRecommendations is the advice for the shape `init` no longer leaves behind: a `base:` naming a
// branch that carries exactly one unlanded changeset, with no stack recorded beside it. It is deliberately
// not a reason. A changeset written before this rule existed must not be held by it, and which key a file
// chose to name its parent with is not a condition a merge should depend on.
func stackLinkRecommendations(ctx context.Context, a *app, s *session) []string {
	if s.cs.Base == "" {
		return nil
	}
	db := a.destination(ctx, s.repo)
	if db.Ref == "" || db.IsBranch(s.cs.Base) {
		// Measuring against the integration branch is not a stack. That is the common case, and it is
		// answered without reading the repository, so `check` costs what it cost before this existed.
		return nil
	}
	pcs, candidates, _ := baseChangesetOn(ctx, s.repo, s.cs.Base, db)
	if pcs == "" || len(candidates) != 1 {
		return nil
	}
	if s.cs.BaseChangeset == "" {
		return []string{fmt.Sprintf("base %s carries changeset %s, so it is your parent rather than your base: record the stack with `git pair init --parent %s`",
			s.cs.Base, pcs, s.cs.Base)}
	}
	// The recorded id is what the ancestor drop and the base derivation both read, so a file whose id and
	// whose base disagree is read two ways at once: the branch says one changeset is below this one, the id
	// says another. Which is stale is the author's fact - the parent may have been renamed, or this file may
	// have been restacked by hand - so it is advice, never a reason. A reason here would gate a merge over a
	// question the reviewer is worse placed to answer than the author.
	if s.cs.BaseChangeset != pcs {
		return []string{fmt.Sprintf("base %s carries changeset %s, while this file records %s: one of the two is stale, "+
			"and the diff is measured against %s - correct it with `git pair init --parent %s --set-parent`",
			s.cs.Base, pcs, s.cs.BaseChangeset, s.cs.Base, s.cs.Base)}
	}
	return nil
}

// integrationGate asks everything the integration policy asks, in one read of the repository, and
// lists every failed condition instead of the first.
//
// It is shared by the two commands that need the answer: `git pair check`, which prints the verdict as
// its whole output, and `git pair change integrate`, which refuses to declare a changeset that the gate
// would refuse. Two implementations of "is the reviewed content still here" would be two answers waiting
// to disagree about what drift is, which is why the second command asks this one rather than reading the
// marker it is about to write.
type integrationGate struct {
	// Summary is the derivation with the tree question asked — the marker verdict, then whether the
	// content it spoke about is still what HEAD carries.
	Summary lifecycle.Summary
	// Verdict is the marker whose outcome licenses the merge: the newest marker, or the newest review
	// under it when the newest marker is a declaration.
	Verdict *lifecycle.Event
	// Terminal is the abandon marker, if this changeset has one.
	Terminal *lifecycle.Event
	// Landed is the commit that brought this changeset's directory onto the destination's first-parent
	// line, empty when the destination does not carry it. Where is what git-pair can honestly say about
	// that commit.
	//
	// It is read from the destination's tree rather than from a ref, which is what makes the answer the
	// same in a fresh clone as in one that has fetched everything: the directory is the record, and the
	// tree is where the destination keeps it.
	Landed string
	Where  landing
	// ReviewedHead is the commit the verdict spoke about, resolved to a full SHA where this clone can.
	ReviewedHead string
	// Parent is the stack reading: whether the branch this changeset is stacked on moved, landed, or
	// ended since the verdict.
	Parent parentStatus
	// Reasons is every failed condition, in the order a reader can act on them. Empty means ready.
	Reasons []string
}

// Declared is the declaration while it is the newest marker on the branch — the author's last statement is
// "merge this" — and nil once anything has been said since.
//
// It is not `Summary.Integrating`, which keeps the newest declaration in the range even when a re-offer, a
// re-review or a retraction has superseded it, because "was a declaration ever made, and where" is a
// question with an answer that survives. The CI gate is not that question. A pipeline that merged on a
// superseded declaration would perform the merge the author had just taken back, and `ready` alone would
// not save it: work put back in review can come back to approved a commit later, with the older
// declaration still sitting in the history naming one of them.
func (g integrationGate) Declared() *lifecycle.Event {
	if m := g.Summary.Marker; m != nil && m.Kind == lifecycle.KindIntegrate {
		return m
	}
	return nil
}

func (a *app) integrationGate(ctx context.Context, s *session, allowFeedback bool) (*integrationGate, error) {
	// The tree question — is the reviewed content still what HEAD carries? — is the one `status` asks
	// observationally and the gate has to answer as a verdict. Asking it through the same derivation is
	// what keeps the two from disagreeing about what drift is.
	reviewed, err := lifecycle.SummarizeAgainstTreeHEAD(ctx, s.repo, s.cs.Slug, s.cs.Base)
	if err != nil {
		return nil, err
	}
	g := &integrationGate{Summary: reviewed, Verdict: integrationVerdict(reviewed)}
	if g.Terminal, err = terminalRecord(reviewed); err != nil {
		return nil, err
	}
	// Reported beside the verdict rather than folded into it: "already landed" is not the same fact as
	// "not ready", and a pipeline re-running this gate after its own landing needs to tell the two apart
	// without matching on the wording of a reason.
	//
	// The destination's tree answers it. Where no destination can be named — a clone that cannot work out
	// which branch is main and was not told — the gate reports no landing and every other condition still
	// applies, because refusing for a reason the reader cannot check is worse than the condition itself.
	if s.trunk.Ref != "" {
		if present, _ := changeset.CarriesDir(ctx, s.repo, s.trunk.Ref, s.cs.Slug); present {
			if ch, err := changeset.LandedChain(ctx, s.repo, s.trunk.Ref, s.cs.Slug); err == nil {
				g.Landed = ch.Landing
				g.Where = landing{
					Commit:          ch.Landing,
					DefaultBranch:   displayRef(s.trunk.LocalName()),
					InDefaultBranch: true,
					BranchKnown:     true,
				}
			} else if !errors.Is(err, changeset.ErrNoChain) {
				return nil, err
			}
		}
	}
	// The lineage question, asked of the same derivation so the two conditions cannot disagree about
	// which marker is newest or what it approved. It is the check the tree cannot do: a rebase that
	// changes no file passes the drift test and fails this one.
	lineage, head, err := lineageReason(ctx, s.repo, reviewed, s.head, allowFeedback)
	if err != nil {
		return nil, err
	}
	g.ReviewedHead = head
	// The stack question. A child's own history can be untouched and its parent can have landed, been
	// rewritten, or been abandoned underneath it, and only the parent's side shows that. Asked of the
	// verdict rather than of the newest marker: a declaration is not an approval, and a parent that moved
	// under a declared child invalidates the approval the declaration is resting on.
	if g.Parent, err = a.parentSinceApproval(ctx, s.repo, s.cs, s.trunk, g.Verdict, s.head); err != nil {
		return nil, err
	}
	g.Reasons = integrationReasons(s.cs.Slug, g.Terminal, reviewed, s.head, allowFeedback, g.Where, lineage, g.Parent.Reason)
	// The shape question, added last: it is the condition with the fewest ways out, and the fixes change what
	// the branch carries rather than what was said about it. It gates the merge because the second changeset
	// arrives in the destination with no approval of its own, and no review of the first one covers it.
	if s.trunk.Ref != "" {
		shape, err := branchShape(ctx, s.repo, s.head, s.trunk)
		if err != nil {
			return nil, err
		}
		if !shape.Offerable() {
			g.Reasons = append(g.Reasons, shapeReason(s.cs.Slug, shape))
		}
	}
	if s.baseIsOwnBranch {
		// The base that names its own branch is refused where the file is read, not only where `init` wrote it.
		// A hand edit afterwards makes the comparison the branch against itself and leaves the reviewer an empty
		// diff; `status` and `change ready` already said so, and the gate could be walked past them.
		g.Reasons = append(g.Reasons, fmt.Sprintf("changeset %s: `base` in %s names %s, the branch the changeset is on, so the base moves with every commit and the changeset can never contain anything",
			s.cs.Slug, s.cs.MetadataPath(), s.cs.Branch))
	}
	return g, nil
}

// integrationVerdict is the marker whose review outcome licenses a merge.
//
// It is the newest marker, except when the newest marker is `git pair change integrate`'s declaration:
// then it is the newest review under it. A declaration is the author adding "and merge this" to a verdict
// that already permits the merge, so it cannot be read as a verdict — it carries no outcome, and the gate
// would refuse the act it was asked to license. Nor can it be treated as absent: the lineage test reads
// the verdict's `Review-Head`, and skipping the declaration without reading under it would let a rewrite
// of the branch pass the gate that a rewrite is precisely what the gate exists to refuse.
//
// Nil means no verdict at all — which for a declaration is the hand-written or half-written history the
// reason line names rather than guesses about.
func integrationVerdict(s lifecycle.Summary) *lifecycle.Event {
	if s.Marker != nil && s.Marker.Kind == lifecycle.KindIntegrate {
		return s.LatestReview
	}
	return s.Marker
}

// eventSHA is an event's full SHA where the event exists, for a JSON field that is empty when it does
// not. Tests and surfaces both print the marker's SHA, and nil-checking at every call site is how one
// of them forgets.
func eventSHA(e *lifecycle.Event) string {
	if e == nil {
		return ""
	}
	return e.SHA
}

// integrationReasons lists every reason this changeset is not integration-ready, in the order a
// reader can act on them: what the reviewers decided, then what moved since they decided it — the
// history first, then the content, because a rewritten branch invalidates the review of it.
//
// `lineage` is the rewritten-history reason, computed by lineageReason and empty when the condition
// passed or did not apply.
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
	head string, allowFeedback bool, landed landing, lineage, parent string) []string {
	if landed.Commit != "" {
		// The other early return, and it comes first. Once the destination carries the directory the
		// review is over: the drift question below it describes a changeset still being worked on,
		// which this one is not. It outranks the abandoned check because a landing happened, and
		// git-pair writes no marker for a changeset that has landed, so an abandonment cannot have
		// followed one.
		return []string{fmt.Sprintf("changeset is already landed at %s%s", short(landed.Commit), landed.reach())}
	}
	if terminal != nil {
		// The one condition that stops the list. Everything below it is fixable, and
		// nothing is fixable about an ending: an abandoned changeset will not be taken
		// forward whatever else happens to its branch (PRD §9.7).
		return []string{fmt.Sprintf("changeset was abandoned by %s and will not be taken forward", terminal.Short)}
	}

	var reasons []string

	// The verdict, not simply the newest marker: a declaration to integrate sits above the approval it
	// rests on and speaks for neither (see integrationVerdict).
	m, verdict := s.Marker, integrationVerdict(s)
	switch {
	case verdict == nil:
		if m != nil && m.Kind == lifecycle.KindIntegrate {
			// The author declared a changeset no review has ever judged — a hand-written marker, or one
			// whose verdict was rewritten away. Naming the declaration is what makes it findable.
			reasons = append(reasons, fmt.Sprintf(
				"the newest marker is the author's declaration to integrate (%s) and no review under it records a verdict: `git pair review submit --approve`", m.Short))
		} else {
			reasons = append(reasons, "the changeset has no lifecycle marker yet: run `git pair change ready`")
		}
	case verdict.Kind == lifecycle.KindUnready:
		reasons = append(reasons, fmt.Sprintf(
			"the author took the changeset out of review (%s): run `git pair change ready` when it is offered again", verdict.Short))
	case verdict.Kind == lifecycle.KindReady:
		reasons = append(reasons, fmt.Sprintf("the changeset is marked ready and has not been reviewed since (%s)", verdict.Short))
	case verdict.Kind == lifecycle.KindReview:
		switch verdict.Outcome {
		case model.OutcomeApprove:
			// Integration is permitted at this head.
		case model.OutcomeFeedback:
			if !allowFeedback {
				reasons = append(reasons, fmt.Sprintf(
					"the newest review is feedback, which is non-blocking and is not sufficient without --allow-feedback (%s)", verdict.Short))
			}
		default:
			// `block`, and anything git-pair could not read as an outcome: the gate fails
			// closed on a marker whose verdict it cannot name.
			reasons = append(reasons, "latest review outcome is blocking")
		}
	default:
		reasons = append(reasons, fmt.Sprintf("the newest lifecycle marker (%s) is not one git-pair can classify", verdict.Short))
	}

	if s.TrailingUnrecognised > 0 {
		// Commits after the marker carrying Review-* trailers git-pair cannot read. The
		// tree cannot speak for them, so the state falls back to WORKING and no
		// comparison of file contents can say what they meant.
		reasons = append(reasons, fmt.Sprintf(
			"%d review marker(s) after %s carry trailers git-pair cannot read", s.TrailingUnrecognised, markerOr(s)))
	}
	if lineage != "" {
		// The rewritten-history condition, and the reason the gate is about a commit rather
		// than only about a tree (PRD §12).
		reasons = append(reasons, lineage)
	}
	if parent != "" {
		// The stack condition: this changeset's own history is untouched and the review of it
		// still reads as an approval, but what it was reviewed against has moved, landed, or
		// ended. The conservative rule says that is the approval's end (PRD §21), and the reason
		// names the kind of movement so it reads as a rule rather than as bad luck.
		reasons = append(reasons, parent)
	}
	if len(s.Drifted) > 0 {
		reasons = append(reasons, fmt.Sprintf("content outside changesets/%s/ changed since %s: %s",
			slug, markerOr(s), strings.Join(s.Drifted, ", ")))
	}

	return reasons
}

// lineageReason asks the question the tree cannot: is the commit the approving review spoke about
// still in this line of history?
//
// It is asked only where the verdict is a review that permits integration — with no approval
// standing there is nothing for a rewrite to invalidate, and a `READY` changeset rebased before
// anyone read it owes no explanation. Where it applies it answers three ways: the named commit is
// an ancestor of HEAD (pass, and its full SHA is returned for `--json`), it is not (the branch was
// rewritten since the review), or this clone cannot tell (the commit is not here at all, which in CI
// is a fetch gap and must not read as a verdict about the work).
//
// "The verdict" is read through a declaration to the review under it, so a branch rewritten after
// `change integrate` still fails here: the declaration inherits the approval's commitment to a commit
// rather than replacing it.
//
// An approval whose marker names no head is refused rather than waved through. The review commit's
// own first parent is the same value as the trailer before a rewrite and a *rewritten* parent after
// one, so deriving it would make the rule pass in exactly the case it exists for — and a marker with
// no `Review-Head` is a marker whose approval covers an unknown commit (PRD §12).
func lineageReason(ctx context.Context, repo *git.Repo, s lifecycle.Summary,
	head string, allowFeedback bool) (reason, reviewedHead string, err error) {
	m := integrationVerdict(s)
	if m == nil || m.Kind != lifecycle.KindReview {
		return "", "", nil
	}
	switch m.Outcome {
	case model.OutcomeApprove:
		// An approval is exactly what a rewrite can invalidate.
	case model.OutcomeFeedback:
		if !allowFeedback {
			// The outcome reason below already refuses this changeset, and the lineage of a
			// feedback that does not permit integration is not the thing standing in the way.
			return "", "", nil
		}
	default:
		return "", "", nil
	}
	if m.ReviewedHead == "" {
		return fmt.Sprintf("review %s names no reviewed commit, so git-pair cannot tell which history its approval covers", m.Short), "", nil
	}
	full, err := repo.RevParse(ctx, m.ReviewedHead+"^{commit}")
	if err != nil {
		if errors.Is(err, git.ErrUnknownRevision) {
			return fmt.Sprintf("review %s names %s as the commit it reviewed, which this repository does not have: fetch it before reading this verdict", m.Short, m.ReviewedHead), m.ReviewedHead, nil
		}
		return "", "", err
	}
	inLine, err := repo.IsAncestor(ctx, full, head)
	if err != nil {
		return "", full, err
	}
	if inLine {
		return "", full, nil
	}
	return fmt.Sprintf("review %s reviewed %s, which is no longer in this history: the branch was rewritten since the review, so the approval does not license integration",
		m.Short, short(full)), full, nil
}

// markerOr names the newest marker for a reason line, which is only reached where a marker
// exists — drift and unreadable markers are both measured against it.
func markerOr(s lifecycle.Summary) string {
	if s.Marker == nil {
		return "the last review"
	}
	return s.Marker.Short
}
