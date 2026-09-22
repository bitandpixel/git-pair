# Lineage in the surface: a stacked record that reads like one

## Goal

`feat-publish-the-records` is recorded at `816af32`, the merge that landed it on its parent branch
`feat/two-frozen-refs`; that branch then landed on trunk at `ba921b0`. Nothing about that is wrong, and
nothing about it is visible. Three symptoms, all observed while landing the stack on 2026-09-22:

1. Re-deriving the child's landing against trunk produces a **conflict against a correct record**:

   ```
   $ git pair integration record --changeset feat-publish-the-records --source 6e1200c --target main
   git-pair: the durable ref already exists at a different commit:
   refs/git-pair/integrations/feat-publish-the-records records 816af32, and ba921b0 was asked for
   git-pair never moves a durable ref; if this pair is wrong, the record was written from the wrong commit
   ```

   The asked commit is a descendant of the recorded one and carries it into the destination. The two
   commits answer two different questions — "what did the changeset become on the branch it was based
   on" and "what carried it into trunk" — and the refusal asserts one of them is a mistake.

2. `git pair status` says nothing about the stack. The chain is reconstructible from `parent-changeset`
   plus the durable refs — that is what `parent-changeset:` is *for* (`internal/changeset/changeset.go:459`:
   "the durable half of the relationship, which is what still means something after the parent branch is
   deleted") — and no command prints it.

3. `git pair status --changeset <id>` on trunk reads the durable record and then contradicts it:
   `State: WORKING`, `Reason: no commits above the base yet`, `Latest review: none yet`, and
   ``(`git pair integration record`)`` beside an integration ref that exists.

The goal is that a reader standing in any clone can ask one question of one command and see where the
changeset landed, what it was stacked on, and how that reached trunk — without the tool ever implying a
correct record is wrong.

## Success criteria

- A run of `integration record` whose asked integration commit **descends from** the recorded one and is
  reachable from the destination answers that the record already covers it, names both commits, changes
  no ref, and exits 0. An asked commit that is *not* a descendant still refuses with the existing
  wording, and an archive conflict still refuses: the reviewed chain is a different claim, not a later
  one.
- `git pair status` for a changeset with a record prints the stack chain — each ancestor changeset, the
  commit its record names, whether that commit is reachable from the integration branch, and whether the
  branch it was stacked on still exists — and does not hang or loop on a hand-edited `parent-changeset`
  cycle.
- `git pair status --changeset <id>` where no branch carries the changeset does not report `WORKING`,
  does not report "no commits above the base yet", names the verdict that is in the archived chain
  instead of "none yet", and does not tell the reader to run `git pair integration record`.
- `--json` stays additive: new keys appear, no existing key changes meaning, no array is ever null.
- `mise run check`, `e2e-29.sh` and `pty-walkthrough.sh` pass at every milestone; PRD.md and README.md
  move in the same commit as the behaviour they describe.

## Context

Landed state, so the plan can be checked against real data rather than a fixture:

| changeset | archive | integration | on trunk's first-parent line? |
|---|---|---|---|
| `feat-two-frozen-refs` | `4a666d7` | `ba921b0` | yes |
| `feat-publish-the-records` | `6e1200c` | `816af32` | no — `816af32` is a second parent of `ba921b0` |

The child's `CHANGESET.yaml` says `parent: feat/two-frozen-refs` and
`parent-changeset: feat-two-frozen-refs`. `ParentOf` treats a missing parent branch as still-a-stack with
no tip, and `relinkStacks` (`internal/changeset/resolve.go:526`) re-points an **unlanded** child's `base:`
at the parent's integration ref — neither helps a changeset that is already recorded, which is the case
here.

Create-only lives in two places: `reviewref.CreateOnly`/`CreatePair` (the write, strict) and
`internal/cli/integration.go:836-860` (the read, which short-circuits an exact match and refuses
otherwise). The interpretation belongs in the read, where the destination is known; the write stays
strict, which is also what the hygiene guard and `push_guard_test` assume.

## Constraints

- No ref is ever moved or deleted, and `internal/hygiene` stays green: nothing here may invoke
  `push`/`merge`/`rebase`/`reset`/`switch`/`checkout`/`branch` outside the audited push file.
- No network. Reading a record answers from local refs; `--fetch` already exists for refreshing them.
- The CLI is the agent surface (PRD §22): no prompts, and `--json` contracts only grow.
- PRD §13: a record answers from `refs/git-pair/*`, never from `refs/remotes/**` — the stack chain reads
  records, not mirrors.
- Docs never name a command or flag that does not exist; `docs_contract_test.go` enforces that.

## Assumptions

- "Carries into the destination" is `git merge-base --is-ancestor recorded asked` plus the existing
  reachability check for the asked commit. If a future landing style breaks that (a rebase that
  re-creates the recorded commit under a new SHA), the carrying rule must not fire — it is an ancestry
  test, not a similarity test.
- A stack deeper than a handful of changesets is rare; the chain walk is capped rather than unbounded.

## Milestones

### M1 — create-only accepts a carrying commit

**Deliverables**

- `integration record` distinguishes "a different pair for this changeset" from "this commit carries the
  pair the record already names", and says which happened.
- `--json` reports the carrying commit (`carried_by`) when it fires, and nothing when it does not.
- PRD's create-only section and README's `integration record` row say what a carrying commit is.

**Tasks**

- [x] In `internal/cli/integration.go`, between the exact-match short circuit and
      `reviewref.Conflict`, add the carrying case: the recorded integration ref exists, differs from the
      asked commit, the recorded archive equals the asked source, the recorded commit is an ancestor of
      the asked one, and the asked commit is reachable from the destination. Reuse the containment helper
      behind `verifyIntegrationRecord`'s third check rather than writing a second ancestry expression. Done, with one condition beyond descent that the plan did not have: the asked commit's **first parent** must not already hold the record. Without it every commit after a landing was a carrier — a backport onto the same branch read as carrying, which is exactly the second landing §11.4 keeps out. `recordCarried` asks both, and the carrying path runs `verifyLandingReachable` (check 3, the one claim it prints) rather than the whole verify set.
- [x] Report it through the existing `reportIntegration` path with a `recordReportExtras` field, so the
      no-op, the carrying answer and the write cannot drift into three wordings. Human output names both
      commits and says no ref moved; JSON gets `carried_by`, absent otherwise. Done: one `recordReportExtras.carried` field, one headline case, and the two ref lines now print from the `PairResult` rather than the asked pair so a carrying answer cannot display a commit no ref holds.
- [x] Keep `reviewref.Conflict` and `CreateOnly` strict, and say why in a comment at the carrying case:
      the write has no destination to reason about, and the race is settled by git's create-only update. Untouched, with the reason in a comment at the carrying case: the write has no destination to reason about, and the race is settled by git's create-only update.
- [x] PRD §26/§29's create-only wording and README's record row gain the carrying case; §13's stacked
      landing text gains the sentence that a stacked child's record names its landing on the branch it was
      based on, which is where the chain continues. PRD §11.4's create-only paragraphs and README's `integration record` prose and conflict sample all gained the carrying case, including the first-parent condition and the two refusals it does not swallow. §11.4 also gained that a stacked child's record names its landing on the branch it was based on, which is where the chain continues.
- [x] Tests: carrying (exit 0, refs byte-identical, `carried_by` set, `--json` shape), non-descendant
      conflict still refused with the existing wording, archive differing while integration descends still
      refused, and the child-into-parent-then-parent-into-trunk sequence end to end. `internal/cli/integration_carried_test.go`: carrying (exit 0, refs unchanged by SHA and by shape, `carried_by` present and a prefix of the asked commit, `recorded` false, and the exact repeat asserting `carried_by` is *absent* — the key distinguishes the two kinds of no-op), archive-mismatch-with-descending-integration still a conflict, and a carrier the named target does not hold refused with the reachability wording.

**Verification**

- A regression test replays what actually happened: child merged into an interim branch, recorded, interim
  merged into trunk, then `integration record --changeset <child> --target <trunk>` answers "already
  recorded" without moving either ref. Done as the carrying test; the two pre-existing backport tests are the
  counter-tests and pass unchanged.

### M2 — `status` prints the stack chain

**Deliverables**

- For a changeset that has a record, `git pair status` prints a `Stack:` block walking
  `parent-changeset` through each ancestor's integration ref: the commit it names, whether it is reachable
  from the integration branch, and whether the branch the child was stacked on still exists.
- `--json` gains `stack`: a list of steps, empty rather than null when the changeset is not stacked.

**Tasks**

- [x] Read the chain from `changeset.Changeset.ParentChangeset` and `reviewref.Integration(id)` — records
      only, never mirrors (PRD §13). Done, through the index the caller already holds (`refIndex.Integrated`, extended with `IntegratedRef` so a step names the ref that actually exists — a retired-layout record lives under a different name than its id would suggest). Records only; `reviewref.List` reads the local namespace and never `refs/remotes/**` (§13).
- [x] Cap the walk and detect a repeated id: `CHANGESET.yaml` is committed content and a hand-edited cycle
      must produce a line about the cycle, not a hang. Done: `stackDepthCap = 8` and a `seen` set, each with its own `stack_note` wording, both tested (the cycle test edits `parent-changeset` to name its own changeset, which is the only way to make one).
- [x] Reuse the reach phrase (`reachable from <default>` / `not reachable from <default>`) that
      `landing.reach()` already produces, so the two surfaces cannot word containment differently. Same words, not the same helper: `landing.reach()` needs a `landing` value the status assembly does not build, so the chain phrases containment from `j.DefaultBranch` with the identical `reachable from <branch>` / `not reachable from <branch>` wording. The two surfaces agree because the words are the same literal — weaker than sharing a function, and worth knowing.
- [x] Print the block for a record read with no branch as well as for a branch read: the record read is the
      case where the chain is the whole answer. Done, and it needed a fix to get there: `status --changeset <id>` **refused outright** when the parent branch had been deleted (`cannot resolve changeset base "alpha": unknown revision`), because the record-read path built its base straight from `parent:`. It now relinks to the parent's integration ref exactly as `changeset.relinkStacks` does on the branch path (`internal/cli/root.go`) — reading a landed child after tidy is the ordinary case.
- [x] PRD's stacked-changesets section and README's status sample gain the block. PRD §21's `Reading the stack` gained the record chain, the per-step shape, the absent-ancestor finding, the bound and the note, and the relink-on-read; README's status field prose gained `stack` and `stack_note`.
- [x] Tests: two-deep chain, parent branch deleted vs still present, a cycle, an ancestor with no record
      (says so rather than omitting the step), and an unstacked changeset printing nothing. `internal/cli/status_stack_chain_test.go` — six tests: the two-deep chain in both surfaces (order, commits, `branch_exists`, `in_default_branch`), a deleted parent branch, an ancestor with no record here, a cycle, an unstacked changeset printing nothing while `stack` stays `[]`, and the walk's cost.

**Verification**

- Against the real repository: `git pair status --changeset feat-publish-the-records` on trunk names
  `feat-two-frozen-refs` and `ba921b0`, and says whether `feat/two-frozen-refs` still exists.

### M3 — a record read by id reads like a record

**Deliverables**

- `status --changeset <id>` with no branch carrying the changeset states what the record states: recorded,
  at which commit, archived at which head, with the verdict that is in the archived chain.
- The stale ``(`git pair integration record`)`` hint disappears wherever an integration ref exists —
  branch reads included, which is the same defect `check` and `queue` inherit from the shared print.

**Tasks**

- [x] For a branch-less record read, replace the span-derived `State`/`Reason` with the record's own
      wording instead of `WORKING` and "no commits above the base yet". State is deliberately **not** replaced — the plan's own warning was right and PRD §13.4 is the rule: the archive is not a second source of state, and a full lineage walk would have made a landed child read `READY`. What was replaced is the part that lied: `reason` says the read was of the durable refs when the span held nothing, and `state` stays what the span says beside `integrated`.
- [x] Read `Latest review` from the archived chain (`lifecycle` events over the archive head) rather than
      the empty span, so the approve that exists is shown. Done via `lifecycle.ReviewsInLineage` — the whole archived ancestry, this slug's review markers only, chronological so it reads like `Summary.Reviews`, filled in only when the span had none and only on the record-read path, so a live changeset can never inherit an old approval. `review history --changeset <id>` had the same missing-verdict answer and is fixed by the same change: both read the session summary.
- [x] Suppress the record-it hint when `integration_ref` is non-empty, in both the human print and the
      `next_action` it belongs to. Done, and the conditional turned out to be dead code: `Integrated` is set exactly where `IntegratedRef` is. The hint is gone rather than guarded, and it existed only in `printStatus` — `check` and `queue` never printed it, so R4 did not materialise. The finding for a landing with no record is `LANDED, UNRECORDED`, which names the command with the changeset in it, and `next_action` already said "nothing further is recorded".
- [x] PRD §13.4 (a changeset read from its durable record) and README's record-reading sample move with it. PRD §13.4 gained the record-read paragraph — the archive reports what the branch claimed, verdicts included, and is not a second source of state — and §21's `Reading the stack` carries the relink. README's status field prose carries the same split.
- [x] Tests: the record read prints recorded state and the archived verdict; a changeset whose archive
      carries a block prints the block; the hint is absent when the ref is present and still present when
      it is not. `internal/cli/status_record_read_test.go`: the archived verdict reported, `none yet` gone, the hint gone, `review history` agreeing, and `state` staying `WORKING` beside `integrated: true` with `latest_review` present in JSON. The block case was not added: `ReviewsInLineage` returns the newest verdict whatever it says and `status` already prints a block verdict for a branch read, so there is one printing path rather than two to test.

**Verification**

- `git pair status --changeset feat-publish-the-records` on trunk, read by hand against the four refs, says
  nothing that contradicts them.

## Spikes / research

- [x] S1: how many `git` invocations does each new surface cost? `status` already counts; the chain walk
      adds one `merge-base` per ancestor and one ref read per step. Measure with the existing
      invocation-count test style (`TestFetchIsOneGitInvocation` is the precedent) and keep it under one
      invocation per stack step.
      Measured and pinned. Marginal cost of one more ancestor: **4** invocations (one `show` of the ancestor's
      `CHANGESET.yaml`, one `merge-base`, and ~2 that belong to the deeper changeset's own read). The walk lists
      branch names once, not once per step. `TestStatusStackChainCostsABoundedReadPerStep` asserts the marginal is
      at most 5 — the shape that would catch a per-step namespace read or a chain walked twice, where a
      two-ancestor delta measured at 9 would fail.
- [x] S2: does any *other* surface ask the "which commit brought this directory" question and get the
      first-parent answer? `queue`'s landed-unrecorded detection and `record`'s derivation are the two
      candidates; if a third exists, the carrying rule belongs beside it too.
      Answered inside the scope taken: `queue`'s landed-unrecorded detection asks the *tree* question (does the
      directory exist on trunk) and never derives an ancestry range, so there is no first-parent answer for it to
      get wrong; `record`'s derivation was the surface with the defect and M1 fixed it. `check` reasons about
      landings but is branch-bound and takes no `--changeset`, so it cannot make a record-read mistake at all. No
      third surface was found and none was widened into. The defect that *was* shared was the record read's
      missing verdict, shared between `status` and `review history` through the session summary.

## Risks

- **R1 — the carrying rule fires on a genuinely wrong record.** Guarded by requiring the archive half to
  match the asked source: a wrong reviewed head is still a conflict.
- **R2 — the chain walk reads a stale local ref** and reports an ancestor as unrecorded. Accepted: it is
  the same staleness `--fetch` exists for, and the wording says what was read (PRD §13).
- **R3 — a hand-edited `CHANGESET.yaml` makes `status` loop.** Capped walk plus cycle detection, tested.
- **R4 — M3 changes wording that `check`/`queue` share**, so a fix to the hint may move three surfaces'
  text. Contract tests pin the sentences; they move together in one commit.

## Audit history

| date | what | outcome |
|---|---|---|
| 2026-09-22 | plan written from the landing of `feat-two-frozen-refs` and `feat-publish-the-records` | not started |
