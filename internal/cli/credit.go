package cli

import (
	"context"
	"errors"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/lifecycle"
)

// creditRefs names the refs the gate may credit for content a branch gained after its marker, in the
// order they are asked.
//
// The rule the list answers is the one the credit exists for: content the reviewer of this changeset had
// no reason to read is not content the author added. That is the integration branch — the reading
// `check` has always made — and a stack's parent branch, which the changeset's own `base:` records and
// its diff is measured from. A child that merges its parent in has moved the ground under its own work,
// exactly as a branch that merges trunk in does, and the parent's content has its own changeset, its own
// approval and its own gate. Refusing it was the one place where the two were read apart.
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
		add(db.Ref, displayRef(db.LocalName()))
	}
	parent, err := changeset.ParentOf(ctx, repo, c, db)
	if err != nil {
		return nil, err
	}
	if parent.Branch == "" || parent.Tip == "" {
		// Measured against the integration branch, or against a commit that is already in it: there is
		// no other ground to cite, and the list above is the whole answer.
		return out, nil
	}
	if c.BaseChangeset != "" && db.Ref != "" {
		if _, err := changeset.LandedChain(ctx, repo, db.Ref, c.BaseChangeset); err == nil {
			// The parent's work is in the destination, so the destination is what vouches for it.
			return out, nil
		} else if !errors.Is(err, changeset.ErrNoChain) {
			return nil, err
		}
	}
	add(parent.Tip, parent.Branch)
	return out, nil
}
