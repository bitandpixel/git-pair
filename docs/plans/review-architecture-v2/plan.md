# Review architecture v2: two frozen refs, local-first landing

## Goal

Take git-pair's durable layer out of the active review loop entirely. While work is in flight the branch
is the whole story — implementation and review interleaved, nothing else to consult. At landing, one
command writes two create-only refs that together are the permanent paper trail: what was reviewed, and
what it became. A rebase after an approval stops being able to carry that approval forward, and a landing
that never got recorded becomes a state git-pair reports instead of a changeset that silently vanishes.

## Success criteria

Observable outcomes, not implementation details:

- `git pair change ready`, `change unready`, `change abandon`, `review submit` and `change archive` write
  **no refs at all**. `git pair status` on a branch in flight shows no ref, and every lifecycle transition
  is still derived from `Review-*` markers on the branch.
- Exactly two durable ref namespaces exist: `refs/git-pair/integrations/<id>` and
  `refs/git-pair/archive/<id>`. Both are written by `git pair integration record`, both are created once,
  and no command in the codebase can move or delete either.
- `git pair integration record` writes **both** refs: `--source` names the value of `archive/<id>`,
  `--commit` the value of `integrations/<id>`. Run twice with the same pair it succeeds and changes
  nothing; run with the same pair where only one ref exists it completes the pair; run with a conflicting
  pair it refuses and names both the existing and the requested target.
- `git pair integration record` with no flags, on the changeset branch, on a repository whose destination
  branch holds the merge, derives both SHAs, verifies them, and writes both refs.
- After a rebase that rewrites history the approval spoke about, `git pair check` exits non-zero and says
  the approved commit is no longer in this line of history. A fast-forward does not do this. A
  tree-identical rebase does not do this either — today it does.
- `git pair status` and `git pair queue` report a changeset directory present in the destination branch
  with no `integrations/<id>` ref as **landed, unrecorded**, with the `integration record` invocation
  printed. In a clone with no `refs/git-pair/*` at all they say the namespace is unfetched instead, once,
  and emit no per-changeset warnings.
- `git pair queue` produces one row per branch. Two branches carrying the same changeset id produce two
  rows with two states.
- A stacked child whose parent branch moved — implementation commit, review commit, approval, rebase, or
  merge — loses its approval, and the reason names which of those five happened.
- `mise run check`, `docs/plans/completed/gitpr-mvp/artifacts/e2e-29.sh` and `pty-walkthrough.sh` pass at
  every milestone.
- PRD.md and README.md describe the two-ref model, and neither mentions an archive ref that moves.

## Context

`requirements.md` in this directory is the spec, supplied in chat on 2026-09-21. It was written against a
design that overlaps the current one in most places and contradicts it in three: a moving per-changeset ref
(current code), a third `refs/git-pair/reviews/*` namespace (nothing in the code), and a seven-field
integration record (the code stores an object id and nothing else, deliberately, PRD §13.2).

`reconciliation.md` in this directory is the full analysis of that gap — sixteen numbered discrepancies,
each with the code and PRD citation, and the reasoning behind the decisions below. It is the supporting
document; this plan is the contract. Read it before reopening anything in Decisions.

What exists now, on `origin/main`:

- Five states derived from markers on the branch (`internal/lifecycle/lifecycle.go`), with the tree
  question — "is the reviewed content still at HEAD?" — answered by `ReconcileStaleness` over paths
  outside `changesets/<slug>/` (`lifecycle.go:107-131`).
- One movable ref per changeset, `refs/git-pair/changesets/<id>/archive`, written by five commands and
  read by eight call sites, including two that use it as state (`resolve.go:388-401` for candidate
  ordering, `:485` for terminality).
- One create-only ref per landed changeset, `…/integration`, discovered via the archive ref pointing
  exactly at `--source` (`reviewref.ArchivesAt` → `integration.go:100`).
- A queue that collapses branches by changeset id (`review.go:441-486`).
- Stack support consisting of one `base:` string, with no parent-movement comparison anywhere.

## Constraints

- PRD §26 and `internal/hygiene/hygiene_test.go` forbid `push`, `merge`, `rebase`, `reset`, `switch`,
  `checkout`, `branch`, and `update-ref -d` in shipped code. `merge-base` is explicitly permitted, which is
  what makes the rebase check in M2 legal. This plan adds no forbidden verb.
- `--json` `state` is an agent contract held at five values; new facts sit beside it, the way
  `integrated` and `abandoned_commit` already do.
