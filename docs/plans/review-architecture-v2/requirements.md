# git-pair Review Architecture Requirements

> Supplied by the reviewer in chat on 2026-06-15 as the requirements for the work planned in
> `../plan.md`. Reproduced here as the source of truth, with the source editor's fence labels removed.
> Where this document and the code disagreed at the time of writing, `../reconciliation.md` records the
> analysis and `../plan.md` §Decisions records the resolution; this file is left unedited.
>
> The reviewer narrowed these requirements twice in the same session — first to the local workflow with
> two create-only refs, then by walking back the `land` command. Those narrowings are *not* edits to this
> document. They are decisions, and they live in `../plan.md` §Decisions.

## Product Thesis

Git-pair is a decentralized code review system for Git where implementation history and review history
live together in Git rather than in a separate forge database.

The goal is to make peer review usable through the same filesystem, Unix tools, editors, IDEs, and agent
workflows developers already use to write code.

A secondary goal is to make review history useful as durable engineering memory. Historical feedback,
decisions, rejected approaches, resolutions, and approvals should remain locally searchable and
interpretable by both humans and agents.

Cryptographic reviewer verification is intentionally deferred. The initial target workflow is a local
developer and local agent operating in the same repository environment, where cryptographic identity
provides limited additional value.

## Core Model

Git-pair distinguishes three related concepts:

### Active branch

While work is active, the ordinary Git feature branch is the primary collaboration and review surface.

The branch carries both:

* implementation commits;
* review commits and review artifacts.

There is no continuously updated remote changeset ref.

The active review state therefore belongs to a specific branch history.

### Changeset

A changeset provides durable identity for a unit of work.

The changeset identity is independent of the branch name and survives:

* branch renames;
* branch deletion after integration;
* archival of the completed review history.

A changeset may appear in more than one branch history. Therefore, while work is active, the changeset ID
alone must not be treated as sufficient to identify the active review surface.

### Integration record

When a changeset reaches a terminal integrated state, Git-pair creates a durable integration ref that
preserves the complete interleaved implementation and review history.

Conceptually:

```text
ACTIVE

feature/foo

A -- B -- R1 -- C -- R2 -- D


INTEGRATED

refs/git-pair/integrations/<changeset-id>
                     |
                     v
A -- B -- R1 -- C -- R2 -- D
```

The ordinary feature branch may subsequently be deleted without losing the Git-pair record.

The integration ref is the critical durable Git-pair ref.

## Review Commits

Reviews are represented as commits within the active branch history.

A review commit contains human-readable review artifacts describing the review event, such as:

* reviewer;
* reviewed state;
* review status;
* comments and threads;
* resolution state;
* approval;
* changes requested;
* feedback-only review.

Review artifacts should be stored in stable, human-readable formats suitable for normal filesystem and
Unix tooling.

They should remain practical to inspect with tools such as:

```bash
git log
git show
git diff
rg
grep
find
$EDITOR
```

and with ordinary IDE filesystem/navigation features.

Git-pair should avoid opaque databases or serialized state when a simple textual artifact can represent
the same information.

## Interleaved History

Implementation and review events intentionally form one Git history.

Example:

```text
A -- B -- R1 -- C -- R2 -- D -- R3
```

where:

```text
A B C D   = implementation changes
R1 R2 R3  = review events
```

A commit may contain both implementation and review-artifact changes.

Git-pair must therefore avoid assuming that commits belong exclusively to either an implementation
category or a review category.

The history should make it possible to reconstruct not only the final implementation, but the development
and review process that produced it:

```text
implementation
→ concern raised
→ implementation changed
→ concern resolved
→ approval
```

The resulting history is intended to function as durable engineering memory.

## Implementation State as a Derived View

Git-pair should conceptually distinguish the raw Git branch tip from the implementation represented by
that branch.

The implementation state is a derived view of:

```text
base...HEAD
```

excluding Git-pair-owned metadata and review artifacts.

Conceptually:

```text
implementation state
=
changes from base to HEAD
minus Git-pair metadata paths
```

A review commit that modifies only Git-pair metadata therefore does not change the implementation state.

A review commit that modifies an ordinary source file does change the implementation state.

This is desirable. For example, if an inline review comment is inserted directly into a source file, a
later implementation pass should normally be required before final approval.

For the initial implementation, Git-pair does not need to expose this derived implementation state as a
synthetic commit or ref. It is a conceptual and comparison model.

The exact canonicalization and hashing strategy may be introduced later if needed.

## Immutable Review Anchors

When a review is created, Git-pair should create an immutable-by-convention ref that points to the
original review commit.

Suggested namespace:

```text
refs/git-pair/reviews/<review-id>
```

For example:

```text
refs/git-pair/reviews/123
        |
        v
        R1
```

These refs are append-only:

* creation is allowed;
* mutation is not;
* deletion is not performed by normal Git-pair operations.

Their purpose is to preserve the original review event even if the active branch is later rebased or
rewritten.

This provides two useful properties:

