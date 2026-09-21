# feat-two-frozen-refs

## Summary

Implements `docs/plans/review-architecture-v2/plan.md`, one milestone per commit. M1 is here: the moving
per-changeset ref is gone, and the only durable refs git-pair has are the two `integration record`
creates at landing.

Until now git-pair wrote a ref during review and moved it as review went on: `change ready` created
`refs/git-pair/changesets/<id>/archive`, every submission moved it, `change archive` advanced it, and
`change unready` and `change abandon` moved it too. That made the ref a second copy of the branch,
updated in some places and not others — a changeset whose author withdrew the offer kept naming the offer
from its durable record — and it made the queue, the gate and `status` read something other than the
markers that are the protocol. This changeset deletes the write sites, the API that could move a ref, and
the command whose whole job was moving it.

What remains: `refs/git-pair/archive/<id>` and `refs/git-pair/integrations/<id>`, written by one
invocation of `git pair integration record`, create-only, with no code path that moves either.

This is M1 through M6: the transition, the rule about history the transition required, the checks that make
the record worth reading afterwards, the flagless local flow that writes it, the report that catches the step
being skipped, the next actions that tell an author where the step is, and the queue's shape and the CLI's
naming. M7-M8 follow — stacked parent tracking and the agent contract.

## What changed

- `internal/reviewref` — rewritten against two flat families under `refs/git-pair`. `Update`,
  `ErrArchiveFrozen`, `Namespace`, `ChangesetID`, `ArchivesAt` and the nested `<id>/<child>` parsing are
  gone; `Archive(id)`/`Integration(id)` name the two paths, `Taken` refuses an id whose either path
  exists, `List` reads both families in one `for-each-ref` pass, `CreatePair` writes archive-then-integration
  and completes a half pair. `FetchRefspec` lost its `+`.
- `internal/git` — `UpdateRef` deleted; `CreateRefIfAbsent` is the only ref write in the product, and it
  is an atomic `update-ref <ref> <sha> <zero-oid>`. Same commit already recorded is a no-op success, a
  different one is `ErrRefTaken` naming both SHAs.
- `internal/cli` — `change archive` deleted (209 lines). `change ready`, `change unready`, `change abandon`
  and `review submit` write commits and nothing else; the first three refuse a recorded changeset, and
  unready and abandon refuse on their no-op paths too. `check` lost `archive` and `archive_current` from
  `--json` and its fifth condition, and prints the head it cleared. `integration record` derives the
  changeset from the directories `--source` carries and trunk does not, and writes both refs. `status`
  reports `integration_ref` beside `integrated`. The next-action strings across `check`, `status`, `change
  wait` and `review submit` are one function now.
- `internal/changeset/resolve.go` — candidates come from trees alone. Ordering between two of them is the
  newest commit touching `changesets/<id>/`, computed only when two survive; `Candidate` lost `Review` and
  `Terminal`.
- `internal/marker` — `RefuseIntegrated` exported, so the refusal of a recorded changeset happens in the
  path that writes markers rather than after the fact.
- `internal/hygiene` — `TestDurableRefsAreOnlyEverCreated`: exactly one `update-ref` in non-test source,
  and the old-value it passes must be 40 zeros.
- `internal/tui` — the session held an archive ref name for no reader; it is gone. The span-walk comment
  that defended tolerating a force-pushed history now says the screen paints any span and the gate is
  what refuses rewritten history.
- `Review-Head` (M2) — `model.TrailerHead`, written by `marker.ReviewMessage` and therefore by
  `reviewops.Submit` alone: a `ready`, `working` or `abandoned` marker names no head, because those
  commands ask no question about lineage. `lifecycle.Event.ReviewedHead` carries the value; `check`'s new
  `lineageReason` asks it of the newest review only where integration is permitted, and the head is
  reported by `check --json`, `status` (a `reviewed:` line) and `review history` (a `REVIEWED` column).
