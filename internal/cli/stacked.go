package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/lifecycle"
	"gitpair/internal/model"
)

// A stacked changeset is measured against its parent branch, and the parent does not wait: it takes
// implementation commits, review submissions, approvals, rebases and merges while the child is being
// read. Every one of those moves the tip the child's diff and its approval were computed against, and
// none of them is visible in the child's own history — the child's commits are unchanged, so `check`'s
// drift test passes. The conservative rule (PRD §21) is that any of them invalidates the child's
// approval, and the only way to apply it is to have written the parent's tip down at the moment the
// approval was given, which is what `Review-Parent-Head` is for.
//
// The classification below exists because a rule that reads as arbitrary gets worked around. "The
// parent moved" does not tell an author whether to rebase, re-review, or wait; "the parent was
// approved, which moved its tip by one review commit" does.

// parentStatus is what the parent branch has done since the child's approval.
type parentStatus struct {
	// Branch is the parent branch's short name, empty when this changeset is not stacked.
	Branch string
	// Changeset is the parent's changeset ID when the stack recorded one.
	Changeset string
	// Recorded is the parent tip the approval named, empty when it named none.
	Recorded string
	// Tip is the parent branch's current tip, empty when the branch is gone.
	Tip string
	// Reason says why the approval no longer stands. Empty means the approval stands.
	Reason string
	// Next is the step the author can take, and travels with Reason.
	Next string
	// Note is a one-line observation that is not a refusal — the parent moved, or there was
	// nothing to compare — for `status` and `queue` to show.
	Note string
	// Landed is the commit the destination carries the parent's work at, empty when the destination holds
	// no directory for the parent. Landing outranks every reading of the branch tip, because `--no-ff`,
	// squash and cherry-pick all leave the parent's branch exactly where the approval recorded it.
	Landed string
	// LandedInDefaultBranch says the landing commit is in the history of the branch git-pair calls the
	// integration branch. False covers "it reached a release branch" and "this clone cannot name the
	// integration branch", which the prose separates with LandedReach.
	LandedInDefaultBranch bool
	// LandedReach is that prose: ", reachable from main", ", not reachable from main", or nothing when
	// no branch identifies itself as the destination.
	LandedReach string
	// StaleBranch says the child's own head already carries the landing, so the parent's branch is dead
	// weight — the destination holds the chain the branch was holding. It is false for a child that has not
	// rebased yet, where that branch is still the base the child is measured on, and false when the branch
	// is gone, because stale is a statement about a branch that is here.
	StaleBranch bool
	// ParentWorktree is a worktree that has the parent branch checked out, which is the reason
	// `git branch -D <parent>` would fail. It is read only where the deletion is being advised, and named
	// as text: removing a worktree is not git-pair's to do (PRD §26).
	ParentWorktree string
	// landedFull is the landing commit in full, for the comparisons that hand a revision to git. The
	// reported fields are shortened; git is not.
	landedFull string
}

// parentSinceApproval asks the stack question for one changeset: has the branch it is stacked on
// moved, landed, or ended since the approval being relied on?
//
// The refusal is for an approval only. A changeset that is READY, or blocked, or waiting on feedback has
// no approval for a parent to invalidate, and refusing it would report the parent's activity as this
// changeset's problem. The reading is not: `status` is where an author looks before offering anything, and
// a child stacked on a parent that has already landed wants to know that before it is offered, so the
// notes are reported for every state and any refusal an unapproved child picks up is demoted to one.
//
// `head` is the child's own tip, which is what tells the two landed cases apart: a child already on the
// landing commit has a stale branch to delete, and a child that has not rebased has a rebase to run.
func (a *app) parentSinceApproval(ctx context.Context, repo *git.Repo, c changeset.Changeset,
	db changeset.DefaultBranchRef, approved *lifecycle.Event, head string) (parentStatus, error) {
	parent, err := changeset.ParentOf(ctx, repo, c, db)
	if err != nil || parent.Branch == "" {
		return parentStatus{}, err
	}
	isApproval := approved != nil && approved.Kind == lifecycle.KindReview && approved.Outcome == model.OutcomeApprove
	st := parentStatus{Branch: parent.Branch, Changeset: c.ParentChangeset, Tip: parent.Tip}
	if isApproval {
		st.Recorded = approved.ReviewedParentHead
	}
	if st.Tip == "" {
		st, err = a.parentGone(ctx, repo, c, db, head, approved, st)
	} else {
		st, err = a.parentLive(ctx, repo, c, db, head, approved, st)
	}
	if err != nil || isApproval {
		return st, err
	}
	if st.Reason != "" {
		// The state has no approval for any of these findings to invalidate: the abandoned parent, the
		// unreconciled stack. Say it, and let `status` print it as an observation.
		st.Note, st.Reason, st.Next = st.Reason, "", ""
	}
	return st, nil
}

