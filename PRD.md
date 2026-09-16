# gitpr MVP Requirements

## 1. Overview

`gitpr` is a local-first peer-review orchestration tool designed primarily for human–coding-agent workflows.

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

`gitpr` should delegate existing functionality wherever possible.

It should use:

-   Git for versioning, commits, branches, diffs, and refs.
-   Git's configured editor (`GIT_EDITOR`, `core.editor`, `$VISUAL`, `$EDITOR`) for text editing.
-   configured Git difftools for code review.
-   GitHub, GitLab, Forgejo, etc. only as optional remote/CI/merge systems.

`gitpr` should not implement its own source-code editor or full diff renderer for the MVP.

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

A Git commit produced through `gitpr review submit`.

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

The directory name should normally derive from the branch name.

For branch names containing `/`, the MVP may normalize them into a filesystem-safe slug.

Example:

```text
branch:
feature/booking-transaction

changeset:
changesets/feature-booking-transaction/
```

The implementation should keep the branch-to-changeset naming rule deterministic.

An override mechanism may be added later but is not required for MVP.

---

# 5. Changeset Metadata

Each changeset contains:

```text
CHANGESET.yaml
```

The minimum required metadata is:

```yaml
base: main
```

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
gitpr change ...
gitpr review ...
```

Shared inspection commands remain top-level.

Target MVP structure:

```text
gitpr
├── change
│   ├── init
│   ├── ready
│   ├── feedback
│   └── wait
│
├── review
│   ├── open
│   ├── about
│   ├── thread
│   ├── submit
│   ├── history
│   ├── queue
│   └── close
│
├── status
└── diff
```

---

# 9. Author Commands

## 9.1 `gitpr change init`

Initializes review scaffolding for the current branch.

Example:

```bash
gitpr change init --base main
```

Creates:

```text
changesets/<current-changeset>/
  CHANGESET.yaml
  ABOUT.md
```

`CHANGESET.yaml`:

```yaml
base: main
```

Requirements:

-   determine current Git branch,
-   create deterministic changeset directory,
-   create metadata,
-   create `ABOUT.md`,
-   accept `--base`,
-   should be safe/idempotent where practical,
-   should not destroy existing changeset data.

For a stacked branch:

```bash
gitpr change init --base booking-transaction
```

## 9.2 `gitpr change ready`

Signals that the current implementation is ready for human review.

Requirements:

-   changeset must exist,
-   working tree should be clean,
-   `ABOUT.md` should exist,
-   run the surviving-review-additions diagnostic described below,
-   fail non-interactively by default if surviving review additions are detected,
-   create a GitPR lifecycle marker commit only if validation succeeds or an explicit override is supplied,
-   make the branch discoverable by `gitpr review queue`.

Suggested commit:

```text
gitpr: ready booking-transaction
```

Include machine-readable Git trailers, e.g.:

```text
GitPR-State: ready
GitPR-Changeset: booking-transaction
```

Readiness applies to the exact implementation state represented by the marker.

Any later implementation commit makes the previous ready marker stale and returns the effective state to `working`.

### Surviving review additions

Before marking the changeset ready, `gitpr` must inspect additions introduced by the most recent review submission.

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
  gitpr change ready --allow-surviving-review-additions
```

The command must remain fully non-interactive.

The explicit override is:

```bash
gitpr change ready --allow-surviving-review-additions
```

This is intended for cases where review-added code or comments are deliberately retained.

The check applies only to additions from the **most recent review submission**, not all historical review additions.

## 9.3 `gitpr change feedback`

Shows what the most recent review submission told the author, by showing the submission itself.

```bash
gitpr change feedback
```

Requirements:

-   diff `latest-review^..latest-review`, using ordinary Git diff output,
-   include the review's changeset artifacts (threads, `ABOUT.md` edits) and any direct code edits the reviewer made,
-   support `--stat` and `--name-only`,
-   exit non-zero with a clear message when the changeset has no review submission yet.

This is the author's answer to "what did the reviewer just tell me?".

