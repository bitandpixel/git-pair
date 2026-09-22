# git-pair

`git-pair` is a local-first peer-review tool for human-plus-agent pairs. It puts a thin
review protocol on top of ordinary git commits, files, refs, editors and difftools rather
than replacing any of them: a changeset directory holds the change description and review
threads, lifecycle transitions are commits carrying `Review-*` trailers, review-relative
diff spans answer "what happened since my last review", and `refs/git-pair/changesets/*` keeps the
complete unsquashed history reachable so a branch can be squash-merged without losing the
review conversation. All review state lives inside the repository, so no code path talks
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

`CHANGESET.yaml` records the changeset's `id`, its `base` and the `branch` that owns it;
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
  ref:     refs/git-pair/changesets/booking-transaction/archive -> 332887c
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
  ref:     refs/git-pair/changesets/booking-transaction/archive -> 0eaad3b
  next:    author: `git pair change archive` before squash/merge
```

The owner then archives the changeset. That half is theirs rather than the reviewer's: an approve
is a judgement about the code, and deciding that the reviewed state is what gets taken forward is
the owner's call. The approval already left the archive current, so this says so and adds nothing:

```bash
$ git pair change archive
Changeset booking-transaction is already archived at 0eaad3b

refs/git-pair/changesets/booking-transaction/archive already points there; nothing moved.

Safe to squash/merge.
Review history stays reachable at refs/git-pair/changesets/booking-transaction/archive
```

An archive ref moves forward and never backwards, so replying in a thread or rewriting `ABOUT.md`
after the approval does not strand the archive short of `HEAD`:

```bash
$ git pair change archive
Archived changeset booking-transaction at 4f2b8c1

refs/git-pair/changesets/booking-transaction/archive: 0eaad3b → 4f2b8c1
```

The gate is its own command, because deciding and observing are different jobs: `status` reports
what the history says, `check` asserts it, and CI reads the exit code.

```bash
$ git pair check
OK: booking-transaction is integration-ready
archive: 4f2b8c1
```

Deleting the branch loses nothing:

```bash
$ git switch main && git branch -D booking-transaction
Deleted branch booking-transaction (was 4f2b8c1).

$ git rev-list --count refs/git-pair/changesets/booking-transaction/archive
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

When two directories survive, the nearest review ref decides, then the `base:` of a stack; when
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
merged the deletion, so they come back — with their whole review history, because the archive ref still
points at them. Prune only what carries an integration ref or a terminal record: those stay retired
because the durable refs, not trunk's tree, are what say the work is over.

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
| review | `review: <outcome> <slug>` | `Review-Outcome: <outcome>`, `Review-Changeset: <slug>` |
| unready | `git-pair: unready <slug>` | `Review-State: working`, `Review-Changeset: <slug>` |
| abandon | `git-pair: abandon <slug>` | `Review-State: abandoned`, `Review-Changeset: <slug>` |

A commit counts as a marker only when its trailer block parses and `Review-Changeset` matches
the changeset being inspected; anything else is an ordinary commit.

**Readiness is withdrawn with a command.** `git pair change unready` commits `Review-State: working`
and takes the changeset out of the queue. Readiness is an offer made with `git pair change ready`, so
withdrawing it is a command too: an author who wants to keep implementing after handing off says so,
instead of leaving a reviewer to guess whether a changeset in the queue is finished work or work in
progress. `working` is not a sixth state — it is `WORKING` chosen on purpose, and a changeset with no
marker derives the same answer. The marker is written only when the changeset is in review (`READY`,
`APPROVED` or `FEEDBACK`); on a `WORKING` or `BLOCKED` changeset there is nothing to withdraw, so the
command succeeds and records nothing. Withdrawing an approval does not delete it: the approval stays
in `git pair review history`, and archiving refuses until a reviewer approves again.

