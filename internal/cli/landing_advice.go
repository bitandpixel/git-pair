package cli

import (
	"context"
	"fmt"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
)

// The landing contract (PRD §29) is one sentence, and four commands print it: `status`, `check`,
// `change wait` and `review submit`. For work measured straight against the integration branch the sentence
// was the base and a merge, and it has never been wrong. For a stacked child it was wrong in two ways at
// once, both of them the base's fault:
//
//   - the base names the parent's *branch*, and that is not where the child's work is going. A child lands
//     where its parent landed, which is the rule `git pair change integrate` walks the records to find —
//     and which `change integrate` then refused to act on, because the sentence the other four were printing
//     asked for a merge onto a branch review could still rewrite;
//   - the child cannot be merged at all until the parent has been. So the next step is the parent's, and
//     saying "merge" without saying "not yet" sends an author to a gate that passes and a merge nobody owns.
//
// So the sentence is built from two readings rather than from the base alone: where the work is going, and
// whether something below it has to land first. Both are read the cheap way, because four surfaces print one
// sentence and `change wait` polls: the status cost test (`TestStatusStackChainCostsABoundedReadPerStep`)
// bounds what a screen may add per ancestor, and this adds none.

// landingContract is what the landing sentence needs beyond the state name: the branch a merge actually
// targets, and the parent that has to reach the destination first.
type landingContract struct {
	// Destination is the branch the merge targets. Empty falls back to the changeset's own base, which is
	// the whole story for work measured against the integration branch and nothing at all for a child.
	Destination string
	// DestinationNote names where the answer came from — " (where alpha landed)" for a child following its
	// parent onto a branch the child's own record does not name — and is empty whenever the base answered,
	// including on the waiting sentence, where the parent has not landed anywhere to name yet.
	DestinationNote string
	// WaitingOn names the parent that has to land before this changeset can: its changeset id when the stack
	// recorded one, its branch when it did not. Empty when nothing below is unlanded.
	WaitingOn string
	// WaitingTarget is where the parent's own work says it is going, which is where this changeset follows
	// it. Empty falls back to Destination.
	WaitingTarget string
}

// landingContractFor reads the two facts a landing sentence states, from the repository rather than from the
// changeset's own file.
//
// Every read here is advice, and advice does not fail a command: a git error falls back to the sentence these
// commands printed before the stack was asked about. The readings that *refuse* — `check`'s gate and `change
// integrate`'s refusal — ask the same questions of the same tree and do return the error, because a merge that
// goes ahead on an unreadable destination is the expensive direction.
func landingContractFor(ctx context.Context, repo *git.Repo, s *session) landingContract {
	below, err := stackBelow(ctx, repo, s.cs, s.trunk)
	if err != nil {
		below = stackUnder{}
	}
	if !below.stacked() {
		// Work measured against the integration branch: the base is the destination, and there is nothing
		// below it to wait for. This is the sentence exactly as it has always read.
		return landingContract{Destination: s.cs.Base}
	}
	// Ask the parent where its own work is going rather than walking the child's chain: the child follows the
	// parent there, so it is the same answer, and the walk is what would make this cost grow with the stack.
	target := below.destination(ctx, repo, s.trunk)
	if target == "" {
		// No record answered, and this clone may not even name the integration branch to fall back on. Say
		// the base rather than nothing: `landingNextAction` turns an empty destination into wording.
		target = s.trunk.Ref
		if target == "" {
			target = s.cs.Base
		}
	}
	out := landingContract{Destination: target, WaitingTarget: target}
	if below.Landed == "" {
		out.WaitingOn = below.name()
		return out
	}
	// The parent has landed, so the merge is the child's own step, and the parenthetical says where the
	// branch name came from rather than leaving one the child never recorded.
	out.DestinationNote = destinationNote(changeset.Destination{
		Ref: target,
		Why: "parent",
		Via: []string{below.name()},
	})
	return out
}

// stackUnder is the stack under a changeset as far as the landing sentence needs it: the parent it sits on,
// and whether the destination carries the parent's record yet.
//
// It is the same rule `git pair change integrate` refuses on — see `unlandedParentReason`, whose condition on
// `parentStatus.Landed` this reproduces — read the cheap way, the way the queue reads the same fact
// (`landedParentNote`): one parent read and one landing lookup, and nothing that walks the parent's markers or
// its chain. What is left out is the verdict, and a sentence about where work is going does not need one; the
// gate and the declaration ask that of `parentSinceApproval`.
type stackUnder struct {
	// Branch is the parent branch as this changeset's record names it, empty for work measured against the
	// integration branch.
	Branch string
	// Changeset is the parent's changeset id, empty when the record named a branch and no changeset — the
	// shape `init` no longer writes and a hand-edited file still can.
	Changeset string
	// Landed is the commit the destination carries the parent's directory at, empty when it does not carry
	// it. Same fact and same derivation as `parentStatus.Landed`.
	Landed string
}

func (su stackUnder) stacked() bool { return su.Branch != "" }

