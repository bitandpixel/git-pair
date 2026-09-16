# gitpr

`gitpr` is a local-first peer-review tool for human-plus-agent pairs. It puts a thin
review protocol on top of ordinary git commits, files, refs, editors and difftools rather
than replacing any of them: a changeset directory holds the change description and review
threads, lifecycle transitions are commits carrying `GitPR-*` trailers, review-relative
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
mise exec -- go build -o ~/bin/gitpr ./cmd/gitpr
mise exec -- go install ./cmd/gitpr          # into $(go env GOPATH)/bin
```

Without mise, any Go 1.27 toolchain works. `gitpr --version` prints `gitpr version 0.1.0`.

## Quickstart

The PRD §29 loop, captured from a real session in a scratch repo; the only hand-authored
content is the `ABOUT.md` text and the reviewer's two inline comments.

```bash
$ git switch -c booking-transaction

$ gitpr change init --base main
created changesets/booking-transaction/
created changesets/booking-transaction/CHANGESET.yaml
created changesets/booking-transaction/ABOUT.md
committed 9f1c2de

Next: fill in changesets/booking-transaction/ABOUT.md, commit it with your implementation, then run `gitpr change ready`.
```

`CHANGESET.yaml` is one line (`base: main`); `ABOUT.md` is a scaffold with `Summary`,
`What changed`, `Design decisions`, `Validation`, `Known limitations` and `Open questions`
headings. Fill it in, commit it with your implementation, then:

```bash
$ gitpr change ready
Ready: booking-transaction
  head:  8065dae
  base:  main
  queue: `gitpr review queue` now lists this changeset

$ gitpr review queue
READY FOR REVIEW

booking-transaction
  base: main
  ready: 0s ago
  head: 8065dae
```

The reviewer runs `gitpr review open` for the TUI, or works from the CLI. Here they edited
`src/service.ts` directly, added a thread, and blocked:

```bash
$ gitpr review thread "concurrency tests"     # then edits the file in $EDITOR
Created thread changesets/booking-transaction/concurrency-tests.md

$ gitpr review submit --block
Review submitted: booking-transaction
  outcome: block
  commit:  332887c
  files:   2 changed
           changesets/booking-transaction/concurrency-tests.md
           src/service.ts
  ref:     refs/reviews/booking-transaction -> 332887c
  next:    author: `gitpr change feedback`, address it, then `gitpr change ready`
```

The review is an ordinary commit: `review: block booking-transaction` followed by a blank
line and the `GitPR-Outcome: block` / `GitPR-Changeset: booking-transaction` trailers.

The author waits for that submission and reads it. `change wait` blocks until a review makes
the changeset actionable and names the commit; `change feedback` shows the submission itself —
the thread, the `ABOUT.md` edits, and any code the reviewer touched:

```bash
$ gitpr change wait --json
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
  "next_action": "`gitpr change feedback`, address it, then `gitpr change ready`"
}

$ gitpr change feedback --stat
gitpr change feedback: review 332887c (block) on booking-transaction
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
$ gitpr diff --unreviewed -- src/service.ts
gitpr diff: 332887c..HEAD (after review 0)
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
$ gitpr change ready

Cannot mark changeset booking-transaction ready.

2 additions from review 332887c still survive unchanged:

src/service.ts:10
    // Please use a transaction here

src/service.ts:11
    // What happens if these execute concurrently?


Review these additions before continuing.

To intentionally preserve them:
  gitpr change ready --allow-surviving-review-additions

Also still present from review 332887c (changeset artifacts, not blocking):
  changesets/booking-transaction/concurrency-tests.md:1  # concurrency tests
  changesets/booking-transaction/concurrency-tests.md:4  ## Response
  changesets/booking-transaction/concurrency-tests.md:6  Will do.
gitpr: cannot mark changeset booking-transaction ready: 2 review addition(s) from 332887c still survive unchanged
```

After resolving the remaining comment `gitpr change ready` succeeds and the changeset is
back in the queue. The reviewer re-reviews with `gitpr review reopen`, which starts on what
changed since their submission, approves, and closes:

```bash
$ gitpr review submit --approve
Review submitted: booking-transaction
  outcome: approve
  commit:  0eaad3b
  files:   none (recorded as an empty review commit)
  ref:     refs/reviews/booking-transaction -> 0eaad3b
  next:    `gitpr review close` before squash/merge