**Archiving moves a ref, not a marker.** `git pair change archive` commits nothing. It advances the
changeset's archive ref onto `HEAD`, and the changeset is finished when that history is merged into
the deployment branch — ordinary git, which git-pair neither runs nor derives. An archived changeset
therefore still reports `APPROVED` (or `FEEDBACK`): `archive_ref` and `archive_commit` say where the
archive stands, and squash-safety is claimed only while that commit is `HEAD`. Ownership follows the
same split: the reviewer approves the code, the owner decides it is what gets taken forward. There is
one ref per changeset and it only moves forward, so nothing can quietly un-archive a chain a squash
merge is about to depend on.

**A changeset can end.** `git pair change abandon` commits `Review-State: abandoned` and moves the
archive ref to it, which is what keeps the whole chain reachable after the branch is deleted. It is
not a sixth state: the changeset reports `WORKING`, because `WORKING` already means "not in review,
nothing owed", and `abandoned_commit` in `status --json` carries the ending beside it. That split is
deliberate — `state` is the field agents branch on, and a changeset that can never move again has to
be recognisable there without teaching every consumer a new state name, so the fact lives in its own
field instead. The ending is durable in both places it can be read from: the branch, and the archive ref.
`change ready`, `change unready` and `review submit` refuse against an abandoned changeset whichever
they meet first, which is also what stops a new branch reusing the name of one that ended. Unlike
`change unready`, which withdraws an offer for now, this one closes the changeset, and re-running it
records nothing.

**A review is corrected by submitting again.** There is no `review undo`. The newest
submission decides the state, earlier ones stay in `git pair review history`, and the summary of
a later submission names what it supersedes. Undo by rewriting history is not on the table —
git-pair runs no `reset`, `rebase` or `push`, and a review commit may already be shared; a
withdrawal *marker* would work but needs a trailer older git-pair builds cannot read, which
would leave two versions of the tool disagreeing about one branch. Moving the review ref back
is not an undo either: state comes from commit trailers, so the submission would still count.

**Derived state.** State comes from walking `base..HEAD` and reading the newest marker, and
nothing else moves it: a commit is not an event in this model. Readiness survives you pushing
more work, and `git pair change unready` is what takes a changeset out of the queue — taking an
offer back is a decision, and decisions are recorded rather than inferred. The drift is still
visible: the reason line counts what arrived since the marker, e.g.
`marked ready by 8065dae (2 commits since)`.

One command asks the harder question, and it is the one whose output gets trusted.
`git pair change archive` compares the tree between the newest marker and `HEAD` and refuses when
anything outside `changesets/<slug>/` differs, because the archive ref it moves is what an agent
checks before squash-merging and it has to name content somebody reviewed. A
commit touching only `ABOUT.md` or a thread is not that drift, and comparing trees rather than
counting commits is what keeps merges and rebases from reporting a change that never happened.
States:
`WORKING`, `READY`, `BLOCKED`, `FEEDBACK`, `APPROVED`. `git pair status` prints the
state plus a one-line `Reason`. There is no state file, and no state for a completed
changeset: completion is an archive ref (below), and the merge that finishes a changeset is not
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
branch — which is what keeps the work findable after the branch is deleted:

```text
refs/git-pair/changesets/<changeset>/archive        the chain: movable, forward-only
refs/git-pair/changesets/<changeset>/integration    where it landed: written once, never moved
```

The archive holds the whole implementation/review/fix chain. `change ready` writes it
at the ready marker, and every submission moves it to the exact resulting `HEAD`, in the same
operation that creates the commit, keeping the whole chain reachable from
garbage collection. The handoff is where the history starts being worth keeping, so an
offered-but-never-reviewed changeset is written too. `change archive` advances it over review
artifacts, `change abandon` moves it to the terminal marker, and `change unready` moves it onto the
withdrawal — the branch is what gets deleted, and a retraction that lives only on the branch is a
retraction the durable record never received. It moves forward and never
backwards: the command refuses a `HEAD` behind the current tip rather than dropping the chain from
the only ref that keeps it reachable. It is not a guarantee against `git push --delete`; it keeps
Git from pruning what git-pair still needs.