// name is how the sentence calls the parent: by changeset when the stack knows it, by branch when it does
// not, which is the preference `parentStatus.parentName` makes so one reader is not told two names for one
// piece of work.
func (su stackUnder) name() string {
	if su.Changeset != "" {
		return su.Changeset
	}
	return su.Branch
}

// destination is where the parent's own work says it is going, which the child's merge names as its own
// target, because a child lands where its parent lands.
//
// Two readings answer that, and they are asked in order of how well they are evidenced.
//
// Where the destination already carries the parent's directory, the destination itself is the answer
// (`Landed`, read from that tree by `stackBelow`). Nothing is read from the record: a record says where the
// parent *said* it was going, and a branch keeps its name after the work it carried has been merged, so a
// stack whose lower levels have landed one after the other leaves every record below naming a finished
// changeset's stale branch. That is the reading that sent an author to merge into work already in.
//
// Where the parent has not landed, the record is the only statement of intent there is, and it is read from
// whichever tree still carries it: this changeset's head, which normally carries its whole stack, then the
// destination, which carries it once the parent has landed and been tidied. The parent's `base:` is then
// asked as a walked value rather than a direct one (`DestinationOfParent`): nothing here resolved it, and the
// branch it names may have been tidied away or may name a changeset that has since landed. A record that
// cannot be read costs the detail and not the sentence: the caller falls back to the integration branch.
func (su stackUnder) destination(ctx context.Context, repo *git.Repo, db changeset.DefaultBranchRef) string {
	if su.Changeset == "" {
		return db.Ref
	}
	if su.Landed != "" {
		// The destination carries the parent's directory — that is what `Landed` records, read from that tree
		// by `stackBelow` — so the child's merge targets the destination, and the record is not consulted.
		return db.Ref
	}
	for _, rev := range []string{"HEAD", db.Ref} {
		if rev == "" {
			continue
		}
		st, err := changeset.StackAt(ctx, repo, rev, su.Changeset)
		if err != nil {
			continue
		}
		parent := changeset.Changeset{
			Slug:          su.Changeset,
			Base:          st.Base,
			ParentBranch:  st.Parent,
			BaseChangeset: st.BaseChangeset,
			Branch:        su.Branch,
		}
		if parent.Base == "" || parent.Base == su.Branch {
			// The parent names the branch it is standing on, which is the answer only until it lands.
			// Anything else the parent records is a branch its own work is measured against, and the
			// destination rule is what decides where that leads.
			return db.Ref
		}
		dest, err := changeset.DestinationOfParent(ctx, repo, parent, db)
		if err == nil && dest.Ref != "" {
			return dest.Ref
		}
		return db.Ref
	}
	return db.Ref
}

// stackBelow asks the one stack question the landing sentence answers: is there a parent under this
// changeset, and has its work reached the destination.
func stackBelow(ctx context.Context, repo *git.Repo, cs changeset.Changeset, db changeset.DefaultBranchRef) (stackUnder, error) {
	parent, err := changeset.ParentOf(ctx, repo, cs, db)
	if err != nil || parent.Branch == "" {
		return stackUnder{}, err
	}
	out := stackUnder{Branch: parent.Branch, Changeset: cs.BaseChangeset}
	if out.Changeset == "" || db.Ref == "" {
		// Nothing to ask the destination about: the record names a branch and no changeset, or this clone
		// cannot name the integration branch to ask. The parent is then read as unlanded, which is the
		// direction that tells a person to land something rather than the one that hands them a merge onto a
		// branch review can still rewrite — and it is the direction `change integrate` refuses in.
		return out, nil
	}
	out.Landed = changeset.LandingCommit(ctx, repo, db.Ref, out.Changeset)
	return out, nil
}

// landingNextAction is the landing contract (PRD §29) in one line, spelled once because four commands tell an
// author this same thing and a fifth spelling is how a contract drifts. git-pair performs the gate and the
// record and nothing in between: the merge itself is ordinary git, performed by whoever owns the branch,
// which is what keeps PRD §26's no-merge posture intact.
//
// The waiting form exists because the merge is not always the next step. A child whose parent has not landed
// is one merge away from a branch review can still rewrite, and `git pair change integrate` refuses to
// declare it for exactly that reason — so the sentence names what is waiting, gives the parent the landing
// step that is actually owed now, and only then names the merge this changeset runs once that has happened.
func landingNextAction(lc landingContract) string {
	target := displayRef(lc.Destination)
	if target == "" {
		target = "the base branch"
	}
	if lc.WaitingOn == "" {
		return fmt.Sprintf("`git pair check`, then merge into %s with ordinary git%s", target, lc.DestinationNote)
	}
	waiting := displayRef(lc.WaitingTarget)
	if waiting == "" {
		waiting = target
	}
	return fmt.Sprintf("waiting on parent %s: land it first (`git pair check`, then merge into %s with ordinary git), "+
		"then `git pair check` and merge into %s", lc.WaitingOn, waiting, target)
}
