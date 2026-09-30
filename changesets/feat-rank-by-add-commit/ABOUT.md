# Rank candidates by the commit that added the directory, and only when ranking is needed

## Summary

The tie-break between changeset directories that the records did not separate is now the commit that **added** each
directory to this line, not the commit that last edited one. The ranking read also moved inside `choose`, so a branch
whose records already decide the answer makes no history read at all.

## What this changes

`distanceFromTouch` becomes `distanceFromAdd`: `git log -1 --diff-filter=A <rev> -- changesets/<id>`, then
`rev-list --count <add>..<rev>`. It asks for the newest commit adding something under the directory, which is still
the right answer after a rebase, a cherry-pick, or a directory deleted and created again. The `-1` meaning is kept: a
line that reaches a directory only through a graft or a shallow boundary ties with other absences instead of
inventing an order.

`nearness` becomes `rankByCreation`, and it is called from `choose` only when more than one candidate survived the
filters. `choose` therefore takes the revision and can report an error, and the function splits into
`filterCandidates` (the two record passes, which need no git) and `decide` (the ordering and the answer), so
`Resolution.WithIgnores` keeps its simulation free of history reads.

A third rule fell out of the change and is worth its own line. When a drop would remove every candidate, the records
contradict each other; that is now reported, and the answer is the candidate list with no selection. Before, that
case reached the tie-break, which picked whichever directory was created later. Creation order is not what the author
meant, so it does not get to answer a contradiction.

## Evidence

`internal/changeset/add_order_test.go`:

- `TestTheChangesetAddedLastIsTheWorkInTheBranch` - a child branch carrying its parent's directory, with the last
  commit editing the parent's `ABOUT.md`. The child is the answer. The fixture uses a `base:` naming a branch, which
  the ancestor drop cannot match against a slug, so the ordering is the only thing that can decide.
- `TestChangesetsAddedByOneCommitStayAmbiguousWhenOneIsEditedAfterwards` - two directories from one commit stay
  undecided even when one of them is edited afterwards. Under the last edit, that later edit broke the tie.
- `TestTheRankingReadHappensOnlyWhenCandidatesRemain` - no `rev-list --count` invocation for a branch with one
  changeset, none for a stack whose recorded link decides, and exactly one per candidate when two remain. The stack
  case is the regression guard: the walk used to run over both directories before the filter reduced them to one.

Both ranking fixtures were checked against the old measurement: with `--diff-filter=A` removed they fail, and with it
in place they pass.

`TestResolveMutualIgnoresKeepsBothCandidates` is the existing test that caught the contradiction case. It passed
before by accident - both directories were last edited by the same commit, so the tie-break found a tie - and now
passes because the rule says to refuse.

`internal/gittest/spawn.go` gains `SpawnShimLines`, which returns the invocations as well as the count. Counting
alone cannot tell a command that skipped a read from one that made a read it did not need.

Measured cost of one ranking read in this repository at 484 commits: 2 to 3 ms per directory, for either commit fact.

## Not included

- No change to what the two filters remove or to the order between them, which landed with `feat-ignores-before-inference`.
- No change to `Distance` as a field, or to anything that reads it.
- No change to how a landed changeset is recognised. This is about candidates, which are by definition not landed.