The integration ref records where the work landed, and `git pair integration record` writes it once.
It is explicit because the fact is not derivable: a merge preserves ancestry, while a squash, a rebase
and a cherry-pick each destroy it, and git-pair is meant to treat all four alike. Patch IDs and
diff-equivalence can help a human recover a lost record; they are not the protocol, and a tool that
inferred integration from them would be confidently wrong about every squash merge. Once the record
exists the archive freezes: no git-pair command writes another marker for that changeset or moves its
archive, because `archive A → integration B` is a statement other people read as fact — and markers are
refused before their commit is written, so a refused command leaves nothing behind.

Both are children of `refs/git-pair/changesets/<changeset>` rather than that path itself, because a
git ref cannot be both a leaf and a namespace — git refuses the leaf once a child exists. Nothing reads
the `refs/reviews/*` layout an earlier version used, so a repository
upgraded from one reports no archive for changesets archived before the move; their commits stay
reachable from their branches, and archiving again writes the new ref.

**Surviving review additions.** Review lines left untouched disappear from a `review..HEAD`
diff, so `change ready` and `change archive` re-derive them with
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
`change unready`, `change wait`, `change archive`, `review submit`, `review history` and
`review queue` change output for it; elsewhere it is accepted and ignored.

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
| `change unready` | none | withdraws the changeset from the review queue; records `Review-State: working` and moves the archive onto it when the changeset is in review, otherwise succeeds and records nothing |
| `change use <id>` | none | records which changeset a branch carrying more than one is working on: writes `ignores: <other ids>` into the chosen changeset's `CHANGESET.yaml` and commits that file; refuses an id the branch does not offer and a record that would leave the branch still undecided; idempotent |
| `change abandon` | none | records the terminal `Review-State: abandoned` and anchors it; `change ready`, `change unready` and `review submit` refuse against it afterwards; idempotent |
| `change feedback` | `--stat`, `--name-only`, `--changeset <slug>` | the diff of the most recent review submission (`review^..review`): threads, `ABOUT.md` edits and reviewer code edits together; exits 2 if there is no submission |
| `change wait` | `--fetch`, `--interval <dur>` (default `10s`), `--timeout <dur>` | blocks until the state leaves `READY` for `BLOCKED`/`FEEDBACK`/`APPROVED`; read-only; `--fetch` runs `git fetch` before each check so a review pushed from another clone is noticed |
| `review open` | `--unreviewed`, `--since-review[=N]`, `--base-review[=N]`, `--base-commit`, `--base-ref`, `--head-review[=N]`, `--head-commit`, `--head-ref` | TUI; needs a terminal; full changeset unless a span flag says otherwise; a `--head-*` flag opens a historical span, which is read-only |
| `review reopen` | none | TUI on `<last review>..current`, the work that has landed since you reviewed; needs a terminal; refuses if no review exists |
| `review about` | — | opens `ABOUT.md` in the editor, creating it if missing |
| `review thread [title...]` | — | slugifies the title, reopens an existing match, prompts for a title only with a terminal |
| `review submit` | one of `--block`/`--feedback`/`--approve`, `-m/--message <text>`, `--no-stage` | stages the whole tree by default, commits (empty commits allowed), then moves the review ref |
| `review history` | `--changeset <slug>` | only review marker commits, indexed from `0` |
| `review queue` | — | every branch in this repo whose changeset is `READY`, longest wait first; read from the repository, not the checkout |
| `change archive` | `--allow-surviving-review-additions`, `--allow-unreviewed-changes` | advances the archive ref onto the reviewed `HEAD` and reports squash-safety; refuses a `HEAD` behind the archive; commits nothing; never merges, pushes or squashes |
| `status` | `--changeset <slug>` | derived state, for this branch's changeset or one named by slug |
| `check` | `--allow-feedback` | asserts integration-readiness and exits 1 when it is not; lists every failed condition; no `--changeset`, because it is the gate a forge runs *on* a revision |
| `integration record` | `--source <sha>`, `--commit <sha>`, `--target <ref>`, `--changeset <id>` | records where a changeset landed by creating its `integration` ref; the changeset is discovered from the archive ref pointing at `--source`, so a pipeline needs the two SHAs it already holds and not the changeset name; needs no checkout and writes no commit; created once, and the archive freezes afterwards |
| `diff [path...]` | `--unreviewed`, `--since-review[=N]`, `--base-review[=N]`, `--base-commit`, `--base-ref`, `--head-review[=N]`, `--head-commit`, `--head-ref`, `--stat`, `--tool` | paths are checked against the span first, so a typo is an error, not an empty diff |

