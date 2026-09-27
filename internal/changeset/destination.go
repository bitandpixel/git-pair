package changeset

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gitpair/internal/git"
	"gitpair/internal/reviewref"
)

// Destination is the branch a changeset's work lands on — the branch a merge targets. It is not the
// measurement base, and for a stack whose parent has landed the two are different answers on purpose:
// the base is the parent's integration ref, which is a commit and cannot be merged into, while the
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
// The changeset's own base answers this in the ordinary case, and stops answering it for a child whose
// parent has landed: the resolver re-points that base at the parent's integration ref (`relinkStacks`),
// which names the commit the parent's work became and no branch at all. A durable ref is not a
// destination — `integration record` refuses one as a `--target` for exactly this reason — so the answer
// has to come from further up the stack.
//
// It comes from the record, because that is where the fact is written: the commit an integration ref
// names carries the parent's `changesets/<id>/CHANGESET.yaml`, and the base recorded there is the branch
// the parent was measured against — the branch the parent *said* it was going to. Walking it means a
// stack keeps one destination rule whether its parent landed on trunk, on a release branch, or on a
// branch that has since been tidied away.
//
// "Said it was going to" is a limit worth stating: a parent landed somewhere other than its own base
// makes this answer wrong, and it is wrong in the direction `integration record` catches — the record
// refuses a landing commit that is not reachable from the destination it derived — and `--target` is the
// named way to say so. Guessing the branch from which refs contain the landing commit would be a second,
// weaker reading of the same fact, and the record already has the stronger one.
//
// Nothing is invented: a walk that cannot resolve an answer falls back to the default branch and says so
// in `Why`, and a base that resolves to nothing at all leaves `Ref` empty — "this clone cannot name a
// destination" is a fact a caller has to be able to tell apart from a guess.
func DestinationFor(ctx context.Context, repo *git.Repo, c Changeset, db DefaultBranchRef) (Destination, error) {
	out := Destination{}
	base, parentChangeset := c.Base, c.ParentChangeset
	seen := map[string]bool{}
	for hops := 0; hops < destinationWalkLimit; hops++ {
		// The relink rule, applied here as well as in the resolver: a stack whose parent has a record is
		// measured against that record, so its destination is whatever its parent's was. Applying it inside
		// the walk rather than relying on the resolver to have done it is what lets the same rule answer for
		// a parent's own recorded stack, read out of a landed tree two hops up.
		if parentChangeset != "" && !underNamespace(base) {
			if _, err := reviewref.ResolveIntegration(ctx, repo, parentChangeset); err == nil {
				base = reviewref.Integration(parentChangeset)
			}
		}
		if base == "" {
			break
		}
		if !underNamespace(base) {
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
			out.Ref, out.Why = base, why
			return out, nil
		}
		id, ok := reviewref.IntegrationID(base)
		if !ok || seen[id] {
			// An archive ref, a retired layout, or a loop: nothing above this says where work goes.
			break
		}
		seen[id] = true
		out.Via = append(out.Via, id)
		commit, err := reviewref.ResolveIntegration(ctx, repo, id)
		if errors.Is(err, reviewref.ErrNotIntegrated) {
			break
		}
		if err != nil {
			return out, err
		}
		// The whole of the parent's stack, not just its base: the answer above it depends on whether the
		// parent was itself stacked on something that has since landed.
		stack, err := StackAt(ctx, repo, commit, id)
		if errors.Is(err, git.ErrUnknownPath) {
			// The landing carried no changeset directory for its own id — a hand-made record, or a
			// record written against a branch that never carried one. There is no base to read.
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

// underNamespace is true for anything under git-pair's durable ref namespace. Only one family in it
// (integrations) says anything about where work lands, and the base is never a plain branch name once it
// is in there at all — so the namespace decides "a durable ref, walk it or refuse it" and
// isIntegrationRef decides which family can be walked.
func underNamespace(ref string) bool {
	return strings.HasPrefix(ref, reviewref.NamespaceRoot+"/")
}

// String is the form a test failure prints: the answer and the reason for it together, because
// "main" tells a reader nothing about which rule produced it.
func (d Destination) String() string {
	return fmt.Sprintf("%s (%s)", d.Ref, d.Why)
}
