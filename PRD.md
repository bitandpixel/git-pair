# git-pair MVP Requirements

## 1. Overview

`git-pair` is a local-first peer-review orchestration tool designed primarily for human–coding-agent workflows.

It does **not** replace Git, editors, difftools, GitHub/GitLab, CI/CD, or coding agents. Instead, it adds a thin review protocol and review-oriented UX on top of ordinary Git commits, files, refs, editors, and difftools.

The core design goal is:

> Keep review feedback inside the Git repository and working tree so that humans and agents can consume review state without depending on a forge-specific review API.

A review may consist naturally of:

-   direct edits to source code,
-   ordinary source-code comments,
-   edits to a changeset's high-level `ABOUT.md`,
-   creation or modification of focused Markdown discussion threads,
-   an explicit review outcome such as blocked, feedback-only, or approved.

The Git commit boundary provides the primary semantics. A commit identified as a review commit means that changes introduced by that commit should be interpreted as review feedback.

---

# 2. Design Principles

## 2.1 Orchestration, not replacement

`git-pair` should delegate existing functionality wherever possible.

It should use:

-   Git for versioning, commits, branches, diffs, and refs.
-   Git's configured editor (`GIT_EDITOR`, `core.editor`, `$VISUAL`, `$EDITOR`) for text editing.
-   configured Git difftools for code review.
-   GitHub, GitLab, Forgejo, etc. only as optional remote/CI/merge systems.

`git-pair` should not implement its own source-code editor or full diff renderer for the MVP.

## 2.2 Forge independence

The review protocol must not depend on GitHub PR comments, GitLab merge-request comments, or other forge-specific concepts.

A repository should retain the same review semantics if its remote is changed.

## 2.3 Git-native history

Development may produce a noisy history:

```text
implementation
ready
review
fix
ready
review
fix
ready
approval
```

That is acceptable on the working branch.

The final feature may later be squash-merged into `main`.

The complete unsquashed review/development history must be preservable via a dedicated Git ref.

## 2.4 Minimal review syntax

Avoid special inline syntax such as:

```text
REVIEW(blocking):
REVIEW(question):
```

unless a human chooses to write it naturally.

Because the enclosing commit is already known to be a review commit, its diff supplies the semantics.

Examples:

```text
source-code edit
    → concrete implementation feedback

new source-code comment
    → localized review feedback

ABOUT.md edit
    → high-level review feedback

new Markdown file
    → focused review thread

edit to existing thread
    → continuation of that discussion
```

---

# 3. Terminology

## Changeset

A logical unit of implementation and review associated with a branch.

For the MVP:

> one branch = one changeset = one review stream

Stacked branches each have their own changeset.

## Author

The party producing the change.

Initially this is typically a coding agent.

## Reviewer

The human evaluating the change.

MVP assumes one author/agent and one human reviewer.

## Review submission

A Git commit produced through `git pair review submit`.

A review submission establishes a durable review boundary.

## Review artifacts

Files within the active changeset directory, including:

-   `ABOUT.md`
-   changeset metadata
-   Markdown review threads

## Review archive

A dedicated Git ref that retains the complete unsquashed branch/review history even after the feature is squash-merged.

---

# 4. Repository Layout

Each changeset has a directory:

```text
changesets/
  booking-transaction/
    CHANGESET.yaml
    ABOUT.md
    concurrency-tests.md
    transaction-boundary.md
```

The directory name is the changeset's **ID**, and the ID is what git-pair calls the work
from here on: it names the directory, and once durable refs exist it names those too
(§13). The branch name is where the default comes from, not the identity.

For branch names containing `/`, the name is normalised into a filesystem-safe slug:
`/` becomes `-`, other characters outside `[A-Za-z0-9._-]` become `-`, runs collapse, and
leading and trailing `-` are trimmed. Case is preserved.

Example:

```text
branch:
feature/booking-transaction

changeset:
changesets/feature-booking-transaction/
```

The rule is deterministic, and it is a suggestion. `change init --id <id>` chooses the ID
instead (§9.1), which is what makes the identity independent of the branch: branches get
renamed, two branches can normalise to the same name, and integration tooling should not
have to infer an identity from a branch.

An ID is never rewritten to fit. An `--id` that would need normalising is refused rather
than quietly changed, because refs named after a string nobody typed are not findable by
the person who typed it.

The directory belongs to the branch its `CHANGESET.yaml` records (§5), not to the branch
whose name it resembles. That is what allows the two names to differ at all, and it is why
a child branch created on top of its parent does not appear to own the changeset directory
it merely inherited.

---

# 5. Changeset Metadata

Each changeset contains:

```text
CHANGESET.yaml
```

The minimum required metadata is:

```yaml
id: booking-transaction
base: main
branch: feature/booking-transaction
```

`id` is the changeset ID, and it is the directory's name rather than a second opinion
about it: a file whose `id` disagrees with the directory holding it is an error to
correct, not a conflict to resolve. `branch` is the claim that makes the directory belong
to a branch, and it is the reason the ID can differ from the branch name. `base` is the
ref the changeset's diff is measured against.

The ID does not change once the changeset has durable refs. Renaming one means moving the
directory and every ref under it, which is not something git-pair does silently; there is
no rename command and no automatic migration.

A changeset written before `branch:` existed has no claim, and is found by the name rule
instead — the same answer the old code gave. A recorded claim always outranks the name.

For stacked branches:

```text
booking-transaction
  base: main

booking-transaction-tests
  base: booking-transaction

booking-transaction-ui
  base: booking-transaction-tests
```

Example:

```yaml
base: booking-transaction
```

The configured base determines the default changeset diff.

Metadata should remain intentionally minimal.

Do not model a complex stack graph in the MVP.

---

# 6. ABOUT.md

`ABOUT.md` is the canonical top-level description of the changeset.

The author/agent should create and populate it before requesting review.

It serves a similar purpose to a good pull-request description and may contain:

-   summary,
-   motivation,
-   implementation overview,
-   significant design decisions,
-   important files/components,
-   tests and validation,
-   known limitations,
-   open questions.

Example:

```md
# Booking transaction locking

## Summary

Adds transactional locking around offering creation and enrollment.

## What changed

-   Added business-scoped locking for offering creation.
-   Added offering-scoped locking for enrollment.
-   Added concurrent capacity tests.

## Design decisions

Administrative scheduling is serialized at the business level because
expected write concurrency is low.

Student enrollment is scoped to an offering to avoid blocking unrelated
offerings.

## Validation

-   Unit tests
-   Integration tests
-   Concurrent final-seat enrollment test
```

The reviewer edits this file naturally.

Edits made in a review commit are high-level feedback.

Agents may also update `ABOUT.md` in later implementation commits in response to review feedback.

---

# 7. Review Threads

Focused discussions live as ordinary Markdown files within the changeset directory.

Example:

```text
changesets/booking-transaction/
  concurrency-tests.md
```

No mandatory conversational schema is required.

Example:

```md
# Concurrency tests

I'm not convinced the current test actually causes the two transactions
to overlap at the capacity check.

Could we explicitly block both transactions immediately before the
check, then release them together?
```

An agent may later append or modify the thread:

```md
## Response

The original test was concurrent at the promise level, but the fixture
serialized access earlier than expected.

I changed the test to synchronize both transactions immediately before
the capacity read.
```

Git history provides authorship and sequencing, so the document format should remain natural and lightweight.

---

# 8. CLI Structure

The CLI has two primary subdomains:

```text
git pair change ...
git pair review ...
```

Shared inspection commands remain top-level.

Target MVP structure:

