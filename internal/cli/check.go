package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/hooks"
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
  git pair check --trust-hooks-once
  git pair check --no-hooks
  git pair check --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(cmd.Context(), a, allowFeedback)
		},
	}
	cmd.Flags().BoolVar(&allowFeedback, "allow-feedback", false,
		"accept non-blocking feedback as sufficient for integration")
	// Both hook flags are on the verdict, in opposite directions. `--trust-hooks-once` runs the scripts the clone
	// has not recorded, which is more checking. `--no-hooks` runs none of them, which is less, and is here because
	// this verdict is what a person reads to find out what is wrong: a hook that fails or misbehaves should not
	// also stop the gate from answering the other questions. It does not buy a clear gate from an unread tree —
	// the position refusal stands, because the off switch says nothing about what the scripts do — and what it did
	// is reported, hook by hook, as `skipped-by-flag`. The integrate job runs `git pair check` without either flag.
	trustHooksOnceFlag(cmd, a)
	noHooksFlag(cmd, a)
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
	// DriftCredited names the paths that differ from the reviewed commit and were not counted as drift
	// because a credit ref carries the same content: what the gate took on trust when a branch that merged
	// its ground in kept its approval. Which refs may be credited is `creditRefs`'s rule — the integration
	// branch, and a stack's unlanded parent. It is a fact about the reading, not a second verdict — the
	// paths the gate did refuse are in `reasons` (PRD §10.4).
	DriftCredited []string `json:"drift_credited,omitempty"`
	// DriftCreditedBy names, per credited path, the branch whose content supplied it. The two answers are
	// different facts: `drift_credited` says a path was excused, and this says who vouched for it — which
	// a reader needs now that a parent branch can be cited and not only trunk (PRD §10.4).
	DriftCreditedBy map[string]string `json:"drift_credited_by,omitempty"`
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
	// Declarable answers the third question about the hand-off, beside the two above: is a declaration wanted
	// at this head? True means `git pair change integrate` would record one; false means one of three things —
	// the gate refused, the parent has not landed so the command refuses ahead of the gate, or this head is
	// already declared, where the command succeeds and records nothing. It is the gate's own verdict conjoined
	// with the command's two early conditions, not the cheaper predicate the human screens print the hand-off
	// line by: that one skips the content and history questions on purpose, and a field that called a
	// rewritten branch declarable would send a reader into a refusal. The only thing `check` cannot see is the
	// declaration's own hooks. It is not implied by `ready`, which asks about the merge, and it does not imply
	// `integrating`, which reports the marker a declaration wrote.
	Declarable bool `json:"declarable"`
	// ParentLanded says the branch this changeset is stacked on has landed: the base is
	// finished work. It sits beside the verdict and never inside `reasons`, because a parent that landed
	// changes nothing this child owns — the diff the reviewer approved is the diff still under test.
	// Refusing it would ask for a re-review of unchanged content.
	ParentLanded       bool   `json:"parent_landed"`
	ParentLandedCommit string `json:"parent_landed_commit,omitempty"`
	ParentStaleBranch  bool   `json:"parent_stale_branch"`
	// ParentMeasuredBase is the commit the approval recorded measuring its diff from, and
	// ParentComparison names the reading that answered the landed-parent question — "contribution" when
	// this branch's contribution was compared with the diff identity the approval recorded, "contribution-patch"
	// when the content half moved and the rendered half of the same recorded value did not (a file this
	// branch edits also changed in the destination), "merge-base" when the older base comparison decided
	// it, empty when neither was asked. They are the two facts behind a parent verdict, and a reader who
	// disagrees with the verdict needs them to say which side moved.
	ParentMeasuredBase string `json:"parent_measured_base,omitempty"`
	ParentComparison   string `json:"parent_comparison,omitempty"`
	// ParentHeadCarriesLanding says this changeset's own head already carries the parent's landing commit.
	// `parent_stale_branch` is the same reading where the parent's branch is still in this clone, and false
	// where it has been deleted; this one answers in both cases, and a gate that advises a rebase should
	// read it before it does.
	ParentHeadCarriesLanding bool `json:"parent_head_carries_landing"`
	// ParentApprovalSurvivesLanding is the reason a passing verdict can name a landed parent without naming
	// the restack: the content comparison ran, this branch still contributes what the approval recorded, and
	// the branch merges cleanly into the destination, so the rewrite would cost the approval the verdict is
	// resting on. False means the step was advised, or that there was no parent landing to compare at all —
	// `parent_landed` and `parent_head_carries_landing` separate those, and `next_action` carries whichever
	// answer the reader is acting on.
	ParentApprovalSurvivesLanding bool `json:"parent_approval_survives_landing"`
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
	// Hooks is what the repository's own scripts at `pre-check` and `post-check` produced, beside the
	// verdict they contributed to. It is the field that answers "who refused this": a reason naming a hook
	// says a file in the repository said no, and this says which one, with what status and for how long.
	Hooks []map[string]any `json:"hooks"`
}