- Hard renames, no dual-parsing, no deprecated aliases. Precedent: `gitpr`→`git-pair`,
  `GitPR-*`→`Review-*`, `review close`→`change complete`, `refs/reviews/*`→`refs/git-pair/changesets/*`.
- PRD.md and README.md are the specification and move in the same commit as the behaviour they describe.
  Every milestone below therefore carries its own spec edits; none of them may be deferred to the last
  milestone.
- `e2e-29.sh` and `pty-walkthrough.sh` are live gates and currently exercise the moving archive ref. They
  break in M1 by design and are rewritten in the same commit.
- Nothing in this plan fetches or pushes. `git.Fetch` (`git.go:514`) takes no refspec and stays that way
  until the remote door is opened on purpose.

## Decisions

### Settled with the reviewer on 2026-09-21

**D1 — Scope is the local loop.** Six entry points: `change init`, `change ready`, `review *`,
`change wait`, `change feedback` + `change ready`, and landing. Remote and CI stay a door: the two refs are
append-only so publishing them is a refspec, and `integration record` already accepts two SHAs from a
caller that has them. *Rejected: a distributed queue registry; a continuously synced remote changeset ref.*

**D2 — Two refs, both create-only, both written at landing.**
`refs/git-pair/integrations/<id>` → the commit that introduced the changeset into the destination branch;
`refs/git-pair/archive/<id>` → the real unsquashed, unrebased tip. Written in that order by one
`integration record` invocation. *Rejected: the moving archive ref (a mutable coordination ref, and the
thing this spec exists to remove); one ref with the lineage left to per-review anchors (durability would
depend on another clone having published its anchors); a metadata commit or annotated tag for the
seven-field record (the pair of refs plus the tree rule already answer all seven, and PRD §13.2's
"an object id and nothing else" survives).*

**D3 — No `land` command.** git-pair performs no merge. The contract is `git pair check` → an ordinary
git merge or squash, performed by the agent on human instruction → `git pair integration record`.
*Rejected: `git pair land` performing the merge in a temporary worktree — it needs a PRD §26 change, a
hygiene carve-out for `merge`, and a worktree policy, and it buys no determinism that M3 does not already
provide.*

**D4 — Record before tidy, by convention, and detection as the backstop.** The agent contract puts
`integration record` before any commit that could disturb the tree evidence. The appearance detector in
M4 is the human-facing backstop. *Rejected: a deletion-path detector for changeset directories removed by a
landing commit — this project has no practice of tidying `changesets/` before landing, so the case is
hypothetical and its detection is the expensive kind.*

**D5 — Unrecorded landings are a reported state.** Directory present in the destination branch, no
`integrations/<id>` ⇒ `landed, unrecorded`, printed by `status` and as its own `queue` section, guarded by
namespace presence so an unfetched clone gets one fetch message instead of N warnings. *Rejected: git hooks
and background checks; changing an exit code because of someone else's merge.*

**D6 — `Review-Head` carries the rebase rule; per-review anchors are deferred.** A review commit records
the commit it spoke about, and an approval counts only while that commit is an ancestor of HEAD. Rebase
rewrites the commit but preserves its message, so the rewritten marker still names the pre-rebase head and
the ancestry test refuses it. *Rejected: keeping `refs/git-pair/reviews/*` now — it is a third namespace,
and allocating `123` is a read-modify-write across clones, which is the coordination this spec bans.*
The consequence is stated in R5 below: after a rebase the original review commit is reflog-only, so
"inspect it exactly as submitted" is deferred with the anchors.

**D7 — The tree rule stays.** PRD §4's comparison against the destination branch's tree remains the way
active work is recognised; the integration ref is the skip signal beside it; landed `changesets/<id>/`
directories live in the destination branch permanently. That is also what makes landing a visible
transition, which M3's derivation depends on.

### Proposed in this plan, open for this changeset's review

**P1 — Verification strictness.** All four checks in M3 refuse by default. If landing a stack as a single
merge turns out to break check 4 (see Spike S1), check 4 becomes a warning for merge commits with more than
one changeset in the transition, and that exception gets its own test rather than a general relaxation.

**P2 — `CHANGESET.yaml` stack shape.** `parent: <branch>` and `parent-changeset: <id>` become the stacked
form, and `parent:` *is* the base: a file that sets both `parent:` and `base:` is refused at read time the
way `ErrIDMismatch` and `ErrBaseConflict` refuse inconsistent metadata today. The `base:` spelling that
names a deleted namespace (`refs/git-pair/changesets/<x>/archive`) stops being accepted.

