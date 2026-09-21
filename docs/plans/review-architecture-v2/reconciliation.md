# Reconciliation: "Review Architecture Requirements" (draft) vs. the current implementation

Status: **analysis / context capture**. Not a plan. Nothing here is decided.
The draft requirements it reconciles were supplied by the project owner in chat on 2026-09-21 and
are not yet in `PRD.md`.

Compared against: `PRD.md` (2693 lines), `README.md` (1328 lines), and the code as of
`6c5d512` on `docs/marks-are-remembered-locally`.

---

## 0. Agreed direction

Settled by the owner on 2026-09-21, narrowing the draft to the **local workflow** with the remote/CI
workflow kept as a door rather than a feature.

### 0.1 Scope: the local loop

Six entry points, and only these:

| Step | Command | Today |
| --- | --- | --- |
| initialize a changeset | `git pair init` | `change init` |
| publish it for review | `git pair change ready` | unchanged |
| review it | `review open` / `about` / `thread` / `submit` | unchanged |
| wait for review | `git pair change wait` | unchanged; `--fetch` is the remote door |
| iterate on review | `change feedback` → fix → `change ready` | unchanged |
| land it on the destination branch | the agent merges with ordinary git, then runs `git pair integration record` | **no new verb** — the contract is agent instructions (§0.8) |

There is **no `land` command.** git-pair performs no merge, squash or rebase, so the posture in PRD §26 and the hygiene test is untouched, no working tree is entered, and no branch pointer git-pair does not own is moved. Landing is ordinary git performed by the agent, followed by one git-pair command that writes the record.

Also in the loop and unchanged in role: `status`, `diff`, `check`, `queue`. **`change archive` is
deleted as a command** — its ref move becomes part of landing, and its checks (surviving additions,
drift, the freeze refusal, squash-safety reporting) move to `check`/`status`. `change unready` and
`change abandon` survive as marker-only commands.

### 0.2 Two refs, both created at landing, both frozen

| Ref | Points at | Written by | Moved | Deleted |
| --- | --- | --- | --- | --- |
| `refs/git-pair/integrations/<changeset-id>` | the commit that introduced the changeset into the destination branch | `integration record` | never | never |
| `refs/git-pair/archive/<changeset-id>` | the real unsquashed, unrebased tip — implementation and review interleaved | the same invocation | never | never |

**One command writes both, and it already takes both.** `integration record`'s existing flags are exactly the two facts: `--source` is the value the archive ref holds, `--commit` is the value the integration ref holds. The current implementation discovers the changeset from the archive ref that points at `--source` (D9) and writes one ref; under this model it verifies the pair and writes two. The argument shape predicted the design, which is why no new verb is needed and why the local flow and the CI door are the same command.

Create-only, both, in one transaction (`CreateRefIfAbsent` for each). What that buys:

- **No moving ref exists anywhere in git-pair.** The five pre-integration write sites
  (`change.go:547/675/785/990`, `reviewops.go:67`) disappear, and `reviewref.Update` plus the §13.3
  freeze machinery collapse into "create both or fail".
- **No force-push is ever needed.** Both refs are append-only by construction, so publishing them is a
  refspec, not a protocol, and there is no `+` in any configuration — which is also what makes the
  remote door cheap (§0.5).
- **The record is complete at the moment the fact is created** — in the local flow, because the same agent that performed the merge runs the command while both SHAs are in its context. Note this is now a *prose* guarantee rather than a transactional one; §0.8 is what makes it enforceable.
- Pre-integration, only the branch holds the chain. The accepted loss window is **"branch deleted before `integration record` runs"**, which with a human-or-agent-performed merge has a second form: **merge done, record never written**. §0.8 covers detection for that case.

### 0.3 Why two refs beat one

The previous exchange landed on a single integration ref naming the landing commit, with the reviewed
lineage left to per-review anchors. Two refs is better, because the archive ref is written while the
branch is still present, so git-pair's own write is what makes the chain permanently reachable. That
removes the durability transfer that the one-ref design depended on — the trail no longer survives only
if some other clone's anchors were published.

It also restores the draft's invariant 12 almost verbatim, with the pair rather than one ref doing the
anchoring, and it means the archive ref keeps the *full* chain including the post-approval metadata
tail that the drift gate allows and that review anchors would have missed.

### 0.4 What "only two refs" costs the draft

The draft's `refs/git-pair/reviews/<review-id>` namespace — its "Immutable Review Anchors" section and
invariants 6 and 7 — is **dropped to Deferred**.

What still holds without it:

- **Invariant 8, rebase invalidates approval.** A review commit records the head it spoke about
  (`Review-Head`, D4). After a rebase the rewritten review commit still carries the *original*
  `Review-Head` value in its message, and that commit is no longer an ancestor of `HEAD`, so the check
  "`Review-Head` is an ancestor of HEAD" fails and the approval is refused. No anchors required.
- Everything branch-derived, which is most of the design.

What is lost, and should be written into Deferred rather than left implicit:

- **The original review commit becomes reflog-only after a rebase**, so "inspect the review exactly as
  it was submitted" and the draft's "stable comparison point for detecting rewritten or altered review
  history" both go away until per-review anchors come back. Git keeps it alive ~90 days by default,
  which is enough for a local pair and not enough for a memory system — a fair trade now, and the
  reason the namespace shape should be reserved in the requirements.

### 0.5 The remote / CI door, explicitly deferred

The posture does not change and the hygiene test stays exactly as it is (no `push`, `merge`, `rebase`,
`reset`):

