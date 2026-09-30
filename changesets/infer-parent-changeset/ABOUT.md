# infer-parent-changeset

## Summary

`git pair init` records `parent:` and `parent-changeset:` when the base it is handed names a branch carrying
exactly one unlanded changeset, and `git pair check` recommends the same declaration for changesets whose
file predates the rule. This is M1 of `docs/plans/derive-destination-from-tree/plan.md`.

## What changed

-   `internal/cli/init.go`: once the base is resolved, a base naming a branch that is not the integration
    branch is asked the question `--parent` already asks. One unlanded changeset on it means the work is
    stacked, so the pair PRD §21 spells is recorded and the run says so. Two or more means nothing can tell
    which one is the parent, so the base stands as written and every candidate is named.
    `parentChangesetOn` now takes the resolved integration branch and returns the candidates it looked at,
    which is also what lets the existing `--parent` warning name them instead of saying only that it could
    not pick.
-   A base three levels down still reads as a stack, which it did not. A branch created from its parent's
    branch carries the whole unlanded ancestry in its tree, so the base carried a changeset per level and
    `init` refused to pick one. A candidate another candidate records as its own parent is a level of the
    stack rather than a second thing living on the branch, so it is dropped before anything is chosen. The
    drop reads the recorded chain and nothing else, which is what keeps two siblings sharing a branch a
    refusal.
-   `internal/cli/check.go`: `recommendations` in `--json` — an array in both verdicts, for the reason
    `reasons` is one — and `recommend:` lines on the human surface. Computed after the gate, never inside it.
-   PRD §21: the rule, and why a base naming a branch is not the same fact as a parent naming one.

## Design decisions

**The pair is recorded, not the id alone.** `parent-changeset:` beside a plain `base:` would break the
measurement base rather than improve it. `BaseFor` rule 1 keeps measuring against a live parent only when
`parent:` names it; with an id and no branch, the derivation falls to `merge-base(child, destination)`, which
while the parent is still unlanded is the point the whole stack forked from trunk — the child's diff would
then contain the parent's work. `renderMetadata` dropping `parent-changeset:` when `parent:` is absent is the
same rule from the writer's side.
**`parent:` and `base:` are not both recorded, and cannot be.** A CHANGESET.yaml carries one or the other:
`parent:` *is* the base (PRD §21), and `stackOf` refuses a file naming both. The pair this changeset records is
`parent:` with `parent-changeset:`, and the second half is what lets the first be read after the parent is
gone - a branch name cannot be checked once its branch is deleted, while a changeset id is answered from the
destination's tree. The other spelling, a plain `base:` naming the parent branch, is not a weaker version of
the same statement: it is a measurement base, so it keeps naming finished work after the parent lands, which
is the state where `check` names a merged branch as the destination.

**The filter reads the chain, so it stops where the record stops.** With nothing recorded below the base, an
ancestor and a sibling are the same shape - two directories, neither landed - and the answer stays the warning
that names both for the author to pick. `Candidate.Distance`, the nearness the resolver computes when a
revision carries more than one, orders them by which directory this branch touched last; that says which work
is live, not which is a parent, and an ordering is not evidence of a relationship.
is there a distinct reason that base: and parent-changeset: cant be used, and remove the parent: concept?

**The branch is the live half, and an id cannot replace it.** A file with `base:` and `parent-changeset:` and no
`parent:` is not rejected, and the id in it is read by nothing: `stackOf` takes `parent:` as the base, has no
branch to attach the id to, and drops the field at parse time - so `check` recommends the pair for that file the
same way it recommends it for any plain `base:`. The halves answer different questions, and neither one can be
derived from the other. `parent:` names a ref, which is what makes a live parent measurable (`BaseFor` rule 1
takes the diff against the parent's branch tip) and what makes "your parent moved since this approval" and "push
here" sayable at all. `parent-changeset:` names a directory in the destination's tree, which is the half that
survives the parent's branch being deleted and the half the destination rule reads. Dropping `parent:` means
every consumer resolves id to branch by scanning this clone's branches for a directory: many-to-one, and a
different answer in a fresh clone than in one holding stale refs, which is the property this repository deleted
its durable ref layer to be rid of. Dropping the id is the same loss seen from the other side: once the branch
is gone there is nothing left to ask the tree about.

**A recommendation, not a reason.** A changeset written before this rule existed must not be held by it, and
which key a file chose to name its parent with is not a condition a merge should depend on. `check` prints
advice the reader can refuse.

**Trunk answers without reading the repository.** The recommendation returns early for a base naming the
integration branch, which is the common case, so `check` costs what it cost before.

**`--set-parent` stays the author's flag.** The inference records a stack that was always there. It does not
restack a changeset that had recorded a different parent, which remains an explicit act.

## Validation

-   `TestInitLooksPastTheAncestorsAStackedBaseCarries` builds the three-level stack the review described, each
    branch created from the branch under it, and asserts the pair is recorded for the top of it.
    `TestInitStillRefusesWhenTheLevelBelowRecordsNoChain` takes the recorded chain away and asserts the authored
    base stands, both candidates are named, and no stack is guessed.
-   `go test ./...` green on this tree. The six new tests: the pair recorded from a one-candidate base, no
    guess and both candidates named at two, trunk still a plain base, the recorded file and the measured base
    unmoved when the parent later gains a second changeset, `check` recommending the declaration in both
    surfaces without writing it as a reason, and `check` silent when the parent is recorded or there is no
    stack.
-   `internal/cli/change_test.go`'s `TestChangeInitRecordsRequestedBase` asserted the old spelling (`base:`)
    for the shape this changeset now records as a stack; it asserts the pair now.
-   `scripts/` has no `init --base <branch carrying a changeset>` usage, so the gate scripts needed no change.
-   `mise run gates` on this head: reported in the branch history below.

## Known limitations

-   The lookup is `refs/heads/<base>`. A base naming a remote-only branch (`origin/feature-x`, or the
    qualified ref) is recorded as written and gets no recommendation. The same question could be asked of that
    tree; it is left alone rather than answered by a guess about which spelling was meant.
-   The recommendation costs one `Resolve` per `check` on a non-trunk base. `internal/cli/cost_test.go` bounds
    `queue`, not `check`, so nothing asserted the old ceiling.

## Open questions

None here. The live-and-landed tie — a base whose branch is still live while a same-slug changeset landed
elsewhere — belongs to M2 of the plan and is recorded there as the decision it needs.
