# feat-landing-is-a-tree-fact

## Summary

Landing stops being a fact git-pair keeps and becomes a fact it reads. A changeset has landed when the
integration branch's tree carries `changesets/<id>/`, and the chain behind that directory — the run on the
destination's first-parent line — is where its review verdicts come from. No read of
`refs/git-pair/archive/*` or `refs/git-pair/integrations/*` is left on the paths that answer "has this
landed", "what did the reviewer say", or "is this offered".

The verdict is strict about the same thing the gate is strict about: an approval names a commit, and the
destination is reviewed only if it holds that commit as well as the approval. A run replayed between the
approval and the landing — a rebase merge, or an author rewriting under an approval — therefore lands
unreviewed, along with the squash that brings no marker at all.

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
- `queue`'s finding changes identity: `LANDED UNREVIEWED` replaces `LANDED, UNRECORDED`. The old finding
  was that `integration record` had not been run — closable by running it. The new one is that the
  integration branch holds a changeset it holds no approval of, which no command closes, so the heading
  prints a read (`status --changeset <id>`) instead of an invocation.
- `landingLicence` (`internal/cli/landed.go`) is the single answer to that question, used by `queue`'s
  finding and by `status`'s `reviewed` field so one read cannot report the two apart. It asks the chain what
  it recorded, then asks `merge-base --is-ancestor` whether the destination holds the commit the approval
  names — of the destination's *tip*, not of `landed_commit`, which is the first commit of the run that
  carried the directory and usually sits behind the approval it is checked against. The reason line names
  which of the three findings it is, and a reason that cannot be checked says so instead of implying that no
  one looked.
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

**An approval licenses the commits it names, so a replayed landing is unreviewed.** Three shapes fail
`reviewed`, and the reason line separates them: the chain holds no verdict; the landing brought no marker
commits at all (a squash, a cherry-pick), which leaves `chain_base`/`chain_head` empty; or the chain holds an
approval whose commit the destination does not hold, which is what a replayed run looks like. The third is
the same test `check` applies to a live branch, and taking it strictly is what keeps the two surfaces from
answering the same question twice. Strictly also means an approval that names no commit — written before
`Review-Head` existed — proves nothing, so it reads false. `state` keeps its own meaning through all of it:
the chain's markers derive `APPROVED`, and the queue's reason says the approval does not cover what landed.

**The strict answer is the only one available after a merge, and it is why the gate runs before one.** A run
approved and then rebased, and a run the merge itself replayed, leave identical commits behind: measured on
two fixtures whose only difference was who did the rebase, both land the same marker SHA and the same
recorded head, and both read the same way. Nothing in the destination separates an author who rewrote under
an approval and was merged anyway from an integrator who pressed rebase on an approved branch. So the
destination answers the strict question or it answers nothing, and the distinction that still existed — the
live branch, with its pre-rewrite commits in front of it and a re-approval still possible — is exactly where
`check` spends it. The §29 golden workflow lands by squash and so asserts `WORKING` rather than `APPROVED`.
PRD §13 has to state these limits in the commit that deletes the refs (M5), which is why that claim is not
made here.

**The cost contract moved honestly.** The queue's landing report used to cost nothing per landing because
it read one ref listing. It now costs a bounded few reads per landing — measured at 11 git invocations each,
bounded at 14 — which is the price of the answer being true in a clone that has fetched nothing but the
destination. `TestReviewQueueCostPerLandedChangesetIsBounded` measures it, in both shapes the queue meets:
landings that came in through one commit (11 reads each, bound 14) and landings whose chain carries markers
(16, bound 21). The second segment came with the verdict rule, because the first could not reach the read the
rule adds and so could not have bounded it.

## Validation