```text
git-pair
├── change
│   ├── init
│   ├── ready
│   ├── unready
│   ├── feedback
│   ├── wait
│   └── complete
│
├── review
│   ├── open
│   ├── about
│   ├── thread
│   ├── submit
│   ├── history
│   └── queue
│
├── status
└── diff
```

---

# 9. Author Commands

## 9.1 `git pair change init`

Initializes review scaffolding for the current branch.

Example:

```bash
git pair change init --base main
git pair change init --id booking-transaction-v2 --base main
```

Creates:

```text
changesets/<id>/
  CHANGESET.yaml
  ABOUT.md
```

`CHANGESET.yaml`:

```yaml
id: booking-transaction-v2
base: main
branch: feature/booking-transaction
```

Requirements:

-   determine current Git branch,
-   determine the changeset ID: `--id` when given, otherwise the branch name normalised (§4),
-   create deterministic changeset directory,
-   create metadata,
-   create `ABOUT.md`,
-   accept `--base`,
-   should be safe/idempotent where practical,
-   should not destroy existing changeset data.

`--id` chooses the identity rather than accepting the default (§4). Three rules follow from
that:

1. **It is not rewritten.** An ID needing normalisation — a space, a path separator, a
   doubled hyphen — is refused. Silently turning `booking v2` into `booking-v2` would name
   refs after a string nobody typed.
2. **It is unique.** An ID already in use stops the command rather than gaining a suffix.
   In use means a `changesets/<id>/` directory in the working tree or in `HEAD`'s tree, or a
   durable ref already belonging to that ID (§13). A suffix would be an identity nobody
   chose, baked into refs the moment the changeset is readied, so the command fails and
   offers a free candidate to type instead:

   ```text
   changeset ID "feature-booking" is already in use: changesets/feature-booking already
   exists, possibly left behind by a changeset that has landed.

   Choose another ID:

     git pair change init --id feature-booking-2
   ```

   Two branches whose names normalise alike are a collision once the directory from one is
   in the other's tree, which is what a derived branch and a landed changeset both look
   like. Two sibling branches that share no directory are not one yet: nothing is in use.
3. **It does not change.** A branch that already owns a changeset cannot acquire a second ID
   by re-running `init`; that would put two claims on one branch (§5).

Deleting a changeset directory releases its ID only when the deletion is committed, since
retiring a changeset's notes is a commit and not a local edit.

When the branch has no changeset but the tree holds one whose `branch:` names a branch that
no longer exists — a renamed branch — `init` says so and how to fix the claim, then proceeds.
Creating a second changeset is a legitimate answer, so this is a warning rather than a gate.

For a stacked branch:

```bash
git pair change init --base booking-transaction
```

## 9.2 `git pair change ready`

Signals that the current implementation is ready for human review.

Requirements:

-   changeset must exist,
-   working tree should be clean,
-   `ABOUT.md` should exist,
-   run the surviving-review-additions diagnostic described below,
-   fail non-interactively by default if surviving review additions are detected,
-   create a review marker commit only if validation succeeds or an explicit override is supplied,
-   anchor the ready marker in `refs/reviews/<changeset>`, so the offered history is reachable
    without the branch (§13),
-   make the branch discoverable by `git pair review queue`.

Suggested commit:

```text
git-pair: ready booking-transaction
```

Include machine-readable Git trailers, e.g.:

```text
Review-State: ready
Review-Changeset: booking-transaction
```

Readiness is an offer about the implementation state the marker sits on, and it is withdrawn by a
command rather than inferred: a later commit does not un-ready the changeset, so work in progress
never drops it out of the queue on its own. `git pair change unready` (§9.6) takes it out on purpose,
and a review submission supersedes the marker.

The tree is consulted at exactly one point in the lifecycle, and it is not here: `change complete`
(§9.5) refuses to archive a head whose reviewed content has moved since the review.

### Surviving review additions

Before marking the changeset ready, `git-pair` must inspect additions introduced by the most recent review submission.

If any such additions still survive unchanged at current `HEAD`, the command must:

1. print the surviving additions and their locations,
2. exit non-zero,
3. not create the ready marker.

Example:

```text
Cannot mark changeset ready.

2 additions from review 8ab932f still survive unchanged:

src/booking/service.ts:84
  // Please use a transaction here

src/booking/service.test.ts:131
  // What happens if these execute concurrently?

Review these additions before marking the change ready.

To intentionally preserve them:
  git pair change ready --allow-surviving-review-additions
```

The command must remain fully non-interactive.

The explicit override is:

```bash
git pair change ready --allow-surviving-review-additions
```

This is intended for cases where review-added code or comments are deliberately retained.

The check applies only to additions from the **most recent review submission**, not all historical review additions.

## 9.3 `git pair change feedback`

Shows what the most recent review submission told the author, by showing the submission itself.

Accepts `--changeset <slug>`, which reads the changeset from whichever branch carries it (see
§11.1 for why only reads may do this).

```bash
git pair change feedback
```

Requirements:

-   diff `latest-review^..latest-review`, using ordinary Git diff output,
-   include the review's changeset artifacts (threads, `ABOUT.md` edits) and any direct code edits the reviewer made,
-   support `--stat` and `--name-only`,
-   exit non-zero with a clear message when the changeset has no review submission yet.

This is the author's answer to "what did the reviewer just tell me?".

`git pair diff --unreviewed` (§17.2) answers a different question — "what has changed after the review I last read?" — and is the reviewer's command. It is usually empty right after a submission, because the submission is then the newest commit on the branch. An author who has just been told a review exists reads it with `git pair change feedback`, not with `git pair diff --unreviewed`.

## 9.4 `git pair change wait`

Blocks until a reviewer makes the changeset actionable.

```bash
git pair change wait
git pair change wait --fetch --interval 30s
git pair change wait --fetch --timeout 2h --json
```

Requirements:

-   exit successfully when the effective state moves from `ready` to `blocked`, `feedback`, or `approved`,
-   report immediately, without waiting, when the changeset is already in one of those states,
-   exit non-zero when the changeset is `working`: the author is not waiting on anyone,
-   poll local review state by default,
-   with `--fetch`, run `git fetch` against the configured remotes before each check, so a review submitted in another clone becomes visible through `refs/remotes/...`,
-   `--interval` sets the polling interval (default 10s),
-   `--timeout` gives up after a duration instead of waiting forever,
-   `--json` prints `previous_state`, `state`, `review_commit`, `ref`, `fetches`, `waited_seconds`, `timed_out` and `next_action`, with state names spelled as `git pair status --json` spells them,
-   a wait that ends because of `--timeout` exits non-zero,
-   waiting never writes to the repository: no commits, no refs, no index changes.

The command is non-interactive and must be safe for an agent to run unattended.

`--fetch` is ordinary Git fetching. Forge notifications, webhooks and forge API polling are out of scope (§25): a review reaches the author by being pushed to the repository, like any other commit.

The author-side loop is therefore:

```bash
git pair change ready
git push origin my-feature
git pair change wait --fetch --json   # exits when a reviewer has acted
git pair change feedback               # read what they said
```

## 9.5 `git pair change complete`

Archives the complete unsquashed review history at HEAD, before squash/merge.

The owner owns this half of the lifecycle. A reviewer's `approve` is a judgement about the code;
completing the changeset is the owner's decision that the reviewed state is what they are taking
forward. The command therefore records no commit and establishes no state (§12).

Responsibilities:

1. verify the working tree is clean,
2. verify the newest review at HEAD permits integration (`approve` or `feedback`), and that the
   content it reviewed is what HEAD still carries: the tree is compared between that marker and
   HEAD, ignoring `changesets/<changeset>/`, and drift refuses the completion (§12) — this is the
   only point in the lifecycle where a commit can stop an operation,