**P3 — Naming.** `git pair queue` and `git pair init` become the documented spellings, hard-renamed with no
aliases, matching the draft's names and the repo's precedent. Everything else keeps its current spelling.

**P4 — Reviewer identity and resolution state stay out of scope.** Both are in the draft's artifact list
and neither has a consumer yet; `check` gating on open threads would change the verdict for every existing
repository. Deferred with the anchors unless a concrete need appears during implementation.

## Milestones

### M1 — Ref substrate: two frozen families — **done 2026-09-21**

**Deliverables**

- `refs/git-pair/integrations/<id>` and `refs/git-pair/archive/<id>` are the only durable refs, and both
  are create-only.
- No command writes a ref during the review. `change archive` no longer exists as a command; `status`
  reports lifecycle from markers alone.
- `reviewref.Update`, the archive-freeze check, and the five write sites are gone.

**Tasks**

- [x] Split `internal/reviewref` into the two flat families. Delete `Update`, `ErrArchiveFrozen`,
  `RefuseIntegrated`, `Namespace`, the `<id>/<child>` parser in `ChangesetID`, and the leaf/namespace
  comment block that only the old nesting needed (`reviewref.go:15`, `:106-125`).
  `RefuseIntegrated` moved rather than disappeared: the refusal of a recorded changeset belongs with the
  thing that writes markers, so it is exported from `internal/marker` and called by `Commit`/`CommitPaths`
  and by the three `change` commands before their no-op paths. `Taken` checks the two exact paths, so a
  nested legacy name reserves nothing.
- [x] Make create-only tolerant of an identical re-create: same target is a no-op, different target is the
  refusal. `CreateRefIfAbsent` (`reviewref.go:195`) is the shared primitive — and it lives in
  `internal/git` now, as the atomic `update-ref <ref> <sha> <zero-oid>`, because that is the layer that
  owns git invocations. `reviewref.CreateOnly` and `CreatePair` are its two callers.
- [x] Delete the write sites: `change.go:547` (ready), `:675` (unready), `:785` (abandon), `:990` (archive),
  `reviewops.go:67` (submit). `git.UpdateRef` is deleted too, so there is no API left that could move a
  ref; the hygiene test asserts one `update-ref` in shipped code and that its old-value is the zero oid.
- [x] Delete `change archive` (`newChangeArchiveCommand`, `change.go:859`) and move its two gates — surviving additions and
  drift — behind the `check` path that already runs them; its squash-safety output moves to M5.
- [x] Point `Present`, `Taken`, `FetchRefspec` and `FetchCommand` at `refs/git-pair`; return both families from
  `List` in the single existing `for-each-ref` pass. `FetchRefspec` carries no `+`, because a fetch that can
  clobber a create-only ref is a way around the rule.
- [x] Replace the state reads in `candidateFor` (`resolve.go:388-401`, `:485`): candidate ordering and
  terminality come from the markers on the branch, not from a ref tip. Ordering is the newest commit
  touching `changesets/<id>/`, computed only when two candidates survive; `Candidate` lost `Review` and
  `Terminal`.
- [x] Rewrite `e2e-29.sh`, `pty-walkthrough.sh`, the `gittest` fixtures, and PRD §3, §9.5-§9.7, §12, §13 and
  README's Quickstart transcript, which currently prints an archive-ref line.

**What landed differently**

- `change archive`'s squash-safety output is retired, not moved to M5: the claim was "the archive is at
  `HEAD`, so a squash loses nothing", and with no pre-landing ref there is nothing left to compare `HEAD`
  against. M5 keeps the fetch guidance and next-action wording.
- Part of M3 came forward: `integration record` derives the changeset from content (the directories
  `--source` carries and trunk does not) and writes **both** refs. With `change archive` gone there was no
  other write point for the archive family, and discovery-by-archive-ref would have kept a ref written
  during review alive. M3 keeps its verification checks and the flagless local flow.
- `check --json` lost `archive` and `archive_current` here rather than in M2/M3 — keeping the keys would
  mean reporting a ref that no longer exists — and `check` no longer reads the archive family at all.
- Fetch guidance moved to the commands that read the namespace: `integration record` warns and records
  anyway (its candidate rule reads trees), `review queue` stays silent, and `check` says nothing about
  refs. §13.4 says which silence is the one to be careful with.
