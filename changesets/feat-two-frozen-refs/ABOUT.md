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

M2-M8 of the plan are not here. `Review-Head` and the rebase rule, landed-unrecorded detection, the
per-branch queue, stacked parent tracking and the agent contract follow.

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
- `internal/tui` — the session held an archive ref name for no reader; it is gone.
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
`pty-walkthrough.sh` prints `PTY: all checks passed`.

New assertions worth naming: `TestChangeReadyWritesNoRefs` and its three siblings;
`TestRecordedChangesetRefusesFurtherWork`, which covers the no-op paths as well as the state-moving ones;
the create-only cases in `reviewref_test.go`; and the hygiene test that pins the single `update-ref`.

## Known limitations

An abandoned changeset's ending is now readable only while its branch stands: the terminal marker no
longer has a ref holding it, so `git branch -D` takes the record with it. `change unready`'s withdrawal is
the same. The plan accepts this (§13.1); the queue names the work it cannot place rather than pretending.

`integration record` still refuses a second landing for a recorded changeset, so a backport to a release
branch is not recordable. Unchanged from before this changeset.

## Open questions

Does `integration record` warn about surviving review additions, now that nothing else checks them after
the offer? PRD §19.3 says today it neither warns nor refuses and leaves the question open; plan M5 owns it.
