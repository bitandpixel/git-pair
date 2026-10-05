package changeset

import (
	"context"
	"errors"

	"gitpair/internal/git"
	"gitpair/internal/model"
)

// Contribution is what a changeset adds above the ground its work sits on, measured from here.
//
// It is the content an approval is a claim about. `status`, `check` and the queue all end up asking whether
// the thing under test is still the thing that was reviewed, and every one of those readings is this
// measurement: the diff between the changeset's work and the commit that work rests on, with the review
// record left out — the record of a review is not part of what the review examined.
type Contribution struct {
	// Base is the commit the diff was measured from, named so a reader can reproduce it and so a refusal
	// can say which side moved.
	Base string
	// Digest is that diff's identity, in the `<version>:<hex>` form `model.FormatDiffID` renders. It is
	// what a recorded `Review-Diff-Id` is compared with.
	Digest string
	// Paths are the files the contribution changes outside the review record. The stack's notes use them
	// to say whether a parent's new commits reach any file this branch touches, which is the difference
	// between "the parent moved" and "the parent moved where it matters".
	Paths []string
	// Measured says the measurement could be taken at all. False means no single commit could be named as
	// the ground — the caller then falls back to a reading that needs less, but does not treat the absence
	// as a content difference (PRD §21).
	Measured bool
}

// stackWalkLimit bounds how far the parent chain is followed when naming the paths a digest leaves out. A
// stack deeper than this is a mistake in the record rather than a stack, and the limit is what keeps a
// `parent:` cycle from spinning forever.
const stackWalkLimit = 32

// DigestExclusions names the paths a contribution digest leaves out: this changeset's directory and every
// ancestor's, in both of their homes.
//
// The review record is excluded because it is not the thing reviewed. Two readings depend on that, and both
// break when the directory is in the digest. A reply in ABOUT.md is a reply to a review, and its presence in
// the contribution would let an author's answer invalidate the approval it answers. And a landing is a
// change to the record before it is a change to the code — `change integrate` moves `changesets/<id>/` to
// `changesets/.landed/<id>/` — so a child measured from a base that carries its parent's record, against a
// head that predates it, reports the parent's review as the child deleting it.
//
// The ancestors are in the list for the same reason the parent is: a child whose work sits on a parent's
// carries the parent's files in its own tree, and the pair cancels in the diff anyway. Listing them costs
// nothing there and saves a false refusal when the child was branched off the integration branch instead and
// never took them in at all.
//
// An ancestor whose record cannot be read is left out of the walk, which loses an exclusion and so loses a
// pass rather than granting one: the direction PRD §21 asks for.
func DigestExclusions(ctx context.Context, repo *git.Repo, c Changeset, db DefaultBranchRef, head string) []string {
	ids := []string{}
	seen := map[string]bool{}
	if c.Slug != "" {
		ids = append(ids, c.Slug)
		seen[c.Slug] = true
	}
	next := c.BaseChangeset
	for hops := 0; next != "" && hops < stackWalkLimit; hops++ {
		if seen[next] {
			break
		}
		seen[next] = true
		ids = append(ids, next)
		stack, err := stackOfAncestor(ctx, repo, db, head, next)
		if err != nil {
			break
		}
		next = stack.BaseChangeset
	}
	out := make([]string, 0, 2*len(ids))
	for _, id := range ids {
		out = append(out, ActiveDirPath(id), LandedDirPath(id))
	}
	return out
}

// stackOfAncestor reads the stack record of an ancestor changeset, from whichever tree still carries it:
// this branch's own head first, because a stack normally carries its parents' directories, and the
// destination second, because a parent that landed left its record there.
func stackOfAncestor(ctx context.Context, repo *git.Repo, db DefaultBranchRef, head, id string) (Stack, error) {
	for _, rev := range []string{head, db.Ref} {
		if rev == "" {
			continue
		}
		stack, err := StackAt(ctx, repo, rev, id)
		if err == nil {
			return stack, nil
		}
		if !errors.Is(err, git.ErrUnknownRevision) && !errors.Is(err, git.ErrUnknownPath) {
			return Stack{}, err
		}
	}
	return Stack{}, git.ErrUnknownPath
}

