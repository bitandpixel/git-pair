# Decide the active changeset from the manifests

## Summary

Adds the execution plan for collapsing the stack link into `base:` plus an always-recorded `base-changeset:`, and
for deciding which changeset a branch is working on from what those files record instead of from which directory
was edited last. The plan is a document only; the behaviour it describes lands in the changesets that follow it.

## What this changes

`docs/plans/base-changeset-collapse/plan.md`, in nine milestones. Each carries a status line, so the document
says what has happened as well as what is planned:

- M2 moves the author's `ignores:` declaration ahead of every candidate-ranking rule.
- M3 ranks candidates by the commit that added a changeset directory instead of the commit that last edited one,
  and reads it only when ranking is needed.
- M4 writes `base:` with `base-changeset:` always recorded, keeps the older spellings readable, and deletes the
  ancestor walker that only existed because the id was not recorded.
- M5 makes the set operation - candidates minus the ids other candidates record as their base changeset - the
  primary rule, with ranking demoted to a tie-break.
- M6 makes the branch shape an invariant: one unlanded changeset plus the ones it is stacked on, refused at
  `init`, at `change ready`, and by `check`, so a second changeset cannot ride into the destination unreviewed.
- M7 gives the exits from that refusal: `change combine`, which folds one changeset into another and archives the
  disappeared directory under the survivor's `.combined/` rather than deleting it, and `change stack --base`, which
  records the stack that branching already created by comparing the changesets on the two branches. Neither rewrites
  a commit. Splitting a branch is named as the third possibility and left to the author, because a rewrite costs a
  review round and deciding which half of a mixed commit belongs where is a decision about the work.

- M8 removes the writer for `ignores:`, which that invariant leaves without a case.
- M9 rewrites the PRD passages that still name `parent:` as the authored link and describe selection by the most
  recently touched directory.

Two have happened while this document sat unoffered: M2 landed in main at `71c55bc`, and M3 is offered as
`feat-rank-by-add-commit`. Their sections record what shipped, including one rule that was not in the plan when it
was written. A drop that would remove every candidate means the records contradict each other, and that now answers
"no selection, here are the candidates" instead of being settled by the ordering - which is what the tie-break would
have done once M3 changed the measurement. `TestResolveMutualIgnoresKeepsBothCandidates` had asserted ambiguity only
because both directories happened to be edited by the same commit.

One milestone boundary moved while the document was unoffered. The exits from the branch-shape refusal were written
inside M6, which left M6 holding an invariant and two commands, and the file numbered M1-M6, M8, M9. They are now
M6 (the invariant) and M7 (the two exits), which is what the numbering already implied.

## Evidence

The plan records measurements taken on `probe/setop-selection`, an experiment branch that will not be offered. The
set operation was installed in `choose`, the suite was run in both modes, and the fixtures that differed are
tabulated in the plan. Two results shaped the milestones:

- With the set operation absent, a branch whose last commit edits the *parent* changeset's directory is answered
  with the parent, not the child. Ranking by the commit that added each directory answers the child, with or
  without the recorded id, so the defect is fixed by M3 on its own.
- With the set operation installed ahead of `ignores:`, a stacked sibling wins over the changeset whose file
  declares it shares the branch. That is why the declaration moves first, and why it is a separate milestone.

Measured cost of both commit facts in this repository (484 commits): 2 to 3 ms per directory, whether the walk
stops at the last edit or at the commit that added the directory.

## Not included

- No code change. Nothing in `internal/` moves in this changeset.
- No migration. The landed changeset directories on trunk keep the fields they were authored with, and the reader
  keeps accepting them; M4 changes the writer only.
- No authored `destination:` field. Where work lands stays computed.
