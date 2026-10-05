# Destination walk: a landed parent is recognised by its id, not by the branch that carried it

## Summary

`DestinationFor` asks a stack's parents where each one landed, and it asked that question with the wrong half
of the record. A parent's file holds two halves: `base:` names the branch the parent was measured on, and
`base-changeset:` names the changeset that branch carries. The destination files its records under the second
of those — `changesets/<id>/` — while the walk compared the first, a branch name, against the list of landed
ids. On a repository whose branches are prefixed (`feat/auth`, landed as `changesets/feat-auth/`) that
comparison matched nothing, so the walk stopped at the branch and reported it as where the child's work should
land.

## Response to review (f31c01b)

Corrected. `base:` and `base-changeset:` are the pair the manifest uses; `parent:` and `parent-changeset:` are
read from files written before that pair existed, and nothing writes them. The earlier text named the older
spelling as if it were the rule, and the code comment followed it. The rule is now stated, commented, and
tested in the current vocabulary: the walk asks the destination about the `base-changeset:` id first, and about
the branch in `base:` — and the slug of that branch — only as the fallback for a record that carries a branch
and no id. `internal/changeset/destination.go` and PRD §13.1 say it that way, and the tests cover the current
pair, the older pair, and the branch-only record.

## Why

Reported while landing a three-deep stack in this repository: after two ancestors had landed on `main`, the
child's declaration printed

```
  merge into:  feat/gate-measures-contribution (where feat-gate-measures-contribution landed)
```

and `queue --json` printed the same value as `awaiting_integration[].destination`. That field is the branch
`scripts/ci/git-pair-integrate.sh` merges into, so an unattended run would have landed the work onto a branch
holding only the first ancestor. `check`'s own next action named `refs/remotes/origin/main` for the same
changeset, so the two surfaces disagreed — which is the promise §13.1 makes about them and this read broke.

The comparison was an exact `slices.Contains` of a branch name against ids. It is right only where a branch
name needs no mapping, which is the shape every existing fixture had: `TestDestinationWalksAParentChain` walks
three landed ancestors named `alpha`, `beta`, `gamma`, where the branch and the id are the same string. The
walk was covered and never tested where the two halves of a record differ.

## How it works

- `landedID(ids, baseChangeset, base)` asks the destination about the recorded id first, then the branch, then
  the branch's slug, and reports the id it matched. The order follows what each half can answer: the id is what
  the destination files, and the branch is the half that goes stale. The branch and its slug stay in the list
  because a record can carry a branch alone — the older pair, or any file whose author wrote down a branch and
  not a changeset.
- When a hop is taken the walk carries the **id** forward as the record to read next, not the branch name. The
  next hop opens `changesets/<id>/CHANGESET.yaml`, so carrying the branch would ask the destination for a
  directory it does not have, and the walk would fall back to the default branch with a `via` naming none of
  what it crossed.
- The loop guard (`seen`) is keyed on the matched id, so the guard and the hop agree about what has been crossed.
- Nothing else moved: the hop limit, the `base`/`parent`/`default` reasons, the `Unreachable` answer for a base
  that no longer resolves, and the rule that an unlanded parent's branch is a legitimate destination.

## Tests

Three shapes in `internal/changeset/destination_test.go`, each a stack three deep with branches
`feat/alpha`, `feat/beta`, `feat/gamma`, both ancestors landed, expecting `main` with `via`
`[feat-beta feat-alpha]`:

- `TestDestinationWalksAParentChainWhoseBranchNamesDifferFromTheirIds` — the current pair,
  `base:`/`base-changeset:`. Red before the fix, where the answer was `feat/alpha` with `via` `[feat-beta]`.
- `TestDestinationWalksAParentRecordedWithTheOldParentKeys` — `parent:`/`parent-changeset:`, because the
  destination keeps whatever spelling the landings it carries brought with it.
- `TestDestinationWalksAParentThatRecordedOnlyItsBranch` — the middle rung records `base: feat/alpha` and no
  `base-changeset:`, which is what pins the branch-and-slug fallback.

One shape in `internal/cli/integrate_test.go`:

- `TestChangeIntegrateNamesTheDestinationWhenBranchAndIdDiffer` — the same three-deep stack driven through the
  surfaces that act on the answer: `change integrate --json` (`destination`, `destination_source`,
  `destination_via`) and the `queue --json` row a pipeline merges into. A single landed ancestor cannot catch
  this, because one landed parent's own record names `main` directly, so the fixture lands two.

## Notes for review

- The bug is older than this branch: `git log -p internal/changeset/destination.go` predates the recent digest
  and drift-credit work. What changed is that a three-deep stack with two landed ancestors became common
  enough to walk into it.
- The fixtures write `CHANGESET.yaml` by hand rather than through `init`, because the point of each is which
  keys the record carries. `stageStacked` (the older helper, still used by the tests above these) writes the
  `parent:` pair; `stackedOn` writes the current one and omits the id when asked.

## Out of scope

- Whether a landed parent should ever be a destination at all — unchanged, and still answered by the records
  plus `--default-branch` for a repository whose trunk is not the one git-pair would pick.
- Tidy of `changesets/<id>/` after a landing, which stays the destination owner's call.