// MeasureContribution measures what `c` contributes above where its work now sits. Pass head as "" to read
// it from the repository, `parentLanding` as the parent's landing commit when its parent has one — the value
// `changeset.LandedChain` derives from the destination — and `recordedBase` as the commit the approval being
// relied on recorded measuring from, which is its `Review-Base-Head` trailer.
//
// The ground is the hard part, and getting it wrong is not a missed refinement: measured from the parent's
// landing commit, a squashed parent's work has no ancestry in the destination to cancel it and appears in the
// child's patch a second time; measured from the destination's tip, everything trunk gained since reads as
// the child's reverse changes, and an approval dies on somebody else's merge in an unrelated file.
//
// So the ground is chosen from candidates rather than named, and the candidate that wins is the newest one
// that is still an ancestor of the head:
//
//   - the commit the approval recorded measuring from, which is the ground the reviewer actually read
//     across, and the only one that survives a parent squashed into the destination;
//   - the merge base of the recorded base with the head, which is the fork point while the parent is still
//     a branch — the parent taking commits of its own does not move where the child's work starts;
//   - the merge base of the parent's landing with the head, which is the parent's own tip after a merge
//     landing and the landing itself after a rebase onto it;
//   - the merge base of the destination with the head, which is the newest of the four once the child has
//     taken trunk in, and the reason merging the destination into a branch no longer costs its approval.
//
// Each candidate enters through a merge base with the head, never as an endpoint of the diff directly, so a
// commit that is not in the head's history can never be chosen. When no candidate contains all the others
// — a branch that merged two grounds, so the candidates are incomparable — there is no single ground to
// measure above, and the answer is not measured. The caller falls back to a reading that compares bases
// instead of comparing content, which is what this question asked before it could be asked directly.
func MeasureContribution(ctx context.Context, repo *git.Repo, c Changeset, db DefaultBranchRef,
	head, parentLanding, recordedBase string) Contribution {
	if head == "" {
		head, _ = repo.Head(ctx)
	}
	if head == "" {
		return Contribution{}
	}
	candidates := []string{recordedBase, MeasuredBase(ctx, repo, c, db, head)}
	for _, ground := range []string{parentLanding, db.Ref} {
		if ground == "" {
			continue
		}
		if mb, err := repo.MergeBase(ctx, ground, head); err == nil {
			candidates = append(candidates, mb)
		}
	}
	base, ok := newestAncestor(ctx, repo, head, candidates)
	if !ok {
		return Contribution{}
	}
	dirs := DigestExclusions(ctx, repo, c, db, head)
	digest, err := repo.DiffRawDigest(ctx, base, head, dirs...)
	if err != nil || digest == "" {
		return Contribution{}
	}
	// The file list is a convenience for the notes, not part of the identity: a note that cannot name the
	// overlapping files still says the parent moved, so a failure here costs a detail rather than the answer.
	paths, err := repo.PathsChangedOutside(ctx, base, head, dirs...)
	if err != nil {
		paths = nil
	}
	return Contribution{Base: base, Digest: model.FormatDiffID(digest), Paths: paths, Measured: true}
}

// newestAncestor picks the candidate that already contains all the others, which is the commit whose tree
// accounts for the most ground and therefore the one the diff above it is smallest and cleanest.
//
// Duplicate candidates are the common case rather than an edge — one commit usually answers three of the four
// questions at once — so they are collapsed before any comparison, which keeps the ancestry checks at about
// one per distinct candidate.
//
// False says no candidate contains all the others, which is reported rather than resolved by preference:
// picking one of two incomparable grounds would decide the question by which candidate was tried first.
func newestAncestor(ctx context.Context, repo *git.Repo, head string, candidates []string) (string, bool) {
	distinct := make([]string, 0, len(candidates))
	seen := map[string]bool{}
	for _, cand := range candidates {
		if cand == "" || seen[cand] {
			continue
		}
		seen[cand] = true
		distinct = append(distinct, cand)
	}
	if len(distinct) == 0 {
		return "", false
	}
	for _, cand := range distinct {
		containsAll := true
		for _, other := range distinct {
			if other == cand {
				continue
			}
			ancestor, err := repo.IsAncestor(ctx, other, cand)
			if err != nil || !ancestor {
				containsAll = false
				break
			}
		}
		if containsAll {
			return cand, true
		}
	}
	return "", false
}