`change ready` checks, in order: clean working tree, `ABOUT.md` exists, the repository has
commits, no blocking surviving additions. `change archive` checks: clean tree, newest
review at `HEAD` is `approve` or `feedback` and still describes what `HEAD` carries (the tree is
compared, ignoring `changesets/<cs>/`), no blocking surviving additions. The last two can be
acknowledged with `--allow-surviving-review-additions` and `--allow-unreviewed-changes`; a block
or a withdrawal cannot. `change unready` checks
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
| 1 | a git-pair rule or the repository state refused the operation | surviving additions; `working tree must be clean`; `ABOUT.md is missing`; `cannot archive <cs>: latest outcome is BLOCKED`; `cannot resolve changeset base "vanished"`; `change wait` timing out, or refusing a changeset that is `WORKING`; `git pair check` printing `NOT READY:`; `integration record` finding no archive at `--source`, a landing that does not carry the changeset, or a record that already exists |
| 2 | usage error | unknown flag, unknown command, or unknown subcommand of `change`/`review`/`integration`; `no changeset for this branch`; `no branch carries changeset "<slug>"`; `cannot tell which branch is the integration branch`; detached HEAD; `--block, --feedback and --approve are mutually exclusive`; `changeset has no review submissions yet`; `changeset <cs> has no review submission yet` (`change feedback`); `--interval expects a duration` (`change wait`); `--fetch` with no remote configured; `"<path>" does not appear in <span>`; editor/TUI commands without a terminal; more than one archive points at `--source` and none was named |
| 3 | the repository or git itself failed | `not a git repository`; a git subprocess exiting non-zero for a reason other than an unresolvable revision |

The split between 1 and 2 is deliberate and load-bearing for agents: exit 1 means the
invocation was correct and the repository said no, so fix the state and retry; exit 2 means
the invocation itself was wrong, so retrying unchanged will fail again.

## JSON contracts