3. run the surviving-review-additions diagnostic,
4. require explicit acknowledgement if surviving additions remain, or if content outside
   `changesets/<changeset>/` has arrived on top of the marker that permits integration —
   `--allow-unreviewed-changes` covers the second case, and nothing covers a `block` or a
   `change unready`,
5. ensure the complete current branch history is anchored under `refs/reviews/`,
6. write the immutable archive ref `refs/reviews/archive/<changeset>/<short-head>` at HEAD, created
   only if absent and never moved,
7. print the resulting archive ref and integration readiness.

It must **not** merge, push, or squash.

Example output:

```text
Completed changeset booking-transaction

Review archive:
  refs/reviews/archive/booking-transaction/91bf204

Safe to squash/merge.
Review history stays reachable at refs/reviews/booking-transaction
```

If surviving review additions remain, completing must fail unless explicitly overridden, and so
must a head whose content moved past the review that permits it:

```bash
git pair change complete --allow-surviving-review-additions
git pair change complete --allow-unreviewed-changes
```

`--json` prints `changeset`, `state`, `head`, `short`, `base`, `review_ref`, `archive_ref`,
`archive_created`, `squash_safe`, `acknowledged_survivors`, `acknowledged_unreviewed_paths` and
`surviving_review_artifacts`. `state` is the derived state, which completion does not change.

Running the command again at the same HEAD succeeds and changes nothing: the operation is "this head
is archived", and an archive ref is never moved or duplicated (§13).

Completion is not a state. The changeset is finished when the archived history is merged into the
deployment branch, which is ordinary git that git-pair neither performs nor derives. `git pair
status` reports `archive_ref` while HEAD is that archived commit and stops reporting it as soon as
other work lands, so a branch with unfinished work never looks finished.

`approve` and `complete` are intentionally separate concepts:

```text
approve
    human judgment about the code

complete
    the owner's archival operation
```

## 9.6 `git pair change unready`

Withdraws the changeset from the review queue when the author wants to keep implementing.

Readiness is an offer the author makes with `change ready`. Without a command that takes it back, the
only way out of `READY` is to commit something, and a reviewer reading the queue cannot tell an offer
that was withdrawn from one that was forgotten. So withdrawal is an act, recorded the way every other
lifecycle act is.

Checks, in order:

1. clean working tree — the marker is a commit,
2. the changeset is in review: `READY`, `APPROVED` or `FEEDBACK`.

Writes `git-pair: unready <slug>` carrying `Review-State: working` and `Review-Changeset: <slug>`.

`working` is not a sixth state. `WORKING` is what a changeset with no marker derives anyway; the value
exists so an author can choose that state deliberately. It is recognised as a marker rather than as an
implementation commit, so the `Reason` line names the retraction instead of counting it as work.

On a `WORKING` or `BLOCKED` changeset there is nothing to withdraw. The command succeeds and records
nothing, which lets a script unready unconditionally and keeps a repeated invocation from leaving two
identical markers behind. Retracting a `BLOCKED` changeset is not what the command means: the author is
already expected to act, and the block stays the newest marker until they ready the changeset again.

Withdrawing an approval supersedes it rather than deleting it. The approval remains in
`git pair review history`, and because the newest marker is now the retraction, `change complete`
(§9.5) refuses until a reviewer approves again.

A reviewer may still submit against an unready changeset — `review submit` accepts any state — which is
what keeps the gate back into `READY` reachable: surviving review additions (§19) are still enforced by
`change ready`.

`--json` prints `changeset`, `branch`, `base`, `state`, `was`, `recorded`, `unready_commit` and
`review_queue_visible`.

## 9.7 `git pair change abandon`

Records that the changeset will not be taken forward, and anchors the record so it outlives the branch.

Two endings exist, and only one of them is git-pair's to record. Completion (§9.5) means *merged*,
which the deployment branch knows and git-pair deliberately does not derive. Abandoning means *not
coming back*, which the author knows the moment they decide it — and without a command for it, the
only way to say so is to delete the branch, which destroys the history that explains why.

Checks, in order:

1. the changeset exists on the checked-out branch — a changeset whose branch is already gone cannot
   be abandoned, because abandoning is a decision about work that is still there, and post-merge
   bookkeeping is not a lifecycle act (§10.6 classifies a leftover directory on its own),
2. clean working tree — the marker is a commit,
3. the changeset has not already ended, in which case the command succeeds and records nothing.

Writes `git-pair: abandon <slug>` carrying `Review-State: abandoned` and `Review-Changeset: <slug>`, then
moves `refs/reviews/<changeset>` (§13) to that commit. The ref move is the point of the operation: a
terminal record on a branch that gets deleted is a record that disappears with it.

`abandoned` is not a sixth state. The changeset reports `WORKING`, which already means "not in
review, nothing owed", and the ending is reported beside the state as `abandoned` and
`abandoned_commit` in `status --json`. Splitting it this way keeps `state` — the field agents branch
on — at its five values, while still making a changeset that can never move again recognisable.

The ending closes the changeset, which is the difference from `change unready` (§9.6), which only
withdraws an offer for now. `change ready`, `change unready` and `review submit` refuse against an
abandoned changeset, and the check consults both places the record can live: the branch chain and the
anchor. Whichever survives is enough, which is also what stops a newly created branch from restarting
a changeset whose name already ended.

`--json` prints `changeset`, `branch`, `state`, `was`, `recorded`, `abandoned_commit` and `review_ref`.

---

# 10. Reviewer Commands

## 10.1 `git pair review open`

Launches the interactive review TUI.

Responsibilities:

-   identify current changeset,
-   determine review span,
-   display changed files,
-   display local reviewed/unreviewed progress,
-   provide shortcuts to external editor/difftool,
-   expose `ABOUT.md`,
-   expose review threads,
-   allow user to mark files done/undone.

The TUI is an orchestration interface rather than a source editor.

## 10.2 `git pair review about`

Opens:

```text
changesets/<changeset>/ABOUT.md
```

using the standard editor.

Editor resolution should follow git's, which means asking git rather than guessing at it:

1. `$GIT_EDITOR`
2. `core.editor` (repository-local first, then global)
3. `$VISUAL`
4. `$EDITOR`
5. the fallback the git build itself would use

`git var GIT_EDITOR` answers all five, so git-pair asks for it and consults 3–5 only when git
cannot answer. The value is a command line, so `code --wait` is a program plus its flags, as git
treats it.

Example:

```bash
git pair review about
```

The TUI should temporarily relinquish terminal control while the editor runs and resume/redraw after it exits.

## 10.3 `git pair review thread`

Creates or opens a focused review-thread Markdown file.

Example:

```bash
git pair review thread "concurrency tests"
```

could create/open:

```text
changesets/booking-transaction/concurrency-tests.md
```

Requirements:

-   slugify title,
-   avoid accidental duplicate threads where possible,
-   if an existing matching thread exists, open it,
-   if no title is supplied in an interactive terminal, prompt for one,
-   open using the editor git resolves.

Thread files should remain ordinary Markdown.

## 10.4 `git pair review submit`

Completes the current human review iteration.

Supported outcomes:

```bash
git pair review submit --block
git pair review submit --feedback
git pair review submit --approve
```

### `--block`

Meaning:

> Changes are required before merge. The author/agent should address this review and return the changeset for another review.

The commit may include:

-   source-code edits,
-   source-code comments,
-   ABOUT edits,
-   new thread files,
-   thread responses/changes.

### `--feedback`

Meaning:

> This review contains non-blocking observations or suggestions. Merge is permitted once other requirements are satisfied.

### `--approve`

Meaning:

> The reviewer considers the changeset acceptable for integration, subject to CI/policy.

Approval must support a clean working tree and therefore may create an empty Git commit.

