# Publish the durable refs

Executing `docs/plans/publish-the-records/plan.md`. The two frozen refs are the paper trail, and
today they exist only in the clone that wrote them: nothing publishes them, nothing notices when they
have not been published, and a clone that wants to know what other clones did has to be handed a
refspec by hand. This changeset makes the gap visible and the remedies explicit.

**All four milestones are done.** The refs are readable from another clone (`--fetch`), a record that has
not travelled is reported (`RECORDED, NOT PUBLISHED`), publishing is a command with an audited call site
(`git pair integration publish`), and the configuration and the ordering are in the contract
(`--configure-fetch`, and "record, publish, then delete the branch").

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

## What changed (M3)

- `git pair integration publish [<changeset>…] [--remote <name>]`: sends a pair to the shared remote in one
  unforced push, then re-reads the remote's copies and reports what is actually there. Idempotent, so CI can
  run it every build. `--json` carries `published`, `already_published` and `failed` (never null), with
  `half_state` on a pair the remote took only half of.
- `internal/git/push.go`: **the audited push helper**, and the only place in shipped source that may invoke
  `git push`. It takes no options, cannot force, cannot delete, and refuses anything option-shaped before git
  sees it. PRD §26 now carries the carve-out — a namespace, not a verb.
- Hygiene enforces the carve-out as a location: `push` is exempted only for that file path,
  `TestPushIsConfinedToTheAuditedFile` rescans with the exemption removed, and
  `TestPushHelperHasOneCaller` allows one caller (`internal/cli/publish.go`). A planted `push` call site fails
  all three, and the planted file is gone.
- `internal/git` gained `FetchRefs`/`FetchPruned` in M2 and `PushDurableRefs`/`PushReport` here;
  `integration record` now prints `next:  git pair integration publish <id>` and `next_action` in JSON.

## Spike results (M3)

- **S1, on GitHub (February 2026, throwaway repository):** client pushes of `refs/git-pair/*` are accepted,
  with branch protection enabled on `main` and no rule naming the namespace, and no forge configuration is
  needed. Unforced non-fast-forward updates of an existing `refs/git-pair/*` ref are **rejected** — by
  GitHub and by local git 2.43 — so create-only holds at the forge and publish never needs a `+`. Caveat: the
  protection baseline did not refuse the owner's own push (`enforce_admins` off), so the spike shows
  protection does not block the namespace; the non-admin case was not measured. The throwaway repository
  `dcasper/git-pair-spike-refs` is private and still exists — deleting it needs the `delete_repo` scope.
- **S2, both servers:** when one refspec is accepted and one rejected, the accepted one **is applied** and git
  exits 1; the rejection lines name refs but no SHAs. So publish pushes with `--porcelain`, branches on
  per-refspec status, and takes both SHAs in the conflict message from its own knowledge — the local value and
  the remote's copy after re-fetching.

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

## What changed (M3)

- `git pair integration publish [<changeset>…] [--remote <name>]`: sends a pair to the shared remote in one
  unforced push, then re-reads the remote's copies and reports what is actually there. Idempotent, so CI can
  run it every build. `--json` carries `published`, `already_published` and `failed` (never null), with
  `half_state` on a pair the remote took only half of.
- `internal/git/push.go`: **the audited push helper**, and the only place in shipped source that may invoke
  `git push`. It takes no options, cannot force, cannot delete, and refuses anything option-shaped before git
  sees it. PRD §26 now carries the carve-out — a namespace, not a verb.
- Hygiene enforces the carve-out as a location: `push` is exempted only for that file path,
  `TestPushIsConfinedToTheAuditedFile` rescans with the exemption removed, and
  `TestPushHelperHasOneCaller` allows one caller (`internal/cli/publish.go`). A planted `push` call site fails
  all three, and the planted file is gone.
- `internal/git` gained `FetchRefs`/`FetchPruned` in M2 and `PushDurableRefs`/`PushReport` here;
  `integration record` now prints `next:  git pair integration publish <id>` and `next_action` in JSON.

## Spike results (M3)

- **S1, on GitHub (February 2026, throwaway repository):** client pushes of `refs/git-pair/*` are accepted,
  with branch protection enabled on `main` and no rule naming the namespace, and no forge configuration is
  needed. Unforced non-fast-forward updates of an existing `refs/git-pair/*` ref are **rejected** — by
  GitHub and by local git 2.43 — so create-only holds at the forge and publish never needs a `+`. Caveat: the
  protection baseline did not refuse the owner's own push (`enforce_admins` off), so the spike shows
  protection does not block the namespace; the non-admin case was not measured. The throwaway repository
  `dcasper/git-pair-spike-refs` is private and still exists — deleting it needs the `delete_repo` scope.