// parentLive reads what the parent's side of the stack says while its branch is still here.
func (a *app) parentLive(ctx context.Context, repo *git.Repo, c changeset.Changeset,
	db changeset.DefaultBranchRef, head string, approved *lifecycle.Event, st parentStatus) (parentStatus, error) {
	// A parent that has been abandoned will never carry the work the child was stacked on, whatever
	// its tip does next. That outranks "it moved" and outranks "it landed": rebasing onto a dead branch
	// is not a way forward.
	gone, err := parentAbandoned(ctx, repo, c, db, st.Branch)
	if err != nil {
		return st, err
	}
	if gone {
		st.Next = "choose a new base with `git pair init --parent <branch> --set-parent`"
		st.Reason = fmt.Sprintf("the parent %s is abandoned on %s: the stack is unreconciled — %s",
			st.parentName(), st.Branch, st.Next)
		return st, nil
	}
	// The landing before the tip comparison, and before the early return below: a parent that landed by
	// merge has not moved its branch, so `Recorded == Tip` is true and reads as "nothing happened"
	// exactly when the work left the branch.
	if st, err = a.parentLanded(ctx, repo, c, db, head, st); err != nil {
		return st, err
	}
	if approved == nil || approved.Kind != lifecycle.KindReview || approved.Outcome != model.OutcomeApprove {
		return st, nil
	}
	if st.Recorded == "" {
		// The approval predates the trailer, or was written by a hand-edited commit. The absence is
		// not evidence that the parent moved, and refusing here would refuse every child approved
		// before parent tracking existed. Say what is missing and let the reviewer decide.
		if st.Note == "" {
			st.Note = fmt.Sprintf("parent %s: the approval recorded no parent tip, so git-pair cannot tell whether it has moved", st.Branch)
		}
		return st, nil
	}
	if st.Recorded == st.Tip {
		if st.Landed == "" {
			return st, nil
		}
		// The parent landed and its branch did not move, so the only thing that changed is what the base
		// points at: the landing commit instead of the parent's branch. An approval is a claim about content, so
		// content decides whether it follows the base (PRD §21).
		same, err := landedBaseIsTheSameWork(ctx, repo, st.Recorded, st.landedFull, head)
		if err != nil {
			return st, err
		}
		if same {
			st.Note = fmt.Sprintf("%s; the base moved onto the landing and the content under it did not, so the approval still measures this work",
				st.Note)
			return st, nil
		}
		advice := fmt.Sprintf("%s and have the result reviewed again", landedParentStep(c, st))
		st.Reason = fmt.Sprintf("the parent %s landed as %s%s and the diff under it differs from what review %s approved: %s",
			st.parentName(), st.Landed, st.LandedReach, approved.Short, advice)
		st.Next = advice
		return st, nil
	}
	moved, err := classifyParentMovement(ctx, repo, st.Recorded, st.Tip)
	if err != nil {
		return st, err
	}
	advice := fmt.Sprintf("rebase onto %s and have the result reviewed again", st.Branch)
	st.Reason = fmt.Sprintf("the parent branch %s moved since review %s approved %s: %s — %s",
		st.Branch, approved.Short, short(st.Recorded), moved, advice)
	st.Next = advice
	return st, nil
}

