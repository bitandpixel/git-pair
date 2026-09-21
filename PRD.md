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

## Durable record

The pair of Git refs `git pair integration record` (§11.4) writes for a changeset that landed:
`refs/git-pair/archive/<changeset>`, which retains the complete unsquashed branch/review history even
after the feature is squash-merged, and `refs/git-pair/integrations/<changeset>`, which names the
commit the work became. Both are created once and never moved, and neither exists while work is in
flight (§13).

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

The directory belongs to no branch. Which changesets a revision is working on is read from
content, and the reading is the same question everywhere: `status`, `review queue`, and a CI job
with two branches fetched all ask it the same way.

### Which changeset a revision is working on

The changesets on a revision are the `changesets/<id>/` directories present in its tree that the
integration branch's tree does not have.

- A directory that has reached the integration branch is landed work. It drops out with no
  integration ref, no branch name, and no dependence on whether the landing was a merge, a
  squash or a cherry-pick — the directory is in trunk either way.
- A directory that exists only here is work in progress, whichever branch line it sits on. That
  is what lets a parent branch and the child branched off it continue one changeset instead of
  the child inventing a second answer about the same work.
- Nothing asks which branch is checked out, so a detached HEAD, a CI checkout and a human's
  branch answer the same question. Branch names are not part of the durable data.
- The cost follows the directories on this revision, not the number of changesets the repository
  has ever had.

Where more than one directory survives, they are ordered by the branch's own history and not by
guesswork: the changeset whose directory this revision touched most recently comes first; a directory
named as another's `base:` is the parent of a stack, so it is not what the revision is working on. When
nothing orders them, the answer is **ambiguous**, and the command refuses, names every
candidate, and gives two ways out: `--changeset <id>` answers for one command, and
`git pair change use <id>` (§9.8) settles it for the branch. Two unrelated changesets on one
branch is a state only the author can settle, and picking one silently would read the wrong diff
base and offer the wrong diff to a reviewer — which is why a tie is a refusal rather than a
heuristic.

The **integration branch** is what "landed" is measured against. A `--default-branch <ref>`
flag states it outright — which is what CI passes, since a job that cloned with `init` and one
`fetch` has no recorded remote default to read. Otherwise git's own answer is used:
`refs/remotes/origin/HEAD`, as `git clone` records it, then a sole `origin/main` or
`origin/master`, then a local `main` or `master`. If none of those exists the command refuses
and names the flag: guessing here would make every directory on the revision look like work in
progress. No git config is read. Two clones of one repository must not disagree about what has
landed.

The consequence worth knowing: if someone merges your unlanded changeset into their branch and
lands *that*, your changeset reads as landed on your own branch too, because your directory is
now in the integration branch's tree. That is the rule working as intended — the work is in
trunk — and it is why a changeset's branch is expected to land its own content.

It runs the other way as well, which is the pruning rule. Deleting landed `changesets/<id>/`
directories from the integration branch makes those changesets unlanded for every branch that has not
merged the deletion, and they return with their review history attached. A repository that tidies trunk
should prune only what carries an integration ref (§13.2): that ref is what keeps the history reachable
once the branch is gone, and a changeset retired by trunk's tree alone comes back with the deletion. A
terminal record (§9.7) is a marker on a branch, so pruning trunk does not retire it either. This is
documented behaviour, not a bug to fix: the resurrection of an unrecorded landing is the rule answering
the question it was given.

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
```

`id` is the changeset ID, and it is the directory's name rather than a second opinion
about it: a file whose `id` disagrees with the directory holding it is an error to
correct, not a conflict to resolve. `base` is the ref the changeset's diff is measured
against, and for a stacked branch it names the changeset it sits on.

Those two keys are what every changeset has. There is no branch field, because the directory does
not belong to a branch (§4): recording one would put per-branch state in the durable data, and the
same commits would then answer differently depending on which branch happened to be checked out —
which is the disagreement the content rule exists to remove.

A third key appears only where an author put it. `ignores: <id> [<id>...]`, written by
`git pair change use` (§9.8), records that this changeset is the one its branch is working on and
that the named changesets are only sharing the branch with it. It is read when the changeset that
wrote it is a candidate, so it speaks about one branch: it is not a statement about the other
changesets, and it cannot change what another branch resolves to.

The ID does not change once the changeset has durable refs. Renaming one means moving the
directory and every ref under it, which is not something git-pair does silently; there is
no rename command and no automatic migration.

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

Shared inspection commands remain top-level. `git pair integration ...` is a third, and the one a
pipeline runs rather than a person: it takes SHAs and refs instead of a checkout, and writes no commit.

Target MVP structure:

```text
git-pair
├── change
│   ├── init
│   ├── use
│   ├── ready
│   ├── unready
│   ├── feedback
│   └── wait
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
├── diff
├── check
│
└── integration
    └── record
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

   Two branches whose names normalise alike are a collision when the directory from one is
   already committed on the line of development you are standing on, which is what a landed
   changeset left on the integration branch looks like. A directory you merely inherited from
   the branch you were created from is the same changeset seen from a second branch, so `init`
   reports it as already initialised instead of refusing a collision between a changeset and
   itself.
3. **It does not change.** The ID of a directory that exists is never rewritten, and a
   changeset that has durable refs keeps its ID for good (§5). Starting a *second* changeset on
   a branch that already carries one is allowed — that is what a stacked branch that begins its
   own work looks like — and `init` warns that the branch now holds two, because the tool has
   two directories to order and the order is rarely what the author meant. `git pair change use`
   (§9.8) is how the author settles it.

Deleting a changeset directory releases its ID only when the deletion is committed, since
retiring a changeset's notes is a commit and not a local edit.

Renaming the branch changes nothing: the directory is the identity, so the changeset resolves
from the branch it was renamed to, and reports that branch as the one in use.

`init` refuses to run on the integration branch. A changeset is measured against that branch,
so one started on it can never contain anything, and every directory it carries already counts
as landed. The refusal is a clarity guard: the rule itself makes such a changeset invisible,
and the message says `git switch -c <branch>` rather than leaving the author to notice.

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
-   write nothing outside the marker commit: no ref is created or moved while work is in flight, and
    the branch is what holds the history until landing (§13),
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

The tree is consulted at exactly one point in the lifecycle, and it is not here: `git pair check`
(§11.3) refuses a head whose reviewed content has moved since the review.

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

## 9.5 Handing the work on

Nothing in git-pair completes a changeset. The author lands the work with ordinary git and then tells
git-pair where it landed, and that last step is the only point in the lifecycle that writes a durable
ref (§13).

```bash
git pair check                                    # the gate a merge runs; its answer is `$?`
git switch main && git merge booking-transaction  # …or a squash, or a cherry-pick: git's choice
git pair integration record \
  --source 91bf204 --commit 4f2c81a --target main # §11.4, and the only write git-pair makes here
```

Three properties are worth naming, because they are what the previous shape of this chapter had
backwards:

-   **A review is a judgement; landing is a decision.** `approve` says the code is good enough. The
    author decides what to take forward and where, and git-pair records that decision instead of
    anticipating it. Nothing about a changeset is preserved before it lands, which is why there is no
    `change archive`: the head a later reader needs is the head the record names, and the record is
    written by the person who knows where the work went.
-   **A merge is not a git-pair operation.** It is not derived, reported as state, or gated (§12).
    `integration record` verifies facts about commits the author chose to make — that the head being
    recorded was reviewed (§11.4), that the landing is in the destination branch's history, that it is the
    commit which brought the changeset directory into it — and never reaches for a merge itself. The
    verifications are what make the record worth reading later; none of them is a decision about whether
    the work should have landed.
