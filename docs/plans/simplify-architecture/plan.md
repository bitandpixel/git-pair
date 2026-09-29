# No durable refs: the destination branch is the record

## Goal

git-pair's durable layer is two create-only refs per changeset, written by one command at landing, plus
the machinery that exists only to serve them: a recorder that verifies a claim, a publisher, a consent
command for refspecs, a mirror namespace so a clone can tell published from unpublished, a detector for
landings nobody recorded, and two enforced invariants in `internal/hygiene` that exist because refs are
written at all. Roughly 2.7k production lines and 3k test lines including `internal/cli/fetch.go`, and
every one of them answers a question the repository can answer by itself.

This plan deletes the concept rather than simplifying it. After it:

- **A changeset is landed when its directory is in the destination branch's tree.** `changesets/<id>/` in
  trunk is the fact; there is no second statement of it. The recorder already proved exactly this before
  it wrote a ref — `verifyIntegrationRecord` checks that the commit carries `changesets/<id>/`
  (`internal/cli/integration.go:657`) and that its first parent does not (`internal/cli/integration.go:671`)
  — so the ref was a cache of a tree read, of the one kind git cannot get wrong.
- **A child of a landed parent is measured against a derived base** — `merge-base(head, destination)` —
  computed by one helper, instead of against `refs/git-pair/integrations/<parent>`.
- **Tidying old changesets moves their directories** from `changesets/<id>/` to `changesets/.landed/<id>/`,
  so the tree keeps saying "this landed, and here is its metadata" after the clutter is out of the way.
- **Nothing in git-pair writes a ref.** Not at landing, not during review, not on request. `refs/git-pair/`
  becomes a namespace the code neither reads nor writes, which is testable as a source property.

The thing this gives up is named in Risks and in Decision D2: after a **squash** merge, the unsquashed
review chain reaches trunk nowhere, and git-pair will no longer hold a copy of it. Merge-commit,
fast-forward, and rebase landings keep the whole conversation in trunk's own history, where a squash
never put it.

## Success criteria

Observable outcomes. Each is checkable by a person or an agent who has not read this plan.

- `git for-each-ref refs/git-pair` can be empty in a repository with landed changesets, and
  `git pair status`, `git pair queue`, and `git pair check` answer correctly there.
- `git pair status --changeset <id>` on trunk prints the landed chain — its markers, verdicts, and stack
  position — for a merge-commit landing, a fast-forward landing, and a rebase landing, with no branch
  carrying the changeset.
- `git pair queue` reports `LANDED UNREVIEWED` for a changeset whose directory is on trunk and whose chain
  carries no approval, and stops reporting `LANDED, UNRECORDED` — a finding about a command nobody ran.
- A child whose parent has landed prints a `Base:` that names the derived commit and the rule that
  produced it, and its diff shows only its own work after a merge landing **and** after it has been
  rebased onto trunk. The rebase case is where today's ref-named base over-reports.
- After `git pair change tidy`, the directories live under `changesets/.landed/`, and `status`, `queue`,
  `check`, `init`, and the destination walk behave exactly as they did before the move.
- No shipped source file writes a ref, and `internal/hygiene` fails the build if one starts to.
- `git pair integration record`, `git pair integration publish`, and `git pair integration configure`
  do not exist; `git pair change integrate` still declares, and declares without writing a ref.
- `mise run gates` is green at every milestone, including the three replays, which are rewritten alongside
  the behaviour they cover rather than patched to accept its absence.
- No document in the repository, and no line of the shipped agent skill, describes a durable ref as a
  thing git-pair writes. `docs_contract_test.go` fails if one does.

## Non-goals

- No replacement durable layer. No single advancing ref, no per-incarnation leaves, no trunk-side index
  file, no tags. All of them re-import the publish, lease, refspec, and force questions this plan closes.
- No `--force-with-lease`, no `remote.origin.push` refspecs, no `core.logAllRefUpdates` guidance. Once
  nothing needs publishing, there is no ref to push and no lease to get wrong.
