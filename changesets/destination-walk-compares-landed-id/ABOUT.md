# Destination walk: a landed parent is matched by id, not only by branch name

## Summary

`DestinationFor` asks a stack's parents where each one landed, and it asked that question with the wrong
string. A parent records a **branch** in `parent:` (`feat/auth`); the destination files its records under
changeset **ids** (`changesets/feat-auth/`). The walk compared the branch name against the list of landed ids,
matched none, stopped at that branch, and reported it as where the child's work should land.

## Why

Reported while landing a three-deep stack in this repository: after two ancestors had landed on `main`, the
child's declaration printed

```
  merge into:  feat/gate-measures-contribution (where feat-gate-measures-contribution landed)
```

and `queue --json` printed the same value as `awaiting_integration[].destination`. That field is the branch
`scripts/ci/git-pair-integrate.sh` merges into, so an unattended run would have landed the work onto a branch
holding only the first ancestor. `check`'s own next action named `refs/remotes/origin/main`, so the surfaces
disagreed about the same changeset.

The comparison is exact today: `slices.Contains(ids, base)` where `base` is the branch name a parent recorded
and `ids` are the destination's record names. They are equal only for a branch whose name needs no mapping, so
every prefixed branch (`feat/…`, `fix/…`) misses. The existing tests use branch names equal to their ids, so
the walk was covered and never tested in the shape that fails.

## How it works

- `landedID(ids, base)` returns the id that names the branch the base records, or nothing. It asks for the
  name as written and for `SlugFromBranch(base)`, because a `base:` can record either a branch or a changeset.
- When a hop is taken, the walk carries the **id** forward as the changeset to read next, not the branch name.
  The next hop reads `changesets/<id>/CHANGESET.yaml`, so carrying the branch would ask the destination for a
  directory it does not have, and the walk would fall back to the default branch with a `via` that named none
  of what it crossed.
- The loop guard (`seen`) is keyed on the id, so the guard and the hop agree about what has been crossed.

## Tests

- `TestDestinationWalksAParentChainWhoseBranchNamesDifferFromTheirIds` (`internal/changeset`) — a stack three
  deep with branches `feat/alpha`, `feat/beta`, `feat/gamma` and ids `feat-alpha`, `feat-beta`, `feat-gamma`,
  both ancestors landed: destination `main`, reason `parent`, `via` `[feat-beta feat-alpha]`. Red before the
  fix, where it returned `feat/alpha` with `via` `[feat-beta]`.
- `TestChangeIntegrateNamesTheDestinationWhenBranchAndIdDiffer` (`internal/cli`) — the same shape driven through
  the commands that act on the answer: `change integrate --json` (`destination`, `destination_source`,
  `destination_via`) and the `queue --json` row a pipeline merges into. The single-ancestor case cannot catch
  this, because one landed parent's own record names `main` directly, so the fixture lands two.

## Notes for review

- Nothing else in the walk changed: the hop limit, the `base`/`parent`/`default` reasons, and the
  `Unreachable` answer for an unreadable base are as they were.
- PRD §13.1 states the rule in the numbered list where the walk is specified; README's `change integrate`
  paragraph now says the printed target and the queue's `destination` field are the same derived value.
- The bug is older than this branch: `git log -p internal/changeset/destination.go` predates the recent
  digest and credit work, which only made a three-deep stack with two landed ancestors common enough to hit it.

## Out of scope

- Whether a landed parent should ever be a destination at all — unchanged, and still answered by the records
  plus `--default-branch` for a repository whose trunk is not the one git-pair would pick.
- Tidy of `changesets/<id>/` after a landing, which stays the destination owner's call.
