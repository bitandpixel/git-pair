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
  a stack is only a `base:` value in `CHANGESET.yaml`
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

$ git pair change init --base main
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
  queue: `git pair review queue` now lists this changeset

$ git pair review queue
READY FOR REVIEW

booking-transaction
  base: main
  ready: 0s ago
  head: 8065dae
```

The reviewer runs `git pair review open` for the TUI, or works from the CLI. Here they edited
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
  next:    author: `git pair check`, then merge into main with ordinary git, then `git pair integration record`
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
next:  `git pair check`, then merge into main with ordinary git, then `git pair integration record`
```

Then the owner lands it, with ordinary git. Squash, rebase-merge, plain merge — git-pair has no
opinion and takes no part; it neither runs a merge nor derives one, because squash and cherry-pick
destroy the ancestry that would have said so.

One command records where the work went, and it is the only git-pair write in the whole handoff:

```bash
$ git pair integration record --source 0eaad3b --commit 4f2b8c1 --target main
booking-transaction: recorded 4f2b8c1 as the integration of 0eaad3b
  archive:     refs/git-pair/archive/booking-transaction -> 0eaad3b
  integration: refs/git-pair/integrations/booking-transaction -> 4f2b8c1
  verified reachable from main
```

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
own with `git pair change init --id booking-transaction-v2`; an ID is never rewritten to fit,
never suffixed to dodge a collision, and never changed once the changeset has refs.

`CHANGESET.yaml` records `id` and `base` and nothing else. `base` is what the diff is measured
against, and for a stack names another changeset. There is no branch field: the directory does
not belong to a branch, so renaming a branch strands nothing, and two clones of the same commits
cannot disagree about what the directory is.

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
nested `refs/git-pair/changesets/<id>/archive`), so a repository upgraded from one reports no record for
work recorded under the old path; those commits stay reachable from their branches.

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
`change unready`, `change wait`, `review submit`, `review history`, `review queue`, `check` and
`integration record` change output for it; elsewhere it is accepted and ignored.

Every command also accepts `--default-branch <ref>`, which states the integration branch that
"has this landed?" is measured against. Without it git-pair reads git's own answer
(`refs/remotes/origin/HEAD`, then a sole `origin/main` or `origin/master`, then a local `main` or
`master`) and refuses if there is nothing to compare against. It is a flag and not a config key
because the answer belongs to the checkout in front of the command: a CI job that fetched one
branch has no remote HEAD, and two clones of one repository must not disagree about what has
landed.

