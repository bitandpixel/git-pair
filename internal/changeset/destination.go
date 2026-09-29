package changeset

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"gitpair/internal/git"
)

// Destination is the branch a changeset's work lands on — the branch a merge targets. It is not the
// measurement base, and for a stack whose parent has landed the two are different answers on purpose:
// the base is the parent's landing commit, which is a commit and cannot be merged into, while the
// destination is the branch the parent's own work was measured against.
type Destination struct {
	// Ref is the branch to merge into, as the caller named it or as git keeps it. Empty means this
	// repository cannot name a destination, which is a different fact from naming the wrong one.
	Ref string
	// Why names where the answer came from, for the sentence that reports it: "base" for this changeset's
	// own `base:`/`parent:`, "parent" for the branch a landed parent landed on, "base-landed" for the same
	// answer reached by refusing this changeset's own base because the destination already carries the
	// changeset it names, and "default" for the integration branch.
	Why string
	// Overrode is the authored `base:` this answer refused, when it refused one: a base naming a changeset
	// the destination already carries, or naming nothing this clone can resolve. Empty when the authored
	// base answered for itself, and when the base was derived rather than written - a derived base was
	// never a claim about where the work is going, so refusing it overrides nobody.
	Overrode string
	// Via lists the parent changesets the walk crossed, nearest first, so a report can name the chain
	// it walked rather than only the last hop.
	Via []string
	// Unreachable is the base the walk ended on when it does not resolve here — a parent's recorded base
	// that has since been tidied away, or this changeset's own. It is reported rather than quietly replaced: the caller still
	// gets a destination (`Ref`, the default branch), and a reader gets to see that the answer came
	// from a fallback rather than from the stack.
	Unreachable string
}

// destinationWalkLimit bounds the parent walk. `CHANGESET.yaml` is committed content, and a
// `parent-changeset:` edited into a loop has to stop the walk rather than hang the command — the same
// reason `status`'s stack walk is bounded.
const destinationWalkLimit = 16

// DestinationFor resolves where a changeset's work lands.
//
// The changeset's own base answers this in the ordinary case, and stops answering it in two others. A base
// the resolver derived from the destination (`BaseDerived`) is a measurement point — the run the child shares
// with the integration branch — and a commit is not a destination. A base that names nothing this clone can
// resolve is the other: a branch
// tidied away, or a value written by the durable-ref layout this repository no longer keeps.
//
// Both cases ask the same question one level up, and the answer comes out of the integration branch: a
// landed parent's `changesets/<id>/CHANGESET.yaml` is in the destination's tree, in either spelling, and the
// base recorded there is the branch the parent said it was going to. Walking it means a stack keeps one
// destination rule whether its parent landed on trunk, on a release branch, or on a branch that has since
// been tidied away — and it needs no ref of git-pair's own to do it.
//
// "Said it was going to" is a limit worth stating: a parent landed somewhere other than its own base makes
// this answer wrong, and it is wrong in the direction the write gate catches. Guessing the branch from which
// history contains the landing would be a second, weaker reading of the same fact.
//
// Nothing is invented: a walk that cannot resolve an answer falls back to the default branch and says so in
// `Why`, and a destination that cannot be named at all leaves `Ref` empty — "this clone cannot name a
// destination" is a fact a caller has to be able to tell apart from a guess.
func DestinationFor(ctx context.Context, repo *git.Repo, c Changeset, db DefaultBranchRef) (Destination, error) {
	return DestinationForReads(ctx, repo, c, db, nil)
}