// parentLanded reads the parent's landing where landing lives — the destination's tree and history — and
// says what it means for the child in front of us.
//
// One chain derivation, and only for a stack that names a parent changeset: the destination either carries
// the parent's directory or it does not, and the run behind it names the commit the parent's work became.
// A parent that reached some other branch is not landed, which is the same answer the write gate and the
// queue give; `parentInDestination` covers the case where the parent's tip is in the destination without its
// directory having arrived.
func (a *app) parentLanded(ctx context.Context, repo *git.Repo, c changeset.Changeset,
	db changeset.DefaultBranchRef, head string, st parentStatus) (parentStatus, error) {
	if c.ParentChangeset == "" || db.Ref == "" {
		// The yaml names a branch and no changeset, so there is nothing to look for in the destination.
		// Silence is the answer: the branch is here, its tip is reported, and nothing is claimed from a name.
		return st, nil
	}
	chain, err := changeset.LandedChain(ctx, repo, db.Ref, c.ParentChangeset)
	if errors.Is(err, changeset.ErrNoChain) {
		return a.parentInDestination(ctx, repo, c, db, st)
	}
	if err != nil {
		return st, err
	}
	commit := chain.Landing
	st.Landed = short(commit)
	st.landedFull = commit
	// The chain was derived from the destination, so its containment there is not a question to ask again;
	// the reach sentence a durable ref needed has nothing left to hedge about.
	st.LandedInDefaultBranch = true
	st.LandedReach = ""
	if head == "" {
		return st, nil
	}
	on, err := repo.IsAncestor(ctx, commit, head)
	if err != nil {
		return st, err
	}
	st.StaleBranch = on
	if on {
		// `git branch -D` is the step this leaves, and in a repository where the parent is still checked
		// out somewhere it fails before it does anything. Read the blocker rather than guess at it: one
		// `worktree list` for the case that prints the deletion.
		path, err := worktreeHolding(ctx, repo, st.Branch)
		if err != nil {
			return st, err
		}
		st.ParentWorktree = path
	}
	if on {
		st.Note = fmt.Sprintf("your head is on %s, so %s", st.Landed, landedParentStep(c, st))
		return st, nil
	}
	st.Note = fmt.Sprintf("%s: this head is still measured on %s, which the landing replaced",
		landedParentStep(c, st), st.Branch)
	return st, nil
}

// landedParentStep is the step a landed parent leaves, spelled once because `status`, `check` and `queue`
// all print it and a fifth spelling is how a contract drifts. It is the sibling of `landingNextAction` for
// the same reason: one sentence, several readers.
//
// It prints the command, because "rebase onto it" is a sentence the author has to translate into three
// arguments, and the translation is the part that goes wrong: the argument people reach for is the parent
// branch's name where the landing commit belongs, and vice versa.
func landedParentStep(cs changeset.Changeset, st parentStatus) string {
	if st.StaleBranch {
		step := fmt.Sprintf("the branch %s is stale — it holds nothing the destination does not: `git branch -D %s`",
			st.Branch, st.Branch)
		if st.ParentWorktree != "" {
			// The deletion will fail there, and the remedy is not git-pair's to run (PRD §26): naming the
			// worktree is the whole of it.
			step += fmt.Sprintf(" (checked out in %s — `git worktree remove %s` first)",
				st.ParentWorktree, st.ParentWorktree)
		}
		return step
	}
	child := cs.Branch
	if child == "" {
		child = cs.Slug
	}
	return fmt.Sprintf("rebase onto it: `git rebase --onto %s %s %s`", st.Landed, st.Branch, child)
}

// worktreeHolding is the path of the worktree that has <branch> checked out, or "" when none does.
//
// It exists for one sentence. `git branch -D <parent>` is the step a landed parent leaves, and in a
// repository that keeps its stacks in worktrees the branch is still checked out somewhere and the delete
// stops with "used by worktree at ...". A reader who has to discover that by running it is a reader who
// just learned the hard way that the tool knew.
func worktreeHolding(ctx context.Context, repo *git.Repo, branch string) (string, error) {
	out, err := repo.Git(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		// The finding is the deletion, and the blocker is a note about it. A failed read must not swallow
		// the advice, so the caller is told nothing was found rather than nothing being true.
		return "", nil
	}
	path, want := "", "refs/heads/"+branch
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
		case line == "branch "+want:
			return path, nil
		}
	}
	return "", nil
}

