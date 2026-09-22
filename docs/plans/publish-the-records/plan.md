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
- [x] **Two refspecs, for two different questions.** Planned as one; the spike showed it is two.
      Fetching `refs/git-pair/*` into the namespace proper (the existing `reviewref.FetchRefspec`, the
      one `FetchCommand` already prints) is *correct* for "give me the records": a replicated record is
      still the record, and it is what makes `Present`, `ResolveIntegration`, `Taken` and the
      landed-unrecorded detection answer properly in a clone that did not do the landing — which is what
      the existing "none *in this clone*" hedge promises. What that shape cannot do is answer "what does
      the *remote* have", because the answer needs a copy that is explicitly somebody else's. So
      `--fetch` asks for both, in one git invocation: the namespace itself, and a mirror under
      `refs/remotes/<remote>/refs/git-pair/*` for M2's published-not-published comparison.
      **The invariant that matters** is not "fetched refs are not records" — it is that
      **the write-side readers never consult the remote-tracking copies**, so a mirror can never stand
      in for a record while pretending to be one.
- [x] `--fetch` on `status`, `queue`, `check`: one `git fetch --quiet --no-tags --prune <remote>` with
      both refspecs. Fetch failure is a warning plus the stale answer, never a hard failure — the same
      three-way shape as everywhere else: answered, refused, cannot tell. Side effect worth documenting:
      the mirrors show up in `git branch -a`, which is git being honest about remote-tracking refs.
- [x] Reworded `namespaceAbsentWarning` to name `--fetch` as the remedy alongside the command. The
      second remedy the task listed — `record --configure-fetch` — is M4's, so it is not named yet: a
      message that points at a flag which does not exist is worse than one that points at a command.
      M4 extends this line.
- [x] PRD §13 gains "Reading them from another clone" — both refspecs, and the rule that a fetched
      record is a record while a mirror never is; README's namespace paragraph names `--fetch` and the
      mirrors.

**What landed differently**

- `remoteForDurableRefs` takes the branch as an argument rather than asking git for it, and skips the
  `git remote` check when the branch's upstream already names a remote. Both are the difference between
  `--fetch` costing one git invocation and costing four, and the cost test pins the number. If the
  upstream's remote has vanished, the fetch fails and the warning says more than the lookup ever would.
- `--fetch` costs exactly two invocations above the same command without it: one fetch carrying both
  refspecs, one deciding which remote to ask. The assertion is on the number, not on the shape, so a
  regression to two negotiations shows up as a failure rather than as a slower pipeline.
- The fixture that writes a record has to run `integration record` itself: `recordFixture` lands the
  work and stops, which is right for its own tests and was wrong for the first draft of these.

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

- [x] New finding in `internal/cli/published.go`, beside `landed.go` and styled like it: a pair whose
      local refs exist and whose mirror copies are absent (`missing`) or hold different values
      (`diverged`). Reuses `unrecordedDisplayCap`, prints once per run, and shares the "as this clone last
      fetched it" framing. `queue` gets its own `RECORDED, NOT PUBLISHED` heading; `status` prints the same
      section after its report.
- [x] Half-published is its own line: with exactly one family on the remote the report says
      "half published: <remote> holds <the other> and not <this one>, so the record cannot be
      reconstructed there". In `--json` it is distinguishable by `missing` having one entry.
- [x] Detection reads `refs/remotes/<remote>/refs/git-pair/*` and nothing else — no `ls-remote`, no
      network. `TestDetectionNeedsNoNetwork` makes `origin` unreachable mid-test and asserts the finding
      still arrives, which is the property rather than the command list.
- [x] Cannot-tell is one sentence in `unpublished_note` and no section: no remote, or a mirror namespace
      this clone has never fetched. It names `--fetch` as the remedy; `--configure-fetch` is M4's, and a
      message pointing at a flag that does not exist is worse than one that points at the flag that does.
      One deviation from the wording above, and it needed thinking through: an *empty* mirror namespace is
      ambiguous — either the remote has no durable refs (the loudest finding there is) or nobody asked.
      Nothing but the run itself can tell those apart, so `--fetch` passing `asked` to the report is the
      disambiguator: mirrors present means compare, just-fetched means compare, otherwise say nothing.
- [x] `check` is untouched by the finding. `TestCheckAnswersTheSameWhicheverWayTheRecordTravelled`
      compares `check`'s exit code and output before and after publishing and requires both to be
      identical — stronger than asserting no refusal, because `check` legitimately refuses a changeset that
      is already recorded, and the publishing must not add a second reason to that answer.
