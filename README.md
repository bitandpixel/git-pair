# git-pair

`git-pair` is a local-first peer-review tool for human-plus-agent pairs. It puts a thin
review protocol on top of ordinary git commits, files, refs, editors and difftools rather
than replacing any of them: a changeset directory holds the change description and review
threads, lifecycle transitions are commits carrying `Review-*` trailers, review-relative
diff spans answer "what happened since my last review", and the two refs
`git pair integration record` writes at landing — `refs/git-pair/archive/<id>` and
`refs/git-pair/integrations/<id>` — keep the complete unsquashed history reachable once the branch has
been squash-merged and deleted. All review state lives inside the repository, so no code path talks
to a forge.

## What it is not

The MVP deliberately does not:

- implement a source-code editor, a full diff renderer, or anything that competes with
  Vim/Neovim, `git difftool`, git, or GitHub/GitLab
- create or manage pull requests, merge branches, squash branches, or push
- run coding agents or CI/CD
- keep inline-comment databases or GitHub-style comment anchoring
- treat per-file review checkmarks as review state — they persist locally under the git directory
  so a review can be resumed, and no command reports them
- model multi-reviewer permissions, multi-author semantics, or complex stacked-branch graphs;
  a stack is a `parent:` value in `CHANGESET.yaml` plus one rule (§Stacked): any parent movement
  ends the child's approval, and the reason says what kind of movement it was
- distinguish a human's code edit from a human's comment
- send notifications, or enforce review refs remotely

## Install

Requires Go 1.27 and `git` on `PATH` (developed against git 2.43.0). The repository pins the
toolchain in `.mise.toml`:

```bash
mise exec -- go build -o ~/bin/git-pair ./cmd/git-pair
mise exec -- go install ./cmd/git-pair          # into $(go env GOPATH)/bin
```

Without mise, any Go 1.27 toolchain works. Build it with the name `git-pair` and git picks it up as
a subcommand, so `git pair review open` and `git-pair review open` are the same program; the docs use
the `git pair` form throughout. `git-pair --version` prints `git-pair version 0.1.0`.

## Quickstart

The PRD §29 loop, captured from a real session in a scratch repo; the only hand-authored
content is the `ABOUT.md` text and the reviewer's two inline comments.

```bash
$ git switch -c booking-transaction

$ git pair init --base main
created changesets/booking-transaction/
created changesets/booking-transaction/CHANGESET.yaml
created changesets/booking-transaction/ABOUT.md
committed 9f1c2de

Next: fill in changesets/booking-transaction/ABOUT.md, commit it with your implementation, then run `git pair change ready`.
```

`CHANGESET.yaml` records the changeset's `id` and its `base`;
`ABOUT.md` is a scaffold with `Summary`, `What changed`, `Design decisions`, `Validation`,
`Known limitations` and `Open questions`
headings. Fill it in, commit it with your implementation, then:

```bash
$ git pair change ready
Ready: booking-transaction
  head:  8065dae
  base:  main
  queue: `git pair queue` now lists this changeset

$ git pair queue
READY FOR REVIEW

booking-transaction
  base: main
  branch: booking-transaction
  ready: 0s ago
  head: 8065dae
```

The reviewer runs `git pair review` for the TUI, or works from the CLI. Here they edited
`src/service.ts` directly, added a thread, and blocked:

```bash
$ git pair review thread "concurrency tests"     # then edits the file in $EDITOR
Created thread changesets/booking-transaction/concurrency-tests.md

$ git pair review submit --block
Review submitted: booking-transaction
  outcome: block
  commit:  332887c
  files:   2 changed
           changesets/booking-transaction/concurrency-tests.md
           src/service.ts
  next:    author: `git pair change feedback`, address it, then `git pair change ready`
```

The review is an ordinary commit: `review: block booking-transaction` followed by a blank
line and the `Review-Outcome: block` / `Review-Changeset: booking-transaction` trailers.

The author waits for that submission and reads it. `change wait` blocks until a review makes
the changeset actionable and names the commit; `change feedback` shows the submission itself —
the thread, the `ABOUT.md` edits, and any code the reviewer touched:

```bash
$ git pair change wait --json
{
  "changeset": "booking-transaction",
  "previous_state": "READY",
  "state": "BLOCKED",
  "review_commit": "332887c",
  "review_commit_full": "332887cf6413e66d45d0ad5d59d93f7d30484ffd",
  "ref": "HEAD",
  "fetches": 0,
  "waited_seconds": 0,
  "timed_out": false,
  "next_action": "`git pair change feedback`, address it, then `git pair change ready`"
}

$ git pair change feedback --stat
git pair change feedback: review 332887c (block) on booking-transaction
 changesets/booking-transaction/concurrency-tests.md | 12 ++++++++++++
 src/service.ts                                      |  1 +
 2 files changed, 13 insertions(+)
```

It returns here at once because the submission is already committed in this clone. Run it
while the reviewer is still working and it polls instead, exiting the moment the state leaves
`READY`; add `--fetch` to pull before each check — that is how a review pushed from another
clone reaches you — `--interval` to change how often, and `--timeout` to give up (exit 1,
still reporting where things stand). `change feedback` is the author's "what did the reviewer
just tell me?". It is not the reviewer's command: right after a submission the
reviewer's own span, `diff --unreviewed`, is empty because the submission is the newest commit.

The author addresses the feedback in code, `ABOUT.md` and the thread. What the reviewer sees
next is the review-relative span, where review lines are deleted — that is how a reviewer sees
feedback being consumed:

```bash
$ git pair diff --unreviewed -- src/service.ts
git pair diff: last review..current
diff --git a/src/service.ts b/src/service.ts
index 942e6f4..82542b6 100644
--- a/src/service.ts
+++ b/src/service.ts
@@ -8,4 +8,3 @@ func enroll() {
 }
 
   // Please use a transaction here
-  // What happens if these execute concurrently?
```

Returning to `ready` is refused while review lines survive untouched. Nothing is committed,
and the command exits 1:

```text
$ git pair change ready

Cannot mark changeset booking-transaction ready.

2 additions from review 332887c still survive unchanged:

src/service.ts:10
    // Please use a transaction here

src/service.ts:11
    // What happens if these execute concurrently?


Review these additions before continuing.

To intentionally preserve them:
  git pair change ready --allow-surviving-review-additions

Also still present from review 332887c (changeset artifacts, not blocking):
  changesets/booking-transaction/concurrency-tests.md:1  # concurrency tests
  changesets/booking-transaction/concurrency-tests.md:4  ## Response
  changesets/booking-transaction/concurrency-tests.md:6  Will do.
git-pair: cannot mark changeset booking-transaction ready: 2 review addition(s) from 332887c still survive unchanged
```

After resolving the remaining comment `git pair change ready` succeeds and the changeset is
back in the queue. The reviewer re-reviews with `git pair review reopen`, which starts on what
changed since their submission, and approves:

```bash
$ git pair review submit --approve
Review submitted: booking-transaction
  outcome: approve
  commit:  0eaad3b
  files:   none (recorded as an empty review commit)
  next:    author: `git pair check`, then merge into main with ordinary git, then `git pair integration record`, then `git pair integration publish`
```

The submission is a marker commit and nothing else. No git-pair ref is written while work is in
flight — the branch holds the whole implementation-and-review chain, and a ref tracking it would be a
staler copy of a story the branch tells better.

The gate is its own command, because deciding and observing are different jobs: `status` reports
what the history says, `check` asserts it, and CI reads the exit code.

```bash
$ git pair check
OK: booking-transaction is integration-ready
head:  0eaad3b
next:  `git pair check`, then merge into main with ordinary git, then `git pair integration record`, then `git pair integration publish`
```

Then the owner lands it, with ordinary git. Squash, rebase-merge, plain merge — git-pair has no
opinion and takes no part; it neither runs a merge nor derives one, because squash and cherry-pick
destroy the ancestry that would have said so.

The three steps are the whole contract — `check`, the landing with ordinary git, `integration
record` — and the order of the last two is not free: **record before tidy**. The record asks which
head was reviewed, and the branch that still carries it is where the answer usually comes from — so
the order that never costs you a flag is record, then delete.

What the branch is needed *for* decides whether deleting it costs anything. A merge carries the
reviewed chain inside itself, as the side the merge brought in, so a merge landing is still
recordable after `git branch -D` and needs no flags; a fast-forward carries it in trunk, and is
recordable once the approval marker on the chain says the work was reviewed there. A squash, a
cherry-pick and a rebase-merge keep the chain on no commit that survives, so for those the branch is
the only source of the answer: delete it first and the same fact has to be supplied by hand with
`--source` and `--commit`, or fetched back, or it is gone.

One command records where the work went, and it is the only git-pair ref write in the whole handoff:

```bash
$ git pair integration record --source 0eaad3b --commit 4f2b8c1 --target main
booking-transaction: recorded 4f2b8c1 as the integration of 0eaad3b
  archive:     refs/git-pair/archive/booking-transaction -> 0eaad3b
  integration: refs/git-pair/integrations/booking-transaction -> 4f2b8c1
  verified reachable from main
  configure:   git pair integration configure adds the durable refspecs to remote.origin.fetch
               and remote.origin.push, so an ordinary fetch keeps this clone able to tell published from
               unpublished, and an ordinary push keeps what it records published
  next:        git pair integration publish booking-transaction, then the branch can go
```

Then publish, and only then tidy:

```bash
$ git pair integration publish
booking-transaction: published to origin (archive + integration)
  configure:   git pair integration configure adds refs/git-pair/*:refs/git-pair/* to remote.origin.push,
               so an ordinary push carries these refs from now on
```

Publishing every build is a repository's decision, and one command makes it git's own:

```bash
$ git pair integration configure
origin: configured for the durable refs
  fetch: added +refs/git-pair/*:refs/remotes/origin/refs/git-pair/* to remote.origin.fetch
  push:  added refs/git-pair/*:refs/git-pair/* to remote.origin.push
  next:  git pair integration publish, to send what this clone already holds
```

After that an ordinary `git fetch` keeps the clone able to tell published from unpublished, and an ordinary
`git push` carries `refs/git-pair/*` with the branches. `publish` remains the command that sends one pair
now and verifies it arrived; `configure` decides what the repository's own git does from here on, and pushes
nothing itself. It is the only configuration git-pair writes, it is written once per clone, and no other
command writes it — `--fetch-only` takes the read half and declines the write half.

Nothing configures a clone on its own behalf, and three surfaces name the option instead, each about the
half its own run is doing, and each quiet in a clone that already has the line: `integration record` prints
the line it did not run, `integration publish` names the push key just after the clone sent that pair by
hand, and the note `status` and `queue` print when they cannot compare a record to the remote names the
fetch key beside the `--fetch` that would have asked once.

The order is the contract (§PRD §29). Once the branch is deleted the refs are the only copy of the archive
chain, so a delete that lands before a publish leaves the chain reachable from nothing outside the machine
that recorded it — which is why `status` and `queue` print `RECORDED, NOT PUBLISHED` rather than trusting
anyone to remember.

A parent that landed without being recorded is the same hazard one level up, and it is the one an author hits
first: the child's chain — the commits a reviewer approved, which trunk never held — is the child's history
until the parent is recorded, and the thing holding it is the parent's branch, which `tidy` is built to
remove. A merge landing holds that chain itself, in the side the merge brought in, and a fast-forward holds
it in trunk; a squash or a cherry-pick holds it nowhere else. So the order stays record-then-tidy, and the
answer to an unrecorded parent is its archive ref, never a rebase of the child: rebase the child and the
parent's chain is gone from the repository and from the child's own history at the same moment.

A stack says what is left of it at the same moment, and this is the ordinary next thing an author hits after
landing a parent: `git pair status`, `git pair check` and `git pair queue` name the landing and print the
command that settles the child. While the child's head is not on the landing it is
`git rebase --onto <landing> <parent-branch> <child-branch>`; once it is, the parent's branch is stale and the
command is `git branch -D <parent-branch>` — with the worktree named when another worktree has that branch
checked out, because the delete fails there and removing a worktree is not git-pair's to do. Nothing in
git-pair runs either one: the rebase and the delete are ordinary git, performed by whoever owns the branches
(PRD §26).

Two refs, written once, and never moved: the chain that was reviewed, and the commit it became. The
changeset is discovered from the directories `--source` carries rather than from a ref someone had to
write first, so a pipeline needs only the two SHAs it already holds.

Deleting the branch now loses nothing:

```bash
$ git switch main && git branch -D booking-transaction
Deleted branch booking-transaction (was 0eaad3b).

$ git rev-list --count refs/git-pair/archive/booking-transaction
10
```

## Concepts

**Changeset.** A changeset is a directory, `changesets/<id>/`, and its identity is its **ID** —
the name of that directory. The branch name is only where the default comes from. The default
normalises the branch: `/` and every character outside `[A-Za-z0-9._-]` become `-`, runs
collapse, leading and trailing `-` are trimmed, case is kept, so
`feature/booking-transaction` becomes `changesets/feature-booking-transaction/`. Choose your
own with `git pair init --id booking-transaction-v2`; an ID is never rewritten to fit,
never suffixed to dodge a collision, and never changed once the changeset has refs.

`CHANGESET.yaml` records `id` and where the changeset sits, and nothing else. `base` is what the
diff is measured against. A stacked changeset spells that as `parent:` — the branch it sits on,
which *is* its base — beside `parent-changeset:`, the changeset living there; the two spellings are
never both written. The parent changeset is what still names the relationship after the parent
lands and its branch is deleted, which is when a child needs it most. There is no branch field: the
directory does not belong to a branch, so renaming a branch strands nothing, and two clones of the
same commits cannot disagree about what the directory is.

**What you are working on.** The changesets on a revision are the `changesets/<id>/` directories
it carries that the **integration branch** does not. A directory in the integration branch's tree
is landed work — merge, squash or cherry-pick, it does not matter which, because the directory is
in trunk either way. A directory only here is work in progress, which is what lets a parent
branch and a child branched off it continue one changeset instead of the child inventing a second
answer about the same work. Nothing asks which branch is checked out, so CI, a detached HEAD and
your own branch all get the same answer.

