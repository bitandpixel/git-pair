package changeset

import (
	"context"
	"fmt"

	"gitpair/internal/git"
	"gitpair/internal/model"
)

// Base is the ref a changeset's diff is measured against, plus the rule that produced it. The rule travels
// with the answer because a base that names the integration branch means three different things about a
// stack — a live parent branch, a parent whose work has landed, and git-pair admitting it could not tell —
// and only the first is obvious from the name.
//
// The type exists so the five surfaces that print a base — `status`, `check`, `change integrate`, the queue's
// parent note, and the diff itself — cannot drift into printing three different rules. `docs/plans/`
// `lineage-in-the-surface/` records the failure mode this prevents: two surfaces agreeing only because they
// print the same literal.
type Base struct {
	// Ref is what to measure against: a branch name, whether it is the parent branch, the destination, or
	// the recorded `base:`. Every caller can pass it straight to git.
	Ref string
	// Why names the rule, in the reader's terms. It is printed beside the base, not instead of it.
	Why string
	// Derived says Ref was derived from the destination rather than recorded by the changeset. It is what
	// tells a caller that the name it holds is a measurement point and not a place work can land.
	Derived bool
	// ParentBranch is the stack relationship, kept separate from the measurement: `parent:` still names
	// the branch the child was built on even when the base has moved to the destination (§21).
	ParentBranch string
	// ParentLanded says the destination's tree carries the directory of the changeset this one is stacked
	// on. It is the fact that rule 2 read to decide it was answering at all, kept so a surface can say "the
	// parent landed" beside a base that no longer names the parent's branch. It is false for a changeset
	// that is not stacked, and for a stack whose parent has not reached the destination.
	ParentLanded bool
}

// BaseFor answers what a changeset is measured against, in this order:
//
//  1. The parent branch, when it exists and its work has not reached the destination. That branch is where
//     the work above it is still being written, and an approval measured against it stays comparable while
//     the parent moves. When the name it answers with is the integration branch, the answer is the copy of
//     it this clone has fetched (`fetchedTrunk`) — the same branch, the fresher one.
//  2. The destination, once the parent's branch is gone or its work has landed. Where the diff starts is
//     still the merge base of the child's head and that destination, and every caller takes it there:
//     `span` re-derives it for the two tree ends, and `base..head` reads identically for the destination and
//     for its own merge base, because any commit reachable from both is an ancestor of it. Naming the
//     destination rather than the commit it resolves to is what the answer is *for* — see below.
//  3. The destination itself, when neither resolves, with `Why` saying so. A base that names nothing is not
//     usable by any caller, so the fallback is a real answer rather than an empty string.
//
// Rule 2 used to answer with the merge-base commit itself, and a surface printing that told a reader nothing.
// The object id overflows the review screen's base row, and what it names is not the landing a reader would
// recognise: after `git rebase --onto <destination>` it is the destination's newest commit, which is usually
// somebody else's work. `docs/plans/` `lineage-in-the-surface/` records the older failure this replaced —
// naming the landing commit as a fixed base, which widened a rebased child's diff to everything the
// destination gained after it. That is what the merge base is for, and it stays the measurement; only the
// name changed.
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
			// The destination answers, and `mb` is the reason it may: it proves this branch and that destination
			// share a run, which is what makes the destination a base rather than an unrelated branch. It is not
			// printed, because a commit the reader cannot name is not an explanation — see rule 2 above.
			out.Derived = true
			out.Ref = db.Ref
			out.ParentLanded = parentLanded
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

// Measure is `BaseFor` for a caller that has to print the rule as well as the ref, cannot fail, and may not
// already hold a head: it fills the head it is not given, and a repository git will not talk to answers with
// the recorded base rather than with an error. `MeasureBase` is the ref it picks; the review screen reads
// the whole answer, because it says "the parent landed" beside the base and only the rule knows that.
//
// The error path keeps no `Why`: there is no rule to name, only the value the file carries.
func Measure(ctx context.Context, repo *git.Repo, c Changeset, db DefaultBranchRef, head string) Base {
	if head == "" {
		head, _ = repo.Head(ctx)
	}
	b, err := BaseFor(ctx, repo, c, head, db)
	if err != nil {
		return Base{Ref: c.Base, ParentBranch: c.ParentBranch}
	}
	return b
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
	return Measure(ctx, repo, c, db, head).Ref
}

