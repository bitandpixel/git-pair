# feat-landing-is-a-tree-fact

## Summary

Landing stops being a fact git-pair keeps and becomes a fact it reads. A changeset has landed when the
integration branch's tree carries `changesets/<id>/`, and the chain behind that directory — the run on the
destination's first-parent line — is where its review verdicts come from. No read of
`refs/git-pair/archive/*` or `refs/git-pair/integrations/*` is left on the paths that answer "has this
landed", "what did the reviewer say", or "is this offered".

This is milestone M2 of `docs/plans/simplify-architecture/plan.md`, stacked on M1 (`landed-tree-model`).

## What changed

- `internal/changeset/chain.go` (new): `LandedChain` derives the run a destination carries behind a
  changeset directory — exact for a merge landing (`landing^2` and `merge-base(landing^1, landing^2)`),
  bounded by the directory's own history for a linear one, and `Squash` when the whole span is one commit.
  `CarriesDir` is the single containment question every surface now asks.
- The write gate (`internal/marker/marker.go`) and the integration gate (`internal/cli/check.go`) ask the
  destination's tree. `marker` takes the destination from its callers, because a gate that reads a tree has
  to be told which tree.
- `status --changeset <id>` with no branch carrying the changeset reads the chain instead of the archive
  ref, and `DirAt` resolves either spelling of the directory so a tidied changeset is still found.
- `queue`'s finding changes identity: `LANDED, UNREVIEWED` replaces `LANDED, UNRECORDED`. The old finding
  was that `integration record` had not been run — closable by running it. The new one is that the
  integration branch holds a changeset whose chain carries no permitting verdict, which no command closes,
  so the heading prints a read (`status --changeset <id>`) instead of an invocation.
- `queue`'s orphan classification and its stacked-parent note read the tree; `parentInTrunkUnrecorded`
  becomes `parentInDestination` and offers no command.
- `status --json` field set: `landed`, `landed_commit`, `landed_branch`, `chain_base`, `chain_head`,
  `reviewed` replace `integrated`, `integrated_commit`, `integration_ref`, `integrated_in_default_branch`,
  `integrated_default_branch`, `archive_ref`, `archive_commit`. All six are present on every document,
  empty rather than absent. `queue --json` and the `status` exit-2 document carry `landed_unreviewed`.
- `scripts/gates/e2e-29.sh`'s durable-layer assertions were rewritten against the tree, and README's JSON
  tables and the sentences around them moved with the behaviour.

## Design decisions

**Landed means the integration branch, and a merge into some other branch leaves the changeset live.** The
durable record refused `change ready`, `change unready`, `review submit`, `change abandon` and
`change integrate` once any record existed, whatever branch it named. The tree model does not, and that is
the wanted behaviour: the release line can revert the merge, the changeset may still have to reach `main` on
its own, and until the integration branch holds the directory the work is unfinished. Two questions, two
trees, kept apart deliberately — landed-ness reads the integration branch (`CarriesDir`, the gate, `check`,
`queue`), and the merge target reads the changeset's own base and stack (`changeset.DestinationFor`, printed
as `merge into:`). Pointing the write gate at the destination would retire a changeset on a release-line
merge, which is the behaviour this milestone rejects.

**A chain read reports the state the chain's markers derive.** A landed merge landing now prints `APPROVED`.
PRD §13.4's rule that the archive is not a second source of state was about walking a ref *outside* the span
and reporting it as live work; the chain is the span, so there is one source. What keeps the printed state
safe is the gate, which refuses on the landing before it asks the drift question.

**A squash or cherry-pick landing keeps nothing.** It carries the tree and leaves the history behind, so
`chain_base`/`chain_head` are empty and `reviewed` is false whatever happened on the branch. The §29 golden
workflow lands in exactly that shape and now asserts `WORKING` rather than `APPROVED`. PRD §13 has to state
the limit in the commit that deletes the refs (M5), which is why that claim is not made here.
> what will the end status be for a changeset that's been squash-merged into trunk?
**Answer, measured against a scratch repository with this branch's binary.** `status --changeset <id>` reports
`landed: true`, `landed_commit` the commit that carried the directory in, `landed_branch: main`,
`chain_base` and `chain_head` empty, `reviewed: false`, and `next_action` "landed at `<sha>` in main: nothing
further is recorded for a changeset that has landed". `queue` lists it under `LANDED UNREVIEWED` with the
reason "the landing carried the directory in one commit, so no review markers came with it". `check` run on a
branch that still carries the directory exits 2 with the landed sentence and the `status --changeset`
pointer.

