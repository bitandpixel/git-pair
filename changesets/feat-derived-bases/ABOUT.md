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

**A commit, not the destination tip, because a tip is not a measurement point.** The base is a branch while the
parent branch is the answer — that branch is where the work above it is still being written, and an approval
against it stays comparable while the parent moves. Once the destination is the answer, the base is
`merge-base(child head, destination)`, and three things follow from writing it as a commit. Measured against
the tip, an unchanged child's span changes every time the destination takes a merge: the diff a reviewer read
is no longer the diff the next person sees. Measured against the tip, two clones of one repository print two
different spans for one changeset, differing by whenever each last fetched — the defect that made the durable
refs unusable, relocated to the surface a reviewer reads most. And the drift test needs a fixed commit to
compare trees at, which a moving name cannot give. The merge base with the child moves only when the child's
own history does or its parent lands, and those are the two moments the span is supposed to move.

Ah I see, and there could potentially be merge conflicts with newly added commits to the trunk as well right?

**The rebased child is the case that beats a ref.** A base naming the landing commit widens the child's diff
to everything the destination gained after that commit; `merge-base` does not. `TestRelinkAfterARebaseOntoTrunkShowsOnlyTheChildsOwnWork`
asserts the child's own commits are the whole span, which is the behaviour the durable ref could not give.

**An approval is a claim about content, not about a base.** The rule in PRD §21 is unchanged, and the property
is sameness of *diff content*, not of file names: `check` passes while `base...head` is the same diff under the
old base and the new one. Head is the same commit on both readings, so the two diffs carry the same content
exactly when the two merge bases carry the same tree — which is the test `landedBaseIsTheSameWork` makes, at two
`merge-base` calls and two `rev-parse`s of `^{tree}` rather than two diffs and a patch id. Same file names
would be both weaker and insufficient: two bases can name the same paths with different content under them.
`landedBaseIsTheSameWork` keeps its shape with the derived value on the "now" side, and a rebase-merge landing
still reports "not the same work".

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

## Discussion

**"Rebase on a freshly fetched main — this is already landed on remote."** The branch was on the merged main
(`4ccc9c7` is an ancestor of every commit here), and the diff that suggested otherwise came from the shared
clone's local `main`, which had never moved past M1's merge: `git pair` resolves the review base against
`main`, so both `main...current` and this review's span carried M2's whole changeset as new files. `main` is
fast-forwarded to `origin/main` now, and M3's diff against it is 16 files with nothing from M2 in it. The
review's own comment in `changesets/feat-landing-is-a-tree-fact/ABOUT.md` was the last file M3 touched outside
its own, and it is gone, so the branch no longer edits a landed changeset at all. Worth noting for the next
restack: `git rebase --onto origin/main` is the base to trust, and a local `main` that has not been fetched
makes a correct branch look like a stale one.

## Open questions

None blocking. Whether `parent.landed_in_default_branch` should stay in the JSON now that it is true whenever
`parent.landed` is — M5 removes the index it was computed from, which is the natural moment to decide.