-   **The record is written once.** Both refs are created, never moved, and re-running the recorder
    with the same pair succeeds without changing anything (§13.3). A landing that needs correcting is
    corrected in git and recorded under a changeset id that has no record yet, not by moving a ref.

`approve` and landing are intentionally separate concepts:

```text
approve
    human judgment about the code

integration record
    the author's statement of where the work went
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
`git pair review history`, and because the newest marker is now the retraction, `git pair check`
(§11.3) refuses until a reviewer approves again.

A reviewer may still submit against an unready changeset — `review submit` accepts any state — which is
what keeps the gate back into `READY` reachable: surviving review additions (§19) are still enforced by
`change ready`.

The withdrawal is the marker commit and nothing else. It used to move the archive ref too, so that a
changeset read from its durable record after `git branch -D` would not report work as offered that its
author had taken back; the queue and the gate read the marker itself, and the only durable refs are the
pair landing writes. So the retraction lives as long as the branch does, and a withdrawal whose branch
has been deleted is unreadable. That is a real loss and the design accepts it (§13.1).

A changeset with an integration record refuses here, including when there is nothing to withdraw: a
changeset whose work has landed has no readiness to retract, and "nothing to withdraw" would be an
answer about the branch rather than about the work being finished.

`--json` prints `changeset`, `branch`, `base`, `state`, `was`, `recorded`, `unready_commit` and
`review_queue_visible`.

## 9.7 `git pair change abandon`

Records that the changeset will not be taken forward.

Two endings exist, and git-pair records both of them — differently, because they are different claims.
Landing the work (*merged*) is recorded by a pair of refs naming the reviewed head and the commit it
became (§11.4, §13), because only the person doing the landing knows where the work went. Abandoning
(*not coming back*) is recorded by a marker on the branch, because the author knows that the moment
they decide it and there is nothing to point at. Without a command for it, the only way to say so
would be to delete the branch, which destroys the history that explains why — and what that history is
worth is a different question for changeset nobody is going to land than for one somebody is.

So the ending is a marker and no ref: an abandoned changeset has no durable record, and its history
goes with its branch (§13.1). What git-pair will not do is maintain refs for work that never finished,
while work that did finished gets a record written once.

Checks, in order:

1. the changeset exists on the checked-out branch — a changeset whose branch is already gone cannot
   be abandoned, because abandoning is a decision about work that is still there, and post-merge
   bookkeeping is not a lifecycle act (§10.6 classifies a leftover directory on its own),
2. clean working tree — the marker is a commit,
3. the changeset has not already ended, in which case the command succeeds and records nothing.

Writes `git-pair: abandon <slug>` carrying `Review-State: abandoned` and `Review-Changeset: <slug>`.
The marker is the whole record of the ending, and it lives on the branch: `status` reads it from there,
the queue drops the changeset because of it, and the commands that move state refuse against it.

`abandoned` is not a sixth state. The changeset reports `WORKING`, which already means "not in
review, nothing owed", and the ending is reported beside the state as `abandoned` and
`abandoned_commit` in `status --json`. Splitting it this way keeps `state` — the field agents branch
on — at its five values, while still making a changeset that can never move again recognisable.

The ending closes the changeset, which is the difference from `change unready` (§9.6), which only
withdraws an offer for now. `change ready`, `change unready` and `review submit` refuse against an
abandoned changeset, and `git pair check` reports the ending as the reason the changeset is not
integration-ready. The refusal reads the branch's own commits, so it is not a reading of a ref somebody
might have failed to fetch — and it lasts exactly as long as the branch. A branch created later under
the same id starts clean, because with the branch gone nothing is left saying the earlier one ended,
and `change init` reserves an id only against the records that landed changesets leave behind (§13).

`--json` prints `changeset`, `branch`, `state`, `was`, `recorded` and `abandoned_commit`.

---

## 9.8 `git pair change use <changeset-id>`

Records which changeset a branch is working on, so that the branch stops being ambiguous.

A branch normally carries one unlanded changeset. It carries more when a sibling's branch is merged
into it, and when a branch created off a sibling starts its own work without `--base` naming that
stack. The rule (§4) orders what it can — the changeset whose directory this revision touched most recently
wins, a `base:` names
the parent of a stack — and refuses between the rest, because choosing one silently means reading
the wrong diff base.

```bash
git pair change use booking-transaction
```

It writes one line, `ignores: <other ids>`, into the **chosen** changeset's `CHANGESET.yaml`, and
commits that file on its own. The record belongs to the changeset that was chosen: clearing the
others' records instead would write into another changeset's directory, which would then be read as
part of this changeset's landing. The commit carries
`Review-Changeset: <id>` and no `Review-State`, because recording which changeset a branch is about
is not a lifecycle event — it must not move a changeset that is in review out of review. Like a
ready marker it is a review artifact rather than an implementation change, so the comparison that
decides integration (§11.3) ignores it.

Checks, in order:

1. `<changeset-id>` is a usable id (§9.1), else exit 2,
2. if the branch already resolves to that id, succeed and record nothing — the useful answer is
   "already", and a commit that changes nothing would be noise,
3. the id is among the candidates. A directory that is present but dropped by another changeset's
   record is refused by name, with the file and line to edit: the author can see that directory, so
   "there is no such changeset here" would be a dead end,
4. the record is simulated before it is written. If writing it would leave the branch undecided —
   another candidate already records a choice that names this one — refuse with exit 2 and name the
   line to remove. Contradicting records stay a refusal rather than becoming a newest-commit-wins
   rule, because which of two hand-edited files is newer is not something git-pair can know reliably,
   and the author can say outright which one they mean.

A recorded choice is data, so it travels with the branch: a branch created from this one inherits
the record, and the record only ever drops a changeset from consideration where the two would
otherwise be tied on that revision. An author who changes their mind edits the file — it is one
line, and the refusal message names it.

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
Review-Head: 8f21c0d4b7a5e9c3d0f4a6b8e1c2d3f4a5b6c7d8
```

or:

```text
review: approve booking-transaction

Review-Outcome: approve
Review-Changeset: booking-transaction
Review-Head: 8f21c0d4b7a5e9c3d0f4a6b8e1c2d3f4a5b6c7d8
```

`Review-Head` is the commit the submission spoke about: `HEAD` as the reviewer submitted, which is the
new commit's own first parent. It is recorded rather than left implicit because the implicit value is
the exact one a rebase changes — the rewritten review commit keeps its message, so the marker that
survives still names the head that is gone from the branch, and §11.3's ancestry condition refuses it.
Deriving the value from the graph instead would defeat the rule: after a rewrite the first parent is
itself rewritten, so the derived head would always be in the line.

A submission that names no head — written before this trailer existed, or by hand — is refused by the
gate rather than assumed, and the reason says the approval covers an unknown commit (§11.3).

The submission is the marker commit and nothing more. It used to move the archive ref onto the resulting
exact `HEAD`, so reviewed history stayed reachable without the branch; review writes no ref now, because
the head a later reader needs is the head the landing records (§11.4), and a ref written at review time is
a ref that has to be moved — and moved back — for work that may never land (§13.1).

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

A changeset directory whose branch is gone is classified rather than skipped, and the record is what
classifies it. A directory with no integration ref and no archive was never recorded, and says nothing —
which includes a clone that has never fetched `refs/git-pair/*` (§13.4): the queue reads local branches
and the refs that outlive them, so an unfetched namespace is silence here rather than an error. A
directory whose archive and base carry the same `changesets/<changeset>/` content landed and was recorded,
and says nothing. Anything else — recorded work whose archive is not in its base and on no branch — is
named in `skipped` with both SHAs, because that line is then the only surviving trace of the work.

