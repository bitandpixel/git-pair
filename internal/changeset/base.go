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
//     the parent moves. When the name it answers with is the integration branch, the answer is the copy of
//     it this clone has fetched (`fetchedTrunk`) — the same branch, the fresher one.
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
	if c.BaseChangeset == "" && c.ParentBranch == "" {
		// Not stacked: the base it recorded is the answer, and inventing a derivation would be second-guessing
		// a `base:` the author wrote on purpose. Reading it is the one place the record is not the whole answer,
		// and `fetchedTrunk` is the correction: the record names trunk, and a name resolves to this clone's own
		// branch before the fetched one.
		if b, ok := fetchedTrunk(db, c.Base, out); ok {
			return b, nil
		}
		out.Ref, out.Why = c.Base, "recorded base"
		if out.Ref == "" {
			out.Ref, out.Why, out.Derived = db.Ref, "no base recorded, so the integration branch", true
		}
		return out, nil
	}

	parentLanded := false
	if db.Ref != "" && c.BaseChangeset != "" {
		parentLanded, _ = CarriesDir(ctx, repo, db.Ref, c.BaseChangeset)
	}
	if c.ParentBranch != "" && !parentLanded {
		if _, err := repo.RevParse(ctx, c.ParentBranch); err == nil {
			if b, ok := fetchedTrunk(db, c.ParentBranch, out); ok {
				return b, nil
			}
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
					c.BaseChangeset, shortRef(db.Ref))
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

// fetchedTrunk is rule 1 where the base names the integration branch: the branch is live, so the answer
// stays a branch rather than a derivation, and the copy to measure against is the one this clone has
// fetched.
//
// The reason is what a name resolves to. `init` records `base: main` because that is the spelling a file
// other machines read should carry, and a bare name resolves under `refs/heads/` first — git's own order.
// In the ordinary clone that branch is where trunk stood the day this one was cut, and `git fetch` moves
// `refs/remotes/origin/main` and leaves it there. Measure against the local copy after a rebase onto the
// fetched trunk and the diff carries every commit the destination gained in between: someone else's merged
// work, in the changeset of the person who rebased. `status` already answered with the fetched ref, because
// `DefaultBranch` prefers it, so the two answers were one changeset's worth of apart.
//
// ok is false in the three cases where the switch would be wrong or worth nothing: the base names a stack
// parent or a release branch rather than the integration branch; the integration branch is a branch of this
// clone rather than a fetched ref, where the two spellings are one commit; and no base was recorded at all,
// which the caller answers from the destination.
func fetchedTrunk(db DefaultBranchRef, name string, out Base) (Base, bool) {
	if name == "" || !db.Fetched() || !db.IsBranch(name) {
		return Base{}, false
	}
	out.Ref, out.Why = db.Ref, "the base names the integration branch, so the copy of it this clone has fetched"
	return out, true
}

// MeasureBase is the ref a caller that has no use for the rule behind it hands to git: the same answer
// `BaseFor` gives, reduced to the ref. The surfaces that print a base read `BaseFor` and keep `Why` — this
// is for the ones that pin a commit and move on, where the honest fallback is the base as recorded rather
// than a refusal. `BaseFor` is the answer with the rule attached; `MeasureBase` is the same answer for a
// caller that cannot report the rule anyway.
//
// It cannot fail by design. A repository git will not talk to answers with the recorded base, which is what
// every one of these surfaces did before this helper existed.
//
// `head` is the caller's own head, and is only read from the repository when the caller does not already
// hold one — a span resolver has it, and asking twice for a rev-parse to answer a question the cheap path
// settles without it is the worse trade.
func MeasureBase(ctx context.Context, repo *git.Repo, c Changeset, db DefaultBranchRef, head string) string {
	if head == "" {
		head, _ = repo.Head(ctx)
	}
	b, err := BaseFor(ctx, repo, c, head, db)
	if err != nil {
		return c.Base
	}
	return b.Ref
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
