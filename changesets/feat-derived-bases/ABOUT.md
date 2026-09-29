# feat-derived-bases

## Summary

What a changeset is measured against is derived from the integration branch, and the rule that produced the
answer travels with it. The stacked child of a landed parent no longer needs a ref of git-pair's own to know
where its own work begins.

This is milestone M3 of `docs/plans/simplify-architecture/plan.md`. It removes the last read of
`refs/git-pair/*` from the surfaces an author uses every day: the base of a stack, the state of a parent, and
the uniqueness of an id.

## What changed

`internal/changeset/base.go` is new: `BaseFor(ctx, repo, cs, head, db)` answers one question with
`Base{Ref, Why, Derived, ParentBranch}`.

1. The parent branch, while it exists and the destination does not carry its directory.
2. `merge-base(childHead, destination)` once the parent has landed, or once its branch is gone — the run the
   child shares with the destination, which is the range the child's own commits sit in.
3. The destination itself when neither resolves. `ErrNoDefaultBranch` when the destination cannot be named.

`Changeset` gained `BaseWhy`, and the resolver caches the derivation in `applyBases` (which replaces
`relinkStacks`) so the surfaces print what one read produced rather than each working it out again.

`status` prints the rule beside a derived base — `Base: 4f2b8c1 — the parent alpha landed, so the run this
branch shares with main` — and `--json` carries `base_ref` and `base_why` beside `base`, which keeps its
meaning as what the changeset recorded about its stack.

`parentLanded` and `parentGone` in `internal/cli/stacked.go` ask `LandedChain` and `CarriesDir` instead of a
ref, `root.go`'s chain read derives its base the same way, and `explainBrokenStack` asks the destination
whether the parent landed there. `refuseTakenID` takes an id's uniqueness from the directory that carries it,
active or landed, and `DirectoryAt` gained `Landed` to tell a tidied landing apart from an uncommitted
deletion. `init` refuses a landed id from the destination's tree before it writes anything.

## Design decisions

**A derived base is printed with its rule.** `Base: 4f2b8c1` alone asks the reader to reconstruct why the
measurement starts there. `base_why` is the same sentence the human sees, so an agent does not have to guess
which of three rules ran.

**The rebased child is the case that beats a ref.** A base naming the landing commit widens the child's diff
to everything the destination gained after that commit; `merge-base` does not. `TestRelinkAfterARebaseOntoTrunkShowsOnlyTheChildsOwnWork`
asserts the child's own commits are the whole span, which is the behaviour the durable ref could not give.

**An approval is a claim about content, not about a base.** The rule in PRD §21 is unchanged: `check` passes
while `base...head` names the same files under the old base and the new one. `landedBaseIsTheSameWork` keeps
its shape with the derived value on the "now" side, and a rebase-merge landing still reports "not the same
work".

**A parent merged only into a release branch is not landed**, so its child keeps measuring against the parent
branch. This is D1 applied to the stack. The durable ref offered a hedge — "landed, not reachable from main" —
that claimed a landing the integration branch had not received; the child now gets no claim until the
directory arrives.

**An id belongs to a directory, not to a ref name.** `refs/git-pair/archive/<id>` claimed a name for work that
might never have been readied. The directory is the work, and `.landed/<id>` holds a name until it is tidied.

## Validation

`mise run gates`: the sharded suite, `scripts/gates/e2e-29.sh`, `pty-walkthrough.sh`, `ci-integrate.sh`.

New and rewritten tests, each named for what it now proves: seven unit tests for `BaseFor` (live parent
branch, landed parent, gone parent branch, rebased stack, fallback, `ErrNoDefaultBranch`, unstacked recorded
base); `TestADerivedBaseReplacesTheParentBranchWhileTheBranchIsHere`;
`TestADerivedBaseNeedsNoRecordInThisClone`, where both of the parent's durable refs are deleted from the
repository and the base is the same commit; `TestAParentMergedOnlyIntoAReleaseBranchIsNotLanded`;
`TestAParentLandedWithoutARecordIsStillLanded`;
`TestAParentWhoseWorkLandedWithoutItsDirectoryMovesTheChildsGround`; and two for id uniqueness — one for a
landing in the working tree, one for `changesets/.landed/<id>` on the integration branch.

## Known limitations

`internal/changeset/destination.go` still reads a parent's `CHANGESET.yaml` from the landing commit's tree, so
a landing that carried no directory loses the parent link there. `status`'s `Stack:` block still prints
`record:` per ancestor from the ref index, with `--json`'s `stack[].integration` beside it. Both are listed in
M5's tasks, where the code around them is rewritten.

A squash, cherry-pick or rebase-merge landing whose commit carries no directory still leaves the changeset
live, and its chain read has no markers to report. PRD §13 states the limit; the wording lands with M5.

## Open questions

None blocking. Whether `parent.landed_in_default_branch` should stay in the JSON now that it is true whenever
`parent.landed` is — M5 removes the index it was computed from, which is the natural moment to decide.