// parentInDestination is the sibling finding seen from the child: the parent's branch tip is already an
// ancestor of the integration branch, so the parent's work has reached the destination and this child is
// measured against ground that has moved. It is a note rather than a state because the child's own markers
// have not moved, and it names no command because there is nothing left to write — the destination's tree
// and its history are the record, and `git pair status --changeset <parent>` reads them.
func (a *app) parentInDestination(ctx context.Context, repo *git.Repo, c changeset.Changeset,
	db changeset.DefaultBranchRef, st parentStatus) (parentStatus, error) {
	if db.Ref == "" || st.Tip == "" {
		return st, nil
	}
	in, err := repo.IsAncestor(ctx, st.Tip, db.Ref)
	if err != nil || !in {
		return st, err
	}
	st.Note = fmt.Sprintf("%s is in %s: the parent's work has reached the destination, so this branch is "+
		"measured against ground that has moved (`git pair status --changeset %s` reads the landing)",
		st.parentName(), displayRef(db.LocalName()), c.ParentChangeset)
	return st, nil
}

// parentName is how the parent is best called: by changeset when the stack knows it, by branch when
// it does not.
func (st parentStatus) parentName() string {
	if st.Changeset != "" {
		return st.Changeset
	}
	return st.Branch
}

// parentGone decides what a vanished parent branch means. The destination is where a landed parent lives,
// so its history is what the absence is read against: the destination carries the parent's directory, or it
// carries neither the branch nor the landing and the child needs a decision from its author rather than a
// new default.
func (a *app) parentGone(ctx context.Context, repo *git.Repo, c changeset.Changeset,
	db changeset.DefaultBranchRef, head string, approved *lifecycle.Event, st parentStatus) (parentStatus, error) {
	if st.Changeset == "" {
		st.Next = "choose a new base with `git pair init --parent <branch> --set-parent`"
		st.Reason = fmt.Sprintf("parent branch %s is gone and the stack records no parent changeset to look for: the stack is unreconciled — %s", st.Branch, st.Next)
		return st, nil
	}
	if db.Ref == "" {
		st.Next = "choose a new base with `git pair init --parent <branch> --set-parent`"
		st.Reason = fmt.Sprintf("parent %s is gone and this repository has no integration branch to read: the stack is unreconciled — %s", st.Changeset, st.Next)
		return st, nil
	}
	chain, err := changeset.LandedChain(ctx, repo, db.Ref, st.Changeset)
	if errors.Is(err, changeset.ErrNoChain) {
		st.Next = "choose a new base with `git pair init --parent <branch> --set-parent`, or land the parent first"
		st.Reason = fmt.Sprintf("parent %s is gone and %s carries no directory for it: the stack is unreconciled — %s",
			st.Changeset, displayRef(db.LocalName()), st.Next)
		return st, nil
	}
	if err != nil {
		return st, err
	}
	commit := chain.Landing
	st.Landed = short(commit)
	st.landedFull = commit
	st.LandedInDefaultBranch = true
	st.LandedReach = ""
	destination := displayRef(db.LocalName())
	// The same rule `parentLive` applies, for the case that has always relinked: the landing moves the
	// base, and an approval is a claim about content. A squash, a rebase-merge and a cherry-pick move the
	// content the review saw into commits the child never had, so the comparison says "different" and the
	// child is told to have the result reviewed again; a plain merge that brought nothing new answers
	// "the same", and the approval stands.
	if approved != nil {
		same, err := landedBaseIsTheSameWork(ctx, repo, st.Recorded, commit, head)
		if err != nil {
			return st, err
		}
		if same {
			st.Note = fmt.Sprintf("parent %s landed as %s%s; the base moved onto the landing and the content under it did not, so the approval still measures this work",
				st.parentName(), st.Landed, st.LandedReach)
			return st, nil
		}
	}
	st.Reason = fmt.Sprintf("the parent %s landed as %s — parent landed as %s; rebase onto %s and have the result reviewed again",
		st.Changeset, short(commit), short(commit), destination)
	st.Next = fmt.Sprintf("parent landed as %s; rebase onto %s", short(commit), destination)
	return st, nil
}