| Command | Flags | Notes |
| --- | --- | --- |
| `change init` | `--id <id>`, `--base <ref>`, `--set-base`, `--about <text>`, `--set-about`, `--no-commit` | creates directory, `CHANGESET.yaml`, `ABOUT.md`, then commits them; never overwrites existing content; `--about` also reads a pipe; default base is the integration branch; refuses on that branch, where a changeset could never contain anything; `--id` names the changeset instead of the branch-derived default, and a collision with a committed directory or ref refuses rather than suffixing |
| `change ready` | `--allow-surviving-review-additions` | fully non-interactive; checks below |
| `change unready` | none | withdraws the changeset from the review queue; records `Review-State: working` when the changeset is in review, otherwise succeeds and records nothing; refuses a changeset whose work is recorded as integrated |
| `change use <id>` | none | records which changeset a branch carrying more than one is working on: writes `ignores: <other ids>` into the chosen changeset's `CHANGESET.yaml` and commits that file; refuses an id the branch does not offer and a record that would leave the branch still undecided; idempotent |
| `change abandon` | none | records the terminal `Review-State: abandoned` and nothing else — no ref, since an abandoned changeset has no landing to record; `change ready`, `change unready` and `review submit` refuse against it afterwards; refuses a changeset whose work is recorded as integrated; idempotent |
| `change feedback` | `--stat`, `--name-only`, `--changeset <slug>` | the diff of the most recent review submission (`review^..review`): threads, `ABOUT.md` edits and reviewer code edits together; exits 2 if there is no submission |
| `change wait` | `--fetch`, `--interval <dur>` (default `10s`), `--timeout <dur>` | blocks until the state leaves `READY` for `BLOCKED`/`FEEDBACK`/`APPROVED`; read-only; `--fetch` runs `git fetch` before each check so a review pushed from another clone is noticed |
| `review open` | `--unreviewed`, `--since-review[=N]`, `--base-review[=N]`, `--base-commit`, `--base-ref`, `--head-review[=N]`, `--head-commit`, `--head-ref` | TUI; needs a terminal; full changeset unless a span flag says otherwise; a `--head-*` flag opens a historical span, which is read-only |
| `review reopen` | none | TUI on `<last review>..current`, the work that has landed since you reviewed; needs a terminal; refuses if no review exists |
| `review about` | — | opens `ABOUT.md` in the editor, creating it if missing |
| `review thread [title...]` | — | slugifies the title, reopens an existing match, prompts for a title only with a terminal |
| `review submit` | one of `--block`/`--feedback`/`--approve`, `-m/--message <text>`, `--no-stage` | stages the whole tree by default, commits (empty commits allowed), and writes nothing else: a submission is a marker commit, not a ref move. The commit names what it reviewed with `Review-Head`, which is what lets `check` refuse a rewritten history |
| `review history` | `--changeset <slug>` | only review marker commits, indexed from `0`, each naming the commit it reviewed under `REVIEWED` |
| `review queue` | — | every branch in this repo whose changeset is `READY`, longest wait first; read from the repository, not the checkout |
| `status` | `--changeset <slug>` | derived state, for this branch's changeset or one named by slug |
| `check` | `--allow-feedback` | asserts integration-readiness and exits 1 when it is not; lists every failed condition — the review's outcome, whether the commit it approved is still in this history, and whether the content still matches; no `--changeset`, because it is the gate a forge runs *on* a revision |
| `integration record` | `--source <sha>`, `--commit <sha>`, `--target <ref>`, `--changeset <id>` | writes both durable refs for one changeset, create-only: the archive at `--source` and the integration at `--commit`. The changeset is discovered from the `changesets/<id>/` directories `--source` carries and the integration branch does not, so a pipeline needs the two SHAs it already holds and not the changeset name; `--changeset` disambiguates a stacked child. Verifies `--commit` is reachable from `--target` and carries the changeset directory; needs no checkout and writes no commit; re-running it with the same pair succeeds and changes nothing |
| `diff [path...]` | `--unreviewed`, `--since-review[=N]`, `--base-review[=N]`, `--base-commit`, `--base-ref`, `--head-review[=N]`, `--head-commit`, `--head-ref`, `--stat`, `--tool` | paths are checked against the span first, so a typo is an error, not an empty diff |

`change ready` checks, in order: clean working tree, `ABOUT.md` exists, the repository has
commits, no blocking surviving additions — the last acknowledged with
`--allow-surviving-review-additions`. `check` asks what the merge would act on: the newest marker is a
review whose outcome permits integration (`approve`, or `feedback` under `--allow-feedback`), nothing
unreadable came after it, the changeset has not ended, it has not already been recorded as integrated,
and the tree still matches what the review looked at (ignoring `changesets/<cs>/`). It reads no other
ref, and every failed condition is reported in one run. `change unready` checks
only for a clean tree, since the marker it writes is empty. `change init` warns
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
| 1 | a git-pair rule or the repository state refused the operation | surviving additions; `working tree must be clean`; `ABOUT.md is missing`; `cannot resolve changeset base "vanished"`; `change wait` timing out, or refusing a changeset that is `WORKING`; `git pair check` printing `NOT READY:`; `integration record` finding no changeset directory at `--source`, a landing that does not carry the changeset, a record naming a different commit, or a changeset already recorded as integrated |
| 2 | usage error | unknown flag, unknown command, or unknown subcommand of `change`/`review`/`integration`; `no changeset for this branch`; `no branch carries changeset "<slug>"`; `cannot tell which branch is the integration branch`; detached HEAD; `--block, --feedback and --approve are mutually exclusive`; `changeset has no review submissions yet`; `changeset <cs> has no review submission yet` (`change feedback`); `--interval expects a duration` (`change wait`); `--fetch` with no remote configured; `"<path>" does not appear in <span>`; editor/TUI commands without a terminal; more than one changeset directory in `--source` and none named with `--changeset` |
| 3 | the repository or git itself failed | `not a git repository`; a git subprocess exiting non-zero for a reason other than an unresolvable revision |