- PRD and README moved further than the listed sections, because both named the old refs in §4, §8, §9.2,
  §9.8, §10.4, §10.6, §11.1, §11.3, §11.4, §19.3, §21, §22, §25, §26 and §29 (PRD) and in Concepts, the
  command table, the JSON contracts and Troubleshooting (README). §9.5 was repurposed as "Handing the work
  on" rather than deleted, so §9.6-§9.8 keep their numbers and their cross-references.
- `e2e-29.sh` was extended rather than rewritten — it now covers both refs, the idempotent re-record, the
  conflicting-record refusal, and the four commands that refuse a recorded changeset.
  `pty-walkthrough.sh` needed no change: the ref it moves mid-session is a branch, not a git-pair ref.
- The TUI holds no archive ref at all now (`Session.archiveRef` and `ArchiveRef()` are gone), so its
  vanished-commit tolerance is exercised only by the existing span tests
  (`TestStepSpanSkipsAStopThatNoLongerResolves` and friends) and by the walkthrough's branch-drift
  scenario. M2's task to rewrite `session.go`'s rewrite-tolerance comment is still open.

**Verification**

- [x] `mise run check`.
- [x] New unit tests: create-only idempotency, conflicting re-create refusal, `Taken` across both families.
- [x] Integration tests asserting that `change ready`, `change unready`, `change abandon` and `review submit`
  leave `refs/git-pair/*` empty — one test per command, so a future write site fails a named test
  (`internal/cli/in_flight_refs_test.go`).
- [x] The TUI passes `pty-walkthrough.sh`. M1 removed the TUI's only git-pair ref, so the session code
  that tolerates vanished commits (`tui/session.go:242`) is exercised by the branch-drift scenario and the
  existing span tests rather than a new one; its comment is M2's to rewrite.

### M2 — `Review-Head` and the rebase rule

**Deliverables**

- Every review commit records the commit it reviewed.
- An approval whose reviewed commit is not an ancestor of HEAD does not license integration, and the
  refusal explains the lineage rather than the tree.
- The draft's invariant 8 is enforced, without a third ref namespace.

**Tasks**

- Add a `Review-Head` trailer written by `reviewops.Submit` and read by `lifecycle.parseEvent`
  (`lifecycle.go:198-230`); carry it on `lifecycle.Event` and expose it in `status` and `review history`.
- Add the ancestry condition with `IsAncestor` (`git.go:246`, `merge-base --is-ancestor`), which the hygiene
  test already permits.
- Reverse the two comments that currently defend rewrite-tolerance (`change.go:977-980` and, in the TUI,
  `session.go:242`) so they describe the new rule instead of the old one.
- PRD §12 and §11.3 updated for the new condition; README's rebase guidance rewritten.

**Verification**

- Table-driven tests: linear rebase → refused; tree-identical rebase → refused (this fails today and is the
  headline case); fast-forward → accepted; amend of an unreviewed commit → unaffected; approval followed by
  a metadata-only commit → accepted.
- `check --json` carries the approved head and the verdict reason; contract tests pin the shape.

### M3 — `integration record` as the two-ref write point

**Deliverables**

- One invocation writes both refs; the local flow needs no flags; the CI flow keeps `--source`/`--commit`.
- A wrong pair of SHAs is refused rather than recorded.
- Discovery no longer depends on a ref that does not exist yet.

**Tasks**

- Discover the changeset from content: directories in `--source`'s tree minus the destination branch's
  (`changeset.DirsAt`), cross-checked against `Review-Changeset` in `--source`'s lineage. Agreement is
  required; disagreement refuses.
- Verify, refusing by default per P1: the changeset is named by both readings; an approving marker exists in
  `--source`'s lineage; `--commit` is reachable from the destination branch (`--target` stops being optional
  wherever the branch is available); `--commit` adds `changesets/<id>/` over its first parent.
- Derive both tips when the flags are absent: archive tip from the changeset branch HEAD or the newest
  `Review-Head`; integration tip from the first-parent transition rule in
  `research/2026-09-21-landing-transition.md`.
- Write archive first, integration second; re-run completes a half-written pair.
- Rewrite the command's long help, which currently promises CI-only semantics and single-ref output.
- PRD §11.4 and §16 rewritten; README's CI block rewritten, including the fetch line for the new roots.

**Verification**

- Integration tests for merge, squash and cherry-pick landings; for the derivation of both tips; for the
  archive-then-integration ordering; for completion after a simulated partial write; and for each of the
  four refusals.
