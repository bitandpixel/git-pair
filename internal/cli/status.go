package cli

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"gitpr/internal/git"
	"gitpr/internal/lifecycle"
	"gitpr/internal/model"
	"gitpr/internal/reviewref"
	"gitpr/internal/span"
)

// --- status -----------------------------------------------------------------

func newStatusCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the effective state of the current changeset",
		Long: `Report the state derived from git history for the changeset on this branch.

State is never stored in a file. Lifecycle markers are commits carrying
GitPR-* trailers, so an implementation commit after a ready or review marker
returns the changeset to WORKING automatically.

With --json the output is a stable contract for agents and automation.`,
		Example: `  gitpr status
  gitpr status --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd.Context(), a)
		},
	}
}

type latestReviewJSON struct {
	Index   int    `json:"index"`
	Outcome string `json:"outcome"`
	Commit  string `json:"commit"`
}

type statusJSON struct {
	Changeset    string            `json:"changeset"`
	Branch       string            `json:"branch"`
	Base         string            `json:"base"`
	State        string            `json:"state"`
	Head         string            `json:"head"`
	HeadFull     string            `json:"head_full"`
	LatestReview *latestReviewJSON `json:"latest_review"`
	ReviewRef    string            `json:"review_ref"`
	ReviewCommit string            `json:"review_commit"`
	ArchiveRef   string            `json:"archive_ref"`
	Uncommitted  bool              `json:"uncommitted"`
	Reviews      int               `json:"reviews"`
	Reason       string            `json:"reason"`
	Span         string            `json:"span"`
	NextAction   string            `json:"next_action"`
	Unrecognised []string          `json:"unrecognised_markers,omitempty"`
}

func runStatus(ctx context.Context, a *app) error {
	s, err := a.load(ctx)
	if err != nil {
		return err
	}
	view, err := buildStatus(ctx, s)
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
	json       statusJSON
	span       span.Span
	latestAge  string
	closed     bool
	archiveRef string
}

func buildStatus(ctx context.Context, s *session) (*statusView, error) {
	view := &statusView{}
	view.json = statusJSON{
		Changeset:   s.cs.Slug,
		Branch:      s.cs.Branch,
		Base:        s.cs.Base,
		State:       string(s.summary.State),
		Head:        short(s.head),
		HeadFull:    s.head,
		Uncommitted: !s.clean,
		Reviews:     len(s.summary.Reviews),
		Reason:      s.summary.Reason,
		ReviewRef:   reviewref.Head(s.cs.Slug),
		NextAction:  nextAction(s.summary),
	}
	for _, e := range s.summary.Unrecognised {
		view.json.Unrecognised = append(view.json.Unrecognised, e.Short+" "+e.Subject)
	}
	if r := s.summary.LatestReview; r != nil {
		view.json.LatestReview = &latestReviewJSON{
			Index:   len(s.summary.Reviews) - 1,
			Outcome: string(r.Outcome),
			Commit:  short(r.SHA),
		}
		view.latestAge = lifecycle.Age(r.When, now())
	}
	if sha, err := reviewref.Resolve(ctx, s.repo, s.cs.Slug); err == nil {
		view.json.ReviewCommit = short(sha)
	} else if !errors.Is(err, reviewref.ErrNoReviewRef) {
		return nil, err
	}
	if sp, err := span.Resolve(ctx, s.repo, s.cs.Base, s.summary, span.Options{}); err == nil {
		view.json.Span = sp.Label
		view.span = sp
	}
	if s.summary.State == model.StateClosed {
		view.closed = true
		if ref, err := archiveRefFor(ctx, s.repo, s.cs.Slug, s.head); err == nil {
			view.archiveRef = ref
			view.json.ArchiveRef = ref
		}
	}
	return view, nil
}

func printStatus(a *app, v *statusView) {
	j := v.json
	a.printf("Changeset: %s\n", j.Changeset)
	a.printf("Branch: %s\n", j.Branch)
	a.printf("Base: %s\n", j.Base)
	a.printf("State: %s\n", j.State)
	a.printf("Head: %s\n", j.Head)
	if j.Span != "" {
		a.printf("Span: %s\n", j.Span)
	}
	if j.Reason != "" {
		a.printf("Reason: %s\n", j.Reason)
	}
	if j.LatestReview != nil {
		a.printf("\nLatest review:\n")
		age := ""
		if v.latestAge != "" {
			age = "  (" + v.latestAge + " ago)"
		}
		a.printf("  outcome: %s\n", j.LatestReview.Outcome)
		a.printf("  commit: %s%s\n", j.LatestReview.Commit, age)
		a.printf("  history: %d review(s) — `gitpr review history`\n", j.Reviews)
	} else {
		a.printf("\nLatest review:\n  none yet\n")
	}
	a.printf("\nReview archive:\n  %s\n", j.ReviewRef)
	if v.archiveRef != "" {
		a.printf("  %s\n", v.archiveRef)
	}
	if j.ReviewCommit != "" {
		a.printf("  points at: %s\n", j.ReviewCommit)
	}
	if len(j.Unrecognised) > 0 {
		a.printf("\nUnrecognised GitPR markers (treated as implementation commits):\n")
		for _, u := range j.Unrecognised {
			a.printf("  %s\n", u)
		}
	}
	a.printf("\nUncommitted changes: %s\n", yesNo(j.Uncommitted))
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

// archiveRefFor returns the close-time archive ref for a changeset, preferring
// the ref whose commit matches the current HEAD and falling back to the newest.
func archiveRefFor(ctx context.Context, repo *git.Repo, slug, head string) (string, error) {
	entries, err := repo.ForEachRef(ctx, reviewref.ArchivePattern(slug))
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.SHA == head {
			return e.Name, nil
		}
	}
	return "", nil
}

// nextAction tells an agent what to run next, so it does not have to re-derive
// the lifecycle from the state name.
func nextAction(s lifecycle.Summary) string {
	switch s.State {
	case model.StateWorking:
		return "implement, commit, then `gitpr change ready`"
	case model.StateReady:
		return "waiting for a reviewer: `gitpr review open`"
	case model.StateBlocked:
		return "address the review, then `gitpr change ready` (see `gitpr diff --unreviewed`)"
	case model.StateFeedback:
		return "optionally address feedback, then `gitpr review close`"
	case model.StateApproved:
		return "run `gitpr review close` before squash/merge"
	case model.StateClosed:
		return "safe to squash/merge; review history is under refs/reviews/"
	}
	return ""
}