When two directories survive, the changeset whose directory this revision touched most recently
decides, then the `base:` of a stack; when
nothing orders them git-pair refuses, names both, and gives two ways out: `--changeset <id>` answers
for one command, and `git pair change use <id>` records the choice in that changeset's
`CHANGESET.yaml` and settles it for the branch. The integration
branch is what "landed" is measured against: `--default-branch <ref>` states it (this is what CI
passes), otherwise git's own answer — `refs/remotes/origin/HEAD`, then a sole `origin/main` or
`origin/master`, then a local `main` or `master`. If none exists the command refuses rather than
inventing a trunk, and no git config is read.

A landing outside the integration branch is invisible to that rule, and that is what makes
`git pair integration record` load-bearing rather than a report. A change merged into `release/2.x`
leaves its directory absent from trunk, so the branch keeps reading as live work until someone records
where it went: the queue keeps listing it as waiting for a reviewer, and `status` keeps offering a next
action that already happened. A release-line repository that treats recording as optional reporting will
show landed changesets as active forever.

The same rule runs the other way when a repository tidies trunk. Deleting landed `changesets/<id>/`
directories from the integration branch makes those changesets unlanded for every branch that has not
merged the deletion, so they come back with their whole review history. Prune only what carries an
integration ref: that ref is what keeps the history reachable once the branch is gone, and a changeset
retired by trunk's tree alone comes back with the deletion. A terminal record is a marker on a branch, so
pruning trunk does not retire it either.

**ABOUT.md and threads.** `changesets/<changeset>/ABOUT.md` is the canonical description of
the change: the author writes it, the reviewer edits it, and edits committed by a review
submission are high-level feedback. Any other `.md` file in that directory is a thread;
`git pair review thread "concurrency tests"` creates or reopens
`changesets/<changeset>/concurrency-tests.md`. Replies are appended sections and git history
supplies authorship and order. Both are plain Markdown with no schema.

**Markers are commits.** `ready` and a review outcome are commits, not state variables:

| Marker | Subject | Trailers |
| --- | --- | --- |
| ready | `git-pair: ready <slug>` | `Review-State: ready`, `Review-Changeset: <slug>` |
| review | `review: <outcome> <slug>` | `Review-Outcome: <outcome>`, `Review-Changeset: <slug>`, `Review-Head: <sha>` |
| unready | `git-pair: unready <slug>` | `Review-State: working`, `Review-Changeset: <slug>` |
| abandon | `git-pair: abandon <slug>` | `Review-State: abandoned`, `Review-Changeset: <slug>` |

A commit counts as a marker only when its trailer block parses and `Review-Changeset` matches
the changeset being inspected; anything else is an ordinary commit.

`Review-Head` is the commit a review spoke about — `HEAD` when the reviewer submitted, which is the
submission's own parent. It is the only marker field that is about history rather than about a
transition, and it is recorded rather than derived because the value it holds is the exact one a rebase
changes: the rewritten review commit keeps its message, so the marker that survives still names the head
this branch no longer has. Deriving it from the graph instead would read the rewritten parent as the
reviewed one, which is the case the field exists to catch.

**Readiness is withdrawn with a command.** `git pair change unready` commits `Review-State: working`
and takes the changeset out of the queue. Readiness is an offer made with `git pair change ready`, so
withdrawing it is a command too: an author who wants to keep implementing after handing off says so,
instead of leaving a reviewer to guess whether a changeset in the queue is finished work or work in
progress. `working` is not a sixth state — it is `WORKING` chosen on purpose, and a changeset with no
marker derives the same answer. The marker is written only when the changeset is in review (`READY`,
`APPROVED` or `FEEDBACK`); on a `WORKING` or `BLOCKED` changeset there is nothing to withdraw, so the
command succeeds and records nothing. Withdrawing an approval does not delete it: the approval stays
in `git pair review history`, and `git pair check` refuses until a reviewer approves again. The
withdrawal is the marker commit and nothing else — no ref moves, because no ref exists yet.

**Landing writes two refs, once.** `git pair integration record` is the only git-pair command that
writes a ref, and it writes two: `refs/git-pair/archive/<id>` holding the unsquashed chain that was
reviewed, and `refs/git-pair/integrations/<id>` naming the commit the work became. Both are created and
never moved, and neither exists while work is in flight — the branch is the record until it lands.

The merge itself is ordinary git, which git-pair neither runs nor derives: squash, rebase-merge and
cherry-pick each destroy the ancestry that would have answered "did this land", so the link is recorded
rather than inferred. A recorded changeset still reports `APPROVED` (or `FEEDBACK`) — landing is not a
marker, and `state` stays the field markers move — with `integrated`, `integrated_commit` and
`integration_ref` beside it. Ownership follows the same split: the reviewer approves the code, the owner
decides what gets taken forward and where. Because both refs are create-only, nothing can quietly rewrite
the pair that a release note, a bisect, or an agent asking "where did this review go" reads as fact.

**A changeset can end.** `git pair change abandon` commits `Review-State: abandoned` — the marker and
nothing else, since an abandoned changeset never lands and so has no durable record to write. It is
not a sixth state: the changeset reports `WORKING`, because `WORKING` already means "not in review,
nothing owed", and `abandoned_commit` in `status --json` carries the ending beside it. That split is
deliberate — `state` is the field agents branch on, and a changeset that can never move again has to
be recognisable there without teaching every consumer a new state name, so the fact lives in its own
field instead. The ending lives where the marker does: read from the branch, it holds for as long as the
branch stands, and `git branch -D` takes it with the history that explains it.
`change ready`, `change unready` and `review submit` refuse against an abandoned changeset whichever
they meet first. Unlike `change unready`, which withdraws an offer for now, this one closes the
changeset, and re-running it records nothing.

**A review is corrected by submitting again.** There is no `review undo`. The newest
submission decides the state, earlier ones stay in `git pair review history`, and the summary of
a later submission names what it supersedes. Undo by rewriting history is not on the table —
git-pair runs no `reset`, `rebase` or `push`, and a review commit may already be shared; a
withdrawal *marker* would work but needs a trailer older git-pair builds cannot read, which
would leave two versions of the tool disagreeing about one branch. Moving a ref back
is not an undo either, and there is nothing left to move: state comes from commit trailers, so the
submission would still count.

**Derived state.** State comes from walking `base..HEAD` and reading the newest marker, and
nothing else moves it: a commit is not an event in this model. Readiness survives you pushing
more work, and `git pair change unready` is what takes a changeset out of the queue — taking an
offer back is a decision, and decisions are recorded rather than inferred. The drift is still
visible: the reason line counts what arrived since the marker, e.g.
`marked ready by 8065dae (2 commits since)`.

One command asks the harder question, and it is the one whose output gets trusted.
`git pair check` compares the tree between the newest marker and `HEAD` and refuses when anything
outside `changesets/<slug>/` differs, because a merge is about to act on its answer and a gate that passed
that head would certify unreviewed content as reviewed. A commit touching only `ABOUT.md` or a thread is
not that drift, and comparing trees rather than counting commits is what keeps merges and rebases from
reporting a change that never happened.

The tree is half the question. `check` also asks whether the commit the approval named is still in this
history — `Review-Head` an ancestor of `HEAD` — and refuses when it is not. A rebase that resolves no
conflict changes no file, so the tree has nothing to report and only the ancestry test refuses: an
approval is about a commit, and git-pair does not read an approval of one commit as approval of the
rewritten version of it. Merging the base in rewrites nothing and passes. A marker naming no head is
refused too, because there is nothing to compare and no honest way to guess.
States:
`WORKING`, `READY`, `BLOCKED`, `FEEDBACK`, `APPROVED`. `git pair status` prints the
state plus a one-line `Reason`. There is no state file, and no state for a completed
changeset: completion is a pair of refs (below), and the merge that finishes a changeset is not
something state derivation can see. (The TUI caches the reviewer's
per-file marks under the git directory; those are reading progress, not state, and no
command reports them.)

**Review spans.** `git pair diff` resolves a span and hands it to git:

| Option | Range | Question |
| --- | --- | --- |
| (default) | `merge-base(base, HEAD)..HEAD` | what is in this changeset |
| `--unreviewed` | `<latest review>..HEAD` | what happened since I reviewed |
| `--since-review=N` | `<Nth review>..HEAD` | what happened since review N |
| `--base-review=N` | `<Nth review>..<span end>` | start at a review rather than the changeset base |
| `--base-commit=SHA` | `<commit>..<span end>` | "diff from here", with no need to know the merge base |
| `--base-ref=NAME` | `<NAME @ its commit>..<span end>` | start at a branch or tag, pinned to where it pointed when you chose it |

**Span labels.** The table above is git's spelling, because that is the range git is handed. What git-pair prints is a label for the same span — and in a label the working-tree end is `current`, not `HEAD`:
`main...current`, `last review..current`, `review -2..current`, `probe@43915ed..8ab932f`. `current`
is the live end — the newest commit *plus* your uncommitted edits — spelled that way rather than
`HEAD` because the live/historical split of the whole screen turns on this endpoint, and `HEAD` names
only the commit half of it and reads like a point in history. A review end keeps the spelling you
typed: `--since-review=-2` labels itself `review -2` even though it lands on the same submission as
`review 0`, because the alias is what you chose, what the picker listed, and what `v` shows when you
step back onto it — two spellings of one span should not look like two spans. `last review` is the
same idea taken to its useful end: it is the one review worth naming, and `-1` asks you to count.
`status --json`'s `span` field carries this same string; the sha behind a review end is in
`review history` and in `status --json`'s `latest_review`.

`current` only ever appears as the head of a *live* span. A span ending at a commit is labelled with
that commit, and the screen marks itself `HISTORICAL · READ ONLY`.
| `--head-review=N` | `<span start>..<Nth review>` | what things looked like at review N |
| `--head-commit=SHA` | `<span start>..<commit>` | a fixed historical range |
| `--head-ref=NAME` | `<span start>..<NAME @ its commit>` | a range measured to where a branch pointed when you chose it |

`--since-review` with no value means `-1`, and so does `--base-review`; indexes are chronological
(`0` first, `-1` latest) and match `git pair review history`. `--unreviewed`, `--since-review` and the
three `--base-*` flags all name the start of the span, so they are mutually exclusive — two of them
disagreeing is a command to fix, not a precedence to remember.

Those spans answer the reviewer's questions. The author's question — *what did the reviewer
just tell me?* — is `git pair change feedback`, which diffs the submission itself
(`review^..review`) and so shows the threads, the `ABOUT.md` edits and any code the reviewer
edited in one place. Immediately after a submission the two disagree completely:
`--unreviewed` is empty, because the submission *is* the newest commit.

Waiting for that submission is a command rather than a notification. `git pair change wait`
re-derives the state on an interval and exits when it leaves `READY`; `--fetch` runs
`git fetch` before each check, so a review pushed from another clone arrives the way every
other commit does — through the repository, with no forge integration and no daemon.

Coming back to a changeset you already reviewed is common enough to have its own command:
`git pair review reopen` opens the TUI on the `<last review>..current` span, the same span as
`review open --unreviewed`. When nothing has been committed since that submission there is
nothing after it to show, so the session opens instead on what the submission was reviewing:
the changeset as it stood when it was made, `merge-base(base, review)..<review>^`. The end is
the submission's *parent* because a review commit carries what the reviewer wrote — threads,
`ABOUT.md` edits, sometimes a file — and those are their output, not the work under review. It
says so on stderr. With no review
submitted it refuses and points at `review open`. Every command without a span flag stays on
the full changeset.

The same range applies to source, `ABOUT.md` and
threads, and the resolved span is always printed to stderr:
`git pair diff: last review..current`.

**The durable refs.** A changeset owns two refs, both named by the changeset id rather than by any
branch — which is what keeps landed work findable after the branch is deleted:

```text
refs/git-pair/archive/<changeset-id>         the chain: implementation and review, interleaved
refs/git-pair/integrations/<changeset-id>    where it landed: written once, never moved
```

**Neither exists while work is in flight.** No git-pair command writes a ref before landing: `change
ready`, `change unready`, `change feedback`, `change wait`, `review submit` and `change abandon` all write
commits and nothing else. While work is going on the branch is the record — it holds the chain, and a ref
tracking it would be a staler copy of a story the branch tells better. That is why the queue, the gate and
`status` read markers rather than refs, and why a clone that has never fetched `refs/git-pair/*` gets the
same answers about work in flight as one that has.

Both refs are written by one call to `git pair integration record`, archive first, because the integration
ref is the one whose existence means *this changeset is finished*: an interrupted run leaves a changeset
that still reads as not-yet-recorded, and the next call completes the pair. The archive then holds the
whole implementation/review/fix chain through garbage collection, a squash merge and `git branch -D`; the
integration ref names the commit the work became. That second fact is why the record is explicit at all: a
merge preserves ancestry, while a squash, a rebase and a cherry-pick each destroy it, and git-pair is meant
to treat all four alike. Patch IDs and diff-equivalence can help a human recover a lost record; they are not
the protocol, and a tool that inferred integration from them would be confidently wrong about every squash
merge.

Neither ref ever leaves this clone unless it is published, and publishing is a command rather than a side
effect: `git pair integration publish` sends the pair to the remote without a `+`, so the create-only rule
holds at the forge as well as locally, and it reports what the remote actually holds afterwards rather than
trusting a push's exit status. A pair the remote already holds is reported as already published, so a
pipeline can run it on every build.

Neither ref ever moves, and there is no code path that moves one: the only write is an atomic create, and
the hygiene test requires that the single `update-ref` in shipped code passes the zero old-value. Asking for
the commit a ref already names succeeds and changes nothing, so a retry finishes a half-written record;
asking for a different commit is refused, naming both SHAs. And once the record exists the markers stop
too: `change ready`, `change unready`, `change abandon` and `review submit` refuse a changeset with an
integration ref, because `archive A → integration B` is a statement other people read as fact — and the
refusal happens before the commit is written, so a refused command leaves nothing behind.

Both are children of `refs/git-pair` rather than refs in it, because a git ref cannot be both a leaf and a
namespace — git refuses the leaf once a child exists — and a flat namespace would put changeset ids next to
whatever git-pair needs later. Nothing reads the layouts earlier versions used (`refs/reviews/*`, then the
nested `refs/git-pair/changesets/<id>/{archive,integration}`), so a repository upgraded from one reports the
landings it recorded under the old path as unrecorded; those commits stay reachable from their branches, and
`git pair integration record` writes the pair where it is read. What a retired ref still means is that this
clone fetched the namespace — which is the one question the old path continues to answer.

