# Publish the records: getting `refs/git-pair/*` off the laptop

## Goal

The two durable refs are the paper trail, and today they exist only in the clone that wrote them. If
that clone goes away before somebody pushes them, the review history is gone and nothing in git-pair
ever says so. This plan makes that state **visible**, makes publishing **one named step**, makes the
read side able to **see what other clones published**, and puts the git configuration that keeps the
refs replicating behind an **explicit consent** rather than a guess.

Two principles carry the whole design:

- **A ref survives by having more than one copy.** Durability comes from replication, not from any
  single clone being careful.
- **git-pair does not go to the network on your behalf unless you ask.** Every network step in this
  plan is either an explicit flag or an explicit command, so the same command line always does the
  same thing and `--json` never depends on a prompt nobody answered.

## Success criteria

Observable outcomes:

- `git pair status` and `git pair queue` report a changeset whose integration and archive refs exist
  **in this clone** and not on the remote as **recorded, not published**, naming the command that
  publishes them. A clone that cannot tell (no remote, or no fetch refspec for the namespace) says
  that instead, once, and emits no per-changeset warnings.
- `git pair integration publish` pushes both refs of a pair, is idempotent, and refuses — naming
  both SHAs — when the remote holds a different value for either. It never forces, never deletes, and
  cannot be pointed at a ref outside `refs/git-pair/`.
- `git pair status --fetch`, `queue --fetch` and `check --fetch` answer from a refreshed view of what
  the remote holds; without `--fetch` they never touch the network and their wording makes clear the
  answer is about *this clone*.
- A fetched copy of a record can never be mistaken for a local one: after `--fetch`, a clone that did
  not perform the landing still answers "no record in this clone" for local questions while
  correctly reporting the remote's copies.
- `git pair integration record --configure-fetch` writes the namespace fetch refspec into the local
  config and prints exactly what it wrote; `git pair integration record` without the flag writes no
  config, prints the one line it did not run, and behaves identically in a terminal and in a pipe.
- The landing contract reads **record, publish, then delete the branch**, in PRD §29, README, and the
  next-action text `record` prints.
- `mise run check`, `docs/plans/completed/gitpr-mvp/artifacts/e2e-29.sh` and
  `pty-walkthrough.sh` pass at every milestone, and PRD.md and README.md move in the same commit as
  the behaviour they describe.

## Context

What exists when this plan starts (`feat/two-frozen-refs`, plan `review-architecture-v2` complete):

- `refs/git-pair/integrations/<id>` and `refs/git-pair/archive/<id>` are written only by
  `git pair integration record`, create-only, and nothing in the codebase can move or delete a ref.
- `git pair integration record` is the only ref write in the product, and it runs locally: it neither
  pushes nor asks anyone else.
- Landed-but-unrecorded is detected (§PRD §4, `internal/cli/landed.go`): a changeset directory in the
  destination with no integration ref is reported, capped, hedged, and `--json` carries
  `landed_unrecorded` as an array that is never null. This plan adds the mirror-image finding and
  should reuse that shape — the cap, the once-per-run hedge, the "in this clone" wording.
- `internal/cli/integration.go:namespaceAbsentWarning` prints the fetch advice when a clone holds no
  `refs/git-pair/*` at all. That is today's entire answer to replication: a line of prose a human may
  or may not run.
- `reviewref.FetchRefspec` exists as the canonical refspec constant; `internal/git.Repo.Fetch` exists
  and `git pair change wait --fetch` is the precedent for an opt-in fetch flag.
- PRD §26 and `internal/hygiene/hygiene_test.go` forbid shipped source from invoking `push` (and from
  containing a bare literal equal to it). That invariant is why nothing publishes today, and this plan
  amends it in a bounded, auditable way rather than dissolving it.
- The test suite already builds real remotes and pushes the namespace refspec (`ci_test.go`), so
  multi-clone fixtures are an established pattern here, not new scaffolding.

## Constraints

- **§26 is a product rule, not a test detail.** Any change is made in PRD §26 first and the test
  second, and the exception is namespace-scoped: one audited helper may push refs under
  `refs/git-pair/` and nothing else may push anything.