A changeset with an integration ref (§13.2) has landed, and the queue has nothing to ask of it. It is
named in `skipped` with the commit it landed as rather than dropped in silence, because unlike a
trunk landing its branch is usually still here — and a landing outside the default branch is exactly
the case the tree rule cannot see, since the directory is still absent from trunk and reads as live
work until someone records where the change went.

A branch the rule cannot resolve is named there too, with its candidates and both ways out (§9.8).
A branch that is quietly missing from the queue is indistinguishable from a branch with nothing to
show, and the queue is where an author looks to find out why a branch is not in it.

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

note: skipped booking-transaction (integrated at 5556bc5)
```

The note is the record talking: `booking-transaction`'s branch may still be checked out and its directory
still absent from trunk, and it is the integration ref that says the queue has nothing to ask of it
(§13.2).

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

Example, in flight:

```text
Changeset: booking-transaction
Branch: booking-transaction
Base: main
State: APPROVED
Head: c18c9a7
Span: main...current
Reason: review 91bf204 (approve) is the newest commit

Latest review:
  outcome: approve
  commit: 91bf204  (20m ago)
  reviewed: a7f3c98
  history: 3 review(s) — `git pair review history`

Uncommitted changes: no
Next: `git pair check`, then merge into main with ordinary git, then `git pair integration record`
```

Nothing durable is reported, because nothing durable exists yet: the branch holds the history and no
git-pair ref has been written (§13.1). After `git pair integration record` (§11.4) the same branch
reads:

```text
Integrated:
  4f2c81a, reachable from main (`git pair integration record`)
  refs/git-pair/integrations/booking-transaction

Review archive:
  refs/git-pair/archive/booking-transaction
    points at: 91bf204
Next: integrated at 4f2c81a: nothing further is recorded for a changeset that has landed
```

Both families print together, because they are one record: the commit the work became, and the chain of
what it went through to get there. The record is also what makes the changeset readable with no branch
at all — `status --changeset <id>` after `git branch -D` says `Branch: none (read from the durable
record)` rather than failing.

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
        "commit": "91bf204",
        "reviewed_head": "a7f3c98"
    },
    "archive_ref": "",
    "archive_commit": "",
    "integrated": false,
    "default_branch": "main",
    "default_branch_commit": "9c41f0b",
    "default_branch_source": "sole-candidate"
}
```

`archive_ref` and `archive_commit` are the durable pair, and both are empty while work is in flight —
they are reported the way they are so a consumer sees one shape either way, and their presence is the
statement that the work landed. `integration_ref` and `integrated_commit` name the landing itself and
appear only with it (§13).

`latest_review.reviewed_head` is the commit that submission spoke about, from its `Review-Head`
trailer (§10.4) — the other end of the comparison `git pair check` makes when it refuses a rewritten
branch (§11.3). It is omitted when the marker names no head, which is itself the answer the gate
refuses on.

The three `default_branch` fields are the other half of the resolution. Which changeset a revision is
working on is a comparison against the integration branch (§4), so a run that reports nothing has
landed is doing so relative to a branch and a commit that the output would otherwise not mention — and
a CI job's trunk can be stale, absent, or named by flag without any of that showing up in a sentence
about the changeset. `default_branch_source` is `flag`, `origin-head` or `sole-candidate`, reported the
same way however the branch arrived, because "how did you know?" is the question a strange answer
raises.

## 11.2 `git pair diff`

Displays or launches the configured diff for the selected logical review span.

The command should understand changeset base metadata and review boundaries.

It should support file-specific invocation:

```bash
git pair diff src/booking/service.ts
```

and span options described below.

`git pair diff` and its span options serve review. An author who wants to read a review that was just submitted uses `git pair change feedback` (§9.3) instead.

## 11.3 `git pair check`

Asserts that the checked-out changeset is integration-ready, and exits non-zero when it is not.

This is the assertion half of the pair with `git pair status` (§11.1). `status` reports what the
history says; `check` answers the one question a merge gate has, in its exit code, so nothing has
to parse prose. CI needs `$?` and nothing else:

```bash
git pair check || exit 1
```

The conditions, all of them reported rather than the first:

1. the changeset has not ended (§9.7),
2. the newest lifecycle marker is a review whose outcome permits integration — `approve`, or
   `feedback` under `--allow-feedback`. A changeset that is merely marked ready, or withdrawn by
   `change unready`, is not reviewed,
3. no marker after it carries `Review-*` trailers this build cannot read,
4. the commit that review spoke about is still in this line of history: `Review-Head` (§10.4) is an
   ancestor of, or is, `HEAD`. A rewrite — rebase, amend, force-push — rewrites the marker and keeps
   its message, so the surviving approval names a commit the branch no longer has. A marker that names
   no head is refused too: the gate cannot place an approval whose commit it does not know, and
   deriving the value from the graph would read the rewritten parent as the reviewed one, which is the
   case the condition exists to catch (§12),
5. the content that review looked at is still what `HEAD` carries: the tree is compared between the
   marker and `HEAD`, ignoring `changesets/<changeset>/`, the same comparison `status` makes,
5. the changeset has not already been integrated (§13.2) — the record says the review is over, so
   this is the other single-reason verdict, and it comes first.

Conditions 4 and 5 are different questions, and a rewrite is what separates them. A rebase that
resolves no conflict changes no file, so the tree comparison passes and only the ancestry test refuses.
That is the requirement "rebase after approval requires re-approval" made enforceable (§12), and it is
why the gate asks about a commit rather than only about a tree.

Every condition is a reading of commits, and only one of them reads a durable ref: the integration ref
is consulted to say "this already landed". Nothing is asked of the archive family (§13.1), which is the
difference from the previous shape of this gate. It used to require that the changeset's archive ref
pointed at `HEAD`, so a changeset in flight could never pass, and a clone that had not fetched
`refs/git-pair/*` reported reviewed work as unreviewed. While work is in flight git-pair writes no ref
at all, and the gate reads the branch that holds it.

Every failed condition is printed, because a gate that reports one problem per run turns a
two-minute fix into a round trip per problem, and a log that explains itself once is the difference
between a check people read and one they re-run.

```text
$ git pair check

OK: booking-transaction is integration-ready
head:  91bf204
next:  `git pair check`, then merge into main with ordinary git, then `git pair integration record`

$ git pair check

NOT READY:
- latest review outcome is blocking
- content outside changesets/booking-transaction/ changed since 91bf204: src/booking/service.ts
```

A rewritten branch says so in the same list:

```text
$ git pair check

NOT READY:
- review 91bf204 reviewed a7f3c98, which is no longer in this history: the branch was rewritten since the review, so the approval does not license integration
```

The name of the rewritten head is in that sentence because it is the other half of the comparison — a
reader who sees only "history moved" cannot tell a rebase from a fetch gap.

The passing verdict names the commit it cleared, not only the changeset: a log that says "ready" without
saying what it looked at cannot be re-read after the branch has moved. `next` is the rest of the handoff
(§9.5), printed by the commands that know where the work stands.

Exit codes are the existing table: 0 integration-ready, 1 not ready, 2 usage, 3 git failed.
Failing is the command working, so the verdict is written to stdout and no `git-pair:` diagnostic is
printed over it.

`--allow-feedback` is the only policy switch:

```text
                    default    --allow-feedback
APPROVED            pass       pass
FEEDBACK            fail       pass
BLOCKED             fail       fail
ended               fail       fail
```

