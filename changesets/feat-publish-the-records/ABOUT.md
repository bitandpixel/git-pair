# Publish the durable refs

Executing `docs/plans/publish-the-records/plan.md`. The two frozen refs are the paper trail, and
today they exist only in the clone that wrote them: nothing publishes them, nothing notices when they
have not been published, and a clone that wants to know what other clones did has to be handed a
refspec by hand. This changeset makes the gap visible and the remedies explicit.

**M1 is done** — the read side. **M2–M4 are pending**: the recorded-but-unpublished finding, `git pair
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

## Measured, not assumed

Spike S4 ran against git 2.43 in a real two-repo fixture: `for-each-ref` keeps the mirror subtree away
from the record namespace; `--prune` with an explicit refspec prunes only that subtree and leaves
`refs/remotes/origin/main` and unrelated remote-tracking entries alone; a no-op re-fetch costs one
invocation and touches nothing.

## Validation

`mise run check` green, including five new tests in `internal/cli/fetch_test.go`
(`TestFetchMakesAPublishedRecordVisibleInAFreshClone`, `TestFetchBringsTheMirrorsToo`,
`TestMirrorsAreNeverRecords`, `TestFetchFailureWarnsAndAnswersAnyway`,
`TestFetchIsOneGitInvocation`). `e2e-29.sh` and `pty-walkthrough.sh` pass.
