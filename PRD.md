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
-   Git's configured editor (git's own resolution: `$GIT_EDITOR`, `core.editor`, `$VISUAL`, `$EDITOR`) for text editing.
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

The first half of that equation is enforced rather than assumed: a branch carries one unlanded changeset, plus the
directories of the changesets it is stacked on. Two directories that no record ties together arrive when work from
another branch comes onto this one - a merge or a pull of a shared branch, a cherry-pick, a squash merge - and the
second would reach the destination with no approval of its own. So `git pair init` refuses to create the second one,
`git pair change ready` refuses to offer the branch, and `git pair check` reports it as a reason the merge is gated.
Two things do not count against a branch: a directory the destination already carries, which is landed work, and the
ancestors a child records above itself, which are one stack rather than several changesets (§9.8).

The link is a record, and `git pair change stack --base <branch>` writes it for changesets that already exist: it
names the branch in `base:` and the changeset that branch carries in `base-changeset:`, in the child's file alone.
The command compares what the two branches carry and refuses rather than guess which of two directories is the level
below.

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

## Landed

A changeset is **landed** when the branch it asks to land on — its *destination*, §13 — carries
`changesets/<id>/`. That directory in that tree is the whole record of the landing: git-pair writes no ref
anywhere in a lifecycle, so nothing has to be told that the work arrived and nothing can be out of date
about it.

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
  .landed/
    waitlist-rebooking/
      CHANGESET.yaml
      ABOUT.md
```

The directory name is the changeset's **ID**, and the ID is what git-pair calls the work
from here on: it names the directory, and it is the name the destination carries after the landing
(`changesets/.landed/<id>/` in a repository that tidies, §12). The branch name is where the default comes
from, not the identity.

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

The rule is deterministic, and it is a suggestion. `init --id <id>` chooses the ID
instead (§9.1), which is what makes the identity independent of the branch: branches get
renamed, two branches can normalise to the same name, and integration tooling should not
have to infer an identity from a branch.

An ID is never rewritten to fit. An `--id` that would need normalising is refused rather
than quietly changed, because an identity the tool derived from a string nobody typed is not findable by
the person who typed it.

The directory belongs to no branch. Which changesets a revision is working on is read from
content, and the reading is the same question everywhere: `status`, `queue`, and a CI job
with two branches fetched all ask it the same way.

### Which changeset a revision is working on

The changesets on a revision are the `changesets/<id>/` directories present in its tree that the
integration branch's tree does not have.

- A directory that has reached the integration branch is landed work. It drops out with no
  record to write, no branch name, and no dependence on whether the landing was a merge, a
  squash or a cherry-pick — the directory is in trunk either way.
- A directory that exists only here is work in progress, whichever branch line it sits on. That
  is what lets a parent branch and the child branched off it continue one changeset instead of
  the child inventing a second answer about the same work.
- Nothing asks which branch is checked out, so a detached HEAD, a CI checkout and a human's
  branch answer the same question. Branch names are not part of the durable data.
- The cost follows the directories on this revision, not the number of changesets the repository
  has ever had.

Where more than one directory survives, the records decide before any ordering. A changeset that
records another as its `base-changeset:` says that one is the work below it, so the parent leaves the
candidate list: **the candidates a command reports are what survives that subtraction**, not every
directory on the branch, and a stacked branch therefore names one id. What the records leave is then
ordered by the branch's own history and not by guesswork: the changeset whose directory joined this
line most recently comes first - the commit that added it, not the commit that last edited one, because
a child branch edits its parent's directory without starting work on the parent. Two directories that
joined on the same commit order nothing between them, and there the answer is **ambiguous**: the
command refuses, names every candidate, and gives two ways out: `--changeset <id>` answers for one
command, and
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

`init` records that branch in a changeset's `base:` as its **branch name** — `main`, not
`refs/remotes/origin/main` — because `git clone` records the remote's default branch in
`refs/remotes/origin/HEAD` and that is the answer the resolution above prefers, so the ref it returns is
usually a fetch ref even in a clone with a local trunk. The name is tried under `refs/heads/` first and then
under `refs/remotes/` every time a base is read, so where it resolves it is the same branch and the better
thing to write; `base:` is a line other machines read, and a fetch ref written there reads as a different
destination — and says *fetched* about a change that has never been pushed. Where the name resolves to
nothing, which is a clone holding the integration branch only under the fetch root, the qualified ref is
recorded instead: a base that does not resolve fails every command, and `cannot resolve changeset base` is the
worse outcome. Where the local copy of the base and its remote copy are different commits, `init` notes it
with the counts, and pushes nothing (§26): the diff measured from this clone is then not the diff the forge
will show.

The consequence worth knowing: if someone merges your unlanded changeset into their branch and
lands *that*, your changeset reads as landed on your own branch too, because your directory is
now in the integration branch's tree. That is the rule working as intended — the work is in
trunk — and it is why a changeset's branch is expected to land its own content.

It runs the other way as well, which is the pruning rule. Deleting landed `changesets/<id>/`
directories from the integration branch makes those changesets unlanded for every branch that has not
merged the deletion, and they return with their review history attached. `git pair change tidy` (§12) is
the operation that exists so a repository does not have to: it moves the directory to
`changesets/.landed/<id>/` in a commit rather than deleting it, so the retirement rides the normal flow and
the chain stays reachable from the commit that moved it. A repository that deletes instead should delete only
what has been moved and is no longer wanted, and a changeset retired by trunk's tree alone comes back with
the deletion. A terminal record (§9.7) is a marker on a branch, so pruning trunk does not retire it either.
This is documented behaviour, not a bug to fix: the resurrection of a landing that trunk's tree alone retired
is the rule answering the question it was given.

### Landed, unreviewed

The rule above has a second half, and it is a report rather than a reading. A `changesets/<id>/`
directory present in the destination's tree **without a landing licence** is work that landed without
review reaching the destination. The licence is two conditions, both read from the destination: the chain
behind the directory carries a verdict that permits a merge, and that verdict names a commit the
destination holds as well (`landingLicence`, `internal/cli/landed.go`). An approval is a statement about a
commit, so a chain that brought the statement and left the commit behind licenses nothing — the same test
the write gate has always run against a rewritten branch, asked where the work arrived instead of where it
was written. The tree rule makes that state invisible: the
changeset stops being a claim, its branch may be deleted, and the paper trail is the merge commit alone. So
the state is detected and reported, by name, with the read that goes and looks:

```text
LANDED UNREVIEWED

  booking-transaction
    on main at 4f2b8c1: the landing carried the directory in one commit, so no review markers came with it

  read one with `git pair status --changeset <id>`
```

`git pair queue` (§10.6) prints it as its own heading, below the queue; `git pair status` (§11.1) prints the
same finding on a branch that carries no changeset of its own, attached to the
answer that already tells you the branch holds no work in progress. Both read it from the destination's
history, from the reads the command was already making, so the report costs nothing per changeset and grows
nothing as a repository ages.

Two properties the wording has to hold:

- **It is a finding, not a queue entry.** No command closes it, because there is nothing to write: the
  verdict either happened somewhere the destination cannot see or it did not happen. The report prints a
  read (`git pair status --changeset <id>`), never an invocation, and never changes the exit code of the
  command that found it.
- **The reason says which reading applies.** `the chain carries no review verdict` means the chain came
  along and holds no marker to read. `the newest verdict is feedback (<sha>), which does not license a
  merge` and `the newest verdict is a block (<sha>)` name the verdict that was newest. A squash or a
  cherry-pick — `the landing carried the directory in one commit, so no review markers came with it` —
  brought the content and left the history on a branch that may already be gone (§13). Then the strict
  half, in its own words: `the approval (<sha>) names <commit>, which the destination does not carry: the
  landing replayed the run, so what was approved is not what landed` is a rebase merge, or an amend the
  author made under an approval; `the approval (<sha>) names no commit, so the destination cannot be
  checked against it` is a marker whose `Review-Head` trailer was lost. Both say one thing about a
  destination that cannot show its approval: the record arrived, and what it licensed did not.

The verdict stays strict where it cannot be checked, rather than passing for want of a counter-example. Two
changesets in this repository's own trunk are in that shape, and a rule that read an unchecked approval as
a licence would have reported neither of them.

The finding is capped where it is printed — ten changesets, the rest counted — because the queue is also
a notification surface, and because a repository with fifty unreviewed landings has a workflow problem
that a fifty-line list will not fix. `--json` carries all of them (§10.6).

This is the cheapest thing in the design that protects the durable-memory goal, and it is why the merge
stays outside git-pair without the paper trail becoming invisible.

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

```yaml
id: booking-transaction-tests
parent: booking-transaction
parent-changeset: booking-transaction
```

`id` is the changeset ID, and it is the directory's name rather than a second opinion
about it: a file whose `id` disagrees with the directory holding it is an error to
correct, not a conflict to resolve. `base` is the ref the changeset's diff is measured
against.

A stacked changeset spells that ref as `parent:` instead, and names the changeset living on
that branch as `parent-changeset:`. The branch is the active locator while the parent is in
flight; the changeset ID is what still means something after the parent lands and its branch is
deleted (§21). `parent:` **is** the base — everything that measures the changeset reads one value
— so a file setting both `parent:` and `base:` is an error to correct, like an `id` that
disagrees with its directory. Changing either takes an explicit flag (§9.1); git-pair never
restacks a changeset on its own.

Those two keys are what every changeset has. There is no branch field, because the directory does
not belong to a branch (§4): recording one would put per-branch state in the durable data, and the
same commits would then answer differently depending on which branch happened to be checked out —
which is the disagreement the content rule exists to remove.

A third key appears only where an author put it. `ignores: <id> [<id>...]`, written by
`git pair change use` (§9.8), records that this changeset is the one its branch is working on and
that the named changesets are only sharing the branch with it. It is read when the changeset that
wrote it is a candidate, so it speaks about one branch: it is not a statement about the other
changesets, and it cannot change what another branch resolves to.

The ID does not change once the changeset has landed. Renaming one means moving the
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

The CLI has two subdomains and a flat front door.

```text
git pair change ...    the author's commands over a changeset in progress
git pair review ...    the reviewer's commands over a branch
```

Four commands are top-level because they are not "a change to a changeset" or "a reviewer action":
`init` starts the author's loop, and an agent's first invocation should say what it does without a group
name in the way; `queue` is read by authors, reviewers and CI alike, so it belongs to neither side; and
`status`, `diff` and `check` read state. Each has one spelling — a
renamed command with an alias behind it is two commands, and the second one stops being documented.

```text
git-pair
├── init
├── change
│   ├── use
│   ├── ready
│   ├── unready
│   ├── abandon
│   ├── feedback
│   └── wait
│
├── review
│   ├── open
│   ├── reopen
│   ├── about
│   ├── thread
│   ├── submit
│   └── history
│
├── queue
├── status
├── diff
├── check
│
└── integration
    ├── record
    └── publish