- A test proving an unreviewed head cannot be recorded as integrated.
- The gate scripts land the same changeset twice, the second time proving the no-op.

### M4 — Landed, unrecorded

**Deliverables**

- A merged-but-unrecorded changeset is visible in `status` and `queue` instead of disappearing.
- An unfetched clone says so once rather than warning per changeset.

**Tasks**

- Compute `dirsInTrunk − integrationRefIDs` inside the existing resolver, from the single `List` pass — no
  new git invocation.
- `queue` gains a `LANDED, UNRECORDED` section printing the `integration record` invocation; `status`
  prints the same finding when run on a branch that carries no changeset of its own, keeping exit 2 on the
  `ErrNoChangeset` path (`root.go:386`).
- Guard with the existing `Present` discipline (`reviewref.go:70`): empty namespace → one fetch message;
  otherwise the wording keeps both readings alive — "not recorded, or not recorded here".
- Cap the list and count the remainder.

**Verification**

- Tests for all four states: recorded, unrecorded, never-a-changeset, and unfetched namespace.
- A test pinning the cost: the resolver issues the same number of git invocations as before this milestone.
- `queue --json` contract test for the new section, with `reasons`-style arrays never `null`.

### M5 — `change archive` retired, next actions rewritten

**Deliverables**

- The approved state tells the author what to do next, including the record step.
- No command or doc still speaks of advancing an archive ref.

**Tasks**

- `status`'s `NextAction` gains the approved case: merge into `<base>`, then `git pair integration record`
  (`status.go:138`, alongside the existing integrated line at `:221`). `check`'s success path prints the same
  next step.
- Move `change archive`'s squash-safety reporting into `status`/`check`; delete the command's flag set
  (`--allow-surviving-review-additions`, `--allow-unreviewed-changes`) or relocate the flags to `check`
  where the corresponding check lives.
- `change unready` and `change abandon` documented as marker-only; PRD §9.6, §9.7 and §29 updated.

**Verification**

- Snapshot/contract tests for `status --json` and `check --json` in the approved state.
- `pty-walkthrough.sh` replays the PRD §29 loop end to end: init → ready → review → feedback → ready →
  approve → merge → record → branch deleted → `status` and `queue` on trunk read correctly.

### M6 — Queue per branch, and naming

**Deliverables**

- One queue row per branch; two branches carrying one changeset show two states (invariant 5).
- `git pair queue` and `git pair init` are the documented spellings.

**Tasks**

- Key `runReviewQueue` by branch instead of by slug (`review.go:441-486`); drop the merged `branches[]` and
  the "best of" choice in `readyEntry` (`:609`).
- Rename `review queue` → `queue` and `change init` → `init`, with no aliases; update the help text,
  README's command table, and every test that names the old spellings.
- PRD §10.6 and README's command table updated.

**Verification**

- A fixture with one changeset on two branches in different states yields two rows with two distinct
  states.
- `--json` contract test for the per-row branch field; every existing test that invoked the old spellings
  fails and is updated in the same commit.

### M7 — Stacked changesets

**Deliverables**

- A child names its parent branch and parent changeset explicitly.
- Any parent movement invalidates the child's approval, and the reason names the kind of movement.
- A parent that landed or ended leaves the child with a stated path forward instead of a broken base.

**Tasks**

- Add `parent:` and `parent-changeset:` to `CHANGESET.yaml` per P2; refuse `parent:` with `base:`; drop the
  dead `refs/git-pair/changesets/<x>/archive` spelling from `parentID` (`resolve.go:500-512`).
- Record the parent tip the child was approved against, beside `Review-Head`, and compare it in `status`,
  `queue` and `check`.
- Classify the movement into implementation commit / review commit / approval / rebase / merge, and put the
  classification in the reason. Without it the rule reads as arbitrary and people stop trusting `check`.
- Parent integrated: resolve through `integrations/<parent>` and print "parent landed as X; rebase onto
  <destination>". Parent abandoned: refuse with "unreconciled — choose a new base", and accept an explicit
  re-parent. Never reparent implicitly.
- Document that a child landed without rebasing has an archive chain that contains the parent's unsquashed
  history, and that this is expected.
- PRD §4 and §9.1 updated for the new metadata; the stacked tests in `stacked_test.go` rewritten.

**Verification**

- One test per movement class, each asserting the specific reason string, plus a test that a child with a
  parent whose branch is gone cannot pass `check`.