- **PRD §22 makes the CLI the agent surface.** Commands are non-interactive, and a prompt would make
  `integration record` behave differently depending on whether a human is attached — the same class of
  context-dependent meaning this codebase has spent a plan removing. Consent is expressed as an
  argument, not a keystroke (see M4's decision).
- `--json` contracts stay additive and arrays stay non-null for new keys, as with `landed_unrecorded`.
- No new ref family beyond the remote-tracking copies M1 introduces, and those are git's own
  remote-tracking shape, not a git-pair invention.
- `integration record` keeps its single responsibility: it verifies and writes refs locally. Publishing
  and configuring live in other commands, so a pipeline can record in one job and publish in another.

## Assumptions

- The remote worth publishing to is `origin`, or one named by `--remote`. There is no git-pair remote
  setting in this plan.
- The target forge permits writes under `refs/git-pair/*` (S1). If it does not, M3's shape changes to
  a notes-ref or out-of-band store and the rest of the plan still stands.
- Fetching the namespace stays cheap enough to be a flag rather than a background task (S3).

---

## M1 — The read side: `--fetch`, and remote copies that are visibly remote

**Deliverables**

- `status`, `queue` and `check` accept `--fetch`, which brings the namespace in before answering.
- Fetched records live in `refs/remotes/<remote>/refs/git-pair/*` and are distinguishable from local
  writes everywhere in the code.
- The advice line that currently tells a reader to run a fetch command names the configuration that
  would stop the question recurring.

**Tasks**

- [ ] `reviewref`: add `RemoteRefspec(remote)` (→ `+refs/git-pair/*:refs/remotes/<remote>/refs/git-pair/*`),
      `RemoteIntegration(remote, id)`, `RemoteArchive(remote, id)`, and a `RemoteList`/index helper. Keep
      the write-side functions untouched: this plan adds a read side, it does not broaden the write side.
- [ ] **Correct the refspec.** Mapping the namespace onto itself
      (`+refs/git-pair/*:refs/git-pair/*`, the shape suggested during planning discussions) would put
      fetched copies in the namespace the tool writes records into — so a fetched integration ref would
      satisfy `ResolveIntegration`, reserve the id in `Taken`, and count as a record for the
      landed-unrecorded detection in a clone that never saw the landing. Remote copies go under
      `refs/remotes/<remote>/`, matching git's own `refs/heads/*` → `refs/remotes/<remote>/*` convention.
- [ ] `--fetch` on `status`, `queue`, `check`. Fetch failure is a warning plus the stale answer, never a
      hard failure — the same three-way shape as everywhere else: answered, refused, cannot tell.
- [ ] Reword `namespaceAbsentWarning` to name both remedies (`--fetch` now, `record --configure-fetch`
      so it stops recurring) instead of only the fetch command.
- [ ] PRD §13 gets the remote-tracking copies and the rule that they are not records; README's
      handoff section mentions `--fetch`.

**Verification**

- A fixture with two clones and a real remote: clone A records and pushes the namespace; clone B, with
  no configuration, says "no record in this clone"; with `--fetch` it reports the remote's copies and
  still says there is no local record.
- `TestAFetchedRecordIsNotALocalRecord`: after `--fetch`, local `ResolveIntegration` and `Taken` answers
  are unchanged, and the landed-unrecorded finding is unchanged.
- A cost test: `--fetch` on an up-to-date clone adds one git invocation, and reading the remote copies
  adds none.

---

## M2 — Recorded, not published

**Deliverables**

- `status` and `queue` report pairs that exist here and not on the remote, with the publish command
  printed.
- A clone that cannot tell says so once, naming the reason (no remote configured, or no fetch refspec
  for the namespace) rather than warning per changeset.
- `--json` carries the finding as `unpublished`, an array that is never null.

**Tasks**

- [ ] New finding in `internal/cli` beside `landed.go`: a pair whose two local refs exist and whose
      remote-tracking copies are absent or hold different values. Reuse the display cap, the
      once-per-run hedge, and the "in this clone" wording.
- [ ] Half-published remotes (one of the two refs accepted, the other rejected) are reported as a
      distinct, louder case: the remote is a place where a pair can be split, and a reader should know.
- [ ] Detection reads remote-tracking refs only — no `ls-remote`, no network. Refreshing is `--fetch`'s
      job (M1), which keeps the "no network unless asked" rule intact and the cost zero.
- [ ] Cannot-tell cases: no remote at all (a local-only repository is legitimate), and remote exists
      but nothing under `refs/remotes/<remote>/refs/git-pair/` has ever been fetched, which is the
      refspec-not-installed case and gets the `--configure-fetch` advice rather than a false alarm.
- [ ] `check` deliberately does **not** refuse on this. The gate is a verdict about the work; a record
      that has not travelled is an operational gap, reported by `status`, `queue` and `record`'s own
      output. If `check` refused, CI without credentials would fail every landing.
- [ ] `record`'s post-record output names publishing (`next:  git pair integration publish`), so the
      step is where the moment is.

**Verification**

- Recorded-and-unpublished is detected; after publishing it disappears; a published pair produces no
  output at all.
- A clone with no remote configured prints the cannot-tell line once, and no per-changeset warnings —
  the M4 (v2 plan) noise lesson, re-asserted for this finding.
- Half-published remote is distinguished from fully unpublished.
- A cost test: detection cost does not grow with the number of refs in the namespace.
- `--json` shape test: `unpublished` present and `[]` when empty.

---

## M3 — `git pair integration publish`

**Deliverables**

- One named command moves a pair to the remote, idempotently, and refuses when the remote disagrees.
- PRD §26 states the bounded exception and the hygiene test enforces it as an audited single call site.

**Tasks**

- [ ] PRD §26 first: git-pair may push refs under `refs/git-pair/` and nothing else; every other
      push/merge/rebase/reset/branch-mutating verb stays forbidden. Then amend
      `internal/hygiene/hygiene_test.go` so `push` is permitted **only** inside the audited push helper
      in `internal/git/`, and add `TestOnlyTheAuditedHelperPushes` (every other non-test call site of
      the push primitive is a failure) plus `TestPublishCannotLeaveTheNamespace` (the helper refuses a
      refspec outside `refs/git-pair/`, and refuses `--force`, `--force-with-lease`, `--delete`,
      `--mirror`, `--all`, `--tags`, and any `--receive-pack`-style argument).
- [ ] `git pair integration publish [<changeset>…]`. No arguments publishes every pair M2 calls
      unpublished — that is the shape CI wants and the shape that makes forgetting impossible. Named
      ids publish just those. `--remote <name>` defaults to `origin`; no remote resolves to a refusal
      that says what it looked for.
- [ ] Both refs of a pair in one `git push` invocation, non-forced, so an existing remote value that
      differs is a rejection rather than an overwrite. Re-running after success reports "already
      published" and changes nothing, mirroring `record`'s idempotence.
- [ ] Conflict wording names both values, the way `reviewref.ErrRefConflict` does locally: what the
      remote holds, what this clone holds, and that the difference is a finding about two recorders —
      never "pass --force".
- [ ] Post-publish verification by reading the remote-tracking copies after a `--fetch`, so the command
      confirms its own claim rather than trusting the push's exit status.
- [ ] No `integration pull`, no `integration fetch`: the read side is `--fetch` and the config line, and
      a wrapper around `git fetch <refspec>` would be a second spelling of something already printed.

**Verification**

- Two-clone fixture: publish from the recording clone, and the other clone sees it with `--fetch`.
- Idempotence: publish twice, second says already published and writes nothing.
- Conflict: remote ref moved by hand → refusal naming both SHAs, exit non-zero, nothing pushed.
- The helper refuses everything the hygiene amendment says it must, asserted per argument.
- `mise run check` green with the amended hygiene test, including a deliberately planted illegal
  `push` call site failing the new invariant test and being removed again.

---

## M4 — Consent for the config, and record-before-publish in the contract

**Deliverables**

- `record --configure-fetch` writes the fetch refspec; `record` without it writes none and prints the
  line it did not run.
- The landing contract reads **record, publish, then delete the branch** everywhere a reader meets it.

**Tasks**

- [ ] **Decided: no prompts.** `record`'s default writes refs and nothing else, and prints one short
      line naming the flag that would configure the fetch refspec. `--configure-fetch` writes it
      (idempotent: already present reports that and changes nothing) and prints the key and value it
      wrote. Rationale for rejecting the interactive version: PRD §22 makes this CLI the agent surface,
      a TTY-dependent prompt makes one command line mean two things, and an unanswered prompt in CI is
      indistinguishable from a declined one — which would silently skip the very step that protects the
      paper trail. Consent belongs in the invocation, where it is visible in a pipeline definition and
      greppable in a log. If interactive confirmation is wanted later it belongs in the TUI, which is
      already a conversation.
- [ ] The refspec written is M1's `+refs/git-pair/*:refs/remotes/<remote>/refs/git-pair/*`, into
      `remote.<remote>.fetch` of the local config. Never `remote.origin.push`: a push refspec replaces
      the default push behaviour for the whole clone, which is a surprising side effect to acquire from
      a record command, and `publish` passes its refspecs explicitly.
- [ ] PRD §29's landing contract gains the ordering: record, publish, then delete the branch — with the
      reason, that after the branch is deleted the refs are the only copy of the archive chain, so a
      delete before a publish is how the history dies. README's handoff section and the `integration
      record` row of the command table move with it.
- [ ] `landingNextAction` and `record`'s trailing advice gain the publish step; §22's agent loop and
      §29's loop text reflect it.
- [ ] PRD §27's "publishing the two ref families" deferral is amended, not deleted: what this plan
      gives up is still given up — forge-level protection and namespace-protected variants stay out — and
      the text should say so rather than look contradicted.
- [ ] Extend `docs_contract_test.go` to the new command and the new ref paths.
- [ ] One spelling rule honoured: no `mise` task, no alias, no second flag for the same config write.

**Verification**

- `record` in a pipe (no TTY) and at a terminal produce byte-identical output, and neither blocks.
- `record --configure-fetch` twice: the second says already configured and leaves the file unchanged
  (asserted on the config file, not on the message).
- A clone configured by `--configure-fetch` needs no `--fetch` flag to answer correctly about a peer
  clone's publish after an ordinary `git fetch`.
- The §29 loop is replayed against the prose: `pty-walkthrough.sh`, plus an e2e step that records,
  publishes from clone A, and detects publication from clone B.

---

## Spikes / Research

| ID  | Question                                                                                                                | Needed before |
| --- | ----------------------------------------------------------------------------------------------------------------------- | ------------- |
| S1  | Does the target forge accept client pushes of `refs/git-pair/*`, including on a repository with branch protection and no rule naming that namespace? Does anything need adding to protection or signing policy? | M3 |
| S2  | When `git push` is given two refspecs and the server accepts one and rejects the other, what does it report, and in what order? The conflict wording needs to name both SHAs from that output. | M3 |
| S3  | What does `git fetch --quiet <remote> <refspec>` cost on a repository with many refs and no `refs/git-pair/*` on the remote — is `--fetch` affordable in a CI loop, and does it need `--prune` semantics for deleted remote refs? | M1 |
| S4  | Does a remote-tracking name of the shape `refs/remotes/origin/refs/git-pair/integrations/<id>` confuse any git plumbing we use (`for-each-ref`, `resolve-ref`, `check-ref-format`), and do git GC/prune rules treat it normally? | M1 |

## Risks

**R1 — The §26 exception spreads.** "One audited helper may push the namespace" is one sentence from
"anything may push". Mitigation: the amendment is written in PRD §26 as a scope, the hygiene test
permits the verb *inside one file*, `TestOnlyTheAuditedHelperPushes` fails on any other call site, and
`TestPublishCannotLeaveTheNamespace` pins the argument-level refusals.

**R2 — A fetched copy read as a local record.** The failure is quiet and bad: a clone would report a
changeset as recorded, and would refuse to record it for itself, on the strength of somebody else's
ref. Mitigation: remote copies live only under `refs/remotes/<remote>/`, the write-side functions never
look there, and `TestAFetchedRecordIsNotALocalRecord` pins it.

**R3 — Detection noise.** An "unpublished" warning in every clone that legitimately has no remote, or
that simply never installed the refspec, trains people to ignore the warning that matters. Mitigation:
the three-way answer (published / not published / cannot tell, with the reason), once per run, capped,
and the cannot-tell case naming the remedy rather than the changesets.

**R4 — `publish` accretes verification.** Once it exists, publishing looks like the place to re-check
reachability, approvals and pairs. Mitigation: `publish` compares values and moves them; `record` owns
verification. A pair that `record` refused cannot be published, and `publish` re-derives nothing.

**R5 — Two recorders disagree and somebody wants to win.** The conflict refusal is the whole point: two
clones recording the same changeset differently is a real disagreement about what landed. Mitigation:
no force flag at all, and a message that says which ref to inspect rather than which flag to pass.

**R6 — `--fetch` in a hot loop.** A cron running `queue --fetch` turns a local command into network
traffic per tick. Mitigation: it is opt-in and documented as such; the answer without it is honest
about being local.

**R7 — The namespace becomes load-bearing before it is stable.** This design retired a namespace
already, and published refs are seen by every clone that fetches them. Mitigation: publish to
`refs/git-pair/*` only, keep forge-level protection deferred in §27, and land M1–M2 (which write
nothing) before M3.

## Verification strategy

Unit tests for the refspec and remote-name helpers in `reviewref`. CLI-harness fixtures with a real
remote and real clones — the pattern `ci_test.go` already establishes — because every interesting
failure here is a two-clone problem: fetched-not-local, published-not-announced, remote-diverged.
Invariant tests for the push scope alongside the existing durable-ref invariants. `--json` shape tests
for the new key. Cost tests for the read side, continuing the queue-cost pattern. The two gate scripts
at every milestone, and PRD/README edits in the same commit as the behaviour, with
`docs_contract_test.go` holding the names honest.

## Audit history

| Date | Audit | Summary |
| ---- | ----- | ------- |
| —    | —     | Plan written from the review-architecture-v2 follow-up discussion; no implementation yet. |