- `integration record` verifies before it writes (M3). `verifyReviewedSource` reads `--source`'s ancestry:
  the markers must name the changeset being recorded, and its newest verdict must permit integration —
  `approve`, or `feedback` with the recorder's new `--allow-feedback`. `verifyLandingReachable` puts the
  landing in the destination's history, the destination being `--target` or, when unnamed, the changeset's
  own `base:` then the default branch. The fourth check is the first-parent transition: the commit must
  *add* `changesets/<id>/`, not merely carry it. `lifecycle.ScanLineage` is the walk all of that reads;
  `reviewref.RecordedPair` and `Conflict` are the record read back before any check runs.
- `internal/reviewref` — `RecordedPair` reads both halves with absent ones as the empty string, and
  `Conflict` returns the write's own conflict error without writing, so the refusal a retry hears is the
  refusal the write would have given.
- Queue per branch (M6) — `queue` prints one row per branch, with a `branch:` line, instead of grouping by
  changeset id and printing whichever branch carried the newest ready marker. Two branches can carry one
  changeset, and they have different heads and different states; a review is a commit appended to a branch,
  so the branch is the thing that is ready.
- Naming (M6, plan P3) — `git pair queue` and `git pair init` are the spellings, hard-renamed with no
  aliases; the two commands moved into `init.go` and `queue.go` because top-level commands live one per
  file. Historical documents keep the old names: they record what was true when they were written.
- `check --json` gained `next_action` (M5), present only when the gate passes: the same sentence `status`
  prints, so an agent that gates on `ready` learns about the merge and the record without parsing human
  output. `TestApprovedStateNamesTheLandingAndTheRecord` pins that sentence across all four spellings and
  the record's absence, and pins the one asymmetry in how the record's two halves appear in JSON.
- Landed, unrecorded (M4) — a `changesets/<id>/` directory in the integration branch with no integration
  ref is a merge whose record never ran, and it is now a reported state: `queue` gives it its own
  heading with the `git pair integration record` invocation, and `status` puts the same finding on its
  exit-2 "no changeset for this branch" answer. `internal/cli/landed.go` holds the detector, the wording,
  and `refIndex` — one read of the durable namespace that also answers the queue's two other questions
  about those refs, which it had been asking once per changeset.
- `changeset.BranchResolutions` became `ScanBranches`, returning `Scan{Branches, TrunkIDs}`: the destination's
  directories are the same listing the resolver already took, and the detector is the subtraction.
- Flagless recording (M3) — `--source` and `--commit` are optional. The landing comes from
  `git.Repo.FirstParentLine` (new) and the transition rule M4's detector will share; the reviewed head comes
  from the branch still carrying the directory, destinations excluded. Ambiguity at either end is exit 2
  naming the candidates, and the answer says which of the two it filled in (`derived:` in the text,
  `"derived"` in the JSON).
- `PRD.md`, `README.md` — the two-ref model throughout: §3's vocabulary, §9.5 repurposed as "Handing the
  work on", §9.6/§9.7's endings, §11.1's status transcript, §11.3's conditions, §11.4's discovery, §12's
  lifecycle, §13 rewritten (13.1 archive, 13.2 integrations, 13.3 create-only, 13.4 fetching), §19.3, §21,
  §22, §25, §26 and §29; README's opener, Quickstart, Concepts, command table, JSON contracts and
  Troubleshooting.
- `docs/plans/completed/gitpr-mvp/artifacts/e2e-29.sh` — extended, not rewritten: both refs, the idempotent
  re-record, the conflicting-record refusal, the four commands that refuse a recorded changeset, and the
  full unsquashed chain reachable from the archive after `git branch -D`.
- Tests: `reviewref_test.go` and `resolve_test.go` rewritten; `in_flight_refs_test.go` new (one
  no-refs-written test per command that moves state, so a future write site fails a named test);
  `archive_test.go` and `anchor_test.go` deleted.

## Design decisions

