# git-pair

`git-pair` is a local-first peer-review tool for human-plus-agent pairs. It puts a thin
review protocol on top of ordinary git commits, files, refs, editors and difftools rather
than replacing any of them: a changeset directory holds the change description and review
threads, lifecycle transitions are commits carrying `Review-*` trailers, review-relative
diff spans answer "what happened since my last review", and `refs/reviews/*` keeps the
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
- persist per-file review checkmarks beyond the current TUI session
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

`CHANGESET.yaml` is one line (`base: main`); `ABOUT.md` is a scaffold with `Summary`,
`What changed`, `Design decisions`, `Validation`, `Known limitations` and `Open questions`
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
  ref:     refs/reviews/booking-transaction -> 332887c
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
  ref:     refs/reviews/booking-transaction -> 0eaad3b
  next:    author: `git pair change complete` before squash/merge
```

The owner then completes the changeset. Completion is theirs rather than the reviewer's: an
approve is a judgement about the code, and deciding that the reviewed state is what gets taken
forward is the owner's call. It adds no commit — the archive ref names the approved head:

```bash
$ git pair change complete
Completed changeset booking-transaction

Review archive:
  refs/reviews/archive/booking-transaction/0eaad3b

Safe to squash/merge.
Review history stays reachable at refs/reviews/booking-transaction
```

Deleting the branch loses nothing:

```bash
$ git switch main && git branch -D booking-transaction
Deleted branch booking-transaction (was 9c04d06).