**Surviving review additions.** Review lines left untouched disappear from a `review..HEAD`
diff, so `change ready` re-derives them with
`git show -U0 --no-renames <review>` and test each line for exact membership in the `HEAD` blob,
reporting `path:line` at `HEAD`. Blank additions are ignored, binaries are skipped via
`--numstat`, repeated identical lines collapse to one entry with a count, and only the most
recent review submission is considered. Additions outside `changesets/<changeset>/` block;
additions inside it are reported as "changeset artifacts, not blocking": a thread answered by
appending still contains every line the reviewer wrote, and `ABOUT.md` edits are normally kept,
so blocking on those would fail almost every real changeset and push users toward reflexive use
of the override. See `docs/plans/completed/gitpr-mvp/research/git-plumbing-findings.md`.

## Command reference

Every command accepts the persistent `--json` flag, but only `status`, `change ready`,
`change unready`, `change wait`, `review submit`, `review history`, `queue`, `check`, `skill list`,
`skill install`, `integration record`, `integration publish` and `integration configure` change output for
it; elsewhere it is accepted and ignored.

Every command also accepts `--default-branch <ref>`, which states the integration branch that
"has this landed?" is measured against. Without it git-pair reads git's own answer
(`refs/remotes/origin/HEAD`, then a sole `origin/main` or `origin/master`, then a local `main` or
`master`) and refuses if there is nothing to compare against. It is a flag and not a config key
because the answer belongs to the checkout in front of the command: a CI job that fetched one
branch has no remote HEAD, and two clones of one repository must not disagree about what has
landed.

