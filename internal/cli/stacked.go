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
	"gitpair/internal/reviewref"
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
	// Landed is the commit the parent's integration record points at, empty when this clone holds no
	// record for the parent. It is the record's claim and not a merge's: a directory in the destination
	// with no record behind it is the `no integration record` note, because nothing durable says the work
	// is finished. Landing outranks every reading of the branch tip, because `--no-ff`, squash and
	// cherry-pick all leave the parent's branch exactly where the approval recorded it.
	Landed string
	// LandedInDefaultBranch says the landing commit is in the history of the branch git-pair calls the
	// integration branch. False covers "it reached a release branch" and "this clone cannot name the
	// integration branch", which the prose separates with LandedReach.
	LandedInDefaultBranch bool
	// LandedReach is that prose: ", reachable from main", ", not reachable from main", or nothing when
	// no branch identifies itself as the destination.
	LandedReach string
	// StaleBranch says the child's own head already carries the landing, so the parent's branch is dead
	// weight — the record holds the chain the branch was holding. It is false for a child that has not
	// rebased yet, where that branch is still the base the child is measured on, and false when the branch
	// is gone, because stale is a statement about a branch that is here.
	StaleBranch bool
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
		st, err = a.parentGone(ctx, repo, c, db, st)
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
	// The record before the tip comparison, and before the early return below: a parent that landed by
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

// parentLanded reads the parent's record and says what it means for the child in front of us.
//
// Two git reads and one ref, and only for a stack that names a parent changeset: the record, whether the
// child's head already carries it, and whether the destination branch does. The order is the cheap one —
// a parent with no record here costs the ref lookup and nothing else.
func (a *app) parentLanded(ctx context.Context, repo *git.Repo, c changeset.Changeset,
	db changeset.DefaultBranchRef, head string, st parentStatus) (parentStatus, error) {
	if c.ParentChangeset == "" {
		// The yaml names a branch and no changeset, so there is no record to ask. Silence is the answer:
		// the branch is here, its tip is reported, and nothing about it is claimed from a name.
		return st, nil
	}
	commit, err := reviewref.ResolveIntegration(ctx, repo, c.ParentChangeset)
	if errors.Is(err, reviewref.ErrNotIntegrated) {
		return a.parentInTrunkUnrecorded(ctx, repo, c, db, st)
	}
	if err != nil {
		return st, err
	}
	st.Landed = short(commit)
	if db.Ref != "" {
		in, err := repo.IsAncestor(ctx, commit, db.Ref)
		if err != nil {
			return st, err
		}
		st.LandedInDefaultBranch = in
		st.LandedReach = ", reachable from " + displayRef(db.LocalName())
		if !in {
			st.LandedReach = ", not reachable from " + displayRef(db.LocalName())
		}
	}
	if head == "" {
		return st, nil
	}
	on, err := repo.IsAncestor(ctx, commit, head)
	if err != nil {
		return st, err
	}
	st.StaleBranch = on
	if on {
		st.Note = fmt.Sprintf("your head is on %s, so the branch %s is stale — it holds nothing the record does not",
			st.Landed, st.Branch)
		return st, nil
	}
	st.Note = fmt.Sprintf("rebase onto it: this head is still measured on %s, which the landing replaced", st.Branch)
	return st, nil
}

// parentInTrunkUnrecorded is the sibling finding: the parent's branch tip is in the integration branch and
// no integration ref exists for it, which is §22's gap seen from the child. The child cannot be measured
// against a durable ref that was never written, so this is reported as a note naming the command rather
// than as a landing.
func (a *app) parentInTrunkUnrecorded(ctx context.Context, repo *git.Repo, c changeset.Changeset,
	db changeset.DefaultBranchRef, st parentStatus) (parentStatus, error) {
	if db.Ref == "" || st.Tip == "" {
		return st, nil
	}
	in, err := repo.IsAncestor(ctx, st.Tip, db.Ref)
	if err != nil || !in {
		return st, err
	}
	st.Note = fmt.Sprintf("%s is in %s with no integration record: `git pair integration record --changeset %s`, and %s",
		st.parentName(), displayRef(db.LocalName()), c.ParentChangeset, unrecordedHedge(false))
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

// parentGone decides what a vanished parent branch means. The branch is where an active parent lives,
// so its absence is read from the durable records: an integration ref says it landed, nothing says it
// ended, and either way the child needs a decision from its author rather than a new default.
func (a *app) parentGone(ctx context.Context, repo *git.Repo, c changeset.Changeset,
	db changeset.DefaultBranchRef, st parentStatus) (parentStatus, error) {
	if st.Changeset == "" {
		st.Next = "choose a new base with `git pair init --parent <branch> --set-parent`"
		st.Reason = fmt.Sprintf("parent branch %s is gone and the stack records no parent changeset to look for: the stack is unreconciled — %s", st.Branch, st.Next)
		return st, nil
	}
	commit, err := reviewref.ResolveIntegration(ctx, repo, st.Changeset)
	if errors.Is(err, reviewref.ErrNotIntegrated) {
		st.Next = "choose a new base with `git pair init --parent <branch> --set-parent`, or land the parent and record it"
		st.Reason = fmt.Sprintf("parent %s is gone with no integration record: the stack is unreconciled — %s", st.Changeset, st.Next)
		return st, nil
	}
	if err != nil {
		return st, err
	}
	l, err := a.describeLanding(ctx, repo, commit)
	if err != nil {
		return st, err
	}
	st.Landed = short(commit)
	st.LandedInDefaultBranch = l.InDefaultBranch
	st.LandedReach = l.reach()
	if err != nil {
		return st, err
	}
	destination := "the branch that carries it"
	switch {
	case l.InDefaultBranch:
		destination = l.DefaultBranch
	case l.BranchKnown:
		destination = fmt.Sprintf("%s, or the branch that carries it", l.DefaultBranch)
	}
	st.Reason = fmt.Sprintf("the parent %s landed as %s — parent landed as %s; rebase onto %s and have the result reviewed again",
		st.Changeset, short(commit), short(commit), destination)
	st.Next = fmt.Sprintf("parent landed as %s; rebase onto %s", short(commit), destination)
	return st, nil
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