$ git rev-list --count refs/reviews/archive/booking-transaction/0eaad3b
9
```

## Concepts

**Changeset.** One branch, one changeset, one review stream. The directory name comes from
the branch name: `/` and every character outside `[A-Za-z0-9._-]` become `-`, runs collapse,
leading and trailing `-` are trimmed, case is kept, so `feature/booking-transaction` becomes
`changesets/feature-booking-transaction/`. `CHANGESET.yaml` holds one meaningful key,
`base`, which for a stack names another changeset's branch.

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
in `git pair review history`, and completion refuses until a reviewer approves again.

**Completion is a ref, not a marker.** `git pair change complete` commits nothing. It anchors the
chain and writes the immutable archive ref at `HEAD`, and the changeset is finished when that
archived history is merged into the deployment branch — ordinary git, which git-pair neither runs
nor derives. A completed changeset therefore still reports `APPROVED` (or `FEEDBACK`), with
`archive_ref` naming the archived commit for as long as `HEAD` is that commit. Ownership follows the
same split: the reviewer approves the code, the owner decides it is what gets taken forward.

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
`git pair change complete` compares the tree between the newest marker's parent and `HEAD` and
refuses when anything outside `changesets/<slug>/` differs, because the archive ref it writes is
what an agent checks before squash-merging and it has to name content somebody reviewed. A
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

**Review refs.** `change ready` writes `refs/reviews/<changeset>` at the ready marker, and every
submission moves it to the exact resulting `HEAD`, in the same operation that creates the commit,
keeping the whole implementation/review/fix chain reachable from garbage collection. The handoff is
where the history starts being worth keeping, so an offered-but-never-reviewed changeset is anchored
too. `git pair change complete` also
writes `refs/reviews/archive/<changeset>/<short-sha>` at the `HEAD` it completes, created only if
absent and never moved, so re-running complete cannot rewrite an archive. Neither kind is a
guarantee against `git push --delete`; they keep Git from pruning what git-pair still needs.

**Surviving review additions.** Review lines left untouched disappear from a `review..HEAD`
diff, so `change ready` and `change complete` re-derive them with
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
`change unready`, `change wait`, `change complete`, `review submit`, `review history` and
`review queue` change output for it; elsewhere it is accepted and ignored.

| Command | Flags | Notes |
| --- | --- | --- |
| `change init` | `--base <ref>`, `--set-base`, `--about <text>`, `--set-about`, `--no-commit` | creates directory, `CHANGESET.yaml`, `ABOUT.md`, then commits them; never overwrites existing content; `--about` also reads a pipe; default base is `main`, else `master`, else a usage error |
| `change ready` | `--allow-surviving-review-additions` | fully non-interactive; checks below |
| `change unready` | none | withdraws the changeset from the review queue; records `Review-State: working` only when it is in review, otherwise succeeds and records nothing |
| `change feedback` | `--stat`, `--name-only` | the diff of the most recent review submission (`review^..review`): threads, `ABOUT.md` edits and reviewer code edits together; exits 2 if there is no submission |
| `change wait` | `--fetch`, `--interval <dur>` (default `10s`), `--timeout <dur>` | blocks until the state leaves `READY` for `BLOCKED`/`FEEDBACK`/`APPROVED`; read-only; `--fetch` runs `git fetch` before each check so a review pushed from another clone is noticed |
| `review open` | `--unreviewed`, `--since-review[=N]`, `--base-review[=N]`, `--base-commit`, `--base-ref`, `--head-review[=N]`, `--head-commit`, `--head-ref` | TUI; needs a terminal; full changeset unless a span flag says otherwise; a `--head-*` flag opens a historical span, which is read-only |
| `review reopen` | none | TUI on `<last review>..current`, the work that has landed since you reviewed; needs a terminal; refuses if no review exists |
| `review about` | — | opens `ABOUT.md` in the editor, creating it if missing |
| `review thread [title...]` | — | slugifies the title, reopens an existing match, prompts for a title only with a terminal |
| `review submit` | one of `--block`/`--feedback`/`--approve`, `-m/--message <text>`, `--no-stage` | stages the whole tree by default, commits (empty commits allowed), then moves the review ref |
| `review history` | — | only review marker commits, indexed from `0` |
| `review queue` | — | every branch in this repo whose changeset is `READY`, longest wait first; read from the repository, not the checkout |
| `change complete` | `--allow-surviving-review-additions`, `--allow-unreviewed-changes` | archives the reviewed `HEAD` and reports squash-safety; commits nothing; never merges, pushes or squashes |
| `status` | — | derived state for the current branch's changeset |
| `diff [path...]` | `--unreviewed`, `--since-review[=N]`, `--base-review[=N]`, `--base-commit`, `--base-ref`, `--head-review[=N]`, `--head-commit`, `--head-ref`, `--stat`, `--tool` | paths are checked against the span first, so a typo is an error, not an empty diff |

`change ready` checks, in order: clean working tree, `ABOUT.md` exists, the repository has
commits, no blocking surviving additions. `change complete` checks: clean tree, newest
review at `HEAD` is `approve` or `feedback` and still describes what `HEAD` carries (the tree is
compared, ignoring `changesets/<cs>/`), no blocking surviving additions. The last two can be
acknowledged with `--allow-surviving-review-additions` and `--allow-unreviewed-changes`; a block
or a withdrawal cannot. `change unready` checks
only for a clean tree, since the marker it writes is empty. `change init` warns
without failing
if the base does not resolve, and commits only the changeset directory (`git commit --only`),
so work you had already staged for another commit stays on your index.

| Exit code | Meaning | Seen as |
| --- | --- | --- |
| 0 | success | — |
| 1 | a git-pair rule or the repository state refused the operation | surviving additions; `working tree must be clean`; `ABOUT.md is missing`; `cannot complete <cs>: latest outcome is BLOCKED`; `cannot resolve changeset base "vanished"`; `change wait` timing out, or refusing a changeset that is `WORKING` |
| 2 | usage error | unknown flag, unknown command, or unknown subcommand of `change`/`review`; `no changeset for this branch`; detached HEAD; `--block, --feedback and --approve are mutually exclusive`; `changeset has no review submissions yet`; `changeset <cs> has no review submission yet` (`change feedback`); `--interval expects a duration` (`change wait`); `--fetch` with no remote configured; `"<path>" does not appear in <span>`; editor/TUI commands without a terminal |
| 3 | the repository or git itself failed | `not a git repository`; a git subprocess exiting non-zero for a reason other than an unresolvable revision |

The split between 1 and 2 is deliberate and load-bearing for agents: exit 1 means the
invocation was correct and the repository said no, so fix the state and retry; exit 2 means
the invocation itself was wrong, so retrying unchanged will fail again.

## JSON contracts

Output is indented two spaces, and empty lists may serialise as `null` rather than `[]`
(`review queue`, `review submit`'s `files`), so test for both.

`git pair status --json`, waiting for the first review. Once a review exists `latest_review`
becomes `{"index": 0, "outcome": "block", "commit": "332887c"}`; `unrecognised_markers` (a
list of `<sha> <subject>`) appears only when non-empty. `review_ref` and `review_commit` describe
the movable anchor and stay `""` until something writes it — `change ready`, or a review
submission — because the ref's name is derivable from the changeset and its existence is the only
fact worth reporting. Once the changeset is completed,
`archive_ref` names the immutable archive ref — the `refs/reviews/archive/<cs>/*` that points
at `HEAD` exactly. It goes back to `""` as soon as other work lands, so an archive of an
ancestor never looks like a finished changeset.

```json
{
  "changeset": "booking-transaction",
  "branch": "booking-transaction",
  "base": "main",
  "state": "READY",
  "head": "8065dae",
  "head_full": "8065dae53c0475596bfc174927075895d9fb8b76",
  "latest_review": null,
  "review_ref": "refs/reviews/booking-transaction",
  "review_commit": "8065dae",
  "archive_ref": "",
  "uncommitted": false,
  "reviews": 0,
  "reason": "marked ready by 8065dae",
  "span": "main...current",
  "next_action": "waiting for a reviewer: `git pair review open` (author: `git pair change wait` to block on it)"
}
```

`git pair review queue --json` — `head` and `ready_commit` are full SHAs. `skipped` names changesets
the queue cannot explain (`null` when empty): a branch whose metadata cannot be read, or a changeset
directory whose branch is gone, its anchor survives, and its content is not in its base. A changeset
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
      "review_ref": "refs/reviews/booking-transaction"
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
  "next_action": "author: `git pair change complete` before squash/merge",
  "outcome": "approve",
  "previous_review": "",
  "review_ref": "refs/reviews/feat",
  "short": "941266b"
}
```

`git pair change complete --json` — `head` is the archived commit and `state` is the derived
state, which completion does not change; `archive_created` is false when this head was already
archived, which is a success rather than a refusal. `acknowledged_unreviewed_paths` counts what
`--allow-unreviewed-changes` covered, so a completion over drift reports `WORKING` with a
non-zero count beside it rather than looking like an ordinary approval.

```json
{
  "acknowledged_survivors": 0,
  "acknowledged_unreviewed_paths": 0,
  "archive_created": true,
  "archive_ref": "refs/reviews/archive/feat/941266b",
  "base": "main",
  "changeset": "feat",
  "head": "941266b18686624cb624722b4e8c348bf03451a7",
  "review_ref": "refs/reviews/feat",
  "short": "941266b",
  "squash_safe": true,
  "state": "APPROVED",
  "surviving_review_artifacts": 0
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
  "next_action": "`git pair change feedback`; feedback is non-blocking, `git pair change complete` when integration is due"
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
git pair change complete            # once approved: archive the reviewed head
```

Never prompt: `change init`, `change ready`, `change unready`, `change feedback`, `change wait`,
`change complete`, `status`, `diff`, `review submit`, `review history`, `review queue`. They report
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
start against your **working tree** instead of two blobs. That is what makes the right-hand
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

`git pair review open` is an orchestration screen, not an editor. The changed files and their
marks come first, the reviewed counter under them, then the changeset documents:

```text
tuishow
base: main  span: unreviewed

○ src/a.ts

0 / 1 reviewed

ABOUT.md
▾ Threads
    does-the-lock-cover-the-map.md
    + new thread…
────────────────────────────────────────
j/k move  tab section  enter open  d diff  p preview  e edit  space reviewed  a about  t new thread
T hide threads  v spans  V picker  s submit  q quit
```

The screen is one navigable list in two blocks: the files the diff touched, and below the
counter the documents the review is made of. `j` runs off the bottom of the files into the
section below — reading the code and
then reading what the changeset says about it is one motion, not two modes — and `Tab` toggles
between the halves. `Enter` does whatever the row under the cursor is for: the difftool for a
file, collapse or expand for the `Threads` heading, the title prompt for `+ new thread…`, and
for a changeset document the same decision `d` makes. `d` means diff on any row. A file row
always has a comparison; a document is worth diffing only when the span changed it *and* it
already existed where the span starts — on a second round of review, that is the two lines the
author rewrote after your last submission, which is what you came back for. A document the
changeset invented has nothing on the left side of that comparison, so it opens in the editor,
and so does one the span left alone; the editor's exit carries a note saying which of the two
happened, because anything written before the handoff is under the editor by the time you look
again. `e` opens the editor regardless of the span, so it is always one key away. `Space`
toggles reviewed on a
file row — which stays under the cursor, since marking is not navigation — and refuses the
rows below the counter. `a` opens
`ABOUT.md` in the editor whatever the span did; `t` prompts for a new thread from anywhere;
`T` collapses the thread list; `v`
steps to the next span this session has been in — the span it opened on, the full and
unreviewed presets, and any span chosen with `V` — every stop in order, wrapping, so nothing on
the ring is unreachable. It means *next* whatever else just happened, a refusal included: one press
of `v` out of a read-only span is not something the ring can promise, since what sits next is
whatever the session visited next, and getting to a span you can review in one keystroke is `V`,
whose head column always offers `Current`. A stop whose commit, tag or ref has since gone is stepped
over and named on the status line, reason included — the span stays on the ring, so a tag that comes
back is a stop again; `s` opens a submit prompt taking `b`, `f` or `a`; `q` quits.
A thread created from the list is written, opened in the editor, and left selected, so the
reviewer can fill it in and come straight back to it. The thread heading counts what it hides —
`▸ Threads (3)` collapsed, plain `▾ Threads` once the threads are on screen — and a rule
separates the whole list from the shortcut bar, so the bar reads as chrome rather than as more
rows.

On a wide terminal the list shares the screen with a preview. From 100 columns and 16 rows the
session puts a column beside the list showing the diff of whatever the cursor is on — the span's
diff, the same one `git pair diff` and the reviewed counter describe — with git's own `+N −M` in its
header. The list takes the width its own paths need, up to 48 columns, and the diff gets the rest:
a changeset of short names is not made to share the screen with whitespace, and one long vendored
path cannot take the diff's columns. `ctrl-f` and `ctrl-b` page through a diff too long to fit, and the note along the bottom says how
much of it is left; `ctrl-d` still quits, which is why paging is not `ctrl-d`. Each line carries the
number git gave it in its hunk header, and a line too wide for the column is broken rather than cut,
with its colour carried across the break. Tabs are shown as the spaces they advance to, because a tab
the width maths scores as zero is a row the terminal wraps for you. `p` switches the pane off.

Below those dimensions there is no room for two columns, and the person who pressed `p` wanted the
diff — so `p` gives the diff the whole screen. Same git bytes, same numbers, same wrapping, full
width, with the file and the span it is measured against on the line above it (the span is named in
the list otherwise, and the list is gone). It scrolls with `j`/`k`, `ctrl-d`/`ctrl-u` for half a page,
`ctrl-f`/`ctrl-b` for a page, `gg` for the top and `G` for the bottom, and gives the list back on `q`,
`Esc`, `Enter` — or `p`, which opened it. Keys mean what this screen's shortcut bar says they mean
while it is up, which is why `q` closes rather than quits (press it twice to leave) and `ctrl-d`
pages rather than quits; nothing else reaches through, because a reviewer who cannot see the list must
not be able to mark a file in it. Closing keeps the place: `p`, `q`, `p` returns to the same lines.
Reading a historical span this way works the same — reading is what a read-only span is for. Under 40
columns or 12 rows even this is unreadable, and the key says which way the terminal is short, naming
the smaller of the two asks because that is the one worth growing to.

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
`HISTORICAL · READ ONLY`, files lose their reviewed gutter, `+ new thread…` is gone, and the
shortcut bar lists only what still works. Pressing a key that does not work says why, names the head
the span is stuck on, and points at `V`, which is how you choose a span you can review — `v` walks the
session's spans and cannot promise where it lands. The difftool
over a historical span compares its two pinned commits rather than your working tree, so it cannot
show you work the span does not contain. `git pair diff` takes the same flags and just prints; the
read-only half is about the screen, where the mistakes would be made.

`V` opens the span picker, where both ends are chosen before either takes effect: `Tab` moves between
the BASE and HEAD columns, `j`/`k` move, `Space` sets the end under the cursor, and `Enter` applies
the pair — with the lines under the columns saying what that pair resolves to, in the same words the
header and the status line use (`main...current`, not a second spelling of the same span), and whether
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
rather than the frame: the shortcut bar and the status wrap into rows first, because a frame taller than
the terminal repaints by scrolling and what scrolls off the bottom is the bar that says how to leave.
`Ctrl-C` leaves from any of these screens — the columns, a drill, the submit prompt.

An endpoint named as a ref is pinned when you choose it, and the pin is what the screen keeps
comparing. So when `probe` moves in another window — a fetch, someone else's push — the header still
reads `probe@bb0f343` and a row above the shortcut bar says:

```text
⚠ probe moved bb0f343 → 43915ed  [r] refresh
```

Nothing follows the branch by itself. `r` re-pins the endpoint to where the ref points now, recomputes
the span and reports what that cost: marks are keyed on each file's diff within the span, so the ones
whose diff changed stop applying, and the count in the report says how many. Ignoring the banner is a
legitimate answer too — the span does not move until asked, and the warning is a row of the screen
rather than the line the last keystroke writes to, so marking a file does not erase it. A ref that has
gone away is not drift: there is nothing to refresh to, and the pin still resolves to the commit it
was chosen for.

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
is remembered rather than resurrected. Nothing is committed or shared — `git status` cannot see the
directory and `git add` cannot stage it — and no command reports marks, so derived state is
unaffected. Deleting that directory forgets the marks; the newest 12 commits per changeset are
kept.

## Troubleshooting

`cannot resolve changeset base "main": unknown revision: main` (exit 1) — the `base` in
`CHANGESET.yaml` is not a ref in this repository (renamed trunk, fresh clone, merged stack).
git-pair never guesses a base: edit the file, or `git pair change init --base <ref> --set-base`.
From `change init` with no `--base`: `cannot infer a base: no main or master branch exists;
pass --base <ref>` (exit 2).

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
git pair change complete --allow-surviving-review-additions
```

Only the most recent review counts, and only additions outside `changesets/<changeset>/`
block; surviving `ABOUT.md` and thread text is listed as non-blocking. A second hatch covers the
case the tree check cannot resolve on its own — content outside `changesets/<changeset>/` that
arrived after the approval and is not worth a second review, such as a README typo:

```bash
git pair change complete --allow-unreviewed-changes
```

The output names how many paths it completed over, and the archive still points at that `HEAD`.
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

`working tree must be clean ...` (exit 1) — `change ready`, `change unready` and `change complete` act
on committed state. `change init` commits its scaffolding, so a fresh changeset does not block `change
ready`; it does block it if you then edit `ABOUT.md` without committing. Use
`change init --no-commit` to fold the scaffolding into your first implementation commit
instead.

`no changeset for this branch: changesets/foo` (exit 2), or `HEAD is detached; check out a
branch first` (exit 2) — the directory is named after the branch, so renaming a branch orphans
its changeset and a detached HEAD has no name.

A missing entry in `git pair review queue` is usually not a queue bug: membership is derived
state, and only a command moves it — `change ready`, `change unready`, or a review submission. A
code change after the ready marker leaves the changeset in the queue, naming the commits in its
reason; what that drift does stop is `change complete`, which refuses to archive a head whose
reviewed content has moved. A changeset whose content has landed in its base is not listed, and
says nothing: the branch may already be gone, and the queue asks what a reviewer can act on. The
tree is not consulted — the queue reads branches and their commits, so it answers the same way from
`main` as from the changeset's own branch.
A hand-written ready marker counts only if `Review-State: ready` and `Review-Changeset: <slug>`
sit in a real trailer block, separated from the subject by a blank line and from each other by
no blank line. `cannot complete <cs>: latest outcome is BLOCKED` (exit 1) is the refusal for a
head whose newest review does not permit integration; completing an already-archived head is not
a refusal at all, because archive refs are never moved, so the second run changes nothing and
says so.