Every review submission should create a standardized commit with machine-readable trailers.

Example:

```text
review: booking-transaction

Review-Outcome: block
Review-Changeset: booking-transaction
```

or:

```text
review: approve booking-transaction

Review-Outcome: approve
Review-Changeset: booking-transaction
```

Immediately after successful review submission, update the changeset's review archive ref to the resulting exact `HEAD`.

## 10.5 `git pair review history`

Accepts `--changeset <slug>`, which reads the changeset from whichever branch carries it (see
§11.1 for why only reads may do this).

Displays all review submissions for the current changeset.

Example:

```text
INDEX   SHA      OUTCOME    AGE
0       a18cf91  block      2d
1       39b71aa  block      1d
2       f7c92e0  feedback   4h
3       c81ea22  approve    20m
```

The index is chronological.

Indexing semantics:

```text
0   first review
1   second review
...
-1  most recent review
-2  second-most-recent review
```

Only commits explicitly identified as review marker commits count as reviews.

Ordinary Git commits do not.

## 10.6 `git pair review queue`

Shows changesets currently ready for human review.

Default scope: current repository.

Scope is the repository, not the checkout: the queue enumerates local branches, reads each one's
`CHANGESET.yaml` and history from that commit, and reports the ones whose derived state is `READY`.
Running it on the deployment branch is therefore meaningful, and the working tree it happens to have
checked out changes nothing.

A changeset directory whose branch is gone is classified rather than skipped. If the anchor and the
base carry the same `changesets/<changeset>/` content, the work landed, and the queue says nothing;
a directory with no anchor was never offered, and also says nothing. Anything else — anchored work
that is not in its base — is named in `skipped`, because that line is the only surviving record of
the work.

Example:

```text
READY FOR REVIEW

booking-transaction-tests
  base: booking-transaction
  ready: 18m ago
  head: a31c9d2

waitlist-rebooking
  base: main
  ready: 1h ago
  head: 92bf019
```

Future/global support should allow:

```bash
git pair review queue --global
git pair review queue --json
```

A global repository registry may eventually live in:

```text
~/.config/git-pair/config.toml
```

with commands such as:

```bash
git-pair repo add ~/dev/cadence
git-pair repo remove ~/dev/cadence
git-pair repo list
```

Global repository management is useful but may be deferred if needed.

The queue command should have a stable machine-readable form suitable for automation and notifications.

---

# 11. Top-Level Commands

## 11.1 `git pair status`

Displays the state derived for a changeset: the checked-out branch's by default, or the one named
by `--changeset <slug>`.

Reads may name any changeset, because reading a commit damages nothing: the slug resolves to
whichever branch carries it, and the state is derived from that commit. Nothing that records a
marker accepts the flag. A marker is a commit, and a commit lands wherever `HEAD` points, so
`change ready --changeset other` would write onto the branch you are standing on while claiming to
describe a different one. Fields that describe the checkout rather than the commit — `uncommitted`,
`span` — report that they cannot answer (`null`, `""`) for a changeset read from elsewhere.

A span of another branch is reachable by naming its ends, which `diff` already does: `git pair diff
--base-ref=main --head-ref=booking`.

Example:

```text
Changeset: booking-transaction
Branch: booking-transaction
Base: main
State: ready
Head: c18c9a7

Latest review:
  outcome: block
  commit: 91bf204

Review anchors:
  movable: refs/reviews/booking-transaction
    points at: 91bf204

Uncommitted changes: no
```

Provide:

```bash
git pair status --json
```

for agent/automation use.

Potential JSON:

```json
{
    "changeset": "booking-transaction",
    "branch": "booking-transaction",
    "base": "main",
    "state": "ready",
    "head": "c18c9a7",
    "latest_review": {
        "index": 2,
        "outcome": "block",
        "commit": "91bf204"
    },
    "review_ref": "refs/reviews/booking-transaction"
}
```

## 11.2 `git pair diff`

Displays or launches the configured diff for the selected logical review span.

The command should understand changeset base metadata and review boundaries.

It should support file-specific invocation:

```bash
git pair diff src/booking/service.ts
```

and span options described below.

`git pair diff` and its span options serve review. An author who wants to read a review that was just submitted uses `git pair change feedback` (§9.3) instead.

---

# 12. Review Lifecycle

Effective lifecycle:

```text
initialized
    ↓
working
    ↓
ready
    ↓
review block
    ↓
working
    ↓
ready
    ↓
review feedback / approve
    ↓
complete (archival, owner's decision — not a state)
```

Any point above can also end: `git pair change abandon` (§9.7) records a terminal marker, and the
commands that move state refuse against the changeset afterwards.

The author's side of that loop is `git pair change ready`, then `git pair change wait` to learn that a reviewer has acted, then `git pair change feedback` to read the submission before addressing it, then `git pair change complete` (§9.5) to archive the reviewed head.

Readiness also ends on purpose. `git pair change unready` (§9.6) writes a `working` marker and takes
the changeset back out of the queue, which is how an author says "not finished after all" instead of
leaving the offer standing while they keep implementing.

State comes from the branch. There is one fallback, and it is not a second source: where a changeset
has no branch — deleted after the work landed, or after it was abandoned — the anchor at
`refs/reviews/<changeset>` is what remains, and deriving from it can only report what the branch last
claimed before it disappeared (§13). Nothing reads a ref to decide that a changeset is ready, and no
command moves state by moving a ref.

Possible effective states:

```text
WORKING
READY
BLOCKED
FEEDBACK
APPROVED
```

Five states, six markers: `working` is written by `change unready` (§9.6) and is the same state a
changeset with no marker derives; `abandoned` (§9.7) is the seventh and names no state — an abandoned
changeset reports `WORKING`, and the ending is reported beside it as `abandoned_commit`.

There is no state for a completed changeset. `complete` archives a head in `refs/reviews/` and records
no commit, and the changeset is finished when that archived history is merged into the deployment
branch. State is derived from a changeset's own commits, so git-pair does not derive the merge:
`git pair status` reports the archive ref for as long as HEAD is the archived commit.

Avoid maintaining a fragile mutable state variable where possible.

Prefer deriving effective state from review marker commits and repository state.

Important safety property:

> State moves when a git-pair command records a marker, and at no other time.

A commit is not a command. An author who keeps working after `change ready` leaves the changeset
`ready`, which is why `change unready` (§9.6) exists: taking an offer back is a decision, and a
decision is recorded rather than inferred. `status` counts the commits that arrived since the
marker, so the drift is visible without being a state.

The archive is where the older property still holds, and it is the command whose output gets
trusted:

```text
review: approve
agent changes implementation
```

`git pair status` reports `APPROVED` — the newest marker is still the approval — and
`git pair change complete` refuses to archive that head. An archive naming unreviewed content
would be a promise the tool cannot keep, so completion is the one command that compares the
tree (§9.5).

---

# 13. Review Archive Refs

Offering a changeset for review should anchor it in a durable local ref, and every review
submission moves that ref to what it reviewed:

```text
refs/reviews/<changeset>
```

Example:

```text
refs/reviews/booking-transaction
```

pointing to the exact current `HEAD`.

This keeps the entire implementation/review/fix chain reachable from Git garbage collection.
The ref is written by `change ready` as well as by a submission, because the history worth saving
begins at the first handoff rather than at the first response: a changeset that was offered and never
reviewed, or whose branch exists only in the reflog, otherwise has nothing holding its marker alive.
As with every review ref it is a warning rather than a guarantee — nothing here stops a later
`git push --delete`.

`change abandon` (§9.7) also moves this ref, to its terminal marker. That is what lets an ending
survive `git branch -D`: the branch is the thing that gets deleted, and the queue and `status` can
still read how the changeset ended from the anchor. Where a changeset has no branch, the ref is the
last readable copy of what the branch claimed — a fallback for reading history, never a source of
state (§12).

