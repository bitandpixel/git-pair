# Publish the durable refs

Executing `docs/plans/publish-the-records/plan.md`. The two frozen refs are the paper trail, and
today they exist only in the clone that wrote them: nothing publishes them, nothing notices when they
have not been published, and a clone that wants to know what other clones did has to be handed a
refspec by hand. This changeset makes the gap visible and the remedies explicit.

**M1 and M2 are done** — the read side, and the finding that reads it. **M3–M4 are pending**: `git pair
integration publish` with the PRD §26 amendment it needs, and `record --configure-fetch` with
"record, publish, then delete the branch" in the landing contract.

## What changed (M1)

- `status`, `queue` and `check` take `--fetch`, which asks the remote once for two things: the records
  (`refs/git-pair/*` into the namespace proper) and mirrors under
  `refs/remotes/<remote>/refs/git-pair/*`.
- `reviewref` gained `MirrorRoot`, `MirrorRefspec`, `FetchRefspecs`, `MirrorIntegration`,
  `MirrorArchive`, `MirrorPresent`; `git.Repo` gained `FetchRefspecs` — one
  `git fetch --quiet --no-tags --prune` carrying several refspecs.
- `namespaceAbsentWarning` names `--fetch` beside the raw fetch command.
- PRD §13 has a new subsection, "Reading them from another clone".

## What changed (M2)

- `internal/cli/published.go`: the finding. `RECORDED, NOT PUBLISHED` in `queue` (styled like
  `LANDED, UNRECORDED`), the same section in `status`, `--json` keys `unpublished` (never null) and
  `unpublished_note`.
- Detection compares the local pair against `refs/remotes/<remote>/refs/git-pair/*` — `reviewref.RemoteList`
  — and nothing else. No `ls-remote`, no network: with the remote made unreachable the finding still
  arrives.
- Half a pair on the remote is a distinct, louder case, because a remote holding one family has a hint and
  no way to reconstruct the record.
- `--fetch` is now **two** fetches: records without `--prune`, mirrors with it. `git.Repo.FetchRefs` and
  `FetchPruned`, `reviewref.FetchPlanFor`.
- `app` caches which remote the durable refs belong to, so the fetch and the comparison share one lookup.

## Design decisions

- **A fetched record is a record; a mirror never is.** Replication is the point of the refs, so a clone
  that fetched `refs/git-pair/integrations/x` truthfully answers "recorded". The mirror subtree exists
  only to be compared against, and no command answers "is this recorded" by reading it. That is the
  invariant the milestone rests on, and `TestMirrorsAreNeverRecords` pins it.
- **Two refspecs in one fetch.** "Is it recorded?" and "has it travelled?" are asked in the same breath;
  a clone holding one without the other would answer them inconsistently.
- **Opt-in.** A command that quietly reaches the network answers differently depending on where it ran,
  and `queue` in a cron loop should not buy a fetch per tick without anyone deciding.
- **A failed fetch warns and answers anyway.** "I could not ask" is not a verdict about the work.
- **The mirror is pruned, the record is not.** `+` on one refspec and none on the other is that
  difference: records are append-only and a fetch that must move one is reporting a bug, while a mirror
  exists to agree with the remote or be wrong.
- `--fetch` costs exactly two git invocations over the same command without it — one fetch, one decision
  about which remote — which is why the remote is handed the branch instead of asking git for it, and
  why it skips the `git remote` check when the branch's upstream already named one.

## The bug M2 found in M1

`git fetch --prune` prunes the destination subtree of *every* refspec in the command. M1's single fetch
carried `refs/git-pair/*:refs/git-pair/*` alongside the mirror refspec, so a clone that had recorded a
landing and not yet published it fetched, and git deleted the records it had come to read — the exact state
M2 exists to report. The M2 fixture found it by recording, fetching, and then reporting its own record as
missing. The M1 spike had measured that prune was scoped to the refspec's subtree; it had not measured it
against a clone with something to lose. Records are fetched unpruned now, mirrors pruned, at the cost of
one extra git call per `--fetch`.

## Design decisions (M1)

Spike S4 ran against git 2.43 in a real two-repo fixture: `for-each-ref` keeps the mirror subtree away
from the record namespace; `--prune` with an explicit refspec prunes only that subtree and leaves
`refs/remotes/origin/main` and unrelated remote-tracking entries alone; a no-op re-fetch costs one
invocation and touches nothing.

## Validation

`mise run check` green. M1: five tests in `internal/cli/fetch_test.go`
(`TestFetchMakesAPublishedRecordVisibleInAFreshClone`, `TestFetchBringsTheMirrorsToo`,
`TestMirrorsAreNeverRecords`, `TestFetchFailureWarnsAndAnswersAnyway`,
`TestFetchIsOneGitInvocation` — the last asserts `--fetch` costs exactly three git invocations).
M2: six in `internal/cli/published_test.go` — the record/publish/clear walk, the cannot-tell line with no
remote and with mirrors never fetched, the unreachable-remote case, `check` answering identically before
and after publishing, `unpublished` present and `[]` on both commands, and a cost test comparing one
recorded pair against fifty-one. `e2e-29.sh` and `pty-walkthrough.sh` pass.
