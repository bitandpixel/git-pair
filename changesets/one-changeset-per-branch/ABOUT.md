# A branch carries one unlanded changeset, plus the ones it is stacked on

## Summary

The model says `one branch = one changeset`. This makes it true: the rule is stated once in
`internal/changeset/shape.go`, and the three commands that can let a branch into a review ask it - `init` before it
writes, `change ready` before it offers, `check` and `integrate` as a gate reason. The PRD states the rule where it
states the model, because a rule that lives only in code is a rule each surface re-invents.

## How it is decided

`ShapeOf` is the rule with no reads: the members nothing else names are the tops, one top is one stack, more than one
top - or none, which is what mutual records leave - is the shape refused. `CheckEdges` adds the two reads the edges
cannot answer on their own, and only on the path about to refuse:

- **a chain hop through a changeset that landed**, by reading the file it keeps on the destination, because a
  three-level stack whose middle level landed looks like two unrelated directories otherwise;
- **a `base:` that names a branch**, resolved by asking which changeset directory that branch carries. This is the
  legacy file with no recorded id, and it is the case that would otherwise refuse `feature/auth` for carrying
  `feature-auth` - an id not spelled like its branch, which git-pair never required anybody to fix.

The rule reads `Resolution.Candidates`, which since M5 is what survives `ignores:` and the set operation: the tops.
`Resolution.Selected` is never consulted - it is nil exactly on the branch that most clearly carries two, and it is
populated on a branch carrying two that the ordering happens to have an answer for, which is the shape that used to
be offered. `change ready` refuses whichever directory the resolver picked.

`init` judges the shape with the record it is about to write, so a child stacked on the changeset its branch carries
is accepted and a second directory tied to nothing is not. The warning that stood there before this is gone: it read
the selection, so it said nothing about the clearest case, and it warned about what is now refused.

The self-referential base is a gate reason as well as an `init` refusal and a `status` report, so the file edited by
hand after `init` wrote it cannot be walked into the queue past a green gate. It is refused where the branch is known
rather than in `stackOf`, which has no branch to compare against.

## Evidence

`internal/cli/branch_shape_test.go`:

| Fixture | Result |
|---|---|
| `TestChangeReadyRefusesABranchCarryingTwoChangesets` | exit 1, both ids named, three ways out named; the resolver has selected one of the two and the refusal stands |
| `TestCheckGatesABranchCarryingTwoChangesets` | `ready: false`, a reason naming both ids and "no approval of its own" |
| `TestChangeReadyAcceptsAChangesetTheDestinationCarriedAfterTheBranchPoint` | offered: the second directory is landed work |
| `TestChangeReadyAcceptsAChildStackedOnTheChangesetItsBranchCarries` | offered: `base-changeset:` recorded, one stack |

Six tests failed when the rule went in, which is the discrimination check: five built the refused shape as a
convenience and moved to branches carrying their own work (`init` with a second `--id`, the two base-inference
fixtures, the check-silence fixture, the queue fixture), and one asserted the old warning, which is now the refusal
fixture in `id_test.go`. The sixth, `TestInitStillRefusesWhenTheLevelBelowRecordsNoChain`, failed for the wrong
reason and passed unchanged once the `base:`-names-a-branch read was added - the only one of the six that was a
false refusal rather than a fixture in the way.

## Cost and consequences

- No read on the ordinary path: a branch that records its stack, or carries one changeset, answers from the set the
  resolver already read.
- `check` and `integrate` now resolve the branch once more for the shape. One tree listing plus one batch object read
  on a command that already does many of both.
- A branch already in the queue carrying two unconnected changesets will start failing the gate. That is the
  intended behaviour and it is not a reason to loosen it; the way out is the one the message prints.
- `init`'s refusal exits 2 (usage) because the arguments name the branch that has the problem; `change ready` exits 1
  (repository state) like the dirty-tree and survival refusals.

## Not included

- `change combine` and `change stack` (M7). The message describes those fixes without naming commands that do not
  exist yet; when they land, the message names them.
- No migration of branches that already carry the shape, and no rewrite of unlanded files.