- The two gate conditions discussed separately — a child's `check` waiting on a parent's landing, and a
  publication requirement — are **not** in this plan. The first is a gate change, not a durable-layer
  change; the second has no subject once refs are gone.
- No change to review commits, inline comments, `ABOUT.md` and thread behaviour, the survival check, or
  the review-span machinery. They read markers on a chain, and this plan changes only where the chain's
  end comes from.
- No migration of the refs a repository already holds. They stop being read; deleting them stays a human
  act with `git update-ref -d`, because git-pair does not delete refs.

## Context

The durable layer was built in three steps, and each step added a reader that the tree could have served.

| what | where | what it is really asking |
| --- | --- | --- |
| "has this changeset landed" | `reviewref.ResolveIntegration` at `internal/cli/check.go:277`, `internal/cli/status.go:470` | is `changesets/<id>/` in trunk's tree |
| "the parent landed while its branch stayed" | `internal/cli/stacked.go:182-207`, `:316-345` | same question, about the parent |
| "which chain does a landed changeset have" | `internal/cli/root.go:294-302` | walk trunk's first-parent line for this changeset's directory |
| "what is the child's base now" | `internal/changeset/resolve.go:596-610`, `internal/cli/root.go:326-333` | `merge-base(head, destination)` |
| "where does a child's work land" | `internal/changeset/destination.go:60-125` | the parent's `CHANGESET.yaml`, which the tree carries |
| "which landings were never recorded" | `internal/cli/landed.go:71-88`, `internal/cli/queue.go:132` | a ref-vs-tree difference — the finding exists only because the ref exists |

The read side is already built: `changeset.newResolver` does one `DirsAt(ctx, repo, db.Ref)` and caches the
result as `onTrunk` (`internal/changeset/resolve.go:244-255`), exported per scan as `TrunkIDs`
(`internal/changeset/resolve.go:258-265`). Landing detection is therefore closer than the ref lookup it
replaces, and it costs nothing per changeset.

What the refs genuinely provide is the unsquashed chain after a squash merge. That case is real and it is
documented as lost rather than worked around:

| landing | chain in trunk's ancestry | verdict readable after the branch dies | `LANDED UNREVIEWED` |
| --- | --- | --- | --- |
| merge commit | yes, via `landing^2` | yes | yes |
| fast-forward | yes, first-parent line | yes | yes |
| rebase merge | yes, replayed, messages intact | yes | yes |
| squash | no | **no** | **no** |

A rebase merge keeps every `Review-*` trailer, because replayed commits keep their messages, and loses
only commit identity — which is the thing `ffChainTip` currently refuses to guess and sends the operator to
`--source` for (`internal/cli/integration.go:319-329`). Deleting the archive claim deletes that refusal.

Three plans become history here and should be marked superseded: `docs/plans/review-architecture-v2/`
(the two-ref design and its create-only invariants), `docs/plans/publish-the-records/` (replicating the
namespace, its `--fetch`, its mirrors, its consent command), and milestone M1 of
`docs/plans/legacy-refs-and-remote-branches/` (retiring a retired layout — there will be no layout).

## Decisions

Recorded so a later reader finds a decision rather than an omission.

- **D1 — Landed means on trunk.** A child merged into its parent branch is *carried*, not landed: its
  directory reaches trunk only when the parent's does. Stacked work merged into a parent is legal and
  reads as carried.
- **D2 — Squash landings lose post-landing reads.** The review chain of a squashed changeset survives only
  in the clone that had the branch, and past git's reflog expiry (30 days by `gc.reflogExpireUnreachable`)
  it survives nowhere. The deferred escape is cheap and separate: `tidy` writes `reviewed:`, `verdict:`,
  `verdict_at:` into `changesets/.landed/<id>/CHANGESET.yaml`, which trunk replicates for free, so the
  *verdict* outlives a squash even though the *conversation* does not. Build it when a squash-merged trunk
  is a real configuration, not before.