`gitpr diff --unreviewed` (§17.2) answers a different question — "what has changed after the review I last read?" — and is the reviewer's command. It is usually empty right after a submission, because the submission is then the newest commit on the branch. An author who has just been told a review exists reads it with `gitpr change feedback`, not with `gitpr diff --unreviewed`.

## 9.4 `gitpr change wait`

Blocks until a reviewer makes the changeset actionable.

```bash
gitpr change wait
gitpr change wait --fetch --interval 30s
gitpr change wait --fetch --timeout 2h --json
```

Requirements:

-   exit successfully when the effective state moves from `ready` to `blocked`, `feedback`, `approved`, or `closed`,
-   report immediately, without waiting, when the changeset is already in one of those states,
-   exit non-zero when the changeset is `working`: the author is not waiting on anyone,
-   poll local GitPR state by default,
-   with `--fetch`, run `git fetch` against the configured remotes before each check, so a review submitted in another clone becomes visible through `refs/remotes/...`,
-   `--interval` sets the polling interval (default 10s),
-   `--timeout` gives up after a duration instead of waiting forever,
-   `--json` prints `previous_state`, `state`, `review_commit`, `ref`, `fetches`, `waited_seconds`, `timed_out` and `next_action`, with state names spelled as `gitpr status --json` spells them,
-   a wait that ends because of `--timeout` exits non-zero,
-   waiting never writes to the repository: no commits, no refs, no index changes.

The command is non-interactive and must be safe for an agent to run unattended.

`--fetch` is ordinary Git fetching. Forge notifications, webhooks and forge API polling are out of scope (§25): a review reaches the author by being pushed to the repository, like any other commit.

The author-side loop is therefore:

```bash
gitpr change ready
git push origin my-feature
gitpr change wait --fetch --json   # exits when a reviewer has acted
gitpr change feedback               # read what they said
```

---

# 10. Reviewer Commands

## 10.1 `gitpr review open`

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

## 10.2 `gitpr review about`

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

`git var GIT_EDITOR` answers all five, so gitpr asks for it and consults 3–5 only when git
cannot answer. The value is a command line, so `code --wait` is a program plus its flags, as git
treats it.

Example:

```bash
gitpr review about
```

The TUI should temporarily relinquish terminal control while the editor runs and resume/redraw after it exits.

## 10.3 `gitpr review thread`

Creates or opens a focused review-thread Markdown file.

Example:

```bash
gitpr review thread "concurrency tests"
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

## 10.4 `gitpr review submit`

Completes the current human review iteration.

Supported outcomes:

```bash
gitpr review submit --block
gitpr review submit --feedback
gitpr review submit --approve
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

GitPR-Outcome: block
GitPR-Changeset: booking-transaction
```

or:

```text
review: approve booking-transaction

GitPR-Outcome: approve
GitPR-Changeset: booking-transaction
```

Immediately after successful review submission, update the changeset's review archive ref to the resulting exact `HEAD`.

## 10.5 `gitpr review history`

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

Only commits explicitly identified as GitPR review submissions count as reviews.

Ordinary Git commits do not.

## 10.6 `gitpr review queue`

Shows changesets currently ready for human review.

Default scope: current repository.

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
gitpr review queue --global
gitpr review queue --json
```

A global repository registry may eventually live in:

```text
~/.config/gitpr/config.toml
```

with commands such as:

```bash
gitpr repo add ~/dev/cadence
gitpr repo remove ~/dev/cadence
gitpr repo list
```

Global repository management is useful but may be deferred if needed.

The queue command should have a stable machine-readable form suitable for automation and notifications.

## 10.7 `gitpr review close`

Finalizes the review lifecycle before squash/merge.

Responsibilities:

1. verify working tree is clean,
2. verify the latest effective review outcome permits integration,
3. run the surviving-review-additions diagnostic,
4. require explicit acknowledgement if surviving additions remain,
5. ensure the complete current branch history is archived,
6. create or update a durable final review ref,
7. mark the changeset logically closed,
8. print the resulting archive ref and integration readiness.