Completing a changeset (§9.5) writes an immutable archival ref alongside it:

```text
refs/reviews/archive/<changeset>/<short-head>
```

Example:

```text
refs/reviews/archive/booking-transaction/91bf204
```

It is created only if absent and never moved, so completing the same head twice cannot rewrite an
archive.

The fundamental invariant is:

> Before the review stack can be considered safely squashable, the complete final unsquashed stack must remain reachable from a dedicated review ref.

Remote propagation of review refs may be added via push configuration/hooks later.

The MVP should ensure local preservation first.

---

# 14. Interactive Review TUI

The TUI should be intentionally small.

Example:

```text
╭ booking-transaction-tests ───╮
│ base  booking-transaction    │
│ span  unreviewed ▸           │
│ ABOUT.md                     │
│ ▾ Threads                    │
│     concurrency-tests.md     │
│     locking.md               │
│     + new thread…            │
╰──────────────────────────────╯

▾ ◐ src/booking/  2/3
  ▾ ✓ concurrency/
      ✓ lock_test.ts
  ✓ fixtures.ts
  ○ main.ts

2 / 3 reviewed
────────────────────────────────
```

Two regions share the row area and the keyboard: the changeset box above, and the file tree below it,
with the reviewed counter under the tree. The box holds what the review is made of — the span it is
measured against, `ABOUT.md`, the thread heading, the threads nested under it, and the entry that starts
another — so reading what the changeset says about the code and choosing the next file are motions on the
same screen rather than modes. The heading collapses and counts what it hides — `▸ Threads (2)` collapsed,
plain `▾ Threads` when they are on screen — so a changeset with many threads stays scannable. `Tab` moves
the keys around the ring (tree, diff where there is room for one, box), `f` and `m` name a region instead
of walking to the next one, and each region keeps its own cursor, so the keys return to the row they left.
The navigation keys belong to the region holding them and mean the same thing in either: `j`/`k` a row,
`gg`/`G` its two ends, `ctrl-d`/`ctrl-u` half a page, `ctrl-f`/`ctrl-b` a page. Each region's shortcut bar
names the keys of that region and none of the others, which is what makes a key of the other region an
absence a reviewer can read rather than a keystroke that vanishes. A rule separates the list from the
shortcut bar.

The box is capped at a third of the space the terminal gives and scrolls inside its own borders, so a
changeset with forty threads is a reason to read the box rather than a reason to hide the tree behind it;
rows that do not fit are counted in the bottom border (`3 more`), a note placed there because a note with
a row of its own would move the layout as it came and went. The borders are also the box's focus light —
single rules, double rules while it holds the keys — the convention the divider uses for the diff, chosen
over styling alone so the focus survives a terminal that renders no bold. The box is closed on all four
sides in both layouts, including the side nearest the diff column: without it the rows inside read as a
column of loose text rather than as the changeset's own block. Its cursor row is highlighted only while
the box holds the keys, since the borders say that already and a row that looks chosen and is not is a row
a reviewer marks by mistake. The span is the box's one
control: `Enter` or `Space` on its row opens the span picker, since what a review is measured against is
worth choosing, and `base` above it is a line of the frame rather than a row, since a base is only read.
The picker commits nothing until its own `Enter`, so the row is available over a historical span as well.
A row that changed in the span keeps its file row as well as its place in the box: the tree is what the
diff did, the box is the shortcut to read what was said about it.

The files are a tree rather than a list of paths. Each sits under its directory, a directory that
holds nothing but one directory is folded into that row (`src/booking/` above is one row, not two),
and a row prints only the name the rows above it have not already said. Each level is indented one step
deeper than the row above it — a directory row spends cells on its fold arrow and its mark gutter, and an
indent the width of that prefix would land every child's mark in the column its parent's mark started at,
which is a flat list with arrows in it. `h` and `l` fold and unfold
the directory under the cursor — the left and right arrows do the same, and `Enter` does both, the
way it does for the thread heading — while `c` folds the whole tree and opens it again, which is how
a changeset of a hundred files is read for its shape before it is read for its detail. Folding moves
the cursor onto the directory when it was hiding the row the cursor was on, so no fold can leave the
cursor somewhere the reviewer did not move it.

A directory's mark is its subtree's: `✓` when every file under it is reviewed, `○` when none is, and
between the two the count of what is left (`◐ 2/3`), because a tick there would be a claim about
files nobody has opened. `Space` on a directory marks every file under it, folded or not — a fold is
a way of looking at the list, not a statement about what has been read — and the next press clears
them. This is the file-level state one level up rather than a second kind of state: nothing is stored
about a directory, marks keep their per-file keys, and a marked directory that quits and reopens is
the same answers it always was. `d` on a directory is git's own answer to *diff this package*: the
pathspec expands into every file the span changed under it, and the preview pane shows that subtree
with the subtree's counts. The tree is a view, so it folds over a read-only span like anything else
a reviewer reads, and shows no marks there.

On a terminal at least 100 columns and 16 rows, the list shares the screen with a preview column:
the diff the common span made to the selected file. It prints git's
own output, colour included, and adds only what a fixed-width column cannot decline to do: the line
number git itself put in the hunk header, a break where a line is too wide, and the spaces a tab
advances to. It does not fold, group, filter, or renumber hunks, and it does not choose
colours, which is the shape PRD §3's refusal to build a diff renderer leaves: the pane is
for glancing, and reading a diff means opening it. The frame fills the terminal — the row
area holds the window's height and every row is padded to its width — so the shortcut bar
sits against the bottom edge rather than under a short list.

`p` moves the keys into that column and does nothing else, so a diff longer than the column is read
with the keys a diff is read with: `j`/`k`, `ctrl-d`/`ctrl-u`, `ctrl-f`/`ctrl-b`, `gg` and `G` scroll
the file already on show, and `Enter` opens the difftool on it. `Esc` hands the keys back to the
region that had them — tree or box — and leaves the pane where it was; `tab` moves them on to the box,
because a region you can only leave by backing out of is not a stop on a ring. `p` pressed where the diff
already holds the keys does nothing, rather than meaning the opposite of what it means in the list, and
`q` quits from the pane as it quits from everywhere else — the pane is one `p` away and the marks are on
disk, so nothing is at stake in the difference. While the pane holds the keys nothing that changes the review can happen —
marking, editing, threading and
submitting are keys that do not occur, which is the whole-screen preview's promise extended to a
column that never hid its list. The frame says which column has the keys by changing what is drawn
rather than only how it is styled: the divider becomes a double rule, the pane's file line becomes a
title, and the shortcut bar becomes the pane's own, naming every key it reads and none it does not.
A terminal resized below the pane's floor takes the column away and gives the keys back to the region
that held them before the pane, because a column that is not drawn cannot hold the keyboard.

When the reviewer has edited a file without committing, the pane shows those edits below the author's,
under a caption naming them, with counts and line numbers of their own. Git's output does not say who
typed a line, so the caption is what keeps the reviewer's work from reading as the author's; the
section is diffed from the revision under review, not the span's start, so it cannot repeat the
author's changes. The reviewed counter stays the span's: a reviewer's typing does not change what has
been reviewed.

A span whose head is a commit rather than the working tree is a look at history, and the screen
says so where the reviewer is already looking: the counter's slot carries `HISTORICAL · READ ONLY`,
the reviewed gutter and the offer to start a thread are absent, and the shortcut bar advertises
only the keys it can take. Marking, editing, threading and submitting are refused with a message
naming the head the span is stuck on and the key that gets out — `v`, which lands on a span the
reviewer can review. Nothing is quietly redirected to a temporary copy of history: the difftool
over a historical span compares its two pinned commits, so it never shows work the span does not
contain, and the preview has no `you` section, because a historical span has no working tree in it.

