# Does the child owe the destination before the merge?

## The questions

The plan's predicate withholds a restack when "the contribution above the landing equals what that approval
recorded". Two things had to be established before that predicate could decide a verdict.

1. **Spike 1 — does anything require the child to take the destination in before it merges?** If a merge of
   the un-restacked child produced a different tree from a merge of the restacked one, the step would be part
   of the landing and withholding it would be wrong.
2. **Spike 3 — is the measured contribution above the landing what the gate itself licenses?** The predicate
   reads `MeasureContribution`; the gate reads drift and lineage. If the two could disagree about the same
   branch, the predicate would have to consult the gate's own measurement instead of a second one.

## How it was measured

One fixture, five shapes. A parent `alpha` approved, a child `beta` cut from it and approved against that tip,
the parent landed on `main`, and the parent's branch left present. The three landing shapes are a plain
`--no-ff` merge, a `--squash`, and a `rebase` + `--ff-only` merge; two variants add an unrelated commit to
trunk before the landing, and make parent and child edit the same file.

For each: `check --json` before the step; the tree at the child's head; the tree of merging the child into
`main` from a throwaway branch; then `git rebase --onto <landing> alpha beta`, the tree again, and
`check --json` again; then the tree of merging the rewritten child into `main`.

## What came out

| Landing                            | `ready` before | Rebase changed the tree?    | The only new reason after the rebase                          | Merge of the un-restacked child | Merge of the restacked child |
| ---------------------------------- | -------------- | --------------------------- | ------------------------------------------------------------- | ------------------------------- | ---------------------------- |
| `--no-ff` merge                    | true           | no                          | rewritten history                                             | `bc7667a02a36`                  | `bc7667a02a36`               |
| `--no-ff` merge, trunk moved first | true           | yes — trunk's new file      | rewritten history                                             | `624c35cbd7fe`                  | `624c35cbd7fe`               |
| `--no-ff` merge, same file in both | true           | no                          | rewritten history                                             | `eed9e91a4bd4`                  | `eed9e91a4bd4`               |
| `--squash`                         | true           | no                          | rewritten history, **and** the diff differs from the approval | `bc7667a02a36`                  | `bc7667a02a36`               |
| `rebase` + `--ff-only`             | true           | no (already on the landing) | none                                                          | `bc7667a02a36`                  | `bc7667a02a36`               |

Both questions answered, and the plan's predicate stands as designed.

**Spike 1: no.** In every shape the merge of the un-restacked child produced the same tree as the merge of the
restacked one. The rebase changed the child's own tree in exactly one row — the one where trunk had moved
meanwhile — and what it added there was trunk's content, which the merge was going to bring either way. The
step is housekeeping, not a precondition of the landing.

**Spike 3: they agree, and the one asymmetry points the same way.** Where the comparison answered
`contribution` and equal, the gate passed and the merge carried the reviewed content. Taking the rewrite then
cost the verdict through one rule only — `Review-Head` no longer names a commit in this history — with the
tree unchanged in three of the five rows. No fixture was found where "equal" and the gate disagreed about a
merge that should happen, so the predicate consults the measurement it has.

The asymmetry worth writing down is the squash row. Before the rewrite the child's contribution measures equal
— the parent's squashed content sits below the ground the child is measured from, so it cancels. After the
rewrite the recorded base is no longer an ancestor of the head, `MeasureContribution` finds no single ground
that contains all its candidates, and the reading falls through to the older comparison of two base trees,
which answers "different". So the same content reads equal until you take the step and different afterwards,
and the refusal that follows is the gate doing its job. That is the strongest argument in the file for
withholding the command from a passing verdict: the tool cannot know the author will decline it, and the
author cannot tell from the verdict that the command is the one that costs them their approval.

## Not asked

Spike 2 — whether carrying the parent's commits misleads `change tidy`, the queue's wait measurement,
`history`, or the review span — was not exercised: no part of this change depends on the answer, and the
implementation neither reads nor writes those surfaces. It stays open, listed in the plan rather than
silently dropped.

Spike 4 — how often the branch-present case is the one that happens — is answered by observation rather than
a count. A landing is a merge into the destination, and no git-pair command deletes the parent's branch, so
the branch is present unless somebody deletes it by hand. Both the incident (`feat-hooks-runner` on
`feat-pre-post-hooks`) and the fixture above land in that case, which is the second independent observation
the plan asked for.