```

---

# 9. Author Commands

## 9.1 `git pair init`

Initializes review scaffolding for the current branch.

Example:

```bash
git pair init --base main
git pair init --id booking-transaction-v2 --base main
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

For a stacked changeset, `--parent <branch>` writes the stack instead of a base (§5):

```bash
git pair init --parent booking-transaction
```

It records the parent branch and the parent's changeset ID, discovered on that branch and left
empty when the branch does not hold exactly one unlanded changeset — a value nobody checked is
not written down as a claim. `--set-parent` restacks an existing changeset, the way `--set-base`
changes a base; `--parent` with `--base` is refused, and `--parent` naming the integration branch
or the current branch is refused, because neither is a stack.

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
   the changeset after a string nobody typed.
2. **It is unique.** An ID already in use stops the command rather than gaining a suffix.
   In use means a `changesets/<id>/` directory in the working tree or in `HEAD`'s tree, in either
   spelling — a landed changeset keeps its name in `changesets/.landed/<id>/` until
   `git pair change tidy` (§9.10) moves it there, and it keeps it afterwards. A suffix would be an
   identity nobody chose, baked into the directory the moment the changeset is readied, so the command
   fails and offers a free candidate to type instead:

   ```text
   changeset ID "feature-booking" is already in use: changesets/feature-booking already
   exists, possibly left behind by a changeset that has landed.

   Choose another ID:

     git pair init --id feature-booking-2
   ```

   Two branches whose names normalise alike are a collision when the directory from one is
   already committed on the line of development you are standing on, which is what a landed
   changeset left on the integration branch looks like. A directory you merely inherited from
   the branch you were created from is the same changeset seen from a second branch, so `init`
   reports it as already initialised instead of refusing a collision between a changeset and
   itself.
3. **It does not change.** The ID of a directory that exists is never rewritten, and a
   changeset that has landed keeps its ID for good (§5). Starting a *second* changeset on
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
git pair init --base booking-transaction
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
-   make the branch discoverable by `git pair queue`.

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

A surviving addition the author resolves by doing the work is removed from the file, not left in place; §19.5
states that policy and where the record of it belongs.

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

Nothing in git-pair completes a changeset. The author lands the work with ordinary git, and that is the
whole of it: the destination carrying `changesets/<id>/` is what makes the change landed, so there is no
step afterwards and nothing git-pair writes at landing (§13).

```bash
git pair check                                    # the gate a merge runs; its answer is `$?`
git switch main && git merge booking-transaction  # …or a squash, or a cherry-pick: git's choice
git pair status --changeset booking-transaction   # landed at 4f2c81a, with the chain that arrived
```

Three properties are worth naming, because they are what the previous shape of this chapter had
backwards:

-   **A review is a judgement; landing is a decision.** `approve` says the code is good enough. The
    author decides what to take forward and where, and git-pair reads that decision from the destination
    instead of anticipating it. Nothing about a changeset is preserved before it lands, which is why there is no
    `change archive`: the history a later reader needs is the history the merge carried, and where the
    merge carried none, git-pair says so rather than inventing one.
-   **A merge is not a git-pair operation.** It is not derived, reported as state, or gated (§12).
    git-pair neither runs one nor needs one to have happened in a particular shape: it asks the destination
    whether the directory is there, and names the commit that brought it in. None of that is a decision
    about whether the work should have landed.
-   **A landing cannot be re-done, because it is not an operation.** There is no flag to re-run and no
    value to correct: a landing that needs correcting is corrected in git, and a changeset the destination
    carries is refused by every command that writes markers (§12).

`approve` and landing are intentionally separate concepts:

```text
approve
    human judgment about the code

the merge
    the author's decision about where the work goes, with ordinary git
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

The withdrawal is the marker commit and nothing else. The queue and the gate read the marker itself, so
the retraction lives as long as the branch does, and a withdrawal whose branch
has been deleted is unreadable. That is a real loss and the design accepts it (§13).

A changeset the destination already carries refuses here, including when there is nothing to withdraw: a
changeset whose work has landed has no readiness to retract, and "nothing to withdraw" would be an
answer about the branch rather than about the work being finished.

`--json` prints `changeset`, `branch`, `base`, `state`, `was`, `recorded`, `unready_commit` and
`review_queue_visible`.

## 9.7 `git pair change abandon`

Records that the changeset will not be taken forward.

Two endings exist, and git-pair records both of them — differently, because they are different claims.
Landing the work (*merged*) is recorded by the destination branch itself: its tree carrying
`changesets/<id>/` is the fact, read wherever it matters (§3, §13), because only the person doing the
landing knows where the work went and git is what says so. Abandoning
(*not coming back*) is recorded by a marker on the branch, because the author knows that the moment
they decide it and there is nothing to point at. Without a command for it, the only way to say so
would be to delete the branch, which destroys the history that explains why — and what that history is
worth is a different question for changeset nobody is going to land than for one somebody is.

So the ending is a marker and nothing else: an abandoned changeset has no landing, and its history
goes with its branch. git-pair writes no ref for work that finished and no ref for work that did not.

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
and `init` reserves an id only against the records that landed changesets leave behind (§13).

`--json` prints `changeset`, `branch`, `state`, `was`, `recorded` and `abandoned_commit`.

---

## 9.8 `git pair change use <changeset-id>`

Records which changeset a branch is working on, so that the branch stops being ambiguous.

A branch normally carries one unlanded changeset. It carries more when a sibling's branch is merged
into it, and when a branch created off a sibling starts its own work without `--base` naming that
stack. The rule (§4) subtracts what the candidates record as their base changesets, orders what it can — the
changeset whose directory joined this line most recently wins — and refuses between the rest, because
choosing one silently means reading the wrong diff base.

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

## 9.9 `git pair change integrate`

Declares that the current changeset is approved and should be merged, by committing the declaration to
its branch.

An approved changeset had an author standing between the approval and the merge button: the gate, the
merge, and the record were three steps one person ran in sequence, and a pipeline that wanted the merge
had to take the decision out of the author's hands to get it. The declaration moves the waiting rather
than the decision. The author says once, in a commit on the branch where every reader sees it, that the
work is approved and should land. Whoever owns the destination branch performs the merge, with ordinary
git.

```bash
git pair change integrate
```

Requirements:

-   the changeset must exist and its working tree be clean — the declaration is a commit, so a dirty tree
    is the blocker `change ready` treats it as (§9.2): repository state, not bad arguments,
-   run the gate of `git pair check` (§11.3) first, as the same code rather than an imitation of it, and
    name every failed condition in one run,
-   refuse a changeset the destination already carries: the work has landed, and a declaration on
    top of it would ask for a second merge,
-   refuse a stacked child whose parent is not landed — see the rule below,
-   write the marker commit and nothing else: no ref — git-pair writes none, anywhere — no config, no
    network (§26),
-   succeed and record nothing when this head is already declared, so a script can declare
    unconditionally,
-   keep `git pair check` as the gate a merge runs. This command requests a merge; it never performs one,
    and it never certifies one.

Suggested commit:

```text
git-pair: integrate booking-transaction
```

Include machine-readable Git trailers, e.g.:

```text
Review-State: integrating
Review-Changeset: booking-transaction
Review-Head: 4f9c1d2e7b6a5389c0d4e1f2a3b4c5d6e7f8091a
```

Two things about a declaration are worth holding onto.

It is about a commit. `Review-Head` names the head that was declared, for the reason a review names one
(§10.4): a rebase rewrites the marker and keeps the message, and the stale name is what makes the gate
refuse the rewrite rather than bless it. A commit after the declaration moves the head it names, so the
next run declares that one instead and the branch keeps a record of every head that was offered for the
merge. Re-running at the head already declared records nothing and succeeds.

It is not a verdict. The approval underneath it stays the thing that permits the merge, which is why
`check` keeps passing after a declaration and keeps refusing after anything that would have made it
refuse before: the content that moved since the approval, the history that was rewritten, the parent that
moved. A declaration is superseded rather than erased — `change unready` (§9.6), a fresh `change ready`,
or a reviewer's submission (§10.4) each becomes the newest marker, and `integrating` (§11.3) goes back to
false. Withdrawing a request therefore needs no rebase and no force-push: it needs one more marker.

A stacked child is refused until its parent is landed. The automatic merge would land the
child on a branch that review can still rewrite, and history a merge is about to sit on can still be
rewritten underneath it. The rule belongs to the request and not to the
gate: `check` answers "may this merge" for a person who can merge a child onto its unlanded parent and
stand behind the result, while this command asks for a merge nobody will be asked again about. Merging a child
onto its parent by hand stays open; git-pair declines to queue it, not to allow it. A parent branch that
records no `parent-changeset:` is refused the same way, because there is no parent to look for and
"cannot tell" goes the direction that asks a person to look again.

The answer names the branch the work is asking to land on. For the child of a landed parent that is not the
branch its own `base:` names — the measurement moved onto the parent's landing when the parent landed
(§21), and a commit is not a destination — so the destination is read from where the parent landed and the
answer says which rule produced it.

The machine-readable answer carries the changeset, the branch, the base, the state before and after, the
head, whether this run wrote the marker, the declaration's commit, the destination with its source and the
chain walked to reach it, `reasons` as an array in both outcomes, and the next step.

Exit codes: `0` declared or already declared, `1` refused, `2` usage, `3` git failed (§22).

Nothing here performs the merge, and the product is not where a merge lives (§26). A repository that wants a
pipeline to perform it can copy one: `.github/workflows/git-pair-integrate.yml` for the trigger and the
permissions, `scripts/ci/git-pair-integrate.sh` for the sequence, and `scripts/gates/ci-integrate.sh` to
replay that job against scratch remotes. Its shape is an example; what a job gates on is §11.3's two fields,
and what makes a landing a fact is §13: the destination carrying the directory.

---

## 9.10 `git pair change tidy [<changeset-id>...]`

Moves the directory of a changeset the integration branch already holds from `changesets/<id>/` to
`changesets/.landed/<id>/`, as one commit of renames on the checked-out branch.

Landing keeps the directory (§12). That is right at the moment it lands — the directory is the record of what
was reviewed, and the tree is what makes the landing a fact — and it is scenery a release later: a reader
looking at `changesets/` wants the work still open, and a directory that has finished its review is not that.
Deleting it would throw away the record, so the command moves it aside instead, into a namespace git-pair owns
and every listing excludes.

It is a changeset like any other. The move is a commit on the branch you are on, so it rides a changeset and a
review, the reviewer sees the whole list as `R100` renames in one span, and it reaches the integration branch
through the same flow as any other change. Nothing is pushed, and no ref is written.

Because the directory's new path is still a directory the integration branch carries, `LandedIDs`,
`ActiveIDs`, `status`, `queue`, `check` and the destination walk answer every question about the changeset the
same way on both sides of the move. What changes is the path, and the name: the ID stays taken while the
landed directory holds it (§5).

Flags: `--all-landed` takes the set this branch carries that the integration branch already holds, instead of
refusing the ones that have not landed; `--dry-run` reports the plan and commits nothing; `--json` emits
`changesets` (always an array, each entry `id`, `from`, `to`, `action`), `commit`, `dry_run`, `destination`.

Refusals, each with its own reason, and none of them moving part of the list:

1. the ID is not landed on the integration branch — the directory is the record of work still open, and
   hiding it would take a live changeset out of every list;
2. a changeset still in flight is stacked on it — the child measures itself against its parent's directory,
   and moving that path out of the active spelling would change the diff a reviewer is looking at;
3. the working tree is not clean, anywhere — including inside `changesets/`, because the file a tidy must not
   sweep is a sibling changeset's half-written `ABOUT.md`;
4. this branch carries no `changesets/<id>/` to move.

`change tidy` with neither IDs nor `--all-landed` is a usage error, as is naming IDs beside `--all-landed`:
two different answers to the same question, and the caller has to pick before anything moves. A second run at
the same set is a no-op that names the directory it found.

Exit codes: `0` moved or nothing to do, `1` refused, `2` usage, `3` git failed (§22).

---

# 10. Reviewer Commands

## 10.1 `git pair review open`

Launches the interactive review TUI.

`git pair review` with no subcommand is this command: the group takes the screen itself and carries the
same span flags, so `git pair review --unreviewed` and `git pair review open --unreviewed` are one
command spelled two ways. `open` stays because a reader of the command tree looks for a verb.

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
3. `$VISUAL` — only when `TERM` names a terminal git can draw on; git skips it when `TERM` is unset or `dumb`
4. `$EDITOR`
5. the fallback the git build itself would use

`git var GIT_EDITOR` answers all five, so git-pair asks for it and consults 3–5 only when git
cannot answer. The value is a command line, so `code --wait` is a program plus its flags, as git
treats it.

The condition on the third rung is not in git's documentation and is worth stating, because it is
what makes a machine depend on `EDITOR` rather than `VISUAL`: with `TERM` unset or `dumb` — a CI
step, an agent, `ssh host git commit` — git passes over `$VISUAL` and takes `$EDITOR`, and with
neither set it answers nothing and exits 1 rather than name a full-screen editor it could not show.
git-pair's fallback does not copy that refusal: it resolves `$VISUAL`, so a person who configured
only `VISUAL` still gets an editor. The divergence is asserted in the console tests.

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

The submission is the marker commit and nothing more. No ref is written at review time: the head a later
reader needs is the head the merge carries into the destination, and a ref written at review time is a ref
that has to be moved — and moved back — for work that may never land (§13).

## 10.5 `git pair review history`

Accepts `--changeset <slug>`, which reads the changeset from whichever branch carries it (see
§11.1 for why only reads may do this).

Displays all review submissions for the current changeset.

Example:

```text
INDEX   SHA      REVIEWED  OUTCOME    REVIEWER  AGE
0       a18cf91  9f2c1de   block      Rae       2d
1       39b71aa  4d7e0b2   block      Rae       1d
2       f7c92e0  c07a9f4   feedback   Nils      4h
3       c81ea22  15b83d7   approve    Nils      20m
```

The index is chronological.

`REVIEWED` is the commit the submission spoke about, from its `Review-Head` trailer (§10.4), and
`REVIEWER` is who made it. Both are the commit's own facts: a review submission is authored by the
reviewer, so `REVIEWER` is that person rather than the author of the change, and `--json` reports
the same value as `reviewer`.

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

## 10.6 `git pair queue`

Shows changesets currently ready for human review, and the approved changesets whose author has asked for the
merge (§9.9) — two lists, because they answer two different people.

Default scope: current repository.

Scope is the repository, not the checkout: the queue enumerates local branches, reads each one's
`CHANGESET.yaml` and history from that commit, and reports the ones whose derived state is `READY`.
Running it on the deployment branch is therefore meaningful, and the working tree it happens to have
checked out changes nothing.

**One row per branch**, because one review lives on one branch: a review submission is a commit appended
to a branch, so the branch is what is ready. A changeset can sit on two branches at once — a copy made to
try a different approach, a parent and the child branched off it — and those branches have different heads,
different marker commits, and often different states. Collapsing them into one row would make work under
review on one branch invisible on the other, so each branch answers for itself and the row names it. Two
branches carrying one changeset are ordered by their own ages, and a tie keeps the branch order git
reports.

A changeset directory whose branch is gone is classified rather than skipped, and the destination is what
classifies it. A directory the destination carries is landed work: it is named in `skipped` with the commit
and the branch it landed on rather than dropped in silence, because its own branch is usually still
here — and a landing in a branch that is not the default one is exactly the case the trunk reading cannot
see, since the directory is absent from trunk and would otherwise read as live work. A directory the
destination does not carry is work in flight, and the queue lists it or explains why it does not. Nothing
here consults a ref: the answer comes from the destination's tree and history, so a clone with only its
branches and trunk gives the same queue as a clone with everything (§13).

Work that reached the destination with **no approval the destination can show** is the opposite case, and the queue
is where it is reported: its own `LANDED UNREVIEWED` heading, naming each changeset and printing the
read that goes and looks (§4's *Landed, unreviewed*). It is not a skip note, because "nothing to do" is
the wrong reading of a merge whose review never reached the destination, and it names no command, because
no command closes it.

A branch the rule cannot resolve is named there too, with its candidates and both ways out (§9.8).
A branch that is quietly missing from the queue is indistinguishable from a branch with nothing to
show, and the queue is where an author looks to find out why a branch is not in it.

Example:

```text
READY FOR REVIEW

booking-transaction-tests
  base: booking-transaction
  branch: booking-transaction
  ready: 18m ago
  head: a31c9d2

waitlist-rebooking
  base: main
  branch: waitlist-rebooking
  ready: 1h ago
  head: 92bf019

note: skipped booking-transaction (landed on main at 5556bc5)

LANDED UNREVIEWED

  waitlist-rebooking
    on main at 4f2b8c1: the landing carried the directory in one commit, so no review markers came with it

  read one with `git pair status --changeset <id>`
```

The heading is the merge somebody made without review reaching the destination (§22). The `reason` says
which of the two readings applies: a chain that arrived and carries no approval, or a landing that carried
the directory in one commit and kept none of the history (§13).
`--json` reports the same finding as `landed_unreviewed`, an array of
`{"changeset", "commit", "chain", "reason"}` — always an array, since it answers a question, and a consumer
should not have to tell "none" apart from "this build predates the question". Every array `--json` prints
follows it. An empty list is `[]`, never null.

The note is the destination talking: `booking-transaction`'s branch may still be checked out and its
directory still absent from trunk, and it is the destination's tree that says the queue has nothing to ask
of it (§13).

Approved work the author has handed over for the merge is a second list, not more rows in the first:

```json
{
    "ready_for_review": [],
    "awaiting_integration": [
        {
            "changeset": "booking-transaction",
            "branch": "booking-transaction",
            "base": "checkout-refactor",
            "state": "INTEGRATING",
            "head": "4f9c1d2e",
            "integrate_commit": "9b7e2c1",
            "declared_age": "2h",
            "destination": "main"
        }
    ]
}
```

The two lists answer two different people. `ready_for_review` is "what is waiting for a reviewer"; this one
is "what a reviewer has already approved and the author has handed over", and printing it under the first
heading would tell a reviewer to look at work somebody already looked at. A declared changeset is never in
both: a declaration is a marker, so a branch carrying one is not in the state the review row asks for. Rows
come from the same read of each branch's history that the review rows do, are ordered longest-waiting
first, and carry `destination` — the branch somebody would merge into, which for the child of a landed
parent is not the `base` the row also prints (§21). The human surface prints them under
`AWAITING INTEGRATION` and omits the section when there is none; the array is `[]` either way.

Future/global support should allow:

```bash
git pair queue --global
git pair queue --json
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

## 10.7 `git pair review reopen`

Launches the same TUI as §10.1 on the span from the most recent review submission to the working tree.

The name carries the reason it exists. `review open` shows the whole changeset, which is the right first
read. After the author answers a block or feedback, the work to read is what arrived since the reviewer's
own submission. `review reopen` names that span without a flag.

```text
git pair review reopen
```

It resolves the span `<last review>..current`, the same span `git pair review open --unreviewed` shows.
An earlier review is `git pair review open --since-review=N`. Needs a terminal, like §10.1. With no
review submission yet it exits 2 and says to run `review open` instead. The author reads a submission
with `git pair change feedback` (§9.3). This command is the reviewer's.

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
Next: `git pair check`, then merge into main with ordinary git
```

Nothing durable is reported, because nothing durable exists yet: the branch holds the history, and
git-pair writes no ref at any point before or after a landing. A changeset the destination carries reads:

```text
Landed:
  4f2c81a in main
  chain:  91bf204..a7f3c98
  review: the chain carries an approval