`V` opens the span picker: two columns, BASE and HEAD, holding a pending checkpoint each. `Space`
sets the end under the cursor, `Enter` applies the pair, and nothing changes before `Enter` — the
lines under the columns already say what the pair resolves to and whether it would be read-only,
which is what makes choosing a historical range safe rather than a negotiation with `Esc`. Both
columns offer the submissions by alias (the newest three as `Review -1`…`Review -3`, older ones by
index), then `Commit…` and `Ref…`; only the base offers the changeset base, only the head offers the
working tree, and `HEAD` is offered nowhere: beside `Working Tree` it would present two
similar-looking current targets when only one of them can be edited. `u` and `f` set the unreviewed
and full-changeset presets, `Tab` switches columns, `Esc` cancels.

The two drills are lists, not a history view. `Commit…` shows subject, short id and age; typing
filters it; a typed revision is taken directly, because the list is a window and history is not, and
a typed id git cannot resolve is refused while the list is still on screen. `Ref…` groups local
branches, remote refs, tags and other refs under headings, shows the name a reviewer would type and
keeps the full `refs/...` name in the checkpoint: a branch and a tag called `main` are two different
choices, and drift has to be watched on the one that was meant.

A ref endpoint is pinned when chosen, and stays pinned for the session. While a ref-backed endpoint is
active the session re-resolves it at the cheap points — on a timer, and after an editor or difftool
closes — and reports movement without acting on it:

```text
⚠ probe moved bb0f343 → 43915ed  [r] refresh
```

The banner is a row above the shortcut bar rather than a status line, because a status line is where
the last keystroke went, and a reviewer who marked a file would otherwise clear the warning by typing.
It is full width rather than a row of the list column for the same reason: in a split screen the list
column is narrow, and a warning that loses its key to an ellipsis warns about nothing. `r` re-pins
every drifted endpoint to where its ref points now, recomputes the span, and reports which refs moved
and how many reviewed marks stopped applying — marks are keyed on the file's diff within the span, so
the marks whose diff changed match nothing and drop out, which is the honest outcome rather than a
reset done for its own sake. Ignoring the banner is legitimate: the comparison the reviewer started is
the comparison they keep. A ref that no longer resolves is not drift — there is nothing to refresh to
— and the span stays on its pin.

Suggested bindings:

```text
j/k      navigate the region that holds the keys
Tab      move the keys around the ring: file tree, diff where there is room for one, changeset box
         (shift-tab the other way); f and m name a region from wherever the keys are
h/l      fold and unfold the directory under the cursor (left and right arrows do the same);
         h from a file or a closed directory moves up to the directory holding it
c        fold the whole tree, and open it again
Enter    activate the row: difftool for a file, fold/unfold for a directory, the decision
         below for ABOUT.md or a thread, collapse/expand for the Threads heading,
         new-thread prompt for + new thread…
d        open the difftool for the selected row: for a file, or for a directory every file
         the span changed under it; and for a document only when the span changed it and it
         already existed where the span starts — otherwise the document opens in the editor,
         with a note naming the reason
e        open the selected row in the editor, whatever the span did
p        move the keys into the diff preview column (wide terminals); inert where the diff already
         has them. Where the terminal is too narrow for a second column it takes the screen for the
         diff instead, since there is no column to move into, and is inert there too
ctrl-f   page the preview down
ctrl-b   page the preview up
Space    toggle reviewed for a file row, or for every file under a directory row

In the preview column, after p:
j/k      scroll the diff a row; ctrl-d/ctrl-u half a page, ctrl-f/ctrl-b a page,
         gg the top, G the bottom
Enter    open the difftool on the file the pane is showing
Esc      hand the keys back to the region that had them, leaving the pane where it is in the file
Tab, f, m  move the keys to another region, as they do from the list
q        quit — from here as from anywhere else

a        open ABOUT.md
t        create a review thread
T        collapse/expand the thread list

v        step to the next span this session has been in: the span it opened on, the full
         and unreviewed presets, and any span chosen with V; from a read-only span, back to
         the last span you could review
V        open the span picker: pending base and head, applied together on enter
r        re-pin a ref endpoint that has moved (offered by the drift banner, which is only on
         screen when there is something to re-pin)
s        submit review (block / feedback / approve)
q        quit, from whichever region holds the keys
```

Submitting review may either occur within the TUI or through the CLI.

---

# 15. External Editor/Difftool Behavior

`git-pair` should not render or edit source code itself.

When launching an external process:

```text
git-pair TUI
    ↓
suspend/release terminal
    ↓
launch editor or difftool
    ↓
wait for process exit
    ↓
resume terminal
    ↓
refresh repository state
```

For direct editing:

```bash
# the editor is whatever git would run: GIT_EDITOR, core.editor, VISUAL, EDITOR
$(git var GIT_EDITOR) path/to/file
```

For diff review, delegate to Git's configured difftool wherever possible.

A typical operation might correspond to:

```bash
git difftool <resolved-span> -- path/to/file
```

The session holds its alternate screen across the handoff, so the tool paints over the session
rather than over the shell: no command history flashing past while the editor starts, and no
`vimdiff` exit message left behind over the prompt afterwards. On return the session clears the
screen it shares with the tool and redraws; the screen the user was on, and its scrollback,
stays untouched.

---

# 16. Local Review Progress

The TUI allows each changed file to be marked:

```text
○ unreviewed
✓ reviewed
```

A directory row carries the same mark one level up, and `Space` on it sets every file under it. It
is a shortcut over the file-level state rather than a new kind of state: the row shows `✓` only when
its files all agree and counts what is left when they do not, the files a fold has hidden are marked
with the ones on screen, and nothing is recorded about the directory itself — so marks keep their
existing keys, and clearing every mark under a directory is remembered exactly the way clearing the
same files one by one is.

This state is **not** part of the durable review artifact.

For MVP it may be in-memory only.

If the underlying diff for a file changes after it was marked reviewed during the current session, `git-pair` should ideally reset it to unreviewed:

```text
✓ reviewed
↓ file changes
○ unreviewed
```

This is desirable but may be implemented after the minimal TUI if necessary.

Longer-term, reviewed hunks/sections may also be individually foldable/markable, but file-level state is sufficient for MVP.

---

# 17. Diff Span Model

Review span is a first-class concept.

There are two common questions:

1. What does the whole changeset contain?
2. What happened after a particular review submission?

The span model should remain simple and use ordinary Git commit ranges.

## 17.1 Full changeset

Default:

```bash
git pair diff
git pair review open
```

means:

```text
changeset.base ... HEAD
```

where `changeset.base` comes from:

```yaml
base: ...
```

## 17.2 `--unreviewed`

Example:

```bash
git pair diff --unreviewed
git pair review open --unreviewed
```

Means:

```text
latest-review.commit .. HEAD
```

It answers:

> What has happened since I submitted my most recent review?

That is the reviewer's question. The author's question — "what did the reviewer just tell me?" — is answered by `git pair change feedback` (§9.3), which shows the submission itself rather than what came after it. Immediately after a submission the two differ completely: `--unreviewed` is empty, because the submission is the newest commit.

This deliberately includes the removal or modification of review-added lines.

That behavior is useful because it makes resolution explicit.

Example review:

```diff
+ // Please use a transaction here
  foo()
```

Agent response:

```diff
- // Please use a transaction here
- foo()
+ transaction(() => foo())
```

The erased review comment visibly indicates that the agent consumed/resolved it.

For a follow-up question:

```diff
  // Can you use a transaction here?
+ // Agent: but we're already in a transaction from the outer scope.
  foo()
```

the surviving original comment plus agent-added response naturally represents an unresolved/continuing discussion.

## 17.3 `--since-review`

Examples:

```bash
git pair diff --since-review
git pair diff --since-review=-1
git pair diff --since-review=-3
git pair diff --since-review=0
```

Semantics:

```text
--since-review
    same as --since-review=-1

--since-review=-1
    latest review commit .. HEAD

--since-review=-3
    third-most-recent review commit .. HEAD

--since-review=0
    first review commit .. HEAD
```

Indexes correspond to `git pair review history`.

## 17.4 Head checkpoints

A span is two checkpoints — a base and a head — and both can be named:

```text
Changeset Base    merge-base(base, HEAD)              base end only
Working Tree      HEAD, pinned when the span is resolved   head end only
Review N          a submission, N chronological        either end
Commit <sha>      immutable                            either end
Ref <name>        pinned to the commit it points at    either end
```

`HEAD` is deliberately not offered as a checkpoint: beside `Working Tree` it presents two
similar-looking "current" targets when only one of them can be edited.

The head decides the mode:

| Head         | Mode       | Diff | Edit code | Edit review artifacts | Mark reviewed | Submit review |
| ------------ | ---------- | ---: | --------: | --------------------: | ------------: | ------------: |
| Working Tree | live       |  yes |       yes |                   yes |           yes |           yes |
| anything else | historical |  yes |        no |                    no |            no |            no |

A ref used as the base with a working-tree head stays live: pinning an endpoint is not the same
thing as being historical.

Refs keep their identity. The name the reviewer chose stays on screen, the span diffs against the
commit it pointed at when chosen, and later movement is reported rather than followed — following a
branch mid-review would silently change what the already-reviewed files meant. Advancing a pinned
checkpoint takes an explicit refresh, which recomputes the span; reviewed marks are keyed on
commits, so a mark that no longer applies simply does not come back.

`git pair diff` and `git pair review open` accept both ends of a span: `--base-review`, `--base-commit` and
`--base-ref` name the start, `--head-review`, `--head-commit` and `--head-ref` the end. `review open`
opens read-only when one of the head flags names the head; naming only the base leaves the span live,
including when that base is a ref pinned at selection.

`--unreviewed` and `--since-review` name the start too, spelled as work left to do, so they are
mutually exclusive with the `--base-*` flags rather than overridden by them. `--base-review` with no
value means `-1`, as the other review flags do. Both families feed the one span resolver, so
`--base-review=0 --head-review=1` and `--since-review=0 --head-review=1` are the same two commits.

There are no mixed span semantics in the MVP.

The same range applies uniformly to:

-   source files,
-   `ABOUT.md`,
-   review threads,
-   other review artifacts.

---

# 18. Review Boundary Semantics

A review submission is itself the review boundary.

Example:

```text
A -- R1 -- B -- C -- R2 -- D
```

Where:

```text
A   implementation
R1  review submission
B/C author response
R2  next review submission
D   next response
```

For review-relative spans:

```text
--since-review=0
    R1..HEAD

--since-review=-1
    R2..HEAD
```

This provides conversational semantics:

```text
author implementation
    ↓
human review commit
    ↓
author response diff
    ↓
human review commit
    ↓
author response diff
```

No attempt should be made to semantically subtract review comments, direct edits, or other human-authored hunks.

---

# 19. Surviving Review Additions

One consequence of using:

```text
review.commit .. HEAD
```

is that review additions left completely untouched by the author will not appear in the next diff.

Example:

Review adds:

```ts
// Please use a transaction here.
foo();
```

If the author leaves this untouched, it disappears from the `review.commit..HEAD` diff because nothing changed.

To prevent forgotten feedback, `git-pair` must implement a Git-native diagnostic.

## 19.1 Definition

A surviving review addition is a line introduced by the **most recent review submission** that still exists unchanged at current `HEAD`.

Examples may include:

-   inline review comments,
-   direct reviewer code edits,
-   additions to `ABOUT.md`,
-   review thread text.

The diagnostic should operate on additions from the latest review commit only.

## 19.2 `git pair change ready`

Before allowing the author to mark the changeset ready, run the surviving-review-additions check.

If surviving additions exist:

-   print their file/location/content,
-   fail with non-zero exit status,
-   do not create the ready marker.

Example:

```text
Cannot mark changeset ready.

2 additions from review 8ab932f still survive unchanged:

src/booking/service.ts:84
  // Please use a transaction here

src/booking/service.test.ts:131
  // What happens if these execute concurrently?

Review these additions before marking the change ready.
```

The author may explicitly override:

```bash
git pair change ready --allow-surviving-review-additions
```

The command must remain fully non-interactive.

This forces an agent to consciously inspect surviving review material rather than accidentally returning unchanged feedback to the reviewer.

## 19.3 `git pair change complete`

The same diagnostic must run before completing a changeset.

If surviving additions remain, `git pair change complete` must fail unless the owner explicitly overrides:

```bash
git pair change complete --allow-surviving-review-additions
```

This provides a final safety check before archival and squash/merge.

## 19.4 Rationale

The mechanism intentionally avoids requiring special review-comment syntax.

Git itself already knows which lines were added by the review commit.

The invariant is:

> Before advancing past the latest review boundary, every addition introduced by that review must either have changed/disappeared or be explicitly acknowledged as intentionally surviving.

---

# 20. Direct Human Code Edits

During a review, the human may directly edit the current implementation.

Those edits are committed as part of the review submission.

If the agent accepts the direct edit unchanged, it may trigger the surviving-review-additions diagnostic because the review-added lines still survive.

That is intentional.

The agent must consciously decide whether to:

-   retain the edit and use the override,
-   modify the edit,
-   remove it,
-   otherwise resolve it.

Do not attempt to distinguish review comments from reviewer-authored implementation code in MVP.

---

# 21. Stacked Branch Support

Each stacked branch has its own independent changeset.

Example:

```text
main
  └─ booking-transaction
       └─ booking-transaction-tests
            └─ booking-transaction-ui
```

Changeset metadata:

```text
booking-transaction:
  base: main

booking-transaction-tests:
  base: booking-transaction

booking-transaction-ui:
  base: booking-transaction-tests
```

Each branch has:

-   its own `ABOUT.md`,
-   its own threads,
-   its own review history,
-   its own review outcome,
-   its own review archive ref.

There is no shared review content between stacked branches in MVP.

Stack relationships only influence the configured base ref.

---

# 22. Agent Contract

Agents should interact with `git-pair` through stable non-interactive commands rather than manually interpreting the marker and trailer format.

Primary agent commands:

```bash
git pair change init --base <ref>
git pair status --json
git pair diff
git pair change ready
git pair change unready
git pair change wait --json
git pair change feedback
git pair change complete
```

Agent behavior:

1. initialize changeset,
2. maintain `ABOUT.md`,
3. implement normally,
4. commit implementation using ordinary Git,
5. run `git pair change ready`,
6. if surviving review additions cause failure, inspect and consciously resolve or explicitly retain them,
7. run `git pair change wait` — with `--fetch` when the reviewer works in another clone — until it reports an actionable state,
8. read the submission with `git pair change feedback`: threads, `ABOUT.md` edits and any code the reviewer edited directly,
9. address blocking feedback,
10. update code and discussion documents as appropriate,
11. commit implementation changes normally,
12. run `git pair change ready` again,
13. once the reviewer's approval stands at `HEAD`, run `git pair change complete` to archive it.

`git pair status --json` remains the way to check state without blocking. `git pair diff --unreviewed` is the reviewer's span command; an author consuming a newly submitted review uses `git pair change feedback`.

