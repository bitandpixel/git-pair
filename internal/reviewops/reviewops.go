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
// parentHead is the tip of the branch this changeset is stacked on, recorded so the approval can
// later be asked whether the parent has moved. Empty means "no parent", which is what an unstacked
// changeset and a parent branch that is not present both look like; the caller decides.
// baseHead is the commit the diff was measured against, recorded so the approval can later be asked
// whether the content it covered has moved. The two are the same commit while the parent's branch is
// the base, and stop being the same commit the moment the parent lands (PRD §21).
func Submit(ctx context.Context, repo *git.Repo, cs changeset.Changeset,
	outcome model.Outcome, body string, stageAll bool, parentHead, baseHead string, db changeset.DefaultBranchRef) (Result, error) {

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
	sha, err := marker.Commit(ctx, repo, marker.ReviewMessage(cs.Slug, outcome, before, parentHead, baseHead, body), db)
	if err != nil {
		return Result{}, err
	}
	files, err := repo.DiffNames(ctx, before, sha)
	if err != nil {
		return Result{Commit: sha}, err
	}
	return Result{Outcome: outcome, Commit: sha, Files: files}, nil
}