Next: integrated at 4f2c81a: nothing further to do for a changeset that has landed
```

The chain is what the merge carried. A squash, a rebase-merge or a cherry-pick brings the directory and
leaves the history behind, and the same block then reads `chain: none — the landing carried the directory
in one commit` with `reviewed: false` in `--json` (§13). Nothing in this block tells the reader to run a
command: the block prints because the destination already holds the directory, and a landing whose chain
carries no verdict is the different finding `LANDED UNREVIEWED` reports (§4).

The landing is also what makes the changeset readable with no branch at all — `status --changeset <id>`
after `git branch -D` says `Branch: none (read from the landed chain)` rather than failing. What that
read reports follows the rule above: the chain in the destination tells what the branch claimed before it
disappeared, and is not a second source of state. So `state` stays what the span says (`WORKING`, beside
`landed`), while the reviews are read from the landed chain rather than from the span — after a merge
landing the reviewed head sits *below* the base, which makes `base..head` empty for exactly the changeset
whose verdicts matter most, and "no reviews yet" about a head a reviewer approved is a wrong answer, not a
harmless one. `reason` says the read was of the landed chain instead of reciting a span nobody asked
about, and `review history` reports the same submissions as `status`. The stack above a landed changeset
is §21's `Stack:` chain.

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
    "state": "READY",
    "head": "c18c9a7",
    "latest_review": {
        "index": 2,
        "outcome": "block",
        "commit": "91bf204",
        "reviewed_head": "a7f3c98"
    },
    "landed": false,
    "landed_commit": "",
    "landed_branch": "",
    "chain_base": "",
    "chain_head": "",
    "reviewed": false,
    "default_branch": "main",
    "default_branch_commit": "9c41f0b",
    "default_branch_source": "sole-candidate"
}
```

`landed`, `landed_commit` and `landed_branch` are always present, with empty strings while the destination
does not carry the directory — they are reported the way they are so a consumer sees one shape either way.
`chain_base`, `chain_head` and `reviewed` describe what the landing carried (§13) and are empty and false
before there is a landing to describe. There is no `integrated` state and never was: landing is reported
beside `state`, not inside it.

`integrating` and `integrate_commit` are the author's declaration (§9.9) and its commit, read by the same
rule `check --json` uses (§11.3): the newest marker, and nothing a later re-offer or review has superseded.
The state already says `INTEGRATING` while that is true; the commit is what `status` adds, because the
author looking at their own branch wants the address of the thing they did, not only the state it produced.
`next_action` for that state names the push that makes the request visible to whoever can act on it, with
the landing contract (§29) still attached — and names `git pair change integrate` again when a commit has
landed on top of the head the declaration covered, because a declaration is about a commit.

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

