package cli

import (
	"context"
	"fmt"
	"strings"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
)

// The invariant every offered branch satisfies: it carries one changeset, plus the changesets it is stacked on
// (PRD §4). Three commands enforce it and they share everything but the sentence that opens the message - `init`
// refuses before it writes, `change ready` refuses before it offers, and `check` reports it as a reason the merge
// is gated, which is what stops the CI job from landing a second changeset that nobody reviewed.
//
// The condition is read from the candidate set and never from the selection. `Resolution.Selected` is nil exactly
// when a branch most clearly carries more than one changeset, so a rule written over it goes silent on the shape it
// exists to catch - which is how the warning this replaced was written.

// branchShape reads the shape of one revision: the changeset directories it carries that the destination does not,
// and what those files record about each other.
func branchShape(ctx context.Context, repo *git.Repo, rev string, db changeset.DefaultBranchRef) (changeset.BranchShape, error) {
	res, err := changeset.Resolve(ctx, repo, rev, db)
	if err != nil {
		return changeset.BranchShape{}, err
	}
	return changeset.CheckBranchShape(ctx, repo, rev, res)
}

// shapeFinding is the part of the message that states what was found. It names every id, because the person
// reading it has to work out which of the two is theirs before choosing a way out.
func shapeFinding(label string, shape changeset.BranchShape) string {
	return fmt.Sprintf("%s carries %d changesets that no record ties together: %s",
		label, len(shape.Members), strings.Join(shape.Members, ", "))
}

// shapeWaysOut is what a reader can do about it. Every command named here exists in this build. The two that would
// name the shape directly - `change combine`, for one piece of work spread over two directories, and `change stack`,
// for two directories that are one stack - are the changeset after this one, and printing a command that does not
// answer yet is worse than printing the fix that does.
func shapeWaysOut(strays []string) string {
	return "They arrive when work from another branch comes onto this one: a merge or a pull of a shared branch, a\n" +
		"cherry-pick, a squash merge, or a branch cut from a branch that already carried both. Make one of these\n" +
		"true:\n" +
		"  - this branch carries one changeset and the others belong elsewhere, so take them out of it,\n" +
		"    where <ref> is the branch or commit this one was cut from:\n" +
		"      git restore --source=<ref> -- " + changeset.Root + "/" + strays[0] + "\n" +
		"  - the changesets are a stack, so record it on each child, which is what `init` records when the base\n" +
		"    names the branch that carries the parent:\n" +
		"      git pair init --base <parent-branch> --set-base\n" +
		"  - the other may have landed since this branch was cut, which makes it not a second changeset at\n" +
		"    all, so fetch and ask again:\n" +
		"      git fetch\n"
}

// refuseSecondChangeset is `init`'s half. It runs after the stack has been worked out and before anything is
// written, so the record the flags just produced is part of the set being judged: creating a changeset stacked on
// the one the branch carries is the shape a stack is made of, and creating one that ties to nothing is the shape
// this refuses.
func (a *app) refuseSecondChangeset(ctx context.Context, repo *git.Repo, branch, id, parent, baseBranch string) error {
	db := a.destination(ctx, repo)
	if db.Ref == "" {
		// Without a destination there is no way to tell a landed changeset from an unlanded one, and this is a
		// question about unlanded directories. Blaming the work for what the clone cannot name is the wrong
		// trade, and every other guard still applies.
		return nil
	}
	res, err := changeset.Resolve(ctx, repo, "HEAD", db)
	if err != nil {
		return err
	}
	edge := changeset.StackEdge{ID: id, Parent: parent, Recorded: parent != ""}
	if parent == "" {
		// The authored base is kept as a branch name so the rule can read it the same way it reads a file
		// written before ids were recorded: `--base booking` says what this work is stacked on, and the
		// refusal has no business punishing an author for an id that is not spelled like its branch.
		edge.BaseBranch = strings.TrimPrefix(baseBranch, "refs/heads/")
	}
	edges := append(changeset.EdgesOf(res), edge)
	shape, err := changeset.CheckEdges(ctx, repo, "HEAD", edges)
	if err != nil {
		return err
	}
	if shape.Offerable() {
		return nil
	}
	a.warn("%s\n\n%s", shapeFinding("changeset "+id, shape), shapeWaysOut(shape.Strays))
	// Bad arguments rather than repository state: the author can pass --base, or create the changeset on a
	// branch of its own, and neither means the repository is wrong.
	return &usageError{fmt.Errorf("cannot create changeset %s on %s: %s", id, branch, strings.ToLower(shapeFinding("the branch", shape)))}
}

// refuseBranchShape is `change ready`'s half, and the one that matters most: propagation is why the rule is here
// and not only at `init`. A branch that acquired the shape from a merge, a pull, or a parent that carried two is
// refused on its way to the queue, whatever the history looks like.
func (a *app) refuseBranchShape(ctx context.Context, s *session) error {
	if s.trunk.Ref == "" {
		return nil
	}
	shape, err := branchShape(ctx, s.repo, s.head, s.trunk)
	if err != nil {
		return err
	}
	if shape.Offerable() {
		return nil
	}
	a.warn("%s\n\n%s", shapeFinding(displayRef(s.cs.Branch), shape), shapeWaysOut(shape.Strays))
	// Repository state, not bad arguments: exit 1, the same way the dirty-tree and survival refusals do.
	return fmt.Errorf("cannot mark %s ready: %s", s.cs.Slug, strings.ToLower(shapeFinding("this branch", shape)))
}

// shapeReason is `check`'s half, in the one-line form every reason takes. It gates the merge, which is the point:
// the second changeset reaches the destination with no approval of its own, and no amount of review on the first
// one covers it.
func shapeReason(slug string, shape changeset.BranchShape) string {
	return fmt.Sprintf("changeset %s: %s, so a second changeset reaches the destination with no approval of its "+
		"own; a branch carries one changeset plus the ones it is stacked on", slug, strings.ToLower(shapeFinding("this branch", shape)))
}