// DestinationForReads is DestinationFor with the memo a command already carries, so a surface that walked the
// chain to describe it does not walk it again to answer where the work lands. Pass nil for no memo.
func DestinationForReads(ctx context.Context, repo *git.Repo, c Changeset, db DefaultBranchRef, reads *Reads) (Destination, error) {
	out := Destination{}
	base, parentChangeset := c.Base, c.ParentChangeset
	if c.BaseDerived {
		// A derived base is where the child's own work starts, not where the work is going. Ask the parent.
		base = ""
	}
	// ownBaseRefused names the authored base this resolver overrode, empty when it did not. It is kept
	// because the field it overrode is committed content a person wrote, and because the answer a reader
	// sees has to say which rule produced it.
	ownBaseRefused := ""
	// One listing of the destination answers "is this a landed changeset" for every hop, including the
	// first; asking it per hop made the walk's cost grow with the depth of the stack rather than with the
	// repository.
	var landed []string
	if db.Ref != "" {
		var err error
		if landed, err = reads.LandedIDs(ctx, repo, db.Ref); err != nil {
			return out, err
		}
	}
	seen := map[string]bool{}
	for hops := 0; hops < destinationWalkLimit; hops++ {
		if base != "" {
			why := "base"
			// Both proofs are asked of every hop, including the first: proving only the walked ones left the
			// authored value returned unchecked, which is the trap - a `base:` naming a branch that had since
			// merged was handed back as a destination, and the sentence built from it told somebody to merge
			// into finished work.
			//
			// The tree fact is asked before the ref exists, at every hop. An authored base may name the
			// changeset by its id, which is a spelling that resolves to no ref at all, and a walked base that
			// is landed has nothing to prove: its own record answers the question. Landedness also wins over
			// the branch still being there (PRD §21), which is the tie-break the plan asked for - the answer
			// comes from the destination's history, so two clones that fetched at different times agree,
			// where "ref existence wins" would let a clone's fetch decide.
			if db.Ref != "" {
				if id, ok := landedMatch(base, landed); ok && !seen[id] {
					if hops == 0 {
						ownBaseRefused = base
					}
					// The base clears and the id goes to the walk below, which reads that changeset's own
					// record out of the destination. Keeping the id in `base` would send it to the existence
					// proof, where a changeset id resolves to nothing and reads as a branch that went away.
					base, parentChangeset = "", id
					continue
				}
			}
			if _, err := repo.RevParse(ctx, base); err != nil {
				if !errors.Is(err, git.ErrUnknownRevision) {
					return out, err
				}
				out.Unreachable = base
				if hops == 0 && !c.BaseDerived {
					// Only the authored value counts as an override: a derived base is a measurement point
					// rather than a claim about where the work is going, and a parent's recorded base that went
					// away is `Unreachable`, which says whose field it was.
					out.Overrode = base
				}
				break
			}
			if hops > 0 {
				why = "parent"
				if ownBaseRefused != "" {
					// The walk went up at all because this changeset's own base had landed. Saying "parent"
					// would be true and less useful than saying why.
					why = "base-landed"
				}
			}
			out.Ref, out.Why, out.Overrode = base, why, ownBaseRefused
			return out, nil
		}
		// Nothing usable to answer with. The parent's own record, read from the integration branch, is
		// the next place the destination is written down.
		if parentChangeset == "" || db.Ref == "" || seen[parentChangeset] {
			break
		}
		seen[parentChangeset] = true
		out.Via = append(out.Via, parentChangeset)
		// The whole of the parent's stack, not just its base: the answer above it depends on whether the
		// parent was itself stacked on something that has since landed.
		stack, err := reads.StackAt(ctx, repo, db.Ref, parentChangeset)
		if errors.Is(err, git.ErrUnknownPath) {
			// The destination carries no directory for that id, so there is no base to read: the parent
			// landed as content alone, or the id names nothing.
			break
		}
		if err != nil {
			return out, err
		}
		base, parentChangeset = stack.Base, stack.ParentChangeset
	}
	if db.Ref != "" {
		out.Ref, out.Why = db.Ref, "default"
	} else {
		out.Ref, out.Why = "", ""
	}
	return out, nil
}

// landedMatch answers whether a base names a changeset the destination already carries, written either as
// the changeset id or as the branch that carried it. The id is what a caller prints: `SlugFromBranch` is
// many-to-one, so `feat/tidy` and `feat-tidy` collapse, and echoing the spelling back would present a
// normalised name as if it were the author's own.
func landedMatch(base string, ids []string) (string, bool) {
	if slices.Contains(ids, base) {
		return base, true
	}
	if slug, err := SlugFromBranch(base); err == nil && slices.Contains(ids, slug) {
		return slug, true
	}
	return "", false
}

// String is the form a test failure prints: the answer and the reason for it together, because
// "main" tells a reader nothing about which rule produced it.
func (d Destination) String() string {
	return fmt.Sprintf("%s (%s)", d.Ref, d.Why)
}