func runCheck(ctx context.Context, a *app, allowFeedback bool) error {
	s, err := a.load(ctx)
	if err != nil {
		return err
	}
	// The repository's own conditions, asked before git-pair's. `check` is a report rather than an action:
	// its whole value is that one run names every reason the merge would be wrong, so a suite that would fail
	// is not skipped because the gate already had a reason of its own. `change integrate` orders these the
	// other way round, and the difference is that it is a write with a cheap answer available first.
	//
	// Every hook runs even after one has failed, for the same reason: a report that stops at the first
	// failure is a report with a round trip added per problem.
	hc := hookContext{
		Point: hooks.PreCheck, Slug: s.cs.Slug, Branch: s.cs.Branch, Base: s.cs.Base, Head: s.head,
	}
	hookResults, err := a.runPoint(ctx, s, hc, reportingMode)
	if err != nil {
		return err
	}
	g, err := a.integrationGate(ctx, s, allowFeedback, hooks.PreCheck, hooks.PostCheck)
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
		Reasons:      append(append([]string{}, g.Reasons...), hookReasons(hookResults)...),
		// Advice the verdict does not depend on. It is computed after the gate, never inside it: a reason
		// refuses a merge, and this does not.
		Recommendations: stackLinkRecommendations(ctx, a, s),
		// The CI half of the gate. `ready` answers "may this merge"; `integrating` answers "did the
		// author ask for one", and a pipeline that merges on the first alone takes the decision out of
		// the author's hands. Both in one command is the point: a CI job should not have to run a second
		// command and join two verdicts that were computed from two different reads of the repository.
		Integrating:     g.Declared() != nil,
		IntegrateCommit: eventSHA(g.Declared()),
		DriftCredited:   g.Summary.DriftCredited,
		DriftCreditedBy: g.Summary.DriftCreditedBy,
	}
	out.Ready = len(out.Reasons) == 0
	// The declaration question, answered from what the gate already read: the parent rule is `change
	// integrate`'s own refusal, spelled by the function that refuses, and the landing is the destination read
	// the gate made. Neither asks git again, which is what keeps a fourth field off the cost the gate is
	// bounded by. The machine answer and the human hand-off line read this one value, so the field and the
	// screen cannot disagree about whether a declaration is wanted.
	df := declareFacts{WaitingOnParent: len(unlandedParentReason(s.cs, g.Parent)) > 0, Landed: g.Landed != ""}
	// Read once for the passing verdict, and carried to the human screen so the merge line and the hand-off
	// line name the same destination. A refused verdict never reaches either, so the read stays off the path
	// CI takes when the answer is no.
	var lc landingContract
	// The declared path's two facts, read on the passing verdict only, and only where the screen is going to
	// print the `declared:` line anyway. Kept beside `lc` so the human print below can offer the alternative
	// exit against the same destination.
	var declared declaredPath
	if out.Ready {
		lc = landingContractFor(ctx, s.repo, s)
		if g.Declared() != nil {
			// The request is already written, so the merge sentence is not this reader's next step: they are
			// either about to make it visible, or done and waiting. Same builder `status` and
			// `change integrate` read, so the three cannot disagree about which of the three it is.
			declared = declaredPath{CoversHead: alreadyDeclared(s) != "",
				Pushed: declarationPushed(ctx, s.repo, s.cs.Branch, s.head)}
			out.NextAction = declaredNextAction(lc, s.cs.Branch, s.cs.Slug, declared)
		} else {
			out.NextAction = landingNextAction(lc)
		}
		if g.Parent.advisesRestack() {
			// Spelled beside the landing contract rather than inside it: `landingNextAction` is one string
			// shared by `status`, `check`, `change ready` and `review`, and the parent's step is a different
			// fact with a different condition on it.
			out.NextAction += fmt.Sprintf("; parent %s landed as %s — %s", g.Parent.parentName(), g.Parent.Landed,
				g.Parent.Step)
		}
	}
	out.ParentLanded = g.Parent.Landed != ""
	out.ParentLandedCommit = g.Parent.Landed
	out.ParentStaleBranch = g.Parent.StaleBranch
	out.ParentMeasuredBase = short(g.Parent.Measured)
	out.ParentComparison = g.Parent.Comparison
	out.ParentHeadCarriesLanding = g.Parent.LandingUnderHead
	out.ParentApprovalSurvivesLanding = g.Parent.ApprovalSurvivesLanding
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
	// The repository's own conditions on the way out, and here they do join the verdict. A post-hook beside a
	// marker cannot reverse the transition it follows, and does not try; `check` writes nothing, so its
	// post-hooks have nothing to reverse and everything to say. The alternative — a hook failing while
	// `ready` stays true — is the one shape a pipeline cannot use, because the exit code and the field would
	// answer different questions.
	hc.Point = hooks.PostCheck
	postResults, err := a.runPoint(ctx, s, hc, reportingMode)
	if err != nil {
		return err
	}
	if reasons := hookReasons(postResults); len(reasons) > 0 {
		out.Reasons = append(out.Reasons, reasons...)
		out.Ready = false
		// The next step was written for a verdict that no longer holds. Leaving it would tell a reader who
		// stops at `next_action` to merge a change the gate refused.
		out.NextAction = ""
	}
	// The declaration question is asked last, because it is a claim about what `change integrate` would do and
	// that command runs this gate before it writes anything: a verdict the hooks refused is not one a
	// declaration should be offered against.
	out.Declarable = out.Ready && declarable(s, df)
	hookResults = append(hookResults, postResults...)
	out.Hooks = hooksForJSON(hookResults)
	if out.Reasons == nil {
		out.Reasons = []string{}
	}

	if a.json {
		return a.emitJSON(out)
	}
	if !out.Ready {
		a.printf("NOT READY:\n")
		// The state the verdict does not act on, printed from the same value `--json` reports for this run:
		// "a reviewer said no" and "a reviewer said yes and something else moved" are different visits to the
		// same screen, and `NOT READY:` alone cannot tell them apart. It is the tree-reconciled reading, so a
		// refusal for content says WORKING where `status` still says APPROVED — the disagreement README
		// explains, and now on one screen rather than across two. Beside the verdict rather than inside it,
		// for the reason `state` sits beside `ready` in the machine form.
		a.printf("state: %s\n", out.State)
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
		// Beside the verdict, not inside it: the gate passed, and the reader still needs to know what is
		// underneath the work. Which half of that is printed depends on what the step would cost. Where it
		// is advised, it is the command, because "rebase onto it" is a sentence the author has to translate
		// into three arguments. Where it is withheld, the comparison sentence takes its place — the reader
		// learns the content was measured against the approval and came out equal, which is the fact that
		// makes the missing command an answer rather than an omission (PRD §29).
		switch {
		case g.Parent.advisesRestack() && g.Parent.Comparison == "":
			// The step is given, and so is the reason it is given without the comparison. Silence about a
			// step would be the worse failure, so the advice stands — and the reader is told it rests on an
			// unknown rather than on a measurement that came out equal.
			a.printf("parent: %s landed as %s; %s — %s\n", g.Parent.parentName(), g.Parent.Landed,
				g.Parent.Note, g.Parent.Step)
		case g.Parent.advisesRestack():
			a.printf("parent: %s landed as %s — %s\n", g.Parent.parentName(), g.Parent.Landed, g.Parent.Step)
		case g.Parent.Note != "":
			a.printf("parent: %s\n", g.Parent.Note)
		default:
			a.printf("parent: %s landed as %s\n", g.Parent.parentName(), g.Parent.Landed)
		}
	} else if g.Parent.Note != "" {
		// The stack observation the verdict did not act on: the parent moved, or its record says too little
		// to compare. Printing it on the passing verdict is the point — the gate is where an author reads
		// before merging, and a note that only appears in `status` is read after the merge.
		a.printf("parent: %s\n", g.Parent.Note)
	}
	a.printRecommendations(out.Recommendations)
	a.printf("next:  %s\n", out.NextAction)
	if alt := declaredAlternative(lc, declared); alt != "" {
		// The mirror of the hand-off line, for the state where the hand-off is already done: the wait is the
		// next step, and the author's own merge stays offered beside it so waiting is never the only thing on
		// the screen.
		a.printf("       %s\n", alt)
	} else if declarable(s, df) {
		// The second exit, and the human form only: `next_action` above stays one instruction. Which of the
		// two is right is the author's and the destination owner's decision, not something git-pair can
		// know — git-pair asks nothing about who merges, or with what (PRD §26).
		a.printf("       %s\n", handoffLine(lc, s.cs.Branch))
	}
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