It must **not** merge, push, or squash by default.

Example output:

```text
Closed changeset booking-transaction

Review archive:
  refs/reviews/archive/booking-transaction/91bf204

Safe to squash/merge.
```

If surviving review additions remain, closing must fail unless explicitly overridden:

```bash
gitpr review close --allow-surviving-review-additions
```

`approve` and `close` are intentionally separate concepts:

```text
approve
    human judgment

close
    review lifecycle / archival operation
```

---

# 11. Top-Level Commands

## 11.1 `gitpr status`

Displays the effective state of the current changeset.

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

Review archive:
  refs/reviews/booking-transaction

Uncommitted changes: no
```

Provide:

```bash
gitpr status --json
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

## 11.2 `gitpr diff`

Displays or launches the configured diff for the selected logical review span.

The command should understand changeset base metadata and review boundaries.

It should support file-specific invocation:

```bash
gitpr diff src/booking/service.ts
```

and span options described below.

`gitpr diff` and its span options serve review. An author who wants to read a review that was just submitted uses `gitpr change feedback` (§9.3) instead.

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
close
```

The author's side of that loop is `gitpr change ready`, then `gitpr change wait` to learn that a reviewer has acted, then `gitpr change feedback` to read the submission before addressing it.

Possible effective states:

```text
WORKING
READY
BLOCKED
FEEDBACK
APPROVED
CLOSED
```

Avoid maintaining a fragile mutable state variable where possible.

Prefer deriving effective state from GitPR lifecycle commits and repository state.

Important safety property:

> Any implementation commit after a ready/review/approval marker invalidates that marker for the current HEAD.

Example:

```text
review: approve
agent changes implementation
```

The branch must no longer be considered approved.

---

# 13. Review Archive Refs

A review submission should update a durable local ref:

```text
refs/reviews/<changeset>
```

Example:

```text
refs/reviews/booking-transaction
```

pointing to the exact current `HEAD`.

This keeps the entire implementation/review/fix chain reachable from Git garbage collection.

When closing a changeset, the implementation may optionally create an immutable archival ref:

```text
refs/reviews/archive/<changeset>/<identifier>
```

Example:

```text
refs/reviews/archive/booking-transaction/91bf204
```

The exact immutable naming convention can be finalized during implementation.

The fundamental invariant is:

> Before the review stack can be considered safely squashable, the complete final unsquashed stack must remain reachable from a dedicated review ref.

Remote propagation of review refs may be added via push configuration/hooks later.

The MVP should ensure local preservation first.

---

# 14. Interactive Review TUI

The TUI should be intentionally small.

Example:

```text
booking-transaction-tests
base: booking-transaction
span: unreviewed

○ src/booking/service.test.ts
✓ src/booking/fixtures.ts
○ src/booking/concurrency.test.ts

2 / 3 reviewed

ABOUT.md
▾ Threads
    concurrency-tests.md
    locking.md
    + new thread…
────────────────────────────────
```

The changed files and the changeset documents form one navigable list rather than separate
modes: `j` continues from the last file into `ABOUT.md` and the threads nested under their
heading, and creating a thread is one of the entries in that list instead of a separate
prompt-plus-browse flow. The heading collapses and counts what it hides — `▸ Threads (2)`
collapsed, plain `▾ Threads` when they are on screen — so a changeset with many threads stays
scannable. A rule separates the list from the shortcut bar. The reviewed counter separates the two
blocks — above it the files the diff touched, below it the documents the review is made of — which
is also what `Tab` skips between. A row that changed in the span keeps its file row as well as its
place in the section below: the list is what the diff did, the section is the shortcut to read it.

On a terminal at least 100 columns and 16 rows, the list shares the screen with a preview column:
the diff the common span made to the selected file, paged with `ctrl-f`/`ctrl-b`. It prints git's
own output, colour included, and adds only what a fixed-width column cannot decline to do: the line
number git itself put in the hunk header, a break where a line is too wide, and the spaces a tab
advances to. It does not fold, group, filter, or renumber hunks, and it does not choose
colours, which is the shape PRD §3's refusal to build a diff renderer leaves: the pane is
for glancing, and reading a diff means opening it. The frame fills the terminal — the row
area holds the window's height and every row is padded to its width — so the shortcut bar
sits against the bottom edge rather than under a short list.

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
j/k      navigate the whole list
Tab      switch between the file section and the changeset section
Enter    activate the row: difftool for a file, the decision below for ABOUT.md or a
         thread, collapse/expand for the Threads heading, new-thread prompt for
         + new thread…
d        open the difftool for the selected row: always for a file, and for a document
         only when the span changed it and it already existed where the span starts —
         otherwise the document opens in the editor, with a note naming the reason
e        open the selected row in the editor, whatever the span did
p        show/hide the diff preview column (wide terminals)
ctrl-f   page the preview down
ctrl-b   page the preview up
Space    toggle reviewed for a file row

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
q        quit
```