// landedBaseIsTheSameWork compares what the child's diff measures under the two bases a landing puts in
// front of it: the tip the approval recorded on the parent's branch, and the commit the parent's work
// became.
//
// `base...head` is the difference between the tree at the merge base and the tree at head, and head is the
// same commit under both readings — so the two diffs carry the same content exactly when the two merge
// bases carry the same tree. That is two `merge-base` calls and two `rev-parse`s, where comparing patches
// would cost two diffs and a patch id on every read of a stacked child.
//
// A comparison that cannot be made answers "different", because that is the direction that asks a human to
// look again rather than the one that lets an unreviewed diff through the gate.
func landedBaseIsTheSameWork(ctx context.Context, repo *git.Repo, oldBase, landed, head string) (bool, error) {
	if oldBase == "" || landed == "" || head == "" {
		return false, nil
	}
	was, err := repo.MergeBase(ctx, oldBase, head)
	if err != nil {
		return false, nil
	}
	now, err := repo.MergeBase(ctx, landed, head)
	if err != nil {
		return false, nil
	}
	wasTree, err := repo.RevParse(ctx, was+"^{tree}")
	if err != nil {
		return false, nil
	}
	nowTree, err := repo.RevParse(ctx, now+"^{tree}")
	if err != nil {
		return false, nil
	}
	return wasTree == nowTree, nil
}

// parentAbandoned reports whether the parent changeset has been abandoned on a branch that is still
// here.
func parentAbandoned(ctx context.Context, repo *git.Repo, c changeset.Changeset,
	db changeset.DefaultBranchRef, branch string) (bool, error) {
	if c.ParentChangeset == "" || db.Ref == "" {
		return false, nil
	}
	summary, err := lifecycle.Summarize(ctx, repo, c.ParentChangeset, db.Ref, "refs/heads/"+branch)
	if err != nil {
		if errors.Is(err, git.ErrUnknownRevision) {
			return false, nil
		}
		return false, err
	}
	return summary.Marker != nil && summary.Marker.Kind == lifecycle.KindAbandoned, nil
}

// classifyParentMovement says what the parent branch did between the tip an approval recorded and the
// tip it holds now. The classes are the ones a reader can act on: an implementation commit means the
// child is now diffing against new work, an approval means the parent is waiting to land, a rebase
// means the parent's history was rewritten, and a merge means someone else's work arrived in it.
func classifyParentMovement(ctx context.Context, repo *git.Repo, from, to string) (string, error) {
	rewritten, err := repo.IsAncestor(ctx, from, to)
	if err != nil {
		return "", err
	}
	if !rewritten {
		return "it was rewritten (rebase or force-push), so the tip the approval named is no longer in its history", nil
	}
	records, err := repo.LogFields(ctx, from+".."+to, "%h", "%p", "%b")
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		// The tips differ but the range is empty: still a moved parent, still nothing to name.
		return "it holds a different commit than the one the approval named", nil
	}
	// LogFields is oldest first, so the newest commit is last. It is the newest that explains what
	// the author has to deal with now; the count says how much of it there is.
	var merged bool
	class := ""
	for i, rec := range records {
		if len(rec) < 3 {
			continue
		}
		isMerge := strings.Contains(rec[1], " ")
		merged = merged || isMerge
		if i == len(records)-1 {
			class = movementClass(rec[2], isMerge)
		}
	}
	if class == "" {
		class = "an implementation commit"
	}
	out := class
	if merged && class != "a merge" {
		out += ", including a merge into it"
	}
	if n := len(records); n > 1 {
		out = fmt.Sprintf("%s (%d commits since)", out, n)
	}
	return out, nil
}

// movementClass names one commit's kind. A commit with two parents is a merge whatever its message
// claims; the git-pair markers are read from trailers rather than subjects, because the subject is
// editable and the trailer is what `check` and `history` agree on.
func movementClass(body string, isMerge bool) string {
	if isMerge {
		// Someone else's work arrived in the parent. Its message says "merge" and its parents say
		// the same thing; the parents are the part that is not prose.
		return "a merge"
	}
	trailers := lifecycle.Trailers(body)
	switch {
	case trailers[model.TrailerOutcome] == string(model.OutcomeApprove):
		return "an approval"
	case trailers[model.TrailerOutcome] != "":
		return "a review commit"
	case trailers[model.TrailerState] != "":
		return "a git-pair marker commit"
	}
	return "an implementation commit"
}
