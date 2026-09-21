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
// is the whole story, and the durable refs are written at landing by `git pair integration record`,
// once, from a head that has stopped moving. A review commit is therefore its own record — `Review-*`
// trailers on an ordinary commit, reachable from the branch, greppable with the tools the reviewer
// already uses.
//
// stageAll controls whether the working tree is swept in first. It defaults to
// true because a review submission normally *is* everything the reviewer just
// did; the CLI's --no-stage exists for reviewers who stage deliberately.
func Submit(ctx context.Context, repo *git.Repo, cs changeset.Changeset,
	outcome model.Outcome, body string, stageAll bool) (Result, error) {

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
	sha, err := marker.Commit(ctx, repo, marker.ReviewMessage(cs.Slug, outcome, body))
	if err != nil {
		return Result{}, err
	}
	files, err := repo.DiffNames(ctx, before, sha)
	if err != nil {
		return Result{Commit: sha}, err
	}
	return Result{Outcome: outcome, Commit: sha, Files: files}, nil
}