| Command | Flags | Notes |
| --- | --- | --- |
| `init` | `--id <id>`, `--base <ref>`, `--set-base`, `--parent <branch>`, `--set-parent`, `--about <text>`, `--set-about`, `--no-commit` | creates directory, `CHANGESET.yaml`, `ABOUT.md`, then commits them; never overwrites existing content; `--about` also reads a pipe; default base is the integration branch, recorded as its branch name where that name resolves and as the fetch ref this clone has to reach it through where it does not; refuses on that branch, where a changeset could never contain anything; `--parent` stacks the changeset instead of naming a base, recording the parent's changeset ID beside it, and `--set-parent` restacks it — never done implicitly, because a parent that moved, landed or died is the author's decision; `--id` names the changeset instead of the branch-derived default, and a collision with a committed directory or ref refuses rather than suffixing |
| `change ready` | `--allow-surviving-review-additions` | fully non-interactive; checks below |
| `change unready` | none | withdraws the changeset from the review queue; records `Review-State: working` when the changeset is in review, otherwise succeeds and records nothing; refuses a changeset whose work is recorded as integrated |
| `change use <id>` | none | records which changeset a branch carrying more than one is working on: writes `ignores: <other ids>` into the chosen changeset's `CHANGESET.yaml` and commits that file; refuses an id the branch does not offer and a record that would leave the branch still undecided; idempotent |
| `change abandon` | none | records the terminal `Review-State: abandoned` and nothing else — no ref, since an abandoned changeset has no landing to record; `change ready`, `change unready` and `review submit` refuse against it afterwards; refuses a changeset whose work is recorded as integrated; idempotent |
| `change feedback` | `--stat`, `--name-only`, `--changeset <slug>` | the diff of the most recent review submission (`review^..review`): threads, `ABOUT.md` edits and reviewer code edits together; exits 2 if there is no submission |
| `change wait` | `--fetch`, `--interval <dur>` (default `10s`), `--timeout <dur>` | blocks until the state leaves `READY` for `BLOCKED`/`FEEDBACK`/`APPROVED`; read-only; `--fetch` runs `git fetch` before each check so a review pushed from another clone is noticed |
| `review`, `review open` | `--unreviewed`, `--since-review[=N]`, `--base-review[=N]`, `--base-commit`, `--base-ref`, `--head-review[=N]`, `--head-commit`, `--head-ref` | TUI; needs a terminal; full changeset unless a span flag says otherwise; a `--head-*` flag opens a historical span, which is read-only; with no subcommand `review` is `review open` and takes the same flags |
| `review reopen` | none | TUI on `<last review>..current`, the work that has landed since you reviewed; needs a terminal; refuses if no review exists |
| `review about` | — | opens `ABOUT.md` in the editor, creating it if missing |
| `review thread [title...]` | — | slugifies the title, reopens an existing match, prompts for a title only with a terminal |
| `review submit` | one of `--block`/`--feedback`/`--approve`, `-m/--message <text>`, `--no-stage` | stages the whole tree by default, commits (empty commits allowed), and writes nothing else: a submission is a marker commit, not a ref move. The commit names what it reviewed with `Review-Head`, which is what lets `check` refuse a rewritten history |
| `review history` | `--changeset <slug>` | only review marker commits, indexed from `0`, each naming the commit it reviewed under `REVIEWED` and the reviewer who submitted it under `REVIEWER` |
| `queue` | — | one row per branch whose changeset is `READY`, longest wait first, plus any landing in the integration branch that no integration record accounts for; read from the repository, not the checkout |
| `status` | `--changeset <slug>` | derived state, for this branch's changeset or one named by slug |
| `check` | `--allow-feedback` | asserts integration-readiness and exits 1 when it is not; lists every failed condition — the review's outcome, whether the commit it approved is still in this history, and whether the content still matches; no `--changeset`, because it is the gate a forge runs *on* a revision |
| `integration publish` | `[<changeset>…]`, `--remote <name>` | sends a changeset's two durable refs to the shared remote, unforced, in one push — the only git-pair command that pushes, and the only thing git-pair may push is `refs/git-pair/*` (§26). No arguments publishes every pair this clone holds; named ids publish just those, and a name with no record here is a refusal rather than a silent no-op. No `+` and no options: a remote that holds a different value rejects the push, and the refusal names both values, because two people recording one landing is a decision rather than a race to win. It then re-reads the remote's copies and reports what is actually there, so one ref arriving while the other is refused is reported as the half-state it is rather than as a single failure. Idempotent — a pair the remote already holds is "already published" and nothing is written, which is what lets CI run it every build |
| `integration record` | `--source <sha>`, `--commit <sha>`, `--target <ref>`, `--changeset <id>`, `--allow-feedback` (all optional) | writes both durable refs for one changeset, create-only: the archive at `--source` and the integration at `--commit`. The changeset is discovered from the `changesets/<id>/` directories `--source` carries and the integration branch does not, so a pipeline needs the two SHAs it already holds and not the changeset name; `--changeset` disambiguates a stacked child. Before it writes: the source's history must name this changeset and its newest verdict must permit integration (`approve`, or `feedback` with `--allow-feedback`); `--commit` must be in the destination branch's history (the `--target` you name, else the changeset's `base:`, else the default branch) and must be the commit that added `changesets/<id>/` there. Name neither SHA and the repository is asked — the landing is the first-parent commit on the destination that added the directory, the reviewed head is the branch still carrying it, wherever this clone holds that branch (a fetched `refs/remotes/origin/<branch>` counts, since that is where git puts a branch a pipeline was handed), and one branch is one candidate however many paths spell it — and anything ambiguous is a usage error naming the candidates. Needs no checkout and writes no commit; re-running it with the same pair succeeds and changes nothing. It writes refs and nothing else: the clone's configuration is `integration configure`'s to write, and `record` prints the one line naming that command when the clone has none. The records themselves stay behind `--fetch`: a record is a claim, and a clone should acquire claims by asking |
| `integration configure` | `--remote <name>`, `--fetch-only` | the only configuration git-pair ever writes, and the command *is* the consent for it: appends the mirror refspec `+refs/git-pair/*:refs/remotes/<name>/refs/git-pair/*` to `remote.<name>.fetch`, so an ordinary `git fetch` keeps this clone able to tell published from unpublished, and appends `refs/git-pair/*:refs/git-pair/*` to `remote.<name>.push`, so an ordinary `git push` publishes what this clone records. Both are `--add` writes, idempotent, each reported with its key, its value and whether it was already there — so it is safe in a pipeline and safe in a repository with its own refspecs. `--fetch-only` writes the read half alone, for a clone that should compare a record against the remote without being the thing that makes it public. The push refspec carries no `+`, so a remote holding a different value rejects the push instead of being overwritten: publishing automatically is a repository's decision, and moving somebody else's record is nobody's. It is a command rather than a flag on `record` because configuration is a property of the clone, and a clone that arrived after a landing has no SHAs to record and had nothing to run. A prompt would make one command line mean two things, and an unanswered prompt in CI reads exactly like a declined one (§PRD §22) |
| `diff [path...]` | `--unreviewed`, `--since-review[=N]`, `--base-review[=N]`, `--base-commit`, `--base-ref`, `--head-review[=N]`, `--head-commit`, `--head-ref`, `--stat`, `--tool` | paths are checked against the span first, so a typo is an error, not an empty diff |
| `skill list` | — | the agent skill this binary carries, and every directory a harness would read it from, each marked `current`, `stale`, `absent` or `unavailable`. `current` means the installed bytes equal this binary's, which is the check that keeps an installed skill from describing an older tool. Read-only |
| `skill show` | `[path]` | prints one compiled-in file of the skill, `SKILL.md` by default, so it can be read or copied without a checkout. No JSON output |
| `skill install` | `--harness agents\|pi\|claude`, `--scope repo\|user`, `--dest <dir>`, `--dry-run`, `--force` | writes the compiled-in skill into a skills directory as `git-pair/`. Matching files are left alone, differing files are refused without `--force`, and files git-pair did not write are reported and kept. `--dest` cannot be combined with `--harness` or `--scope` |
| `skill agents-md` | — | prints the pointer stanza for `AGENTS.md` or `CLAUDE.md`, for a harness with no skill discovery. No JSON output |

`change ready` checks, in order: clean working tree, `ABOUT.md` exists, the repository has
commits, no blocking surviving additions — the last acknowledged with
`--allow-surviving-review-additions`. `check` asks what the merge would act on: the newest marker is a
review whose outcome permits integration (`approve`, or `feedback` under `--allow-feedback`), nothing
unreadable came after it, the changeset has not ended, it has not already been recorded as integrated,
and the tree still matches what the review looked at (ignoring `changesets/<cs>/`). It reads no other
ref, and every failed condition is reported in one run. `change unready` checks
only for a clean tree, since the marker it writes is empty. `init` warns
without failing
if the base does not resolve, and commits only the changeset directory (`git commit --only`),
so work you had already staged for another commit stays on your index.

**Reading any changeset, writing one.** `status`, `review history` and `change feedback` accept
`--changeset <slug>` and resolve it from whichever branch carries that changeset, so you can ask
about work you do not have checked out. Nothing that records a marker takes the flag: a marker is a
commit, and a commit lands wherever `HEAD` is, so `change ready --changeset other` would write onto
the branch you are standing on while claiming to describe a different one. When the slug names
nothing, those reads exit 2. For a span of another branch, name its ends: `git pair diff
--base-ref=main --head-ref=booking`.

| Exit code | Meaning | Seen as |
| --- | --- | --- |
| 0 | success | — |
| 1 | a git-pair rule or the repository state refused the operation | surviving additions; `working tree must be clean`; `ABOUT.md is missing`; `cannot resolve changeset base "vanished"`; `change wait` timing out, or refusing a changeset that is `WORKING`; `git pair check` printing `NOT READY:`; `integration record` finding no changeset directory at `--source`, a head that was never reviewed, a landing that is not in the destination branch or did not add the changeset directory, or a record naming a different commit; `integration configure` in a repository with no remote to write |
| 2 | usage error | unknown flag, unknown command, or unknown subcommand of `change`/`review`/`integration`/`skill`; `no changeset for this branch`; `no branch carries changeset "<slug>"`; detached HEAD; `cannot tell which branch is the integration branch`; `--block, --feedback and --approve are mutually exclusive`; `changeset has no review submissions yet`; `changeset <cs> has no review submission yet` (`change feedback`); `--interval expects a duration` (`change wait`); `--fetch` with no remote configured; `"<path>" does not appear in <span>`; editor/TUI commands without a terminal; more than one changeset directory in `--source` and none named with `--changeset`; `skill install` with an unknown `--harness` or `--scope`, or with `--dest` alongside either |
| 3 | the repository or git itself failed | `not a git repository`; a git subprocess exiting non-zero for a reason other than an unresolvable revision |

The split between 1 and 2 is deliberate and load-bearing for agents: exit 1 means the
invocation was correct and the repository said no, so fix the state and retry; exit 2 means
the invocation itself was wrong, so retrying unchanged will fail again.

## JSON contracts

Output is indented two spaces. An array is never null. It says `[]` for "asked, and none". A missing key
then means the build did not ask.

Two fields answer null on purpose, and neither is a list. `status`'s `latest_review` is an object that
does not exist before the first review. `uncommitted` is a bool that reports the question belongs to a
checkout this command does not stand in.

`git pair integration configure --json` reports the `remote` it wrote, then `fetch` and `push`, each
`{"key", "refspec", "already_configured"}`. A half is absent when it was not offered — `--fetch-only` leaves
`push` out — so a pipeline can tell "not asked" from "asked, and the line was already there". Every other
command's answer about configuration is silence: `git pair integration record --json` has no configuration
key at all, because writing configuration is not that command's to do.

`git pair change feedback`, `git pair diff`, `git pair skill show` and `git pair skill agents-md` have no
JSON output. `--json` is a global flag, so all four accept it. Each now says on stderr that the flag changed
nothing, because an empty stdout reads to a machine as an empty answer.

`git pair status --json`, waiting for the first review. Once a review exists `latest_review`
becomes `{"index": 0, "outcome": "block", "commit": "332887c", "reviewed_head": "1a2b3c4"}` —
`reviewed_head` is the commit that submission spoke about, from its `Review-Head` trailer, and it is
omitted when the marker names none; `unrecognised_markers` (a
list of `<sha> <subject>`) appears only when non-empty. Read another changeset with `--changeset`
and two fields report that they cannot answer — `uncommitted` is `null` and `span` is `""` — because
both describe the checkout rather than the commit, and `next_action` names the branch to switch to.
`abandoned` is true once `change abandon` has ended the changeset, with `abandoned_commit` naming the
terminal marker; `state` stays `WORKING`, because the ending is a fact beside the state rather than a
sixth state value. `archive_ref` and `archive_commit` describe the
durable pair and stay `""` while work is in flight, because nothing writes a ref before landing —
their name is derivable from the changeset, so their existence is the only fact worth reporting.
`integrated` is true once `git pair integration record` has recorded where
the work landed, with `integrated_commit` naming that commit and `integration_ref` the ref that holds
it, and `state` is untouched by it: landing
is a fact beside the state, not a sixth state value. The human surface prints no "run
`git pair integration record`" beside a ref that already exists — the finding for a landing with no record
is `LANDED, UNRECORDED`, which names the command with the changeset in it. Reading a landed changeset by id
(`status --changeset <id>`, no branch carrying it) keeps that split: `state` stays what the span says while
the reviews come from the archived chain, because after a merge landing the archived head sits below the
base and `base..head` is empty for exactly the changeset whose verdicts matter most. `integrated_in_default_branch` says whether that
commit is in the history of the branch git-pair calls the integration branch, and
`integrated_default_branch` names that branch — work that retired into `release/2.x` and never reached
the default branch must not read like a default-branch landing, and what git-pair reports is the
containment it can derive rather than a branch name no ref stores. A recorded changeset's own two refs
say what it became and not where that reached, so `stack` walks the chain the child's `parent-changeset:`
starts: one entry per ancestor, nearest first, with the ancestor's id, the branch it was stacked on and
whether this clone still has that branch, its recorded commit and ref, and whether that commit is in the
integration branch's history. It is `[]` for a changeset that sat on the integration branch, and
`stack_note` is the one sentence for where the walk stopped — a hand-edited cycle, the depth cap, or a
read that failed — so a short list is never mistaken for the whole chain. `default_branch`,
`default_branch_commit` and `default_branch_source` name the branch "landed" was measured against,
the commit it pointed at, and how the run learned it (`flag`, `origin-head` or `sole-candidate`):
a CI log that says nothing has landed has two causes, a stale fetch and a wrong trunk, and neither
one is visible in a sentence about the changeset.
`next_action` is the landing contract (§PRD §29) in one line: `check`, then the merge with ordinary git,
then `integration record`, then `integration publish`. Once the record exists it says there is nothing
further to record instead.

```json
{
  "changeset": "booking-transaction",
  "branch": "booking-transaction",
  "base": "main",
  "default_branch": "main",
  "default_branch_commit": "9c41f0b",
  "default_branch_source": "sole-candidate",
  "state": "READY",
  "head": "8065dae",
  "head_full": "8065dae53c0475596bfc174927075895d9fb8b76",
  "latest_review": null,
  "archive_ref": "",
  "archive_commit": "",
  "uncommitted": false,
  "abandoned": false,
  "integrated": false,
  "reviews": 0,
  "reason": "marked ready by 8065dae",
  "span": "main...current",
  "next_action": "waiting for a reviewer: `git pair review open` (author: `git pair change wait` to block on it)"
}
```

`git pair queue --json` — `head` and `ready_commit` are full SHAs. `skipped` names changesets
the queue cannot explain (`[]` when empty): a branch whose metadata cannot be read, a branch the
resolution rule cannot settle between two changesets, a recorded changeset whose archive is in no base
and on no branch, or a changeset whose integration ref says it landed — that one names the commit,
because the branch is usually still here and its disappearance from the queue would otherwise be a
mystery. A changeset that has landed in its base, and a directory with no branch and no record anywhere, are both
silent — neither is work a reviewer can act on, and a clone that never fetched `refs/git-pair/*` is
silent here rather than wrong (PRD §13.4). What is *not* silent is a directory the integration branch
carries with no integration ref: that is a merge whose record never ran, and it is reported by name.

```json
{
  "ready_for_review": [
    {
      "changeset": "booking-transaction",
      "branch": "booking-transaction",
      "base": "main",
      "state": "READY",
      "head": "af740a30d9cee930b85324aedfbc0aa4b33c1408",
      "ready_commit": "af740a30d9cee930b85324aedfbc0aa4b33c1408",
      "ready_age": "0s"
    }
  ],
  "skipped": ["untracked-work (cannot resolve changeset base \"other\": unknown revision: other)"],
  "parent_notes": ["waitlist-rebooking: parent booking-transaction landed as 4f2b8c1 — the branch booking-transaction is stale — it holds nothing the record does not"],
  "landed_unrecorded": [
    {
      "changeset": "waitlist-rebooking",
      "command": "git pair integration record --changeset waitlist-rebooking"
    }
  ]
}
```

`parent_notes` is what the queue says about a row it is not refusing: the branch underneath a READY child
has an integration record, so the base a reviewer is about to read against is finished work. It is a note
and not a row, and never a reason — a READY changeset has no approval for a parent to invalidate.

`landed_unrecorded` is the queue's second job: work that reached the integration branch while nobody
wrote its record. Each entry carries the invocation that closes the gap, and the human form prints the
same thing under its own heading:

```text
LANDED, UNRECORDED

  waitlist-rebooking
    in main with no integration record. Record it with:
      git pair integration record --changeset waitlist-rebooking
```

The state is the one the merge leaves behind when the step after it is skipped: the directory is in
trunk, so the rules that find work in progress stop seeing it, and the branch may already be deleted.
`git pair status` prints the same finding on a branch that carries no changeset of its own — attached to
its exit-2 "no changeset for this branch" answer, which stays exit 2 because the branch really does hold
no work in progress. On that branch `status` also runs the published-or-not comparison, so the destination
branch — the branch every changeset eventually lands on, and the one where a record that never left the
clone was invisible — reports both halves. Its `--json` there is a document rather than only an error:
`reason`, `landed_unrecorded`, `unpublished` and `unpublished_note`, with the two lists present and empty
when there is nothing to report.

When the destination already holds the directory, the answer says the changeset landed. It then names
`git pair status --changeset <id>` rather than telling you to run `git pair init` over work that has a
record.

Both reports say "no integration record *in this clone*", and mean it: a record written where the merge
ran arrives with `git fetch origin 'refs/git-pair/*:refs/git-pair/*'` — or with `--fetch`, which
`status`, `queue` and `check` all accept and which also brings the mirrors under
`refs/remotes/<remote>/refs/git-pair/*` that "has this been published yet?" is measured against. A
fetched record is a record; a mirror is only a comparison, and nothing answers "is this recorded" from
one — and they are two fetches, because the mirror side is pruned and the record side must never be.
What the mirrors are for is the finding `queue` prints as `RECORDED, NOT PUBLISHED`, beside
`LANDED, UNRECORDED`: changesets whose record this clone holds and the remote, as last fetched, does not.
`status` prints it on a changeset branch after its report, and on the destination branch beside the failure
that says there is nothing there to report.
Half a pair on the remote is its own, louder case — the remote has a hint and no way to reconstruct the
record from it. `--json` reports it as `unpublished`, and `unpublished_note` when there was nothing to
compare against. `check` does not refuse on it: a record that has not travelled is a durability risk, not
a bad verdict. Publishing is ordinary git — `git push origin 'refs/git-pair/*:refs/git-pair/*'`. And an
empty namespace is stated
once as one condition rather than once per changeset: empty means nothing at all lives under
`refs/git-pair/`, which is a fact about the fetch rather than about the two families, so a clone holding
only a ref from the retired layout is not told to go and fetch. A landing recorded only under that retired
`refs/git-pair/changesets/<id>/integration` path is reported as unrecorded — nothing reads that path as a
record any more, and the finding names the command that writes the pair where something does. The
printed list caps at ten and counts the rest; `--json` carries all of them.

`git pair review history --json`

```json
{
  "changeset": "booking-transaction",
  "reviews": [
    {
      "age": "0s",
      "index": 0,
      "outcome": "block",
      "reviewed_head": "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
      "reviewer": "Rae",
      "sha": "332887cf6413e66d45d0ad5d59d93f7d30484ffd",
      "short": "332887c",
      "subject": "review: block booking-transaction",
      "when": "2026-09-16T00:43:44Z"
    }
  ]
}
```

`git pair review submit --approve --json`, on a clean tree and the first review
(`previous_review` is the full SHA of the submission this one supersedes, empty when there
is none)

```json
{
  "changeset": "feat",
  "commit": "941266b18686624cb624722b4e8c348bf03451a7",
  "empty": true,
  "files": [],
  "next_action": "author: `git pair check`, then merge into main with ordinary git, then `git pair integration record`, then `git pair integration publish`",
  "outcome": "approve",
  "previous_review": "",
  "short": "941266b"
}
```

`git pair check --json` — the verdict an automation consumes. `ready` is the answer, `reasons` names
every failed condition (an empty array when it passed, so a consumer branches on `ready` rather than
handling two shapes), and `policy` records which rule produced the verdict — `approve-only`, or
`approve-or-feedback` with `--allow-feedback` — so a gate's decision in a log can be re-derived.
`head` and `reviewed_head` are full SHAs, not the short forms the human output prints, because the job
comparing the verdict built one of them and the other is the end of the lineage comparison.
`reviewed_head` is the commit the newest permitting review named; it is reported whichever way the
verdict went and omitted only when that marker names no head. Nothing else in the verdict reads a
durable ref except `integrated`, which reports a changeset already recorded as landed (PRD §11.3).

This form carries the verdict in `ready` rather than in the exit code: a not-ready run prints its
JSON and exits 0, so a job piping it into `jq` keeps git-pair's answer separate from the pipeline's.
Usage errors and git failures still exit 2 and 3 here.

A passing verdict also carries `next_action` — the same sentence `status` prints in its `next_action`,
naming the merge and then `git pair integration record` — so an agent that gates on `ready` learns what
comes next without parsing the human output. It is present only when `ready` is true, because on a
failing gate the next step is `reasons`.

```json
{
  "changeset": "feat",
  "ready": false,
  "state": "APPROVED",
  "head": "941266b18686624cb624722b4e8c348bf03451a7",
  "reviewed_head": "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
  "reasons": [
    "content outside changesets/feat/ changed since 1a2b3c4: src/service.ts"
  ],
  "policy": "approve-only",
  "integrated": false
}
```

`git pair integration record --json` — what the recorder wrote, with full SHAs so a pipeline can
compare them against the revisions it built. `target` is the branch the landing was verified against, in
branch form, and is empty when nothing identified one and so no containment check was made;
`target_derived` appears only when git-pair chose that branch itself rather than the caller naming it.
`recorded` means this call wrote at least one ref and `already_recorded` means both refs already named this
exact pair: a retry is a success that changed nothing, and the two are worth telling apart in a log. A
retry answers from the record before it runs any checks, so it prints no verification and refuses on no
check — including when it arrives with fewer flags than the run that wrote the pair. `derived` lists the
flags git-pair filled in from the repository (`"commit"`, `"source"`) and is absent when you named both.
`fast_forward` says the two refs name one commit because the destination's own line carries the reviewed
chain — the fast-forward shape, where there is no side branch to name and the commit the work became is the
commit that carries the approval.

CI passes both SHAs — a shallow clone may hold neither branch — while the person who merged can name
neither:

```json
{
  "changeset": "booking-transaction",
  "source": "a7f3c98d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
  "commit": "d91c21ed5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0c",
  "target": "origin/main",
  "target_derived": false,
  "derived": ["commit", "source"],
  "archive_ref": "refs/git-pair/archive/booking-transaction",
  "integration_ref": "refs/git-pair/integrations/booking-transaction",
  "recorded": true,
  "already_recorded": false
}
```

`git pair change ready --json` prints `changeset`, `branch`, `base`, `state`, `head`, `ready_commit` (full
SHA), `review_queue_visible` and `acknowledged_survivors`. `surviving_review_artifacts` appears only when a
surviving-additions report existed — which is when `--allow-surviving-review-additions` acknowledged lines.

`git pair change unready --json` — `was` is the state the command found and `state` the state after
it, which differ only when a marker was written. `recorded` is false when there was nothing to
withdraw, which is a success rather than a refusal, and `unready_commit` is empty then.

```json
{
  "changeset": "booking-transaction",
  "branch": "booking-transaction",
  "base": "main",
  "state": "WORKING",
  "was": "READY",
  "recorded": true,
  "unready_commit": "4f0c1a253c0475596bfc174927075895d9fb8b76",
  "review_queue_visible": false
}
```

`git pair change wait --json` — states are spelled as `status` spells them. `ref` is where the
activity appeared: `HEAD`, or a remote-tracking branch when it was found with `--fetch` and
has not been brought into this branch yet. `review_commit` and `review_commit_full` appear
only when a review submission is what ended the wait. `timed_out` is the difference between
"a reviewer acted" and "gave up", and both exit 0 and 1 respectively:

```json
{
  "changeset": "booking-transaction",
  "previous_state": "READY",
  "state": "FEEDBACK",
  "review_commit": "332887c",
  "review_commit_full": "332887cf6413e66d45d0ad5d59d93f7d30484ffd",
  "ref": "HEAD",
  "fetches": 3,
  "waited_seconds": 91,
  "timed_out": false,
  "next_action": "`git pair change feedback`; feedback is non-blocking, `git pair check`, then merge into main with ordinary git, then `git pair integration record`"
}
```

## Agent contract

The non-interactive loop from PRD §22:

```bash
git pair init --base main --about "$ABOUT"   # once, on a named branch
git pair status --json              # read state and next_action without blocking
git pair diff                       # see the whole changeset
# implement and commit with ordinary git
git pair change ready               # hand off to the reviewer
# changed your mind about being done? git pair change unready
# (do it before continuing, rather than leaving the offer standing)
git pair change wait --fetch --json # block until a reviewer acts; exits 1 on --timeout
git pair change feedback            # read that submission: threads, ABOUT.md, code edits
# address feedback in code, ABOUT.md and threads; commit normally
git pair review history --json      # enumerate review commits
git pair change ready               # again
git pair check                      # assert integration-readiness; $? is the answer
```

Rebasing after an approval invalidates it, and `check` says so in as many words. Re-offer the work with
`change ready` and wait for a reviewer; do not edit the marker's `Review-Head` to make the comparison
line up — that trailer is the reviewer's statement about what they looked at, not the author's to amend.

`check` is the last step an agent runs. Landing is not the agent's: the merge is ordinary git run by
whoever owns the destination branch, and `git pair integration record` follows it (§13).

Never prompt: `init`, `change ready`, `change unready`, `change abandon`, `change feedback`,
`change wait`, `status`, `check`, `diff`, `review submit`, `review history`,
`queue`. They report
and exit instead of asking, even with a terminal attached.

Refuse with exit 2 when stdin or stdout is a pipe or a regular file, because launching an
editor or the TUI against one would hang: `review` with no subcommand, `review open`, `review reopen`,
`review about`, and `review thread`
(which creates the thread file and then refuses to open it). Read and write those files
directly instead; they are ordinary files in the working tree. `/dev/null` counts as a
character device, so redirecting to `/dev/null` does not produce this refusal — it makes
`review open` and `review reopen` fail with `could not open a new TTY` (exit 1) and
`review about` launch the
real editor.

An agent must not approve its own work. `git pair review submit --approve` is a reviewer action
and nothing in git-pair checks who ran it — identity and permissions are out of scope, so keeping
approval on the human side of the pair is a convention you enforce. When an agent records
progress it uses `git pair change ready`, and when the work is not finished after all it withdraws the
offer with `git pair change unready` rather than leaving a reviewer looking at a stale offer.

Recording the landing is not an agent's action either: `git pair integration record` states where the
work landed, which is a fact about the merge someone else performed. In practice the pipeline that
merged runs it. An agent asked whether the work may land reads `git pair check` and stops there.

**As a CI gate.** `check` reads the repository — the branch's commits and their trees — so a plain
checkout of the branch is enough to run it, and nothing about the durable refs changes its answer:

```bash
git pair check || exit 1
```

In the human form the exit code is the verdict: 0 ready, 1 not ready, 2 usage, 3 git failed. A
not-ready run writes one bullet per failed condition to stdout, and the passing form names the commit it
cleared, so the log explains the gate without a second run. `--json` carries the same facts — `ready`,
`reasons`, `policy` and a full SHA in `head` — but moves the verdict into `ready` and exits 0 either way,
which is why a JSON gate asks `jq` rather than `$?`:

```bash
git pair check --json | jq -e '.ready'
```

One thing a gate needs to know: `check` accepts `feedback` only with `--allow-feedback`. Whether a
non-blocking review is enough to land is a policy of the repository, and the flag is the place it is
stated — there is no config key for it, because the command that runs the gate is the only place the
policy is known.

**Recording the landing.** After the merge, whoever did it tells git-pair where the work ended up. A
pipeline names both SHAs, because a shallow checkout holds neither branch:

```bash
git pair integration record --source "$SOURCE_SHA" --commit "$TARGET_SHA" --target origin/main
```

A person standing on the branch they merged into names neither, and the repository answers:

```bash
git merge --no-ff booking-transaction
git pair integration record
```

`--source` is the head that was reviewed — the branch tip, not the merge commit — and `--commit` is the
commit the work became. The changeset is discovered from the `changesets/<id>/` directories `--source`
carries and the integration branch does not, so the pipeline needs no name a human chose; `--changeset`
settles it when the source carries two, which is what a stacked child does. A squash, a rebase and a
cherry-pick are all recordable, and none of them leaves ancestry between the two SHAs: the record is what
connects them. Both refs are create-only — a re-run with the same pair is a success that changed nothing,
and asking for a different commit is refused, naming both. The one exception is a commit that *carries*
what the record already names, and only when that commit is what brought the record in: a stacked child
lands on its base branch, that branch later lands on trunk, and the re-run against trunk names a descendant
whose own first parent did not yet hold the record. That answers "already recorded at <the recorded commit>,
and <this commit> carries it into <destination>", writes nothing, and reports `carried_by` in `--json`. A
commit whose first parent already held the record is a second landing on the same branch — a backport — and
a different reviewed head is a different claim, so both stay conflicts. Once the record
exists `check` refuses the changeset as already integrated and the commands that write markers refuse it
too.

What the recorder refuses is what makes the pair worth reading later, and all four checks are refusals
rather than warnings:

- **the source was not reviewed.** The newest marker naming the changeset in `--source`'s history must
  permit integration — `approve`, or `feedback` with `--allow-feedback`, the same switch `check` offers.
  An unreviewed head cannot be recorded as integrated.
- **the changeset's history names a different changeset.** Tree discovery says which directories a commit
  carries; the markers say which changeset was reviewed. When they disagree the refusal names both, which
  is the difference between "nobody reviewed this" and "this is someone else's reviewed work".
- **the landing is not in the destination branch.** `--target` names the destination; without it git-pair
  tries the changeset's own `base:` and then the default branch, and takes the first that holds the commit.
  A commit in neither is refused naming what it tried — landing on a release branch is allowed, only not
  silent, so pass `--target release/2.x` when that is where it went.
- **the landing commit did not add the changeset directory.** A commit whose first parent already carries
  `changesets/<id>/` did not bring the changeset in, so it is a follow-up on the destination branch rather
  than the landing. This is the check that survives squash, rebase and cherry-pick, because the directory's
  arrival is a fact about trees.

**Fetching the durable refs.** `git pair integration record` reads and writes
`refs/git-pair/archive/<id>` and `refs/git-pair/integrations/<id>`, and neither is fetched by default: a
clone maps `refs/heads/*` into `refs/remotes/*` and nothing else. A job handed the branch and nothing more
gets a true statement about its own checkout — `this clone holds no refs/git-pair/* refs at all`, with the
fetch printed — on the one command whose answer a reader elsewhere will trust. `check` needs no refs at all
and says nothing about them. Two fetches make a checkout complete:

```bash
git clone --branch "$BRANCH" --single-branch "$REPO" work && cd "$work"
git fetch origin --no-tags main:refs/remotes/origin/main          # the integration branch
git fetch origin 'refs/git-pair/*:refs/git-pair/*'                # the durable refs
git pair check
```

The fetch carries no `+`: those refs are create-only, so a fetch that could clobber them would be a way
for a clone to move a ref that git-pair's own code cannot.

The default branch is not the optional one. The rule that decides which changeset a revision is working
on compares its tree against trunk's, so a checkout holding one branch refuses to invent a trunk
(exit 2) rather than read every directory as work in progress — and `--default-branch origin/main`
states it directly instead of fetching. A `--single-branch` clone records no `origin/HEAD`, which is
why the second line above names the branch.

Publishing is the other half. `git pair integration publish` sends one pair when somebody runs it, and a
repository can make publishing automatic with `git pair integration configure`, which appends
`refs/git-pair/*:refs/git-pair/*` to `remote.origin.push` so an ordinary `git push` carries the namespace
with the branches. That line is written on request and never as a side effect of another command, because
publishing review history is a decision about who gets to read it — the archive holds every commit of the
review, including ones the author later dropped. Until it is run, the author's clone holds the only copy,
and a CI job reporting missing refs is describing exactly that.

The configured push refspec carries no `+`, for the same reason the fetch one does: a remote holding a
different value rejects the push instead of being overwritten by it. A repository can publish on every push
and still have no push that moves somebody else's record.

## The agent skill

The contract above is also a file an agent reads. `skills/git-pair/` is the skill — the author-side loop,
the roles, the exit codes and the prohibitions in one page, with the flags, the JSON shapes and the
landing contract in references beside it — and the same bytes are compiled into the binary.

Every page under `skills/` is held to the command tree by `docs_contract_test.go`, the way the spec and
this file are: a command name or a ref path the prose uses must be one the code answers to, and the
reference page must name every command. That check exists because the first version of this skill lived
beside a different repository and documented a `review close` that had been removed and a `refs/reviews/*`
namespace that never existed. Prose that drifts from the code is read as a promise. What the check does not
cover is the flags, the JSON field names, and the harness directories below: those are prose, and a reviewer
reads them.

Put it where the agents working on a repository read skills:

```bash
$ git pair skill list
git-pair skill "git-pair" — 4 files, from git-pair 0.1.0

  agents  repo   .agents/skills/git-pair              absent
  agents  user   $HOME/.agents/skills/git-pair        absent
  pi      repo   .pi/skills/git-pair                  absent
  pi      user   $HOME/.pi/agent/skills/git-pair      absent
  claude  repo   .claude/skills/git-pair              absent
  claude  user   $HOME/.claude/skills/git-pair        absent

current is the same bytes this git-pair ships; stale is an older copy, read as fact.

$ git pair skill install
installed the git-pair skill into .agents/skills/git-pair
  wrote      SKILL.md
  wrote      references/cli.md
  wrote      references/installing-the-skill.md
  wrote      references/integration.md

Commit .agents/skills/git-pair so every clone, worktree and CI job gets the same copy.
A harness that was already running has read its skill directories: restart it if the skill does not appear.
```

`--harness` chooses whose directories to write, `--scope` chooses the repository or the home directory,
and `--dest` names a directory outright for a harness git-pair has no row for. The paths are each harness's
documented discovery rules, cited with the date they were read in
[`skills/git-pair/references/installing-the-skill.md`](skills/git-pair/references/installing-the-skill.md)
and in the `skillHarness` comment — not a probe of what some version of a harness happens to scan:

| Harness | Per repository | Per machine |
| --- | --- | --- |
| `agents` (default; Codex CLI and pi both read it) | `.agents/skills/` | `~/.agents/skills/` |
| `pi` | `.pi/skills/` | `~/.pi/agent/skills/` |
| `claude` | `.claude/skills/` | `~/.claude/skills/` |

Copying is equally supported: copy `skills/git-pair/` — the directory, with its `references/` — into any
of those. `git pair skill list` still reports `current` or `stale` against it, which is the point of
compiling the skill in: the check does not depend on how the files got there.

For a harness with no skill discovery, print the pointer stanza instead:

```bash
git pair skill agents-md >> AGENTS.md
```

It names where the skill is and the three rules agents most often get wrong. A repository can carry both
halves — the stanza for every harness, and the installed skill for the ones that read it.

## Configuration

The editor is whatever git would use: git-pair asks git with `git var GIT_EDITOR`, so the
precedence is git's — `GIT_EDITOR`, then `core.editor`, then `VISUAL`, then `EDITOR`, then
whatever fallback the git build was configured with. `git config core.editor vim` is enough, including when it is set in the repository rather than globally, and a caller that
injects `GIT_EDITOR` (a hook, another tool) is honoured the way every other git consumer
honours it. Only when git cannot answer does git-pair fall back to `$VISUAL`, then `$EDITOR`, then
`vi`. Automated harnesses must clear `GIT_EDITOR` along with `VISUAL`/`EDITOR`, and every editor
probe should be wrapped in `timeout`, because an editor that takes the terminal and is never
driven will block forever.

One rung of that ladder carries a condition git does not document: `VISUAL` is read only when
`TERM` names a terminal git believes it can draw on, so with `TERM` unset or `dumb` — a CI step, an
agent, `ssh host git commit` — git passes over `VISUAL` and takes `EDITOR`, and with neither set it
answers nothing and exits 1 rather than name a full-screen editor. So `EDITOR` is the dependable
variable for a headless environment and `core.editor` the dependable one everywhere; `VISUAL` alone
can be invisible to git. It is not invisible to git-pair, which falls back to `VISUAL` in exactly
the case where git refused to answer — a deliberate difference, asserted in `console_test.go`.

The resolved value is a command line, expanded by `/bin/sh` with word splitting exactly as git
expands it, so `core.editor="code --wait"` works: `code` is the program and `--wait` its flag. A
program path containing a literal space needs quoting inside the value
(`git config core.editor '"/my editor.sh" --wait'`), again as it does for git. The editor runs
with the repository root as its working directory and receives the absolute path of the file.

Diff viewing goes through git, so git configuration decides what you see, e.g.
`git config diff.tool vimdiff`. `git pair diff --tool` and the TUI's `Enter` key both run
`git difftool --no-prompt <from> -- <paths>` — one revision, so the tool compares the span's
start against your **working tree** instead of two blobs, and a path may be a directory, which is
git's own way of asking for every file the span changed under it. That is what makes the right-hand
buffer the real file: edits persist, and changes you made with `e` show up when you open the
tool again. A span that ends at a commit instead of your working tree has no working tree in the
comparison, so it passes both pinned revisions — `git difftool --no-prompt <from> <to> -- <paths>`
— and both buffers are the blobs that span named. `diff.tool`, `difftool.<tool>.cmd` and
`difftool.prompt` behave as elsewhere.

The TUI holds its screen across a handoff. The session takes an alternate screen and keeps it
while the editor or difftool runs, so the shell's scrollback is never exposed — a session that
releases the screen first shows the command history for as long as the tool takes to start, and
then inherits whatever the tool prints on the way out, `vimdiff`'s `2 files to edit` among it.
When the tool closes, the session clears the screen it shares with the tool and redraws, and
when the session ends the screen you were on comes back exactly as it was, scrollback
included.
The printed and `--stat` diffs stay on the committed span, because they describe review state
while the tool is for working on it. Plain `git pair diff` runs `git diff` with
`core.quotePath=false` and inherits your pager and colour settings; the span label goes to
stderr so stdout stays pipeable.

`git pair review` is an orchestration screen, not an editor (`git pair review open` is the same command
under a longer name). What the review is made of is at the top, the changed files and their marks below it,
and the reviewed counter under those:

```text
╭ tuishow ─────────────────────────────╮
│ base  main                           │
│ span  unreviewed ▸                   │
│ ABOUT.md                             │
│ ▾ Threads                            │
│     does-the-lock-cover-the-map.md   │
│     + new thread…                    │
╰──────────────────────────────────────╯
═══════════════════════════════════════
▾ ○ src/
    ▾ ○ ui/
        ○ picker.ts
    ○ a.ts +
═══════════════════════════════════════
0 / 2 reviewed
────────────────────────────────────────
j/k move  gg/G ends  ctrl-d/u half page  ctrl-f/b page  h/l fold  c fold all  enter open  d diff
space reviewed  e edit  a about  t threads  T new thread  v spans  V picker  s submit  p preview
z full  f files  tab preview  q quit
```

The screen has two regions where the keys can be: the changeset box at the top, and the file tree under
it. The tree is bounded by a rule of its own above and below, and those two rules are its focus light —
single while the keys are in the box or the diff, double while they are in the tree, the same convention
the box's borders and the divider use. Rules rather than a frame because a frame's two cells each side
are columns, and the narrow terminal that needs the regions told apart most is the one with no columns to
spare; the row between the box and the tree was blank, so the pair costs one row of the tree's window.
Each rule also carries the count of what the window hides at the end that count is about — `↑ 3` on the
top rule for the rows above the window, `↓ 11` on the bottom rule for the rows below it — with whitespace
each side so it reads as a note pinned to the rule rather than as the last cell of a row. The two counts of
one region share a numeric field as wide as the longer of them, so the arrows sit in one column and the
digits line up under them (`↑  1` above `↓ 34`), and the pair reads as the two ends of one thing rather than
as two remarks a cell apart. It goes on a
rule because a rule is not a row: the count used to have a row of its own, which cost the window a row
the moment the list was scrolled, and the row it cost was the one a page had just landed the cursor on.

`Tab` is the toggle between the two things the screen is made of — the list column and, where the terminal
has room for one, the diff. The box is not a third stop: it is the other half of the list column, so `k`
from the tree's top row steps into it and `j` from its last row steps back out. `Tab` hands the keys back to
the half that gave them up, and each half keeps its own cursor, so `Tab` returns to the row it left rather
than to the top of a list. `f` names the tree from wherever the keys are, and `a` and `t` name a row of the
box — `ABOUT.md`, the thread heading — and take the keys with them. Where there is no room for a column
beside the list, the diff's half of the toggle is its whole-screen form: `Tab` opens it, and from inside it
`Tab` and `f` close it on the way back. The navigation keys belong to the half holding them and mean the
same thing in both: `j`/`k` a row, `gg`/`G` the two ends of the half holding them, `ctrl-d`/`ctrl-u` half
a page, `ctrl-f`/`ctrl-b` a page. A page is counted in the window you are standing in, which is why
paging stops at the shared edge while a single step crosses it. A jump is counted the same way, for the
same reason: the two halves are two windows of two heights, so `gg` and `G` stop at the ends of the half
with the keys, and the way into the other half is `Tab`, `f`, `a` or `t` — keys that each say where they
are going. A half with no rows has no ends of its own, and there the jump goes to the half that has them.
(`ctrl-d` used to quit, which is why paging used to be
`ctrl-f` and `ctrl-b` and nothing else; `ctrl-c` and `q` are what quit.) The shortcut bar is the bar of the
half that holds the keys, which is how a key belonging to the other half is a key that is not offered rather
than a key that quietly does nothing.

Each file sits under its directory, and a row prints only the name the rows above it have not said.
A directory that holds nothing but one directory is folded into that row. `src/` above holds `a.ts` and `ui/`,
so it gets a row of its own. A chain of single-child directories becomes one row printed `docs/plans/active/`.
Each level is indented four cells, so a child's name starts two cells right of the directory it is under.
That is what the four cells are for: a directory row spends two on its fold arrow and two on its mark
gutter before its name, and a file row only the gutter, so an indent of two a level puts every child's
name in exactly the column its parent's name started in — which is a flat list with arrows in it.
`h` and `l` fold and unfold the directory under the cursor — arrows do the same, and `Enter` does
both, the way it does for the thread heading — and `c` folds the whole tree and opens it again,
which is how a changeset of a hundred files gets read for shape before it gets read for detail.
Folding a directory that was hiding the cursor leaves the cursor on the directory, not on whatever
row its old index now points at.

One character goes after a file's name for what the span did to that file. git's own status answers it: `+`
for a file the span created, `-` for one it deleted, `~` for one it moved. No sign means the span only changed
the file. That is what a span usually does, so a sign marks the exception a reviewer came to find.

The character is dim, and it sits after the name the way a directory's count sits after its name. It is a fact
about the file rather than part of its name. The move is git's rename detection, so a repository with
`diff.renames` off gets `-` and `+` for the pair git called two files.
A directory's mark is its subtree's: `✓` when every file under it is reviewed, `○` when none is,
and between the two the count of what is left (`▸ ◐ src/ 2/7`) — a tick there would be a claim about
files nobody opened. `Enter` does whatever the row under the cursor is for: the difftool for a
file, fold or unfold for a directory and for the `Threads` heading, the title prompt for `+ new
thread…`, and for a changeset document the same decision `d` makes. `d` means diff on any row. A file
row always has a comparison; so does a directory, where git expands the pathspec into every file the
span changed under it — `d` is "diff this package". A document
is worth diffing only when the span changed it *and* it already existed where the span starts — on a
second round of review, that is the two lines the author rewrote after your last submission, which is
what you came back for. A document the
changeset invented has nothing on the left side of that comparison, so it opens in the editor, and so
does one the span left alone; the editor's exit carries a note saying which of the two
happened, because anything written before the handoff is under the editor by the time you look
again. `e` opens the editor regardless of the span, so it is always one key away — for a directory it
names the two keys that do reach what is under it instead. `Space`
toggles reviewed on a file row — which stays under the cursor, since marking is not navigation — and
on a directory row it sets every file under it, folded or not, because a fold is a way of looking at
the list rather than a statement about what has been read; the next press clears them. The rows in the
box are read rather than diffed, so marking one says so. `a` and `t` put the
cursor on `ABOUT.md` and on the thread heading from wherever the keys are in the column, and take the keys
with them; `Enter` on the heading collapses or expands the list, and `e` on the row `a` landed on opens
`ABOUT.md` in the editor whatever the span did. `T` prompts for a new thread from anywhere — in the
footer's own line, over the list and the diff both, rather than as a screen that takes them away while the
title is typed; `v`
steps to the next span this session has been in — the span it opened on, the full and
unreviewed presets, and any span chosen with `V` — every stop in order, wrapping, so nothing on
the ring is unreachable. It means *next* whatever else just happened, a refusal included: one press
of `v` out of a read-only span is not something the ring can promise, since what sits next is
whatever the session visited next, and getting to a span you can review in one keystroke is `V`,
whose head column always offers `Current`. A stop whose commit, tag or ref has since gone is stepped
over and named in the notification band, reason included — the span stays on the ring, so a tag that
comes back is a stop again; `s` opens a submit prompt taking `b`, `f` or `a`; `q` quits.
A thread created from the list is written, opened in the editor, and left selected in the box, so the
reviewer can fill it in and come straight back to it; the preview keeps showing the file it was showing,
because the row it reads that diff from is the file tree's cursor, which a thread has no business moving.
The thread heading counts what it hides —
`▸ Threads (3)` collapsed, plain `▾ Threads` once the threads are on screen — and a rule
separates the whole list from the shortcut bar, so the bar reads as chrome rather than as more
rows.

The box is a box because its rows are not files, and a row of paths under a counter of files reads as a
file. It holds the span the review is measured against, `ABOUT.md`, the thread heading, the threads, and
the row that starts another — what a reviewer reads before choosing a file, above the files it is
choosing between. The first time the box takes the keys it opens on `ABOUT.md` rather than on its first
row: the span above it is the thing a reviewer *changes*, and the span is what they set when they came,
not what they came to read. Once the reviewer moves the cursor themselves the box leaves it where they put
it, including across a trip to the editor. It is capped at a third of the space the terminal gives and scrolls inside its own
borders, so forty threads are a reason to read the box rather than a reason to hide the tree behind it;
each border says how many rows are off that end (`↑ 3` in the top border, `↓ 3` in the bottom one), which
is a note inside the border rather than a row of its own because a note that came and went would move the
whole layout. The borders
are also the box's focus light: single rules while the keys are elsewhere, double rules (`╔ ═ ╗`) while
the box holds them, the same convention the divider uses for the diff and for the same reason — bold and
faint are the one thing a terminal is not obliged to render. The box is closed on all four sides in both
layouts: leaving its right side open where the diff column is drawn made the rows inside it read as a
column of loose text rather than as the changeset's own block, and a rule two cells away is not the same
rule. Its cursor row is highlighted only while the box holds the keys — the borders already say that much,
and a row that looks chosen and is not is a row a reviewer marks by mistake.

The span is the box's one control. Its row ends in `▸`, and `Enter` or `Space` on it opens the span
picker, because what a review is measured against is worth choosing; `base` above it is a line of the
frame rather than a row, because there is nothing to do with a base but read it. The picker changes
nothing until its own `Enter`, which is why the row works over a historical span too, where `Space` on a
file is still refused.

The shortcut bar occupies the bottom band, and the band is as tall as the tallest shortcut bar the
screen can show. The bar changes when the keys move between the file tree, the changeset box and the
diff; the band does not, and neither does a message about your last keystroke — the span `v` landed on,
what a refresh cost, why a key did nothing — which is drawn over the bar rather than under it. What
happens next is decided by what the message asked for. A note about what just happened fades after a
few seconds and the keys return, because missing it costs nothing. A refusal or a failure waits for
you, because the next key depends on having read it, and any keypress or `Esc` dismisses it. `Esc`
dismisses anything. A message long enough to need more rows than the band has loses its tail to an
ellipsis rather than growing the frame.

On a wide terminal the list shares the screen with a preview. From 100 columns and 16 rows the
session puts a column beside the list showing the diff of whatever the cursor is on — the span's
diff, the same one `git pair diff` and the reviewed counter describe — with git's own `+N −M` in its
header. The list takes the width its own rows need, up to 48 columns, and the diff gets the rest:
a changeset of short names is not made to share the screen with whitespace, and one long vendored
path cannot take the diff's columns — which is one thing the tree is for, since a row prints a base
name where the flat list printed the whole path. Park the cursor on a directory and the pane shows
what the span did to the whole subtree, git's pathspec doing the expanding, with the subtree's `+N −M`
rather than one file's. Each line carries the number git gave it in its hunk header,
and a line too wide for the column is broken rather than cut, with its colour carried across the break.
Tabs are shown as the spaces they advance to, because a tab the width maths scores as zero is a row the
terminal wraps for you.

The changeset box's rows come into the pane on the same terms: put the cursor on ABOUT.md or on a thread —
with `a` or `t`, or by walking up out of the tree — and the pane shows the file's own text, with the file's
own line numbers, rather than a diff of it. A diff of a document against nothing reports every line as
added, which is true and says nothing about what the document says, and the pane is the place on this screen
for reading text. The Threads heading is the whole conversation at once: every thread in the order the box
lists them, each named above its own text, which is what the threads are when the list is collapsed. The
header counts what is on show in the one place counts sit, beside the name: a diff's `+N −M`, a document's
lines, the heading's threads.

Two file rows come into the pane as the file, not as a patch: one the span created, one it moved unchanged.
A new file's patch is its own text with a `+` on every line. An unchanged move's patch is two lines about a path.
Every other file row keeps its patch, because a rename with edits has edits to show.

`Enter` follows the same rule the pane applies: on a file the span created it opens the editor, because
the comparison the key would otherwise open has nothing on one side of it. That is the choice the screen
already makes for an `ABOUT.md` or a thread the changeset invented. It is a different choice from `d`,
which opens the difftool on that same row: a reviewer who wants to see the `+` on every line has a key
that says so. An unchanged move keeps `Enter` on the difftool, because there the rename is the comparison
and it is the reason the file is under review at all.

A deletion is the only place the removed text still is, so its row keeps the patch too. The text is the file
at the span's head, not the working copy. What the reviewer has edited in it without committing is drawn
into that text where it lands, in the order git wrote it: the `-` line at the number that line has in the
file under review, then the `+` lines that replaced it, unnumbered — an added line is in no file anyone is
reviewing. The pane moves those lines to where they belong and reorders nothing, which is what keeps it the
same patch `d` and `git pair diff` show. Each of those rows ends in `← you`, the same mark that names your
rows in the diff's own reviewer section, and the pane's word rather than git's colour: in a pane that reads a
file as a file, every marked line belongs to the reviewer, and that is the fact the marker states. The header
says `you edited it` with git's counts for that section. A
move names the path it came from, which the tree's `~` has no room for. `Enter` there still opens the
difftool, because the row is still a file.

Over a historical span the text is still the file on disk, because that is the file `e` would open, so the
header adds `working copy` rather than letting someone read history that is not there; the editor stays
refused over history, since reading cannot change anything and writing can. `Enter` opens what the pane is
showing — the difftool for a diff, the editor for a document — the same choice the row makes when the key is
pressed there, and the pane's bar says `enter open` over a document and over a file the span added, and
`enter diff` over a diff. The search and the paging are the pane's rather than the diff's, so a name is
chased through the prose the way it is chased through a hunk.

`p` moves into the pane and does nothing else: the keys go to the diff, and it scrolls with the
keys the whole-screen preview uses — `j`/`k` a row, `d`/`u` or `ctrl-d`/`ctrl-u` half a page,
`ctrl-f`/`ctrl-b` a page, `gg` the top and `G` the bottom — over the file the pane was already showing. Paging a diff that
way is the whole point: the four page keys belong to whichever region holds them, so a diff too long to
fit is paged by moving into it rather than by borrowing the list's keys from across the screen.
`Enter` there opens the file on show — the difftool for it, or the editor for a file the span added,
which is the key the pane's own note points at.

`z` changes the shape of the screen and nothing else. Pressed where a diff is on show it takes that diff
to the whole terminal; pressed again it gives the screen back to the region that asked — the pane if `z`
was pressed inside the pane, the list if it was pressed there. The keys, the file and the place in the
file stay where they were, so the two presses are one reading gesture rather than two different ones and
the reviewer ends where they started. The two shapes are the two the terminal picks by itself: a column
beside the list where there is room, the whole screen where there is not. `z` is a key of the list column
too, where the row under the cursor says which diff — so reading every diff at full width never needs the
pane first, and the keys come with the screen, because a whole-screen diff that left them in a list it had
just hidden would be reading a keystroke from nothing. Where a column was given and no longer fits, `z`
says so with the number the window is short by instead of closing the diff you were reading; where there
was never a column it has nothing to refuse over, and gives the list back.

`Esc` hands the keys back to the region that had them — the tree, or the box if the keys came from the box —
and leaves the pane where it was, so coming back returns to the same lines. `p` does not do that: pressed
where the diff already holds the keys it is the no-op its name promises, because a key that meant "the
diff" in one region and "not the diff" in the one it just moved you to has to be remembered rather than
read off the screen. `q` quits, as it does from every other part of the screen — the overlay below is
the one place it does not, and the reason is that the list is still drawn here. `tab`, `shift-tab` and `f`
leave the pane without quitting: the diff is one of the two stops the keys have, and a stop you can only
leave by backing out of it is not a stop. What `p` no longer does is hide the pane — in a
terminal with room for two columns the diff is on screen, and the key that leaves it alone is `Esc`. While the diff holds the keys nothing
that changes the review happens: `Space`, `e`, `T`, `s` and the rest are keys that do not occur, the
same promise the whole-screen preview makes, with the difference that here the list is still on the
screen and the row you are not marking is one you can see. The two jumps into the box — `a` and `t` — work
here and take the keys with them, the way `tab` and `f` do: the box is drawn beside the diff you are
reading, so the cursor they move is one you can see move, and the pane follows the jump on to ABOUT.md or the
threads, which is usually why you left the diff. The overlay is the exception and the rule is the box — the
overlay is the one screen where the box is not drawn — so under it those two are keys that do not occur, and
the way to the box is the key that brings the list back. The frame says which column has the keys
three ways: the divider becomes a double rule (`║` instead of `│`), the pane's file line stops being a
caption and becomes a title, and the list's cursor loses its reverse video so the screen never carries
two cursors. The first two are glyphs and text, so they survive a terminal that renders no styling at
all. The shortcut bar becomes the pane's own, which names every key the pane reads and none of the ones
it does not.

Searching the file on screen is `/`, which reads a term in the pane's bottom row — the row the note
about the rest of the file uses, so the field costs no rows and moves nothing. Every match is marked as
the term is typed, and the marks are SGR rather than colour: underline for the matches, reverse video for
the one you are on, because the diff's green and red are git's and a highlight that painted over them
would hide which kind of line a match sits on. `Enter` closes the field and jumps to the first match at or
below what was already on screen; `n` and `N` walk the matches and wrap at both ends, moving the pane only
as far as it has to to keep the match visible. A term written entirely in lower case is looked for in any
case, and one with a capital in it exactly, so `/lock` finds `LockManager` while `/Lock` does not find
`lock`. The term is looked for in git's line rather than in the row it was drawn on, so a term the column
broke in half is still found — and marked on both halves. The bottom row counts the matches while the pane
is scrolled, which is what says whether `n` has anywhere left to go. `Enter` on an empty term clears the
marks, and `Esc` closes the field and keeps the term that was already committed, because a mistyped
search should not cost you the match you were reading; the second `Esc` gives the keys back, as it did
before the field existed. `Backspace` at an empty field closes it and `ctrl-u` kills what has been typed,
as they do in a shell's prompt, and the field stays on the pane's bottom row whatever the length of the
file — a short one is padded rather than leaving the prompt floating under its last line. While the field is open it owns the keys — `q` types a `q` rather than quitting,
and the pane's scrolling keys wait — which is the same promise the thread title makes. The term outlives the
file it was typed in: move to another row and the same term marks that one too, which is how a name you are
chasing across a changeset gets chased; the row it had landed on does not travel, because it means nothing
in the new file.

Below those dimensions there is no room for two columns, and the person who pressed `p` wanted the
diff — so `p` gives the diff the whole screen, which is the same request answered as well as the window
allows. Pressed there it is inert, since the diff already has the screen and the keys. A terminal dragged
below those dimensions while the pane has the keys loses both — a column that is not drawn cannot hold
the keyboard, and the list takes the keys back. Same git bytes, same numbers, same wrapping, full
width, with the file and the span it is measured against on the line above it (the span is named in
the list otherwise, and the list is gone). It scrolls with `j`/`k`, `d`/`u` or `ctrl-d`/`ctrl-u` for half a page,
`ctrl-f`/`ctrl-b` for a page, `gg` for the top and `G` for the bottom, and `/` searches it the same way the
pane does. `Esc`, `Enter` or `q` gives the list
back, and so do the keys that move the keys — `tab`, `shift-tab` and `f` — since the half of the list one of
them names is only drawn once the diff stops covering the screen. The diff is a stop on the ring here as much as anywhere, which is what lets `tab` reach it: a narrow
terminal is the one case where the ring would otherwise have a hole where the pane cannot be. Keys mean
what this screen's shortcut bar says they mean while it is up — including `q`, which gives the list back
here as `esc` and `enter` do rather than quitting as it does in the pane, and `ctrl-d`, which pages rather
than quits; nothing else reaches through, because a reviewer who cannot see
the list must not be able to mark a file in it. Closing keeps the
place however you close it: `p`, `Esc`, `p` returns to the same lines, and so does `f`, `p`. `z` gives the
list its column back wherever the terminal has one to give back, and the bar names the key only there: on
the terminal too narrow for a column the key can only refuse, and a bar that names a refusing key names a
feature nobody is offered.
Reading a historical span this way works the same — reading is what a read-only span is for. Under 40
columns or 13 rows even this is unreadable, and the key says which way the terminal is short, naming
the smaller of the two asks because that is the one worth growing to. The row floor is its bar's
fault: the shortcut bar is what wraps there, and a key the band cannot draw is a key nobody is
offered, so the floor counts the bar's rows and lets the diff have what is left.

The pane draws your uncommitted edits into the author's diff, at the line numbers they carry: a line you
deleted is drawn where the author's patch shows that line rather than twice, once as the author's addition and
once as your deletion, and a line you added follows the line it sits after. Each line you changed carries
`← you`, and only those lines do — the lines around them belong to the file, the author's rows below belong to
the author, and git's rows about which file the patch is about are nobody's work. Your counts are on the header
beside the file — `main.go  +2 −0  ·  you edited it  +1 −1` — so the number next to the path cannot be read as a
tally that includes your typing. None of this is decoration: git's bytes do not say who typed them, and an added
line you wrote is the same green as one the author wrote.

The merge works on line numbers, and it needs the numbers of one file to work: on a directory's pane, or a
changeset box, several files share the screen and a number on it belongs to no one of them, so there the
reviewer's section stays a section — its own hunk headers and git's chrome, above the author's rows with a blank
row between. A file the pane reads as text has no diff to merge into at all, so your edits are drawn into the
file's own text instead, at the position each one lands in.

On the pane of a single file your rows also take a colour of the pane's own, because the pane has one more fact
to tell and a line number is how it knows it. An addition you made is blue. A line you deleted that the span
had added is purple, and leads with `×` where git drew `-`: it is the row that undoes reviewed work, which is
not what a deletion of a line that predates the span says, and that one is amber. A line number is what tells
those two apart, so the colours appear only where the numbers all belong to one file.

Measuring your edits from the revision under review rather than from the span's start is what keeps them from
repeating the author's work. What they mean is "not in the revision under review", which is your typing while
the author's work is committed — the state `change ready` hands you. In a tree where the author is still
working it carries their unreviewed lines too, and marks them as yours: nothing on this screen can tell who
typed an uncommitted line. The reviewed counter still counts the span alone, so your typing never changes what
"reviewed" means — and `d`/`Enter` still open the working tree, which is where those edits live.

What the pane prints is git's own bytes and git's own colours: no hunk model, no folding, and no colour of its
own on the author's rows — the line that keeps it a preview rather than a diff renderer, since reading a diff
properly means opening it and `Enter` is one keypress away. What it does is place two diffs git printed side by
side by the numbers in their headers. The one sign it rewrites belongs to a row that was the span's work and is
yours no longer, and it keeps git's bytes on every row as what that row is a line of, so a search finds `-gone`
on a row the pane painted with `×`.

A span can start anywhere as well as end anywhere — `diff --base-ref=main`,
`--base-commit=abc1234`, `--base-review=0` — which is how a script names a starting point without
computing the merge base itself, and how the same span can be described from either end
(`--base-review=0 --head-review=1` is `--since-review=0 --head-review=1`).

A span can end at a commit instead of your working tree — `review open --head-review=-1`,
`--head-commit=abc1234`, `--head-ref=origin/main` — and that is a look at history, not a review. The
screen stops offering anything that changes something: the counter's place is taken by
`HISTORICAL · READ ONLY`, files and directories lose their reviewed gutter, `+ new thread…` is gone, and the
shortcut bar lists only what still works. Folding the tree is one of the things that still works — it
changes nothing about the review, and a historical changeset is as worth reading for shape as a live
one. Pressing a key that does not work says why, names the head
the span is stuck on, and points at `V`, which is how you choose a span you can review — `v` walks the
session's spans and cannot promise where it lands. The difftool
over a historical span compares its two pinned commits rather than your working tree, so it cannot
show you work the span does not contain. `git pair diff` takes the same flags and just prints; the
read-only half is about the screen, where the mistakes would be made.

`V` opens the span picker, where both ends are chosen before either takes effect: `Tab` moves between
the BASE and HEAD columns, `j`/`k` move, `Space` sets the end under the cursor, and `Enter` applies
the pair — with the lines under the columns saying what that pair resolves to, in the same words the
header and the band use (`main...current`, not a second spelling of the same span), and whether
the screen would go read-only, before you commit to it. When git cannot resolve the pair there is no
span to name, and the line shows what you chose instead (`changeset base → nonsense`). `u` and `f` are the unreviewed and full-changeset
presets; `Esc` leaves the span exactly as it was. Each column lists the review submissions — the newest
as `Last Review`, then `Review -2` and `Review -3`, older ones by the index you would type — then
`Commit…` and `Ref…`; only the base offers the changeset base, and only the head offers `Current`
(the live end, labelled `latest + edits`). `HEAD` appears nowhere in either list: beside `Current` it
would be two similar-looking live targets when only one of them can be edited. `Commit…` is a searchable list of subjects and short ids that also takes a typed
revision, so history past the window is one keystroke away, and refuses an id git does not know while
the list is still on screen. `Ref…` groups branches, remote refs, tags and other refs under headings,
showing `main` and `origin/main` while the checkpoint keeps `refs/heads/main` — a branch and a tag
with the same name are two different choices, and drift has to be watched on the one you meant.

Inside either drill the keys are in one of two modes, and the shortcut bar names the keys of the mode
you are in. Typing is the default, and what you type is the filter — a space included, since `response
0` is a thing to search for — with `Enter` picking and `Esc` stepping back to the columns. `Tab` hands
the keys to the list: `j`/`k`, `gg`/`G`, `ctrl-d`/`ctrl-u` and `ctrl-f`/`ctrl-b`, `Space` or `Enter` to
pick, `Tab` to give the keys back to the filter. The block after the filter is the caret, so it is
where your typing goes; navigation mode drops it. `Backspace` deletes a character and nothing else —
with an empty filter it does nothing, because it used to throw the whole drill away. A drill also says
which end it is choosing for (`for BASE`), since the columns that would otherwise say it are off screen.
A checkpoint chosen from a drill has no row of its own, so the asterisk goes on the `Commit…` or `Ref…`
row it came from, and `V` reopens with the cursor there. A short terminal shrinks the candidate lists
rather than the frame: the shortcut bar wraps into rows first and the band is counted at the height it
always takes, because a frame taller than the terminal repaints by scrolling and what scrolls off the
bottom is the bar that says how to leave.
`Ctrl-C` leaves from any of these screens — the columns, a drill, the submit prompt.

An endpoint named as a ref is pinned when you choose it, and the pin is what the screen keeps
comparing. So when `probe` moves in another window — a fetch, someone else's push — the header still
reads `probe@bb0f343` and the band says:

```text
⚠ probe moved bb0f343 → 43915ed  [r] refresh
```

Nothing follows the branch by itself. `r` re-pins the endpoint to where the ref points now, recomputes
the span and reports what that cost: marks are keyed on each file's diff within the span, so the ones
whose diff changed stop applying, and the count in the report says how many. Ignoring the banner is a
legitimate answer too — the span does not move until asked. The warning is derived from the span rather
than written by the last keystroke, so nothing you type erases it: a note may borrow the band for its few
seconds, and then the warning is back. A ref that has gone away is not drift: there is nothing to refresh
to, and the pin still resolves to the commit it was chosen for.

In a narrow window the shortcut bar wraps between shortcuts rather than through them —
`space reviewed` never arrives split in half — and the list gives up the rows it takes.
Submitting commits the review, moves the ref and **ends the session**, printing one line about
what it did; `Esc` from the prompt returns to the list. The terminal is released while an
external program runs and the repository is re-scanned afterwards, so a reviewed mark survives
only while that file's diff within the span is unchanged.

Marks also survive quitting: they are written under the repository's git directory at
`$(git rev-parse --absolute-git-dir)/git-pair/marks/<changeset>/<commit>.json`, keyed on the commit
the review was looking at, with each file's diff key stored beside it. Reopening the same commit
restores exactly the marks whose files still have that content, so a new commit, a rebase, or a
different span cannot bring back a mark that no longer describes anything, and clearing every mark
is remembered rather than resurrected. A directory you marked is in that file as its files, one row
each — nothing is recorded about the directory, and no fold is — which is why marking a package and
reopening it lands the marks on the same nine files rather than on whatever the tree happens to look
like. Nothing is committed or shared — `git status` cannot see the
directory and `git add` cannot stage it — and no command reports marks, so derived state is
unaffected. Deleting that directory forgets the marks; the newest 12 commits per changeset are
kept.

## Development

`mise run check` is the gate: gofmt, `go vet`, then the Go test suite. Run it before you call a
change done.

The suite runs in several `go test` processes (`scripts/test-sharded.sh`) rather than one. Most of a
test's span is a git subprocess, and one process cannot overlap them: `internal/cli` and
`internal/tui` drive the product in-process through the process working directory and the process
standard streams, so their tests take turns. Splitting a package's tests across processes by name
keeps each process single-threaded exactly as it is today, and reclaims the overlap. `mise run
test:serial` is the same suite in one process, with the output `go test ./...` gives.

Two scripted replays sit above it. Each one runs the installed binary as a subprocess, so it reaches
what a Go test cannot:

| Gate | What it proves | Needs |
| --- | --- | --- |
| `scripts/gates/e2e-29.sh` | The PRD §29 loop end to end in a scratch repo, through review, approve, `check`, record, publish and `--fetch` | `git` |
| `scripts/gates/pty-walkthrough.sh` | The review TUI under a real pty: first paint, the file tree, marks, the span walk, the difftool handoff | `git`, `python3` |

`mise run gates` builds the binary, then runs both. Each script also takes a binary path as its first
argument. A pipeline that installs the build elsewhere passes its own path.

Both scripts resolve the default binary by the rule `mise run build` uses. A branch installs and tests
its own namespaced name, rather than a build another worktree left behind. See
`scripts/install-name.sh`.

Work here is planned in `docs/plans/<name>/plan.md`, and a finished plan moves to
`docs/plans/completed/`. Each change carries its own directory under `changesets/`. The tool reviews
itself. `git pair change ready` offers the change, and `git pair review open` opens it for a
reviewer.

## Troubleshooting

`no changesets/<id>/ directory exists in a81c123, so there is nothing to record` (exit 1,
`integration record`) — the recorder discovers the changeset from the directories the source commit
carries, so this means one of two things, and the message names both: `--source` is the wrong commit
(the usual mistake — `--source` is the head that was reviewed, not the merge commit), or the changeset
directory was never committed, which is the changeset having no identity to land. An abbreviated SHA is
fine; `--source` is resolved through `rev-parse` before discovery.

`more than one changeset directory exists in a81c123: aaa and bbb` (exit 2) — a stacked child carries
its parent's directory as well as its own. `--changeset <id>` names which one the record is for.

The recorder also refuses a record it does not believe, and each of these says which belief failed:

`nothing in a81c123's history is a git-pair marker for changeset booking, so this head has never been
offered for review` (exit 1) — the directory is committed content, so tree discovery finds the changeset
in a head nobody has offered. `change ready` offers the work and `review submit --approve` records the
verdict; a landing follows a verdict. The sibling refusal, `the newest marker for booking ... is
`Review-State: ready`, which is an offer rather than a verdict`, is the same rule one step later: an
offered-but-unreviewed head is not integration-ready, and `git pair check` refuses it too. If this pair
lands on non-blocking feedback, `--allow-feedback` says so.

`no marker in a81c123's history names changeset booking; the markers there name booking-v1` (exit 1) — the
commit carries the directory and its own history reviews a different changeset, which is what a copied
directory or a renamed id leaves behind. The record would say this head is the reviewed head of `booking`,
and the commits disagree; the refusal names both ids because "review it again" would be the wrong advice.

`4f2b8c1 is not reachable from main: the record would say the work landed somewhere it is not` (exit 1) —
the destination check. When you passed `--target`, that ref is the destination and the commit is not in it.
When you did not, the refusal says what it tried and where each guess came from (the changeset's `base:`,
then the default branch) — and says the flag that settles it, because landing a change on a release branch
is allowed, only not silent: `--target release/2.x`.

`more than one changeset on main is missing its integration record: booking and booking-follow-up`
(exit 2, no flags given) — the command was asked to work out which changeset landed, and two of them are
waiting for a record. `--changeset <id>` picks one; naming `--source` and `--commit` picks one and says
which commits. `no branch here carries changesets/booking/, so git-pair cannot see which head was reviewed`
is the same rule at the other end, for a landing that did not carry the chain itself: a squash or a
cherry-pick keeps the reviewed head on no commit that survives, so with the branch gone nothing in this clone
holds it unless the durable refs were fetched, and guessing which commit was approved is the one thing this
command must not do. Both refusals name the flag that settles the question.

`nothing to record: each of the 9 changeset directories on main already has its archive and integration refs
here` (exit 2, no flags given) — the destination's directories all have their pairs, so there is nothing
left to write. It is a usage error rather than green because a record is create-only — "record it again" is
never the finding — and because a pipeline that reads zero as success is a pipeline in which a landing sits
unrecorded while `tidy` is allowed to delete the branch that still holds the chain. A run that can name one
changeset gets the other answer: `--changeset <id>`, or a destination that carries exactly one directory,
reaches the record and exits 0 with `already_recorded`. The remedy the refusal names for a landing on a
branch git-pair did not search is `--target <ref>`; for a record written in another clone it is fetching the
namespace.

`4f2b8c1 does not add changesets/booking/ over its first parent d91c21e` (exit 1) — the commit carries the
directory but did not bring it in, so it is a follow-up on the destination branch rather than the landing.
The refusal names the transition it wanted and the `git log --first-parent` command that lists the
candidates; a merge, a squash or a cherry-pick of the reviewed branch is the commit to record.

`the durable ref already exists at a different commit: refs/git-pair/integrations/booking records
d91c21e, and 4f2b8c1 was asked for` (exit 1) — the changeset is already recorded, and git-pair never
moves a durable ref, so there is no flag for this. A landing that needs correcting is corrected in git
and recorded under an id with no record yet; the refusal names both commits so a re-run from a stale
pipeline can be told from a genuine second landing. A re-run whose commit is a *descendant* of the
recorded one is neither: it is the stacked landing, and it succeeds with `carried_by` instead of refusing.

`warning: this clone holds no refs/git-pair/* refs at all; fetch them before trusting anything that
says never recorded` — printed by `integration record` alongside a record it wrote anyway. The candidate
rule reads trees, not refs, so an empty namespace does not change what gets written; what it changes is
what a reader elsewhere believes, and a clone that has never fetched is one where *never recorded* is a
claim about the clone. A clone maps `refs/heads/*` into `refs/remotes/*` and nothing else, so the refs
are absent even from a clone of a repository that published them properly:
`git fetch origin 'refs/git-pair/*:refs/git-pair/*'`. `git pair check` says nothing about refs at all,
because nothing in its verdict depends on one.

`cannot resolve changeset base "main": unknown revision: main` (exit 1) — the `base` in
`CHANGESET.yaml` is not a ref in this repository (renamed trunk, fresh clone, merged stack).
git-pair never guesses a base: edit the file, or `git pair init --base <ref> --set-base`.
From `init` with no `--base`: `cannot infer a base: no main or master branch exists;
pass --base <ref>` (exit 2).

`base:` is written as a branch name — `main`, not `refs/remotes/origin/main` — because `git clone` records
the remote's default branch in `refs/remotes/origin/HEAD` and that is the answer `DefaultBranch` prefers,
so the ref it reaches is usually a fetch ref even in a clone with a local trunk. The two spellings resolve to
one branch every time a base is read (local first, then fetched), and the file is read on machines that have
fetched different things, so the name is what belongs in it. Where the name resolves to nothing — a clone
holding the integration branch only under the fetch root — the qualified ref is recorded instead, because a
base that does not resolve fails every command. Where the local copy of that branch and its remote copy are
different commits, `init` says so and gives the counts, because the diff measured from here is then not the
diff the forge will show: `note: main is not the same commit here and on origin: 1 here that origin does not
have, 0 on origin that is not here`. It is a note; pushing trunk is yours.

`cannot tell which branch is the integration branch: ...` (exit 2) — there is nothing to compare
against, so "has this landed?" has no answer and every changeset directory on the revision would
look like work in progress. Pass `--default-branch origin/main` (a CI job that fetched one branch
has no recorded remote HEAD, and a repository may name trunk something else), or record git's own
answer once with `git remote set-head origin --auto`. `init` reports the same problem as
`cannot infer a base: ...; pass --base <ref>`, because the recorded base and the landed test come
from the same resolution. In CI the missing answer is usually the checkout: a job that fetched one
branch has neither a remote HEAD nor `origin/main` to compare against, which says nothing about the
changeset it was asked about — the refusal names the fetch shape
(`git fetch origin '<branch>:refs/remotes/origin/<branch>'`) beside the flag.

```text
this revision contains more than one changeset: aaa and bbb; name the one you mean with
--changeset <id>, or record the choice with `git pair change use <id>`
```

(exit 2) — the branch carries two unlanded changeset directories and nothing in the branch's own
history orders them: no commit touched one directory more recently than the other, and neither names the
other as its `base:`. That is what a sibling merged in looks like, and what a branch created off a sibling looks
like once it starts its own work. `git pair change use <id>` settles it for the branch, by recording
the choice in the chosen changeset's `CHANGESET.yaml`; `--changeset <id>` answers for one command;
and `git pair init --base <sibling>` at creation time is what makes a stack readable without
any record at all.

`main is the integration branch, so a changeset started on it can never contain anything` (exit 2
from `init`) — a changeset is measured against the integration branch, so one started on it
can never contain anything. `git switch -c <branch>` first.

A changeset that reads as uninitialised on its own branch, or that has vanished from
`queue`, usually means its directory reached the integration branch — commonly because a
sibling merged your unlanded branch and *that* landed. Your directory is in trunk's tree, which is
precisely what the rule tests, so the cure is to land your own branch rather than someone else's
merge of it. `git ls-tree <integration-branch> changesets/` shows whether the directory is there — and
if it is there while no integration record names it, `queue` does not leave you to work that out:
it prints the changeset under `LANDED, UNRECORDED`.

`ABOUT.md already has content: changesets/<cs>/ABOUT.md is not empty (pass --set-about to
replace it)` (exit 2) — `init --about` refuses to discard a description that is
already there. Same rule, same flag shape as changing a base.

`self-referential changeset base: changeset "main" cannot be based on main, the branch it
lives on` (exit 2 from `init`, exit 1 from `change ready`) — everything is measured as
`base...HEAD`, so a changeset based on its own branch is empty forever and its `ready` marker
can never be observed. Use a branch of its own, or point `base` at an ancestor. The test is on
the ref, not the commit: a branch created a moment ago shares `main`'s tip and is valid.

`N additions from review <sha> still survive unchanged` (exit 1) — resolve them, or
acknowledge them deliberately:

```bash
git pair change ready --allow-surviving-review-additions
```

Only the most recent review counts, and only additions outside `changesets/<changeset>/`
block; surviving `ABOUT.md` and thread text is listed as non-blocking. It is the only hatch left:
`git pair check` has no override flag, because its answer is what a merge acts on, and the recorder runs
no diagnostic at all — refusing to write down a landing that has already happened would be an argument
about a decision the author can no longer un-take (PRD §19.3).

```opening an editor needs a terminal```, ``` `git pair review open` needs a terminal ``` and
``` `git pair review reopen` needs a terminal ``` (exit 2) — both stdin and stdout must be
character devices. Under an agent, a pipe or cron, edit the
files directly and use `git pair diff`, `git pair status` and `git pair review submit`.

`changeset has no review submissions yet; run git pair diff for the full changeset` (exit 2) —
`--unreviewed` and `--since-review` need a review submission. Out of range:
`no review at index 9: this changeset has 1 review(s) (valid: 0..0 or -1..-1)`.

`"<path>" does not appear in main...HEAD; changed paths: ...` (exit 2) — the path is not in
the resolved span; the error lists what is.

`working tree must be clean ...` (exit 1) — `change ready` and `change unready` act
on committed state. `init` commits its scaffolding, so a fresh changeset does not block `change
ready`; it does block it if you then edit `ABOUT.md` without committing. Use
`init --no-commit` to fold the scaffolding into your first implementation commit
instead.

`no changeset for this branch: changesets/foo` (exit 2), or `HEAD is detached; check out a
branch first` (exit 2) — the directory is named after the branch by default, so a detached HEAD has
no name to derive one from. Renaming a branch needs nothing: resolution reads which changeset
directories the revision carries, not which branch you are standing on, and `--changeset <id>` reads
one from any branch while HEAD stays where it is.

`git pair check` printing `NOT READY:` while `git pair status` says `APPROVED` is one of three things,
and every bullet names which:

- **The branch was rewritten.** `review 91bf204 reviewed a7f3c98, which is no longer in this history` —
  the approval named a commit (`Review-Head`) that this branch no longer contains, so a rebase, amend or
  force-push moved under the review. The tree may match exactly; that is the case this condition exists
  for. Re-approve the rewritten head (`git pair change ready`, then a review submission), or merge the
  base instead of rebasing onto it. A bullet naming a head this repository `does not have` is different:
  that is a fetch gap, not a rewrite, and it is the clone that needs fixing.
- **The content moved.** `content outside changesets/<cs>/ changed since …` — that is what the approval
  was about, and a changeset-only commit is not that drift, so the approval stands.
- **The policy refused.** The newest review is `feedback`, which the default policy does not accept; the
  bullet says so, and `--allow-feedback` is the switch.

A fourth bullet, `changeset is already integrated at …`, is not a problem to fix: the record says the
review is over, and re-running the gate after a landing reports that rather than a second opinion.
`check` is also the one command that ignores your
working tree: it asserts the commit, and uncommitted edits are not in `HEAD` to be reviewed.

A missing entry in `git pair queue` is usually not a queue bug: membership is derived
state, and only a command moves it — `change ready`, `change unready`, or a review submission. A
code change after the ready marker leaves the changeset in the queue, naming the commits in its
reason; what that drift does stop is `git pair check`, which refuses a head whose reviewed content has
moved. A changeset whose content has landed in its base is not listed, and
says nothing: the branch may already be gone, and the queue asks what a reviewer can act on. The
queue asks what a reviewer can act on, and it answers the same way from `main` as from the
changeset's own branch: it enumerates local branches and resolves each one's changeset, so
membership does not depend on where you happen to be standing. Rows are per branch rather than per
changeset — a review is a commit appended to a branch, so the branch is what is ready — which means one
changeset on two branches (a copy made to try something else, a parent and its child) is two rows with two
heads, and each row prints the branch it speaks for. A branch it cannot resolve — two changesets on one
branch, say — is named in `skipped` rather than left out quietly.

A missing entry that is *not* work in progress is reported rather than hidden. A changeset directory the
integration branch carries with no integration record is a landing nobody recorded, and `queue`
gives it its own `LANDED, UNRECORDED` heading with the `git pair integration record` invocation that
closes the gap; `git pair status` on a branch carrying no changeset of its own says the same inside its
exit-2 answer. It is the one state where the paper trail is nothing but the merge commit, which is why
the queue looks for it instead of waiting to be asked. Read it as "not recorded *here*" until
`git fetch origin 'refs/git-pair/*:refs/git-pair/*'` says otherwise.

A hand-written ready marker counts only if `Review-State: ready` and `Review-Changeset: <slug>`
sit in a real trailer block, separated from the subject by a blank line and from each other by
no blank line.

A head whose newest review does not permit integration is `check`'s problem, and it reports it as a
`NOT READY:` bullet with exit 1 — the gate working, and every other failed condition in the same run.
A changeset whose work is recorded as landed is refused by the commands that would move it instead:
`change ready`, `change unready`, `change abandon` and `review submit` all say
`changeset <id> is recorded as integrated at <sha>, so git-pair records nothing further for it` (exit 1)
and write nothing.
