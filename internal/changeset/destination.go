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
	// changeset's own `base:` (or the older `parent:`), "parent" for the branch a landed parent landed
	// on, "default" for the integration branch.
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

// DestinationFor resolves where a changeset's work lands, from the record of the changeset this clone is
// standing on.
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
	return destinationFor(ctx, repo, c, db, false)
}

// DestinationOfParent resolves where a parent's work lands, from a record read out of somebody else's tree —
// a parent's `changesets/<id>/CHANGESET.yaml` in this changeset's head or in the integration branch, rather
// than the record of the changeset this clone is standing on.
//
// The distinction is the one the walk already makes between its first hop and every hop after it, and it is
// about who proved the value. A changeset's own `base:` names a branch this clone resolved in order to
// compute the state it is reporting, so believing it costs nothing. A parent's `base:` is read out of a tree
// that may well be a landed one, and a branch keeps its name after the work it carried has been merged: the
// value that answered "where is this going" when the parent was cut is the value that names finished work
// once the parent has landed. Asking the first hop to prove itself — resolve the name, and refuse it when the
// integration branch already carries it as a landed changeset — is what makes a stack whose lower changesets
// have all been merged end at the branch holding those landings instead of at a stale branch beneath them.
//
// The answer is the same shape as `DestinationFor`'s, including `Why`: with the first hop proved, it is
// reported as "parent" rather than "base", because it came from a parent's record either way.
func DestinationOfParent(ctx context.Context, repo *git.Repo, c Changeset, db DefaultBranchRef) (Destination, error) {
	return destinationFor(ctx, repo, c, db, true)
}

// destinationFor is the walk both entry points share. proveBase says whether the value in `c.Base` was
// resolved by the caller (`DestinationFor`, standing on its own changeset) or read out of another
// changeset's tree (`DestinationOfParent`), and so whether the first hop has to prove itself.
func destinationFor(ctx context.Context, repo *git.Repo, c Changeset, db DefaultBranchRef, proveBase bool) (Destination, error) {
	out := Destination{}
	base, baseChangeset := c.Base, c.BaseChangeset
	if c.BaseDerived {
		// A derived base is where the child's own work starts, not where the work is going. Ask the parent.
		base = ""
	}
	seen := map[string]bool{}
	// The destination's namespace is asked once for the walk rather than once per hop. Every hop asks the same
	// question of the same branch — does this branch already carry that changeset, under either spelling —
	// and the answer cannot change while the walk is running, so a walk four hops deep would otherwise list
	// the same three trees four times. `LandedIDs` is a memoized read in a normal run and a counted one in
	// the chain cost test, which is where the difference between the two shapes becomes visible.
	var landedIDs []string
	var namespaceRead bool
	landedAt := func() ([]string, error) {
		if !namespaceRead {
			ids, err := LandedIDs(ctx, repo, db.Ref)
			if err != nil {
				return nil, err
			}
			landedIDs, namespaceRead = ids, true
		}
		return landedIDs, nil
	}
	for hops := 0; hops < destinationWalkLimit; hops++ {
		if base != "" {
			why := "base"
			if hops > 0 || proveBase {
				// Only the values the caller did not resolve itself need proving: a changeset measured
				// against a base this clone cannot resolve has already failed every command that derives its
				// state, so the direct answer is proven by the caller having got this far. A value read out of
				// another changeset's tree — walked into, or handed in by `DestinationOfParent` — may name a
				// branch that has since been tidied away.
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
			if (hops > 0 || proveBase) && db.Ref != "" {
				ids, err := landedAt()
				if err != nil {
					return out, err
				}
				// The recorded id is asked first, and the branch only beside it: `base-changeset:` names the
				// changeset, which is what the destination files, while `base:` names the branch that carried it.
				// The loop guard belongs to the walk below, which marks the id before it reads the record. Marking
				// it here would make the walk refuse the very hop this rule just decided to take.
				if id, landed := landedID(ids, baseChangeset, base); landed && !seen[id] {
					// The record is filed under the id, so the id is what the next hop has to read it by. Carrying
					// the branch name over would ask the destination for a directory it does not have, and the walk
					// would fall back to the default branch with a reason that names none of what it crossed.
					base, baseChangeset = "", id
					continue
				}
			}
			out.Ref, out.Why = base, why
			return out, nil
		}
		// Nothing usable to answer with. The parent's own record, read from the integration branch, is
		// the next place the destination is written down.
		if baseChangeset == "" || db.Ref == "" || seen[baseChangeset] {
			break
		}
		seen[baseChangeset] = true
		out.Via = append(out.Via, baseChangeset)
		// The whole of the parent's stack, not just its base: the answer above it depends on whether the
		// parent was itself stacked on something that has since landed.
		stack, err := StackAt(ctx, repo, db.Ref, baseChangeset)
		if errors.Is(err, git.ErrUnknownPath) {
			// The destination carries no directory for that id, so there is no base to read: the parent
			// landed as content alone, or the id names nothing.
			break
		}
		if err != nil {
			return out, err
		}
		base, baseChangeset = stack.Base, stack.BaseChangeset
	}
	if db.Ref != "" {
		out.Ref, out.Why = db.Ref, "default"
	} else {
		out.Ref, out.Why = "", ""
	}
	return out, nil
}

// landedID reports which of the names a parent's record carries — if any — is a changeset the integration
// branch holds, and under which of its own records.
//
// The record carries two halves, and the order they are asked in follows what each can answer. `base-changeset:`
// names the changeset below, which is what the destination files, so it still answers once the branch under the
// work is gone; `base:` keeps naming the branch, which is the state in which a reader that trusted it names
// finished work as the destination. The branch is asked as well, and its slug beside it, because a record can
// carry the branch alone: `parent:` and `parent-changeset:` are read from files written before that pair
// existed, and such a file can name a branch and no changeset. The match is reported rather than assumed,
// because the next hop opens `changesets/<id>/` and a branch name there asks the destination for a directory it
// does not have — which is how a walk that compared only the branch name came to offer a stale parent branch as
// where a child's work should land, in the queue and on the command a pipeline reads.
func landedID(ids []string, names ...string) (string, bool) {
	for _, name := range names {
		if name == "" {
			continue
		}
		if slices.Contains(ids, name) {
			return name, true
		}
		if slug, err := SlugFromBranch(name); err == nil && slices.Contains(ids, slug) {
			return slug, true
		}
	}
	return "", false
}

// String is the form a test failure prints: the answer and the reason for it together, because
// "main" tells a reader nothing about which rule produced it.
func (d Destination) String() string {
	return fmt.Sprintf("%s (%s)", d.Ref, d.Why)
}