$ gitpr review close
Closed changeset booking-transaction

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
`gitpr review thread "concurrency tests"` creates or reopens
`changesets/<changeset>/concurrency-tests.md`. Replies are appended sections and git history
supplies authorship and order. Both are plain Markdown with no schema.

**Markers are commits.** `ready`, a review outcome, and `close` are commits, not state
variables:

| Marker | Subject | Trailers |
| --- | --- | --- |
| ready | `gitpr: ready <slug>` | `GitPR-State: ready`, `GitPR-Changeset: <slug>` |
| review | `review: <outcome> <slug>` | `GitPR-Outcome: <outcome>`, `GitPR-Changeset: <slug>` |
| close | `gitpr: close <slug>` | `GitPR-State: closed`, `GitPR-Changeset: <slug>` |

A commit counts as a marker only when its trailer block parses and `GitPR-Changeset` matches
the changeset being inspected; anything else is an ordinary commit.

**A review is corrected by submitting again.** There is no `review undo`. The newest
submission decides the state, earlier ones stay in `gitpr review history`, and the summary of
a later submission names what it supersedes. Undo by rewriting history is not on the table —
gitpr runs no `reset`, `rebase` or `push`, and a review commit may already be shared; a
withdrawal *marker* would work but needs a trailer older gitpr builds cannot read, which
would leave two versions of the tool disagreeing about one branch. Moving the review ref back
is not an undo either: state comes from commit trailers, so the submission would still count.

**Derived state.** State comes from walking `base..HEAD`, reading the newest marker, then
asking whether what it approved is still there: if anything outside `changesets/<slug>/`
differs between the marker's parent and `HEAD`, the marker is stale and the state is
`WORKING`, so committing code after an approval silently invalidates it. A commit that only
touches `ABOUT.md` or a thread does not — PRD §421 invalidates a marker on a later
*implementation* commit, and editing the description is not one. Comparing trees rather than
counting commits is also what keeps merges and rebases from reporting a change that never
happened. States:
`WORKING`, `READY`, `BLOCKED`, `FEEDBACK`, `APPROVED`, `CLOSED`. `gitpr status` prints the
state plus a one-line `Reason`. There is no state file. (The TUI caches the reviewer's
per-file marks under the git directory; those are reading progress, not state, and no
command reports them.)

**Review spans.** `gitpr diff` resolves a span and hands it to git:

| Option | Range | Question |
| --- | --- | --- |
| (default) | `merge-base(base, HEAD)..HEAD` | what is in this changeset |
| `--unreviewed` | `<latest review>..HEAD` | what happened since I reviewed |
| `--since-review=N` | `<Nth review>..HEAD` | what happened since review N |
| `review reopen`, when nothing landed since | `merge-base(base, review)..<review>^` | what that submission was reviewing |

`--since-review` with no value means `-1`; indexes are chronological (`0` first, `-1`
latest) and match `gitpr review history`.

Those spans answer the reviewer's questions. The author's question — *what did the reviewer
just tell me?* — is `gitpr change feedback`, which diffs the submission itself
(`review^..review`) and so shows the threads, the `ABOUT.md` edits and any code the reviewer
edited in one place. Immediately after a submission the two disagree completely:
`--unreviewed` is empty, because the submission *is* the newest commit.

Waiting for that submission is a command rather than a notification. `gitpr change wait`
re-derives the state on an interval and exits when it leaves `READY`; `--fetch` runs
`git fetch` before each check, so a review pushed from another clone arrives the way every
other commit does — through the repository, with no forge integration and no daemon.

Coming back to a changeset you already reviewed is common enough to have its own command:
`gitpr review reopen` opens the TUI on the `<latest review>..HEAD` span, the same span as
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
`gitpr diff: 332887c..HEAD (after review 0)`.

**Review refs.** Every submission moves `refs/reviews/<changeset>` to the exact resulting
`HEAD`, in the same operation that creates the commit, keeping the whole
implementation/review/fix chain reachable from garbage collection. `gitpr review close` also
writes `refs/reviews/archive/<changeset>/<short-sha>` at the pre-close `HEAD`, created only if
absent and never moved, so re-running close cannot rewrite an archive.