**Create-only tolerates the identical re-create and refuses a different target.** A retry after a
half-written record has to finish rather than fail on the half that worked, so asking for the commit a ref
already names succeeds and says nothing moved. Asking for a different commit is refused with no override
flag: the pair is what a release note, a bisect, or an agent asking "where did this review go" reads as
fact, and moving it under them is the failure the milestone exists to make impossible.

**`Review-Head` is recorded, never derived from first parent.** The plan required recording it; what
needed deciding was what to do when a marker names no head, and the answer is to refuse. Deriving the
value from the graph would read the rewritten parent as the reviewed one — the exact case the rule exists
to catch — so a marker that names nothing cannot be placed, and git-pair does not guess at an approval.
The cost here is close to zero (every marker in this repository's own changesets came from a build that
records the trailer); the cost of the compat hole is a rule with an exception in it.

**The lineage question is asked only where integration is permitted.** A `block` verdict says `block`;
the gate does not complicate a refusal the author already understands with a second one about ancestry.
The two ways the head can be missing get two reasons, because a rewritten branch and a clone that has
not fetched are different things to fix.
- **The recorder asks for the newest verdict, not for an approval somewhere.** The plan's wording was "an
  approving marker exists"; a superseded approval is not the reviewer's answer, and `change unready` or
  `change abandon` after an approval must take the head out of the recordable set. So the newest marker
  naming the changeset decides, on the same reading `check` makes — which is why the recorder grew
  `--allow-feedback` rather than a laxer default: the two commands a person runs one after the other must
  not disagree about what counts as reviewed.
- **A derived destination is tried, not assumed.** `--target` absent means trying the changeset's `base:`
  and then the default branch and taking the first that contains the commit. Checking only the first name
  git-pair can reach would refuse the ordinary landing of a stacked child, whose `base:` is its parent
  branch and whose landing is trunk. Nothing derivable is a check not made, not a refusal: "this repository
  cannot say where work lands" is not the same fact as "the work did not land where it was said to".
- **The record is read before anything is verified.** A retry answers from the record, and a different pair
  is refused with what is on it. Both belong ahead of the checks: a second landing into a branch that
  already carries the directory also fails the transition check, and the reader needs the answer about the
  record rather than the incidental complaint about the tree.
- **A landing the retired layout recorded is recorded.** `reviewref.List` now reports
  `refs/git-pair/changesets/<id>/{integration,archive}` under kinds of their own, and the detector counts the
  integration one. The alternative was that every repository upgrading from the pre-two-ref release opened
  with a screenful of findings nobody caused, and a report that does that gets ignored the one time it is
  true. An archive ref alone still says nothing about a landing, and `Taken` still lets a legacy name be
  reused — different questions, tested apart.
- **The finding is a report, not a reading.** The tree rule is untouched: a directory in the destination is
  still not a claim, and exit codes are unchanged. What changed is that a state which used to be invisible
  now says who should run what.
- **Derivation never chooses.** The local flow reads the graph — the first-parent transition, the branch
  carrying the directory — because the person who merged should not have to translate it into object ids,
  but two candidates at either end is a usage error rather than a pick, and `--source` is not reconstructed
  from `Review-Head` after a branch deletion. A durable ref written from a plausible guess is worse than a
  command that asked. The one deliberate leniency is the second pass over already-recorded directories, so
  a CI job that records on every build hears "already recorded" instead of a failed build.

**`integration record` derives the changeset from content, not from the archive ref.** Discovery-by-archive
would have kept a ref written during review alive, which is the thing being deleted. The directories
`--source` carries and trunk does not is §4's rule read at a commit, so it needs no ref to have been
written first, no fetch, and no branch standing around. It is why part of M3 came forward.

**`check` reads no durable ref except to say "already landed".** The old gate required the archive to
point at `HEAD`, so a changeset in flight could not pass it and a clone that had not fetched the namespace
reported reviewed work as unreviewed.

**Fetch guidance travels with the command that reads the namespace.** `integration record` warns and
records anyway; `check` says nothing about refs; the queue's silence is documented as the one to be
careful with.

