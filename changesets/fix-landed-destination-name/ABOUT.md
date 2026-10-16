# fix-landed-destination-name

## Summary

A child stacked on a stack was told to merge its work into a branch that holds nothing the destination does
not. `git pair check` said _merge into `feat/hooks-check-gate`_ beside a `parent:` line saying that
changeset had landed, while `git pair change integrate` — the command the author then runs — printed
`merge into:  main`. Four surfaces printed the wrong destination, and the declaration beside them printed
the right one.

The wrong name is the branch under the parent. A record says where its changeset said it was going, and a
branch keeps that name after the work it carried has been merged, so a stack whose lower levels have been
merged one after the other leaves every record below naming finished work as a destination. The rule that
refuses such a name already existed in the destination walk, one hop up, and had the reason for its own
guard written beside it: a value "read out of a landed tree" has to be proved. The parent's `base:` is such a
value, and it entered the walk at the hop that believes the caller.

## What changed

- `internal/changeset/destination.go` gained `DestinationOfParent`: the same walk, with the first hop asked
  to prove itself — resolve the name, and refuse it when the integration branch already carries that
  changeset. `DestinationFor` keeps its old belief and delegates to the shared `destinationFor`, so the
  caller standing on its own changeset pays nothing for a value it resolved itself.
- The same file lists the destination's namespace once per walk instead of once per hop. `LandedIDs` is
  three listings of one tree, and every hop asked the same question of the same branch, so the walk
  repeated an answer that cannot change while it runs.
- `internal/cli/landing_advice.go`: `stackUnder.destination` reads the tree where the destination already
  carries the parent's directory (`Landed`, which `stackBelow` reads from that tree) and walks the parent's
  record only where it does not.
- `internal/cli/integrate.go`: `destinationNote` names the nearest changeset the walk crossed rather than
  the farthest, and says nothing when the answer came with no name at all.
- New tests: `internal/cli/landing_advice_stale_base_test.go` (a three-level stack, both lower changesets
  landed, and the same stack with the parent still unlanded) and
  `TestDestinationOfParentProvesTheRecordItIsHanded` in `internal/changeset/destination_test.go`. The cost
  comment in `internal/cli/status_stack_chain_test.go` carries the numbers this change leaves behind.

## Design decisions

**Two entry points rather than one walk that always proves.** Believing the first hop is sound for the
changeset this clone is standing on, because the caller resolved that base to compute the state it is
reporting. It is not sound for a record read out of somebody else's tree. Folding the two together would
make `change integrate` and `queue` re-prove a value they had already resolved, and would erase the
distinction the walk's own comment says matters. The alternative that looked shortest — setting
`BaseDerived` on the parent's record in the caller, so the walk skipped it — states a derivation that did not
happen, and was rejected for that.

**The landed parent's destination comes from the tree, not from the record.** `Landed` means the destination
carries the parent's directory, which is the fact the child's merge cares about. The record's claim is one
step further from the truth and can be contradicted by the very landing that produced it. This also drops a
limitation for this sentence that `DestinationFor` still carries for the unlanded case: a parent landed
somewhere other than its own base made the record's answer wrong, and no longer does here.

**The parenthetical names the nearest crossed changeset.** "Where `beta` landed" and "where `alpha` landed"
can both be true of the same destination, and the one that is useful is the work the reader is following. It
is also the pair-agreement property the tests already assert: the next step and the `merge into:` line beside
it should not name two different changesets for one destination.

**Every `DestinationFor` caller was checked, not assumed.** `change integrate` and both `queue` call sites
pass `Resolution.Selected.Changeset` — a base the resolver resolved, which is what the belief is for. The
landing sentence was the only caller handing over a record read out of a tree.

## Validation

- `go test ./internal/changeset/ ./internal/cli/` passes, and `mise run gates` passes on the whole tree.
- `TestChildOfALandedStackIsToldWhereItsOwnMergeGoes` is the reported shape: alpha landed, beta landed on
  it with `base: alpha`, gamma standing on beta. All eight readings — `status`, `check`, `change wait`,
  `review submit` and the four `--json` fields — end in `merge into main with ordinary git (where beta
landed)`, none offers `alpha` or `beta` as a merge target, and `change integrate`'s `merge into:` line
  names the same branch and the same changeset.
- `TestChildOfAnUnlandedParentWhoseBaseHasLandedWaitsOnTheDestination` is the same stack with the parent
  still unlanded, where the bug shows in the waiting sentence: both halves name the destination, and the
  landed branch is named nowhere in the line.
- `TestDestinationOfParentProvesTheRecordItIsHanded` puts one record in front of both entry points and
  pins the difference: believed, it answers `feat/alpha (base)`; proved, it answers `main (parent)`. The
  test says what to do if the two ever agree — one of the entry points should go.
- `TestStatusStackChainCostsABoundedReadPerStep` is what caught the first version of this fix, which listed
  the destination's namespace per hop and came to 13 invocations per extra ancestor against a budget of 10. With both readings in place the fixture measures 99, 106, 113 for depths 3, 4 and 5 — seven per
  ancestor, and two invocations cheaper per run than before the change, because the landed case reads the
  destination it has already read rather than a record above it.

## Known limitations

- A parent merged into a branch that is not the integration branch is still read as unlanded, because
  `stackBelow` asks about one branch. That case takes the record path, where the parent's own claim still
  decides the destination.
- Where the destination carries the parent's directory, the destination wins over the record even when the
  record names another branch. Stated deliberately: the tree outranks the claim, and `change integrate`
  permits the declaration on the same reading.
- `DestinationOfParent` proves a base by resolving it and by asking the destination whether it holds that
  changeset. A parent whose record names a _live_ changeset as its base is answered from the record, which
  is the intent of a stack that has not landed yet.

## Open questions

- None open. The one thing worth a reviewer's eye is the pair of beliefs: `DestinationFor` believing its
  first hop is now load-bearing for three call sites, and the test that pins the difference is the only
  thing that will notice if one of them starts handing over a record it did not resolve.