- **S2, both servers:** when one refspec is accepted and one rejected, the accepted one **is applied** and git
  exits 1; the rejection lines name refs but no SHAs. So publish pushes with `--porcelain`, branches on
  per-refspec status, and takes both SHAs in the conflict message from its own knowledge — the local value and
  the remote's copy after re-fetching.

## Design decisions (M1)

Spike S4 ran against git 2.43 in a real two-repo fixture: `for-each-ref` keeps the mirror subtree away
from the record namespace; `--prune` with an explicit refspec prunes only that subtree and leaves
`refs/remotes/origin/main` and unrelated remote-tracking entries alone; a no-op re-fetch costs one
invocation and touches nothing.

## What changed (M4)

- `integration record --configure-fetch` appends `+refs/git-pair/*:refs/remotes/<origin>/refs/git-pair/*` to
  `remote.<name>.fetch` — `--add`, idempotent, printing the key and the value, or "already fetches the
  durable mirrors". It is the only configuration git-pair ever writes, and it is consent spelled as an
  argument: no prompt, anywhere, because §22 makes this CLI the agent surface.
- Without the flag, `record` writes no config and prints one line naming it — in a clone that already has
  the line, it prints nothing.
- `remote.<name>.fetch` gets the **mirror** refspec only. A record is a claim that a landing happened; a
  clone acquires claims by asking (`--fetch`), not from a config line written weeks earlier.
- PRD §29's contract is now check → land → record → **publish** → tidy, with the reason: after the branch is
  deleted the refs are the only copy of the chain. `landingNextAction`, `record`'s `next:` line, §22's agent
  loop and README's handoff all carry it; `contract_test.go` pins the sentence and asserts record precedes
  publish.
- §27's remote-enforcement deferral is amended rather than deleted: automatic pushing, forge-level namespace
  protection and server-side validation stay out, and the text says what M3 did take on.

## Design decisions (M3)

- **Publishing is a command, not `record --push`.** The two acts have different permissions and sometimes
  different owners: a pipeline may record in a job that can read the repository and publish in one that can
  write it, and `record` stays network-free either way.
- **No arguments publishes every pair this clone holds**, not the pairs the finding calls unpublished. The
  finding compares against mirrors — a memory of the last fetch — and a publish that trusts a stale mirror can
  skip a ref the remote lost. Publishing everything is idempotent and cannot be wrong about the remote.
- **A named id with no record is a refusal**, not a successful no-op: a typo or a record made elsewhere should
  not exit 0.
- **No `integration pull` / `integration fetch`.** `--fetch` and the configuration line are the read side; a
  wrapper around `git fetch <refspec>` would be a second spelling of something already printed.

## What M4's own gates caught

The first draft wrote the config line *before* the refs, reasoning that an additive config line is the
reversible half. The pty walkthrough's fixture — whose changeset ends its scenarios blocked — made `record`
refuse, and the config line was written anyway: a refused record mutating the clone's fetch behaviour. Now
the flag is preflighted (no remote is a refusal with an exit code, before anything is written) and the write
follows a successful record, so a refusal leaves the repository as it found it. The same run then prints no
hint about a flag the clone already used.

## Validation

`mise run check` green. M1: five tests in `internal/cli/fetch_test.go`
(`TestFetchMakesAPublishedRecordVisibleInAFreshClone`, `TestFetchBringsTheMirrorsToo`,
`TestMirrorsAreNeverRecords`, `TestFetchFailureWarnsAndAnswersAnyway`,
`TestFetchIsOneGitInvocation` — the last asserts `--fetch` costs exactly three git invocations).
M2: six in `internal/cli/published_test.go` — the record/publish/clear walk, the cannot-tell line with no
remote and with mirrors never fetched, the unreachable-remote case, `check` answering identically before
and after publishing, `unpublished` present and `[]` on both commands, and a cost test comparing one
recorded pair against fifty-one. M3: seven in `internal/cli/publish_test.go` — publish-then-read-from-another-clone, idempotence asserted
against the remote's SHAs, the conflict refusal (both values named, the word "force" absent, the remote
unchanged), the half-state with a server-side hook, both remote refusals, the never-null JSON shape, and the
named-id refusal. `e2e-29.sh` and `pty-walkthrough.sh` pass.