- **D3 — `--fetch` leaves `status`, `queue`, and `check`.** It was added so those commands could answer
  about refs other clones had written (`docs/plans/publish-the-records/`); with no refs there is nothing of
  ours to fetch, and the no-unasked-network rule stays unbroken. `change wait --fetch` stays — it polls for
  somebody else's commits, which is a different question, and it already handles the remote-less case
  (`internal/cli/change.go:785-786`).
- **D4 — Tidy moves, and only moves.** Deletion is deferred, because deleting the directory also deletes
  the tree's statement that the changeset existed and the metadata the destination walk reads.
- **D5 — `change integrate` stays as it is, minus the ref.** The declaration is a marker commit, which is
  how it works today; `marker.RefuseIntegrated` (`internal/marker/marker.go:192-217`) becomes a tree check.
- **D6 — Nothing writes a ref, enforced.** The invariant replaces the one being deleted rather than
  disappearing with it.
- **D7 — The publish question is closed by deletion.** git-pair never pushed, nothing needed pushing, and
  the durable copy's absence is now the design rather than a failure mode.

## Constraints

- `mise run gates` is the definition of done and what CI runs, so every milestone ends green under it:
  `check` (gofmt, vet, the sharded suite), then `e2e-29.sh`, `pty-walkthrough.sh`, and `ci-integrate.sh`.
  The replays are contracts — rewritten with the behaviour, never loosened to accommodate its removal.
- `PRD.md` and `README.md` move in the same commit as the behaviour they describe. The repository's own
  rule, and the reason `docs_contract_test.go` exists.
- Exit codes are an agent contract: 0 success, 1 business-rule refusal, 2 usage error, 3 git failure
  (`internal/cli/root.go`). `--json` shapes are contracts too, each with a contract test, each array `[]`
  rather than `null`.
- git-pair does not push, merge, rebase, reset, switch, checkout, or create/delete a branch
  (`internal/hygiene/hygiene_test.go:58-80`). `tidy` therefore commits on the checked-out branch only, the
  way marker commits already do (`internal/marker/marker.go:156`).
- Two hygiene invariants are deleted with the code they guard — the single-`update-ref`-call-site rule with
  its all-zeros old value (`internal/hygiene/hygiene_test.go:647-736`), and the audited-push fence naming
  one permitted file and one permitted caller (`internal/hygiene/hygiene_test.go:89-110`, covering
  `forbiddenFuncNames`, `auditedPushFile`, `auditedPushCaller`; the code being `internal/git/push.go` and
  `internal/cli/publish.go`). D6 replaces the first; nothing replaces the second because nothing pushes.
- `internal/hygiene` reads source as syntax, not as text. A replacement invariant must do the same, or it
  will not survive its first legitimate `merge-base` call.

## Assumptions

- The trunks that matter land by merge commit, fast-forward, or rebase. If a trunk that squash-merges turns
  out to matter, D2's verdict field becomes a milestone rather than a note.
- A changeset id is unique per repository and is never reused while its directory exists anywhere under
  `changesets/`, including `.landed/`.
- `DirExists`-style tree reads are cheap enough to run per command. They already are: `DirsAt` is one
  `ls-tree`, and the resolver runs it once per scan.
- No agent depends on `archive_ref`, `integrated`, `integrated_commit`, `integrated_ref`,
  `landed_unrecorded`, or `unpublished` in `--json`. These are contract deletions; the agent contract in
  README §Agent contract is the place that would have promised them.

## Milestones

Reads move before writes are deleted, so the replays keep proving behaviour until the moment they cannot.
One commit per milestone, `mise run check` per commit, `mise run gates` at the end of each milestone.

### M1 — `.landed/` enters the tree model

#### Deliverables

- `changesets/.landed/<id>/` is a recognised place to keep a landed changeset's metadata, and is never
  mistaken for an active changeset.
- Two named predicates exist for tree reads, so every reader asks the same question the same way.

#### Tasks