`feedback` is the case the switch exists for. A non-blocking review is a review of that head, and
whether it is enough to land is a policy of the repository rather than a fact about the commit, so the
default refuses and `--allow-feedback` says otherwise. There is no per-changeset config for this: the
flag is on the command that runs the gate, which is the one place the policy is known.

`--json` prints `changeset`, `ready`, `state`, `head`, `reasons`, `policy` (`approve-only` or
`approve-or-feedback`, so a verdict in a log carries the policy that produced it), `reviewed_head`,
`integrated` and `integrated_commit`. `head` and `reviewed_head` are full SHAs rather than the short
forms the human output prints, because the consumer compares them against the revision it built —
`integrated_commit` is short, matching `status`. `reviewed_head` is the commit the newest permitting
review named (§10.4), reported whether or not the verdict is ready, and omitted where the marker names
no head. `reasons` is an array in both verdicts, so a consumer branches on `ready` instead of handling
two shapes for one fact.

In this form the verdict is `ready` rather than `$?`: a not-ready run prints its JSON and exits 0, so
a job piping it into `jq` keeps git-pair's answer separate from the pipeline's. Only the human form
exits 1 on a not-ready verdict; 2 for a usage error and 3 for a git failure apply to both.

`integrated` is reported beside the verdict rather than folded into it. A pipeline that runs this gate
before integrating will re-run it after, and needs to tell "not ready" from "this already happened"
without matching on the wording of a reason.

Two absences are decisions rather than gaps:

-   **No `--changeset`.** This is the check a forge runs *on* a revision. Naming a second changeset
    would make the verdict ambiguous about what was gated, so the flag does not exist here even
    though `status` and `diff` take it.
-   **No surviving-review-additions diagnostic.** §19 lists that check among readiness
    conditions, but `change ready` is where the decision is made and acknowledged, and conditions 2
    and 4 here already guarantee nothing has moved since the marker. Re-deriving it in `check`
    re-litigates a decision the author already took, in a command with no override flag to take it
    again.
-   **No ref to be holding.** The verdict does not depend on any durable ref existing, so a clone that
    has never fetched `refs/git-pair/*` gets the same answer about work in flight as one that has. What
    the namespace is for is the record of work that finished, and the commands that read it say when
    they have none to read (§11.4, §13.4).

The assertion is about the commit, not the checkout: uncommitted changes are not in `HEAD` and
cannot invalidate a review of it, so a dirty working tree does not change the answer. `status`
reports the dirt, because `status` is observing.

## 11.4 `git pair integration record`

```bash
git pair integration record --source <sha> --commit <sha> [--target <ref>] [--changeset <id>] [--allow-feedback]
```

Records that the reviewed head `--source` became the commit `--commit`, by writing **both** durable
refs for that changeset (§13):

```text
refs/git-pair/archive/<changeset-id>       = --source    the unsquashed tip that was reviewed
refs/git-pair/integrations/<changeset-id>  = --commit    the commit the work became
```

This is the CI half of the lifecycle, and the only point in it that writes a ref: once the forge has
merged, squashed, rebased or cherry-picked, the pipeline holds the two SHAs and not the name a human gave
the work. git-pair performs none of those operations and derives none of them — the link between a
reviewed head and a landing is recorded because squash, rebase and cherry-pick destroy the ancestry that
would otherwise have carried it.

The refs are written in that order, archive first, because the integration ref is the one whose existence
means *this changeset is finished*: if the process stops between the two writes the changeset still reads
as not-yet-recorded and the next invocation completes the pair. The reverse order could leave a finished
changeset whose chain nothing holds.

The changeset is **discovered from content, not from a ref**. `--source` is resolved through `rev-parse`
first — an abbreviated SHA pasted from a CI log must resolve before discovery, not match nothing — and
then the `changesets/<id>/` directories in its tree are the candidates, minus the ones the destination
branch already carries (a landed directory is in trunk precisely because this command is being asked to
record it, so the subtraction is skipped when it would leave nothing, and when there is only one candidate
to begin with). That is §4's rule read at the commit the record names, and it is the only discovery that
works from a CI checkout: it asks two trees, so it needs no ref to have been written first, no namespace
to have been fetched, and no branch to be standing around.

Resolution comes first, because every check below is about commits the caller named:

1. `--source` and `--commit` are both required — exit 2, since nothing about the repository is wrong;
2. `--source` resolves to a commit this repository has, else the failure names both possibilities — a
   shallow clone and a wrong SHA look identical from here;
3. the candidates. None fails, saying what that usually means: the wrong commit was supplied, or the
   changeset directory was never committed. More than one is exit 2 listing them and naming
   `--changeset` — the same rule every other command applies to ambiguity, so an agent learns one
   convention rather than one per command. A stacked child carrying its parent's directory is the case
   this asks about (§21);
4. `--changeset` disambiguates and never substitutes: the id named must itself be a directory
   `--source` carries;
5. `--commit` resolves.

**Then the record is read, before anything is verified.** If both refs already name exactly this pair, the
command answers "already recorded" and writes nothing — and it answers before the four checks below,
because a CI re-run arrives with whatever flags the second job happened to have, and re-verifying a fact
already on the record against a different set of assumptions can only produce a refusal about the
assumptions. A *different* pair for a changeset that already has one is refused here too, naming what is on
the record: that is the answer to the question a re-run is asking, and the alternative is a refusal about
the tree, which a second landing into a branch that already carries the changeset directory would also
fail.

**Then four verifications, each a refusal rather than a warning.** A record is the durable answer to
"where did this reviewed work go", and the checks exist because an confidently-written wrong pair is worse
than a refused command — the paper trail is what a release note, a bisect, or an agent trusts without
re-deriving it.

1. **The changeset is the changeset `--source`'s history names.** Tree discovery says which directories
   the commit carries; the markers in its ancestry say which changeset was worked and reviewed. They have
   to agree. A source whose markers name only some other changeset — a directory copied out of a stacked
   parent, an id renamed after the review — is refused with both names in the sentence, because "never
   reviewed" would send the reader to review work that has been reviewed, under a name that is not its own.
2. **Its newest verdict permits integration.** The newest marker naming the changeset in `--source`'s
   ancestry must be a review whose outcome permits integration: `approve`, or `feedback` with
   `--allow-feedback`, the recorder's copy of `check`'s flag (§11.3). No markers, an offer with no verdict,
   a block, feedback without the flag and an ending all refuse. This is the same reading `check` makes of
   the same history, which is the point: the two commands a person runs one after the other must not
   disagree about what counts as reviewed, and an unreviewed head written into the durable pair would make
   the pair lie.
3. **`--commit` is in the destination branch's history.** With `--target`, that ref is the destination and
   the check is exactly one containment test. Without it, git-pair names the destination itself and tries
   what it can: the `base:` recorded in the changeset's own `CHANGESET.yaml`, then the default branch, and
   the first that contains the commit is the one the record is verified against. Trying both is what keeps
   a stacked child recordable — its `base:` is its parent branch, and a stack merged into trunk in one go
   lands the child on trunk — and it is not a licence to be wrong: a commit in neither history is refused,
   naming what was tried and where each guess came from. Landing somewhere else is allowed, only not
   silent: `--target <ref>` is how you say so. When nothing identifies a destination at all, no check is
   made and `target` comes back empty. "This repository cannot say where work lands" is a different fact
   from "the work did not land where it was said to", and only the second one refuses.
4. **`--commit` is the commit that brought `changesets/<id>/` there.** Not that the directory is present —
   that the commit *added* it over its first parent. The directory is committed content that travels with
   the change through merge, squash and cherry-pick, so its arrival is the fact that identifies a landing
   commit when ancestry has been destroyed; and presence alone cannot tell the landing from a follow-up
   commit on the destination branch that merely carries what the landing put there. A record pointing at
   such a commit — a release-branch housekeeping commit, say — fails here instead of quietly succeeding.

