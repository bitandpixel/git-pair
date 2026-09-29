package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"gitpair/internal/changeset"
	"gitpair/internal/lifecycle"
	"gitpair/internal/marker"
	"gitpair/internal/model"
)

// --- change integrate -------------------------------------------------------

// `change integrate` is the author's half of an automatic merge, and it is a marker and nothing more.
//
// Before it, an approved changeset had an author standing between the approval and the merge button: the
// gate, the merge, and the record were three steps one person ran in sequence, and CI could only take the
// merge over by ignoring the author. After it, the author says once — in a commit, on the branch, where
// every reader sees it — that the work is approved and should be merged, and whoever owns the destination
// branch acts on that. The merge is still ordinary git, and git-pair writes no ref when it happens:
// the destination's tree is the record. What moved is only who waits.
//
// It stays inside PRD §26. git-pair writes no merge, pushes nothing, and writes no ref; the declaration is
// a request that somebody else performs the merge, and `check` remains the gate that merge runs.

type integrateOptions struct {
	allowFeedback bool
}

func newChangeIntegrateCommand(a *app) *cobra.Command {
	opts := &integrateOptions{}
	cmd := &cobra.Command{
		Use:   "integrate",
		Short: "Declare the current changeset approved and ready to be merged",
		Long: `Declare that this changeset is approved and should be merged, by committing the
declaration to its branch.

The declaration is one empty commit — ` + "`git-pair: integrate <slug>`" + `, carrying
` + "`Review-State: integrating`" + ` and the head it covers — and nothing else: no ref, no config, no
network. Push the branch and it is the trigger. The gate a merge runs is still
` + "`git pair check --json`" + `, and CI's whole question is its two answers together:

  git pair check --json | jq -e '.ready and .integrating'

` + "`ready`" + ` answers "may this be merged"; ` + "`integrating`" + ` answers "did the author ask". A
pipeline that merged on the first alone would take the decision out of the author's hands, which is why
git-pair has never merged anything (PRD §26) and never will.

This command is not a merge and not a queue. git-pair performs no merge and writes no ref while work is
in flight, before it or after it: the landing is the changeset directory in the destination's tree, which
ordinary git writes (PRD §13.4). What the declaration changes is who waits for whom.

The gate is ` + "`git pair check`" + `, run before the commit is written, and it is the same code rather
than an imitation of it: no approval standing, the approved commit rewritten out of this history, content
outside ` + "`changesets/<id>/`" + ` changed since the approval, a stacked parent that moved or ended, and an
abandoned or already-landed changeset are each refused, every failed condition named in one run.

Two things about a declaration are worth holding onto:

It is about a commit. ` + "`Review-Head`" + ` names the head that was declared, for the reason a review
names one: a rebase rewrites the marker and keeps the message, and the stale name is what makes the gate
refuse the rewrite rather than bless it.

It is not a verdict. The approval underneath it stays the thing that permits the merge, which is why
` + "`check`" + ` keeps passing after you run this and keeps refusing after anything that would have made it
refuse before.

A stacked child is refused until its parent is landed. Declaring the child would ask for a merge onto a
branch that review can still rewrite, and a declaration whose base review can move is a declaration a gate
cannot check. Merging a child onto its parent by hand stays open — git-pair declines to queue it, not to
allow it.

Re-running at the same head records nothing and succeeds, so a script can declare unconditionally. A commit
after the declaration moves the head it names, and the next run declares that one instead.

Exit codes: 0 declared or already declared, 1 refused, 2 usage, 3 git failed.`,
		Example: `  git pair change integrate
  git pair change integrate --allow-feedback
  git pair change integrate --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChangeIntegrate(cmd.Context(), a, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.allowFeedback, "allow-feedback", false,
		"accept non-blocking feedback as sufficient, as git pair check --allow-feedback does")
	return cmd
}

// integrateResult is what one run did, and the shape the report prints. Commit is the declaration's SHA
// whether this run wrote it or found it already there; Written separates the two, because "I declared
// this" and "this was already declared" are different facts in a log and the same success in a script.
type integrateResult struct {
	Commit  string
	Written bool
	Reasons []string
}

// integrateJSON is the machine-readable answer. `reasons` is an array in both outcomes, so a caller
// reading a refusal and a caller reading a success handle one shape; `recorded` says this run wrote the
// marker, which is the difference between a build that acted and one that confirmed.
type integrateJSON struct {
	Changeset       string `json:"changeset"`
	Branch          string `json:"branch"`
	Base            string `json:"base"`
	State           string `json:"state"`
	Was             string `json:"was"`
	Head            string `json:"head"`
	Recorded        bool   `json:"recorded"`
	IntegrateCommit string `json:"integrate_commit,omitempty"`
	Destination     string `json:"destination,omitempty"`
	// DestinationSource names where the destination came from — "base" for this changeset's own base,
	// "parent" for the branch a landed parent landed on, "default" for the integration branch — because
	// the answer for a stacked child is not the branch its own yaml names, and a reader auditing a merge
	// needs to know which rule produced the target.
	DestinationSource string   `json:"destination_source,omitempty"`
	DestinationVia    []string `json:"destination_via,omitempty"`
	// DestinationUnreachable is the base the walk ended on when it no longer resolves here: the
	// destination below is then the default branch, and the reader learns that from this field rather
	// than from a coincidence.
	DestinationUnreachable string   `json:"destination_unreachable,omitempty"`
	Reasons                []string `json:"reasons"`
	NextAction             string   `json:"next_action,omitempty"`
}

func runChangeIntegrate(ctx context.Context, a *app, opts *integrateOptions) error {
	s, err := a.load(ctx)
	if err != nil {
		return err
	}
	if !s.clean {
		// The declaration is a commit, so a dirty tree is the same blocker it is for `change ready`:
		// repository state, not bad arguments — exit 1.
		return fmt.Errorf("working tree must be clean before declaring %s ready to integrate; commit or stash your changes first", s.cs.Slug)
	}
	if s.head == "" {
		return fmt.Errorf("nothing to declare: the repository has no commits yet")
	}
	if s.baseIsOwnBranch {
		return fmt.Errorf("cannot declare %s ready to integrate: base %q is this branch itself, so the changeset can never contain commits. Set `base` in %s to an ancestor of %s, or move the work to its own branch",
			s.cs.Slug, s.cs.Base, s.cs.MetadataPath(), s.cs.Branch)
	}
	if err := a.refuseIfAbandoned(ctx, s); err != nil {
		return err
	}
	// The recorder's gate, run here rather than inherited: a changeset with a record has landed, and a
	// declaration on top of it would be a request to merge work that is already merged.
	if err := marker.RefuseIntegrated(ctx, s.repo, s.cs.Slug, s.trunk); err != nil {
		return err
	}

	g, err := a.integrationGate(ctx, s, opts.allowFeedback)
	if err != nil {
		return err
	}
	reasons := append(append([]string{}, g.Reasons...),
		unlandedParentReason(s.cs, g.Parent)...)
	dest, err := changeset.DestinationFor(ctx, s.repo, s.cs, s.trunk)
	if err != nil {
		return err
	}
	if len(reasons) > 0 {
		return reportIntegrate(a, s, dest, integrateResult{Reasons: reasons}, g.Summary.State)
	}
	if at := alreadyDeclared(s); at != "" {
		return reportIntegrate(a, s, dest, integrateResult{Commit: at}, g.Summary.State)
	}
	sha, err := marker.Commit(ctx, s.repo, marker.IntegrateMessage(s.cs.Slug, s.head), s.trunk)
	if err != nil {
		return fmt.Errorf("creating integrate marker: %w", err)
	}
	return reportIntegrate(a, s, dest, integrateResult{Commit: sha, Written: true}, model.StateIntegrating)
}

// unlandedParentReason is the automatic path's rule about stacks: a child is declared only once the branch
// it is stacked on has landed.
//
// It belongs here and not in `integrationReasons`, and the difference is the two questions. `check` asks
// "may this merge?", and a person who owns both branches can answer yes to merging a child onto a parent
// that has not landed — that shape is still supported, it just stays a human decision. This command asks
// "merge this without asking me again". A branch whose own review is still open can be rebased, amended,
// or blocked an hour from now, and an unattended merge into it would be performed against history that
// stopped existing. Refusing the request is the whole mitigation; the human path stays open, so nothing is
// lost but the automation.
func unlandedParentReason(cs changeset.Changeset, parent parentStatus) []string {
	if parent.Branch == "" || parent.Landed != "" {
		return nil
	}
	if parent.Changeset == "" {
		// The stack names no parent changeset, so there is nothing to ask the destination about. "Cannot
		// tell" goes the direction that asks a person to look again, which is how every other
		// unanswerable comparison in this codebase is resolved.
		return []string{fmt.Sprintf(
			"this changeset is stacked on %s, which records no parent changeset, so git-pair cannot show that branch has landed: record the relationship with `git pair init --parent %s --set-parent`, or land %s on the integration branch before declaring this one",
			parent.Branch, parent.Branch, parent.Branch)}
	}
	return []string{fmt.Sprintf(
		"the parent %s is not landed on the integration branch, so declaring %s would ask for a merge onto %s — a branch review can still rewrite. Land the parent first: `git pair check`, then merge with ordinary git. Merging %s onto %s by hand stays open; git-pair declines to queue it, not to allow it",
		parent.Changeset, cs.Slug, parent.Branch, cs.Slug, parent.Branch)}
}

// alreadyDeclared is a declaration that still covers this head, or "" when one is needed.
//
// The test is "HEAD is the declaration commit itself": nothing has been put on top of the declaration
// since it was written, so there is nothing new to hand over. It is deliberately not "the state is already
// INTEGRATING", because the state survives a commit and the claim does not: the marker names the commit
// that was declared, and a commit after it — even one inside `changesets/<id>/`, which no gate counts as
// drift — moves the thing the declaration is about. The author re-declares the head they are actually
// handing over, and the chain shows each head that was offered for the merge.
func alreadyDeclared(s *session) string {
	if m := s.summary.Marker; m != nil && m.Kind == lifecycle.KindIntegrate && s.head == m.SHA {
		return m.SHA
	}
	return ""
}

// reportIntegrate is the answer in whichever shape was asked for. One function so the run that wrote a
// marker, the run that found one, and the run that refused cannot drift into three shapes.
func reportIntegrate(a *app, s *session, dest changeset.Destination, res integrateResult, state model.State) error {
	out := integrateJSON{
		Changeset:              s.cs.Slug,
		Branch:                 s.cs.Branch,
		Base:                   s.cs.Base,
		State:                  string(state),
		Was:                    string(s.summary.State),
		Head:                   s.head,
		Recorded:               res.Written,
		IntegrateCommit:        res.Commit,
		Destination:            dest.Ref,
		DestinationSource:      dest.Why,
		DestinationVia:         dest.Via,
		DestinationUnreachable: dest.Unreachable,
		Reasons:                orEmpty(res.Reasons),
	}
	if len(res.Reasons) == 0 {
		out.NextAction = integrateNextAction(s, dest)
	}
	if a.json {
		if len(res.Reasons) > 0 {
			_ = a.emitJSON(out)
			return errSilent
		}
		return a.emitJSON(out)
	}
	if len(res.Reasons) > 0 {
		a.printf("Cannot declare %s ready to integrate:\n", s.cs.Slug)
		for _, r := range res.Reasons {
			a.printf("- %s\n", r)
		}
		// The verdict is the output and the reasons each carry their own step, so this exits 1 without
		// also prefixing a `git-pair:` error line over the answer — the contract `check` established.
		return errSilent
	}
	if !res.Written {
		a.printf("%s is already declared ready to integrate at %s (head %s): nothing recorded\n",
			s.cs.Slug, short(res.Commit), short(s.head))
		a.printf("  merge into: %s%s\n", displayRef(dest.Ref), destinationNote(dest))
		return nil
	}
	a.printf("Integrating: %s\n", s.cs.Slug)
	a.printf("  head:        %s\n", short(s.head))
	a.printf("  declared by: %s\n", short(res.Commit))
	a.printf("  merge into:  %s%s\n", displayRef(dest.Ref), destinationNote(dest))
	a.printf("  next:        %s\n", out.NextAction)
	return nil
}

// integrateNextAction is the step that makes the declaration reachable by whoever can act on it. It names
// push as a command the author runs and never as one git-pair ran: publishing is not this tool's verb
// (PRD §26), and a log line that reads like a completed action is how a tool acquires a side effect nobody
// agreed to.
func integrateNextAction(s *session, dest changeset.Destination) string {
	branch := s.cs.Branch
	if branch == "" {
		branch = "<branch>"
	}
	target := displayRef(dest.Ref)
	if target == "" {
		target = "the destination branch"
	}
	return fmt.Sprintf("`git push origin %s` — %s merges what `git pair check --json` reports ready and integrating",
		branch, target)
}

// destinationNote labels a destination that did not come from this changeset's own base. Short on
// purpose: the refusal is where an explanation belongs, and a reader who merged a parent onto a release
// branch wants to see that the child followed it rather than a word about the rule.
func destinationNote(dest changeset.Destination) string {
	switch dest.Why {
	case "parent":
		return fmt.Sprintf(" (where %s landed)", dest.Via[len(dest.Via)-1])
	case "default":
		if dest.Unreachable != "" {
			return fmt.Sprintf(" (the branch %s named is gone from here)", displayRef(dest.Unreachable))
		}
		return " (the default branch)"
	}
	return ""
}