- [ ] In `internal/changeset`, exclude `.landed` from the active-children listing in `DirsAt`
  (`internal/changeset/changeset.go:361-385`). Verified necessary: `DirsAt` lists every immediate child
  directory with no dotfile or underscore exclusion, so `changesets/.landed` currently becomes a changeset
  id named `.landed` and `candidateFor` then fails the branch on a missing `CHANGESET.yaml`
  (`internal/changeset/resolve.go:445-453`). One fix inside `DirsAt` covers every caller.
- [ ] Export `ActiveIDs(ctx, repo, rev)` (children of `changesets/`, minus `.landed`) and
  `LandedIDs(ctx, repo, trunkRef)` (`ActiveIDs` of trunk, plus the children of `changesets/.landed/`).
  `onTrunk` and `TrunkIDs` become the two, so nothing re-implements either.
- [ ] `internal/changeset/tidy.go`: `LandedDirPath(id)` and the pair of pathspecs
  (`changesets/<id>/`, `changesets/.landed/<id>/`) that history walks need. M4 uses them and M2's chain
  walk depends on them, so they exist before either caller.
- [ ] Move the `.landed` literal and the exclusion rule into one place with a comment naming the `DirsAt`
  trap, so the next namespace added under `changesets/` is a decision rather than an accident.

#### Verification

- Unit test: a branch carrying `changesets/active/`, `changesets/.landed/done/`, and a `.landed` entry of
  its own lists exactly `active` as active and `done` as landed, and the branch resolves rather than
  erroring.
- Unit test: `ActiveIDs` of a revision with no `changesets/` at all is empty, not an error, matching the
  `DirsAt` unknown-revision tolerance at `internal/changeset/changeset.go:364-366`.
- `mise run gates` unchanged in behaviour: nothing reads `LandedIDs` yet.

### M2 — Landing is a tree fact

#### Deliverables

- `status`, `queue`, and `check` decide landed-ness from the destination's tree. No ref read is on any of
  those paths.
- `git pair status --changeset <id>` on trunk reads a landed changeset's chain from trunk's history.
- `LANDED UNREVIEWED` replaces `LANDED, UNRECORDED` in `queue`, human and `--json`.
- The write gate refuses markers for a landed changeset without consulting a ref.

#### Tasks

- [ ] `internal/cli/landed.go`: replace the ref index (`:71-88`) and `unrecordedLandings` (`:99-110`) with
  the unreviewed detector — a landed id whose chain carries no approval marker. Keep the file's own
  reasoning for the heading (`:16-22`): a finding worth a heading is one the reader must act on, and "the
  author stopped one command early" is no longer a finding anybody can act on.
- [ ] Chain range for a landed changeset: the contiguous run on trunk's first-parent line carrying this
  changeset's directory, both pathspecs from M1. Range end is the newest commit in the run, range start the
  commit that introduced the directory. Feed it to the existing
  `lifecycle.Summarize(ctx, repo, slug, base, head)` (`internal/lifecycle/lifecycle.go:98`) so the state,
  verdict, and staleness reads are the same functions the live path uses.
- [ ] `internal/cli/root.go:294-302`: the archive-ref fallback becomes the trunk walk above. Keep the
  distinction the current comment draws (`:296-300`) — a changeset that never landed has nothing to read,
  and the usage error should still say so rather than inventing a chain.
- [ ] `internal/cli/status.go:461-480`: drop `archive_ref`, `archive_commit`, `integrated`,
  `integrated_commit`, `integrated_ref`; add `landed`, `landed_commit`, `landed_in_default_branch`,
  `chain_base`, `chain_head`, `reviewed`. Update `internal/cli/contract_test.go` and the README JSON table
  in the same commit.
- [ ] `internal/cli/check.go:277-284`: `g.Recorded` and `g.Where` become the tree landing and its reach.
  Add the unreviewed reason to `integrationReasons` in the documented order — history, then content, then
  the tree, then landing hygiene (`internal/cli/check.go:334-347`) — so the reader is told what the
  reviewers decided before being told the paperwork is tidy.
- [ ] `internal/marker/marker.go:192-217`: `RefuseIntegrated` asks the tree. The comment's two reasons
  (an author who forgot the branch was merged; a reviewer working from an old clone) both survive a tree
  read, and the exit code stays 1.