`state` is `WORKING`, and that is the part of the answer worth arguing about. `state` is derived from the
markers in the chain, and a squash carries none, so the honest derivation has nothing to report. The fields
that answer "what happened to this work" are `landed`, `landed_commit` and `reviewed`; `state` answers a
different question — what the markers in the span say — and inventing a state for the landing would make
landing a state, which PRD §13 and the skill both refuse to do. One sentence is awkward as measured: the
header says `Branch: none (read from the landed chain)` and the reason says "no git-pair lifecycle markers
on this branch". Both are true of the derivation and neither reads well alone; the rephrasing belongs in M5,
which rewrites this surface again, so it is not changed here.

**The cost contract moved honestly.** The queue's landing report used to cost nothing per landing because
it read one ref listing. It now costs a bounded few reads per landing — measured at 11 git invocations each,
bounded at 14 — which is the price of the answer being true in a clone that has fetched nothing but the
destination. `TestReviewQueueCostPerLandedChangesetIsBounded` measures it.
> is this cost per-changeset? or for the whole report?
**Per landed changeset, on top of a fixed cost for the report.** The test runs `queue --json` twice over one
repository — once with an empty destination, once with the destination carrying 300 landed directories — and
bounds `(withDirs - empty) / 300`. The number under the bound is the marginal cost of one landing; `empty` is
the report's own cost and is deliberately not bounded, because it does not grow with the landings. Measured:
11 invocations per landing in this repository's fixture and 10.0 in a smaller scratch one, bounded at 14;
the report itself cost 18 invocations in the same scratch run. So a destination with fifty landings costs
roughly 570 invocations, which is the price of the answer being true in a clone that fetched nothing but the
destination.

## Validation

- `go test ./...` green, including the sharded run `mise run check`.
- One test per landing shape at the derivation level (`internal/changeset/chain_test.go`): merge,
  fast-forward, rebase, squash, tidied directory, and a landing on a branch that is not the destination.
  At the CLI level (`internal/cli/landed_test.go`): a merge landing (chain read, verdict, `reviewed` true),
  a squash landing (the limitation asserted, not hidden), and the negative — a reviewed merge landing
  produces no finding.
- `TestLandedChangesetRefusesFurtherWork`: five commands refuse, exit 1, nothing committed.
- `TestLandingOnAnotherBranchLeavesTheWorkInProgress` and `TestARecordAgainstAnotherBranchClaimsNoLanding`:
  the release-line decision above, on the command surface and the reporting surface.
- `TestDurableRefsDisagreeWithTheTreeAndChangeNothing`: both ref families written, both lying, every answer
  unchanged. This is the test that says M5 is safe.
- `scripts/gates/e2e-29.sh`: 121 assertions green, including both `LANDED UNREVIEWED` sentences, the
  branch-deleted `status --changeset` read, and the record-that-changes-nothing.
- `mise run gates` (check, e2e-29, pty-walkthrough, ci-integrate) — see the note in this changeset's review
  thread for the run this branch was handed over with.

## Known limitations

- A merge whose directory arrived on the first parent (a conflict resolved in the merge itself) has no
  second-parent chain; the linear derivation is used, which is honest but coarser.
- `chain_head` for a linear landing is the newest commit that still carries the directory, not the commit the
  branch stopped at: a fast-forward leaves no landing commit, so the end of a linear run is not derivable
  from the destination. `Chain.Linear` names the case.
- `--fetch` still exists on `status`, `queue` and `check`, and the record/publish commands still write and
  read refs. They become inert weight in M5, which deletes them.
- PRD.md still describes the durable layer in 120 places. M6 rewrites it; the README sections that were
  contracts on the JSON shape and the finding moved with the behaviour here.

## Open questions

- Should `queue`'s `LANDED UNREVIEWED` heading distinguish "the branch was merged without review" from "the
  review happened and the landing kept none of it" more loudly than a sentence? The reason line does the
  work today; the shape is the same heading either way.
> could we have a REVIEW DISCARDED status that's derivable?
**Not derivable in the sense this plan requires.** What the destination proves is that the chain behind the
directory carries no permitting verdict. At least three histories produce exactly that tree: never reviewed;
reviewed and squash-merged; reviewed, merged, and then rewritten away. `REVIEW DISCARDED` would name the
second of the three, and the destination cannot tell them apart — that is the limit PRD §13.3 states in the
milestone that deletes the refs.

It *is* derivable in one narrower case: the branch is still here, so its own markers still carry the approval.
That is precisely the class of answer this plan retires — true in the clone that did the merge, false in a
fresh clone that fetched only the destination — and the queue's answer has to be the same in both. So the
finding stays the disjunction, and the reason line says which shape the *landing* is: a chain that carries no
verdict, or no chain at all. Where the distinction is worth keeping, the fix is upstream of the read:
`--no-ff` keeps the chain in the destination, and `change tidy` (M4) moves the directory on a branch that
still holds it.