func (a *app) integrationGate(ctx context.Context, s *session, allowFeedback bool, points ...hooks.Point) (*integrationGate, error) {
	// The tree question — is the reviewed content still what HEAD carries, and where it is not, whose
	// content is the difference? — is the one `status` asks observationally and the gate has to answer as a
	// verdict. Asking it through the same derivation is what keeps the two from disagreeing about drift.
	//
	// What may be cited is handed in as a question rather than an answer, because most gate runs have
	// nothing to cite: nothing moved above the marker, and the stack reads that locate a parent and ask the
	// destination whether it landed would be paid for nothing. `scripts/gates/ci-integrate.sh` bounds those
	// reads by counting the git invocations `check --json` makes on a two-level stack.
	credit := lifecycle.CreditLookup(func() ([]lifecycle.CreditRef, error) {
		return creditRefs(ctx, s.repo, s.cs, s.trunk)
	})
	reviewed, err := lifecycle.SummarizeAgainstTreeHEAD(ctx, s.repo, s.cs.Slug, s.cs.Base, credit)
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
	// The repository's own scripts, asked last: it is the one condition the branch cannot fix by editing the
	// branch, and the one a green verdict used to hide. It lives in the gate rather than in the report beside it
	// because the difference is the verdict — `check` used to answer "ready" while the checks the repository
	// asked for were skipped for want of a recorded position, and a pipeline that merged on that answer merged
	// work the team's own suite never saw. The caller names the points it runs, because the gate is shared and
	// the two commands do not run the same ones: `check` runs `pre-check` and `post-check`, and a declaration
	// runs `pre-integrate` and `post-integrate`. Asking about both from both would stop a merge over a script
	// neither command executes.
	hooksReason, err := a.hooksPositionReason(ctx, s.repo, points...)
	if err != nil {
		return nil, err
	}
	if hooksReason != "" {
		g.Reasons = append(g.Reasons, hooksReason)
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
// acknowledged, and the acknowledgement is recorded nowhere — so re-deriving it here would
// re-litigate a decision the author already took, in a command with no override flag to take it
// again. Say what that leaves outside the gate rather than implying it is covered: the drift
// condition below excludes the changeset directory, so reviewer text in `ABOUT.md` can reach the
// destination unaltered, exactly as an addition acknowledged with
// `--allow-surviving-review-additions` already can. What this command promises is that nothing
// outside that directory moved since the marker and that the author was asked before the marker,
// and it promises nothing more.
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