- [ ] `internal/cli/queue.go`: `:132` becomes the unreviewed detector, `:162` the tree landing,
  `:324-350` the orphan classification loses its ref lookups, `:242-248` and `:261-280` lose `unpublished`
  and rename `landed_unrecorded` to `landed_unreviewed`.
- [ ] Rewrite `scripts/gates/e2e-29.sh`'s 43 durable-layer assertions to assert the tree facts instead, and
  add a case where the branch is deleted before `status --changeset` is asked about it.

#### Verification

- CLI tests on `gittest` fixtures, one per landing shape: merge commit, fast-forward, rebase merge. Each
  asserts `queue` reports the changeset landed, `status --changeset` prints the verdict and the chain
  endpoints, and `check` refuses with the unreviewed reason when the fixture drops the approval marker.
- One test per squash case, asserting the *limitation* — `status --changeset` says the chain is not
  readable here and names why. A limitation nobody tests turns into a bug report.
- A repository fixture with `refs/git-pair/*` refs present and inconsistent: every answer comes from the
  tree, so the refs are inert. This is the test that proves M5 is safe.
- `mise run gates`.

### M3 — Derived bases replace the parent's integration ref

#### Deliverables

- One exported helper answers "what is this changeset measured against", and every surface uses it.
- `Base:` prints a derived commit plus the rule that produced it, for a child whose parent has landed.
- The destination walk reads the parent's metadata from the tree.
- `relinkStacks` and the record-path relink no longer read a ref.

#### Tasks

- [ ] `internal/changeset`: `BaseFor(ctx, repo, child, destination) (ref, why string)`, rules in order —
  (1) parent branch exists and has not landed → the parent branch; (2) otherwise
  `merge-base(childHead, destination)`, with `destination` from the walk below; (3) neither resolvable →
  the default branch with `why` naming the fallback. Cache the value in the scan beside `onTrunk` so five
  call sites cannot disagree; the known weakness recorded in
  `docs/plans/lineage-in-the-surface/plan.md` M2 (two surfaces agreeing only because they print the same
  literal) is the cautionary case.
- [ ] `internal/changeset/resolve.go:596-610`: delete the ref-driven relink; `relinkStacks` becomes the
  derived base or disappears into `BaseFor`.
- [ ] `internal/cli/root.go:326-333`: same, on the chain-read path.
- [ ] `internal/changeset/destination.go:60-125`: walk `ActiveIDs`/`LandedIDs` and read the parent's
  `CHANGESET.yaml` from the tree instead of from the landing commit's tree. This also fixes the case the
  current code breaks on — a landing that carried no directory of its own (`:109-113`).
- [ ] `internal/cli/stacked.go:182-207` and `:316-345`: parent-landed and parent-gone read the tree.
  `st.landedFull` becomes the derived base, and the `landedBaseIsTheSameWork` comparison (`:381-402`) keeps
  its shape with the derived value on the "now" side.
- [ ] `internal/cli/status.go:586-640`: the `Stack:` block prints landed-ness and reach per ancestor with
  no per-ancestor ref read; the `record:` line becomes a `landed:` line.
- [ ] `--json`: `stack[].integration` becomes `stack[].landed_commit`; `base_ref` gains `base_why`.
  Contract tests in the same commit.

#### Verification

- Unit tests for `BaseFor`, one per rule, plus the two landing shapes and the rebased-onto-trunk case. The
  rebase case is the one that beats today's behaviour: assert the child's diff contains only its own work,
  where a ref-named base would have included trunk's later commits.
- The rebase-merge stack case: the parent's commits are replayed under the child, so
  `landedBaseIsTheSameWork` reports "not the same work" and the advice names the rebase. Assert that, because
  it is correct behaviour arriving from a derived value rather than from a rule.
- A clone where the parent branch still exists after landing: rule (1) answers, and no derivation runs.
- `mise run gates`.

### M4 — `git pair change tidy`

#### Deliverables