Submitting review may either occur within the TUI or through the CLI.

---

# 15. External Editor/Difftool Behavior

`gitpr` should not render or edit source code itself.

When launching an external process:

```text
gitpr TUI
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

This state is **not** part of the durable review artifact.

For MVP it may be in-memory only.

If the underlying diff for a file changes after it was marked reviewed during the current session, `gitpr` should ideally reset it to unreviewed:

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
gitpr diff
gitpr review open
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
gitpr diff --unreviewed
gitpr review open --unreviewed
```

Means:

```text
latest-review.commit .. HEAD
```

It answers:

> What has happened since I submitted my most recent review?

That is the reviewer's question. The author's question — "what did the reviewer just tell me?" — is answered by `gitpr change feedback` (§9.3), which shows the submission itself rather than what came after it. Immediately after a submission the two differ completely: `--unreviewed` is empty, because the submission is the newest commit.

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
gitpr diff --since-review
gitpr diff --since-review=-1
gitpr diff --since-review=-3
gitpr diff --since-review=0
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

Indexes correspond to `gitpr review history`.

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

`gitpr diff` accepts `--head-review`, `--head-commit` and `--head-ref`. `gitpr review open` accepts
the same and opens read-only when one of them names the head.

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

To prevent forgotten feedback, `gitpr` must implement a Git-native diagnostic.

## 19.1 Definition

A surviving review addition is a line introduced by the **most recent review submission** that still exists unchanged at current `HEAD`.

Examples may include:

-   inline review comments,
-   direct reviewer code edits,
-   additions to `ABOUT.md`,
-   review thread text.

The diagnostic should operate on additions from the latest review commit only.

## 19.2 `gitpr change ready`

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
gitpr change ready --allow-surviving-review-additions
```

The command must remain fully non-interactive.

This forces an agent to consciously inspect surviving review material rather than accidentally returning unchanged feedback to the reviewer.

## 19.3 `gitpr review close`

The same diagnostic must run before closing a changeset.

If surviving additions remain, `gitpr review close` must fail unless the reviewer explicitly overrides:

```bash
gitpr review close --allow-surviving-review-additions
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

Agents should interact with `gitpr` through stable non-interactive commands rather than manually interpreting GitPR internals.

Primary agent commands:

```bash
gitpr change init --base <ref>
gitpr status --json
gitpr diff
gitpr change ready
gitpr change wait --json
gitpr change feedback
```

Agent behavior:

1. initialize changeset,
2. maintain `ABOUT.md`,
3. implement normally,
4. commit implementation using ordinary Git,
5. run `gitpr change ready`,
6. if surviving review additions cause failure, inspect and consciously resolve or explicitly retain them,
7. run `gitpr change wait` — with `--fetch` when the reviewer works in another clone — until it reports an actionable state,
8. read the submission with `gitpr change feedback`: threads, `ABOUT.md` edits and any code the reviewer edited directly,
9. address blocking feedback,
10. update code and discussion documents as appropriate,
11. commit implementation changes normally,
12. run `gitpr change ready` again.

