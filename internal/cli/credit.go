package cli

import (
	"context"
	"errors"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/lifecycle"
	"gitpair/internal/model"
)

// creditRefs names the refs the gate may credit for content a branch gained after its marker, in the
// order they are asked.
//
// The rule the list answers is the one the credit exists for: content the reviewer of this changeset had
// no reason to read is not content the author added. That is the integration branch — the reading
// `check` has always made — and a stack's parent branch, which the changeset's own `base:` records and
// its diff is measured from. A child that merges its parent in has moved the ground under its own work,
// exactly as a branch that merges trunk in does, and the content it took in is content the parent's own
// reviewer read. Refusing it was the one place where the two were read apart.
//
// Two things keep the parent citation from becoming a way in. It is cited at the commit its newest
// approval spoke about and not at its branch tip, so whatever the author puts on the parent afterwards —
// their own work, or a copy of this child's post-approval commits — is credited by nobody: the credit
// vouches for content a reviewer saw, not for what a branch happens to hold today. And a parent with no
// approval of its own is not cited at all, which is the same reading `parentLanded` and `parentAbandoned`
// take: nothing is claimed from a branch name. `base-changeset:` is what makes the parent's changeset
// findable, and without it there is no record to ask who reviewed this content.
//
// A landed parent is deliberately absent, and the reason is the same one the destination walk gives for
// refusing to name a landed parent's branch as a child's destination: a branch keeps its name after the
// work it carried was merged, so the name is not evidence about content any more. Once the parent has
// landed, the destination holds its work and is cited for it. Until then the child cannot be declared
// (`unlandedParentReason`), which is what makes citing the branch safe: nothing reaches the destination
// on a child's verdict alone.
//
// The refs are resolved to commits here rather than passed as names, so the credit is the one this
// decision was taken about and not a ref that moved between the decision and the read.
func creditRefs(ctx context.Context, repo *git.Repo, c changeset.Changeset,
	db changeset.DefaultBranchRef) ([]lifecycle.CreditRef, error) {
	out := []lifecycle.CreditRef{}
	add := func(ref, name string) {
		if ref == "" {
			return
		}
		for _, known := range out {
			if known.Ref == ref {
				return
			}
		}
		out = append(out, lifecycle.CreditRef{Ref: ref, Name: name})
	}
	if db.Ref != "" {
		// Resolved for the same reason the parent is: an approval is about content at a moment, and the
		// branch this clone read is the moment. A destination that does not resolve here credits nothing,
		// which is how it behaved before it could be named beside another ref.
		sha, err := repo.RevParse(ctx, db.Ref)
		if err != nil {
			if !errors.Is(err, git.ErrUnknownRevision) {
				return nil, err
			}
		} else {
			add(sha, displayRef(db.LocalName()))
		}
	}
	if db.Ref == "" {
		// Nothing to rest the citations on. The parent's approval is read out of the range between the
		// destination and the parent's branch, and a clone that cannot name the destination cannot ask it
		// — so this is the reading where content above the marker is simply drift, as it was before a
		// parent could be cited at all.
		return out, nil
	}
	if c.BaseChangeset == "" {
		return out, nil
	}
	parent, err := changeset.ParentOf(ctx, repo, c, db)
	if err != nil {
		return nil, err
	}
	if parent.Branch == "" {
		// Measured against the integration branch, or against a commit that is already in it: there is
		// no other ground to cite, and the list above is the whole answer.
		return out, nil
	}
	if _, err := changeset.LandedChain(ctx, repo, db.Ref, c.BaseChangeset); err == nil {
		// The parent's work is in the destination, so the destination is what vouches for it.
		return out, nil
	} else if !errors.Is(err, changeset.ErrNoChain) {
		return nil, err
	}
	reviewed, err := approvedHead(ctx, repo, c.BaseChangeset, db, parent.Branch)
	if err != nil {
		return nil, err
	}
	add(reviewed, parent.Branch)
	return out, nil
}

// approvedHead resolves the commit the parent changeset's newest approving review spoke about, on that
// parent's own branch, and returns "" where there is no such commit: no approval, an approval that named
// no commit, or a commit this clone does not have.
//
// It is the parent's review and nobody else's. The child's approval covers the child's diff measured from
// the parent, which is precisely the reading that never saw the parent's own files; the only reviewer who
// did is the parent's, so the parent's approval is what has to be here for the content to be citable.
//
// Two consequences follow from asking a commit rather than a branch, and both are the direction of less
// trust rather than more. Content the parent took on after that commit is credited by nobody, which is what
// stops an author from pushing their own work onto the parent and merging it back as somebody else's. And
// the approval is read out of a walk of the parent's own history from its tip, so a parent whose branch was
// rewritten since the approval — a rebase, a force-push, a reset — offers no citable commit at all.
//
// `lifecycle.Summarize` is asked of the parent branch with the destination as its base, which is the
// reading `parentAbandoned` uses for the same question — what happened on the branch below this one.
func approvedHead(ctx context.Context, repo *git.Repo, parentID string,
	db changeset.DefaultBranchRef, branch string) (string, error) {
	if branch == "" {
		return "", nil
	}
	summary, err := lifecycle.Summarize(ctx, repo, parentID, db.Ref, "refs/heads/"+branch)
	if err != nil {
		if errors.Is(err, git.ErrUnknownRevision) {
			return "", nil
		}
		return "", err
	}
	for i := len(summary.Reviews) - 1; i >= 0; i-- {
		r := summary.Reviews[i]
		if r.Outcome != model.OutcomeApprove {
			continue
		}
		if r.ReviewedHead == "" {
			// An approval that names no commit cannot say which content it covers, and guessing from
			// the marker's first parent would be a guess about somebody else's review.
			return "", nil
		}
		sha, err := repo.RevParse(ctx, r.ReviewedHead+"^{commit}")
		if err != nil {
			if errors.Is(err, git.ErrUnknownRevision) {
				return "", nil
			}
			return "", err
		}
		return sha, nil
	}
	return "", nil
}