- One command moves landed changeset directories out of the way, on the checked-out branch, as an ordinary
  commit that can ride its own changeset and its own review.
- `status`, `queue`, `check`, `init`, and the destination walk behave identically before and after.

#### Tasks

- [ ] `internal/cli/tidy.go`: `git pair change tidy <id>…`, `--all-landed`, `--dry-run`, `--json`. Moves
  `changesets/<id>/` to `changesets/.landed/<id>/` in one commit on the checked-out branch, written with the
  same commit machinery as a marker (`internal/marker/marker.go:156`), and touching nothing else.
- [ ] Refusals, each exit 1 with its own reason: the id is not landed (nothing to move); some active
  changeset names it as `parent-changeset:` (`changeset.ScanBranches` already reads every branch, so both
  guards cost one pass); the working tree is dirty outside `changesets/`, as `check` and `change integrate`
  already require; the id is not present on this branch.
- [ ] `init`'s name check: the `reviewref.Taken` role (`internal/reviewref/reviewref.go:168-184`) becomes
  "the id is not present under `changesets/` or `changesets/.landed/`, and no branch carries it". Weaker
  than a durable ref by design, and the `.landed/` half is what stops the reuse that matters.
- [ ] README: tidy is a changeset like any other — the reviewer sees the whole list as renames in one span,
  and the move reaches trunk through the normal flow.
- [ ] `scripts/gates/e2e-29.sh`: tidy a landed changeset, then re-run every assertion the plan touches
  (status, queue, check, `--changeset`, init on the freed name) and require them to hold.

#### Verification

- CLI test: a tidy commit moves only the named directories, its diff is renames only, and a second run is a
  no-op naming the reason.
- CLI test: refusing a still-in-flight id, and refusing an id named as a live child's `parent-changeset:`.
- CLI test: after tidy, `LandedIDs` still reports the id landed and `ActiveIDs` does not report it active,
  on the tidied branch and on trunk.
- `mise run gates`, including the walkthrough — the walkthrough's own scratch repository is where a
  rename-vs-delete difference would show up as a missing changeset.

### M5 — Delete the durable-ref subsystem

#### Deliverables

- `refs/git-pair/` is unread and unwritten. The commands, the package, the push helper, the invariants, the
  CI steps, and the `--fetch` that existed for the refs are gone.
- A hygiene invariant fails the build if shipped source writes any ref.

#### Tasks

- [ ] Delete `internal/reviewref` (467), `internal/cli/integration.go` (1366), `internal/cli/publish.go`
  (415), `internal/cli/configure.go` (281), `internal/cli/published.go` (210), the ref half of
  `internal/cli/landed.go` (184), `internal/git/push.go` (204), and their tests (~3.0k lines).
- [ ] Delete the commands and their registrations: `integration`, `integration record`,
  `integration publish`, `integration configure`. `git pair change integrate` stays.
- [ ] `internal/hygiene/hygiene_test.go`: drop the single-`update-ref` rule (`:647-736`) and the audited-push
  fence (`:89-110`); add, in the same syntax-reading style, that no shipped file passes `update-ref`,
  `symbolic-ref`, or `git tag` to git, and that no string literal under `refs/git-pair/` is built outside
  test fixtures. Keep the layer A/B/C structure so a spread slice cannot hide a write.
- [ ] `--fetch`: delete `internal/cli/fetch.go` (113) — its help text already says the flag exists for the
  durable refs and their mirrors (`:24`), and its three users are `status.go:58`, `check.go:72`, and
  `queue.go:88`. `change wait --fetch` is a separate flag (`internal/cli/change.go:713`) that polls for
  somebody else's commits, and stays. README's `--fetch` rows and the agent contract go in the same commit.
- [ ] `.github/workflows/git-pair-integrate.yml`: drop the record and publish steps and the `fetch-depth: 0`
  justification (`:69-70`, `:91-96`); the job's remaining work is gate, merge, push. Keep the
  `workflow_run`→`schedule`→`dispatch` shape and the comment explaining why there is no `push` trigger.