1. the original review commit remains reachable and is not garbage-collected;
2. Git-pair has a stable comparison point for detecting rewritten or altered review history.

These refs are not cryptographic proof of reviewer identity.

In the initial local-first threat model, they primarily protect against accidental or tool-driven
rewriting rather than a malicious actor with unrestricted repository write access.

Future remote deployments may strengthen this model by protecting the review-ref namespace server-side.

## Rebasing

Active branches may be rebased.

Because review commits are ordinary Git commits, rebasing may rewrite both implementation commits and
review commits.

For the initial implementation:

* rebasing after approval invalidates that approval;
* rewritten review commits must not be treated as equivalent to the original approval automatically;
* a new approval is required after a rebase affecting the reviewed branch history.

The immutable review ref preserves the original review event for inspection and comparison.

Git-pair must not silently reinterpret an earlier approval as approval of rewritten history.

### Future patch-equivalence support

Git-pair should leave room for future optional behavior that permits approval to survive a rebase when
Git-pair can prove that the reviewed implementation patch is unchanged.

Conceptually:

```text
original implementation patch
==
rebased implementation patch

→ prior approval may be carried forward
```

This behavior is intentionally deferred.

The initial default remains:

> Rebase after approval requires re-approval.

## Cryptographic Verification

Cryptographic reviewer verification is out of scope for the initial implementation.

Git-pair should not initially require:

* GPG keys;
* SSH signing keys;
* reviewer key enrollment;
* key synchronization;
* key rotation handling;
* trusted signer registries;
* signed review commits.

Review identity is therefore advisory in the same sense that ordinary unsigned Git author metadata is
advisory.

This is acceptable for the initial primary use case:

```text
developer + local agent
operating in the same repository environment
```

The architecture should leave room for signed review commits later.

A future trust model may use:

* signed Git commits;
* forge-backed reviewer identity;
* protected immutable review refs;
* repository-local trusted signer configuration.

## Remote Collaboration

Remote collaboration should initially rely on ordinary Git branch transport.

Git-pair should not introduce a continuously synchronized active-changeset ref merely to support remote
discovery.

If an agent and reviewer are on different machines, the active feature branch is pushed and fetched using
normal Git mechanisms.

Git-pair review commits travel through that branch like any other commit.

This keeps active collaboration aligned with ordinary Git:

```text
feature branch
    =
implementation state
+
review state
```

The immutable per-review refs may also be published if remote preservation is desired, but they are not
moving coordination refs.

## Review Queue

Git-pair may support a lightweight local review queue without introducing a distributed queue registry.

The queue is a derived view over locally visible branches.

Conceptually, `git pair queue` should:

1. enumerate relevant local branches;
2. determine whether each branch contains an active Git-pair changeset;
3. skip branches whose changeset has already been integrated;
4. inspect that branch's Git-pair history;
5. determine the branch-local review state;
6. include branches that are ready for review.

Conceptually:

```text
feature/a
  changeset: A
  not integrated
  state: ready
  → queue entry

feature/b
  changeset: B
  integrated
  → skip

scratch
  no Git-pair changeset
  → skip
```

The queue is branch-oriented because review commits are appended to branches.

A changeset appearing in multiple branches may therefore produce multiple branch-local states.

Git-pair should not automatically collapse those branches into one queue entry solely because they share
a changeset ID.

The queue should remain reconstructible from Git history rather than becoming stored mutable state.

A future `--all` or similar mode may inspect remote-tracking branches that are already present locally,
but Git-pair does not initially promise a globally synchronized remote review inbox.

## Integration Ref

When a changeset is integrated, Git-pair must create a durable integration ref before the active branch
can safely disappear.

Suggested namespace:

```text
refs/git-pair/integrations/<changeset-id>
```

The integration ref must preserve the complete Git-pair history required to reconstruct the changeset's
implementation and review process.

It should remain useful regardless of whether code reaches trunk through:

* merge;
* squash;
* rebase;
* another integration strategy.

The integration record should also retain enough metadata to relate the historical reviewed lineage to
the resulting state on trunk.

For example:

```text
changeset-id
historical branch tip
reviewed implementation state
integration target
integrated commit
integration method
terminal status
```

The exact artifact format is a separate implementation detail.

## Changeset Identity

A stable changeset identifier should be created at:

```bash
git pair init
```

and remain associated with the work for the life of the changeset.

Changeset identity must be independent of branch identity.

However, while active, Git-pair must not use changeset identity as a substitute for branch identity.

A changeset may be present in multiple branches. Therefore:

```text
changeset ID
    = durable logical identity

branch
    = active collaboration and review lineage
```

## Stacked Changesets

Stacked changesets are supported.

While a parent is active, a child changeset should track a specific parent branch.

For example:

```text
main
  \
   feature/a
        \
         feature/b
```

The child may record metadata conceptually equivalent to:

```yaml
parent:
  branch: feature/a
  changeset: A
```

The branch is the active parent locator.

The changeset ID provides durable identity and helps resolve the relationship later if the parent branch
disappears after integration.