**Surviving review additions.** Review lines left untouched disappear from a `review..HEAD`
diff, so `change ready` and `review close` re-derive them with
`git show -U0 --no-renames <review>` and test each line for exact membership in the `HEAD` blob,
reporting `path:line` at `HEAD`. Blank additions are ignored, binaries are skipped via
`--numstat`, repeated identical lines collapse to one entry with a count, and only the most
recent review submission is considered. Additions outside `changesets/<changeset>/` block;
additions inside it are reported as "changeset artifacts, not blocking": a thread answered by
appending still contains every line the reviewer wrote, and `ABOUT.md` edits are normally kept,
so blocking on those would fail almost every real changeset and push users toward reflexive use
of the override. See `docs/plans/active/gitpr-mvp/research/git-plumbing-findings.md`.

## Command reference

Every command accepts the persistent `--json` flag, but only `status`, `change ready`,
`change wait`, `review submit`, `review history`, `review queue` and `review close` change
output for it; elsewhere it is accepted and ignored.

| Command | Flags | Notes |
| --- | --- | --- |
| `change init` | `--base <ref>`, `--set-base`, `--about <text>`, `--set-about`, `--no-commit` | creates directory, `CHANGESET.yaml`, `ABOUT.md`, then commits them; never overwrites existing content; `--about` also reads a pipe; default base is `main`, else `master`, else a usage error |
| `change ready` | `--allow-surviving-review-additions` | fully non-interactive; checks below |
| `change feedback` | `--stat`, `--name-only` | the diff of the most recent review submission (`review^..review`): threads, `ABOUT.md` edits and reviewer code edits together; exits 2 if there is no submission |
| `change wait` | `--fetch`, `--interval <dur>` (default `10s`), `--timeout <dur>` | blocks until the state leaves `READY` for `BLOCKED`/`FEEDBACK`/`APPROVED`/`CLOSED`; read-only; `--fetch` runs `git fetch` before each check so a review pushed from another clone is noticed |
| `review open` | `--unreviewed`, `--since-review[=N]` | TUI; needs a terminal; full changeset unless a span flag says otherwise |
| `review reopen` | none | TUI on `<latest review>..HEAD`, or on the span that review covered when nothing landed since; needs a terminal; refuses if no review exists |
| `review about` | — | opens `ABOUT.md` in the editor, creating it if missing |
| `review thread [title...]` | — | slugifies the title, reopens an existing match, prompts for a title only with a terminal |
| `review submit` | one of `--block`/`--feedback`/`--approve`, `-m/--message <text>`, `--no-stage` | stages the whole tree by default, commits (empty commits allowed), then moves the review ref |
| `review history` | — | only GitPR review submissions, indexed from `0` |
| `review queue` | — | every changeset in this repo whose derived state is `READY`, longest wait first |
| `review close` | `--allow-surviving-review-additions` | archives and closes; never merges, pushes or squashes |
| `status` | — | derived state for the current branch's changeset |
| `diff [path...]` | `--unreviewed`, `--since-review[=N]`, `--stat`, `--tool` | paths are checked against the span first, so a typo is an error, not an empty diff |

`change ready` checks, in order: clean working tree, `ABOUT.md` exists, the repository has
commits, no blocking surviving additions. `review close` checks: clean tree, latest outcome
`approve` or `feedback`, no blocking surviving additions. `change init` warns without failing
if the base does not resolve, and commits only the changeset directory (`git commit --only`),
so work you had already staged for another commit stays on your index.

| Exit code | Meaning | Seen as |
| --- | --- | --- |
| 0 | success | — |
| 1 | a gitpr rule or the repository state refused the operation | surviving additions; `working tree must be clean`; `ABOUT.md is missing`; `cannot close <cs>: latest outcome is BLOCKED`; `changeset <cs> is already closed`; `cannot resolve changeset base "vanished"`; submitting to a closed changeset; `change wait` timing out, or refusing a changeset that is `WORKING` |
| 2 | usage error | unknown flag, unknown command, or unknown subcommand of `change`/`review`; `no changeset for this branch`; detached HEAD; `--block, --feedback and --approve are mutually exclusive`; `changeset has no review submissions yet`; `changeset <cs> has no review submission yet` (`change feedback`); `--interval expects a duration` (`change wait`); `--fetch` with no remote configured; `"<path>" does not appear in <span>`; editor/TUI commands without a terminal |
| 3 | the repository or git itself failed | `not a git repository`; a git subprocess exiting non-zero for a reason other than an unresolvable revision |

The split between 1 and 2 is deliberate and load-bearing for agents: exit 1 means the
invocation was correct and the repository said no, so fix the state and retry; exit 2 means
the invocation itself was wrong, so retrying unchanged will fail again.