`gitpr status --json` remains the way to check state without blocking. `gitpr diff --unreviewed` is the reviewer's span command; an author consuming a newly submitted review uses `gitpr change feedback`.

An agent must **not approve its own work**.

Approval remains a reviewer action.

---

# 23. Review Outcomes

## Block

```text
GitPR-Outcome: block
```

Meaning:

> Changes are required before integration.

Agent should address the review and return the changeset to `ready`.

## Feedback

```text
GitPR-Outcome: feedback
```

Meaning:

> Feedback exists but is non-blocking.

Integration is permitted once other requirements pass.

## Approve

```text
GitPR-Outcome: approve
```

Meaning:

> Human review accepts the current implementation.

Integration is permitted once CI/policy passes.

Any implementation commit after an approval invalidates approval for the new HEAD.

---

# 24. Queue and Notifications

`gitpr review queue` provides a deterministic query for actionable review work.

This enables future automation such as:

-   desktop notifications,
-   Zulip/Slack notifications,
-   cron jobs,
-   agent supervisors,
-   developer dashboards.

The machine-readable interface should be sufficient for external automation:

```bash
gitpr review queue --json
```

`gitpr` itself does not need to implement notifications in MVP.

The author's side of notification is `gitpr change wait` (§9.4): it polls GitPR state, and with `--fetch` it polls through ordinary `git fetch`, so a review pushed from another clone reaches the author without a forge integration, a webhook, or a long-lived service. Waiting is a command an author or agent runs, not a daemon `gitpr` operates.

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
local GitPR review
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
gitpr diff --since-review=-1 --reviewer=david
```

and reviewer-specific queues.

Do not design the MVP around this yet.

## Global queue

Repository registry and:

```bash
gitpr review queue --global
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
j/k      move through the list: files, then the changeset section
Tab      switch between the two sections
Enter    activate the selected row: diff a file, diff or read a changeset document
         (whichever gives a real comparison), collapse the thread list, create a thread
d        open the difftool for the selected row, falling back to the editor with a note
e        edit the selected file or document
p        show/hide the diff preview column
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

gitpr change init --base main
```

Agent:

-   implements feature,
-   populates `ABOUT.md`,
-   commits implementation,
-   runs:

```bash
gitpr change ready
```

Human:

```bash
gitpr review queue
gitpr review open
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
gitpr review submit --block
```

Agent:

-   waits for the review:

```bash
gitpr change wait --fetch --json
```

which exits once the submission makes the changeset actionable and names the review commit,

-   reads what the reviewer said:

```bash
gitpr change feedback
```

which shows the submission itself: the threads, the `ABOUT.md` edits, and the code the reviewer edited directly,

-   addresses review comments and direct edits,
-   responds in code/ABOUT/threads,
-   commits changes.

If any additions from the review submission still survive unchanged:

```bash
gitpr change ready
```

must fail and show them.

The agent must consciously resolve them or explicitly acknowledge them with:

```bash
gitpr change ready --allow-surviving-review-additions
```

Once ready again, the human runs:

```bash
gitpr review open --unreviewed
```

and sees the changes made after the prior review submission, including explicit deletion or modification of prior inline review feedback.

Human approves:

```bash
gitpr review submit --approve
gitpr review close
```

`gitpr review close` runs the same surviving-review-additions safety check before finalization.

At this point:

-   the complete unsquashed history is reachable via a review archive ref,
-   the branch can safely be pushed and squash-merged,
-   `main` can retain a single clean feature commit,
-   the detailed review history remains recoverable.

---

# 30. Product Thesis

`gitpr` treats Git itself as the protocol for agent-era peer review.

The codebase contains the review context. Git commits establish review boundaries. Markdown provides natural high-level conversation. Existing editors and difftools remain the code-review surface. Dedicated refs preserve the complete review history without forcing that noise into `main`.

The tool's role is deliberately narrow:

> **Coordinate the review lifecycle, make the right Git state easy to understand, and remove the ceremony between an authoring agent and a human reviewer.**
