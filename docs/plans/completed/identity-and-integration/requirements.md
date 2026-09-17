# git-pair Changeset Identity, Archival, Integration, and CI Requirements

> Supplied by the reviewer on 2026-09-17 as the requirements for the work planned in `../plan.md`.
> Reproduced here as the source of truth, with the source editor's fence labels removed. Where this
> document and the code disagreed at the time of writing, `plan.md` §Decisions records the resolution
> and this file was left unedited.

## 1. Scope

This document specifies three related parts of `git-pair`:

1. changeset ID overrides,
2. archival and integration refs,
3. CI-oriented validation and integration commands.

The goals are:

* preserve a durable identity for a changeset independently of branch naming,
* preserve the complete unsquashed author/reviewer history,
* unambiguously link that history to the commit that eventually lands in the target branch,
* support squash, rebase, merge-commit, cherry-pick, or other integration strategies,
* provide simple non-interactive commands suitable for CI,
* avoid forge-specific assumptions in the core `git-pair` protocol.

---

# 2. Changeset Identity

## 2.1 Branch name as default, not durable identity

A changeset normally derives its initial ID from the current branch name.

Example:

```text
branch:
feature/booking-transaction

default changeset id:
feature-booking-transaction
```

The exact branch-to-ID normalization algorithm must be deterministic.

However:

> The branch name is only the default source for an ID. It is not the durable identity of the changeset.

This matters because:

* branch names may collide,
* branches may be renamed,
* users may intentionally want a different changeset identity,
* integration tooling should not need to infer changeset identity from branch names.

---

# 3. Changeset ID Override

`git pair change init` must support explicitly selecting a changeset ID.

Example:

```bash
git pair change init \
  --id booking-transaction-v2 \
  --base main
```

The resulting changeset metadata:

```yaml
id: booking-transaction-v2
base: main
```

Directory:

```text
changesets/
  booking-transaction-v2/
    CHANGESET.yaml
    ABOUT.md
```

All durable Git Pair refs must use the changeset ID, not the branch name.

Example:

```text
refs/git-pair/changesets/booking-transaction-v2/archive
refs/git-pair/changesets/booking-transaction-v2/integration
```

---

# 4. Canonical Changeset ID

Once initialized, the metadata `id` is the canonical Git Pair identity for the changeset.

Conceptually:

```text
branch name
    ↓ default suggestion only

changeset ID
    ↓ canonical identity

changesets/<id>/
refs/git-pair/changesets/<id>/...
```

Other Git Pair features must resolve the changeset through this canonical ID once it exists.

---

# 5. ID Collision Handling

Changeset IDs must be unique within the repository's Git Pair namespace.

Before initializing a changeset, Git Pair must check for conflicts such as existing refs:

```text
refs/git-pair/changesets/<id>/archive
refs/git-pair/changesets/<id>/integration
```

and existing changeset metadata/directories where appropriate.

If the automatically derived ID collides, initialization must fail clearly.

Example:

```text
Changeset ID "feature-booking" is already in use.

Choose another ID:

  git pair change init --id feature-booking-2
```

Do not silently append random suffixes.

---

# 6. ID Mutability

The changeset ID should be treated as stable after the changeset begins producing durable Git Pair refs.

For MVP:

* before durable refs exist, manually changing the ID may be tolerated,
* once an archive ref exists, direct ID mutation should be rejected or treated as unsupported,
* a future explicit rename/migration command may safely move all related metadata and refs.

Do not attempt transparent ID migration in MVP.

---

# 7. No Root Active-Changeset Pointer

Do not use a committed repo-root pointer such as:

```text
.gitpair
```

to identify the current changeset.

Such a file creates unnecessary conflicts, particularly with:

* stacked branches,
* parallel changesets,
* rebases,
* merges.

Durable changeset discovery for integration should instead use Git Pair refs.

---

# 8. Changeset Ref Namespace

Each changeset owns a ref namespace:

```text
refs/git-pair/changesets/<id>/
```

For a completed/integrated changeset, the two primary refs are:

```text
refs/git-pair/changesets/<id>/archive
refs/git-pair/changesets/<id>/integration
```

Example:

```text
refs/git-pair/changesets/booking-transaction/archive
refs/git-pair/changesets/booking-transaction/integration
```

The hierarchy is intentional.

Do not attempt to simultaneously create:

```text
refs/git-pair/changesets/booking-transaction
```