What check 3 does not assume matters: `--commit` need not descend from `--source`. A squash landing has no
ancestry between the two, and the record is the thing that connects them. Reachability verifies a ref
rather than deriving where the work landed, which is why it does not reopen the derivation this design
removed.

Both writes are create-only (`update-ref <ref> <sha> <old>` with `<old>` the zero oid), behind an
existence check that prints git-pair's explanation rather than git's `refusing to update ref`. Asking for
the commit a ref already names succeeds and changes nothing, so a retry after a half-written record
completes it; asking for a different commit is refused, and the refusal names the commit that *is*
recorded and the one that was asked for. The check alone would be a race; the create-only write is what
stops two pipelines recording the same landing from both winning (§13.3).

A second landing is refused rather than recorded. A backport to a release branch is a fact about that
branch's history, which git already records; git-pair keeps one pair per changeset, not one per landing.

The command needs no checkout and writes no commit: it is addressed by SHA and ref, and running it from
the default branch, a release branch or a detached CI checkout is the same operation. Exit codes are the
usual table: 0 recorded, 1 a rule refused, 2 usage, 3 git failed.

```text
$ git pair integration record --source a7f3c98 --commit d91c21e --target origin/main
booking-transaction: recorded d91c21e as the integration of a7f3c98
  archive:     refs/git-pair/archive/booking-transaction -> a7f3c98
  integration: refs/git-pair/integrations/booking-transaction -> d91c21e
  verified reachable from origin/main
```

Without `--target` the same line says which branch git-pair chose and how:

```text
  verified reachable from main (the changeset's base branch)
```

The no-op answer prints nothing about reachability, because it made no checks: it read the record, found
this pair already on it, and wrote nothing.

Re-running it is a success that changed nothing, and says so:

```text
booking-transaction: already recorded d91c21e as the integration of a7f3c98; nothing changed
```

`--json` prints `changeset`, `source`, `commit`, `target`, `target_derived`, `archive_ref`,
`integration_ref`, `recorded` and `already_recorded`, with full SHAs, so a pipeline can compare them
against what it built. `recorded` means this call wrote at least one ref and `already_recorded` means both
already named this exact pair — a retry is a success, and the two successes are worth telling apart in a
log. `target` is the branch the landing was verified against in branch form (`main`, `origin/main`), empty
when nothing identified one and so no containment check was made; `target_derived` is present and true only
when git-pair chose that branch rather than the caller naming it, which is the one verification in the
answer that nobody asked for.