// MeasuredBase is the commit `MeasureBase` measures from, rather than the name it prints it under.
//
// The two differ whenever the base is a branch, and the difference is the reason both exist. A ref is the
// right answer for a reader and for git: `origin/main` says what the child is measured against, and it
// stays the fresh copy. A commit is the right answer for anything that has to mean the same thing later —
// a review marker, whose base may be a branch that is deleted before the next clone arrives — and the
// commit a `base...head` diff starts at is the merge base of the two, which is also what a derived base
// already names.
//
// It answers "" when git cannot name the commit, which is the caller's cue to record nothing. An absent
// record reads as "this was not said"; a wrong one reads as a comparison (PRD §21).
func MeasuredBase(ctx context.Context, repo *git.Repo, c Changeset, db DefaultBranchRef, head string) string {
	ref := MeasureBase(ctx, repo, c, db, head)
	if ref == "" {
		return ""
	}
	if head == "" {
		head, _ = repo.Head(ctx)
	}
	if head == "" {
		return ""
	}
	if mb, err := repo.MergeBase(ctx, ref, head); err == nil && mb != "" {
		return mb
	}
	// No merge base to take: an unrelated or unresolvable base. The ref may still name a commit — a
	// derived base does — and a commit is a truthful answer where a branch name would not be.
	if commit, err := repo.RevParse(ctx, ref+"^{commit}"); err == nil {
		return commit
	}
	return ""
}

// Measurement is what a review submission records about the diff it reviewed, written beside
// `Review-Head`. It is three ends of one measurement, and one call measures all three so that the two
// surfaces that submit a review — `review submit` and the review screen — cannot record different things
// about the same head:
//
//   - ParentHead answers the movement question: has the branch this changeset is stacked on moved since
//     the approval? It is the parent's *branch* tip, which is what a reader of `status` is shown, and it
//     stops being readable the moment that branch is deleted.
//   - BaseHead answers the attribution question: which side moved, and so what the author should be told
//     to do about it. It is the commit the diff started at, which the deleted branch cannot name.
//   - DiffID answers the content question: is the diff under test the diff that was approved? One
//     comparison, rather than an inference from how far the world has moved since (PRD §21).
//
// Each field is written only when it could be measured, and an unmeasured one reads as "this was not
// said" rather than as a difference — the direction that does not refuse an approval nobody moved.
type Measurement struct {
	ParentHead string
	BaseHead   string
	DiffID     string
}

// MeasureSubmission measures what a review of `c` at `head` should record. Pass head as "" to have it
// read from the repository, which is what the review screen does and what a caller that already holds a
// head should not pay for twice.
//
// It does not fail. A submission is a reviewer's verdict, and a digest this clone could not compute is
// not a reason to refuse to record that verdict: the marker simply carries no `Review-Diff-Id`, which is
// the same readable absence as an approval written before the trailer existed. Every reading of the
// value then falls back to the comparison it can still make.
func MeasureSubmission(ctx context.Context, repo *git.Repo, c Changeset, db DefaultBranchRef, head string) Measurement {
	var m Measurement
	if head == "" {
		head, _ = repo.Head(ctx)
	}
	// An unreadable parent is recorded as no parent, which is what a submission on a branch whose parent
	// is gone already looks like: the absence is honest, and the reader can see it.
	if parent, err := ParentOf(ctx, repo, c, db); err == nil {
		m.ParentHead = parent.Tip
	}
	m.BaseHead = MeasuredBase(ctx, repo, c, db, head)
	if m.BaseHead == "" || head == "" {
		return m
	}
	// The review record is excluded, this changeset's and every ancestor's: a reply to a review thread
	// would otherwise change the digest of the approval it is written into, and the record a landing moves
	// would otherwise read as content the branch deleted. `MeasureContribution` excludes the same paths, and
	// the two must agree — a submission measured one way and a check measured another refuses every approval
	// ever written.
	identity, err := repo.DiffIdentity(ctx, m.BaseHead, head, DigestExclusions(ctx, repo, c, db, head)...)
	if err != nil || identity.Raw == "" {
		return m
	}
	m.DiffID = model.FormatDiffID(recordedIdentity(identity))
	return m
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