- [ ] `scripts/gates/ci-integrate.sh` (313 lines): drop the record, publish, and "already recorded"
  scenarios; keep gate, merge, re-run-is-a-no-op, conflicting-merge, and probe cases; keep the assertion
  that the two workflow files still name each other.
- [ ] Mark `docs/plans/review-architecture-v2/`, `docs/plans/publish-the-records/`, and
  `docs/plans/legacy-refs-and-remote-branches/` superseded, each with a line pointing here.
- [ ] PRD §13, §12, §21, §22, §26, §29 and README §Concepts, §Command reference, §JSON contracts,
  §Configuration, §Landing a declared change from CI: rewrite in the same commits as the deletions, and
  state the squash limitation (D2) where §13 currently promises the archive.

#### Verification

- `git grep -n 'refs/git-pair' -- '*.go'` is empty outside test fixtures that deliberately plant inert refs.
- The new hygiene test fails alone: a scratch `r.Git(ctx, "update-ref", …)` in shipped code is caught, and
  `repo.MergeBase` still passes — the same "syntax, not substring" property the file documents at `:16-32`.
- A fixture repository seeded with `refs/git-pair/archive/*` and `…/integrations/*` produces byte-identical
  command output to one without them.
- `mise run gates`, and one full CI run on the branch: the integrate job merges without touching a ref.

### M6 — No document still describes a ref

#### Deliverables

- The shipped skill and every document describe the workflow as it now is.
- `docs_contract_test.go` fails on a surviving reference to a durable ref.

#### Tasks

- [ ] `internal/cli/docs_contract_test.go:117-127`: `TestRefPathsInTheDocsAreOnesWeWrite` becomes
  "no `refs/git-pair/` path appears in any document, README, PRD, or skill file", with no whitelist. The
  migration prose that needed the old whitelist entry becomes a paragraph in this plan, which is not a
  document the test reads.
- [ ] `skills/git-pair/SKILL.md` and `references/integration.md` (11 mentions between them): the lifecycle
  story without refs — ready, review, submit, integrate, merge with ordinary git, tidy when the list grows.
  `references/cli.md` (10 mentions) loses the three commands.
- [ ] README §The agent skill and §Agent contract: what an agent can now assume about a landed changeset,
  and what it must not assume about a squashed one.
- [ ] Sweep PRD and README for prose that assumes a second statement of a fact — "recorded", "unrecorded",
  "published", "paper trail" — and rewrite each to the tree fact or delete it.

#### Verification

- `mise run check` (which runs the docs contract tests).
- `TestCommandsNamedInTheDocsExist` and `TestEveryCommandIsNamedInTheDocs` still pass, so the deletions left
  no orphan help text and no undocumented survivor.
- Read `skills/git-pair/SKILL.md` top to bottom and follow it against a scratch repository. A skill that
  only passes a parser test is how an agent ends up running a command that no longer exists.

## Spikes / research

Record conclusions here before implementation, one file per question under `research/`.

- **S1 — the landed run under each landing shape.** For merge-commit, fast-forward, and rebase landings,
  does `git log --first-parent -- changesets/<id>/ changesets/.landed/<id>/` give one unambiguous
  (start, end) pair, including after a tidy on trunk? The tidy case is the one expected to bite: once
  trunk's current tree holds `.landed/<id>/`, a walk using only the old pathspec finds nothing.
- **S2 — the derived base against the ref, per shape.** Compare `merge-base(head, destination)` with the
  value `refs/git-pair/integrations/<parent>` named, for the same fixture, across merge, fast-forward,
  rebase, and rebased-onto-trunk child cases. Expected: equal except in the rebased case, where the derived
  value is tighter. If they differ anywhere else, M3 is wrong and the difference is the finding.
- **S3 — `git pair status --changeset <id>` with no branch and a squashed landing.** Confirm the honest
  output is a refusal that names why, and that no partial chain is printed as if complete.
