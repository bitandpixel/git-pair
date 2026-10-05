// Package reviewops performs review operations shared by the CLI and the TUI,
// so both record reviews identically.
package reviewops

import (
	"context"
	"fmt"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/marker"
	"gitpair/internal/model"
)

// Result describes a completed review submission.
type Result struct {
	Outcome model.Outcome `json:"outcome"`
	// Commit is the review submission's full SHA.
	Commit string `json:"commit"`
	// Files are the paths the review changed; empty for an empty review.
	Files []string `json:"files"`
}

// Empty reports whether the review recorded no file changes, which is valid and
// normal for an approval on a clean tree.
func (r Result) Empty() bool { return len(r.Files) == 0 }

// Submit records a review submission.
//
// It writes a commit and nothing else. There is no ref to move: while work is in flight the branch
// is the whole story, and no ref is written at landing or at any other point,
// once, from a head that has stopped moving. A review commit is therefore its own record — `Review-*`
// trailers on an ordinary commit, reachable from the branch, greppable with the tools the reviewer
// already uses.
//
// stageAll controls whether the working tree is swept in first. It defaults to
// true because a review submission normally *is* everything the reviewer just
// did; the CLI's --no-stage exists for reviewers who stage deliberately.
//
// `m` is what the submission records about the diff it reviewed — the parent's tip, the commit the diff
// was measured from, and that diff's identity. It arrives measured rather than as three strings because
// the two surfaces that submit a review must not be able to record different things about the same head;
// `changeset.MeasureSubmission` is the one place that measurement is written, and `changeset.Measurement`
// is where each field's question is explained.
func Submit(ctx context.Context, repo *git.Repo, cs changeset.Changeset,
	outcome model.Outcome, body string, stageAll bool, m changeset.Measurement, db changeset.DefaultBranchRef) (Result, error) {

	if !outcome.Valid() {
		return Result{}, fmt.Errorf("invalid review outcome %q", outcome)
	}
	if _, err := repo.Head(ctx); err != nil {
		return Result{}, fmt.Errorf("cannot submit a review with no commits: %w", err)
	}
	if stageAll {
		if err := repo.StageAll(ctx); err != nil {
			return Result{}, err
		}
	}
	before, err := repo.Head(ctx)
	if err != nil {
		return Result{}, err
	}
	// The submission names the commit it was made against. `before` is that commit and the
	// new commit's first parent, so the trailer records the value a rebase changes — which is
	// the whole point of writing it down rather than leaving it to be read off the graph.
	sha, err := marker.Commit(ctx, repo, marker.ReviewMessage(cs.Slug, outcome, before, m, body), db)
	if err != nil {
		return Result{}, err
	}
	files, err := repo.DiffNames(ctx, before, sha)
	if err != nil {
		return Result{Commit: sha}, err
	}
	return Result{Outcome: outcome, Commit: sha, Files: files}, nil
}
