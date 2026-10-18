# fix-stale-parent-branch-advice

## Summary

`git pair check`, `git pair status` and `git pair queue` told authors to run `git branch -D <parent>` on a
parent branch that still held commits the destination had never held. The landed-parent reading asked one
containment question — does this head already carry the landing commit — and printed the deletion whenever it
held. That question says where the child sits. The deletion is about the branch, and a parent that took another
round of work after landing has a branch full of commits nobody has merged. The advice now requires both facts,
and the state where the branch is simply gone has a sentence of its own.

This is the only piece of advice in that family that cannot be undone: the rebase it prints beside it can be
re-run, and the worktree blocker is already only text.

## What changed

Measured on a repository where `alpha` was approved and landed into `main`, `beta` was approved on top of the
landing commit, and `alpha` then took one commit nobody landed (`git log main..alpha` lists `3a4cee6`):

```console
$ git pair check --json          # before
ready True  parent_stale_branch True  parent_head_carries_landing True
next_action: … parent alpha landed as 37d5a3c — the branch alpha is stale —
             it holds nothing the destination does not: `git branch -D alpha`

$ git pair check --json          # after
ready True  parent_stale_branch False  parent_head_carries_landing True
next_action: … parent alpha landed as d5dcb1e — the branch alpha still holds commits the destination does
             not, so it is not stale — land or drop that work on alpha before the branch goes
```

`status` prints the same step in `parent.step` and `queue` prints it in `parent_notes`, so all three surfaces
changed together and all three are tested. Where the parent branch really is spent — nothing above the landing,
which is the case the sentence was written for — the deletion is still advised, on all three.

Two states are reported apart that were one field: `parent_stale_branch` now means "this head is on the landing
**and** the parent's branch holds nothing the destination lacks", while `parent_head_carries_landing` keeps the
old meaning on its own. `status` documents `stale_branch` the same way.

## Design decisions

**One containment question, three answers.** `parentBranchReading` resolves the parent's branch and asks whether
the commit is in the destination, and returns `branchAbsent`, `branchSpent` or `branchHoldsWork`. A bool could
not tell "the branch is not here" from "the branch has work in it", and those two need different sentences —
the first leaves no command to run at all. Absence is read from `RevParse`'s own sentinel rather than from
`merge-base`'s stderr, because that command says `Not a valid object name`, which is not a wording
`IsUnknownRevision` claims to recognise; resolving first also means the containment question is asked of a
commit id rather than of a name that could mean two branches.

**Fail closed toward the silence.** An unreadable branch, an empty destination ref, or a read that fails for a
reason other than absence all answer `branchHoldsWork`, which prints no command. The failure this guards is a
deletion advised on an unanswered question, so the cost of uncertainty is a missing hint rather than lost
commits.

**`BranchGone` is stated rather than inferred.** The old first case of `landedParentStep` tested
`LandingUnderHead && Tip == ""`, where an empty `Tip` meant "the branch is gone" — true on the path that reads
the tip, and quietly false for the queue, which builds the same struct without reading it. Making the fact a
field lets the queue set it from the reading above and keeps the gone sentence away from a branch that is
present.

**What did not move.** The rebase sentence, the worktree blocker (`git worktree remove <path>` as text, never
run — PRD §26), and the queue's cost shape are unchanged: the queue still pays one chain derivation for a row
stacked on a changeset, and the extra resolution and containment question run only when the child is already on
the landing, which is the same window in which the deletion was printed before.

## Validation

`mise run check` and `mise run gates` (sharded suites plus the e2e, pty and CI-integration gates), including
the cost ladder in the CI-integration gate.

`internal/cli/parent_stale_branch_test.go` pins the gate verdict, the `status` step and the queue note for a
parent holding unlanded work; the same file pins that a spent branch still gets the deletion, and that a deleted
parent branch gets the gone sentence rather than either claim. `internal/cli/advice_contract_test.go` gained
rows for the three states, so the sentences are read by the contract that checks every emitted command names
something this build answers to.

Both directions were checked by putting the defect back. Making the reading always answer `branchSpent` — the
old behaviour — fails the check, `status` and queue tests with the exact stale sentence they refuse; making it
always answer `branchHoldsWork` fails `TestTheDeletionIsStillAdvisedWhenTheParentBranchHoldsNothingNew`.

## Known limitations

The containment question is asked of the integration branch the landing chain was derived from. Where the
parent's work reached the destination through another branch, or the clone has no integration branch to name,
the question cannot be answered and the deletion is withheld — the fail-closed side of this trade, and the
direction a reviewer should push back on if the missing hint turns out to cost more than the risk.

The queue continues to spell the step without the worktree blocker, as it did before: the blocker is one read
per row on a reviewer's surface, and the author's own `status` prints it.