and child refs beneath it, because Git refs cannot act as both a leaf and a namespace.

---

# 9. Archive Ref

## 9.1 Purpose

The archive ref preserves the complete unsquashed changeset history.

It includes:

* implementation commits,
* ready lifecycle commits,
* review commits,
* review feedback,
* agent responses,
* approval commits,
* close lifecycle commits where applicable.

Example:

```text
refs/git-pair/changesets/booking-transaction/archive
    ↓
A -- R1 -- B -- R2 -- C -- APPROVE
```

The archive ref keeps this entire history reachable even if the feature is later squash-merged.

---

# 10. Archive Ref Updates

Review submission must update the archive ref automatically to the resulting review commit.

Example:

```bash
git pair review submit --block
```

or:

```bash
git pair review submit --approve
```

must result in:

```text
refs/git-pair/changesets/<id>/archive → current review commit
```

This means approval commonly leaves the archive already current.

---

# 11. Archive Advancement After Review

An author may sometimes add non-implementation commits after a review, for example:

* responding in a review thread,
* updating `ABOUT.md`,
* making other review-artifact-only changes.

A command may be provided to safely advance the archive ref without inventing a new lifecycle state.

Recommended command:

```bash
git pair change archive
```

Expected semantics:

```text
if archive already points to HEAD:
    succeed idempotently

if only permitted post-review metadata/artifact changes occurred:
    advance archive to HEAD

if implementation changed in a way that invalidates approval:
    fail and require another review cycle
```

Exact validation rules may be refined separately.

---

# 12. Closed Changesets

`git pair change close` is a terminal operation for changesets that will **not** be integrated.

It must:

1. create a Git Pair lifecycle commit representing closure,
2. update the archive ref to include that closure commit,
3. make the changeset terminal as `CLOSED`,
4. remove it from normal actionable queues.

Example:

```text
WORKING
   ↓
git pair change close
   ↓
CLOSED
```

`CLOSED` means:

> Preserve the changeset's history, but stop treating it as active work. It is not intended for production integration.

A closed changeset must not pass integration readiness checks.

---

# 13. Integration Ref

## 13.1 Purpose

The integration ref records where a changeset actually landed in the integration/default branch.

Example:

```text
archive:
refs/git-pair/changesets/booking-transaction/archive
    → A

integration:
refs/git-pair/changesets/booking-transaction/integration
    → B
```

Where:

* `A` is the final unsquashed changeset/review stack,
* `B` is the resulting commit in the integrated target history.

---

# 14. Why Integration Must Be Explicit

Normal merge commits may preserve ancestry and therefore allow integration to be inferred.

Squash merges, rebases, and cherry-picks rewrite commit identity.

Do not attempt to determine permanent integration status primarily through:

* patch IDs,
* tree similarity,
* diff equivalence,
* commit-message heuristics.

These may be useful for diagnostics or recovery, but not as the canonical protocol.

Instead:

> Integration must be recorded explicitly.

---

# 15. Integration Strategy Independence

Git Pair must not care how archive commit `A` became integrated commit `B`.

Supported cases include:

```text
normal merge:
A remains reachable from target history

squash:
A → squash commit B

rebase:
A → rewritten commit B

cherry-pick:
A → cherry-picked commit B
```

The integration record makes all of these equivalent from Git Pair's perspective:

```text
changeset archive A
    ↓
integration link
    ↓
target commit B
```

---

# 16. Discovering the Changeset During Integration

The integration process will typically know:

```text
SOURCE_SHA
INTEGRATED_SHA
```

It must not need to know the changeset ID ahead of time.

Git Pair should derive the changeset ID from the source SHA by finding archive refs pointing at it.

Conceptually:

```bash
git for-each-ref \
  --points-at "$SOURCE_SHA" \
  --format='%(refname)' \
  refs/git-pair/changesets/
```

Expected match:

```text
refs/git-pair/changesets/booking-transaction/archive
```

From which Git Pair derives:

```text
changeset id = booking-transaction
```

---

# 17. Source SHA Discovery Invariant

For automatic integration discovery to work, the archive ref must point to the exact source SHA supplied by the integration process.

This creates an important invariant:

> The source SHA passed to integration recording must have exactly one corresponding Git Pair archive ref.

---

# 18. Zero Archive Matches

If no archive ref points at the source SHA:

```text
No Git Pair changeset archive points to source commit a81c123.

Integration cannot be recorded automatically.
```

