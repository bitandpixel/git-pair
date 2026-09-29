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
	// Why names where the answer came from, for the sentence that reports it: "base" for this
	// changeset's own `base:`/`parent:`, "parent" for the branch a landed parent landed on, "default"
	// for the integration branch.
	Why string
	// Via lists the parent changesets the walk crossed, nearest first, so a report can name the chain
	// it walked rather than only the last hop.
	Via []string
	// Unreachable is the base the walk ended on when it does not resolve here — a parent's recorded base
	// that has since been tidied away. It is reported rather than quietly replaced: the caller still
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
// with the integration branch — and a commit is not a destination, the same reason a landing is never
// refuses one as a `--target`. A base that names nothing this clone can resolve is the other: a branch
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
	out := Destination{}
	base, parentChangeset := c.Base, c.ParentChangeset
	if c.BaseDerived {
		// A derived base is where the child's own work starts, not where the work is going. Ask the parent.
		base = ""
	}
	seen := map[string]bool{}
	for hops := 0; hops < destinationWalkLimit; hops++ {
		if base != "" {
			why := "base"
			if hops > 0 {
				// Only the walked values need proving: a changeset measured against a base this clone
				// cannot resolve has already failed every command that derives its state, so the direct
				// answer is proven by the caller having got this far. A parent's recorded base is read
				// out of a landed tree and may name a branch that has since been tidied away.
				if _, err := repo.RevParse(ctx, base); err != nil {
					if !errors.Is(err, git.ErrUnknownRevision) {
						return out, err
					}
					out.Unreachable = base
					break
				}
				why = "parent"
			}
			// A parent's recorded base that names a directory the integration branch carries is a landed
			// changeset, not a destination: the branch outlived the work it carried, which is the same fact
			// that moves a child's measurement base. Keep walking, through that changeset's own record.
			if hops > 0 && db.Ref != "" {
				ids, err := LandedIDs(ctx, repo, db.Ref)
				if err != nil {
					return out, err
				}
				// The loop guard belongs to the walk below, which marks the id before it reads the record. Marking
				// it here would make the walk refuse the very hop this rule just decided to take.
				if slices.Contains(ids, base) && !seen[base] {
					base, parentChangeset = "", base
					continue
				}
			}
			out.Ref, out.Why = base, why
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
		stack, err := StackAt(ctx, repo, db.Ref, parentChangeset)
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

// String is the form a test failure prints: the answer and the reason for it together, because
// "main" tells a reader nothing about which rule produced it.
func (d Destination) String() string {
	return fmt.Sprintf("%s (%s)", d.Ref, d.Why)
}
