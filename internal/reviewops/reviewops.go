// Package reviewops performs review operations shared by the CLI and the TUI,
// so both record reviews identically.
package reviewops

import (
	"context"
	"fmt"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/lifecycle"
	"gitpair/internal/marker"
	"gitpair/internal/model"
	"gitpair/internal/reviewref"
)

// Result describes a completed review submission.
type Result struct {
	Outcome model.Outcome `json:"outcome"`
	// Commit is the review submission's full SHA.
	Commit string `json:"commit"`
	// Files are the paths the review changed; empty for an empty review.
	Files []string `json:"files"`
	// Ref is the review ref moved to Commit.
	Ref string `json:"review_ref"`
}

// Empty reports whether the review recorded no file changes, which is valid and
// normal for an approval on a clean tree.
func (r Result) Empty() bool { return len(r.Files) == 0 }

// Submit records a review submission and anchors it in refs/reviews/.
//
// stageAll controls whether the working tree is swept in first. It defaults to
// true because a review submission normally *is* everything the reviewer just
// did; the CLI's --no-stage exists for reviewers who stage deliberately.
//
// The ref is updated in the same call as the commit: an anchored review is the
// point of the operation.
func Submit(ctx context.Context, repo *git.Repo, cs changeset.Changeset, summary lifecycle.Summary,
	outcome model.Outcome, body string, stageAll bool) (Result, error) {

	if !outcome.Valid() {
		return Result{}, fmt.Errorf("invalid review outcome %q", outcome)
	}
	if summary.State == model.StateClosed {
		return Result{}, fmt.Errorf("changeset %s is closed", cs.Slug)
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
	ref, err := reviewref.Update(ctx, repo, cs.Slug, sha)
	if err != nil {
		return Result{Commit: sha, Ref: ref}, fmt.Errorf(
			"review commit %s was created but %s could not be updated: %w", short(sha), ref, err)
	}
	files, err := repo.DiffNames(ctx, before, sha)
	if err != nil {
		return Result{Commit: sha, Ref: ref}, err
	}
	return Result{Outcome: outcome, Commit: sha, Files: files, Ref: ref}, nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
