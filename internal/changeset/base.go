package changeset

import (
	"context"
	"fmt"

	"gitpair/internal/git"
)

// Base is the ref a changeset's diff is measured against, plus the rule that produced it. The rule travels
// with the answer because a reader shown only `base: 4f2b8c1` cannot tell a branch from a landing from a
// fallback, and the three mean different things about a stack: one is a live branch somebody will move
// again, one is where landed work joined the destination, and one is git-pair admitting it could not tell.
//
// The type exists so the five surfaces that print a base — `status`, `check`, `change integrate`, the queue's
// parent note, and the diff itself — cannot drift into printing three different rules. `docs/plans/`
// `lineage-in-the-surface/` records the failure mode this prevents: two surfaces agreeing only because they
// print the same literal.
type Base struct {
	// Ref is what to measure against: a branch name while the parent branch is the answer, and a commit
	// otherwise. Every caller can pass it straight to git.
	Ref string
	// Why names the rule, in the reader's terms. It is printed beside the base, not instead of it.
	Why string
	// Derived says Ref is a commit derived from the destination rather than a branch this repository has.
	Derived bool
	// ParentBranch is the stack relationship, kept separate from the measurement: `parent:` still names
	// the branch the child was built on even when the base has moved to a landing (§21).
	ParentBranch string
}

// BaseFor answers what a changeset is measured against, in this order:
//
//  1. The parent branch, when it exists and its work has not reached the destination. That branch is where
//     the work above it is still being written, and an approval measured against it stays comparable while
//     the parent moves.
//  2. The merge base of the child's head and the destination, once the parent's branch is gone or its work
//     has landed. This is the case a durable ref got wrong: naming the parent's landing commit as the base
//     widened the child's diff to everything the destination gained after that commit, while the merge base
//     is where the child's own work starts — including after a rebase, where the parent's commits arrive
//     under the child and the child's diff stays its own.
//  3. The destination itself, when neither resolves, with `Why` saying so. A base that names nothing is not
//     usable by any caller, so the fallback is a real answer rather than an empty string.
//
// Cost is one containment read, one revision read, and one `merge-base`, and only for a changeset that is
// stacked at all. `Scan` caches it beside the destination's own directory list so the surfaces share one
// derivation per branch rather than paying for it five times.
func BaseFor(ctx context.Context, repo *git.Repo, c Changeset, head string, db DefaultBranchRef) (Base, error) {
	out := Base{ParentBranch: c.ParentBranch}
	if c.ParentChangeset == "" && c.ParentBranch == "" {
		// Not stacked: the base it recorded is the answer, and inventing a derivation would be second-guessing
		// a `base:` the author wrote on purpose.
		out.Ref, out.Why = c.Base, "recorded base"
		if out.Ref == "" {
			out.Ref, out.Why, out.Derived = db.Ref, "no base recorded, so the integration branch", true
		}
		return out, nil
	}

	parentLanded := false
	if db.Ref != "" && c.ParentChangeset != "" {
		parentLanded, _ = CarriesDir(ctx, repo, db.Ref, c.ParentChangeset)
	}
	if c.ParentBranch != "" && !parentLanded {
		if _, err := repo.RevParse(ctx, c.ParentBranch); err == nil {
			out.Ref, out.Why = c.ParentBranch, "the parent branch, which still carries the work below this one"
			return out, nil
		}
		// The parent branch is named and gone. Rule 2 below answers, and its `Why` says which half is missing.
	}

	if head != "" && db.Ref != "" {
		mb, err := repo.MergeBase(ctx, head, db.Ref)
		if err != nil {
			return out, fmt.Errorf("merge base of %s and %s: %w", shortRef(head), shortRef(db.Ref), err)
		}
		if mb != "" {
			out.Derived = true
			out.Ref = mb
			switch {
			case parentLanded && c.ParentBranch != "":
				out.Why = fmt.Sprintf("the parent %s landed, so the run this branch shares with %s",
					c.ParentChangeset, shortRef(db.Ref))
			case parentLanded:
				out.Why = fmt.Sprintf("the parent landed, so the run this branch shares with %s", shortRef(db.Ref))
			case c.ParentBranch != "":
				out.Why = fmt.Sprintf("the parent branch %s is gone, so the run this branch shares with %s",
					c.ParentBranch, shortRef(db.Ref))
			default:
				out.Why = fmt.Sprintf("the run this branch shares with %s", shortRef(db.Ref))
			}
			return out, nil
		}
	}

	if db.Ref != "" {
		out.Ref, out.Why, out.Derived = db.Ref, "no parent branch and no shared run with the integration branch, so the integration branch", true
		return out, nil
	}
	return Base{}, ErrNoDefaultBranch
}

// shortRef names a ref the way a person reads it, for a sentence rather than for git.
func shortRef(ref string) string {
	for _, prefix := range []string{"refs/remotes/", "refs/heads/", "refs/"} {
		if len(ref) > len(prefix) && ref[:len(prefix)] == prefix {
			return ref[len(prefix):]
		}
	}
	return ref
}