- Publishing both refs is one refspec over `refs/git-pair/*`, append-only, `+`-free.
- `integration record --source --commit [--target]` survives as the *remote* form of the landing
  record: it writes the two refs and performs no merge, which is what a forge pipeline needs.
- `change wait --fetch` stays ordinary `git fetch`; the moment it needs the namespace it must pass the
  refspec (`git.Fetch`, `git.go:514`, takes none today).
- Per-review anchor publication, server-side namespace protection, and protected-remote enforcement all
  stay in Deferred.

### 0.6 Decisions from earlier in the review that still stand

- **READY is an explicit marker the author writes** (`Review-State: ready`, `change ready`/`unready`),
  not a derived condition. The surviving-review-additions gate stays where it is, at `ready`.
- **Nothing preserves the chain before landing.** Accepted, and narrower than it sounds: the branch is
  the collaboration surface, and the loss window is deleting it before landing (§0.2).
- **PRD §4's trunk-tree rule stays.** Recognition of active work stays tree-based, the integration ref is
  the skip signal, and landed `changesets/<id>/` directories live in the destination branch permanently —
  which is also what makes landing a visible transition for §0.7's rule.

### 0.7 Determinism, verification, and idempotency

With no `land` command, `integration record` is the single write point, and an agent supplies (or has
derived) both SHAs. Three properties it needs to have:

**Derivation, so the agent does not have to be clever.** Locally the two values are recoverable, so both
flags should become optional with the current behaviour as the explicit escape hatch for CI:

- **The archive tip** is the changeset branch's `HEAD`, and the drift gate already guarantees it is the
  approved state modulo git-pair metadata: `ReconcileStaleness` refuses drift outside
  `changesets/<slug>/` (`lifecycle.go:107-131`). Where the branch is gone, the newest `Review-Head` in
  the lineage (D4) is the reviewed head.
- **The integration tip** is the newest commit on the destination branch's first-parent line whose tree
  adds `changesets/<id>/` and whose first parent's tree does not. Verified in a scratch repo against both
  a `--no-ff` merge and a squash — the rule named the merge commit and the squash commit respectively. It
  is PRD §4's tree rule read backwards, which is why keeping the tree rule and keeping landed
  `changesets/<id>/` in the destination branch are load-bearing rather than cosmetic.
- Explicit `--source`/`--commit` stay required-by-usage in CI, where the checkout has neither branch; the
  command's current "takes a position rather than a checkout" framing survives as the CI mode.

**Verification, because an agent can supply two plausible SHAs that are wrong.** The record is the paper
trail, so the command should refuse rather than trust:

1. `--source` carries a changeset directory for the id, or its lineage carries `Review-Changeset` for it
   (§0.7's content-based discovery — both readings must agree).
2. `--source`'s lineage contains an approving review marker; recording an unreviewed or blocked head as
   integrated is the failure the whole protocol exists to prevent.
3. `--commit` is reachable from the destination branch (`--target`, which today is optional and should
   stop being so wherever the branch is available).
4. `--commit`'s tree adds `changesets/<id>/` over its first parent — the derivation above used as a
   consistency check, which catches "you recorded the wrong merge" and "you pointed at the branch tip
   instead of the landing commit".

**Idempotency and ordering, because agents retry.** Two `update-ref` calls cannot be atomic, so:

- write the **archive ref first**, then the integration ref. The integration ref is the one whose
  existence means "this changeset is finished" (it drives `status`, `queue` and `check`), so a crash
  between the two leaves a changeset that reads as recorded-but-not-finished, and a retry finishes it.
  The reverse order leaves a finished changeset whose chain nothing anchors.
- "ref already exists pointing at exactly this SHA" is a **no-op**, not an error. Today
  `CreateIntegration` (`reviewref.go:195-202`) refuses any re-create, which makes a re-run after a partial
  failure unrecoverable without deleting a ref — and deleting is not something this design does.
  Create-only still means *never moved*: same target is fine, different target is the refusal.

### 0.8 The agent contract is now a product surface

Because the merge is performed outside git-pair, the sequence

```
git pair check                       # the gate: approved, no drift, nothing unaddressed
<ordinary git merge or squash into the base branch, by the agent, per human instruction>
git pair integration record          # writes both refs; derives both tips where it can
```

is the integration contract, and it lives in prose the agent follows. Two consequences the requirements
have to accept:

1. **It must exist in this repository.** `dcasper/git-pair` has no `skills/` directory; the loop is
   currently spec'd only as PRD §29 and README prose. A sibling repository (`bitandpixel/pi-git-pair`) carries a `skills/git-pair/SKILL.md` that already describes a namespace
   this codebase never used (`refs/reviews/archive/<slug>/<sha>`) and a command that no longer exists
   (`review close`) — which is evidence both that agents need this contract written down and how fast it
   drifts. Treat the agent instructions as a documented surface with the same change discipline as the
   CLI help, and keep the canonical wording in the requirements with the skill pointing at it.
2. **A skipped record step must be detectable, or the contract is unenforced.** Today the tree rule makes
   a landed changeset *silently disappear* from `status` and `queue`: the directory is in the destination
   branch, so it is landed work, and no integration ref exists to say it was recorded. That is exactly the
   state in which the paper trail is lost. New derived condition — **directory present in the destination
   branch, no integration ref ⇒ "landed, unrecorded"**, reported by `status` and by `queue` as its own
   heading, with the `integration record` invocation printed. It is the one requirement added by walking
   back `land`, and the cheapest thing in this design that protects the durable-memory goal.

## 1. The one architectural inversion

Everything else in the draft is compatible with the current design or additive. This is the change
that has to be settled first, because it moves five commands, one gate, and two namespaces:

| | Today | Draft |
| --- | --- | --- |
| What holds review history during work | `refs/git-pair/changesets/<id>/archive`, **moved on every lifecycle step** | nothing; "there is no continuously updated remote changeset ref" |
| What holds it permanently | same archive ref, plus `…/integration` naming the landing commit | two create-only refs written at landing: `refs/git-pair/integrations/<id>` (the landing commit) and `refs/git-pair/archive/<id>` (the unsquashed chain) — §0.2 |
| Per-review durability | none (the review is a commit; its SHA is its only name) | `refs/git-pair/reviews/<review-id>`, created once, never moved |
| Who can be wrong about state | the branch (refs are "never a source of state", PRD §12) | the branch, explicitly — and the draft strengthens that |

So the draft does **not** simply delete the archive ref — it retires the *moving* part of it, and the
agreed model keeps the name. The draft's own text is ambiguous about which end the durable ref names
(its diagram points at `D`, the historical tip; its field list wants both ends), and §0.2 resolves that
by writing **one ref per end**, both at landing, both frozen. Nothing moves; the record is complete the
moment the fact exists. See D10.

What genuinely disappears either way: a ref that moves *while work is active*. Five write sites.

## 2. Disposition map

| Draft concept | Current implementation | Action |
| --- | --- | --- |
| Active branch is the review surface | already true: state derives from `Review-*` markers on the branch (`lifecycle.Summarize`), refs are read for reporting | keep |
| Changeset identity independent of branch | already true: `changesets/<id>/` dir name is the id; `change init --id` overrides the branch-derived default; `ErrIDMismatch` guards it | keep |
| Interleaved history, one history | already true: markers are commits on the branch; `KindImplementation` is the absence of a marker | keep |
| Review commits + artifacts in `changesets/<id>/` | already true: `ABOUT.md`, `CHANGESET.yaml`, `*.md` threads; `git log/show/diff`, `rg` all work | keep |
| Implementation state as a derived view | **partly implemented, under two different definitions** (D6) | formalize |
| Immutable review anchors `refs/git-pair/reviews/<id>` | absent | **new** |
| Rebase invalidates approval | **contradicted by current behavior** (D3) | reversal |
| Integration record with 7 metadata fields | ref stores "an object id and nothing else" (PRD §13.2) by explicit decision | **rework** (D10) |
| Queue is branch-oriented, no collapsing by id | queue **collapses by slug** (`review.go:441-486`) | rework (D7) |
| `git pair init`, `git pair queue` | `git pair change init`, `git pair review queue` | rename? (D13) |
| Stacked: structured `parent: {branch, changeset}` + invalidation rules | single `base:` field; **no parent-movement invalidation exists** | **largest new feature** (D11) |
| Agent memory / historical search | satisfied by construction (files + commits); no index | keep, document |
| Cryptography deferred | already absent | keep |
| No distributed mutable state / no queue registry | already true | keep |

## 3. Major discrepancies

### D1. The archive ref is load-bearing in more places than PRD §12 admits

PRD §12 says the durable ref is "a fallback for reading history, never a source of state". The code
does not fully honor that, and the draft's removal therefore breaks more than reporting:

- `internal/changeset/resolve.go:451` (`reviewTips`) → `candidateFor` (`:388-401`) uses the archive
  tip to compute `Distance` (ordering when a revision carries more than one changeset directory) and
  `Terminal` via `refIsTerminal` (`:485`, reading `Review-State: abandoned` off the commit the ref
  names). That is state derived from the ref, on the path that answers "which changeset is this?".
- `check`'s central condition is `archive == HEAD` (`check.go:113`, `ArchiveCurrent` at `:151`,
  `integrationReasons` ordering at `:196-207`). The JSON fields `head`, `archive`,
  `archive_current` are the documented CI contract (README, "Gates").
- `change archive`'s forward-only rule (`change.go:968-988`) and `terminalRecord` (`:823`),
  `status`'s archive line (`status.go:163`, `:203`), `classifyOrphan` (`review.go:554`) and the
  no-branch fallback (`root.go:258`) all resolve it locally.
- Write sites: `change.go:547` (ready), `:675` (unready), `:785` (abandon), `:990` (archive),
  `reviewops.go:67` (submit). `reviewref.Update` is deliberately the single write path so the §13.3
  freeze cannot be forgotten.

**Decision needed:** delete, or rename/freeze into `refs/git-pair/integrations/<id>` (§1). If the
replacement for `check`'s condition is "the newest marker is an approval whose recorded head is an
ancestor of HEAD" (D3/D4), the gate gets stronger; but `--json` shape changes and every consumer
documented in README breaks.

**Closed by §0.2:** `refs/git-pair/integrations/<id>` names the landing commit and `refs/git-pair/archive/<id>` names the unsquashed tip, both create-only, both written by the landing operation. No ref moves anywhere in the design. Still open: `check`'s replacement condition and
the `archive`/`archive_current` JSON contract (§5.4), and the two state reads `resolve.go` makes off the
ref (`Distance` ordering at `:388-399`, `Terminal` at `:485`), which have no successor and must move
onto the branch.
### D2. Reachability hole: who holds the chain before integration?

A ref pointing at review commit `R2` keeps `A B C R1 R2` reachable, because reachability is
transitive. So per-review anchors cover the draft's own diagram up to the last review. What nothing
covers under the draft:

1. implementation commits **after** the last review (`D` in `A B R1 C R2 D`);
2. a changeset offered but **never reviewed** — no review commit, hence no anchor;
3. an **abandoned** changeset, whose branch gets deleted;
4. anything whose branch is deleted without integration.

PRD §13.1 named exactly this and wrote the archive on `change ready` for it: "the history worth
saving begins at the first handoff rather than at the first response: a changeset that was offered
and never reviewed, or whose branch exists only in the reflog, otherwise has nothing holding its
marker alive."

**Decided: accept the loss** (decision 3). Cases 2, 3 and 4 are unrecoverable by design, and case 1 is
recoverable only while the branch exists. Three places this has to be stated rather than discovered:

- PRD §13.1's never-reviewed / reflog-only scenario becomes a **known limitation**: keep the branch
  until the integration ref exists. Deserves a sentence in README's operational material.
- `classifyOrphan` (`review.go:546-580`) and the no-branch fallback (`root.go:250-272`) lose the data
  they read today. Both must degrade to an honest "no record" rather than the current "never anchored
  means never offered, so the directory is a leftover": under the new model a directory with no branch
  and no integration ref is *either* a leftover or lost work, and git-pair can no longer tell which.
  Reporting it as a leftover would be a false statement about deleted work.
- `refIsTerminal` (`resolve.go:485`) and the anchor read in `terminalRecord` (`change.go:823`) go
  away, so "was this abandoned?" becomes branch-local knowledge. See D12.

**Resolved for the post-landing case by §0.2:** the archive ref names the real tip, so the post-approval tail the drift gate allows is preserved. Case 1 stays open only *before* landing, which is the accepted window (§0.6).

### D3. Rebase behavior inverts

Draft invariant 8: rebase after approval invalidates the approval, and git-pair "must not silently
reinterpret an earlier approval as approval of rewritten history".

Current behavior is the opposite, deliberately:

- state is derived by walking markers reachable from HEAD, so a rebased approval marker is still an
  approval marker;
- the drift check compares **trees**, not lineages — `SummarizeAgainstTreeHEAD`
  (`lifecycle.go:169-180`, `ReconcileStaleness`) reports drift only for paths outside
  `changesets/<slug>/`, so a tree-preserving rebase drifts nothing;
- `change archive` explicitly permits following a rewrite: "A rebase is not this: rewritten history
  is neither ahead nor behind, and its markers moved with it" (`change.go:977-980`);
- the TUI already tolerates commits that "were force-pushed away" (`tui/session.go:242`).

Net effect today: rebase, run `change archive` again, `check` goes green, and the approval survives
silently. That is precisely what invariant 8 forbids. The good news: immutable anchors are the
mechanism that makes invariant 8 implementable, so D1/D3 want deciding together.

**Closed by §0.4:** invariant 8 becomes implementable without any new ref, through the `Review-Head`
trailer plus one ancestry test. What must be written into the requirements is the loss §0.4 names — the
pre-rebase review commit is reflog-only — and the current tolerant comments (`change.go:977-980`,
`tui/session.go:242`) have to be reversed, not just left unused.
### D4. Nothing records *what* was approved

The reviewed head is implicit — the review commit's first parent. That is the exact value a rebase
changes, so invariants 8 and 10 and the draft's "stable comparison point for detecting rewritten or
altered review history" all need it recorded explicitly. Today's trailers are `Review-Outcome`,
`Review-State`, `Review-Changeset` (`model.go:77-80`).

**Needed:** a reviewed-head field (e.g. `Review-Head: <sha>`), parsed by `lifecycle.parseEvent`
(`:198-230`), reported by `status`/`check`/`review history`. This is also the natural join key from
`refs/git-pair/reviews/<id>` back to the branch, and the seed for deferred patch-equivalence.

**Required by §0.4.** `Review-Head` is now the mechanism behind invariant 8, so it is not optional and
it is the first piece of the plan that changes the review protocol (milestone 2).
### D5. Review ids: allocating `refs/git-pair/reviews/<review-id>` is a distributed-state problem

The draft's example is `refs/git-pair/reviews/123`, which implies a counter. A counter is a
read-modify-write across clones; two clones allocating `123` for different commits is the
last-pusher-wins hazard, and `CreateRefIfAbsent` (`reviewref.go:195`) only makes it *loud*, not
correct. This is the one place the draft reintroduces the coordination it otherwise bans.

**Now load-bearing under §0:** the changeset id has to be a *path component* of the anchor —
`refs/git-pair/reviews/<changeset-id>/<sha>` — so the reviewed lineage is findable from the changeset
alone (`for-each-ref refs/git-pair/reviews/<id>/`) with no index and no metadata blob. A flat
`refs/git-pair/reviews/<n>` would strand the lineage behind a lookup only the integration record
could answer.

**Recommend:** derive the id from content — `refs/git-pair/reviews/<changeset-id>/<short-sha>` or
`…/<full-sha>` — which is collision-free, idempotent, needs no allocation, and matches the
`review_commit` fields already in `change wait --json` and `review submit` output.

**Closed by §0.4:** the per-review namespace is dropped to Deferred, so the allocation problem leaves
with it. Keep the analysis for when anchors return — a counter is still the wrong answer.
### D6. "Git-pair-owned metadata paths" is currently defined two different ways

The draft's derived view is `base...HEAD` minus git-pair metadata. The code already does this twice,
inconsistently:

- drift (`lifecycle.go:93`, `:346`) excludes only `changesets/<slug>/` — the *own* changeset dir;
- survival (`survival.go:151`) excludes all of `changesets/`.

And a third category exists outside the tree entirely: markers live in commit messages, so a
metadata-only review commit that changes *only* the trailer set is invisible to tree comparison.

**Needed:** one definition of the metadata path set (all `changesets/**`? `CHANGESET.yaml` +
`ABOUT.md` + `*.md` threads only?), owned by the `changeset` package, used by drift, survival, spans,
and any future fingerprint. The draft defers canonicalization/hashing, which is fine, but not the
path set — invariant 10's conservative rule is currently written in terms of raw tips precisely to
avoid having to name it (see D11).

**Elevated by decision 4:** landed changeset directories now accumulate in trunk, so the metadata path
set is load-bearing for the derived view from day one. Deferred is the *fingerprint*, not the *set*.
### D7. The queue must stop collapsing by changeset id

Draft invariant 5 + "Git-pair should not automatically collapse those branches into one queue entry
solely because they share a changeset ID."

`runReviewQueue` (`review.go:419-486`) does the opposite: it keys a map by `cs.Slug`, appends every
branch carrying that slug (`:466`), then asks `readyEntry` over *all* of them (`:486`, `:609`) and
emits one row. So a changeset in two branches today yields one row with a merged `branches[]` and a
"best" state — a lossy choice between two branch-local states.

Changes: one row per branch; `--json` shape changes (`branches[]` disappears or becomes single-valued);
step 3 ("skip integrated") already exists via `ResolveIntegration` (`:478`).

### D8. The trunk-tree rule is not mentioned in the draft, and it is doing real work

PRD §4 ("Which changeset a revision is working on") decides activity by *tree comparison against
trunk*: a `changesets/<id>/` directory present here and absent in trunk is in-progress work; a
directory already in trunk is landed and drops out. `DirsAt`/`newResolver` (`resolve.go:198-212`)
implement it, and PRD §4 insists `status`, `review queue` and CI all ask it the same way. The draft's
queue step 2-3 instead says "determine whether each branch contains an active changeset" and "skip
branches whose changeset has already been integrated" — i.e. ref-based, with no tree rule.

These are not equivalent, and the difference is a regression risk: integration refs are **not fetched
by default** (`FetchRefspec`, `reviewref.go:52`; `git.Fetch` runs a bare `git fetch` —
`git.go:514-521`). Drop the tree rule and a fresh clone of trunk reads every landed
`changesets/<id>/` as active, unreviewed work until someone fetches a namespace. Keep the tree rule
and CI still needs the `--default-branch`/trunk fetch that PRD §13.4 documents.

**Decided: keep the tree rule** (decision 4). Consequences to write into the requirements: PRD §4's
single-derivation rule survives, so the draft's queue steps 2-3 need rewording to
"tree-recognition, then skip by integration ref"; CI keeps the default-branch fetch PRD §13.4
documents; `changesets/<id>/` directories live in trunk permanently once landed; and the integration
ref becomes a skip signal *beside* the tree rule rather than a replacement for it — the one place
"one derivation everywhere" now needs an explicit carve-out (tree decides activity, ref decides that
activity is over).

### D9. Integration discovery breaks with the archive ref

**Closed by decisions 1 and 3** — and it has a better answer than the ref it replaces.