When the clone holds no `refs/git-pair/*` refs at all, the command prints a warning naming the fetch
(§13.4) and records anyway. The candidate rule means its answer comes from trees rather than refs, so the
namespace being empty changes nothing about what it writes — but it changes what a reader elsewhere
believes, and a clone that has never fetched is one where `never recorded` is a claim about the clone.

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
landed (a pair of refs written once by `git pair integration record` — not a state)
```

Any point above can also end: `git pair change abandon` (§9.7) records a terminal marker, and the
commands that move state refuse against the changeset afterwards.

The author's side of that loop is `git pair change ready`, then `git pair change wait` to learn that a
reviewer has acted, then `git pair change feedback` to read the submission before addressing it, then
`git pair check` and the landing itself (§9.5). Every step is a command an agent runs; the merge in the
middle is the one step git-pair leaves to git.

Readiness also ends on purpose. `git pair change unready` (§9.6) writes a `working` marker and takes
the changeset back out of the queue, which is how an author says "not finished after all" instead of
leaving the offer standing while they keep implementing.

State comes from the commits on the branch. There is one fallback, and it is not a second source: where
a changeset has no branch — deleted after the work landed — the archive ref at
`refs/git-pair/archive/<changeset-id>` is what remains, and deriving from it can only report what the
branch last recorded before it disappeared (§13.1). An abandoned changeset whose branch is gone has no
record at all and reports nothing, because an ending recorded only on a branch is an ending that went
with it. Nothing reads a ref to decide that a changeset is ready, and no command moves state by moving a
ref.

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

There is no state for a landed changeset. Landing is not a marker: git-pair does not perform the merge
and does not derive it either, because squash, rebase and cherry-pick each destroy the ancestry that
would have answered the question. `git pair integration record` (§11.4) is the merge's record, written
once by whoever did the landing, and `git pair status` reports it beside the state rather than as another
value of it.

Avoid maintaining a fragile mutable state variable where possible.

Prefer deriving effective state from review marker commits and repository state.

Important safety property:

> State moves when a git-pair command records a marker, and at no other time.

A commit is not a command. An author who keeps working after `change ready` leaves the changeset
`ready`, which is why `change unready` (§9.6) exists: taking an offer back is a decision, and a
decision is recorded rather than inferred. `status` counts the commits that arrived since the
marker, so the drift is visible without being a state.

The drift that property guards is the one a merge would act on:

```text
review: approve
agent changes implementation
```

`git pair status` reports `APPROVED` — the newest marker is still the approval — and `git pair check`
(§11.3) refuses that head. A gate that passed it would certify unreviewed content as reviewed, so
`check` is the one command that compares the tree (§11.3).

An approval is about a commit, and the tree is only half of it. The other half is lineage, and it is
the half a rewrite changes without touching a file:

```text
review: approve          (Review-Head: a7f3c98)
git rebase main          (a7f3c98 becomes a different commit; the message, and the trailer, survive)
```

`git pair check` (§11.3) refuses that branch, because the commit the reviewer accepted is not in this
history. Merging the base in rewrites nothing, and passes. This is the requirement "rebase after
approval requires re-approval", and `Review-Head` (§10.4) is what makes it enforceable without a ref
namespace: the marker carries its own evidence of what it approved.

Two consequences worth stating plainly. An approval whose marker names no head is refused rather than
trusted, because git-pair cannot tell which history it covered. And a review submission that is itself
rewritten is not the submission the reviewer made — the requirement's "must not be treated as
equivalent to the original approval automatically" — which is what it means for the pre-rewrite review
commit to be unreachable once the branch moves on (§13.4).

---

# 13. The Durable Refs

git-pair has exactly two durable refs per changeset, and writes both once, at landing (§11.4):

```text
refs/git-pair/archive/<changeset-id>         the chain: implementation and review, interleaved
refs/git-pair/integrations/<changeset-id>    the commit the changeset became
```

Example:

```text
refs/git-pair/archive/booking-transaction
refs/git-pair/integrations/booking-transaction
```

Both are children of one root, `refs/git-pair`, and of nothing else: no ref lives at the root or at
either family name, because a ref cannot be a leaf and a namespace at once, and git enforces that by
refusing the create. Naming is by changeset id, never by branch, which is what keeps landed work
findable after the branch is gone.

**Neither ref exists while work is in flight.** No git-pair command writes a ref before landing:
`change ready`, `change unready`, `change feedback`, `change wait`, `review submit` and `change abandon`
all write commits and nothing else. While work is going on the branch is the record — it holds the chain,
and a ref tracking it would be a staler copy of a story the branch tells better. This is the property the
previous shape of this chapter had backwards, and it is why the queue, the gate and `status` read markers
rather than refs.

## 13.1 The archive ref

It holds the complete unsquashed implementation/review/fix chain, which is what keeps that history
reachable through garbage collection, a squash merge, and `git branch -D`. The changeset names it, not the
branch, so landed work stays findable after the branch is gone; where a changeset has no branch, this ref
is the last readable copy of its history — a fallback for reading history, never a source of state (§12).

Absence is an answer. An in-flight changeset has no archive, and `status --json` reports `archive_ref` and
`archive_commit` empty rather than omitting them, so a consumer sees one shape either way. A changeset read
by `--changeset` whose directory survives on no branch and which has no archive is reported as an orphan by
`review queue` (§10.6) and is not a changeset to any command that writes.

One half of the pair can exist without the other, and only in one direction. `integration record` writes
the archive first (§11.4), so an interrupted run leaves a changeset that still reads as not-yet-recorded
and the next invocation completes the pair. An archive with no integration ref is therefore a half-written
record rather than a finished one, and it must not be read as "landed": integrated-ness comes from
`refs/git-pair/integrations/<id>` alone (§13.2).

The two paths an id uses are reserved against `change init` (§9.1): a new changeset may not take an id
whose archive or integration ref already exists, which is what stops a fresh branch from attaching itself
to a landed changeset's history. The check is of those two exact paths, so an unrelated ref under
`refs/git-pair` — a nested name git-pair no longer uses — does not reserve anything.

As with every durable ref this is a warning rather than a guarantee: nothing here stops a later
`git push --delete`, and git-pair runs no `push` at all (§26).

The invariant the pair exists to hold:

> The complete final unsquashed chain of a changeset that landed remains reachable from that changeset's archive ref.

## 13.2 The integration ref

The second ref records where the changeset landed:

```text
refs/git-pair/archive/booking-transaction      → A   the head that was reviewed
refs/git-pair/integrations/booking-transaction → B   the commit it became
```

It is written once by `git pair integration record` (§11.4) and never moved. It exists because the
fact cannot be derived: a merge preserves ancestry, while a squash, a rebase and a cherry-pick each
destroy it — and those four are meant to be equivalent from git-pair's point of view. Patch IDs, tree
similarity and commit-message heuristics may help a human recover a record that was lost; they are
not the protocol. A tool that inferred integration through them would be confident and wrong about
every squash merge, which is the common case on a forge.

Because the ref is the only answer, integrated-ness is derived from its presence and nothing else:
`status` reports it, `review queue` skips it, `git pair check` (§11.3) refuses a changeset that already
has one, and the commands that move state (`change ready`, `change unready`, `change abandon`,
`review submit`) refuse against it.

The ref stores an object id and nothing else, so git-pair does not claim to know the *name* of the
branch a landing reached. What it reports instead is derived and labelled as such: whether the
recorded commit is in the history of the branch git-pair calls the integration branch
(`integrated_in_default_branch`), and which branch that was (`integrated_default_branch`). That is the
distinction the field exists for — work that retired into `release/2.x` and never reached the default
branch must not read like a default-branch landing. The alternative, hanging a name on the ref by
pointing it at an annotated tag object, would put a peel in front of every reader of the ref and
break the exact-object matching §11.4's checks depend on.

## 13.3 Create-only

Neither ref ever moves. There is no code path that moves one: the only write is an atomic create
(`update-ref <ref> <sha> <zero-oid>`), and the hygiene test is what keeps that true — it requires exactly
one `update-ref` invocation in shipped code, and requires the old-value it passes to be the zero oid.

Asking for the commit a ref already names succeeds and changes nothing, so a retry after a half-written
record finishes it. Asking for a different commit is refused, and the refusal names both the recorded
commit and the one asked for, and says that git-pair never moves a durable ref. There is no flag for
overriding it: a landing that needs correcting is corrected in git and recorded under an id that has no
record yet, not by moving a ref out from under the readers who trusted it.

The pair `archive A → integration B` is the whole product of integration recording, and everyone who
reads it later — a release note, a bisect, an agent asked where this review went — reads it as a statement
of fact. A `change ready` on a branch someone forgot to delete would silently change what that statement
says, so the commands that write markers refuse a changeset with an integration record. That refusal lives
in the path that commits them, one step before the write: a command that wrote a marker and only then
learned the changeset was recorded would leave the marker on the branch with nothing pointing at it — a
half-write the author can undo only by rewriting history.

## 13.4 Getting the refs where they are needed

These refs are not fetched by default. A clone maps `refs/heads/*` into `refs/remotes/*` and nothing
else, so a CI job handed the branch has neither record. The two failures that produces must stay separate,
because their fixes are in different places:

| What is missing | What it means | What fixes it |
| --- | --- | --- |
| this changeset's record, while other git-pair refs exist | the work never landed | land it, then `git pair integration record` |
| the whole namespace | the checkout did not fetch it | `git fetch origin 'refs/git-pair/*:refs/git-pair/*'` |

The fetch carries no `+`: these refs are create-only, so a fetch that could clobber them would be a
way for a clone to move a ref that git-pair's own code cannot.

That is requirements §24's requirement stated as a rule: a command that needs the namespace says when the
namespace is empty, and does not dress a fetching problem up as a lifecycle verdict. It is the commands
that *read* the namespace say anything about it, and each says what its own answer would otherwise hide.
`integration record` warns and records anyway — its candidate rule reads trees, not refs, so an empty
namespace changes nothing about what it writes and everything about what a reader elsewhere believes
(§11.4). `git pair check` says nothing about refs, because nothing in its verdict depends on one (§11.3).
`review queue` prints no warning at all, which is the one silence to be careful with: work recorded in
another clone is simply absent from a queue that never fetched (§10.6).

Publishing is configuration, not behaviour. git-pair runs no `push` — the hygiene test forbids it, and
publishing review history is a decision about who gets to read it — so the refs stay local until a
repository configures

```bash
git config --add remote.origin.push '+refs/git-pair/*:refs/git-pair/*'
```

Until then the landing machine's clone holds the only copy, and the command that says so is reporting the
truth rather than failing.

The same section of the README covers the fetch a CI job needs for the *default branch* as well: the tree
rule (§4) compares the revision against trunk's tree, so a one-branch checkout has nothing to compare
against and resolution refuses. That refusal is a usage error — the caller supplies the answer with
`--default-branch` or with a fetch — and it names the fetch shape, because in CI the thing at fault is the
checkout and not the changeset.

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
plain `▾ Threads` when they are on screen — so a changeset with many threads stays scannable. `Tab` toggles
the two stops the keys have — the list column and, where the terminal has room for one, the diff — and `f`
names the tree instead of walking to the next stop. The box is not a third stop: `k` off the tree's top row
steps into it and `j` off its last row steps back out, and `a` and `t` name a row of it — `ABOUT.md`, the
thread heading — from wherever in the column the keys are. Each half keeps its own cursor, so the keys
return to the row they left. The navigation keys belong to the half holding them and mean the same thing in
either: `j`/`k` a row — and one step across the edge the two halves share, which is the only place a step
leaves the half it started in; `gg`/`G` the two ends of the whole column; `ctrl-d`/`ctrl-u` half a page and
`ctrl-f`/`ctrl-b` a page, counted in the window you are standing in. Each half's shortcut bar names the keys
of that half and none of the others, which is what makes a key of the other half an absence a reviewer can
read rather than a keystroke that vanishes. A rule separates the list from the
shortcut bar. The tree is bounded by two rules of its own — the row that separated it from the box above,
and one under its last row, with the reviewed counter below that — and those two are its focus light:
single rules while the box or the diff holds the keys, double while the tree holds them. Two rules rather
than a frame because the cells a frame spends each side are columns, and the narrow terminal that needs
the regions told apart most is the one with none to spare.

The box is capped at a third of the space the terminal gives and scrolls inside its own borders, so a
changeset with forty threads is a reason to read the box rather than a reason to hide the tree behind it;
rows that do not fit are counted in the bottom border (`3 more`), a note placed there because a note with
a row of its own would move the layout as it came and went. The borders are also the box's focus light —
single rules, double rules while it holds the keys — the convention the divider uses for the diff, chosen
over styling alone so the focus survives a terminal that renders no bold. The box is closed on all four
sides in both layouts, including the side nearest the diff column: without it the rows inside read as a
column of loose text rather than as the changeset's own block. Its cursor row is highlighted only while
the box holds the keys, since the borders say that already and a row that looks chosen and is not is a row
a reviewer marks by mistake. The box keeps the row the reviewer left it on, wherever the keys went; `a` is
the key that puts the cursor on `ABOUT.md`, since that is what a reviewer came to the box to read, and the
span row above it is what they change — and change least often.

The span is the box's one
control: `Enter` or `Space` on its row opens the span picker, since what a review is measured against is
worth choosing, and `base` above it is a line of the frame rather than a row, since a base is only read.
The picker commits nothing until its own `Enter`, so the row is available over a historical span as well.
A row that changed in the span keeps its file row as well as its place in the box: the tree is what the
diff did, the box is the shortcut to read what was said about it.

The files are a tree rather than a list of paths. Each sits under its directory, a directory that
holds nothing but one directory is folded into that row (`src/booking/` above is one row, not two),
and a row prints only the name the rows above it have not already said. Each level is indented four cells,
which puts a child's name two cells right of the directory above it: a directory spends four cells before
its name — the fold arrow and the mark gutter — and a file only the gutter, so two cells a level lands
every child's name in exactly the column its parent's name started in. `h` and `l` fold and unfold
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

The shortcut bar holds the bottom band, and the band is as tall as the tallest shortcut bar the screen
can show: the bar changes when the keys move between the file tree, the changeset box and the diff, and
the band does not. A message about the last keystroke is drawn over the bar rather than beneath it, so
the list does not reflow to make room for it and the keys the reviewer is reading for stay where they
were. Dismissal follows what the message asked of the reviewer:

-   a **note** reports what just happened — the span `v` landed on, what `r` cost, marks resumed at
    startup — and fades after a few seconds, because reading it is a courtesy rather than a
    requirement, and the shortcut bar is worth the rows it was using;
-   a **refusal or a failure** explains why a key did nothing, or what went wrong, and stays until
    the reviewer presses something or dismisses it with `Esc`: the next key depends on having read
    it, and a clock that retires it would retire the instruction with it;
-   the **drift warning** is not a message the reviewer caused, so nothing they type can clear it.

`Esc` dismisses whatever is in the band. Only one thing is shown at a time, in that order of need: a
prompt the reviewer is typing into, then what the last key did, then the drift warning. A message
needing more rows than the band has loses its tail to an ellipsis rather than growing the frame —
the shortcut bar is longer than any message the screen sends, so the bar is normally the taller of
the two.

On a terminal at least 100 columns and 16 rows, the list shares the screen with a preview column:
the diff the common span made to the selected file. It prints git's
own output, colour included, and adds only what a fixed-width column cannot decline to do: the line
number git itself put in the hunk header, a break where a line is too wide, and the spaces a tab
advances to. It does not fold, group, filter, or renumber hunks, and it does not choose
colours, which is the shape PRD §3's refusal to build a diff renderer leaves: the pane is
for glancing, and reading a diff means opening it. The frame fills the terminal — the row
area holds the window's height and every row is padded to its width — so the shortcut bar
sits against the bottom edge rather than under a short list.

The prompts ask in a line of the footer and leave the screen otherwise as it was, so the diff column
survives a thread title being typed and a submit prompt being answered: the reviewer who pressed `T`
beside a diff comes back from the editor to that diff rather than to a screen that lost it on the way.

`p` moves the keys into that column and does nothing else, so a diff longer than the column is read
with the keys a diff is read with: `j`/`k`, `d`/`u` or `ctrl-d`/`ctrl-u`, `ctrl-f`/`ctrl-b`, `gg` and `G`
scroll the file already on show (`d`/`u` being `less`'s spelling of the same half page, kept because a
reviewer who reaches for it in a diff is reaching for something they know), `Enter` opens the difftool on
it, and `/` searches it. The search reads a term in the pane's bottom row — the row the note about the
rest of the file uses, so the field costs no rows — marks every match as the term is typed, and marks them
with SGR rather than colour: the diff's green and red are git's bytes, and a highlight that painted over
them would hide which kind of line a match sits on. `Enter` jumps to the first match at or below the
screen and `n`/`N` walk them, wrapping at both ends and moving the pane only as far as the match needs.
The term is looked for in git's line rather than in the row it was drawn on, so a term the column broke in
half is found and marked on both halves; a term written entirely in lower case is looked for in any case,
and one with a capital in it exactly. `Esc` closes the field and keeps the term already committed, and the
term outlives the file it was typed in, because the name being chased across a changeset is the same name
in the next file. `Esc` on the closed field hands the keys back to the
region that had them — tree or box — and leaves the pane where it was; `tab` moves them back to whichever
half of the column had them, because a region you can only leave by backing out of is not a stop on a ring. `p` pressed where the diff
already holds the keys does nothing, rather than meaning the opposite of what it means in the list, and
`q` quits from the pane as it quits from everywhere else — the pane is one `p` away and the marks are on
disk, so nothing is at stake in the difference. While the pane holds the keys nothing that changes the review can happen —
marking, editing, threading and
submitting are keys that do not occur, which is the whole-screen preview's promise extended to a
column that never hid its list. The two jumps into the box — `a` and `t` — are not among them: they name a
row, take the keys to it and leave the diff, which is what makes the cursor they move a visible one, and the
pane follows them on to the document the row points at. Under the whole-screen overlay they are keys that do
not occur, because the box is not drawn there to receive them; the box is reached there by the keys that
bring the list back. The frame says which column has the keys by changing what is drawn
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

The list column's other half is documents rather than files, and the pane reads them the same way it reads
a diff: ABOUT.md and each thread, when the box's cursor is on them, are shown as their own text with the
file's own line numbers, and the Threads heading is every thread in the order the box lists them, each named
above its text. The pane is the screen's place for reading text, and a changeset document is the one thing on
this screen that is pure text to read; a diff of one against nothing would report every line as added, which
is true and carries no information about what it says. The header counts what is on show — git's `+N −M` for
a diff, lines for a document, threads for the heading — and the pane follows the box's cursor the way it
follows the tree's, which is what makes the two halves one column. `Enter` in the pane opens what is on show
with the rules the row itself would apply: the difftool for a diff, the editor for a document, and the row's
own refusal where history makes the editor the wrong tool. Over a historical span the document is still read
from the working tree — it is the file the editor would open — so the header says `working copy` rather than
letting a reviewer read history that is not there.

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

The banner shares the bottom band with the shortcut bar rather than adding a row of its own, so warning
about the span cannot move the span. It is derived from the session rather than written by the last
keystroke, which is what keeps it alive: a note may borrow the band for its few seconds, and then the
warning is back until `r` moves the pin. It is full width rather than a row of the list column because in
a split screen the list column is narrow, and a warning that loses its key to an ellipsis warns about
nothing. `r` re-pins
every drifted endpoint to where its ref points now, recomputes the span, and reports which refs moved
and how many reviewed marks stopped applying — marks are keyed on the file's diff within the span, so
the marks whose diff changed match nothing and drop out, which is the honest outcome rather than a
reset done for its own sake. Ignoring the banner is legitimate: the comparison the reviewer started is
the comparison they keep. A ref that no longer resolves is not drift — there is nothing to refresh to
— and the span stays on its pin.

Suggested bindings:

```text
j/k      navigate the half of the list column that holds the keys, one step at a time across the edge it
         shares with the other half; gg and G are the two ends of the whole column
Tab      toggle the keys between the list column and the diff where there is room for one (shift-tab the
         other way); f names the file tree from wherever the keys are
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
j/k      scroll the diff a row; d/u or ctrl-d/ctrl-u half a page, ctrl-f/ctrl-b a page,
         gg the top, G the bottom
/        search the file on show: the term marks its matches as it is typed, enter closes the
         field and jumps to the first match at or below the screen, n and N walk the matches
         (wrapping), and esc closes the field keeping the term already committed
a, t     put the cursor and the keys on ABOUT.md or the Threads heading, leaving the diff;
         the pane follows them on to the document (not under the overlay, where the box
         is not drawn)
Enter    open the difftool on the file the pane is showing
Esc      hand the keys back to the list column, leaving the pane where it is in the file
Tab, f   move the keys back to the list column, as they do from anywhere in it
q        quit — from here as from anywhere else

a        put the cursor on ABOUT.md (e on that row opens it in the editor)
t        put the cursor on the Threads heading (Enter collapses or expands the list)
T        create a review thread

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

This state is **not** part of the durable review artifact. It is reading progress: it is never
committed, never pushed, and reported by no command, so a mark cannot move derived state.

**It is remembered locally.** Marks are written under the repository's git directory at
`$(git rev-parse --absolute-git-dir)/git-pair/marks/<changeset>/<commit>.json` — one set per commit
the review has looked at, the newest 12 kept — and reopening that commit restores them. Deleting
that directory forgets the marks and costs nothing else.

When the underlying diff for a file changes, the mark stops applying to it: each mark is stored
with the diff key of the file it belongs to, so only a file whose diff within the span is unchanged
comes back marked.

```text
✓ reviewed
↓ file changes
○ unreviewed
```

A new commit, a rebase, or a different span cannot revive a mark that no longer describes anything,
and clearing every mark is remembered rather than resurrected.

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

## 19.3 Where the diagnostic runs

`change ready` (§19.2) is the only command that runs the diagnostic, and `--allow-surviving-review-additions`
is a flag on that command alone.

Archiving used to run it as a final check before a squash or merge. With the command gone, the question is
what the check would protect at the two points that remain:

-   `git pair check` (§11.3) cannot pass a changeset whose content moved after the review that permits it,
    so it does not need this diagnostic to stop unreviewed work reaching a merge — and a gate with no
    override flag is the wrong place to acknowledge a surviving addition, because the flag would have to
    exist there too;
-   `git pair integration record` (§11.4) records a landing that has already happened, done by ordinary git
    on a branch the recorder does not own. Refusing to *write down* work the author has already shipped is
    an argument about a decision that is no longer theirs to un-take.

So today the recorder neither warns nor refuses. Whether it should warn — the record being the thing a
later reader trusts — is open.

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
-   its own durable record, written when it lands (§13).

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
git pair check
```

`git pair integration record` (§11.4) is the agent's to *read about* and not to run; it belongs to
whoever performed the landing, which is CI in the intended setup.

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
13. run `git pair check` to assert integration-readiness. Its exit code is the answer, and an agent
    asked to confirm that a changeset may land should call it rather than read `status` output,
    because `status` is observing and `check` is deciding (§11.3),
14. stop there. Landing is not the agent's step: the merge is ordinary git run by whoever owns the
    destination branch, and `git pair integration record` (§11.4) follows it.

An agent that rebases a branch after an approval has invalidated it, and `check` will say so (§12). The
response is to re-offer the work — `change ready`, then wait for a reviewer — and not to argue with the
gate: git-pair does not read an approval of one commit as approval of its rewritten successor, and an
agent editing a `Review-Head` trailer to make the comparison line up would be recording an approval it
did not receive. Where the history has to be tidied, tidy it before the handoff.

`git pair status --json` remains the way to check state without blocking. `git pair diff --unreviewed` is the reviewer's span command; an author consuming a newly submitted review uses `git pair change feedback`.

When the author needs to keep implementing after handing off, `git pair change unready` (§9.6) withdraws
the offer before that work starts. It succeeds when there is nothing to withdraw, so an agent may run
it unconditionally rather than branching on state.

An agent must **not approve its own work**.

Approval remains a reviewer action, and so is the merge: git-pair hands the work on and stays out of
the destination branch (§9.5). Nothing an agent runs makes a changeset landable — `git pair check` is
what asserts the gate, and it accepts `feedback` only when told to — and nothing an agent runs preserves
history on the reviewer's behalf, because the refs that do that are written once, by the landing.

Recording the landing (§11.4) belongs to whoever performs the merge, which in practice is CI, not to
the agent: an agent that recorded its own integration would be asserting a fact about the forge's
action rather than about its own. `git pair check`'s `integrated` is the field a pipeline reads to
learn the record already exists.

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
longer carries what was accepted: `git pair check` refuses it (§11.3), and `git pair change ready`
offers the new head for review.

The same is true when nothing in the tree changes. A rebase leaves the approval as the state and
refuses the merge, because the approval names a commit — `Review-Head`, §10.4 — that this history no
longer contains (§12).

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
refs/git-pair/*
```

preserves the detailed human–agent development/review history, once the work has landed (§13).

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
-   make per-file review checkmarks part of the review record; they persist locally to resume a review (§16),
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

File-level progress is delivered: marks are remembered per commit and stop applying when a file's
diff changes (§16). What remains is the hunk-level half:

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

## Remote record enforcement

-   pre-push hooks,
-   server-side validation,
-   automatic pushing of `refs/git-pair/*`,
-   protection against destructive rewrites of a changeset whose record has been written.

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
Tab      toggle the keys between the list column and the diff (f names the file tree; the changeset box
         is the other half of the list column, reached by walking up out of the tree)
Enter    activate the selected row: diff a file, diff or read a changeset document
         (whichever gives a real comparison), collapse the thread list, create a thread
d        open the difftool for the selected row, falling back to the editor with a note
e        edit the selected file or document
p        move the keys into the diff preview column; inert where the diff already has them.
         Where there is no second column, it takes the screen for the diff
ctrl-f   page the preview down
ctrl-b   page the preview up
/        search the diff the preview column (or the whole screen) is showing; n/N walk the matches
Space    mark file reviewed/unreviewed

a        put the cursor on ABOUT.md
t        put the cursor on the Threads heading
T        create a review thread

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

Human approves:

```bash
git pair review submit --approve
```

and the gate is what the author and CI both run:

```bash
git pair check
```

At this point:

-   the branch carries the complete unsquashed history, and holds it until the work lands,
-   the branch can safely be pushed and squash-merged,
-   `main` can retain a single clean feature commit.

The owner lands it with ordinary git and git-pair records where it went:

```bash
git pair integration record --source <approved-head> --commit <landing-commit> --target main
```

From then on the durable pair holds the story: the complete unsquashed history is reachable from
`refs/git-pair/archive/<id>`, the landing is `refs/git-pair/integrations/<id>`, and the branch can be
deleted without losing the detailed review history (§13).

---

# 30. Product Thesis

`git-pair` treats Git itself as the protocol for agent-era peer review.

The codebase contains the review context. Git commits establish review boundaries. Markdown provides natural high-level conversation. Existing editors and difftools remain the code-review surface. Dedicated refs preserve the complete review history without forcing that noise into `main`.

The tool's role is deliberately narrow:

> **Coordinate the review lifecycle, make the right Git state easy to understand, and remove the ceremony between an authoring agent and a human reviewer.**