## JSON contracts

Output is indented two spaces, and empty lists may serialise as `null` rather than `[]`
(`review queue`, `review submit`'s `files`), so test for both.

`gitpr status --json`, waiting for the first review. Once a review exists `latest_review`
becomes `{"index": 0, "outcome": "block", "commit": "332887c"}`; `unrecognised_markers` (a
list of `<sha> <subject>`) appears only when non-empty. Once the changeset is closed,
`archive_ref` names the immutable archive ref — the newest `refs/reviews/archive/<cs>/*`
reachable from `HEAD`, which is the approved commit rather than the close marker itself.

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
  "review_commit": "",
  "archive_ref": "",
  "uncommitted": false,
  "reviews": 0,
  "reason": "marked ready by 8065dae",
  "span": "main...HEAD",
  "next_action": "waiting for a reviewer: `gitpr review open` (author: `gitpr change wait` to block on it)"
}
```

`gitpr review queue --json` — `head` and `ready_commit` are full SHAs, and `skipped` names
changeset directories the queue could not classify (`null` when empty).

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

`gitpr review history --json`

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

`gitpr review submit --approve --json`, on a clean tree and the first review
(`previous_review` is the full SHA of the submission this one supersedes, empty when there
is none)

```json
{
  "changeset": "feat",
  "commit": "941266b18686624cb624722b4e8c348bf03451a7",
  "empty": true,
  "files": null,
  "next_action": "`gitpr review close` before squash/merge",
  "outcome": "approve",
  "previous_review": "",
  "review_ref": "refs/reviews/feat",
  "short": "941266b"
}
```

`gitpr review close --json`

```json
{
  "acknowledged": false,
  "archive_created": true,
  "archive_ref": "refs/reviews/archive/feat/941266b",
  "changeset": "feat",
  "commit": "dc590e579a05040189261a1e451a3b744568d074",
  "review_ref": "refs/reviews/feat",
  "squash_safe": true,
  "state": "CLOSED",
  "surviving_additions": 0
}
```

`gitpr change ready --json` returns the `status` fields plus `ready_commit` (full SHA),
`review_queue_visible`, `acknowledged_survivors` and `surviving_review_artifacts`.

`gitpr change wait --json` — states are spelled as `status` spells them. `ref` is where the
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
  "next_action": "`gitpr change feedback`; feedback is non-blocking, `gitpr review close` when integration is due"
}
```

## Agent contract

The non-interactive loop from PRD §22:

```bash
gitpr change init --base main --about "$ABOUT"   # once, on a named branch
gitpr status --json              # read state and next_action without blocking
gitpr diff                       # see the whole changeset
# implement and commit with ordinary git
gitpr change ready               # hand off to the reviewer
gitpr change wait --fetch --json # block until a reviewer acts; exits 1 on --timeout
gitpr change feedback            # read that submission: threads, ABOUT.md, code edits
# address feedback in code, ABOUT.md and threads; commit normally
gitpr review history --json      # enumerate review commits
gitpr change ready               # again
```

Never prompt: `change init`, `change ready`, `change feedback`, `change wait`, `status`,
`diff`, `review submit`, `review history`, `review queue`, `review close`. They report and
exit instead of asking, even with a terminal attached.

Refuse with exit 2 when stdin or stdout is a pipe or a regular file, because launching an
editor or the TUI against one would hang: `review open`, `review reopen`, `review about`,
and `review thread`
(which creates the thread file and then refuses to open it). Read and write those files
directly instead; they are ordinary files in the working tree. `/dev/null` counts as a
character device, so redirecting to `/dev/null` does not produce this refusal — it makes
`review open` and `review reopen` fail with `could not open a new TTY` (exit 1) and
`review about` launch the
real editor.

An agent must not approve its own work. `gitpr review submit --approve` is a reviewer action
and nothing in gitpr checks who ran it — identity and permissions are out of scope, so keeping
approval on the human side of the pair is a convention you enforce. When an agent records
progress it uses `gitpr change ready`.

## Configuration

Editors resolve as `$VISUAL`, then `$EDITOR`, then `vi` — the same precedence git uses, so a
machine with `VISUAL=vim` set ignores an `EDITOR` override. Automated harnesses must clear
both, and every editor probe should be wrapped in `timeout`, because an editor that takes the
terminal and is never driven will block forever.

