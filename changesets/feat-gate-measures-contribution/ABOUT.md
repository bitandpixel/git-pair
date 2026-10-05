# feat-gate-measures-contribution

## Summary

The gate asks the content question directly. A stacked child's approval now stands while the content its
branch contributes is the content the approval measured, and falls when it is not. The parent's own
movement — an implementation commit, a review submission, an approval, a rebase, a merge into it — is a
note on the verdict, and never a reason.

Three rules changed, and one guard was added:

-   The landed-parent comparison compares this branch's contribution, measured from the ground its work
    now sits on, with the `Review-Diff-Id` the approval recorded. The older reading — the two bases a
    landing puts in front of the child, compared by the trees they carry — stays as the fallback for an
    approval that recorded no identity, which is every approval written before that trailer.
-   That comparison is asked whenever the parent's work has landed, whatever the parent's branch did
    afterwards. Gating it on the branch still standing at the tip the approval recorded sent the ordinary
    ordering — a parent reviewed after its child, so its approval marker is a commit the child never
    recorded — to the weaker fallback, which agreed for the wrong reason.
-   Parent movement produces a note that names the kind of movement and whether the parent's new commits
    reach any file this branch changes.
-   An approval kept on the landed path is also asked whether the branch still merges into the destination,
    and is refused with the conflicting paths when it would not.
-   `status --json` and `check --json` carry the two facts behind a parent verdict: `parent.measured_base`
    / `parent_measured_base`, and `parent.comparison` / `parent_comparison` naming which reading answered.

## Why the movement rule went

The old rule was "any change to the parent branch after a child is approved invalidates the child's
approval", from the v2 requirements' initial conservative rule. One round of parent review was enough to
kill every child stacked on it: the parent's own approval marker is a commit, and a commit was enough. The
children had not changed. Nothing had been read differently, and nothing had to be.

The rule was conservative in the wrong direction. It was conservative about *bookkeeping* — a tip had moved
— while the thing an approval is a claim about is a diff. And it did not protect the case that matters: a
child that merges somebody else's branch in was passing under it, because its parent had not moved.

## The ground: which commit a contribution is measured from

This is where the change was won or lost, and the first version of it was wrong in a way the existing suite
caught: measuring the child's diff from the parent's **landing commit**. In the fixtures that meant the
parent's content appeared in the child's patch as reverse deletions, and six approvals that pass today
started refusing, including `TestCheckPassesAChildOnALandedParentAndNamesTheStep`.

What was measured, in a scratch repository with a stacked child and a squashed parent:

| the base the diff is taken from | digest | right? |
| ------------------------------- | ------ | ------ |
| recorded at review (the parent's tip) | `774e172c…` | the reviewed contribution |
| `mb(main, head)` / `mb(landing, head)` | `91a20100…` | wrong — books the squashed parent's work as the child's |
| two-dot from the landing commit | `774e172c…` here, but the parent's content in the fixtures' shape | wrong whenever the child never took the parent in |
| two-dot from the destination's tip | `774e172c…`, then `debd53f1…` after one unrelated trunk commit | wrong — approvals die on somebody else's merge |

No single commit answers for every shape of the world, so the ground is chosen rather than named: the
newest of four candidates that is still an ancestor of the head — the recorded `Review-Base-Head`, the
merge base of the recorded base with the head, the merge base of the parent's landing with the head, and
the merge base of the destination with the head. Each enters through a merge base with the head, so a commit
outside the head's history can never be picked; `gitpair/internal/changeset/contribution.go` has the case
table with the shape each candidate is right for.

When no candidate contains all the others — a branch that merged two grounds — there is no single ground to
measure above, the answer is "not measured", and the older two-base comparison decides. A caller that had to
choose one of two incomparable candidates would be deciding the question by which one it tried first.

## What the digest leaves out

`changesets/<id>/` and `changesets/.landed/<id>/` for this changeset **and for every ancestor**. The
ancestor half is what the first version missed. `change tidy` moves the parent's record into
`changesets/.landed/<parent>/`, so a child measured from a base that carries that directory, against a head
that predates it, reports the parent's review as the child deleting it.

What stays in the digest is the consequence to accept: a commit that changes only review bookkeeping has an
empty contribution and cannot be refused on content. That is already how marker commits are treated, and the
`Review-Head` lineage rule still refuses a branch whose reviewed commits are gone.

## The merge the gate can see coming

`git merge-tree --write-tree -z` answers what a merge would produce, and writes objects and nothing else: no
ref moves, the index and working tree are untouched, and no commit exists afterwards. It is not a merge in
the sense PRD §26 forbids, which is why `classify` treats it as a pure read and the memo may keep the
answer.

The guard is on the landed path where an approval is kept. A branch that would conflict has no clean patch to
compare at all — what would land is whatever resolution somebody writes afterwards, over content no
reviewer saw — and the merge job, after the gate said ready and CI said green, is the worst place to find
out. An error from git adds no refusal: a clone whose git is too old for the command, or a destination it
cannot name, would otherwise be refused for the shape of its own environment. The conservative direction
PRD §2 asks for is about the question that *grants* an approval; this condition only ever adds refusals.

## Cost

One more read on top of a read. `status --changeset` on the five-deep landed stack in
`TestStatusStackChainCostsABoundedReadPerStep` went from 81, 84, 89 to 90, 97, 104 git invocations for
depths 3, 4 and 5, so the marginal per extra ancestor went from about 5 to about 7. The budget in that test
went from 5 to 10 with the measurements beside it. The added reads are each ancestor's directory (the
exclusion walk needs the record), the merge bases between the candidates, the ancestry comparisons among
them, the digest, and the merge probe. The marginal is still per-ancestor and constant, which is the
property the test bounds.

