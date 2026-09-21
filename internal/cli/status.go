package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"gitpair/internal/lifecycle"
	"gitpair/internal/model"
	"gitpair/internal/reviewref"
	"gitpair/internal/span"
)

// --- status -----------------------------------------------------------------

func newStatusCommand(a *app) *cobra.Command {
	var changesetSlug string
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

With --json the output is a stable contract for agents and automation.`,
		Example: `  git pair status
  git pair status --json
  git pair status --changeset booking-transaction`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd.Context(), a, changesetSlug)
		},
	}
	cmd.Flags().StringVar(&changesetSlug, "changeset", "",
		"read the changeset with this slug, from whichever branch carries it")
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
	// Integrated reports the presence of an integration ref, which is the only record that a
	// changeset landed: squash, rebase and cherry-pick destroy the ancestry that would otherwise
	// answer it. Like `abandoned`, it sits beside `state` rather than inside it — the lifecycle
	// states are what markers move, and landing is not a marker.
	Integrated bool `json:"integrated"`
	// IntegratedCommit is where the record points, and IntegratedRef is the ref that holds it — the
	// address to fetch, diff, or hand to another person.
	IntegratedCommit string `json:"integrated_commit,omitempty"`
	IntegratedRef    string `json:"integration_ref,omitempty"`
	// IntegratedInDefaultBranch says the recorded landing commit is in the history of the branch
	// git-pair calls the integration branch, and IntegratedDefaultBranch names that branch. Both
	// are derived at read time, and the pair rather than a single `integrated_target`: a ref stores
	// an object id and no branch name, so the branch a landing reached is not something git-pair
	// keeps. Work that retired into a release branch and never reached the default branch must not
	// read like a default-branch landing, and a lone "main" that was never recorded would be worse.
	IntegratedInDefaultBranch bool     `json:"integrated_in_default_branch"`
	IntegratedDefaultBranch   string   `json:"integrated_default_branch,omitempty"`
	Reviews                   int      `json:"reviews"`
	Reason                    string   `json:"reason"`
	Span                      string   `json:"span"`
	NextAction                string   `json:"next_action"`
	Unrecognised              []string `json:"unrecognised_markers,omitempty"`
}

func runStatus(ctx context.Context, a *app, slug string) error {
	s, err := a.loadFor(ctx, slug)
	if err != nil {
		return err
	}
	view, err := buildStatus(ctx, a, s)
	if err != nil {
		return err
	}
	if a.json {
		// view itself is unexported-only; emit its JSON shape.
		return a.emitJSON(view.json)
	}
	printStatus(a, view)
	return nil
}

type statusView struct {
	json      statusJSON
	span      span.Span
	latestAge string
	// integratedReach phrases where the landing commit sits, for the text surface: the JSON
	// surface reports the same facts as fields a consumer can branch on.
	integratedReach string
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
	if r := s.summary.LatestReview; r != nil {
		view.json.LatestReview = &latestReviewJSON{
			Index:        len(s.summary.Reviews) - 1,
			Outcome:      string(r.Outcome),
			Commit:       short(r.SHA),
			ReviewedHead: short(r.ReviewedHead),
		}
		view.latestAge = lifecycle.Age(r.When, now())
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
		a.printf("Branch: none (read from the durable record)\n")
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
	if j.Integrated {
		a.printf("\nIntegrated:\n  %s%s (`git pair integration record`)\n", j.IntegratedCommit, v.integratedReach)
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
	}
	return ""
}

// landingNextAction is the step after an approval, spelled once because four commands tell an author
// this same thing and a fifth spelling is how a contract drifts. git-pair performs the gate and the
// record and nothing in between: the merge itself is ordinary git, performed by whoever owns the
// branch, which is what keeps PRD §26's no-merge posture intact.
func landingNextAction(base string) string {
	if base == "" {
		base = "the base branch"
	}
	return fmt.Sprintf("`git pair check`, then merge into %s with ordinary git, then `git pair integration record`", base)
}