Output is indented two spaces, and empty lists may serialise as `null` rather than `[]`
(`review queue`, `review submit`'s `files`), so test for both.

`git pair status --json`, waiting for the first review. Once a review exists `latest_review`
becomes `{"index": 0, "outcome": "block", "commit": "332887c"}`; `unrecognised_markers` (a
list of `<sha> <subject>`) appears only when non-empty. Read another changeset with `--changeset`
and two fields report that they cannot answer — `uncommitted` is `null` and `span` is `""` — because
both describe the checkout rather than the commit, and `next_action` names the branch to switch to.
`abandoned` is true once `change abandon` has ended the changeset, with `abandoned_commit` naming the
terminal marker; `state` stays `WORKING`, because the ending is a fact beside the state rather than a
sixth state value. `archive_ref` and `archive_commit` describe the
changeset's archive and stay `""` until something writes it — `change ready`, or a
review submission — because the ref's name is derivable from the changeset and its existence is the
only fact worth reporting. `integrated` is true once `git pair integration record` has recorded where
the work landed, with `integrated_commit` naming that commit, and `state` is untouched by it: landing
is a fact beside the state, not a sixth state value. `integrated_in_default_branch` says whether that
commit is in the history of the branch git-pair calls the integration branch, and
`integrated_default_branch` names that branch — work that retired into `release/2.x` and never reached
the default branch must not read like a default-branch landing, and what git-pair reports is the
containment it can derive rather than a branch name no ref stores. `default_branch`,
`default_branch_commit` and `default_branch_source` name the branch "landed" was measured against,
the commit it pointed at, and how the run learned it (`flag`, `origin-head` or `sole-candidate`):
a CI log that says nothing has landed has two causes, a stale fetch and a wrong trunk, and neither
one is visible in a sentence about the changeset.
`next_action` calls the changeset squash-safe only while `archive_commit`
is `HEAD`: an archive of an ancestor is a statement about history, not a claim that the work is
done. Once the record exists `next_action` says there is nothing further to record instead.

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
  "archive_ref": "refs/git-pair/changesets/booking-transaction/archive",
  "archive_commit": "8065dae",
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
the queue cannot explain (`null` when empty): a branch whose metadata cannot be read, a changeset
directory whose branch is gone, its anchor survives, and its content is not in its base, or a changeset
whose integration ref says it landed — that one names the commit, because the branch is usually still
here and its disappearance from the queue would otherwise be a mystery. A changeset
that has landed in its base, and a directory that was never offered for review, are both silent —
neither is work a reviewer can act on.

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
      "ready_age": "0s",
      "archive_ref": "refs/git-pair/changesets/booking-transaction/archive"
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
  "next_action": "author: `git pair change archive` before squash/merge",
  "outcome": "approve",
  "previous_review": "",
  "archive_ref": "refs/git-pair/changesets/feat/archive",
  "short": "941266b"
}
```

`git pair change archive --json` — `head` is the commit named and `state` is the derived state,
which archiving does not change. `archive_ref` is the ref, `archive_was` the commit it named before
the call (empty where there was none), and `archive_advanced` whether it moved: already being there
is a success, reported as such. `acknowledged_unreviewed_paths` counts what
`--allow-unreviewed-changes` covered, so archiving over drift reports `WORKING` with a non-zero
count beside it rather than looking like an ordinary approval.

```json
{
  "acknowledged_survivors": 0,
  "acknowledged_unreviewed_paths": 0,
  "archive_advanced": true,
  "archive_ref": "refs/git-pair/changesets/feat/archive",
  "archive_was": "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
  "base": "main",
  "changeset": "feat",
  "head": "941266b18686624cb624722b4e8c348bf03451a7",
  "short": "941266b",
  "squash_safe": true,
  "state": "APPROVED",
  "surviving_review_artifacts": 0
}
```

`git pair check --json` — the verdict an automation consumes. `ready` is the answer, `reasons` names
every failed condition (an empty array when it passed, so a consumer branches on `ready` rather than
handling two shapes), and `policy` records which rule produced the verdict — `approve-only`, or
`approve-or-feedback` with `--allow-feedback` — so a gate's decision in a log can be re-derived.
`head` and `archive` are full SHAs, not the short forms the human output prints, because the job
comparing them built one of them; `archive_current` is the two being equal.

This form carries the verdict in `ready` rather than in the exit code: a not-ready run prints its
JSON and exits 0, so a job piping it into `jq` keeps git-pair's answer separate from the pipeline's.
Usage errors and git failures still exit 2 and 3 here.

```json
{
  "changeset": "feat",
  "ready": false,
  "state": "APPROVED",
  "head": "941266b18686624cb624722b4e8c348bf03451a7",
  "archive": "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
  "archive_current": false,
  "reasons": [
    "archive does not point to the current source commit: refs/git-pair/changesets/feat/archive is at 1a2b3c4, HEAD is 941266b"
  ],
  "policy": "approve-only",
  "integrated": false
}
```

`git pair integration record --json` — what the recorder wrote, with full SHAs so a pipeline can
compare them against the revisions it built. `target` is empty when `--target` was not given, and
then no reachability check was made.

```json
{
  "changeset": "booking-transaction",
  "source": "a7f3c98d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
  "commit": "d91c21ed5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0c",
  "target": "origin/main",
  "integration_ref": "refs/git-pair/changesets/booking-transaction/integration",
  "recorded": true
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
  "next_action": "`git pair change feedback`; feedback is non-blocking, `git pair change archive` when integration is due"
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
git pair change archive             # once approved: put the archive where the work is
git pair check                      # assert integration-readiness; $? is the answer
```

Never prompt: `change init`, `change ready`, `change unready`, `change abandon`, `change feedback`,
`change wait`, `change archive`, `status`, `check`, `diff`, `review submit`, `review history`,
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

**As a CI gate.** `check` reads the repository — commits, the archive ref, the trees — so a plain
checkout of the branch is enough to run it:

```bash
git pair check || exit 1
```

In the human form the exit code is the verdict: 0 ready, 1 not ready, 2 usage, 3 git failed. A
not-ready run writes one bullet per failed condition to stdout, so the log explains the gate without
a second run. `--json` carries the same facts — `ready`, `reasons`, `policy` and full SHAs in `head`
and `archive` — but moves the verdict into `ready` and exits 0 either way, which is why a JSON gate
asks `jq` rather than `$?`:

```bash
git pair check --json | jq -e '.ready'
```

Two things a gate needs to know. `check` accepts `feedback` only with `--allow-feedback`, which is
stricter than `change archive` — a head that was approved, or given non-blocking feedback, may be
archived, and the repository decides at the gate whether feedback alone is enough to land. And the archive condition needs
`refs/git-pair/changesets/<id>/archive` to be present: git-pair never pushes it (§13), so a CI job
that was not given those refs reports the archive as missing rather than the review as absent.

**Recording the landing.** After the merge, the same job tells git-pair where the work ended up:

```bash
git pair integration record --source "$SOURCE_SHA" --commit "$TARGET_SHA" --target origin/main
```

`--source` is the commit the archive names — the head that was reviewed — not the merge commit, and
the changeset is discovered from it, so the pipeline needs no name a human chose. A squash, a rebase
and a cherry-pick are all recordable, and none of them leaves ancestry between the two SHAs: the
record is what connects them. Recording is create-only, so a re-run refuses loudly rather than
doubling the bookkeeping, and once the record exists the archive is frozen and `check` refuses the
changeset as already integrated. `--target` is optional; with it, git-pair verifies the landing commit
is reachable from the ref you name — the one assumption it will accept about where the work went.

**Fetching the durable refs.** `check` and `integration record` read
`refs/git-pair/changesets/<id>/archive` and `…/integration`, and those are not fetched by default: a
clone maps `refs/heads/*` into `refs/remotes/*` and nothing else. A job handed the branch and nothing
more gets a true statement about its own checkout — `this clone has no refs/git-pair/changesets/* refs
at all`, with the fetch printed — and a verdict it should not act on. Two fetches make a checkout
complete:

```bash
git clone --branch "$BRANCH" --single-branch "$REPO" work && cd "$work"
git fetch origin --no-tags main:refs/remotes/origin/main                    # the integration branch
git fetch origin '+refs/git-pair/changesets/*:refs/git-pair/changesets/*'   # the durable refs
git pair check
```

The default branch is not the optional one. The rule that decides which changeset a revision is working
on compares its tree against trunk's, so a checkout holding one branch refuses to invent a trunk
(exit 2) rather than read every directory as work in progress — and `--default-branch origin/main`
states it directly instead of fetching. A `--single-branch` clone records no `origin/HEAD`, which is
why the second line above names the branch.

Publishing is the other half, and git-pair does not do it: nothing in the tool runs `push`, so the
refs stay in the author's clone until the repository is configured to share them.

```bash
git config --add remote.origin.push '+refs/git-pair/changesets/*:refs/git-pair/changesets/*'
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
screen and the row you are not marking is one you can see. The two jumps into the box — `a` and `t` — do not
occur here either: they move a cursor along with the keys, and a cursor that moves under a diff you are
reading is the invisible action the focus exists to prevent. The frame says which column has the keys
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

`no changeset archive points at a81c123, so integration cannot be recorded automatically` (exit 1,
`integration record`) — three possibilities, and the message names all three because they look the same
from here: the durable refs were never fetched (`refs/git-pair/changesets/*` is not fetched by
default — `git fetch origin '+refs/git-pair/changesets/*:refs/git-pair/changesets/*'`), the changeset
was never archived, or `--source` is not the commit the archive names. That last one is the usual
mistake: `--source` is the archived head that was reviewed, not the merge commit, and the check that
fails is `--points-at`, so an exact match is required. An abbreviated SHA is fine — it is resolved
before discovery.

When that message goes on to say `and this repository holds no refs/git-pair/changesets/* refs at all`,
it is the first possibility and nothing else, and the message prints the fetch to run. A clone maps
`refs/heads/*` into `refs/remotes/*` and nothing else, so the durable refs are absent even from a clone
of a repository that published them properly. `git pair check` says the same thing rather than reporting
the review history as unanchored: with nothing fetched, "no archive" is a fact about the checkout, and
a verdict built from it is about the clone and not the work.

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

(exit 2) — the branch carries two unlanded changeset directories and nothing in the durable data
orders them: neither owns a review ref nearer than the other, and neither names the other as its
`base:`. That is what a sibling merged in looks like, and what a branch created off a sibling looks
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
git pair change archive --allow-surviving-review-additions
```

Only the most recent review counts, and only additions outside `changesets/<changeset>/`
block; surviving `ABOUT.md` and thread text is listed as non-blocking. A second hatch covers the
case the tree check cannot resolve on its own — content outside `changesets/<changeset>/` that
arrived after the approval and is not worth a second review, such as a README typo:

```bash
git pair change archive --allow-unreviewed-changes
```

The output names how many paths it archived over, and the archive still points at that `HEAD`.
Neither hatch overrides a `block` or a `change unready`.

```opening an editor needs a terminal```, ``` `git pair review open` needs a terminal ``` and
``` `git pair review reopen` needs a terminal ``` (exit 2) — both stdin and stdout must be
character devices. Under an agent, a pipe or cron, edit the
files directly and use `git pair diff`, `git pair status` and `git pair review submit`.

`changeset has no review submissions yet; run git pair diff for the full changeset` (exit 2) —
`--unreviewed` and `--since-review` need a review submission. Out of range:
`no review at index 9: this changeset has 1 review(s) (valid: 0..0 or -1..-1)`.

`"<path>" does not appear in main...HEAD; changed paths: ...` (exit 2) — the path is not in
the resolved span; the error lists what is.

`working tree must be clean ...` (exit 1) — `change ready`, `change unready` and `change archive` act
on committed state. `change init` commits its scaffolding, so a fresh changeset does not block `change
ready`; it does block it if you then edit `ABOUT.md` without committing. Use
`change init --no-commit` to fold the scaffolding into your first implementation commit
instead.

`no changeset for this branch: changesets/foo` (exit 2), or `HEAD is detached; check out a
branch first` (exit 2) — the directory is named after the branch by default, so a detached HEAD has
no name to derive one from. Renaming a branch needs nothing: resolution reads which changeset
directories the revision carries, not which branch you are standing on, and `--changeset <id>` reads
one from any branch while HEAD stays where it is.

`git pair check` printing `NOT READY:` while `git pair status` says `APPROVED` is usually one of two
things, and both bullets name which: the archive ref names the approved commit while `HEAD` has moved
past it (run `git pair change archive` — a changeset-only commit is not drift, so the approval
stands), or the newest review is `feedback`, which the default policy does not accept (the bullet
says so, and `--allow-feedback` is the switch). `check` is also the one command that ignores your
working tree: it asserts the commit, and uncommitted edits are not in `HEAD` to be reviewed.

A missing entry in `git pair review queue` is usually not a queue bug: membership is derived
state, and only a command moves it — `change ready`, `change unready`, or a review submission. A
code change after the ready marker leaves the changeset in the queue, naming the commits in its
reason; what that drift does stop is `change archive`, which refuses to move the archive over a head whose
reviewed content has moved. A changeset whose content has landed in its base is not listed, and
says nothing: the branch may already be gone, and the queue asks what a reviewer can act on. The
queue asks what a reviewer can act on, and it answers the same way from `main` as from the
changeset's own branch: it enumerates local branches and resolves each one's changeset, so
membership does not depend on where you happen to be standing. A branch it cannot resolve — two
changesets on one branch, say — is named in `skipped` rather than left out quietly.
A hand-written ready marker counts only if `Review-State: ready` and `Review-Changeset: <slug>`
sit in a real trailer block, separated from the subject by a blank line and from each other by
no blank line. `cannot archive <cs>: latest outcome is BLOCKED` (exit 1) is the refusal for a
head whose newest review does not permit integration. Two other refusals belong to the archive
itself: running `change archive` on a `HEAD` that is already archived is not a refusal — the ref is
already where it should be, so the second run moves nothing and says so — while a `HEAD` *behind*
the archive is, because moving there would drop the archived chain from the only ref holding it
reachable.