- A test for parent-integrated resolution against a real squash landing.

### M8 — The agent contract and the deferred list

**Deliverables**

- The integration contract exists as text the agent follows, with the canonical wording in the requirements.
- The deferred list in PRD §27 matches what this plan actually gave up.

**Tasks**

- Record the three-step contract — `check`, ordinary git merge by the agent, `integration record` — in PRD
  §29 and README, with record-before-tidy stated as the ordering rule (D4).
- Add to Deferred: per-review anchors and what they cost (R5), publishing the two ref families and the
  namespace-protected variants of them, patch-equivalent approval carry-forward, reviewer identity and
  thread resolution state.
- Decide in this milestone, not before: whether this repository carries a `skills/git-pair/` surface. The
  sibling repository `bitandpixel/pi-git-pair` carries one already, and it documents a namespace this code
  never used (`refs/reviews/archive/<slug>/<sha>`), a command this code removed (`review close`), and a pi
  command surface (`/pair-review`, `/pair-wait`) that does not exist here at all — evidence both that the
  contract is wanted and that it rots when it lives beside the code rather than in the spec. If one is
  created here, it points at the requirements rather than replacing them.

**Verification**

- The PRD §29 loop and the agent contract agree line for line with observed command output, checked by
  replaying `pty-walkthrough.sh` against the prose.
- A test that every ref path and command name appearing in PRD.md and README.md exists in the code — the
  cheapest thing that keeps this from drifting again.

## Spikes / Research

| ID | Question | Needed before |
| --- | --- | --- |
| S1 | Does the first-parent transition rule hold when one merge lands several changesets at once, and when a child is landed before its parent? | M3 |
| S2 | Does folding integration refs into the existing `List` pass really cost zero git invocations in `queue`? | M4 |
| Done | Does the transition rule identify the landing commit for `--no-ff` merges and squashes? Yes, measured — `research/2026-09-21-landing-transition.md`. | — |

## Risks

**R1 — `check --json` is a published CI contract.** Dropping `archive` and `archive_current` and adding the
lineage fields breaks anything that reads them. Mitigation: hard rename per precedent, called out in README's
Gates section, and the change lands in M2 where the condition that replaces it lands — never separately.

**R2 — Detection noise.** A false "landed, unrecorded" warning on every changeset in a freshly cloned repo
would train people to ignore the one warning that protects the paper trail. Mitigation: the `Present` guard,
the both-readings wording, the capped list, and a test for the unfetched case.

**R3 — The conservative stacked rule.** Every parent review commit trips it, so a child cannot stay approved
while its parent is under review. This is the spec's choice, not a bug, but it fails in practice if the
reason is opaque. Mitigation: M7's named causes, and a documentation line saying to rebase and re-request
after the parent settles.

**R4 — Migration.** Repositories holding `refs/git-pair/changesets/<id>/{archive,integration}` will show
every past landing as unrecorded, and `Taken` will keep the old ids reserved. Mitigation: that warning is
also the migration nudge; the migration is "run `integration record` for the ones you want linked", not a
ref rename; `Taken`'s behaviour with legacy names gets an explicit test.

**R5 — Dropping per-review anchors loses altered-history inspection.** After a rebase the original review
commit is reflog-only, so invariant 6/7 in the spec are not met by this plan, and "detect altered review
history" is unimplemented. Mitigation: refused explicitly in D6, recorded as deferred with the namespace
shape reserved so it can return without redesign.

**R6 — Naming churn.** M6 touches help text, README's command table, and every test that invokes the
old spellings. Mitigation: it is its own
milestone, so it can be dropped without disturbing M1-M5 if the review says no.

## Verification strategy

`mise run check` (gofmt, vet, tests) gates every commit. The two gate scripts under
`docs/plans/completed/gitpr-mvp/artifacts/` are treated as end-to-end contracts and rewritten alongside the
behaviour they exercise, never patched to accommodate a regression. New behaviour gets a test at the layer
where it is observable: marker and ref semantics at the unit layer, command behaviour and exit codes through
`internal/cli`'s integration harness against `gittest` fixtures, and the full human-and-agent loop only in
`pty-walkthrough.sh`. Every `--json` change ships with a contract test pinning field names and the
never-`null` array rule, because those shapes are agent contracts rather than implementation detail.

## Audit History

| Date | Audit | Summary |
| --- | --- | --- |
| — | — | No audits yet. |