- [ ] `record`'s post-record output names publishing (`next:  git pair integration publish`), so the
      step is where the moment is. **Deferred to M3**, which is the commit that makes the command exist:
      printing a `next:` line that points at nothing is the same mistake as the `--configure-fetch`
      wording above, one milestone earlier.

**Verification**

- Recorded-and-unpublished is detected; after publishing it disappears; a published pair produces no
  output at all.
- A clone with no remote configured prints the cannot-tell line once, and no per-changeset warnings —
  the M4 (v2 plan) noise lesson, re-asserted for this finding.
- Half-published remote is distinguished from fully unpublished.
- A cost test: detection cost does not grow with the number of refs in the namespace —
  `TestDetectionCostDoesNotGrowWithTheNamespace` compares `queue`'s git invocations with 1 and with 51
  recorded pairs (equal) and asserts 51 findings, so a constant cost that found nothing cannot pass.
- `--json` shape test: `unpublished` present and `[]` when empty, on both `status` and `queue`.

**What landed differently**

- **M1's prune decision was destructive and a test found it.** `git fetch --prune` prunes the destination
  subtree of *every* refspec in the command, so the single fetch carrying both `refs/git-pair/*` and the
  mirror refspec deleted the local records of a clone that had recorded a landing and not published it —
  the exact state M2 exists to report. The fixture recorded, fetched, and then reported its own record as
  missing. `reviewref.FetchPlanFor` now returns the two asks separately (`Records` unpruned,
  `Mirrors` pruned), `git.Repo` has `FetchRefs` and `FetchPruned` rather than one `FetchRefspecs`, and
  `--fetch` costs three invocations rather than two. The M1 spike had measured that prune was *scoped*; it
  had not measured it against a clone that had something to lose.
- The remote is resolved once per run and cached on `app`, because `--fetch` and the comparison ask the
  same question in the same command.
- PRD §13 now documents both the two-fetch split and why, plus the three rules of the finding; README's
  namespace paragraph carries the same shape in fewer words. PRD says publishing is ordinary git for now;
  M3 replaces that sentence with the command.

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
| Done  | Does a remote-tracking name of the shape `refs/remotes/origin/refs/git-pair/integrations/<id>` confuse the plumbing we use? No — measured on git 2.43 in a real two-repo fixture: `for-each-ref`, `rev-parse` and `check-ref-format` all handle it, `for-each-ref refs/git-pair/` does **not** see it (so mirrors cannot leak into the write namespace), and `git fetch --prune origin '+refs/git-pair/*:refs/remotes/origin/refs/git-pair/*'` prunes inside that subtree only — `refs/remotes/origin/main` and unrelated `refs/remotes/origin/*` entries survive a prune. Consequence adopted: `--fetch` uses `--prune`, because a mirror that outlives the ref it mirrors would report a deleted record as published. | M1 |
| Done  | Does fetching with a `+` on the mirror side and none on the records side behave as intended, and does a no-op re-fetch cost anything? Yes on both: one `git fetch --quiet --no-tags --prune` with both refspecs, and a re-fetch with nothing changed exits 0 without touching refs. | M1 |
| Open  | What does `--fetch` cost in a CI loop against a remote with many refs and no `refs/git-pair/*` at all — is it a per-tick cost worth warning about in the flag help? Measured locally at small scale only. | M1 |

## Risks

**R1 — The §26 exception spreads.** "One audited helper may push the namespace" is one sentence from
"anything may push". Mitigation: the amendment is written in PRD §26 as a scope, the hygiene test
permits the verb *inside one file*, `TestOnlyTheAuditedHelperPushes` fails on any other call site, and
`TestPublishCannotLeaveTheNamespace` pins the argument-level refusals.

**R2 — A mirror read as a record.** Two distinct failure modes, and the spike separated them.
Fetched copies *inside* `refs/git-pair/` are records — replication is the point, and `Taken` refusing a
new id that exists remotely is correct rather than a bug. What must never happen is a mirror under
`refs/remotes/<remote>/` standing in for a record: it is the comparison basis, not the fact.
Mitigation: the write-side readers (`Present`, `Taken`, `ResolveIntegration`, `ResolveArchive`) read the
namespace and nothing else — asserted by `TestMirrorsAreNeverRecords` — and `--prune` keeps the mirrors
from outliving what they mirror.

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
