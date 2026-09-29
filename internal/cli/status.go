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
	"gitpair/internal/reviewref"
	"gitpair/internal/span"
)

// --- status -----------------------------------------------------------------

func newStatusCommand(a *app) *cobra.Command {
	var changesetSlug string
	var doFetch bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the effective state of a changeset",
		Long: `Report the state derived from git history for a changeset.

State is never stored in a file. Lifecycle markers are commits carrying
Review-* trailers, and state moves when a git-pair command records one:
` + "`change ready`" + ` offers the changeset, ` + "`change unready`" + ` withdraws it, and a
review submission answers it. Ordinary commits do not change state; they are named
in the reason, and they are what ` + "`git pair check`" + ` refuses to pass.

While work is in flight the branch is the whole story: no ref is written by any of those commands,
and the only durable refs git-pair has are the two that ` + "`git pair integration record`" + ` writes
once the work has landed. ` + "`status`" + ` reports both, beside the state, when they exist.

--changeset reads another changeset by slug, from whichever branch carries it, so
you can ask about work you do not have checked out. Reads are the only commands
that do: a marker is a commit, and a commit lands on the branch you are standing on.

With --json the output is a stable contract for agents and automation.

--fetch asks the remote for the durable refs and their mirrors before answering, which is how you see
a landing recorded in somebody else's clone. Without it nothing here reaches the network, and what
status says about other clones is limited to what this one has fetched.`,
		Example: `  git pair status
  git pair status --json
  git pair status --changeset booking-transaction`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd.Context(), a, changesetSlug, doFetch)
		},
	}
	cmd.Flags().StringVar(&changesetSlug, "changeset", "",
		"read the changeset with this slug, from whichever branch carries it")
	fetchFlag(cmd, &doFetch)
	return cmd
}

type latestReviewJSON struct {
	Index   int    `json:"index"`
	Outcome string `json:"outcome"`
	Commit  string `json:"commit"`
	// ReviewedHead is the commit this submission spoke about, from its `Review-Head` trailer.
	// It is the value `check` measures lineage against, so a reader who is told "not in this
	// history" by the gate can see what the two ends of that comparison were (PRD §11.1).
	ReviewedHead string `json:"reviewed_head,omitempty"`
}