On a branch that carries no changeset of its own the command has nothing to report, and the answer is a
usage refusal. When that branch is the integration branch the refusal also names the landings it carries
that hold no approval of what they carry, as the `LANDED UNREVIEWED` finding (§4's *Landed, unreviewed*):
"this branch holds no work in progress" and "work landed here with no review behind it" are two halves of
one situation, and a reader told only the first goes looking for a branch they forgot rather than at the
merge in front of them. It names no command, because none closes it. The exit code is
unchanged — the branch really does not carry work in progress, which is what that code means, and there is
nothing to write about the landings — and with `--json` the answer is a document rather than only an error:
`reason` and `landed_unreviewed`, where `git pair queue --json` (§10.6) carries the same list.

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
   `feedback` under `--allow-feedback` — or a declaration (§9.9) standing on such a review, in which case
   the review underneath it is the verdict. A changeset that is merely marked ready, or withdrawn by
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
5. the changeset has not already landed (§13.2) — the destination carrying the directory says the review is over, so
   this is the other single-reason verdict, and it comes first.

Conditions 4 and 5 are different questions, and a rewrite is what separates them. A rebase that
resolves no conflict changes no file, so the tree comparison passes and only the ancestry test refuses.
That is the requirement "rebase after approval requires re-approval" made enforceable (§12), and it is
why the gate asks about a commit rather than only about a tree.

Every condition is a reading of commits. One of them reads the destination's tree: the landing test, which
asks whether the destination already carries `changesets/<id>/` and, when it does, refuses the changeset as
finished work (§13). Nothing else is consulted outside the branch's own history, and the gate asks about a
commit rather than a checkout, so a clone with nothing but the branch and the destination gives the same
verdict as a clone with everything. git-pair writes no ref at any point in a lifecycle, so there is no
namespace to fetch before a gate can be trusted.

Every failed condition is printed, because a gate that reports one problem per run turns a
two-minute fix into a round trip per problem, and a log that explains itself once is the difference
between a check people read and one they re-run.

```text
$ git pair check

OK: booking-transaction is integration-ready
head:  91bf204
next:  `git pair check`, then merge into main with ordinary git

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
`integrated`, `integrated_commit`, `integrating`, `integrate_commit` and `next_action`. `head` and `reviewed_head` are full SHAs rather than the short
forms the human output prints, because the consumer compares them against the revision it built —
`integrated_commit` and `integrate_commit` are short in `status` and full here, matching the other
commit fields of this command. `reviewed_head` is the commit the newest permitting
review named (§10.4), reported whether or not the verdict is ready, and omitted where the marker names
no head. `reasons` is an array in both verdicts, so a consumer branches on `ready` instead of handling
two shapes for one fact.

`ready` and `integrating` are two answers to two questions, and the gate a pipeline runs is the
conjunction:

```bash
git pair check --json | jq -e '.ready and .integrating'
```

`ready` is "may this merge" — the verdict, the lineage, the tree, the parent. `integrating` is "did the
author ask for one": the newest marker is a `git pair change integrate` declaration (§9.9) covering this
head. Neither implies the other, and neither belongs inside the other. A gate that merged on `ready` alone
would merge every approved changeset the moment it was approved, which takes the decision out of the
author's hands; a gate that merged on `integrating` alone would merge a request whose approval had been
rewritten, withdrawn, or answered since. Both come from one run because they are computed from one read of
the branch, and two commands would be two answers waiting to disagree.

`integrating` reads the newest marker, not the newest declaration in the history: a re-offer, a re-review
or a retraction supersedes a declaration without erasing it, and a merge performed on a superseded request
is the merge the author has just taken back. The human form prints the declaration beside the verdict as
`declared: <sha>`, because a log that says "ready" about a branch somebody handed over should say that too.

`next_action` is the handoff the passing verdict licenses (§9.5) — the gate, then the merge, then the
record — and it is present only when `ready` is true. When the gate failed its next step is its
`reasons`, and a consumer should not have to decide which of two fields to believe. The sentence is the
one `status` prints, spelled by the same function and tested against it: this is the command an agent
runs to decide whether work may land, and the step that follows should not have to be parsed out of a
human line. The human form prints the same string as `next:`.

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

-   **No ref to be holding.** The verdict does not depend on any git-pair ref existing — there are none — so a clone that fetched the branch and the destination gives the same answer as a clone with everything else. The landing test reads the destination's tree (§13); there is no namespace to fetch before a gate can be trusted.

The assertion is about the commit, not the checkout: uncommitted changes are not in `HEAD` and
cannot invalidate a review of it, so a dirty working tree does not change the answer. `status`
reports the dirt, because `status` is observing.

Landing is not a command. The destination's tree is the record (§13), and §29 is the contract that says what a landing consists of: the gate, the merge, and nothing afterwards.

## 11.5 The agent skill

The agent contract (§22) is a product surface, and it ships inside the tool. `skills/git-pair/` holds it
in this repository, and the same bytes are compiled into the binary. Both halves matter: the directory is
what a person or an agent finds by walking the repository, and what a project commits into its own skills
directory; the compiled copy is what makes an installed skill checkable, because a binary installed with
`go install` has no source tree beside it. A skill that drifted from the commands it documents is worse
than no skill, because it is read as a promise — which is what happened to the first one, written beside
a different repository.

`git pair skill list` prints the skill this binary carries and every directory a harness would read it
from, each marked with one state:

| State | Means |
| --- | --- |
| `current` | the installed bytes equal this binary's |
| `stale` | a skill of the same name is installed there and its bytes differ |
| `absent` | the `git-pair` directory is not there |
| `unavailable` | the location has no home here — a repository-scoped row run outside any repository, or a user-scoped row with no home directory to resolve |

An existing directory holding anything other than this byte-for-byte set is `stale`, including a directory
that is empty: `absent` is about the directory, and everything else is about the bytes.

`current` is defined by bytes rather than by a version string, because the version string is written by a
person who is editing the skill and does not have to write it. `--json` prints `skill`, `version`,
`files`, `repository` and `targets`, each target carrying `harness`, `scope`, `path` (the skill's own
`git-pair` directory, inside the skills directory a harness scans), `state` and — only when non-empty —
`unmanaged`, the files in an installed skill that git-pair did not write.

`git pair skill show [path]` prints one compiled-in file of the skill, `SKILL.md` by default, so it can be
read or copied without a checkout. The answer is the document, so there is no `--json` form and the flag
says so on stderr.

`git pair skill install` writes the compiled-in skill into a skills directory as `git-pair/`, with its
`references/` beside `SKILL.md`. A skills directory is the container a harness scans, and the argument is
always that container:

| Flag | Default | Rule |
| --- | --- | --- |
| `--harness` | `agents` | one of `agents`, `pi`, `claude`; `codex` is accepted as a spelling of `agents`, because Codex CLI reads the `.agents/skills` locations. Anything else is a usage error naming what git-pair knows and pointing at `--dest`. The two directories per harness are those harnesses' documented discovery rules — the documents and the date they were read are recorded with `skillHarness` in `internal/cli/skill.go` and cited in the skill's install reference |
| `--scope` | `repo` inside a git repository, `user` outside one | `repo` writes under the repository root, `user` under the home directory. `--scope repo` with no repository is a usage error with both ways out in the message |
| `--dest` | none | the container to write into, for a harness git-pair has no row for. It names the destination outright, so combining it with `--harness` or `--scope` is a usage error rather than a precedence rule |
| `--dry-run` | off | resolve the destination and report what would be written, writing nothing. It makes the same refusal a real install would make, which is what makes it a preflight rather than a different command |
| `--force` | off | replace installed files that differ |

An install never deletes. Files that match this binary are left alone and reported unchanged; files that
differ are refused — exit 1, naming each one — until `--force` says otherwise; files git-pair did not
write are reported as unmanaged and kept, because a team's own notes beside the shipped contract are not
git-pair's to remove. A second run writes nothing and succeeds. Each file is written through a temporary
name and renamed into place, so an interrupted install cannot leave a `SKILL.md` that parses as the
beginning of one; the whole skill is not one transaction, and `skill list` reporting `stale` is what says so.

The closing advice is a fact about the destination, so it is printed only for a destination whose scope was
asked for: commit the directory for a repository scope, and nothing for `--dest`, because git-pair cannot
say who else can read a directory it was handed.

`--json` prints `skill`, `version`, `harness`, `scope`, `dest`, `path`, `dry_run`, `written`, `unchanged`
and `unmanaged`. `written` names the files the call wrote, or under `--dry-run` the files it would have
written. The two identity fields are empty when `--dest` was used, because neither was consulted.

`git pair skill agents-md` prints the pointer stanza for a harness with no skill discovery — the text an
agent reads from `AGENTS.md` or `CLAUDE.md` when nothing else would tell it this repository reviews changes
with git-pair. It is a short pointer, not a second contract: it says where the skill is and names the rules
an agent most often gets wrong. The stanza is prose in this repository (`skills/agents-md.md`) rather than
a Go string, so it is reviewed as documentation.

`list`, `show` and `agents-md` are read-only: they stat, read and print, and write nothing. `install` is
the only command in the family that touches the filesystem, and it touches nothing outside the directory it
names.

Every page under `skills/` is held to the command tree by the same check as this file and the README: a
command name or ref path the prose uses must be one the code answers to, and the command reference page
must name every command the code has.

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
integrating (an author's request, not a verdict — `git pair change integrate`, §9.9)
    ↓
landed (the destination carries the directory — a fact about a tree, not a state)
```

Any point above can also end: `git pair change abandon` (§9.7) records a terminal marker, and the
commands that move state refuse against the changeset afterwards.

The author's side of that loop is `git pair change ready`, then `git pair change wait` to learn that a
reviewer has acted, then `git pair change feedback` to read the submission before addressing it, then
`git pair check` and the landing itself (§9.5). Every step is a command an agent runs; the merge in the
middle is the one step git-pair leaves to git. `git pair change integrate` (§9.9) sits on that last step:
it does not perform the merge, it records that the author is handing the approved head over for one, so
whoever owns the destination branch — or the pipeline standing in for them — knows to act without asking.

Readiness also ends on purpose. `git pair change unready` (§9.6) writes a `working` marker and takes
the changeset back out of the queue, which is how an author says "not finished after all" instead of
leaving the offer standing while they keep implementing. It withdraws a declaration the same way, which is
what makes stopping an automatic merge need no rebase.

State comes from the commits on the branch. There is one fallback, and it is not a second source: where
a changeset has no branch — deleted after the work landed — the chain inside the destination is what
remains, and reading it can only report what the branch last recorded before it disappeared (§13.1). An abandoned changeset whose branch is gone has no
marker behind it and reports nothing, because an ending recorded only on a branch is an ending that went
with it. Nothing reads a ref to decide that a changeset is ready — git-pair writes none — and no command
moves state by moving anything but a commit.

Possible effective states:

```text
WORKING
READY
BLOCKED
FEEDBACK
APPROVED
INTEGRATING
```

Six states, seven markers: `working` is written by `change unready` (§9.6) and is the same state a
changeset with no marker derives; `abandoned` (§9.7) is the seventh and names no state — an abandoned
changeset reports `WORKING`, and the ending is reported beside it as `abandoned_commit`.

`INTEGRATING` is a state because it is a marker, and the safety property below admits no other kind of
state: it moves when `git pair change integrate` records a declaration, and at no other time. It is not a
second verdict — the approval underneath it is what permits a merge, which is why `check` (§11.3) reports
`ready` and `integrating` as two answers to two questions rather than folding one into the other. A changeset
that has been declared is also still whatever its approval says it is, and a superseded declaration leaves
the state to whatever superseded it.

There is no state for a landed changeset. Landing is not a marker: git-pair does not perform the merge
and does not need one to have happened in a particular shape. The destination carrying
`changesets/<id>/` is the fact (§13), and `git pair status` reports `landed`, `landed_commit` and
`landed_branch` beside the state rather than as another value of it. The distinction between `INTEGRATING`
and that report is the whole difference between a
request and a fact: one is a commit the author made, the other is a directory git put in a tree.

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
commit to be unreachable once the branch moves on.

---

# 13. Landings

A landing is not a command. There is no `integration record`, nothing to publish, and no ref to fetch:
a changeset is landed when the destination branch's tree carries `changesets/<id>/`, and that directory
is the whole record. git-pair reads that fact wherever it needs to know — `status`, `queue`, `check`, the
stack walk — and writes nothing to make it true.

The consequences are the properties this chapter is about: nothing about a landing can be stale, half
written, unpublished, fetched or unfetched, because nothing about a landing is stored. What is stored is
git.

## 13.1 The destination

Each changeset has a **destination**: the branch its landing is measured against. It is read per changeset
rather than guessed once for the repository:

1. the branch the changeset's own `base:` names, when it names one;
2. for a stacked child, where its landed parent went — a stack lands as a line, so the child's destination
   is the branch that carries the parent, and the walk names the parents it followed;
3. otherwise the repository's integration branch (§4): `--default-branch <ref>`, then
   `refs/remotes/origin/HEAD`, then a sole `origin/main` or `origin/master`, then a local `main` or
   `master`.

If none resolves the command refuses rather than inventing a trunk (§22's exit 2). A merge into a branch
that is *not* the destination is not a landing for these rules, and the changeset keeps reading as live
work: the queue lists it, `status` offers a next action, and no command will move its directory. That is
not a bug to work around with a record — there is nothing to record — it is the answer to the question the
checkout asked. The remedy is to tell git-pair which branch is the destination (`--default-branch
release/2.x`, or the changeset's `base:`), which is a fact about the repository rather than a claim about
a merge.

The destination is also what the automatic merge asks for (§9.9): `queue --json` prints it per declared
changeset as `destination`, and the same derivation answers both questions, so the branch a reviewer is
told about and the branch a pipeline would merge into cannot disagree.

## 13.2 What a landing reports

`git pair status --changeset <id>` reports the landing beside the state:

```json
{
  "landed": true,
  "landed_commit": "4f2c81a",
  "landed_branch": "main",
  "chain_base": "91bf204",
  "chain_head": "a7f3c98",
  "reviewed": true
}
```

- `landed_commit` is the commit that brought the directory into the destination: the first-parent commit on
  the destination whose own first parent did not carry it. That test is a fact about trees, so it survives
  every merge shape.
- `chain_base` and `chain_head` bound the run of work the destination carries behind the directory — the
  span a reviewer read, and where the markers they left sit.
- `reviewed` says the destination holds a permitting verdict (`approve`, or `feedback` where the repository
  accepts it) **and** the commit that verdict names. It is `false` for a chain with no approval, for an
  approval whose commit the destination does not carry, and for an approval that names no commit.
  `state` still reports the marker the chain carries, because what was written and what the destination can
  show are two questions, and a replayed landing is the case where they come apart.

Reading a landed changeset by id, with no branch carrying it, reads the same chain: the `state`, the
verdict and the thread files come from the destination's history. The stack above a child is walked the
same way — one entry per ancestor, each with the commit the destination carries it at — and the walk costs
a bounded number of git reads per step, which is what keeps `status` cheap as a repository grows.

## 13.3 What a landing keeps, and what it does not

A landing keeps what the merge carried:

| shape | chain | `reviewed` |
| --- | --- | --- |
| merge (`--no-ff`) | the reviewed chain, inside the merge commit | what the chain says |
| fast-forward | the destination's own line carries it | what the chain says |
| squash, rebase-merge, cherry-pick | none: `chain_base` and `chain_head` are `""` | `false` |

A squash brings the content and destroys the ancestry, so the approval, the intermediate review diffs and
the fix commits are gone from the destination — and they are gone from the destination's history, not from
the clone, which means no fetch recovers them. git-pair reports that as `reviewed: false` with an empty
chain. It does not guess a landing from a patch ID or a diff comparison: a tool that inferred integration
from content would be confidently wrong about every squash, and confidently *right* is the failure mode
here.

This is the honest limit of the model, stated rather than papered over:

> git-pair cannot tell you about a verdict the destination does not carry.

`LANDED UNREVIEWED` (§4) is that limit made visible at the surface that reviews work, and `change tidy`
(§13.5) is the mitigation for a repository that squashes: move the directory while the chain is still
reachable, in a commit that rides the normal review flow.

## 13.4 git-pair writes no refs

No command writes a ref, at any point in a lifecycle, for any reason: not at `change ready`, not at
`review submit`, not at landing, and not for an abandoned changeset. No command pushes. The only filesystem
writes git-pair makes outside `.git` are the changeset files it creates, and the only thing it moves is a
directory, with `git mv`, in `change tidy`.

This is pinned rather than hoped for. A hygiene test fails the build if shipped code shells out to
`update-ref`, `symbolic-ref` or `git tag`, and a contract test fails the build if any document names a path
under the namespace this tool used to own, because a document describing a ref the code no longer writes is
worse than no document.

The absence is the feature. There is no ref for a clone to be missing, no namespace for a CI job to fetch
before it can answer a question, no create-only pair to leave half-written by an interrupted run, no
fork-local history to publish, and no ref that can drift from the tree it describes. Everything that used
to be kept durable about a landing is kept by the destination branch, which already had to be durable for
the work to mean anything.

## 13.5 A landed changeset is finished

Because the destination's tree is what says so, the destination also closes the changeset: `change ready`,
`change unready`, `change abandon` and `review submit` refuse a changeset the destination carries, because
the review of finished work is over. The refusal happens before any commit is written, so a refused command
leaves nothing behind.

`git pair change tidy` is the one command that moves a landed changeset's directory, from
`changesets/<id>/` to `changesets/.landed/<id>/`, as one commit of renames on the branch it is run on:

```bash
git pair change tidy booking-transaction --dry-run
git pair change tidy --all-landed
```

It refuses, with one reason each and moving nothing: an id that has not landed, an id a changeset still in
flight is stacked on (the child's diff is measured against that directory), a dirty working tree, and an id
this branch does not carry (the move is a commit here, so the directory has to be here). A second run is a
no-op naming the directory it found. Tidying is itself a changeset — the reviewer sees the whole list as
renames in one span — and it reaches the destination through the normal flow, which is what keeps §4's
pruning rule from ever being exercised by hand.

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
      ✓ lock_test.ts +
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
leaves the half it started in; `gg`/`G` the two ends of the half holding them, because the two halves are
two windows of two heights and a jump to the end of the other one lands the reviewer on a list they were
not reading; `ctrl-d`/`ctrl-u` half a page and
`ctrl-f`/`ctrl-b` a page, counted in the window you are standing in. A half with no rows has no ends of
its own, and there the jump goes to the half that has them. Each half's shortcut bar names the keys
of that half and none of the others, which is what makes a key of the other half an absence a reviewer can
read rather than a keystroke that vanishes. A rule separates the list from the
shortcut bar. The tree is bounded by two rules of its own — the row that separated it from the box above,
and one under its last row, with the reviewed counter below that — and those two are its focus light:
single rules while the box or the diff holds the keys, double while the tree holds them. Two rules rather
than a frame because the cells a frame spends each side are columns, and the narrow terminal that needs
the regions told apart most is the one with none to spare. Each rule carries the count of what the window
hides at the end that count is about — `↑ 3` on the top rule for rows above the window, `↓ 11` on the
bottom rule for rows below it — padded with whitespace each side. The two counts of one region share a
numeric field as wide as the longer of them, so the two arrows sit in one column and the digits line up
under them (`↑  1` above `↓ 34`): the pair is the two ends of one thing, not two remarks a cell apart. It rides on the rule because a rule is
not a row: a count with a row of its own took one out of the window as soon as the list was scrolled, and
the row it took was the one a page had just landed the cursor on.

The box is capped at a third of the space the terminal gives and scrolls inside its own borders, so a
changeset with forty threads is a reason to read the box rather than a reason to hide the tree behind it;
each border counts what is off that end (`↑ 3` above the window, `↓ 3` below it), a note placed there
because a note with
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

A file row carries one character after its name for what the span did to that file. git's own status answers
it: `+` for a file the span created, `-` for one it deleted, `~` for one it moved. No sign means the span only
changed the file. That is what a span usually does, so a sign marks the exception a reviewer came to find.

The character is dim, and it sits after the name the way a directory's count sits after its name. It is a fact
about the file rather than part of its name. The move is git's rename detection, so a repository with
`diff.renames` off gets `-` and `+` for the pair git called two files.

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
advances to. It does not fold, group, filter, or renumber hunks, and it puts no colour of its own on
the author's rows, which is the shape PRD §3's refusal to build a diff renderer leaves: the pane is
for glancing, and reading a diff means opening it. The one place it does add colour, and rewrite one sign,
is the reviewer's own uncommitted work on the pane of a single file, which nothing in git's output could
attribute. The frame fills the terminal — the row
area holds the window's height and every row is padded to its width — so the shortcut bar
sits against the bottom edge rather than under a short list.

The prompts ask in a line of the footer and leave the screen otherwise as it was, so the diff column
survives a thread title being typed and a submit prompt being answered: the reviewer who pressed `T`
beside a diff comes back from the editor to that diff rather than to a screen that lost it on the way.

`p` moves the keys into that column and does nothing else, so a diff longer than the column is read
with the keys a diff is read with: `j`/`k`, `d`/`u` or `ctrl-d`/`ctrl-u`, `ctrl-f`/`ctrl-b`, `gg` and `G`
scroll the file already on show (`d`/`u` being `less`'s spelling of the same half page, kept because a
reviewer who reaches for it in a diff is reaching for something they know), `Enter` opens the file on show
— the difftool, or the editor for a file the span added, which has no other side of it to compare
against — and `/` searches it. `z` changes the shape of the screen rather than the keys: it gives the diff
the whole screen, and gives the screen back to whichever region asked for it — the preview column, or the
list column if `z` was pressed there — keeping the keys, the file and the place in the file.
These are the two shapes the terminal chooses by itself. Where a column was given and no longer fits, `z`
reports which way the window is short rather than closing the diff; where the terminal never had a column,
it gives the list back. The search reads a term in the pane's
bottom row — the row the note about the
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
disk, so nothing is at stake in the difference. Over the whole-screen overlay `q` gives the list back the
way `esc` and `enter` do, because that screen carries nothing but the diff: the list the key would leave is
not on it, and on the narrow terminal where the overlay is all the diff can be a `q` that quit would end the
session for a reviewer who wanted the list back. While the pane holds the keys nothing that changes the review can happen —
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

When the reviewer has edited a file without committing, the pane draws those edits into the author's diff at
the line numbers they carry: a line the reviewer deleted is drawn where the author's patch shows that line,
rather than once as the author's addition and once as the reviewer's deletion, and a line the reviewer added
follows the line it sits after. Each line the reviewer changed carries the marker that says whose it is and
nothing else on the screen carries it — not the context lines around them, which belong to the file, not the
author's rows, not git's rows about which file the patch is about. The counts for the reviewer's work are on the
title beside the file, where the pane puts counts anyway, so the number next to the path cannot be read as a
tally that includes the reviewer's typing. Git's output does not say who typed a line, so the marker is what
keeps the reviewer's work from reading as the author's; the reviewer's diff is taken from the revision under
review, not the span's start, so it cannot repeat the author's changes. The reviewed counter stays the span's: a
reviewer's typing does not change what has been reviewed. What the reviewer's diff holds is whatever is not in
the revision under review. That is the reviewer's typing while the author's work is committed — the state a
handoff leaves — and in a tree where the author is still working it also carries their unreviewed lines, which
the marker then claims. Nothing on this screen attributes an uncommitted line: the claim rests on git-pair's
model that the tree under review is the reviewer's, and a tree holding the author's uncommitted work is outside
it.

The merge is arithmetic on git's own numbers — the author's head and the reviewer's base are the same file — and
it needs the numbers of one file to be arithmetic at all. On a directory's pane, and on the changeset box, the
reviewer's rows stay a section of their own above the author's, with git's chrome and their own `@@` headers
untouched, because a number on that screen belongs to no one of the files shown. A file the pane reads as text
has no diff to merge into, so the reviewer's edits are drawn into the file's own text at the position each lands
in.

On the pane of one file the reviewer's rows carry more than the marker, because the pane has one more fact and
line numbers are how it knows it: an addition the reviewer made is blue, a line the reviewer deleted that the
span had added is purple and leads with `×` rather than git's `-`, and a line the reviewer deleted that
predates the span is amber. Git draws the same `-` for both kinds of deletion, and they are not the same
thing — one undoes reviewed work — and the intersection of the two diffs' line numbers is what tells them
apart without a claim about what their text has in common. Because that arithmetic needs every number to
belong to one file, a directory's pane and the changeset box keep git's colours and the marker alone. The
bytes git printed stay on the row as what it is a line of, so a search matches `-gone` on a row the pane
painted with `×`.

The list column's other half is documents rather than files, and the pane reads them the same way it reads
a diff: ABOUT.md and each thread, when the box's cursor is on them, are shown as their own text with the
file's own line numbers, and the Threads heading is every thread in the order the box lists them, each named
above its text. The pane is the screen's place for reading text, and a changeset document is the one thing on
this screen that is pure text to read; a diff of one against nothing would report every line as added, which
is true and carries no information about what it says. The header counts what is on show in the one place
counts sit, beside the name — git's `+N −M` for a diff, lines for a document, threads for the heading — and
the pane follows the box's cursor the way it
follows the tree's, which is what makes the two halves one column. `Enter` in the pane opens what is on show
with the rules the row itself would apply: the difftool for a diff, the editor for a document, and the row's
own refusal where history makes the editor the wrong tool. Over a historical span the document is still read
from the working tree — it is the file the editor would open — so the header says `working copy` rather than
letting a reviewer read history that is not there.

Two file rows read as the file rather than as a patch: one the span created, one it moved unchanged. A new
file's patch is its own text with a `+` on every line. An unchanged move's patch is two lines about a path.
Every other file row keeps its patch, because a rename with edits has edits to show. A deletion is the only
place the removed text still is, so its row keeps the patch too.

The text is the file at the span's head, not the working copy. What the reviewer has edited in it without
committing is drawn into that text where it lands, in the order git wrote it: the `-` line at the number that
line has in the file under review, then the `+` lines that replaced it, unnumbered — an added line is in no
file anyone is reviewing. The pane moves those lines to where they belong and reorders nothing, which is what
keeps it the same patch `d` and `git pair diff` show. Each of those rows carries `← you`, the same mark that
names the reviewer's rows in the diff's own section, and the pane's word rather than git's colour: in a pane
that reads a file as a file, every marked line is the reviewer's, and that is the fact the marker states. The
header says `you edited it` with git's counts for that section. A move names
the path it came from, which the tree's `~` has no room for. `Enter` in the pane keeps making the row's own
choice: the editor for a file the span added, because the comparison has nothing on one side of it, and
the difftool for an unchanged move, because there the rename is the comparison. `d` is the key that asks
for the patch of either.

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
         shares with the other half; gg and G are the two ends of the half holding them
Tab      toggle the keys between the list column and the diff where there is room for one (shift-tab the
         other way); f names the file tree from wherever the keys are
h/l      fold and unfold the directory under the cursor (left and right arrows do the same);
         h from a file or a closed directory moves up to the directory holding it
c        fold the whole tree, and open it again
Enter    activate the row: the editor for a file the span added and for nothing else, difftool
         for every other file, fold/unfold for a directory, the decision
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
z        read the row the keys are on over the whole screen, taking the keys with them; `z` again, or
         `Esc`, gives the list back
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
Enter    open what the pane is showing: the difftool, or the editor for a document and for a file
         the span added — the same choice the row makes when the key is pressed on it
z        give the diff the whole screen, or give the screen back to the region `z` was pressed in —
         the preview column, or the list column if the keys never left it — keeping the keys,
         the file and the place in it. Where a column was given and no longer fits, it says so.
         From the list column it is the row under the cursor that goes to the whole screen
Esc      hand the keys back to the list column, leaving the pane where it is in the file
Tab, f   move the keys back to the list column, as they do from anywhere in it
q        in the pane, quit — from here as from anywhere else; over the whole-screen overlay, give the
         list back, as esc and enter do

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
q        quit, from whichever region holds the keys — except over the whole-screen preview, where it
         gives the list back
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

Landing has no command of its own (§13), so the question the second bullet used to answer no longer
exists: there is no recorder left to run the diagnostic as a final check, and one point in the lifecycle
remains where a survivor is acknowledged — the offer for review. Two reasons kept it out of the gate:

-   `git pair check` (§11.3) cannot pass a changeset whose content moved after the review that permits it,
    so it does not need this diagnostic to stop unreviewed work reaching a merge — and a gate with no
    override flag is the wrong place to acknowledge a surviving addition, because the flag would have to exist there too.

## 19.4 Rationale

The mechanism intentionally avoids requiring special review-comment syntax.

Git itself already knows which lines were added by the review commit.

The invariant is:

> Before advancing past the latest review boundary, every addition introduced by that review must either have changed/disappeared or be explicitly acknowledged as intentionally surviving.

## 19.5 Resolving a surviving addition

§19.2 forces inspection. What an author should then do with a reviewer comment inside an implementation file
— a path outside `changesets/<id>/`, which is the boundary §19 uses for blocking versus non-blocking — is
policy, and the policy is:

-   The author applies the feedback and then removes the reviewer's inline text, in the same commit. A
    comment that outlives the work it asked for is feedback the repository has not finished consuming.
-   The author does not copy the comment before deleting it. Git history already holds the exchange: the
    review commit introduced the line (§19.1) and a later commit removed it.
-   The author records the substance when it needs further discussion, elaboration, or collaboration, and not
    otherwise. There are two homes: an `Addressed feedback` section of `ABOUT.md` (§6), and a new thread
    (§7). The choice follows the content — a thread is a conversation, `ABOUT.md` describes the change — and
    neither is a requirement to fill in. `init` does not scaffold the section.
-   An addition the author keeps is not resolved by deletion. It survives, and §19.2's override acknowledges
    it. That is the normal outcome for a direct code edit (§20) the author accepts.

Nothing enforces the removal. Deleting an addressed comment makes the addition disappear from `HEAD`, so the
diagnostic stops reporting it, and an author can delete a comment without doing the work it asked for. The
diagnostic guarantees inspection; §19.4 states the invariant and this section states what inspection is for.

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

The absence of a distinction does not make the two answers equal. §19.5 states the expected one for a comment
the reviewer left in an implementation file: apply it, then delete it. An edit that is now the code the author
wants is the case for the override.

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

Each level names the one below it (§5):

```text
booking-transaction:
  base: main

booking-transaction-tests:
  parent: booking-transaction
  parent-changeset: booking-transaction

booking-transaction-ui:
  parent: booking-transaction-tests
  parent-changeset: booking-transaction-tests
```

A base named at `init` is read by the same rule. When `--base` names a branch carrying exactly one unlanded
changeset, the work is stacked on that branch, and `init` records the pair above in place of a plain `base:`.
The two are not interchangeable: a `base:` keeps naming the branch after the work on it lands, which is the
state in which every command that reads it names finished work as the destination, while `parent:` is read
against the destination and answers the same question once the parent's branch is gone. Two or more unlanded
changesets on that branch is not a guess `init` makes — one may be the parent and another a sibling sharing
the branch — so the base stands as written and every candidate is named for the author to pick. `check`
recommends the same declaration for a changeset whose file predates this; it recommends, and never refuses.

Each branch has:

-   its own `ABOUT.md`,
-   its own threads,
-   its own review history,
-   its own review outcome,
-   its own landing, read from the destination when it has one (§13).

There is no shared review content between stacked branches in MVP.

## The conservative approval rule

A parent branch moves while its child is being read, and none of it is visible in the child's
history: the child's commits are unchanged, so the drift test and the rebase test both pass. The
rule is therefore:

> Any change to the parent branch after a child is approved invalidates the child's approval.

This includes parent implementation commits, parent review commits, parent approval commits,
rebases of the parent, and merges into the parent. It is intentionally conservative: git-pair does
not attempt to tell a metadata-only parent commit from an implementation change.

A review submission for a stacked changeset records the parent's tip in `Review-Parent-Head`,
beside `Review-Head`. That is what makes the question askable afterwards, and it is written only
for a changeset that is stacked. `status`, `queue` and `check` compare the recorded tip with the
branch's current one.

**The reason names the kind of movement** — implementation commit, review commit, approval,
rebase, or merge — because a rule that reports only "the parent moved" reads as arbitrary, and an
author who thinks the gate is being pedantic stops trusting it.

A submission whose approval recorded no parent tip is not refused: the absence says the trailer
was not written, which is not evidence that the parent moved. `status` says what is missing.

The rule is about a parent **branch** moving. A parent that *lands* is a different event, and it is
judged on content rather than on movement — see "When the parent lands" below.

## When the parent lands

Landing does not delete anything, so the ordinary state of a child whose parent has landed is a parent
branch that is still there, still at the tip the approval recorded, holding work that is already in the
destination. That is the case the tip comparison cannot see: `Recorded == Tip` reads as "nothing happened"
in the one moment where the work finished. The record is the fact, and the branch is where the record used
to live, so:

-   the child's measurement base becomes the parent's landing commit while the parent's branch is still
    present, not only after it is deleted: the destination carries `changesets/<parent>/`, and reading the
    commit that brought it in answers the same question the parent's ref used to. A destination this clone
    cannot read keeps the branch base and says which half is missing,
    rather than failing with `unknown revision` on a base another clone wrote down.
    approval yet. Without an approval it is a note and never a reason: a changeset that has not been
    offered has nothing for a parent to invalidate. `status --json` carries `parent.landed`,
    `parent.landed_commit`, `parent.landed_in_default_branch` and `parent.stale_branch`; `check --json`
    carries `parent_landed`, `parent_landed_commit` and `parent_stale_branch`.
-   the step is printed as the command, because "rebase onto it" is a sentence the author has to translate
    into three arguments and gets wrong: `git rebase --onto <landing> <parent-branch> <child-branch>` while
    the child's head does not carry the landing, and `git branch -D <parent-branch>` once it does — with the
    worktree named, and `git worktree remove <path>` as text, when another worktree holds that branch and the
    delete would stop there. git-pair runs neither (§26).

**An approval follows the base only when the content did not move.** `base...head` is the difference between
the tree at the merge base and the tree at head, and head is the same commit under both bases, so the two
diffs carry the same content exactly when the two merge bases carry the same tree — two `merge-base` calls
and two `rev-parse`s rather than a patch comparison on every read. Identical, and the approval stands:
nothing the reviewer looked at has changed. Different, and it does not: a squash, a rebase-merge or a
cherry-pick moved the content the review saw into commits the child never had, and `check` refuses with the
reason naming that. A comparison the clone cannot make answers "different", which is the direction that asks
a human to look again rather than the one that lets an unreviewed diff through the gate.

A child landed without rebasing carries a chain that includes the parent's commits. That is expected: the
chain is the history of the branch that was merged, and the parent's own landing is a separate fact about
a separate branch.

### Where a child is measured is not where it lands

Relinking answers "what is this diff measured against", and the answer for a stacked child is a commit:
the parent's landing in the destination. That answer is right for the diff and wrong for a destination — a
commit names no branch anything can merge into.

So the destination is read from the tree instead. The landing commit carries the parent's
own `CHANGESET.yaml`, and the base written there is the branch the parent's work was measured against — the
branch the parent *said* it was going to. The same rule is applied at each level, so a three-deep stack ends
on the branch under all of it; the walk is bounded and cycle-guarded, because that yaml is committed content.
It appears as `destination` in `git pair change integrate` (§9.9) and `queue --json` (§10.6), with the note
beside the answer saying the destination came from the branch its landed parent was based on.

Two limits belong with the rule. "Said it was going to" is a limit: a parent landed somewhere other than its
own base makes this answer wrong. Nothing hides that: the answer is printed with the rule that produced it,
in `destination_source` and `destination_via`, at every surface that reads it, so a reader sees which parents
were walked and can correct the record with `--default-branch <ref>` or the changeset's own `base:` (§13.1).
And nothing is invented: a walk that cannot resolve an answer falls back to the default
branch and says so in `destination_source` and `destination_unreachable`, rather than reporting a guess as a
fact about the work.
fact about the work.

**A child is not declared until its parent has landed.** `git pair change integrate` (§9.9) refuses a child
whose parent is not landed, and refuses a parent branch that records no
`parent-changeset:` because there is no parent to look for. That rule belongs to the request and not to the
gate: `check` answers "may this merge" for a person who can merge a child onto an unlanded parent and
stand behind the result, while a declaration asks for a merge nobody will be asked again about, onto a
branch review can still rebase, amend or block an hour from now — and a merge into it would sit on history
that may stop existing. Refusing the request is the whole
mitigation; the human path stays open, so nothing is lost but the automation.

## When the parent is abandoned

A parent that was abandoned rather than integrated leaves the child **unreconciled**. git-pair does
not silently reparent a child: the reason refuses and names the step,

```bash
git pair init --parent <branch> --set-parent
```

and the author chooses the new base. A parent branch that is gone with no landing is
reported the same way, because from this clone the two look alike.

## Reading the stack

`status` prints a `Stack:` section naming the parent branch, its changeset, the tip the approval
recorded and the parent's current tip, or the parent's absence.

Once the changeset has landed the section gains the other half of the same question, because the landing
names the commit the work became and nothing about whether any of it reached the integration branch: one
line per ancestor — nearest first, from `parent-changeset:` through each ancestor's own landing — naming the
commit the destination carries that ancestor at, and whether the branch it was stacked on is still in this
clone. An ancestor with no landing is printed as absent rather than skipped: that is the finding. The walk
is bounded — `CHANGESET.yaml` is committed content, and a `parent-changeset:` edited into a loop stops the
walk with a note rather than a hang, which is also why the note exists at all. `--json` reports the same
walk as `stack` (never null) and `stack_note`.

Reading a changeset by id from the destination's chain relinks the same way the branch path does: the
parent's landing commit in the destination becomes the base — the same boundary the branch was — whether or
not the branch is still in this clone. The branch name stays recorded in the changeset, so the cases stay
tellable apart: a parent that landed, a parent whose branch this clone has never seen, and a parent that
moved are three different sentences.

`queue` lists READY branches, which by definition have no approval to invalidate, so it notes instead the
rows sitting on a parent that has moved ahead — the diff a reviewer is about to read is measured against a
parent that is no longer current — and the rows sitting on a parent that has **landed**, which is the case
`behindParent` cannot see: it counts parent commits the branch does not have, and a merge into the
destination leaves the parent's branch with none. Both print as notes, never as rows or reasons, and
`--json` carries them as `parent_notes`.

---

# 22. Agent Contract

Agents should interact with `git-pair` through stable non-interactive commands rather than manually interpreting the marker and trailer format.

Primary agent commands:

```bash
git pair init --base <ref>
git pair status --json
git pair diff
git pair change ready
git pair change unready
git pair change wait --json
git pair change feedback
git pair check
git pair change integrate
```

There is no landing command for an agent to run or to read about: git-pair writes nothing at landing (§13).
`--json` has one contract. Every array it prints is `[]` for "asked, and none", never null. A job then
branches on a field, not on the presence of a key. Two fields answer null on purpose.

`latest_review` is an object that does not exist before the first review. `uncommitted` reports that the
question belongs to a checkout this command does not stand in. `git pair change feedback` and
`git pair diff` have no JSON output at all. They print the report itself, and `--json` says on stderr that
it changed nothing.

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
14. where the repository's flow asks for it, run `git pair change integrate` (§9.9) to hand the approved
    head over for merging. It is a request written as a commit: it performs no merge, writes no ref, and
    pushes nothing, and it refuses anything `check` would refuse — so an agent may run it unconditionally
    after a passing gate and read the refusal if there is one.
15. stop there. Landing is not the agent's step: the merge is ordinary git run by whoever owns the
    destination branch (§13). There is nothing to run afterwards — the destination carrying
    `changesets/<id>/` is what makes the work landed.

An agent that rebases a branch after an approval has invalidated it, and `check` will say so (§12). The
response is to re-offer the work — `change ready`, then wait for a reviewer — and not to argue with the
gate: git-pair does not read an approval of one commit as approval of its rewritten successor, and an
agent editing a `Review-Head` trailer to make the comparison line up would be recording an approval it
did not receive. Where the history has to be tidied, tidy it before the handoff.

`git pair status --json` remains the way to check state without blocking. `git pair diff --unreviewed` is the reviewer's span command; an author consuming a newly submitted review uses `git pair change feedback`.

When the author needs to keep implementing after handing off, `git pair change unready` (§9.6) withdraws
the offer before that work starts. It succeeds when there is nothing to withdraw, so an agent may run
it unconditionally rather than branching on state. It withdraws a declaration too, which is the way to
stop a pipeline from merging work that has just gone back in review.

An agent must **not approve its own work**.

Approval remains a reviewer action, and so is the merge: git-pair hands the work on and stays out of
the destination branch (§9.5). Nothing an agent runs makes a changeset landable — `git pair check` is
what asserts the gate, and it accepts `feedback` only when told to — and nothing an agent runs preserves
history on the reviewer's behalf: the chain is whatever the merge carried (§13.3).

There is no recording step, so there is nothing for the agent to assert about the merge: landing is a
fact about the destination's tree, and `git pair check`'s `integrated` is the field a pipeline reads to
learn the work is already finished.

Nothing of the landing can be skipped, because there is no step; what can be wrong is the review, and
that is what the finding is for. A changeset directory in the destination with no approval it can show is
either a squash that left the review on a branch that no longer exists, a merge that arrived without one, or
a landing that replayed the run and so carried the approval without the commits it named. `git pair queue` prints it under
`LANDED UNREVIEWED`, and `git pair status` says the same on a branch
carrying no work of its own (§4's *Landed, unreviewed*). A supervisor agent that runs the queue between
steps sees a landing whose review did not reach the destination instead of a changeset that quietly
disappeared, which is what makes the sequence above a contract rather than an expectation.
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

`git pair queue` provides a deterministic query for actionable review work.

This enables future automation such as:

-   desktop notifications,
-   Zulip/Slack notifications,
-   cron jobs,
-   agent supervisors,
-   developer dashboards.

The machine-readable interface should be sufficient for external automation:

```bash
git pair queue --json
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

and the history of how the work got there rides in the destination branch, as far as the merge carried
it (§13.3).

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

There is no carve-out. **git-pair may not push anything**, at any point in a lifecycle. There used to be
one, for a namespace of landing records, and it existed because that memory was local: a record that never
left the clone ended with the laptop. The memory of a landing is now the destination branch, which lives on
the remote by definition, so there is nothing left to publish — and a landing that is not on the remote has
not happened, which is the correct thing for a tool to report rather than to repair.

- nothing in shipped code invokes `push`, `update-ref`, `symbolic-ref` or `git tag`; the hygiene test fails
  the build on any of them, and a planted call site anywhere fails it too;
- `merge`, `rebase`, `reset`, `switch`, `checkout` and branch management stay forbidden exactly as above;
- the only thing git-pair moves is a directory, with `git mv`, in `change tidy` (§13.5).

`git pair change integrate` (§9.9) is inside this list, not an exception to it. It declares that a merge is
wanted; it performs none, pushes nothing, and writes no ref. The distinction is what
makes the feature compatible with the rule at all: git-pair has never merged anything, and the command that
asks for a merge is the reason that stays true — the merge is still ordinary git, performed by whoever owns
the destination branch, and `git pair check` (§11.3) is still the gate they run. A future command that
performed the merge would need a carve-out of the kind this list no longer has.

---

# 27. Likely Post-MVP Features

Potential later enhancements include:

## Deferred by review-architecture-v2

Explicitly given up while the durable-ref layer was being taken out, each with what it would cost:
-   **Per-review anchors.** Attaching threads and marks to a specific review submission rather than
    to the changeset. Cost: the anchor has to survive the rewrite it describes — a rebase renames
    every commit around it — so it means either a content hash beside the anchor or a rule about
    which anchors die, and both are user-visible in the middle of a review.
-   **A durable memory of a landing of its own** — a forge-protected ref family, or an out-of-band notes
    ref, holding the reviewed chain beside the work. Under this model there is no local memory of a
    landing to publish, so the question is whether git-pair should keep one at all. Cost: the layer this
    design just removed — create-only rules, publication consent, half-written pairs, unfetched
    namespaces, and a carve-out in §26 for the one command allowed to push. If chains lost to squash
    merges become the problem that justifies it, `change tidy` (§13.5) answers it with commits.
-   **Patch-equivalent carry-forward of approvals.** Letting a child's approval survive its parent
    landing when the child's diff against the new base is provably the diff that was reviewed
    (§21). Cost: a patch-id equivalence rule that has to be right, because every case where it is
    wrong approves code nobody read. The conservative rule is kept instead.
-   **Reviewer identity and thread resolution state.** Per-reviewer permissions, "who is this
    comment from" as data, and resolved/unresolved threads. Cost: identity is not in git's commit
    model in any way git-pair can enforce, and thread state is state — it wants a ref, a file, or a
    server, all of which this product's thesis refuses while the commits can carry the answer.

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
git pair queue --global
```

## Remote record enforcement

There is nothing local left to enforce. A landing is a branch on the remote, and the remote already
governs branches; the durable refs this section was written against are gone (§13.4). What stays deferred
is the family of mechanisms that would make git-pair responsible for a forge or a server rather than for a
repository:

-   pre-push hooks,
-   server-side validation,
-   forge-level protection of anything outside `refs/heads/*` and `refs/tags/*`,
-   protection against destructive rewrites of a changeset that has landed: the client refuses to write
    markers on one, and refuses to force or rewrite anything, which is not the same as being unable to.

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
Enter    activate the selected row: the editor for a file the span added, otherwise the difftool,
         or read a changeset document (whichever gives a real comparison), collapse the thread list,
         create a thread
d        open the difftool for the selected row, falling back to the editor with a note
e        edit the selected file or document
p        move the keys into the diff preview column; inert where the diff already has them.
         Where there is no second column, it takes the screen for the diff
z        take the whole screen: from the diff, give it the screen or give the screen back to the diff;
         from the list, open the row under the cursor over the whole screen and give the list back
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

git pair init --base main
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
git pair queue
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

The owner lands it with ordinary git, and the destination branch is the record of where it went:

```bash
git pair status --changeset booking-transaction   # landed at <merge commit>, with the chain the merge carried
```

## The landing contract

Landing is three steps, in this order, and nothing else:

1.  `git pair check` — the gate, run by the author and by CI alike.
2.  `git pair change integrate` — the author declares this head ready to be merged (§9.9), on the branch,
    in a commit. A repository that keeps a person in the loop can skip it and nothing else changes; a merge
    performed without it is a merge nobody asked for, which is why CI's gate is `jq -e '.ready and
    .integrating'` rather than `ready` alone.
3.  The landing itself, with **ordinary git**: merge, squash-merge, or whatever forge button the
    repository uses. git-pair writes no merge, no ref, and nothing afterwards.

The declaration changes who waits, not who decides. Step 2 is a commit the author makes; step 3 is still
performed by whoever owns the destination branch. There is no step 4: the destination carrying
`changesets/<id>/` is the fact that the work landed, not a fact that needs somebody to state it (§13).

**What the merge carried is what the landing keeps.** A merge brings the reviewed chain with it, so
`git pair status --changeset <id>` reads the approval, the chain and the thread files out of the destination
afterwards. A squash, a rebase-merge or a cherry-pick brings the content and leaves the chain behind, and the
status report says `reviewed: false` with an empty chain (§13.3). Where that loss matters, the order that
preserves the history is: land, then `git pair change tidy` the directory into
`changesets/.landed/<id>/` from a branch that still holds the chain, before the branch goes. A repository
that squashes and tidies keeps the review history in commits; a repository that squashes and deletes does
not, and no later command can recover it.

**A pipeline can run step 3.** This repository carries one shape of that: a workflow file whose only
job is the triggers, the permissions and the build, and a shell script that holds the sequence — the gate of
step 1, the head's own checks proven green where the repository has them (the forge's answer, not git-pair's,
and asked about the commit the declaration names), an ordinary `git merge --no-ff` into the destination the
queue names, and the push.
`scripts/gates/ci-integrate.sh` replays that job against scratch remotes, which is what keeps the example
true without a runner. It is an example and not a contract: the merge is ordinary git, no part of it is a
git-pair subcommand (§26), and a repository that lands with a forge button instead needs only the same two
fields (§11.3) and the same push.
---

# 30. Product Thesis

`git-pair` treats Git itself as the protocol for agent-era peer review.

The codebase contains the review context. Git commits establish review boundaries. Markdown provides natural high-level conversation. Existing editors and difftools remain the code-review surface. Nothing beyond git carries the review: the destination branch's tree says what landed and the merge carries the chain as far as it can (§13), so no ref of the tool's own is needed to keep the history findable.

The tool's role is deliberately narrow:

> **Coordinate the review lifecycle, make the right Git state easy to understand, and remove the ceremony between an authoring agent and a human reviewer.**