When the author needs to keep implementing after handing off, `git pair change unready` (§9.6) withdraws
the offer before that work starts. It succeeds when there is nothing to withdraw, so an agent may run
it unconditionally rather than branching on state.

An agent must **not approve its own work**.

Approval remains a reviewer action. Completing a changeset is the owner's action and is not
approval: it archives the head the reviewer approved, and the merge that finishes the changeset
stays with the human.

---

# 23. Review Outcomes

## Block

```text
Review-Outcome: block
```

Meaning:

> Changes are required before integration.

Agent should address the review and return the changeset to `ready`.

## Feedback

```text
Review-Outcome: feedback
```

Meaning:

> Feedback exists but is non-blocking.

Integration is permitted once other requirements pass.

## Approve

```text
Review-Outcome: approve
```

Meaning:

> Human review accepts the current implementation.

Integration is permitted once CI/policy passes.

An implementation commit after an approval leaves the approval as the state, and the head no
longer carries what was accepted: `git pair change complete` refuses to archive it (§9.5), and
`git pair change ready` offers the new head for review.

---

# 24. Queue and Notifications

`git pair review queue` provides a deterministic query for actionable review work.

This enables future automation such as:

-   desktop notifications,
-   Zulip/Slack notifications,
-   cron jobs,
-   agent supervisors,
-   developer dashboards.

The machine-readable interface should be sufficient for external automation:

```bash
git pair review queue --json
```

`git-pair` itself does not need to implement notifications in MVP.

The author's side of notification is `git pair change wait` (§9.4): it polls local review state, and with `--fetch` it polls through ordinary `git fetch`, so a review pushed from another clone reaches the author without a forge integration, a webhook, or a long-lived service. Waiting is a command an author or agent runs, not a daemon `git-pair` operates.

---

# 25. GitHub / Forge Role

GitHub or another forge remains useful for:

-   canonical remote hosting,
-   CI,
-   branch protection,
-   required checks,
-   merge queues,
-   security scanning,
-   deployment,
-   squash merging,
-   human collaboration outside this workflow.

Detailed code review does not need to occur in the forge.

The desired integration model is:

```text
local review state
    ↓
push normal branch/review history
    ↓
CI / policy on forge
    ↓
squash merge
    ↓
clean main history
```

while:

```text
refs/reviews/*
```

preserves the detailed human–agent development/review history.

---

# 26. MVP Non-Goals

The MVP should explicitly not attempt to:

-   implement a source-code editor,
-   implement a full custom diff renderer,
-   replace Vim/Neovim,
-   replace Git difftool,
-   replace Git,
-   replace GitHub/GitLab,
-   create or manage pull requests,
-   merge branches,
-   squash branches,
-   run coding agents,
-   run CI/CD,
-   implement inline-comment databases,
-   implement GitHub-style comment anchoring,
-   persist per-file review checkmarks permanently,
-   model multi-reviewer permissions,
-   model multi-author review semantics,
-   model complex stacked-branch graphs,
-   semantically distinguish human code edits from human comments,
-   implement notifications directly,
-   implement remote review-ref enforcement initially.

---

# 27. Likely Post-MVP Features

Potential later enhancements include:

## Review progress

-   persistent file review state,
-   hunk-level reviewed/unreviewed state,
-   fold reviewed hunks,
-   jump to next unreviewed hunk,
-   automatic invalidation when hunks change.

## Vim/Neovim integration

A review UI inspired by `vimdiff` with:

-   changed-file navigator,
-   regular Vim folding,
-   ability to fold changed/reviewed hunks,
-   direct editing of current-side working-tree buffers,
-   review progress indicators.

## Multi-user support

Possible future selectors:

```bash
git pair diff --since-review=-1 --reviewer=david
```

and reviewer-specific queues.

Do not design the MVP around this yet.

## Global queue

Repository registry and:

```bash
git pair review queue --global
```

## Remote archive enforcement

-   pre-push hooks,
-   server-side validation,
-   automatic pushing of `refs/reviews/*`,
-   protection against destructive rewrites before archival.

## Forge projection

Automatically project:

```text
ABOUT.md
```

into a GitHub/GitLab PR description while keeping the repository version as the source of truth.

---

# 28. Initial Keybindings for TUI

Suggested defaults:

```text
j/k      move through the region that holds the keys
Tab      move the keys around the ring: files, diff, changeset section (f and m name one)
Enter    activate the selected row: diff a file, diff or read a changeset document
         (whichever gives a real comparison), collapse the thread list, create a thread
d        open the difftool for the selected row, falling back to the editor with a note
e        edit the selected file or document
p        move the keys into the diff preview column; inert where the diff already has them.
         Where there is no second column, it takes the screen for the diff
ctrl-f   page the preview down
ctrl-b   page the preview up
Space    mark file reviewed/unreviewed

a        open ABOUT.md
t        create thread
T        collapse/expand the thread list

v        step through the spans this session has been in:
         opened-on, full changeset, unreviewed, then any span chosen with V;
         from a read-only span, back to the last span that was reviewable
V        span picker: BASE and HEAD columns, space to choose an end, enter to apply;
         Commit… and Ref… open searchable lists, u and f are the two presets
r        re-pin drifted ref endpoints, offered by ⚠ <ref> moved <a> → <b>  [r] refresh

s        submit review
q        quit
```

The exact bindings may evolve.

---

# 29. MVP Success Criteria

The MVP is successful if the following workflow works cleanly:

```bash
git switch -c booking-transaction

git pair change init --base main
```

Agent:

-   implements feature,
-   populates `ABOUT.md`,
-   commits implementation,
-   runs:

```bash
git pair change ready
```

Human:

```bash
git pair review queue
git pair review open
```

Within the review:

-   traverses changed files,
-   opens files in Vim/vimdiff,
-   directly edits implementation where useful,
-   adds normal source comments,
-   edits `ABOUT.md`,
-   creates Markdown review threads,
-   marks files reviewed.

Then:

```bash
git pair review submit --block
```

Agent:

-   waits for the review:

```bash
git pair change wait --fetch --json
```

which exits once the submission makes the changeset actionable and names the review commit,

-   reads what the reviewer said:

```bash
git pair change feedback
```

which shows the submission itself: the threads, the `ABOUT.md` edits, and the code the reviewer edited directly,

-   addresses review comments and direct edits,
-   responds in code/ABOUT/threads,
-   commits changes.

If any additions from the review submission still survive unchanged:

```bash
git pair change ready
```

must fail and show them.

The agent must consciously resolve them or explicitly acknowledge them with:

```bash
git pair change ready --allow-surviving-review-additions
```

Once ready again, the human runs:

```bash
git pair review open --unreviewed
```

and sees the changes made after the prior review submission, including explicit deletion or modification of prior inline review feedback.

Human approves, and the owner completes the changeset:

```bash
git pair review submit --approve
git pair change complete
```

`git pair change complete` runs the same surviving-review-additions safety check before archival, and
records no commit: it anchors the chain and writes the archive ref at the approved head.

At this point:

-   the complete unsquashed history is reachable via a review archive ref,
-   the branch can safely be pushed and squash-merged,
-   `main` can retain a single clean feature commit,
-   the detailed review history remains recoverable.

---

# 30. Product Thesis

`git-pair` treats Git itself as the protocol for agent-era peer review.

The codebase contains the review context. Git commits establish review boundaries. Markdown provides natural high-level conversation. Existing editors and difftools remain the code-review surface. Dedicated refs preserve the complete review history without forcing that noise into `main`.

The tool's role is deliberately narrow:

> **Coordinate the review lifecycle, make the right Git state easy to understand, and remove the ceremony between an authoring agent and a human reviewer.**