`integration record` discovers the changeset **by scanning archive refs for an exact SHA match**
(`reviewref.ArchivesAt` `:211-229`, used at `integration.go:100`, with the §24 "no refs at all vs no
archive" distinction at `:109`). Under decisions 1 and 3 nothing points at `--source` at record time,
so `ArchivesAt` is dead and cannot simply be re-pointed.

The replacement is content-derived, which is what the draft's own principles want:

1. **The tree rule already answers it.** `changeset.DirsAt(--source)` lists every `changesets/<id>/`
   at the reviewed commit, and the tree rule (kept by decision 4) subtracts the ones already in trunk.
   One id, deterministic, no ref needed — the same question `status` and the queue already ask the same
   way (PRD §4).
2. **The markers confirm it.** Review commits in `--source`'s lineage carry `Review-Changeset: <id>`
   (`model.go:79`, parsed at `lifecycle.go:206`), as do the ready/unready markers. A walk over
   `--source`'s trailers is an independent second reading of the same id.

Disagreement between the two is the case worth reporting (a directory renamed mid-flight, or a
`--source` from an unrelated line), and `--changeset` already exists as the disambiguator. The §24
failure taxonomy also changes shape: "no archive ref" vs "no namespace at all" both become "this
commit carries no changeset directory", which a wrong `--source` causes rather than a missed fetch.

**Closed twice over by §0.7:** content-based discovery is the mechanism, and for the local flow
`git pair land` does not need discovery at all — it knows both SHAs because it made one and is standing
on the other. Discovery matters only for `integration record`, the remote/CI door.
### D10. The integration record's payload contradicts a decision PRD §13.2 made on purpose

Draft metadata: `changeset-id`, `historical branch tip`, `reviewed implementation state`,
`integration target`, `integrated commit`, `integration method`, `terminal status`.

PRD §13.2 stores an object id *and nothing else*, and rejects naming the target branch
deliberately: "hanging a name on the ref by pointing it at an annotated tag object would put a peel
in front of every reader of the ref and break the exact-object matching §11.4's discovery depends
on". Also note the current split: archive → historical tip, integration → landing commit. The
draft's ASCII diagram shows `refs/git-pair/integrations/<id>` pointing at **D, the historical tip**,
while its field list wants both the historical tip *and* the integrated commit. Two readings:

- **(a) role swap** — `integrations/<id>` points at the historical tip (invariant 12: the ref
  "permanently anchors the completed implementation and review history"), landing commit + method +
  target become metadata. Preserves `--target` reachability (`integration.go:138`) and
  `describeLanding` (`:270`) only if metadata is machine-readable.
- **(b) current semantics kept**, and the historical chain preserved by review anchors instead —
  which re-opens D2 (anchors stop at the last review).

**Closed by §0.2: neither reading, because both ends get a ref.** `refs/git-pair/integrations/<id>` keeps today's payload (the landing commit) and `integration record` keeps writing no commit; the historical end reading (a) wanted becomes `refs/git-pair/archive/<id>`. PRD §13.2's "an object id and nothing else" stands for both, and D9's content-based discovery is the only mechanism — which it already had to be.

That leaves the draft's seven-field record **optional rather than required**: changeset id, the
lineage and the landing are all recoverable from the two create-only ref families plus the tree rule.
What is genuinely not recoverable is `integration method`, and the *name* of the historical branch
once the branch is gone — both cheap to record at `integration record` time if anyone wants them, and
a better fit for `git pair status` output than for durable storage.

**Where the metadata should live**, if it is recorded at all. Given the draft's own principles
(filesystem artifacts, grep-friendly, agent-readable, no opaque databases), the coherent answer is a
**metadata commit** whose tree holds `INTEGRATION.yaml` and whose message carries trailers: no
peel, greppable, durable, and it makes the whole record human-readable. Note the costs:
`integration record` currently "writes no commit" and works from any branch precisely so it can run
in CI (`integration.go` long description) — a metadata commit needs a parent (landing commit?
historical tip? orphan?) and an identity; and `ForEachRef` reads only `%(objectname)`
(`git.go:561`), so any annotated-tag option needs peel-aware plumbing before it can be compared to a
commit SHA. Also: `reviewed implementation state` cannot be a fingerprint (canonicalization is
deferred), so it must be stored as "the reviewed head commit" and labelled as such.

**Closed by §0.2:** both refs are object ids and nothing else, so PRD §13.2 stands unchanged and the
metadata-home question never arises. The draft's seven-field record becomes derived `status` output;
the two fields that are genuinely unrecoverable (`integration method`, and the historical branch name
once the branch is gone) can be stored when someone asks for them.
### D11. Stacked changesets are the largest new feature, and the conservative rule has a product cost

Current stack support is one field: `base:` in `CHANGESET.yaml`, whose `parentID` parser accepts an
id, `refs/heads/<x>`, or `refs/git-pair/changesets/<x>/archive` (`resolve.go:500-512` — the last
spelling is deleted by this draft). There is **no** parent-movement approval invalidation anywhere, no
child-vs-parent comparison, and no abandoned-parent handling ("must not silently reparent" is new
behavior; today nothing reparents, but nothing refuses either).

New work: structured `parent: {branch, changeset}` (schema change + migration for the deleted
archive-ref spelling), recording the parent tip a child was approved against (another trailer, cf.
D4), an invalidation check in `status`/`queue`/`check`, and an explicit "needs reconciliation" state
for orphaned-by-abandonment children.

Flag the interaction: the draft says stacked validity must not be raw-tip equality because review
commits move the parent tip, then defers the implementation-state relaxation, and sets the initial
rule as "any change to the parent branch after a child is approved invalidates the child's approval".
In the workflow this tool creates, *every parent review* moves the parent tip. So a stacked child
cannot remain approved while its parent is under review at all. That may be acceptable as a v1 rule,
but it should be a stated, deliberate consequence rather than an accident — it effectively means
"rebase and re-approve the child only after the parent has settled".

**Hardening checklist implied by §0.1 and §0.2.** Local-first makes most of this cheaper than the
draft assumes, because the parent and child are usually in the same clone:

1. **Structured `parent: {branch, changeset}`** in `CHANGESET.yaml`, replacing the single `base:`
   spelling. `parentID` (`resolve.go:500-512`) currently accepts `refs/git-pair/changesets/<x>/archive`,
   a namespace this design deletes — so the parser and its tests change here.
2. **Record the parent tip the child was approved against**, as a trailer beside `Review-Head`, and
   compare it in `status`/`queue`/`check`. Today there is no parent comparison at all.
3. **Name the invalidation instead of just reporting it.** Invariant 10 trips on *every* parent review
   commit, because review commits move the parent tip — so the reason has to distinguish "parent
   implementation moved" from "parent review conversation moved" from "parent rebased" from "parent
   integrated", even though all four invalidate. Without that, the rule reads as arbitrary and pushes
   users toward ignoring `check`.
4. **Parent integration resolution:** the child reads the parent's `integrations/<parent>` for where the
   work landed and `archive/<parent>` for the chain it is standing on, and `status` says plainly
   "parent integrated as X; rebase onto <destination>" rather than today's silent base failure.
5. **Abandoned parent:** the child's parent branch is gone with no integration ref, so `check` refuses
   with "unreconciled: choose a new base" and `init`/`use` accept an explicit re-parent. Never
   reparent silently (draft requirement) — and under this design nothing *can* reparent silently,
   because there is no ref to quietly follow.
6. **A stacked child's archive ref is a superset.** If the child is landed without being rebased onto
   trunk first, its archive chain contains the parent's unsquashed history too. Expected, harmless, and
   worth one sentence in the requirements so nobody files it as duplication — and it is the reason a
   child landed first preserves the parent's chain even if nobody ever landed the parent.
### D12. `change archive` loses its subject; `unready` and `abandon` lose their durable effect

PRD §9.5/§9.6/§9.7 are written as ref moves. With no active ref:

**Decided (decisions 1 and 3):** unready/abandon become marker-only, and `change archive` is deleted as
a command. Its checks (surviving additions, drift, the freeze refusal, squash-safety reporting) move to
`check`/`status`, which already share the same derivation.

- `change archive` (README: "advances the archive ref onto the reviewed HEAD and reports
  squash-safety") has no ref to advance. Its *checks* still matter: the surviving-review-additions
  gate (`survival`), the drift gate (`ReconcileStaleness`), the integrated-freeze refusal
  (`reviewref.RefuseIntegrated`), and squash-safety reporting. These need a home — most plausibly
  `check`/`status`, which already share the same derivation.
- `change unready` moves the ref "for the same reason read the other way" (PRD §13.1), and
  `change abandon`'s ref move is described in code as "the point of the whole operation"
  (`change.go:783-787`). Marker-only versions reintroduce the problem PRD §13.1 solved: after
  `git branch -D`, nothing says the offer was withdrawn or the work ended. Today `refIsTerminal`
  answers that from the ref (`resolve.go:485`).

Decision couples to D2.

### D13. The draft never names the lifecycle commands, and `ready` is ambiguous

Named in the draft: `git pair init`, `git pair queue`, and the two ref namespaces. Not mentioned at
all: `change ready`, `change unready`, `change abandon`, `change feedback`, `change wait`, `change
use`, `change archive`, `review open|about|thread|submit|history`, `status`, `diff`, `check`,
`integration record` — all of which exist and are spec'd (PRD §9-§11), and invariants 8 and 10 are
*about* approval validity, which is lifecycle.

The blocking ambiguity is `ready`. Draft queue step 6 says "include branches that are ready for
review", which reads either as:

- **(a) still a marker the author writes** (`Review-State: ready`, `change ready`/`unready` survive)
  — minimal change; or
- **(b) derived** (branch carries a changeset with no unresolved threads/blocking review) — deletes
  `change ready`, `change unready`, the READY state, the surviving-additions gate placement at
  `change.go` ready, and half the state machine, and makes "ready" mean something different in every
  clone.

**Decided: (a), the explicit marker.** `change ready`/`change unready` survive as marker-only commands
and the surviving-additions gate stays where it is. Open in D13 is therefore only the naming surface
below, plus where `change archive`'s checks land (D12).

Naming too: `git pair init` vs `git pair change init`, `git pair queue` vs `git pair review queue`
(PRD §10.6). Renames break the documented surface and the tests around it; worth doing once, on
purpose, if at all.

### D14. "A changeset ID alone must not identify the active surface" breaks id-addressed commands

Several surfaces address a changeset by id alone: `status --changeset`, `change use <id>`,
`integration record --changeset`, and the branch-less fallback at `root.go:258` which resolves a slug
by falling back to the durable ref. If a changeset can legitimately live on two branches with two
states, these need a branch disambiguation rule (or a refusal that names the candidates). The
fallback's ref target also changes namespace, and must stop implying "the" archive.

### D15. Namespace, plumbing and docs blast radius

- `reviewref.root` (`:33`) `refs/git-pair/changesets` → `refs/git-pair/reviews/` +
  `refs/git-pair/integrations/`; `FetchRefspec`/`FetchCommand` (`:52`, `:55`) become one or two
  refspecs; `Present` (`:70`) and `Taken` (`:84`) must scan the new roots — `Taken` is what stops
  `change init` reusing a name with history, and its "any child counts" argument no longer applies
  to a flat `integrations/<id>`.
- `ChangesetID` (`:106-125`) rejects a child containing `/` and expects exactly `<id>/<child>`; that
  parser is gone. Bonus: the "a ref cannot be both a leaf and a namespace" rule (PRD §13.1,
  `reviewref.go:15`) was the only reason refs were nested under `<id>/`; a flat
  `integrations/<id>` leaf is legal, so that whole paragraph of PRD §13.1 becomes obsolete.
- Plumbing gaps that any new layout must close: `git.Fetch` (`git.go:514`) takes no refspec, so
  `change wait --fetch` never sees published refs; `ForEachRef` (`git.go:561`) reads `%(objectname)`
  only, so nothing can peel a tag.
- Docs: 95 `archive` mentions in `PRD.md`, 90 in `README.md`. Sections to rewrite: §2.3, §3
  ("Review archive"), §4, §9.5-§9.7, §10.6, §11.3, §11.4, §12, all of §13, §16, §24, §25-§27.
  README's Quickstart transcript literally prints
  `ref: refs/git-pair/changesets/booking-transaction/archive -> 332887c`, plus the CI fetch block.
- Code surface (lines per file, prod+test, touching `reviewref.`): `change.go` 1526, `review.go` 800,
  `tui/session.go` 670, `resolve.go` 575, `root.go` 411, `integration.go` 363, `status.go` 341,
  `check.go` 283, `marker.go` 164, `reviewops.go` 84, plus `reviewref` and `gittest` fixtures.
  The TUI's ref/anchor assumptions are the least obvious risk.
- The hygiene test (`internal/hygiene/hygiene_test.go`) is **unaffected and stays right**: the draft's
  "remote collaboration via ordinary branch transport" is the same posture as forbidding
  `push`/`merge`/`rebase`/`reset`.

**Amended by §0.2:** the new roots are `refs/git-pair/integrations/<id>` and
`refs/git-pair/archive/<id>`, both flat leaves, so `ChangesetID`'s `<id>/<child>` parser and the whole
"a ref cannot be both a leaf and a namespace" argument in PRD §13.1 and `reviewref.go:15` are deleted
rather than rewritten — one of the few places this design removes complexity instead of moving it.
### D16. Additions that are cheap but need deciding

- **Reviewer identity as an artifact field.** Draft lists `reviewer` among review-commit contents;
  today identity is git author metadata only (`lifecycle.Event.Author`), no trailer, and PRD §27 lists
  `--reviewer=` as future. Cheap to add, but decide the source (git `user.name`? a
  `git-pair.reviewer` config? a flag?) — the deferred signing work will want a stable identity string.
- **Resolution state.** New concept: no current machinery tracks whether a thread is resolved. Only
  outcome (`block|feedback|approve`) and surviving review additions (`survival`) exist. Decide where
  resolution is recorded (thread file front matter? a trailer?) and whether `check` gates on open
  threads — that is a gate that does not exist today and would change every team's `check` verdict.
- **Deferred list vs PRD §26/§27.** Aligned in substance; needs re-filing, and two items move in the
  *opposite* direction from current README "What it is not": queue `--all` over already-fetched
  remote-tracking branches, and per-review ref publication.

## 4. What carries over untouched

Branch-derived state, changeset identity as directory name, `changesets/<id>/` artifact layout,
ABOUT.md and thread formats, the TUI's review surface, spans (`--unreviewed`, review-relative diff),
`change wait --fetch`'s polling model, forge independence, the no-push posture, hygiene test,
editor/difftool delegation, and the whole "review history is grep-able engineering memory" premise —
which the current design already satisfies structurally.

## 5. Remaining gates

The two-ref / local-first / no-`land` model closed review-id allocation, metadata home, discovery,
`check`'s archive condition, pre-integration durability, and the merge-scope question. What still blocks
a plan:

1. **`check`'s replacement condition** — `archive == HEAD` has no referent, so the gate becomes "newest
   marker is an approval, its `Review-Head` is an ancestor of HEAD, no drift". Decides the `archive` /
   `archive_current` JSON fields, which README documents as the CI contract (D1, D9).
2. **How strict `integration record`'s verification is** — refuse, or warn, on each of §0.7's four checks.
   Refusing is right for an agent-supplied pair; the case for warning is a stack landed as one merge, where
   a later changeset's landing commit may not be the first-parent transition the rule expects.
3. **Argument derivation vs required flags** — how much `integration record` derives locally before it is
   guessing, and whether CI keeps requiring both explicitly (recommended: yes, and say so in the help
   text that currently claims the opposite about what the caller has).
4. **Stacked invalidation reporting** — invariant 10 trips on every parent review commit, so the reason has
   to name which kind of parent movement happened (D11's checklist is the scope).
5. **CLI renames** — `git pair init`, `git pair queue`, and whether the current `change init` /
   `review queue` spellings survive as aliases.
6. **Reviewer identity and resolution state** — both deferrable; `check` gating on open threads is the one
   with behavioural consequence (D16).
7. **Migration** — repositories holding `refs/git-pair/changesets/<id>/{archive,integration}` and `base:`
   values naming them, plus the `Taken` interaction (D14, D15).
8. **Per-branch queue rows** (invariant 5, D7) — orthogonal and still required.

## 6. Suggested sequencing

Each milestone is independently shippable and leaves the suite green.

1. **Ref substrate.** Split `reviewref` into the two create-only families under
   `refs/git-pair/integrations/` and `refs/git-pair/archive/`; delete `Update`, the freeze check, and the
   five pre-integration write sites (`change.go:547/675/785/990`, `reviewops.go:67`); make create-only
   tolerant of an identical re-create (§0.7). `Present`/`Taken`/`FetchRefspec` follow the new roots.
2. **`Review-Head`.** Record the reviewed head in review commits and make "is an ancestor of HEAD" a
   first-class condition in `lifecycle`, `status` and `check`. Highest requirements leverage per line
   changed: it is what makes invariant 8 real without a third ref namespace.
3. **`integration record` as the two-ref write point.** Create both refs, in §0.7's order, with §0.7's
   verification, then the optional derivations for the local flow. This milestone is where landing stops
   being a gap in the protocol.
4. **Unrecorded-landing detection** (§0.8.2) in `status` and `queue`, so a skipped record step is a
   reported state rather than a vanished changeset.
5. **Retire `change archive`.** Move its checks into `check`/`status`; fold its squash-safety reporting into
   `status` output. `change unready`/`abandon` become marker-only and their PRD text follows.
6. **Queue per branch** (invariant 5) with the `--json` shape change, plus the `git pair queue` naming if
   §5.5 survives.
7. **Stacked hardening** (D11): structured `parent`, recorded parent tip, named invalidation, parent
   integration resolution, abandoned-parent refusal.
8. **Agent contract and requirements rewrite.** The loop prose (PRD §29, README, and a `skills/git-pair/`
   surface if this repo adopts one) moves with this: the three-step integration contract, the deferred
   additions from §0.4, and PRD §2.3, §3, §9.5-9.7, §10.6, §11.3, §11.4, §12, all of §13, §16, §24-§27.
   Docs describing refs should move with milestone 1 rather than wait.