### Parent branch review changes

Because review commits themselves advance the parent branch tip, stacked-child validity must not be based
solely on raw branch-tip equality.

Conceptually, Git-pair distinguishes:

```text
raw branch tip
```

from:

```text
implementation state of that branch
```

A parent review commit that modifies only Git-pair metadata does not represent an implementation change.

However, to keep the initial implementation simple, Git-pair does not yet attempt to preserve child
approval based on implementation-state equivalence.

### Initial conservative stacked-approval rule

For the initial implementation:

> Any change to the parent branch after a child is approved invalidates the child's approval.

This includes:

* parent implementation commits;
* parent review commits;
* parent approval commits;
* rebases;
* merges into the parent branch.

This rule is intentionally conservative.

It avoids requiring Git-pair to distinguish metadata-only parent changes from implementation changes
during the first implementation.

A future version may relax this rule using the derived implementation-state model.

### Parent integration

When a parent changeset is integrated:

1. the parent receives a durable integration ref;
2. the parent branch may be deleted;
3. the child retains the parent changeset identity in its stack metadata;
4. Git-pair may resolve the former parent relationship through the parent's integration record.

The parent integration record therefore serves as the durable bridge between:

```text
former active parent branch
```

and:

```text
integrated parent state on trunk
```

For the initial implementation, parent integration should be treated as a parent-state change and
therefore invalidates existing approval on the child.

The child may then be rebased, merged, or otherwise reconciled onto the integrated parent/trunk state
before being reviewed again.

Future versions may allow child approval to survive parent integration when Git-pair can prove that the
child's reviewed implementation delta remains unchanged.

### Abandoned parent

If a parent changeset is abandoned rather than integrated, Git-pair must not silently reparent the child.

The child should be marked as requiring explicit reconciliation.

The author may then choose a new base such as:

```text
main
another active feature branch
another changeset lineage
```

## Agent Memory and Historical Search

Review artifacts should be designed so historical engineering context can be recovered using local
filesystem and Git operations.

Agents should be able to answer questions such as:

* Has this approach been reviewed before?
* Why was this implementation changed?
* What concern caused this code to take its current form?
* Was a similar issue raised previously?
* Which implementation followed a particular review comment?
* What changed between a rejected review and eventual approval?
* What reasoning led to the version that was ultimately integrated?

This should not require a remote API, forge database, vector database, or proprietary memory service.

Semantic indexes or RAG systems may be layered on top later, but the repository itself remains the source
of truth.

Review artifacts should therefore favor:

* human-readable text;
* predictable paths;
* stable identifiers;
* grep-friendly structure;
* explicit relationships between review events and changesets.

## Design Principles

Git-pair should prefer:

* ordinary Git primitives over custom synchronization protocols;
* filesystem artifacts over opaque databases;
* immutable historical anchors over mutable remote coordination state;
* branches as active review surfaces;
* stable changeset identity for durable history;
* explicit review events over inferred external state;
* local-first operation;
* provider independence;
* compatibility with Unix tools and editors;
* agent-readable history.

Git-pair should avoid introducing distributed mutable state unless a feature clearly requires it.

In particular, an active custom ref namespace should not exist solely to enable a global review queue.

## Deferred Capabilities

The following are intentionally deferred rather than rejected:

* cryptographically signed review commits;
* trusted reviewer key management;
* historical key rotation;
* patch-equivalent approval carry-forward after rebases;
* approval preservation across metadata-only parent changes;
* approval preservation across parent integration;
* canonical implementation-state fingerprints;
* protected remote review-ref namespaces;
* globally synchronized review queues;
* forge-backed reviewer identity;
* semantic indexing of historical review artifacts.

## Core Invariants

The initial implementation should preserve these invariants:

1. **The feature branch is the active collaboration and review surface.**
2. **Implementation and review history coexist in the branch history.**
3. **Review state belongs to the branch history, not merely to the changeset ID.**
4. **The changeset ID is durable identity and is independent of branch name.**
5. **A changeset may appear in more than one branch.**
6. **Review commits may be preserved by immutable per-review refs.**
7. **Immutable review refs are append-only and are never rewritten by normal Git-pair operations.**
8. **A rebase after approval invalidates that approval by default.**
9. **A stacked child tracks a specific active parent branch.**
10. **Any parent-branch change after child approval invalidates the child's approval in the initial
    implementation.**
11. **Parent integration creates a durable bridge from the deleted parent branch to its integrated trunk
    state.**
12. **The integration ref permanently anchors the completed implementation and review history.**
13. **Deleting an integrated feature branch must not destroy the Git-pair record.**
14. **The review queue is derived from locally visible branch histories rather than stored as distributed
    mutable state.**
15. **Review artifacts remain human-readable, filesystem-accessible, and agent-searchable.**
16. **Cryptographic reviewer identity is deferred, but the architecture should permit it later.**
17. **Git-pair should not recreate forge-like distributed coordination machinery unless a concrete feature
    requires it.**