- Recorded from this planning conversation, so the questions are not re-opened:
  `core.logAllRefUpdates = true` does not cover custom ref namespaces (verified: no reflog entry for a
  non-fast-forward `update-ref` under `refs/git-pair/`; `= always` did log one), `DirsAt` has no dotfile
  exclusion, and git's per-ref push policy (`remote.<name>.push` replacing `push.default`, `+` meaning
  force-without-lease, `--force-with-lease` needing a fetched basis) is unstable enough that removing the
  need to publish is a better answer than configuring it.

## Risks

- **A squash-merged trunk loses the review record.** Not degraded — lost, after reflog expiry, everywhere.
  Mitigation: D2 states it in PRD §13 rather than in a footnote; the deferred verdict field is the cheap
  answer if it turns out to matter; and "do not squash-merge changesets" belongs in the skill. Accepting
  this is the whole price of the plan, so it must be visible in the documents, not discovered.
- **Two surfaces computing the same base differently.** The failure mode this plan creates, because the
  base is now derived in three places instead of read from one ref. Mitigation: one `BaseFor`, cached in the
  scan, with the disagreement tested at each consumer rather than assumed away.
- **`.landed/` breaks the "first commit that added the directory" derivation.** Mitigation: M1 ships both
  pathspecs before any walk exists, and S1 measures it before M2 depends on it.
- **A freed id is reused.** `reviewref.Taken` was absolute; the tree check is not, because a tidied
  changeset's name frees once its directory goes. Mitigation: `.landed/` is part of the check, and the
  residual case — a tidied id resurrected years later — reads as a new changeset with the old name, which
  is a cosmetic loss. Stated rather than papered over.
- **An abandoned-then-landed changeset can be re-offered from a surviving old branch.** `terminalRecord`
  (`internal/cli/change.go:541-556`) consults the archive ref precisely because it cannot have moved.
  Mitigation: the landing write gate refuses markers for any landed changeset regardless of branch state,
  which covers the common case; the residue is a change of behaviour, tested and documented.
- **The replays are where this plan actually gets difficult.** `e2e-29.sh` carries 43 durable-layer
  assertions and `ci-integrate.sh` is built around recording. Mitigation: they are contracts, rewritten per
  milestone with the behaviour, and `mise run gates` per milestone means a milestone that cannot make them
  green is not finished.
- **`--fetch` disappearing breaks an agent habit.** CI that passes `--fetch` to `queue` or `check` gets a
  usage error. Mitigation: the flag removal is its own commit with the README rows, and CI in this
  repository is the only known caller.
- **Repositories already holding the refs.** 22 archive and 22 integration refs in this repository alone.
  They become inert, which is safe, and M2's inconsistent-refs test is what proves it. Deleting them is a
  human act with `git update-ref -d`; git-pair will not, and PRD should say so once rather than let people
  guess.

## Verification strategy

`mise run check` (gofmt, vet, the sharded suite) gates every commit, and `mise run gates` gates every
milestone — the whole ladder, including the three replays, so no milestone ships with a contract loosened.

Layering follows where a fact is observable. The two predicates and `BaseFor` are unit-tested in
`internal/changeset` against `gittest` fixtures that plant directories by path. Landing, the chain read, the
unreviewed finding, and tidy are command behaviours and are tested through `internal/cli` on fixtures, one
assertion per landing shape rather than one per command — the landing shape is the variable that decides
whether the answer exists. Contract deletions ride the contract tests that pinned them
(`internal/cli/contract_test.go`, `internal/cli/docs_contract_test.go`), so a removed field cannot survive
its own milestone. The full human-and-agent loop stays with `pty-walkthrough.sh` and the PRD §29 replay, and
the merge the declaration asks for stays with `ci-integrate.sh`.

Every assertion added here fails alone. For each landing shape, removing that shape's rule turns its
assertion red; for the two predicates, the `.landed` exclusion is proven by a fixture that would otherwise
report `.landed` as a changeset; for `BaseFor`, each rule is proven by removing just it. An assertion that
passes for two reasons is not evidence, and this plan deletes the code that most of the current evidence
was checking.

## Audit History

| Date | Audit | Summary |
| ---- | ----- | ------- |