- `go test ./...` green, including the sharded run `mise run check`.
- One test per landing shape at the derivation level (`internal/changeset/chain_test.go`): merge,
  fast-forward, rebase, squash, tidied directory, and a landing on a branch that is not the destination.
  At the CLI level (`internal/cli/landed_test.go`): a merge landing (chain read, verdict, `reviewed` true) and
  a squash landing (the limitation asserted, not hidden), plus the three the verdict rule needs — a merge-commit
  landing keeps its review (`TestAMergeCommitLandingKeepsTheReviewItCarried`: this repository lands that way, and
  it is the case a stricter rule would have to be wrong about), a replayed landing does not
  (`TestAReplayedLandingIsUnreviewedBecauseTheApprovedCommitsDidNotArrive`: `reviewed` false, `state` still
  `APPROVED`, the chain read intact, and a reason naming the commit that did not arrive), and an approval whose
  `Review-Head` trailer has been stripped (`TestAnApprovalThatNamesNoCommitLicensesNothing`, whose reason says
  the approval names no commit rather than implying that nobody looked).
- `TestLandedChangesetRefusesFurtherWork`: five commands refuse, exit 1, nothing committed.
- `TestLandingOnAnotherBranchLeavesTheWorkInProgress` and `TestARecordAgainstAnotherBranchClaimsNoLanding`:
  the release-line decision above, on the command surface and the reporting surface.
- `TestDurableRefsDisagreeWithTheTreeAndChangeNothing`: both ref families written, both lying, every answer
  unchanged. This is the test that says M5 is safe.
- `scripts/gates/e2e-29.sh`: 121 assertions green, including both `LANDED UNREVIEWED` sentences, the
  branch-deleted `status --changeset` read, and the record-that-changes-nothing. The step that reads the
  author's clone after another clone landed and published failed on the second review round, and the product
  was innocent: the note it looks for was in the output, and `set -o pipefail` reported `grep -q`'s early exit
  as the producer's SIGPIPE. Six sites in `e2e-29.sh` and three helpers in `pty-walkthrough.sh` piped a live
  git-pair or `python3` process straight into `grep -q`; all nine now capture first and match against the
  capture. No assertion was weakened — one that reports failure while its own text is present is not an
  assertion, and `refuse` in particular would have passed on the SIGPIPE.
- `mise run gates` (check, e2e-29, pty-walkthrough, ci-integrate) — see the note in this changeset's review
  thread for the run this branch was handed over with.

## Known limitations

- **An approval that names no commit reads as unreviewed.** `Review-Head` is what the verdict is checked
  against, and markers written before that trailer existed do not carry it, so `reviewed` is false for work
  that was reviewed. Two of the twenty-four changesets in this repository's own trunk are in that shape
  (`docs-marks-are-remembered-locally`, `fix-exit-code-wording`), which is the price of the rule being
  strict about what it cannot check rather than generous about it. The other twenty-one stay reviewed: this
  repository lands by merge commit, which carries the approved commits, so the finding fires on replays and
  on nothing else.

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

## Discussion

Reviewer notes and the answers to them, kept out of the design prose above. Round 1 is review 48ff349,
round 2 is review ef0bc65.

### Round 1

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

> is this cost per-changeset? or for the whole report?
**Per landed changeset, on top of a fixed cost for the report.** The test runs `queue --json` twice over one
repository — once with an empty destination, once with the destination carrying 300 landed directories — and
bounds `(withDirs - empty) / 300`. The number under the bound is the marginal cost of one landing; `empty` is
the report's own cost and is deliberately not bounded, because it does not grow with the landings. Measured:
11 invocations per landing in this repository's fixture and 10.0 in a smaller scratch one, bounded at 14;
the report itself cost 18 invocations in the same scratch run. So a destination with fifty landings costs
roughly 570 invocations, which is the price of the answer being true in a clone that fetched nothing but the
destination.

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
ok I think this is fine. If the user of this library wants to have accurate review history, they'll have to use
a different merge strategy than squash-merge. I think this is fine, because squash-merge likely means they already
do not care about a meticulous history, and more of a streamlined trunk. The one case I'm still unsure about is the
rebase-merge. Do we treat that as UNREVIEWED as well, or are we planning to somehow derive the review state from trailers?
And can you move all these reviewer notes and answers to a Discussion section below?