**§9.5 was repurposed rather than deleted**, so §9.6-§9.8 keep their numbers and every cross-reference to
them stays valid.

## Validation

`mise run check` (gofmt, vet, `go test ./...`) passes. `mise run build` then
`docs/plans/completed/gitpr-mvp/artifacts/e2e-29.sh` prints `E2E: all checks passed`, and
`pty-walkthrough.sh` prints `PTY: all checks passed`. The replay now rebases a throwaway copy of the
branch after an approval and asserts the gate refuses while the tree comparison has nothing to report,
which is what makes the case test the lineage rule rather than the content rule. It also records the
release-branch landing without `--target` first, asserting the refusal names the trunk it tried and the
flag that settles it, and that nothing was written.

New assertions worth naming: `TestChangeReadyWritesNoRefs` and its three siblings;
`TestRecordedChangesetRefusesFurtherWork`, which covers the no-op paths as well as the state-moving ones;
the create-only cases in `reviewref_test.go`; and the hygiene test that pins the single `update-ref`.
For M2: `TestCheckRefusesHistoryTheApprovalDidNotReview` (tree-identical rebase refused, rebase onto a
moved trunk refused, merge of the trunk accepted, implementation commit refused for content rather than
lineage, changeset-only commit accepted, reset back to the reviewed head accepted), the two no-head cases,
and `TestReviewSubmitRecordsTheReviewedHead`. For M3:
`TestIntegrationRecordRefusesWhatWasNeverReviewed` (six verdict shapes, each asserting nothing was
written), `TestIntegrationRecordRefusesACommitThatDidNotAddTheChangeset`,
`TestIntegrationRecordDerivesTheDestination` (derived trunk accepted, unnamed release branch refused with
the flag named), `TestIntegrationRecordAcceptsAChildLandedOnTrunk`,
`TestIntegrationRecordAnswersFromTheRecordBeforeTheChecks`, `TestIntegrationRecordDerivesBothTips`,
`TestIntegrationRecordDerivationRefusesToChooseBetweenTwo`,
`TestIntegrationRecordDerivationStopsWhenTheBranchIsGone`, and `TestRecordedPairAndConflict`.
For M6: `TestReviewQueueKeepsOneRowPerBranchForOneChangeset`.
For M4: `TestQueueAndStatusReportALandingNobodyRecorded`,
`TestQueueAcceptsALandingTheRetiredLayoutRecorded`, `TestQueueSaysOnceThatTheNamespaceIsAbsent`,
`TestQueueHedgesWhenOtherRecordsExist`, `TestQueueCountsLandingsItDoesNotPrint`,
`TestQueueJSONCarriesTheSectionAsAnArray`, `TestQueueDoesNotReportLiveWorkAsALanding`,
`TestQueueDoesNotReportALandingOnAnotherBranch`, and
`TestReviewQueueCostDoesNotGrowWithUnrecordedLandings`, and `TestApprovedStateNamesTheLandingAndTheRecord`; `e2e-29.sh` merges a changeset without recording it
and reads the finding back out of both commands.

## Known limitations

An abandoned changeset's ending is now readable only while its branch stands: the terminal marker no
longer has a ref holding it, so `git branch -D` takes the record with it. `change unready`'s withdrawal is
the same. The plan accepts this (§13.1); the queue names the work it cannot place rather than pretending.

`integration record` still refuses a second landing for a recorded changeset, so a backport to a release
branch is not recordable. Unchanged from before this changeset.

A review marker written by an older build — or by hand — names no `Review-Head`, and `git pair check`
refuses it rather than assuming the head was the marker's parent. That is deliberate (see the design
decision above), but it does mean an approval recorded before this changeset stops licensing a merge
until it is re-submitted. Nothing in this repository is in that position; a pair with a long-lived branch
could be.

## Open questions

Does `integration record` warn about surviving review additions, now that nothing else checks them after
the offer? PRD §19.3 says today it neither warns nor refuses and leaves the question open; plan M5 owns it.