The command must fail.

This may mean:

* archive refs were not fetched,
* the changeset was never properly archived,
* the wrong source SHA was supplied.

---

# 19. Multiple Archive Matches

Multiple changesets may technically point to the same SHA.

If more than one archive ref matches:

```text
Source commit a81c123 is referenced by multiple changesets:

  booking-transaction
  booking-transaction-v2

Specify the changeset explicitly.
```

The normal integration path must fail rather than guess.

Provide an escape hatch such as:

```bash
git pair integration record \
  --changeset booking-transaction \
  --source a81c123 \
  --commit d91c21e
```

---

# 20. `git pair integration record`

Provide a fully non-interactive, CI-friendly command:

```bash
git pair integration record \
  --source <source-sha> \
  --commit <integrated-sha>
```

Optional:

```bash
--target <ref>
```

Example:

```bash
git pair integration record \
  --source a7f3c98 \
  --commit d91c21e \
  --target origin/main
```

---

# 21. Integration Record Behavior

`git pair integration record` must:

1. validate `source` exists,
2. find archive refs pointing exactly at `source`,
3. require exactly one match unless `--changeset` is supplied,
4. derive the changeset ID,
5. reject a `CLOSED` changeset,
6. validate the integrated commit exists,
7. optionally verify the integrated commit is reachable from the supplied target ref,
8. ensure no conflicting integration ref already exists,
9. create:

```text
refs/git-pair/changesets/<id>/integration
    → <integrated-sha>
```

10. return success.

---

# 22. Integration Ref Immutability

Integration records should be treated as immutable by default.

If:

```text
archive A → integration B
```

has already been recorded, another invocation attempting:

```text
archive A → integration C
```

must fail loudly.

Example:

```text
Integration already recorded:

  booking-transaction
  source:      a7f3c98
  integrated:  d91c21e

Refusing to rewrite integration history.
```

A future explicit repair/admin command may exist for exceptional cases.

Do not silently mutate integration refs.

---

# 23. Archive Immutability After Integration

Before integration, the archive ref may advance as the changeset evolves.

After integration is recorded:

```text
refs/git-pair/changesets/<id>/archive
refs/git-pair/changesets/<id>/integration
```

should both be treated as logically frozen.

Git Pair commands must reject normal attempts to move the archive ref after integration.

This protects the permanent mapping:

```text
archive source A → integrated target B
```

---

# 24. Fetching Git Pair Refs in CI

CI environments may not fetch custom refs automatically.

Integration/check workflows must ensure Git Pair refs are available.

Conceptually:

```bash
git fetch origin \
  'refs/git-pair/changesets/*:refs/git-pair/changesets/*'
```

The exact fetch strategy may be optimized.

Git Pair CI commands should provide a clear error when custom refs are unavailable rather than incorrectly reporting state.

Example:

```text
No Git Pair changeset refs are available locally.

Fetch refs/git-pair/changesets/* before running this command.
```

---

# 25. Derived States

Relevant states include:

```text
WORKING
READY_FOR_REVIEW
BLOCKED
FEEDBACK
APPROVED
INTEGRATION_READY
INTEGRATED
CLOSED
```

`INTEGRATION_READY` is a **derived condition**, not necessarily a lifecycle commit.

`INTEGRATED` is derived from the presence of a valid integration ref.

`CLOSED` is a terminal lifecycle state created explicitly by:

```bash
git pair change close
```

---

# 26. Integration Readiness

A changeset is integration-ready when Git Pair's integration policy is satisfied.

At minimum this includes:

* changeset is not closed,
* latest relevant review outcome permits integration,
* review applies to the current source state,
* archive ref is current and points at the intended source SHA,
* no implementation change has invalidated that review,
* required review-addition validations have been resolved or explicitly accepted.

Exact policy may vary regarding `FEEDBACK`.

---

# 27. `git pair check`

Provide a bare CI-oriented validation command:

```bash
git pair check
```

Meaning:

> Assert that this changeset is currently integration-ready according to Git Pair policy.

This command is intended primarily for:

* required CI checks,
* pre-integration automation,
* merge gates,
* agent automation.

---

# 28. `git pair check` Exit Contract

Success:

```text
$ git pair check

OK: booking-transaction is integration-ready
archive: a7f3c98
```

Exit code:

```text
0
```

Failure:

```text
$ git pair check

NOT READY:
- latest review outcome is blocking
- archive does not point to the current source commit
```

Exit code must be non-zero.

CI should only need the exit code.

---

# 29. Feedback Policy

`git pair check` must support a policy switch controlling whether non-blocking feedback is sufficient for integration.

Default recommendation:

```bash
git pair check
```

requires explicit `APPROVED`.

Policy:

```text
APPROVED → pass
FEEDBACK → fail
BLOCKED  → fail
```

Permissive mode:

```bash
git pair check --allow-feedback
```

Policy:

```text
APPROVED → pass
FEEDBACK → pass
BLOCKED  → fail
```

A closed changeset always fails.

---

# 30. Status vs Check

Keep inspection and assertion separate.

## `git pair status`

Observational.

Example:

```text
Changeset: booking-transaction
Review: approved
Archive: current
Integration ready: yes
Integrated: no
Closed: no
```

It may also provide:

```bash
git pair status --json
```

## `git pair check`

Assertive.

Example:

```bash
git pair check
```

CI should use this command rather than parse human-readable `status` output.

---

# 31. Typical Successful Lifecycle

```text
implementation
    ↓
git pair change ready
    ↓
review
    ↓
git pair review submit --approve
    ↓
archive ref automatically advances to approval commit A
    ↓
git pair check
    ↓
CI passes
    ↓
forge integrates change however it wants
    ↓
resulting target commit B
    ↓
git pair integration record --source A --commit B
    ↓
integration ref created
    ↓
INTEGRATED
```

Refs:

```text
refs/git-pair/changesets/booking-transaction/archive
    → A

refs/git-pair/changesets/booking-transaction/integration
    → B
```

---

# 32. Abandoned Lifecycle

```text
WORKING / READY / BLOCKED / etc.
    ↓
git pair change close
    ↓
closure lifecycle commit C
    ↓
archive ref advances to C
    ↓
CLOSED
```

There is no integration ref.

```text
refs/git-pair/changesets/foo/archive
    → C

refs/git-pair/changesets/foo/integration
    absent
```

Closed changesets should disappear from normal actionable queues.

---

# 33. CI Integration Model

The core Git Pair protocol should remain forge-independent.

A forge-specific adapter is responsible for determining:

```text
SOURCE_SHA
INTEGRATED_SHA
```

For example, after a squash merge:

```text
source PR head:
A

new target commit:
B
```

The adapter runs:

```bash
git pair integration record \
  --source "$SOURCE_SHA" \
  --commit "$INTEGRATED_SHA" \
  --target origin/main
```

Git Pair itself resolves the changeset through archive refs.

---

# 34. Important Invariants

The implementation should preserve these invariants:

### Identity

```text
changeset ID != necessarily branch name
```

The ID is canonical once initialized.

### Archive

```text
archive ref → latest durable unsquashed changeset history
```

### Integration discovery

```text
source SHA → exactly one archive ref → changeset ID
```

### Integration

```text
integration ref → actual target commit
```

### Terminal outcomes

```text
INTEGRATED
or
CLOSED
```

are terminal.

### After integration

```text
archive and integration mappings are immutable
```

under normal Git Pair operations.

---

# 35. MVP Non-Goals

Do not require the MVP to:

* infer integration from patch equivalence,
* automatically support every forge,
* automatically configure GitHub/GitLab required checks,
* rewrite integration records after force-pushes,
* migrate/rename changeset IDs after refs exist,
* garbage-collect archived changesets,
* store a root-level active-changeset pointer,
* create separate refs for every lifecycle state,
* use branch names as permanent identity.

---

# 36. Summary

The durable Git Pair model should be:

```text
branch
    ↓ default ID suggestion

changeset ID
    ↓
refs/git-pair/changesets/<id>/

    archive
      → complete unsquashed author/review history

    integration
      → commit where that changeset landed
```

Changeset identity may be explicitly overridden:

```bash
git pair change init --id <id>
```

Review submissions keep the archive current.

CI validates readiness with:

```bash
git pair check
```

After actual integration, automation records the relationship with:

```bash
git pair integration record \
  --source <archive-tip-sha> \
  --commit <integrated-target-sha>
```

Git Pair derives the changeset ID by finding the archive ref that points at the supplied source SHA.

This provides permanent, unambiguous provenance across merge commits, squash merges, rebases, and other integration strategies without requiring forge-specific state in the core protocol.