The value is expanded by `/bin/sh` with word splitting, exactly as git expands it, so
`EDITOR="code --wait"` works: `code` is the program and `--wait` its flag. A program path
containing a literal space needs quoting inside the value
(`EDITOR='"/my editor.sh" --wait'`), again as it does for git. The editor runs with the
repository root as its working directory and receives the absolute path of the file.

Diff viewing goes through git, so git configuration decides what you see, e.g.
`git config diff.tool vimdiff`. `gitpr diff --tool` and the TUI's `Enter` key both run
`git difftool --no-prompt <from> -- <paths>` — one revision, so the tool compares the span's
start against your **working tree** instead of two blobs. That is what makes the right-hand
buffer the real file: edits persist, and changes you made with `e` show up when you open the
tool again. `diff.tool`, `difftool.<tool>.cmd` and `difftool.prompt` behave as elsewhere.
The printed and `--stat` diffs stay on the committed span, because they describe review state
while the tool is for working on it. Plain `gitpr diff` runs `git diff` with
`core.quotePath=false` and inherits your pager and colour settings; the span label goes to
stderr so stdout stays pipeable.

`gitpr review open` is an orchestration screen, not an editor — header, changed files with
per-file marks, progress count, and the changeset documents:

```text
tuishow
base: main  span: unreviewed
○ src/a.ts
0 / 1 reviewed
ABOUT.md
Threads (1)
j/k move  enter difftool  e edit  space reviewed  a about  t thread  T browse  v span  s submit  q quit
```

`Enter` opens the selected file in the difftool, `e` in the editor, `Space` toggles reviewed on
the row under the cursor — which stays there, since marking is not navigation — `a` opens
`ABOUT.md`, `t` prompts for a new thread, `T` browses threads, `v` toggles the
full/unreviewed span, `s` opens a submit prompt taking `b`, `f` or `a`, `q` quits. Submitting
commits the review, moves the ref and **ends the session**, printing one line about what it
did; `Esc` from the prompt returns to the list. The terminal
is released while an external program runs and the repository is re-scanned afterwards, so a
reviewed mark survives only while that file's diff within the span is unchanged.

Marks also survive quitting: they are written under the repository's git directory at
`$(git rev-parse --absolute-git-dir)/gitpr/marks/<changeset>/<commit>.json`, keyed on the commit
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
gitpr never guesses a base: edit the file, or `gitpr change init --base <ref> --set-base`.
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
gitpr change ready --allow-surviving-review-additions
gitpr review close --allow-surviving-review-additions
```

Only the most recent review counts, and only additions outside `changesets/<changeset>/`
block; surviving `ABOUT.md` and thread text is listed as non-blocking.

```opening an editor needs a terminal```, ``` `gitpr review open` needs a terminal ``` and
``` `gitpr review reopen` needs a terminal ``` (exit 2) — both stdin and stdout must be
character devices. Under an agent, a pipe or cron, edit the
files directly and use `gitpr diff`, `gitpr status` and `gitpr review submit`.

`changeset has no review submissions yet; run gitpr diff for the full changeset` (exit 2) —
`--unreviewed` and `--since-review` need a review submission. Out of range:
`no review at index 9: this changeset has 1 review(s) (valid: 0..0 or -1..-1)`.

`"<path>" does not appear in main...HEAD; changed paths: ...` (exit 2) — the path is not in
the resolved span; the error lists what is.

`working tree must be clean ...` (exit 1) — `change ready` and `review close` act on committed
state. `change init` commits its scaffolding, so a fresh changeset does not block `change
ready`; it does block it if you then edit `ABOUT.md` without committing. Use
`change init --no-commit` to fold the scaffolding into your first implementation commit
instead.

`no changeset for this branch: changesets/foo` (exit 2), or `HEAD is detached; check out a
branch first` (exit 2) — the directory is named after the branch, so renaming a branch orphans
its changeset and a detached HEAD has no name.

A missing entry in `gitpr review queue` is usually not a queue bug: membership is derived
state, and a code change after the ready marker returns the changeset to `WORKING` (a commit
touching only `ABOUT.md` or a thread leaves it `READY`).
A hand-written ready marker counts only if `GitPR-State: ready` and `GitPR-Changeset: <slug>`
sit in a real trailer block, separated from the subject by a blank line and from each other by
no blank line. `changeset <cs> is already closed` and `changeset <cs> is closed` (both exit 1)
are the refusals for re-closing and for submitting after closing; archival stays
idempotent because the archive ref is never moved.
