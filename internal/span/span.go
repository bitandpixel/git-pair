// Package span resolves "what should this review look at" into a pair of
// commits, per PRD §17.
package span

import (
	"context"
	"errors"
	"fmt"

	"gitpr/internal/git"
	"gitpr/internal/lifecycle"
)

// Kind distinguishes the two span families.
type Kind int

const (
	// Full is `base...HEAD`: the whole changeset.
	Full Kind = iota
	// SinceReview is `<review>..HEAD`: what happened after a review.
	SinceReview
)

func (k Kind) String() string {
	if k == Full {
		return "full"
	}
	return "since-review"
}

// Options come straight from CLI flags. Both zero values mean "full changeset".
type Options struct {
	Unreviewed bool
	// SinceReview is nil unless --since-review was given. Indexes are
	// chronological and may be negative: -1 is the most recent review.
	SinceReview *int
}

func (o Options) empty() bool { return !o.Unreviewed && o.SinceReview == nil }

// ErrNoReviews is returned when a review-relative span is requested but the
// changeset has never been reviewed.
var ErrNoReviews = errors.New("changeset has no review submissions yet")

// Span is a resolved commit range. From and To are full SHAs, so callers can
// hand them to git as two separate revs and never rely on `...` semantics.
type Span struct {
	Kind    Kind
	From    string
	To      string
	FromRef string // how the start was named: a base ref or a short review SHA
	// Label is the human-facing rendering, e.g. "main...HEAD" or "8ab932f..HEAD".
	Label string
	// ReviewIndex is the resolved chronological index of the start review, or
	// -1 for a full span.
	ReviewIndex int
}

// Resolve turns options into a commit range against the current HEAD.
func Resolve(ctx context.Context, repo *git.Repo, base string, summary lifecycle.Summary, opts Options) (Span, error) {
	head, err := repo.Head(ctx)
	if err != nil {
		return Span{}, err
	}
	if opts.empty() {
		from, err := repo.MergeBase(ctx, base, head)
		if err != nil {
			return Span{}, fmt.Errorf("cannot resolve changeset span against base %q: %w", base, err)
		}
		return Span{
			Kind: Full, From: from, To: head,
			FromRef: base, Label: fmt.Sprintf("%s...HEAD", base), ReviewIndex: -1,
		}, nil
	}

	idx := -1
	if opts.SinceReview != nil {
		idx = *opts.SinceReview
	}
	if len(summary.Reviews) == 0 {
		return Span{}, ErrNoReviews
	}
	review, ok := summary.ReviewIndex(idx)
	if !ok {
		return Span{}, fmt.Errorf("no review at index %d: this changeset has %d review(s) (valid: 0..%d or -1..-%d)",
			idx, len(summary.Reviews), len(summary.Reviews)-1, len(summary.Reviews))
	}
	resolved := idx
	if resolved < 0 {
		resolved += len(summary.Reviews)
	}
	return Span{
		Kind: SinceReview, From: review.SHA, To: head,
		FromRef:     review.Short,
		Label:       fmt.Sprintf("%s..HEAD (after review %d)", review.Short, resolved),
		ReviewIndex: resolved,
	}, nil
}