The split between 1 and 2 is deliberate and load-bearing for agents: exit 1 means the
invocation was correct and the repository said no, so fix the state and retry; exit 2 means
the invocation itself was wrong, so retrying unchanged will fail again.

## JSON contracts

Output is indented two spaces, and empty lists may serialise as `null` rather than `[]`
(`review queue`, `review submit`'s `files`), so test for both.

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
is a fact beside the state, not a sixth state value. `integrated_in_default_branch` says whether that
commit is in the history of the branch git-pair calls the integration branch, and
`integrated_default_branch` names that branch — work that retired into `release/2.x` and never reached
the default branch must not read like a default-branch landing, and what git-pair reports is the
containment it can derive rather than a branch name no ref stores. `default_branch`,
`default_branch_commit` and `default_branch_source` name the branch "landed" was measured against,
the commit it pointed at, and how the run learned it (`flag`, `origin-head` or `sole-candidate`):
a CI log that says nothing has landed has two causes, a stale fetch and a wrong trunk, and neither
one is visible in a sentence about the changeset.
`next_action` is the handoff (§PRD §9.5): `check`, then the merge with ordinary git, then
`integration record`. Once the record exists it says there is nothing further to record instead.

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

`git pair review queue --json` — `head` and `ready_commit` are full SHAs. `skipped` names changesets
the queue cannot explain (`null` when empty): a branch whose metadata cannot be read, a branch the
resolution rule cannot settle between two changesets, a recorded changeset whose archive is in no base
and on no branch, or a changeset whose integration ref says it landed — that one names the commit,
because the branch is usually still here and its disappearance from the queue would otherwise be a
mystery. A changeset that has landed in its base, and a directory with no record at all, are both silent
— neither is work a reviewer can act on, and a clone that never fetched `refs/git-pair/*` is silent here
rather than wrong (§PRD §13.4).

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
  "skipped": ["untracked-work (cannot resolve changeset base \"other\": unknown revision: other)"]
}
```

`git pair review history --json`

```json
{
  "changeset": "booking-transaction",
  "reviews": [
    {
      "age": "0s",
      "author": "Rae",
      "index": 0,
      "outcome": "block",
      "sha": "332887cf6413e66d45d0ad5d59d93f7d30484ffd",
      "short": "332887c",
      "reviewed_head": "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
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
  "files": null,
  "next_action": "author: `git pair check`, then merge into main with ordinary git, then `git pair integration record`",
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
compare them against the revisions it built. `target` is empty when `--target` was not given, and
then no reachability check was made. `recorded` means this call wrote at least one ref and
`already_recorded` means both refs already named this exact pair: a retry is a success that changed
nothing, and the two are worth telling apart in a log.

```json
{
  "changeset": "booking-transaction",
  "source": "a7f3c98d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
  "commit": "d91c21ed5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0c",
  "target": "origin/main",
  "archive_ref": "refs/git-pair/archive/booking-transaction",
  "integration_ref": "refs/git-pair/integrations/booking-transaction",
  "recorded": true,
  "already_recorded": false
}
```

`git pair change ready --json` returns the `status` fields plus `ready_commit` (full SHA),
`review_queue_visible`, `acknowledged_survivors` and `surviving_review_artifacts`.

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
git pair change init --base main --about "$ABOUT"   # once, on a named branch
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

Never prompt: `change init`, `change ready`, `change unready`, `change abandon`, `change feedback`,
`change wait`, `status`, `check`, `diff`, `review submit`, `review history`,
`review queue`. They report
and exit instead of asking, even with a terminal attached.

Refuse with exit 2 when stdin or stdout is a pipe or a regular file, because launching an
editor or the TUI against one would hang: `review open`, `review reopen`, `review about`,
and `review thread`
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

**Recording the landing.** After the merge, the same job tells git-pair where the work ended up:

```bash
git pair integration record --source "$SOURCE_SHA" --commit "$TARGET_SHA" --target origin/main
```

`--source` is the head that was reviewed — the branch tip, not the merge commit — and `--commit` is the
commit the work became. The changeset is discovered from the `changesets/<id>/` directories `--source`
carries and the integration branch does not, so the pipeline needs no name a human chose; `--changeset`
settles it when the source carries two, which is what a stacked child does. A squash, a rebase and a
cherry-pick are all recordable, and none of them leaves ancestry between the two SHAs: the record is what
connects them. Both refs are create-only — a re-run with the same pair is a success that changed nothing,
and asking for a different commit is refused, naming both — and once the record exists `check` refuses the
changeset as already integrated and the commands that write markers refuse it too. `--target` is optional;
with it, git-pair verifies the landing commit is reachable from the ref you name — the one assumption it
will accept about where the work went.

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

Publishing is the other half, and git-pair does not do it: nothing in the tool runs `push`, so the
refs stay in the author's clone until the repository is configured to share them.

```bash
git config --add remote.origin.push '+refs/git-pair/*:refs/git-pair/*'
```

Documented rather than configured on anyone's behalf, because publishing review history is a decision
about who gets to read it — the archive holds every commit of the review, including ones the author
later dropped. Until that line is run, the author's clone holds the only copy, and a CI job reporting
missing refs is describing exactly that.

## Configuration

The editor is whatever git would use: git-pair asks git with `git var GIT_EDITOR`, so the
precedence is git's — `GIT_EDITOR`, then `core.editor`, then `VISUAL`, then `EDITOR`, then
whatever fallback the git build was configured with. `git config core.editor vim` is therefore
enough, including when it is set in the repository rather than globally, and a caller that
injects `GIT_EDITOR` (a hook, another tool) is honoured the way every other git consumer
honours it. Only when git cannot answer does git-pair fall back to `$VISUAL`, then `$EDITOR`, then
`vi`. Automated harnesses must clear `GIT_EDITOR` along with `VISUAL`/`EDITOR`, and every editor
probe should be wrapped in `timeout`, because an editor that takes the terminal and is never
driven will block forever.

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

`git pair review open` is an orchestration screen, not an editor. What the review is made of is at the
top, the changed files and their marks below it, and the reviewed counter under those:

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
    ○ a.ts
═══════════════════════════════════════
0 / 2 reviewed
────────────────────────────────────────
j/k move  gg/G ends  ctrl-d/u half page  ctrl-f/b page  h/l fold  c fold all  enter open  d diff
space reviewed  e edit  a about  t threads  T new thread  v spans  V picker  s submit  p preview
f files  tab preview  q quit
```

The screen has two regions where the keys can be: the changeset box at the top, and the file tree under
it. The tree is bounded by a rule of its own above and below, and those two rules are its focus light —
single while the keys are in the box or the diff, double while they are in the tree, the same convention
the box's borders and the divider use. Rules rather than a frame because a frame's two cells each side
are columns, and the narrow terminal that needs the regions told apart most is the one with no columns to
spare; the row between the box and the tree was blank, so the pair costs one row of the tree's window.

`Tab` is the toggle between the two things the screen is made of — the list column and, where the terminal
has room for one, the diff. The box is not a third stop: it is the other half of the list column, so `k`
from the tree's top row steps into it and `j` from its last row steps back out. `Tab` hands the keys back to
the half that gave them up, and each half keeps its own cursor, so `Tab` returns to the row it left rather
than to the top of a list. `f` names the tree from wherever the keys are, and `a` and `t` name a row of the
box — `ABOUT.md`, the thread heading — and take the keys with them. Where there is no room for a column
beside the list, the diff's half of the toggle is its whole-screen form: `Tab` opens it, and from inside it
`Tab` and `f` close it on the way back. The navigation keys belong to the half holding them and mean the
same thing in both: `j`/`k` a row, `gg`/`G` the two ends of the column, `ctrl-d`/`ctrl-u` half a page,
`ctrl-f`/`ctrl-b` a page. A page is counted in the window you are standing in, which is why paging stops at
the shared edge while a single step crosses it. (`ctrl-d` used to quit, which is why paging used to be
`ctrl-f` and `ctrl-b` and nothing else; `ctrl-c` and `q` are what quit.) The shortcut bar is the bar of the
half that holds the keys, which is how a key belonging to the other half is a key that is not offered rather
than a key that quietly does nothing.
directory holding nothing but one directory is folded into that row (`src/` above holds `a.ts` and
`ui/`, so it gets its own row; a chain of single-child directories would be one row and print
`docs/plans/active/`), and every row prints only the name the rows above it have not already said.
Each level is indented four cells, so a child's name starts two cells right of the directory it is under.
That is what the four cells are for: a directory row spends two on its fold arrow and two on its mark
gutter before its name, and a file row only the gutter, so an indent of two a level puts every child's
name in exactly the column its parent's name started in — which is a flat list with arrows in it.
`h` and `l` fold and unfold the directory under the cursor — arrows do the same, and `Enter` does
both, the way it does for the thread heading — and `c` folds the whole tree and opens it again,
which is how a changeset of a hundred files gets read for shape before it gets read for detail.
Folding a directory that was hiding the cursor leaves the cursor on the directory, not on whatever
row its old index now points at.
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
when rows are off screen the bottom border says how many (`3 more`), which is a note inside the border
rather than a row of its own because a note that came and went would move the whole layout. The borders
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
header counts what is on show — a diff's `+N −M`, a document's lines, the heading's threads.

Over a historical span the text is still the file on disk, because that is the file `e` would open, so the
header adds `working copy` rather than letting someone read history that is not there; the editor stays
refused over history, since reading cannot change anything and writing can. `Enter` opens what the pane is
showing — the difftool for a diff, the editor for a document — the same choice the row makes when the key is
pressed there, and the pane's bar says `enter open` over a document and `enter diff` over a diff. The
search and the paging are the pane's rather than the diff's, so a name is chased through the prose the way it
is chased through a hunk.

`p` moves into the pane and does nothing else: the keys go to the diff, and it scrolls with the
keys the whole-screen preview uses — `j`/`k` a row, `d`/`u` or `ctrl-d`/`ctrl-u` half a page,
`ctrl-f`/`ctrl-b` a page, `gg` the top and `G` the bottom — over the file the pane was already showing. Paging a diff that
way is the whole point: the four page keys belong to whichever region holds them, so a diff too long to
fit is paged by moving into it rather than by borrowing the list's keys from across the screen.
`Enter` there opens the difftool on that file, which is the key the pane's own note points at. `Esc`
hands the keys back to the region that had them — the tree, or the box if the keys came from the box —
and leaves the pane where it was, so coming back returns to the same lines. `p` does not do that: pressed
where the diff already holds the keys it is the no-op its name promises, because a key that meant "the
diff" in one region and "not the diff" in the one it just moved you to has to be remembered rather than
read off the screen. `q` quits, as it does from every other part of the screen. `tab`, `shift-tab` and `f`
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
pane does. `Esc` or `Enter` gives the list
back, and so do the keys that move the keys — `tab`, `shift-tab` and `f` — since the half of the list one of
them names is only drawn once the diff stops covering the screen. The diff is a stop on the ring here as much as anywhere, which is what lets `tab` reach it: a narrow
terminal is the one case where the ring would otherwise have a hole where the pane cannot be. Keys mean
what this screen's shortcut bar says they mean while it is up — including `q`, which quits here too, and
`ctrl-d`, which pages rather than quits; nothing else reaches through, because a reviewer who cannot see
the list must not be able to mark a file in it. Closing keeps the
place however you close it: `p`, `Esc`, `p` returns to the same lines, and so does `f`, `p`.
Reading a historical span this way works the same — reading is what a read-only span is for. Under 40
columns or 13 rows even this is unreadable, and the key says which way the terminal is short, naming
the smaller of the two asks because that is the one worth growing to. The row floor is its bar's
fault: the shortcut bar is what wraps there, and a key the band cannot draw is a key nobody is
offered, so the floor counts the bar's rows and lets the diff have what is left.

Below the author's diff, the pane shows whatever you have edited without committing, under
`── you · uncommitted` with counts and line numbers of its own. That caption is not decoration: git's
bytes do not say who typed them, and an added line you wrote is the same green as one the author
wrote. Measuring that section from the revision under review rather than from the span's start is
what keeps it from repeating the author's work. The reviewed counter still counts the span alone, so
your typing never changes what "reviewed" means — and `d`/`Enter` still open the working tree, which
is where those edits live.

What the pane prints is git's own bytes: no hunk model, no folding, no colours of its own — the line
that keeps it a preview rather than a diff renderer, since reading a diff properly means opening it
and `Enter` is one keypress away. The frame fills the terminal: the list keeps the window's height
even when there are few files, so the shortcut bar rests against the bottom edge.

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

## Troubleshooting

`no changesets/<id>/ directory exists in a81c123, so there is nothing to record` (exit 1,
`integration record`) — the recorder discovers the changeset from the directories the source commit
carries, so this means one of two things, and the message names both: `--source` is the wrong commit
(the usual mistake — `--source` is the head that was reviewed, not the merge commit), or the changeset
directory was never committed, which is the changeset having no identity to land. An abbreviated SHA is
fine; `--source` is resolved through `rev-parse` before discovery.

`more than one changeset directory exists in a81c123: aaa and bbb` (exit 2) — a stacked child carries
its parent's directory as well as its own. `--changeset <id>` names which one the record is for.

`the durable ref already exists at a different commit: refs/git-pair/integrations/booking records
d91c21e, and 4f2b8c1 was asked for` (exit 1) — the changeset is already recorded, and git-pair never
moves a durable ref, so there is no flag for this. A landing that needs correcting is corrected in git
and recorded under an id with no record yet; the refusal names both commits so a re-run from a stale
pipeline can be told from a genuine second landing.

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
git-pair never guesses a base: edit the file, or `git pair change init --base <ref> --set-base`.
From `change init` with no `--base`: `cannot infer a base: no main or master branch exists;
pass --base <ref>` (exit 2).

`cannot tell which branch is the integration branch: ...` (exit 2) — there is nothing to compare
against, so "has this landed?" has no answer and every changeset directory on the revision would
look like work in progress. Pass `--default-branch origin/main` (a CI job that fetched one branch
has no recorded remote HEAD, and a repository may name trunk something else), or record git's own
answer once with `git remote set-head origin --auto`. `change init` reports the same problem as
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
and `git pair change init --base <sibling>` at creation time is what makes a stack readable without
any record at all.

`main is the integration branch, so a changeset started on it can never contain anything` (exit 2
from `change init`) — a changeset is measured against the integration branch, so one started on it
can never contain anything. `git switch -c <branch>` first.

A changeset that reads as uninitialised on its own branch, or that has vanished from
`review queue`, usually means its directory reached the integration branch — commonly because a
sibling merged your unlanded branch and *that* landed. Your directory is in trunk's tree, which is
precisely what the rule tests, so the cure is to land your own branch rather than someone else's
merge of it. `git ls-tree <integration-branch> changesets/` shows whether the directory is there.

`ABOUT.md already has content: changesets/<cs>/ABOUT.md is not empty (pass --set-about to
replace it)` (exit 2) — `change init --about` refuses to discard a description that is
already there. Same rule, same flag shape as changing a base.

`self-referential changeset base: changeset "main" cannot be based on main, the branch it
lives on` (exit 2 from `change init`, exit 1 from `change ready`) — everything is measured as
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
on committed state. `change init` commits its scaffolding, so a fresh changeset does not block `change
ready`; it does block it if you then edit `ABOUT.md` without committing. Use
`change init --no-commit` to fold the scaffolding into your first implementation commit
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

A missing entry in `git pair review queue` is usually not a queue bug: membership is derived
state, and only a command moves it — `change ready`, `change unready`, or a review submission. A
code change after the ready marker leaves the changeset in the queue, naming the commits in its
reason; what that drift does stop is `git pair check`, which refuses a head whose reviewed content has
moved. A changeset whose content has landed in its base is not listed, and
says nothing: the branch may already be gone, and the queue asks what a reviewer can act on. The
queue asks what a reviewer can act on, and it answers the same way from `main` as from the
changeset's own branch: it enumerates local branches and resolves each one's changeset, so
membership does not depend on where you happen to be standing. A branch it cannot resolve — two
changesets on one branch, say — is named in `skipped` rather than left out quietly.
A hand-written ready marker counts only if `Review-State: ready` and `Review-Changeset: <slug>`
sit in a real trailer block, separated from the subject by a blank line and from each other by
no blank line.

A head whose newest review does not permit integration is `check`'s problem, and it reports it as a
`NOT READY:` bullet with exit 1 — the gate working, and every other failed condition in the same run.
A changeset whose work is recorded as landed is refused by the commands that would move it instead:
`change ready`, `change unready`, `change abandon` and `review submit` all say
`changeset <id> is recorded as integrated at <sha>, so git-pair records nothing further for it` (exit 1)
and write nothing.