### Round 2

> ok I think this is fine. If the user of this library wants to have accurate review history, they'll have to use
> a different merge strategy than squash-merge. I think this is fine, because squash-merge likely means they already
> do not care about a meticulous history, and more of a streamlined trunk. The one case I'm still unsure about is the
> rebase-merge. Do we treat that as UNREVIEWED as well, or are we planning to somehow derive the review state from trailers?
> And can you move all these reviewer notes and answers to a Discussion section below?

**A rebase-merge keeps the review. Measured with this branch's binary, in two shapes.** A rebase replays the
branch's commits, and the markers *are* commits, so `review: approve` arrives on the destination as a commit
with its trailer intact and the chain walk finds it. Rebase, fast-forward the destination, delete the branch:
`state: APPROVED`, `landed: true`, `chain_base` and `chain_head` both naming the replayed run,
`reviewed: true`, and `queue` reports no `LANDED UNREVIEWED`. Repeated with a rebase onto a `main` that had
moved on, so every SHA on the branch was rewritten: same answer. Deriving the verdict from trailers in the
chain is not a plan for later, it is what the read already does — there is nothing else for it to read, and
M5 deletes the refs that were the alternative.

So `UNREVIEWED` is reserved for the one case where no marker commit arrives at all: a squash or a
cherry-pick, which is a single fresh commit with no run behind it. The rule is about whether the reviewed
run is present in the destination, not about which merge button was pressed. One detail of the fast-forward
shape is already in Known limitations: with no merge commit, `landed_commit` names the first commit of the
replayed run that carries the directory, and `chain_head` the newest one still carrying it.

**Keep the two fields apart, because the rewrite treats them differently.** A marker carries
`Review-Changeset` (the name) and `Review-Head` (the commit the reviewer looked at), and they do different
work. Identity runs on the name: `lifecycle` admits a marker only when `Review-Changeset` is this slug, and
the chain selects commits by whether the tree carries `changesets/<id>/`. Nothing selects by SHA, and
`reviewedHead` checks the trailer's shape without resolving it, so a rewrite cannot detach a verdict from
its changeset — which is the whole reason the answer above comes out `reviewed: true`. The drift question
runs on the SHA, and there the rewrite is fatal on purpose: rebase a *live* branch after an approval, without
landing it, and `check` refuses — "review `<sha>` reviewed `<head>`, which is no longer in this history: the
branch was rewritten since the review, so the approval does not license integration". Measured both ways:
landed, the verdict reads in a fresh `--no-local` clone where the commit `Review-Head` names is not even
present; unlanded, the same rewrite costs the head its licence. A rebase keeps the record attached to the
changeset and does not keep the approval current for work that has not landed.

**Reversed after this answer, and the answer above is left as it was written.** The owner read the split the
other way: a rebase-merge landing, and a merge of a branch whose approval had already lapsed, should both read
`reviewed: false`. That is the strict half now in `landingLicence`, and the two shapes it names are the two
that leave identical commits behind, which is what makes it the only rule able to cover both. What survives
from the answer above is the part the reversal stands on — the record is keyed by name, the licence by the
SHA — and what changed is which of the two a landing verdict is allowed to answer with. The measurement the
reversal was argued from: of the twenty-four changesets already in this repository's trunk, twenty-one keep
`reviewed: true` (all landed by merge commit, which carries the approved commits with them), one was already
false, and two flip for the reason in Known limitations.

The move asked for is this section.

**One thing this round changed that is not a reply to a question.** The PRD still describes the finding this
changeset replaced: six passages naming `LANDED, UNRECORDED` and printing the `integration record` invocation
that used to close it (§4, §10.6, §11.1, §22), and the skill repeats it. M5 rewrites all of them as part of
deleting the refs, so they are not rewritten here too — the text would collide on the rebase and the two
milestones would edit the same paragraphs twice. Flagged rather than fixed, because a reviewer reading this
branch against the PRD is reading two documents that disagree.