## Tests that changed their minds

Three tests encoded the old rule and were rewritten, with their intent preserved:

-   `TestAnyParentMovementEndsTheChildsApproval` → `TestParentMovementIsANoteAndNotAReason`. Same five
    shapes of parent activity, same classification assertions; the verdict is now OK with a note.
-   `TestCheckStillRefusesAParentThatMovedAfterTheApproval` → `TestCheckNotesAParentThatMovedAfterTheApproval`.
-   `TestChildOfALandedParentIsToldWhereTheWorkWent` — the squash landing with the branch deleted now
    passes, which is the case this changeset exists for. Its "where the work went" assertions stay.
-   `TestRelinkDropsAnApprovalWhoseDiffDiffers` → `TestRelinkOntoTheLandingCostsAReviewForTheRewrite`. Its
    fixture rebases, and the reason that fires is the `Review-Head` lineage rule, not the content rule: a
    rebase costs a review whatever it changes. The content rule's own refusal is new coverage instead.

New coverage, in `internal/cli/contribution_gate_test.go`: a parent editing a file the child also edits
says so in the note; a squash landing keeps the child's approval and reports `parent_comparison`; a branch
that would conflict with the destination is refused with the path; an approval with no identity falls back
to the base comparison and says which reading answered; new work on a child is still refused.

## Where this stops

The perverse case that started this — a child merging the destination in, the thing a reviewer most wants
before a merge — is still refused, but not by the stack rule. It is refused by the rule that counts content
the review never saw, which reads the branch's own history and cannot yet tell the destination's arrivals
from the author's. `TestMergingTheDestinationInIsStillUnreviewedWork` pins that boundary so the next change
to that rule has something to break.

The identity's own limit sits next to that one, and is narrower than it first looks. Where trunk's work is in
files this branch does not touch, taking the destination in moves the ground and leaves the contribution
alone — `TestContributionIsUnmovedByTakingTheDestinationIn` asserts the value stayed equal *and* the ground
moved, so an implementation that measured the same pair twice could not pass for one that followed the
ground. Where trunk edits a file this branch also edits, the value moves however far the line is, because a
blob OID is the identity of a whole file and the trunk's line is now inside the child's post-image
(`TestContributionCountsATrunkEditToAFileTheChildAlsoChanges`). Teaching the unreviewed-content rule about the
destination therefore needs more than this identity: it has to know whose line is whose, and a digest of
whole-file blobs does not.

`docs/plans/review-architecture-v2/requirements.md` carries the requirement this supersedes, annotated
where it stands.