// parentJSON is the stack this changeset sits on as the reader sees it now: the parent branch, where
// its tip stands, the tip the newest approval recorded, and whether those two still agree. Absent for
// a changeset that is not stacked (PRD §21).
type parentJSON struct {
	Branch    string `json:"branch"`
	Changeset string `json:"changeset,omitempty"`
	Tip       string `json:"tip,omitempty"`
	Recorded  string `json:"recorded_at_approval,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Next      string `json:"next,omitempty"`
	Note      string `json:"note,omitempty"`
	// Landed says the parent has an integration record in this clone: its work is in a destination, and
	// the branch named above is what is left of it. The record is the only thing that can say this. A
	// `--no-ff` merge leaves the parent's branch exactly where the approval recorded it, so every
	// tip comparison in this file reads "nothing happened" in the one case where the work finished.
	Landed bool `json:"landed"`
	// LandedCommit is where the parent's record points, in the same short form as `tip`.
	LandedCommit string `json:"landed_commit,omitempty"`
	// LandedInDefaultBranch says the landing commit is in the history of the branch this run called the
	// integration branch. False is both "it reached a release branch" and "no branch here identifies
	// itself", which `landed_reach` spells out in prose.
	LandedInDefaultBranch bool   `json:"landed_in_default_branch"`
	LandedReach           string `json:"landed_reach,omitempty"`
	// StaleBranch says this changeset's own head already carries the landing, so the parent's branch is
	// dead weight: the record holds the chain it was holding, and deleting the branch loses nothing
	// git-pair can still read. False for a child that has not rebased onto the landing yet, where that
	// branch is still the base the diff is measured on.
	StaleBranch bool `json:"stale_branch"`
}

// stackStep is one changeset the reported one was stacked on. The step reports what the record and the
// branch say from here, not what they promised at the time: a parent whose branch is gone and whose record
// is in the integration branch's history is a landed parent, and that is the fact the reader of a child's
// status needs.
type stackStep struct {
	Changeset string `json:"changeset"`
	// Branch is the parent branch as the child's CHANGESET.yaml recorded it, empty when the yaml never
	// named one.
	Branch string `json:"branch,omitempty"`
	// BranchExists says the local clone still has that branch. False is not evidence it was deleted —
	// a clone that has never fetched it says the same thing, which is why the human wording says "is
	// gone" only where the reader is being told about the chain, not about a verdict.
	BranchExists bool `json:"branch_exists"`
	// Integration and IntegrationRef are the parent's record as this clone holds it: empty when this
	// clone has no record of that ancestor, which `--fetch` is the answer to.
	Integration    string `json:"integration_commit,omitempty"`
	IntegrationRef string `json:"integration_ref,omitempty"`
	// InDefaultBranch says the parent's recorded commit is in the integration branch's history. The
	// branch itself is reported once, at the top of the status JSON.
	InDefaultBranch bool `json:"in_default_branch"`
}

// stackDepthCap bounds the walk. CHANGESET.yaml is committed content, so `parent-changeset` can be
// hand-edited into a loop, and a chain is a fact about how work was organised rather than a graph the tool
// has to terminate on.
const stackDepthCap = 8

type statusJSON struct {
	Changeset string `json:"changeset"`
	Branch    string `json:"branch"`
	Base      string `json:"base"`
	// DefaultBranch is the integration branch this run compared the revision against,
	// DefaultBranchCommit the commit it named, DefaultBranchSource how the run learned it: `flag`,
	// `origin-head` or `sole-candidate`. The three belong together because a run that misreports
	// what has landed is the expensive failure of that rule, and the output has to be explainable
	// from itself rather than from what the machine happened to fetch. Named the way `base` is,
	// and shortened like `archive_commit`, because "landed" is measured against this branch and
	// `base` is only where the diff starts.
	DefaultBranch       string            `json:"default_branch"`
	DefaultBranchCommit string            `json:"default_branch_commit,omitempty"`
	DefaultBranchSource string            `json:"default_branch_source"`
	State               string            `json:"state"`
	Head                string            `json:"head"`
	HeadFull            string            `json:"head_full"`
	LatestReview        *latestReviewJSON `json:"latest_review"`
	// Parent is the stack, and is nil for a changeset measured against the integration branch.
	Parent *parentJSON `json:"parent,omitempty"`
	// ArchiveRef and ArchiveCommit report the changeset's archived chain: the unsquashed
	// implementation-and-review tip the record was made from. They are non-empty only once the
	// changeset has been recorded, because that is the only moment git-pair writes the ref. An
	// in-flight changeset has no archive to report — the branch holds the chain, and reporting a
	// ref that does not exist would be reporting a fact about the tool rather than the work.
	ArchiveRef      string `json:"archive_ref"`
	ArchiveCommit   string `json:"archive_commit"`
	Uncommitted     *bool  `json:"uncommitted"`
	Abandoned       bool   `json:"abandoned"`
	AbandonedCommit string `json:"abandoned_commit,omitempty"`
	// Integrating reports the author's declaration — `git pair change integrate` — and IntegrateCommit is
	// the commit that wrote it. It sits beside `state` for the reason `integrated` does: the state name
	// already says INTEGRATING while the declaration is the newest marker, and what the field adds is the
	// address of the thing that says so. The same rule `check --json` uses, so one read of the branch
	// cannot answer "did the author ask" two ways.
	Integrating     bool   `json:"integrating"`
	IntegrateCommit string `json:"integrate_commit,omitempty"`
	// Integrated reports the presence of an integration ref, which is the only record that a
	// changeset landed: squash, rebase and cherry-pick destroy the ancestry that would otherwise
	// answer it. Like `abandoned`, it sits beside `state` rather than inside it — the lifecycle
	// states are what markers move, and landing is not a marker.
	Integrated bool `json:"integrated"`
	// IntegratedCommit is where the record points, and IntegratedRef is the ref that holds it — the
	// address to fetch, diff, or hand to another person.
	IntegratedCommit string `json:"integrated_commit,omitempty"`
	IntegratedRef    string `json:"integration_ref,omitempty"`
	// Unpublished lists changesets whose record this clone holds and whose remote — as this clone last
	// fetched it — does not, or does not identically (§11.1). Never null: an empty list answers "nothing
	// is waiting to be published" and a missing key would answer "this build does not know how to look".
	Unpublished []unpublishedPair `json:"unpublished"`
	// UnpublishedNote is the one sentence for why the list is empty when the question could not be asked
	// at all — no remote, or a mirror namespace this clone has never fetched.
	UnpublishedNote string `json:"unpublished_note,omitempty"`
	// IntegratedInDefaultBranch says the recorded landing commit is in the history of the branch
	// git-pair calls the integration branch, and IntegratedDefaultBranch names that branch. Both
	// are derived at read time, and the pair rather than a single `integrated_target`: a ref stores
	// an object id and no branch name, so the branch a landing reached is not something git-pair
	// keeps. Work that retired into a release branch and never reached the default branch must not
	// read like a default-branch landing, and a lone "main" that was never recorded would be worse.
	IntegratedInDefaultBranch bool   `json:"integrated_in_default_branch"`
	IntegratedDefaultBranch   string `json:"integrated_default_branch,omitempty"`
	// Stack is the chain of changesets this one was stacked on, nearest first, read from
	// `parent-changeset` and each ancestor's own record. Never null: an empty list answers "this
	// changeset sat on the integration branch", and a missing key answers "this build cannot look".
	// It is filled for a recorded changeset, whose chain is the difference between a landing that
	// reached the integration branch and one that only reached a branch that later did (§11.4).
	Stack []stackStep `json:"stack"`
	// StackNote is the one sentence for where the walk stopped — a cycle, a depth cap, or a read that
	// failed — rather than a silent short list that reads as the whole chain.
	StackNote    string   `json:"stack_note,omitempty"`
	Reviews      int      `json:"reviews"`
	Reason       string   `json:"reason"`
	Span         string   `json:"span"`
	NextAction   string   `json:"next_action"`
	Unrecognised []string `json:"unrecognised_markers,omitempty"`
}

func runStatus(ctx context.Context, a *app, slug string, doFetch bool) error {
	s, err := a.loadFor(ctx, slug)
	if err != nil {
		return a.landingsOnNoChangeset(ctx, slug, doFetch, err)
	}
	if doFetch {
		a.fetchDurableRefs(ctx, s.repo, s.cs.Branch)
	}
	// The published-or-not comparison needs the namespace indexed by changeset, which `buildStatus`
	// reads for its own reasons without sharing it. One extra `for-each-ref` is the price of not
	// threading an index through a constructor that has no use for one; `queue`, where the cost actually
	// matters, reuses the index it already has.
	idx, err := indexDurableRefs(ctx, s.repo)
	if err != nil {
		return err
	}
	rep := a.publicationReport(ctx, s.repo, s.cs.Branch, idx, "`git pair status --fetch` asks for them", doFetch)
	view, err := buildStatus(ctx, a, s)
	if err != nil {
		return err
	}
	view.json.Unpublished = rep.Findings
	view.json.UnpublishedNote = rep.Note
	// The chain is only worth walking for a changeset that has a record: before that the answer to
	// "where does this sit" is the branch under the reader's feet, which the `parent:` line above the
	// fold already says. Once the changeset is recorded its history is two refs and a yaml file, and the
	// question "did any of it reach trunk" stops being answerable from the checkout.
	if view.json.Integrated {
		view.json.Stack, view.json.StackNote = a.stackChain(ctx, s, idx)
	}
	if a.json {
		// `stack` is `[]` when the walk did not run, so the key never arrives as a null: the reader cannot
		// tell "not stacked" from "this build did not look" otherwise.
		view.json.Stack = orEmpty(view.json.Stack)
		// view itself is unexported-only; emit its JSON shape.
		return a.emitJSON(view.json)
	}
	printStatus(a, view)
	// Published-or-not is a separate question from anything `printStatus` answers, and it is asked of
	// the whole namespace rather than of this changeset, so it goes after the per-changeset report rather
	// than inside it.
	a.printUnpublished(rep, true)
	return nil
}

// landingsOnNoChangeset annotates the one `status` failure that has a second half to it.
//
// "No changeset for this branch" is true and stays the answer — exit 2, because the command asked about
// work in progress and this branch holds none. But when the branch is the destination, the same
// repository may also be holding directories that landed with no record written, and a reader told only
// the first half goes looking for a branch they forgot instead of the record they did not write. The
// finding is printed where a reader on that branch will see it, with the invocation that closes the gap
// (PRD §22's contract, made detectable).
//
// Every step of the detection is best-effort: this path already has an answer, and a report about
// landings is never a reason to fail in a new way.
//
// The published-or-not comparison belongs here too, for the reason the section title states: the
// destination branch is the branch every changeset eventually lands on, and it was the one branch where a
// record that never left the clone was invisible. It costs one `for-each-ref` over the mirrors and no
// network, and `lookupDurableRemote` already defines an empty branch as "no particular branch", so origin
// is the answer. `--fetch` reaches this path only now: the fetch used to sit behind a successful load, so
// on the destination branch it never ran at all.
func (a *app) landingsOnNoChangeset(ctx context.Context, slug string, doFetch bool, err error) error {
	if slug != "" || !errors.Is(err, changeset.ErrNoChangeset) {
		return err
	}
	repo, rerr := a.loadRepo(ctx)
	if rerr != nil {
		return err
	}
	db, derr := changeset.DefaultBranch(ctx, repo, a.defaultBranch)
	if derr != nil {
		return err
	}
	dirs, derr := changeset.ActiveIDs(ctx, repo, db.Ref)
	if derr != nil {
		return err
	}
	durable, derr := indexDurableRefs(ctx, repo)
	if derr != nil {
		return err
	}
	if doFetch {
		a.fetchDurableRefs(ctx, repo, "")
	}
	rep := a.publicationReport(ctx, repo, "", durable, "`git pair status --fetch` asks for them", doFetch)
	unrecorded := durable.unrecordedLandings(dirs)
	if a.json {
		// A document on stdout and the failure on stderr, because the caller is a machine that has to tell
		// "asked, and none" from "this build could not look". Both lists are present and empty when there is
		// nothing to report, which is the shape `statusJSON` commits to on the success path.
		_ = a.emitJSON(map[string]any{
			"reason":            messageOf(err),
			"landed_unrecorded": orEmpty(unrecorded),
			"unpublished":       orEmpty(rep.Findings),
			"unpublished_note":  rep.Note,
		})
		return unrecordedInStatus(err, unrecorded, durable.NamespaceEmpty, displayRef(db.Ref))
	}
	// Printed before the error returns so both halves reach the reader who asked the question: the
	// findings on stdout, the reason for the exit code on stderr.
	a.printUnpublished(rep, false)
	return unrecordedInStatus(err, unrecorded, durable.NamespaceEmpty, displayRef(db.Ref))
}

type statusView struct {
	json      statusJSON
	span      span.Span
	latestAge string
	// integratedReach phrases where the landing commit sits, for the text surface: the JSON
	// surface reports the same facts as fields a consumer can branch on.
	integratedReach string
}

// stackChain walks the changesets this one was stacked on, nearest first. Each step is read from the
// durable records and from the ancestor's own CHANGESET.yaml, which is what makes it answer the question a
// recorded child cannot answer from its own two refs: the child's record names the commit it became on the
// branch it was based on, and whether any of that reached the integration branch is a fact about the
// parent's record, not this one.
//
// The walk is bounded twice over — a `parent-changeset` cycle and a depth cap — because CHANGESET.yaml is
// committed content and can say anything. A step whose ancestor has no record here is reported rather than
// skipped: the absence is the finding, and `--fetch` is the answer to it.
//
// Cost: one `for-each-ref` for the branch names (the records themselves are already in the index the caller
// holds), then one `merge-base` and one tree read per step.
func (a *app) stackChain(ctx context.Context, s *session, idx refIndex) ([]stackStep, string) {
	steps := []stackStep{}
	held, err := localBranchSet(ctx, s.repo)
	if err != nil {
		// The chain is withheld rather than half-shown: every step would have to say "branch gone" on
		// the strength of a read that failed, and a reader cannot tell that from a deleted parent.
		return steps, fmt.Sprintf("this clone could not list its branches (%s), so it cannot say which parent branches still exist", err)
	}
	seen := map[string]bool{s.cs.Slug: true}
	id, branch := s.cs.ParentChangeset, s.cs.ParentBranch
	for depth := 0; id != ""; depth++ {
		if seen[id] {
			return steps, fmt.Sprintf("changeset %s is named twice in this chain, so the walk stops there", id)
		}
		seen[id] = true
		if depth >= stackDepthCap {
			return steps, fmt.Sprintf("the chain is deeper than %d changesets, so the walk stops there", stackDepthCap)
		}
		step := stackStep{Changeset: id, Branch: branch, BranchExists: branch != "" && held[branch]}
		if sha, ok := idx.Integrated[id]; ok {
			step.Integration = sha
			step.IntegrationRef = idx.IntegratedRef[id]
			if s.trunk.Ref != "" {
				in, err := s.repo.IsAncestor(ctx, sha, s.trunk.Ref)
				if err != nil {
					return steps, fmt.Sprintf("the chain could not ask whether %s reaches %s (%s)", id, displayRef(s.trunk.Ref), err)
				}
				step.InDefaultBranch = in
			}
		}
		steps = append(steps, step)

		// The next hop comes from the ancestor's own yaml, read where it is known to live: its landing
		// commit, or failing that this changeset's head, whose tree carries the directories of everything
		// it was stacked on.
		at := step.Integration
		if at == "" {
			at = s.head
		}
		stack, err := changeset.StackAt(ctx, s.repo, at, id)
		if err != nil {
			return steps, fmt.Sprintf("changesets/%s/ cannot be read at %s, so the chain above it is unread here", id, short(at))
		}
		id, branch = stack.ParentChangeset, stack.Parent
	}
	return steps, ""
}

// localBranchSet is the branch names this clone holds, as a set.
func localBranchSet(ctx context.Context, repo *git.Repo) (map[string]bool, error) {
	refs, err := repo.ForEachRef(ctx, "refs/heads")
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(refs))
	for _, r := range refs {
		if name, ok := strings.CutPrefix(r.Name, "refs/heads/"); ok {
			out[name] = true
		}
	}
	return out, nil
}

func buildStatus(ctx context.Context, a *app, s *session) (*statusView, error) {
	view := &statusView{}
	view.json = statusJSON{
		Changeset:   s.cs.Slug,
		Branch:      s.cs.Branch,
		Base:        s.cs.Base,
		State:       string(s.summary.State),
		Head:        short(s.head),
		HeadFull:    s.head,
		Uncommitted: uncommitted(s),
		Reviews:     len(s.summary.Reviews),
		Reason:      s.summary.Reason,
		NextAction:  nextAction(s.summary, s.cs.Base),
	}
	for _, e := range s.summary.Unrecognised {
		view.json.Unrecognised = append(view.json.Unrecognised, e.Short+" "+e.Subject)
	}
	// The resolution that chose this changeset compared against trunk, so the run knows trunk even
	// when nothing else in the output says so. Failing to name the commit it pointed at is not worth
	// failing a status over — the branch and its provenance are the parts a reader acts on.
	view.json.DefaultBranch = displayRef(s.trunk.Ref)
	view.json.DefaultBranchSource = s.trunk.Source
	if sha, err := s.repo.RevParse(ctx, s.trunk.Ref); err == nil {
		view.json.DefaultBranchCommit = short(sha)
	}
	if at := s.summary.Abandoned; at != nil {
		view.json.Abandoned = true
		view.json.AbandonedCommit = at.SHA
	}
	// The declaration, read the way `check` reads it: the newest marker, and nothing that a later
	// re-offer or review has superseded.
	if m := s.summary.Marker; m != nil && m.Kind == lifecycle.KindIntegrate {
		view.json.Integrating = true
		view.json.IntegrateCommit = short(m.SHA)
	}
	if r := s.summary.LatestReview; r != nil {
		view.json.LatestReview = &latestReviewJSON{
			Index:        len(s.summary.Reviews) - 1,
			Outcome:      string(r.Outcome),
			Commit:       short(r.SHA),
			ReviewedHead: short(r.ReviewedHead),
		}
		view.latestAge = lifecycle.Age(r.When, now())
	}
	// The stack. An approval measures itself against a parent tip as well as a head, and the parent
	// moves in ways this branch's own history cannot show, so `status` says what the approval
	// recorded and whether it still stands (PRD §21).
	if ps, err := a.parentSinceApproval(ctx, s.repo, s.cs, s.trunk, s.summary.Marker, s.head); err != nil {
		return nil, err
	} else if ps.Branch != "" {
		view.json.Parent = &parentJSON{
			Branch:                ps.Branch,
			Changeset:             ps.Changeset,
			Tip:                   short(ps.Tip),
			Recorded:              short(ps.Recorded),
			Reason:                ps.Reason,
			Next:                  ps.Next,
			Note:                  ps.Note,
			Landed:                ps.Landed != "",
			LandedCommit:          ps.Landed,
			LandedInDefaultBranch: ps.LandedInDefaultBranch,
			LandedReach:           ps.LandedReach,
			StaleBranch:           ps.StaleBranch,
		}
	}
	if sha, err := reviewref.ResolveArchive(ctx, s.repo, s.cs.Slug); err == nil {
		// The name of the ref is derivable from the slug, so it is only worth
		// reporting once the ref exists: its absence is the answer to "has this ever been
		// recorded", which a slug-derived string could never give.
		view.json.ArchiveRef = reviewref.Archive(s.cs.Slug)
		view.json.ArchiveCommit = short(sha)
	} else if !errors.Is(err, reviewref.ErrNoArchiveRef) {
		return nil, err
	}
	if integrated, err := reviewref.ResolveIntegration(ctx, s.repo, s.cs.Slug); err == nil {
		view.json.Integrated = true
		view.json.IntegratedCommit = short(integrated)
		view.json.IntegratedRef = reviewref.Integration(s.cs.Slug)
		l, err := a.describeLanding(ctx, s.repo, integrated)
		if err != nil {
			return nil, err
		}
		view.json.IntegratedInDefaultBranch = l.InDefaultBranch
		view.json.IntegratedDefaultBranch = l.DefaultBranch
		view.integratedReach = l.reach()
	} else if !errors.Is(err, reviewref.ErrNotIntegrated) {
		return nil, err
	}
	// The span names the working span of this checkout — `base...current` — so it
	// means nothing for a changeset read from another branch. Saying nothing beats
	// printing a span that points somewhere else.
	if s.onCurrentBranch {
		if sp, err := span.Resolve(ctx, s.repo, s.cs.Base, s.summary, span.Full()); err == nil {
			view.json.Span = sp.Label
			view.span = sp
		}
	}
	if s.baseIsOwnBranch {
		// "no commits above the base yet" is technically true and useless here:
		// with base == branch there never will be any. Say what is actually wrong.
		view.json.Reason = fmt.Sprintf("base %q is this branch itself, so nothing can ever be above it", s.cs.Base)
		view.json.NextAction = fmt.Sprintf("give the change its own branch (`git switch -c <name>`), or set `base` in %s to an ancestor of %s",
			s.cs.MetadataPath(), s.cs.Branch)
	}
	if !s.onCurrentBranch {
		// Every command that records something writes to the branch that is checked
		// out, because a marker is a commit. The next step for a changeset you are
		// only reading is to stand on it.
		view.json.NextAction = fmt.Sprintf("`git switch %s` to act on it: git-pair records markers on the branch you have checked out", s.cs.Branch)
	}
	if view.json.Integrated {
		// Last, because it supersedes both answers above. Once the record exists, handing the work
		// on has happened: the two refs are written, nothing in git-pair moves them, and no
		// git-pair command is the next step. What is left of the branch's life is ordinary git.
		view.json.NextAction = fmt.Sprintf("integrated at %s: nothing further is recorded for a changeset that has landed", view.json.IntegratedCommit)
	}
	return view, nil
}

// uncommitted reports the working tree only when the session describes the checked-out
// branch. For a changeset read from elsewhere the tree holds someone else's work, and
// `false` would be a lie while `true` would be about the wrong thing.
func uncommitted(s *session) *bool {
	if !s.onCurrentBranch {
		return nil
	}
	v := !s.clean
	return &v
}

func printStatus(a *app, v *statusView) {
	j := v.json
	a.printf("Changeset: %s\n", j.Changeset)
	if j.Branch != "" {
		a.printf("Branch: %s\n", j.Branch)
	} else {
		// A changeset read from its durable record has no branch to name; saying "Branch:" with
		// nothing after it would read as a bug rather than as an absence.
		a.printf("Branch: none (read from the landed chain)\n")
	}
	a.printf("Base: %s\n", j.Base)
	a.printf("State: %s\n", j.State)
	a.printf("Head: %s\n", j.Head)
	if j.Span != "" {
		a.printf("Span: %s\n", j.Span)
	}
	if j.Reason != "" {
		a.printf("Reason: %s\n", j.Reason)
	}
	if j.Abandoned {
		a.printf("\nTerminal:\n  abandoned by %s (`git pair change abandon`)\n", short(j.AbandonedCommit))
	}
	if j.Integrating {
		// Beside the state rather than inside it, in the block where the branch's other standing facts are:
		// the state says what the newest marker is, and this names the commit that made it so.
		a.printf("\nDeclared:\n  ready to integrate at %s (`git pair change integrate`)\n", j.IntegrateCommit)
	}
	if j.Integrated {
		// Nothing here tells the reader to run `git pair integration record`: this block prints because that
		// command already wrote the ref, and the `--json` answer on the same facts is "nothing further is
		// recorded for a changeset that has landed". A landing with no record anywhere is a different
		// finding — the LANDED, UNRECORDED section, which names the command with the changeset in it.
		a.printf("\nIntegrated:\n  %s%s\n", j.IntegratedCommit, v.integratedReach)
		if j.IntegratedRef != "" {
			a.printf("  %s\n", j.IntegratedRef)
		}
	}
	if j.LatestReview != nil {
		a.printf("\nLatest review:\n")
		age := ""
		if v.latestAge != "" {
			age = "  (" + v.latestAge + " ago)"
		}
		a.printf("  outcome: %s\n", j.LatestReview.Outcome)
		a.printf("  commit: %s%s\n", j.LatestReview.Commit, age)
		if j.LatestReview.ReviewedHead != "" {
			// The commit the reviewer was looking at, which is not this commit: a review
			// submission is a child of the head it reviewed.
			a.printf("  reviewed: %s\n", j.LatestReview.ReviewedHead)
		}
		a.printf("  history: %d review(s) — `git pair review history`\n", j.Reviews)
	} else {
		a.printf("\nLatest review:\n  none yet\n")
	}
	if p := j.Parent; p != nil || len(j.Stack) > 0 || j.StackNote != "" {
		// One section, because both halves answer the same question from the two sides available:
		// `parent:` is the stack as the approval was recorded against it, and the records are the stack as
		// this clone can still read it. A landed parent has no branch tip to compare an approval against,
		// and a child's own two refs say nothing about whether the branch it landed on ever reached the
		// integration branch — which is the question a reader of a landed child is actually asking.
		a.printf("\nStack:\n")
		if p != nil {
			who := p.Branch
			if p.Changeset != "" {
				who = fmt.Sprintf("%s (changeset %s)", p.Branch, p.Changeset)
			}
			switch {
			case p.Landed && p.Tip != "":
				// Above the tip comparisons, because the tip is the wrong instrument here: a merge into the
				// destination leaves the parent's branch exactly where the approval recorded it, so "unchanged
				// since the approval" would be a true sentence about a parent whose work is already integrated.
				a.printf("  parent: %s at %s — landed as %s%s\n", who, p.Tip, p.LandedCommit, p.LandedReach)
			case p.Tip == "":
				a.printf("  parent: %s — the branch is gone\n", who)
			case p.Recorded == "":
				a.printf("  parent: %s at %s\n", who, p.Tip)
			case p.Recorded == p.Tip:
				a.printf("  parent: %s at %s — unchanged since the approval\n", who, p.Tip)
			default:
				a.printf("  parent: %s at %s (approval recorded %s)\n", who, p.Tip, p.Recorded)
			}
			if p.Reason != "" {
				// The reason already names the step — it is a refusal in another command's mouth, and a
				// reader of `status` is the same reader — so it is printed once rather than twice.
				a.printf("  stale:  %s\n", p.Reason)
			}
			if p.Note != "" {
				a.printf("  note:   %s\n", p.Note)
			}
		}
		// Each step is the ancestor's own record, which is the half the `parent:` line cannot carry: a
		// branch tip moves, and a landed parent has no tip at all.
		for _, st := range j.Stack {
			who := st.Changeset
			if st.Branch != "" {
				if st.BranchExists {
					who += " (branch " + st.Branch + ")"
				} else {
					who += " (branch " + st.Branch + " is gone)"
				}
			}
			if st.Integration == "" {
				a.printf("  record: %s — no record in this clone; `--fetch` brings what the remote holds\n", who)
				continue
			}
			reach := "not reachable from " + j.DefaultBranch
			if st.InDefaultBranch {
				reach = "reachable from " + j.DefaultBranch
			}
			a.printf("  record: %s -> %s, %s\n", who, short(st.Integration), reach)
		}
		if j.StackNote != "" {
			a.printf("  note:   %s\n", j.StackNote)
		}
	}
	if j.ArchiveRef != "" {
		// Both families belong to the record, so they read together: the commit the work became, and
		// the chain of what it went through to get there.
		a.printf("\nReview archive:\n  %s\n", j.ArchiveRef)
		if j.ArchiveCommit != "" {
			a.printf("    points at: %s\n", j.ArchiveCommit)
		}
	}
	if len(j.Unrecognised) > 0 {
		a.printf("\nUnrecognised review markers (treated as implementation commits):\n")
		for _, u := range j.Unrecognised {
			a.printf("  %s\n", u)
		}
	}
	if j.Uncommitted != nil {
		a.printf("\nUncommitted changes: %s\n", yesNo(*j.Uncommitted))
	}
	if j.NextAction != "" {
		a.printf("Next: %s\n", j.NextAction)
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// nextAction tells an agent what to run next, so it does not have to re-derive
// the lifecycle from the state name.
func nextAction(s lifecycle.Summary, base string) string {
	if s.Abandoned != nil {
		// The state is WORKING, which would otherwise read as "keep going". Nothing is
		// owed on an abandoned changeset, and `change ready` refuses it, so the honest
		// next step is none. (PRD §9.7)
		return "abandoned: nothing further is recorded; `git pair review history --changeset <slug>` reads what happened"
	}
	switch s.State {
	case model.StateWorking:
		return "implement, commit, then `git pair change ready`"
	case model.StateReady:
		return "waiting for a reviewer: `git pair review open` (author: `git pair change wait` to block on it)"
	case model.StateBlocked:
		return "address the review, then `git pair change ready` (read it with `git pair change feedback`)"
	case model.StateFeedback:
		if s.Stale {
			return "the head moved since the review: read it with `git pair change feedback`, " +
				"then `git pair change ready` to offer the new head"
		}
		return "optionally address feedback (read it with `git pair change feedback`), then " + landingNextAction(base)
	case model.StateApproved:
		if s.Stale {
			// The gate asks the tree, and it refuses this head (PRD §9.5). Pointing an agent at it
			// anyway would be a surprise it cannot predict from `state`.
			return "the head moved since the review: `git pair change ready` to offer it for review again"
		}
		return landingNextAction(base)
	case model.StateIntegrating:
		// The author's step is over; the merge belongs to whoever owns the destination branch, and git-pair
		// performs none of it (PRD §26). The push is named first because until the branch is on the remote
		// nobody else can see the request at all.
		if s.Stale {
			// A declaration is about a commit, and a commit has landed on top of the one it named.
			return "the head moved since the declaration: `git pair change integrate` to declare this head"
		}
		return "push the branch so whoever merges can see the request; then " + landingNextAction(base)
	}
	return ""
}

// landingNextAction is the landing contract (PRD §29) in one line, spelled once because four commands tell
// an author this same thing and a fifth spelling is how a contract drifts. git-pair performs the gate and the
// record and nothing in between: the merge itself is ordinary git, performed by whoever owns the
// branch, which is what keeps PRD §26's no-merge posture intact.
func landingNextAction(base string) string {
	if base == "" {
		base = "the base branch"
	}
	// The steps are the landing contract (PRD §29): record, then publish, then the branch may go. Publish
	// is spelled here because after the branch is deleted the refs are the only copy of the chain, and a
	// reader told only to record has been told to leave that copy unpublished.
	return fmt.Sprintf("`git pair check`, then merge into %s with ordinary git, then "+
		"`git pair integration record`, then `git pair integration publish`", base)
}
