# git-pair

`git-pair` is a local-first peer-review tool for human-plus-agent pairs. It puts a thin
review protocol on top of ordinary git commits, files, editors and difftools rather
than replacing any of them: a changeset directory holds the change description and review
threads, lifecycle transitions are commits carrying `Review-*` trailers, review-relative
diff spans answer "what happened since my last review", and a landing is read from the
destination branch's own tree — the branch carrying `changesets/<id>/` is what makes a changeset
landed, so there is no record to write, nothing to publish, and no ref to fetch. All review state lives
inside the repository, so no code path talks to a forge.

## What it is not

The MVP deliberately does not:

- implement a source-code editor, a full diff renderer, or anything that competes with
  Vim/Neovim, `git difftool`, git, or GitHub/GitLab
- create or manage pull requests, merge branches, squash branches, or push
  (`git pair change integrate` asks for a merge, in a commit, for somebody else to perform — it merges
  nothing and pushes nothing)
- write any ref of its own, at any point in a lifecycle. `git pair change tidy` is the only command that
  moves anything, and it moves a directory with `git mv`
- run coding agents or CI/CD
- keep inline-comment databases or GitHub-style comment anchoring
- treat per-file review checkmarks as review state — they persist locally under the git directory
  so a review can be resumed, and no command reports them
- model multi-reviewer permissions, multi-author semantics, or complex stacked-branch graphs;
  a stack is a `base:` naming the branch it sits on beside a `base-changeset:` naming the changeset
  there, plus one rule (§Stacked): any parent movement ends the child's approval, and the reason says
  what kind of movement it was
- distinguish a human's code edit from a human's comment
- send notifications

## Install

### The published binary

Prebuilt binaries for macOS and Linux, on amd64 and arm64, are published with every tag:

```bash
curl -fsSL https://github.com/bitandpixel/git-pair/releases/latest/download/install.sh | sh
```

The installer takes the platform from `uname` and the archive name from that release's `checksums.txt`
rather than constructing it — the download directory is keyed by tag (`v0.1.0`) and the archive is named
by version (`0.1.0`), so only the index knows both — then checks the SHA-256 and writes
`~/.local/bin/git-pair`. It takes `--version v0.1.0` to install a specific release, with or without the
leading `v`, `--install-dir DIR` to write somewhere else, and `--force` to replace a file at that path
which is not a working git-pair binary; `--help` lists them, and
[`packaging/install.sh`](packaging/install.sh) is short enough to read before piping it. It never uses
sudo, and it prints the `PATH` line you need when the install directory is not already on `PATH`.

What the checksum proves is a complete download and the right archive, not the publisher: both files
come over TLS from the same place. To verify a release by hand, pin the version, read the release notes,
and compare the hash yourself.

The binary must be named `git-pair` for git to find it as a subcommand, so `git pair review open` and
`git-pair review open` are the same program; the docs use the `git pair` form throughout.

### From source

Requires Go 1.27 and `git` on `PATH` (developed against git 2.43.0). The repository pins the
toolchain in `.mise.toml`:

```bash
mise exec -- go build -o ~/bin/git-pair ./cmd/git-pair
mise exec -- go install ./cmd/git-pair          # into $(go env GOPATH)/bin
```

Without mise, any Go 1.27 toolchain works. What `--version` reports says how the binary was built. A
plain `go build` prints `git-pair version 0.0.0-untagged` — the fallback at
`internal/cli/root.go:Version`, worded so that a build stamped by nobody cannot be mistaken for a
release. `mise run build` and `mise run build:prod` stamp the commit through the same linker flag, from
`scripts/dev-version.sh`, and print `git-pair version 0.0.0-dev+498c623`, with `.dirty` when the tree
held uncommitted work — still no number a release could be named, plus the one thing a bug report needs.
A release prints its tag, stamped by goreleaser through
`-ldflags -X gitpair/internal/cli.Version=…`. Cutting a release is `git tag v0.2.0 && git push
origin v0.2.0`; everything before that tag is checked locally by `mise run release:check`,
`release:snapshot` and `release:gate`, and `.github/workflows/release.yml` says what the runner does.

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
headings — or the headings your repository prefers, when it commits
`.git-pair/about-template.md` (see [Configuration](#configuration)). Fill it in, commit it with your
implementation, then:

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

The author addresses the feedback in code, `ABOUT.md` and the thread, and deletes the reviewer's
comment from the source once the work it asked for is done. History already holds the exchange: the
review commit introduced the line and a later commit removed it, so the comment needs no
transcription. Record it only when it still needs discussion, elaboration or another person, and
then in an `Addressed feedback` section of `ABOUT.md` or in a new thread (PRD §19.5). What the
reviewer sees next is the review-relative span, where review lines are deleted — that is how a
reviewer sees feedback being consumed:

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


Review these additions before continuing: apply each one, then remove the comment it came
from. The review commit keeps its history. Record anything that needs further discussion in
ABOUT.md or in a new thread.

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
  next:    author: `git pair check`, then merge into main with ordinary git
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
next:  `git pair check`, then merge into main with ordinary git
```

When the merge is somebody else's job — a pipeline, or the person who owns the destination branch — the
author says so once, in a commit on the branch:

```bash
$ git pair change integrate
Integrating: booking-transaction
  head:        0eaad3b
  declared by: 91bf204
  merge into:  main
  next:        `git push origin booking-transaction` — main merges what `git pair check --json` reports ready and integrating
```

That is the whole command: one empty marker, no ref, no push, no merge. It runs the gate above before it
writes, so nothing `check` would refuse can be declared, and `git pair change unready` takes the request
back. `merge into:` is the destination the walk of PRD §13.1 arrives at — for a child of landed parents, the
branch those ancestors reached, found by each ancestor's `base-changeset:` id rather than the branch its
`base:` still names (`feat/auth`, where the destination's record is `changesets/feat-auth/`) — and
`queue --json` prints the same value, which is what a pipeline merges into. What moved is who waits for
whom — the merge is still ordinary git run by whoever owns the branch, and CI's gate is both answers of that
one command:

```bash
git pair check --json | jq -e '.ready and .integrating'
```

Then the owner lands it, with ordinary git. Squash, rebase-merge, plain merge — git-pair has no
opinion and takes no part; it neither runs a merge nor derives one, because squash and cherry-pick
destroy the ancestry that would have said so.

The steps are the whole contract — `check`, the declaration when the merge is not the author's, the landing
with ordinary git — and nothing after the merge. A landing is not written down separately: the destination
branch carrying `changesets/<id>/` is what makes a changeset landed, and that directory is the whole record.
There is no ref to write, nothing to publish, and no step an integration can forget.

What the branch is needed *for* decides whether deleting it costs anything, and this is the one place the
tree model has a limit worth stating before you rely on it. A merge carries the reviewed chain inside
itself, as the side the merge brought in, so after a merge landing `git pair status --changeset <id>`
still reports the approval, the responses and the chain its reviewer read. A fast-forward carries the same
chain in trunk. A squash, a cherry-pick and a rebase-merge keep the chain on no commit that survives: the
directory arrives and the history does not, so `status` says `reviewed: false` with an empty
`chain_base` and `chain_head`. PRD §13 states that limit; the mitigation, where a review has to survive a
squash, is to merge with `--no-ff`, or to tidy while the branch is still there (below) so the chain stays
reachable from a branch rather than from nothing.

Nothing is tidied automatically, because a move is a commit and a commit is somebody's decision:

```bash
$ git pair change tidy booking-transaction
booking-transaction: changesets/booking-transaction -> changesets/.landed/booking-transaction
  committed: 6c1d2ef
```

`change tidy` moves a landed changeset's directory into `changesets/.landed/` and commits the move, so the
active listing holds work in progress and nothing else. It refuses a dirty tree, and it refuses an id the
destination does not carry — the changeset has to have landed. Run it on a branch whose history still holds
the reviewed chain if you want that chain to stay readable, and note that after the destination's own
tidy commit the chain it names is the destination's, so a squash's chain is still gone. Once the move is in
the destination it also ends the `LANDED UNREVIEWED` report for that changeset (§10.6): the reviewed rename
commit is the repository acknowledging the record it filed away, and `status --changeset <id>` still answers
`reviewed: false` for it afterwards.

Deleting the branch now loses what the destination did not carry, and says so rather than pretending:

```bash
$ git switch main && git branch -D booking-transaction
Deleted branch booking-transaction (was 0eaad3b).

$ git pair status --changeset booking-transaction --json | jq '{landed, landed_commit, reviewed}'
{
  "landed": true,
  "landed_commit": "4f2b8c1",
  "reviewed": true
}
```

A parent that landed while its child is still stacked on it is the thing an author hits first after a
landing. The child's chain — the commits a reviewer approved, which trunk never held — is the child's
history until the child is rebased onto the landing or the parent's branch goes away, and a merge landing
holds that chain itself while a squash holds it nowhere else. So the order that never costs you history is:
land the parent, then settle the child, then delete.

A stack says what is left of it at the same moment, and this is the ordinary next thing an author hits after
landing a parent: `git pair status`, `git pair check` and `git pair queue` name the landing and print the
command that settles the child. While the child's head is not on the landing it is
`git rebase --onto <landing> <parent-branch> <child-branch>`; once it is, the parent's branch is stale and the
command is `git branch -D <parent-branch>` — with the worktree named when another worktree has that branch
checked out, because the delete fails there and removing a worktree is not git-pair's to do. Once the parent's
branch is deleted as well and this head is already on the landing, the step is nothing, and the command says
that instead of printing a rebase onto a branch that no longer exists. Nothing in
git-pair runs either one: the rebase and the delete are ordinary git, performed by whoever owns the branches
(PRD §26).

A landing does not by itself end the child's approval. `check` compares the diff under test with the diff the
approval measured, which it can do because the submission recorded the commit it measured from
(`Review-Base-Head`, PRD §10.4). A plain merge landing that brought nothing new leaves that diff alone, and the
approval stands; a squash, a rebase-merge or a cherry-pick moved the reviewed content into commits the child
never had, and `check` refuses with the reason saying which of the two it found. An approval old enough to
have recorded neither the parent tip nor the measured base is a note rather than a refusal, because the
absence says the trailer was not written and nothing more (PRD §21).

Two facts, read from the destination, and never stored: the directory that says the work landed, and the
commit that brought it. A changeset is discovered from the trees a revision carries, so a pipeline needs
only the branch it already has.

## Concepts

**Changeset.** A changeset is a directory, `changesets/<id>/`, and its identity is its **ID** —
the name of that directory. The branch name is only where the default comes from. The default
normalises the branch: `/` and every character outside `[A-Za-z0-9._-]` become `-`, runs
collapse, leading and trailing `-` are trimmed, case is kept, so
`feature/booking-transaction` becomes `changesets/feature-booking-transaction/`. Choose your
own with `git pair init --id booking-transaction-v2`; an ID is never rewritten to fit,
never suffixed to dodge a collision, and never changed once the changeset has refs.

`CHANGESET.yaml` records `id` and where the changeset sits, and nothing else. `base` is what the
diff is measured against. A stacked changeset spells that as the branch it sits on, which *is* its
base, beside `base-changeset:`, the changeset living there. The changeset named there is what still
names the relationship after the parent lands and its branch is deleted, which is when a child needs
it most. `parent:` and `parent-changeset:` are the same pair under older key names: they are read
from files written before this pair existed and nothing writes them, and a file that sets both an
old and a new spelling of the branch is an error to correct rather than a conflict to resolve. There is no branch field: the
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
for one command, and the two exits settle it for the branch: `git pair change stack --base <branch>` records the
link when one directory is the work the other sits on, and `git pair change combine --into <id>` folds the two when
they are one piece of work. The integration
branch is what "landed" is measured against: `--default-branch <ref>` states it (this is what CI
passes), otherwise git's own answer — `refs/remotes/origin/HEAD`, then a sole `origin/main` or
`origin/master`, then a local `main` or `master`. If none exists the command refuses rather than
inventing a trunk, and no git config is read.

A landing in a branch that is not the changeset's destination is invisible to that rule, and the changeset
keeps reading as live work: the queue lists it as waiting for a reviewer, and `status` offers a next action
that already happened. The destination is where the tree read looks — the branch the changeset asks to land
on, which for a stack is where its landed parent went — so a change merged into `release/2.x` while its
destination says `main` is not reported as landed. The fix is to tell git-pair which branch is the
integration branch (`--default-branch release/2.x`, or the changeset's own `base:`), not to write a record:
under this model there is nothing to write, and a release-line repository that leaves the destination unset
will show landed changesets as active forever.

The same rule runs the other way when a repository tidies trunk. Deleting landed `changesets/<id>/`
directories from the integration branch makes those changesets unlanded for every branch that has not
merged the deletion, so they come back with their whole review history, and `change tidy` exists so that a
tidy is a move into `changesets/.landed/` rather than a delete. Prune what has been moved and is no longer
wanted; a changeset retired by trunk's tree alone comes back with the deletion. A terminal record is a
marker on a branch, so pruning trunk does not retire that either.

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
| review | `review: <outcome> <slug>` | `Review-Outcome: <outcome>`, `Review-Changeset: <slug>`, `Review-Head: <sha>`, `Review-Base-Head: <sha>`, `Review-Diff-Id: <version>:<hex>[+<hex>]`, and `Review-Parent-Head: <sha>` for a stacked changeset |
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

The other two fields name where the reviewed diff started rather than what it ended at.
`Review-Base-Head` is the commit the submission measured its diff from, and `Review-Parent-Head` (a
stacked changeset only) is the tip of the branch it is stacked on. `check` compares the second with the
parent branch's current tip, which is how it says whether the parent moved, and compares the recorded
identity below with the contribution this branch makes from whichever commit is its ground today, which is
how it answers whether the diff it would land is the diff that was approved (§11.3, PRD §21.1); the first
is what makes that refusal name which side moved. While a parent's branch is the base the two fields name
the same commit; once that parent has landed they differ, and the branch the second one names is often
deleted — which is why the first one is a commit.

`Review-Diff-Id` is the third of the three, and the only one that names the content: the identity of the
recorded diff between that base and that head, in two halves separated by `+`. The first hashes what the diff
changes — modes, blob OIDs, statuses, paths — with the review record left out: this changeset's directory and
every ancestor's, in both of their homes, so replying to a review thread cannot change the identity of the
approval the reply is written into, and a parent's landing cannot read as the child deleting the parent's
review. The second is `git patch-id --verbatim` over the patch the same `git diff` prints, which is what
changed and where: it is what lets an approval stand when the destination edited a file this branch also
edits, on a line nowhere near this branch's, and what refuses when that edit is inside the context the
reviewer read (PRD §21.1). Both halves come from one invocation, so they cannot disagree about the ground or
the exclusions, and they travel in one trailer for the same reason. A git that cannot compute the second half
leaves it off, and the gate asks the stricter question.

It carries a
version because the value is defined by the rules that produced it. A marker whose version this build does
not know reads as no identity recorded, which is an absence rather than a difference, and the reader gets
the comparison still available; `1:` carried the first half alone, and is compared on it.

**Readiness is withdrawn with a command.** `git pair change unready` commits `Review-State: working`
and takes the changeset out of the queue. Readiness is an offer made with `git pair change ready`, so
withdrawing it is a command too: an author who wants to keep implementing after handing off says so,
instead of leaving a reviewer to guess whether a changeset in the queue is finished work or work in
progress. `working` is not a sixth state — it is `WORKING` chosen on purpose, and a changeset with no
marker derives the same answer. The marker is written only when the changeset is in review (`READY`,
`APPROVED`, `FEEDBACK` or `INTEGRATING`); on a `WORKING` or `BLOCKED` changeset there is nothing to withdraw, so the
command succeeds and records nothing. Withdrawing an approval does not delete it: the approval stays
in `git pair review history`, and `git pair check` refuses until a reviewer approves again. The
withdrawal is the marker commit and nothing else.

**Landing is a fact about the destination's tree.** A changeset is landed when the destination branch
carries `changesets/<id>/`, and that directory is the whole record: git-pair writes no ref at landing, and
nothing has to be told that the work arrived. The destination is read per changeset — the branch its own
`base:` names, or, for a stack, where its landed parent went — so the answer does not depend on a guess
about which branch is "the" trunk.

The merge itself is ordinary git, which git-pair neither runs nor derives. What a landing keeps is what the
merge carried: a merge keeps the reviewed chain inside itself, so `status --changeset <id>` still names the
approval and the chain; a squash, a rebase-merge and a cherry-pick keep the content and destroy the
ancestry, so `status` reports the landing with `reviewed: false` and an empty chain. PRD §13 states that
limit, and `change tidy` is the way to keep a chain when the repository squashes: it moves the directory to
`changesets/.landed/<id>/` in a commit, on a branch that still holds the history, rather than deleting it.
A landed changeset still reports `APPROVED` (or `FEEDBACK`) — landing is not a marker, and `state` stays the field markers move — with `landed`, `landed_commit` and
`landed_branch` beside it, read from the destination's tree rather than from a ref. Ownership follows the same
split: the reviewer approves the code, the owner decides what gets taken forward and where. And because the
answer is the destination's own history, which git-pair never writes, there is nothing git-pair can quietly
rewrite that a release note, a bisect, or an agent asking "where did this review go" reads as fact.

**A changeset can end.** `git pair change abandon` commits `Review-State: abandoned` — the marker and
nothing else, since an abandoned changeset never lands. It is
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
`WORKING`, `READY`, `BLOCKED`, `FEEDBACK`, `APPROVED`, `INTEGRATING`. `git pair status` prints the
state plus a one-line `Reason`. There is no state file, and no state for a completed
changeset: completion is a pair of refs (below), and the merge that finishes a changeset is not
something state derivation can see. `INTEGRATING` is the sixth because it is a marker — the author's
request that this head be merged (§9.9) — and it is not a verdict: the approval underneath it is what
permits the merge, which is why `check` answers `ready` and `integrating` separately. (The TUI caches the reviewer's
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

**git-pair writes no refs.** Not at landing, not while work is in flight, not anywhere: `change ready`,
`change unready`, `change feedback`, `change wait`, `review submit` and `change abandon` all write commits
and nothing else, `change tidy` moves a directory with `git mv`, and no command pushes. While work is going
on the branch is the record — it holds the chain — and afterwards the destination's tree is. That is why the
queue, the gate and `status` need no fetch to answer: a clone that has trunk and nothing else gives the same
answers about landings as one that has the whole repository.

Because nothing is stored about a landing, nothing can be out of date, unpublished, half-written, or left
behind by a refspec nobody configured. What is lost is the ability to name history the destination does not
carry — a squash's chain, or an abandon marker on a branch that was deleted — and git-pair reports those as
the limits they are (`reviewed: false`, an empty `chain_base`) rather than inferring a fact from a patch ID
it cannot verify.

The write gate follows from the same read: `change ready`, `change unready`, `change abandon` and
`review submit` refuse a changeset the destination already carries, because the review of a landed changeset
is over, and the refusal happens before the commit is written, so a refused command leaves nothing behind.

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
`skill install` change output for
it; elsewhere it is accepted and ignored.

Every command also accepts `--default-branch <ref>`, which states the integration branch that
"has this landed?" is measured against. Without it git-pair reads git's own answer
(`refs/remotes/origin/HEAD`, then a sole `origin/main` or `origin/master`, then a local `main` or
`master`) and refuses if there is nothing to compare against. It is a flag and not a config key
because the answer belongs to the checkout in front of the command: a CI job that fetched one
branch has no remote HEAD, and two clones of one repository must not disagree about what has
landed.

`GIT_PAIR_DEFAULT_BRANCH` is that same value carried once in the process environment, which is the
form a CI job reaches without threading the flag through every call, and the one the shipped merge job
sets. The flag outranks it: the command line is the statement about this invocation, and a value
inherited from the shell cannot contradict it. What the value moves is the destination rather than the
amount of work, so using it is made visible rather than trusted — the run says once on stderr which
variable named the branch and what it named, `status --json` reports `default_branch_source: "env"`
where the flag would have said `"flag"`, and a value that resolves to nothing is refused exactly as a
mistyped flag would be. An empty value is not set, so a job that means to leave the variable unset does
not leave an empty ref to resolve.

Every command also accepts `--no-cache`, which ignores the two local caches and derives everything
from git again. Read-only commands ask git the same questions on every run — which changesets have landed,
what chain each one left behind, what markers sit on a branch — and the answers to those questions follow
from commit ids, which do not change. So they are kept, in one case in memory for the length of the run and
in the other under the repository's git directory (the same place review marks live, where `git status`
cannot see them and `git add` cannot stage them; deleting the directory loses nothing but time). Neither is
a store of state: state is still derived from history, and a key that no longer matches the repository is a
miss rather than an answer. `--no-cache` is therefore not a way to fix a wrong answer — it is how you
establish that an answer was not wrong, and how a machine that wants nothing written under its git directory
says so. `GIT_PAIR_NO_CACHE`, set to anything non-empty, does the same for every invocation in a process
environment, which is the form a CI job reaches without threading the flag through each call. The test suite
uses it for a sharper reason: the bounds that count git subprocesses to say how much work a formulation does
must count the formulation, and a memo that answers a duplicated read would let such a bound pass on the
duplicate it exists to catch.

| Command | Flags | Notes |
| --- | --- | --- |
| `init` | `--id <id>`, `--base <ref>`, `--set-base`, `--parent <branch>`, `--set-parent`, `--about <text>`, `--set-about`, `--no-commit` | creates directory, `CHANGESET.yaml`, `ABOUT.md`, then commits them; never overwrites existing content; `--about` also reads a pipe; default base is the integration branch, recorded as its branch name where that name resolves and as the fetch ref this clone has to reach it through where it does not; refuses on that branch, where a changeset could never contain anything; `--parent` stacks the changeset instead of naming a base, recording the parent's changeset ID beside it, and `--set-parent` restacks it — never done implicitly, because a parent that moved, landed or died is the author's decision; `--id` names the changeset instead of the branch-derived default, and a collision with a committed directory or ref refuses rather than suffixing |
| `change ready` | `--allow-surviving-review-additions` | fully non-interactive; checks below |
| `change integrate` | `--allow-feedback` | declares the approved head ready to be merged, as one empty marker commit (`Review-State: integrating`, `Review-Head`): no ref, no push, no merge (§9.9). Runs `git pair check`'s gate first, as the same code, and names every failed condition — so a declaration cannot be made for work the gate would refuse. Refuses a stacked child whose parent is not landed: the automatic merge would land it on a branch review can still rewrite. Idempotent at the head it declared; a commit after that head is declared next time |
| `change unready` | none | withdraws the changeset from the review queue; records `Review-State: working` when the changeset is in review (including a changeset whose declaration is being taken back), otherwise succeeds and records nothing; refuses a changeset the integration branch already holds |
| `change stack --base <branch>` | records the stack link between this branch's changeset and the one <branch> carries: writes `base: <branch>` and `base-changeset: <id>` into the child's `CHANGESET.yaml` and nowhere else, prints the old and new value of both keys, and says to offer the branch again; refuses when the two branches share no changeset, when more than one could be the level below, or when this branch carries more than one changeset of its own |
| `change combine --into <id>` | `--threads` | folds one changeset into another when the two directories are one piece of work: the disappeared one moves whole to `changesets/<id>/.combined/<gone>/` and stays inert there, the survivor keeps its own `base:` and `base-changeset:` and gains one line pointing at the archive, and the command refuses while either changeset is offered or under review. `--threads` copies the disappeared threads, prefixed with its id |
| `change abandon` | none | records the terminal `Review-State: abandoned` and nothing else; `change ready`, `change unready` and `review submit` refuse against it afterwards; refuses a changeset the integration branch already holds; idempotent |
| `change tidy [<id>...]` | `--all-landed`, `--dry-run`, `--json` | `git pair change tidy` moves the directory of a changeset the integration branch already holds from `changesets/<id>/` to `changesets/.landed/<id>/`, as one commit of renames on this branch. A changeset like any other: it rides a changeset and a review, the reviewer sees the whole list as renames in one span, and the move reaches trunk through the normal flow. Refuses an id that has not landed, a dirty working tree, and an id this branch does not carry — each with its own reason, and none of them moving part of the list. A landed parent whose child is still open moves, and the run notes the branch whose changeset records it as its base. A second run is a no-op naming the directory it found. Once the move is in the destination it also ends the `LANDED UNREVIEWED` report for that changeset, because the reviewed rename commit is the acknowledgement (§12) |
| `change feedback` | `--stat`, `--name-only`, `--changeset <slug>` | the diff of the most recent review submission (`review^..review`): threads, `ABOUT.md` edits and reviewer code edits together; exits 2 if there is no submission |
| `change wait` | `--fetch`, `--interval <dur>` (default `10s`), `--timeout <dur>` | blocks until the state leaves `READY` for `BLOCKED`/`FEEDBACK`/`APPROVED`; read-only; `--fetch` runs `git fetch` before each check so a review pushed from another clone is noticed |
| `review`, `review open` | `--unreviewed`, `--since-review[=N]`, `--base-review[=N]`, `--base-commit`, `--base-ref`, `--head-review[=N]`, `--head-commit`, `--head-ref` | TUI; needs a terminal; full changeset unless a span flag says otherwise; a `--head-*` flag opens a historical span, which is read-only; with no subcommand `review` is `review open` and takes the same flags |
| `review reopen` | none | TUI on `<last review>..current`, the work that has landed since you reviewed; needs a terminal; refuses if no review exists |
| `review about` | — | opens `ABOUT.md` in the editor, creating it if missing |
| `review thread [title...]` | — | slugifies the title, reopens an existing match, prompts for a title only with a terminal |
| `review submit` | one of `--block`/`--feedback`/`--approve`, `-m/--message <text>`, `--no-stage` | stages the whole tree by default, commits (empty commits allowed), and writes nothing else: a submission is a marker commit, not a ref move. The commit names what it reviewed with `Review-Head`, which is what lets `check` refuse a rewritten history, and where it measured with `Review-Base-Head`, which is what lets `check` tell which side moved, and what it measured with `Review-Diff-Id`, the identity of that diff in two halves — the content, and the rendered hunks (`<version>:<raw>+<patch>`) (§11.3) |
| `review history` | `--changeset <slug>` | only review marker commits, indexed from `0`, each naming the commit it reviewed under `REVIEWED` and the reviewer who submitted it under `REVIEWER` |
| `queue` | — | one row per branch whose changeset is `READY`, longest wait first, plus a `LANDED UNREVIEWED` heading for a landing whose destination holds no approval of what it carries (a finding no command closes, and one a `change tidy` that reached the destination does end, because the move is the acknowledgement); read from the repository, not the checkout. A second list, `AWAITING INTEGRATION` / `awaiting_integration`, holds approved work whose author has asked for the merge (§9.9) with the branch it is asking to land on — never in both lists, because a declaration is a marker and a branch carrying one is not `READY` |
| `status` | `--changeset <slug>` | derived state, for this branch's changeset or one named by slug, plus the landing read from the destination's tree: `landed`, `landed_commit`, `landed_branch`, and the chain that arrived with it (`chain_base`, `chain_head`, `reviewed`) |
| `check` | `--allow-feedback` | asserts integration-readiness and exits 1 when it is not; lists every failed condition — the review's outcome, whether the commit it approved is still in this history, and whether the content still matches; reports `integrating` beside the verdict, so CI's gate is one command and two fields (`jq -e '.ready and .integrating'`); no `--changeset`, because it is the gate a forge runs *on* a revision |
| `diff [path...]` | `--unreviewed`, `--since-review[=N]`, `--base-review[=N]`, `--base-commit`, `--base-ref`, `--head-review[=N]`, `--head-commit`, `--head-ref`, `--stat`, `--tool` | paths are checked against the span first, so a typo is an error, not an empty diff |
| `skill list` | — | the agent skill this binary carries, and every directory a harness would read it from, each marked `current`, `stale`, `absent` or `unavailable`. `current` means the installed bytes equal this binary's, which is the check that keeps an installed skill from describing an older tool. Read-only |
| `skill show` | `[path]` | prints one compiled-in file of the skill, `SKILL.md` by default, so it can be read or copied without a checkout. No JSON output |
| `skill install` | `--harness agents\|pi\|claude`, `--scope repo\|user`, `--dest <dir>`, `--dry-run`, `--force` | writes the compiled-in skill into a skills directory as `git-pair/`. Matching files are left alone, differing files are refused without `--force`, and files git-pair did not write are reported and kept. `--dest` cannot be combined with `--harness` or `--scope` |
| `skill agents-md` | — | prints the pointer stanza for `AGENTS.md` or `CLAUDE.md`, for a harness with no skill discovery. No JSON output |

`change ready` checks, in order: clean working tree, `ABOUT.md` exists, the repository has
commits, no blocking surviving additions — the last acknowledged with
`--allow-surviving-review-additions`. `check` asks what the merge would act on: the newest marker is a
review whose outcome permits integration (`approve`, or `feedback` under `--allow-feedback`), nothing
unreadable came after it, the changeset has not ended, the integration branch does not already hold it,
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
| 1 | a git-pair rule or the repository state refused the operation | surviving additions; `working tree must be clean`; `ABOUT.md is missing`; `cannot resolve changeset base "vanished"`; `change wait` timing out, or refusing a changeset that is `WORKING`; `git pair check` printing `NOT READY:`; `git pair change integrate` refusing work the gate would refuse — no approval standing, content that moved since it, a rewritten history, a contribution that is not the diff the approval measured, a branch that would conflict with its destination, a dirty tree — or refusing a stacked child whose parent has not landed; `change tidy` naming a changeset that has not landed, or one this branch does not carry |
| 2 | usage error | unknown flag, unknown command, or unknown subcommand of `change`/`review`/`skill`; `no changeset for this branch`; `no branch carries changeset "<slug>"`; detached HEAD; `cannot tell which branch is the integration branch`; `--block, --feedback and --approve are mutually exclusive`; `changeset has no review submissions yet`; `changeset <cs> has no review submission yet` (`change feedback`); `--interval expects a duration` (`change wait`); `"<path>" does not appear in <span>`; editor/TUI commands without a terminal; `skill install` with an unknown `--harness` or `--scope`, or with `--dest` alongside either |
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
state value of its own. `landed` is true when the integration branch's tree carries the changeset's
directory, `landed_commit` names the commit it arrived in, and `landed_branch` names the branch the read was
taken from — the pair is the answer, because a bare `true` would not say which branch decided. Landing is
read from the destination rather than from a ref, so a clone that has fetched nothing but the integration
branch answers it the same way. `chain_base` and `chain_head` bound the run of work the destination carries
behind the directory — the span a reviewer read, and where the markers they left sit — and are `""` exactly
when the record came alone: the directory arrived in one commit that is not a merge, and the chain holds no
marker for the changeset, which is what a squash or a cherry-pick leaves. A linear landing prints its range
even when its record came in a single commit, as long as the chain carries markers — there is a run there to
read, and the range reaching the destination's tip is the shape of a linear run, not a claim that everything
in it belongs to this changeset.
`reviewed` says the destination holds a permitting verdict **and** the commit that verdict names, so it is
`false` for a chain with no approval, for an approval whose commit the destination does not carry, and for an
approval that names no commit. `state` is untouched by all of it: landing
is a fact beside the state, not a state value of its own. `integrating` and `integrate_commit` are the
other pair beside it, and the only one that is also a state: `INTEGRATING` while a `git pair change
integrate` declaration (§9.9) is the newest marker, the commit named so the author can see what they did
and not only what it produced. The human surface prints no command beside a landing that has happened —
the finding for work in the destination that the destination holds no approval of is `LANDED UNREVIEWED`,
which prints a read rather than a command, because nothing closes it with one (a merged `change tidy` ends
the report, and it is a commit rather than an invocation). Reading a landed changeset by
id (`status --changeset <id>`, no branch carrying it) reads the same chain, so its `state`, its verdict and
its thread files come from the destination's history — with the squash case above the honest limit, and the
fields say so rather than reporting an empty range as a verdict. `stack` walks the chain the child's
`parent-changeset:` starts: one entry per ancestor, nearest first, with the ancestor's id, the branch it was
stacked on and whether this clone still has that branch, and the commit the destination carries it at. It is
`[]` for a changeset that sat on the integration branch, and
`stack_note` is the one sentence for where the walk stopped — a hand-edited cycle, the depth cap, or a
read that failed — so a short list is never mistaken for the whole chain. `default_branch`,
`default_branch_commit` and `default_branch_source` name the branch "landed" was measured against,
the commit it pointed at, and how the run learned it (`flag`, `origin-head` or `sole-candidate`):
a CI log that says nothing has landed has two causes, a stale fetch and a wrong trunk, and neither
one is visible in a sentence about the changeset.
`next_action` is the landing contract (§PRD §29) in one line: `check`, then the merge with ordinary git.
Once the directory is in the destination it says there is nothing further to do.

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
  "landed": false,
  "landed_commit": "",
  "landed_branch": "",
  "chain_base": "",
  "chain_head": "",
  "reviewed": false,
  "uncommitted": false,
  "abandoned": false,
  "reviews": 0,
  "reason": "marked ready by 8065dae",
  "span": "main...current",
  "next_action": "waiting for a reviewer: `git pair review open` (author: `git pair change wait` to block on it)"
}
```

`git pair queue --json` — `head` and `ready_commit` are full SHAs. `skipped` names changesets
the queue cannot put in a list (`[]` when empty): a branch whose metadata cannot be read, a branch the
resolution rule cannot settle between two changesets, a changeset the destination already carries — that
one names the commit and the branch, because the branch is usually still here and its disappearance from the
queue would otherwise be a mystery — or a changeset directory with no branch and no landing. A changeset
that has landed in its destination is silent — it is not work a reviewer can act on. What is *not* silent is a
directory the destination carries with no approval it can show: that is a merge whose review never
reached the destination, and it is reported by name.

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
  "skipped": ["booking-transaction (landed on main at 4f2b8c1)"],
  "awaiting_integration": [
    {
      "changeset": "waitlist-rebooking",
      "branch": "waitlist-rebooking",
      "base": "main",
      "state": "INTEGRATING",
      "head": "7c31b0a0d9cee930b85324aedfbc0aa4b33c1409",
      "integrate_commit": "2ce9f4a1d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a1",
      "declared_age": "2h",
      "destination": "main"
    }
  ],
  "parent_notes": ["waitlist-rebooking: parent booking-transaction landed as 4f2b8c1 — the branch booking-transaction is stale — it holds nothing the destination does not"],
  "landed_unreviewed": [
    {
      "changeset": "waitlist-rebooking",
      "commit": "4f2b8c1",
      "chain": "",
      "reason": "the landing carried the directory in one commit, and the chain carries no review markers"
    },
    {
      "changeset": "offer-expiry",
      "commit": "9d1c07e",
      "chain": "3b6a2f1..d40c81a",
      "reason": "the approval (d40c81a) names 41e7b19, which the destination does not carry: the landing replayed the run, so what was approved is not what landed"
    }
  ]
}
```

`parent_notes` is what the queue says about a row it is not refusing: the branch underneath a READY child
has landed, so the base a reviewer is about to read against is finished work. It is a note
and not a row, and never a reason — a READY changeset has no approval for a parent to invalidate.

`awaiting_integration` is the second list, and it answers a different person. `ready_for_review` is "what is
waiting for a reviewer"; this is "what a reviewer approved and the author has handed over for merging"
(§9.9), oldest request first, each row carrying the branch somebody would merge into. A changeset is never
in both: a declaration is a marker, so a branch carrying one is not `READY`. The human form prints them
under `AWAITING INTEGRATION`, and prints nothing when there are none — the array is `[]` either way.

`landed_unreviewed` is the queue's second job: work that reached the integration branch with nothing in the
destination approving what arrived. Nothing closes it with a command, so the heading prints the read that
goes and looks, and the `reason` says which of the three it is — no verdict in the chain, a chain that came
with no verdict-bearing commits at all (the squash, which leaves `chain` empty), or an approval that names a
commit the destination does not hold (a replayed run, which does not). A landing the destination carries only
under `changesets/.landed/` is in neither the heading nor the array: a `change tidy` commit that reached the
destination moved that record out of the way on purpose, which is the acknowledgement (§12). The finding
survives the filing — `git pair status --changeset <id>` reads the same chain and answers the same way — and
a tidy nobody has merged silences nothing, because the destination carries the directory in place until the
move arrives.

```text
LANDED UNREVIEWED

  waitlist-rebooking
    on main at 4f2b8c1: the landing carried the directory in one commit, and the chain carries no review markers
  offer-expiry
    on main at 9d1c07e, chain 3b6a2f1..d40c81a: the approval (d40c81a) names 41e7b19, which the destination
    does not carry: the landing replayed the run, so what was approved is not what landed

  read one with `git pair status --changeset <id>`
```

The state is the one the merge leaves behind when the review never came: the directory is in
trunk, so the rules that find work in progress stop seeing it, and the branch may already be deleted.
`git pair status` prints the same finding on a branch that carries no changeset of its own — attached to
its exit-2 "no changeset for this branch" answer, which stays exit 2 because the branch really does hold
no work in progress. Its `--json` there is a document rather than only an error: `reason` and
`landed_unreviewed`, with the list present and empty when there is nothing to report.

When the destination already holds the directory, the answer says the changeset landed. It then names
`git pair status --changeset <id>` rather than telling you to run `git pair init` over work that is finished.

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
  "next_action": "author: `git pair check`, then merge into main with ordinary git",
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
verdict went and omitted only when that marker names no head. `landed` reports a changeset the
destination already holds (PRD §11.3), read from that branch's tree. Six more fields are the stack
beside the verdict (PRD §21): `parent_landed` and `parent_landed_commit` say the branch this child was
measured against is finished work and name the commit it became, `parent_stale_branch` says the
parent's branch is still here holding nothing the destination lacks, `parent_head_carries_landing`
says this head is already on the landing — which is the half that stays answerable after that branch is
deleted, and the half that decides whether any rebase is owed — and `parent_measured_base` and
`parent_comparison` are the two facts behind a parent verdict: the commit the approval recorded measuring
from, and which reading answered it — `contribution` when the recorded content identity was compared with what
this branch contributes today, `contribution-patch` when the content moved and the recorded identity of the
rendered hunks did not (a file this branch edits also changed in the destination, and the branch still merges
cleanly), and `merge-base` when the older comparison of the two bases decided.

A branch that merged the integration branch in also carries `drift_credited`: the paths that differ from the
reviewed commit and were not counted as drift because the destination carries that content (PRD §11.3). It is
what the verdict rests on, reported rather than assumed, so a reader who does not believe a passing gate can
see what was taken on trust. Content the destination does not carry — an ancestor still sitting on its own
branch, somebody else's branch, a conflict resolved by hand — is in `reasons`, not here.

This form carries the verdict in `ready` rather than in the exit code: a not-ready run prints its
JSON and exits 0, so a job piping it into `jq` keeps git-pair's answer separate from the pipeline's.
Usage errors and git failures still exit 2 and 3 here.

A passing verdict also carries `next_action` — the same sentence `status` prints in its `next_action`,
naming the merge — so an agent that gates on `ready` learns what
comes next without parsing the human output. It is present only when `ready` is true, because on a
failing gate the next step is `reasons`.

`integrating` answers the other half of a merge gate, and the two are reported apart because they are two
questions: `ready` is "may this merge", `integrating` is "did the author ask for one" (§9.9).
`integrate_commit` is the declaration, full like `head`, and absent when the newest marker is not one. A
superseded declaration — put back in review, re-offered, or answered by a reviewer — reads false, so a
pipeline cannot perform a merge the author has just taken back. CI's gate is one command and both fields:

```bash
git pair check --json | jq -e '.ready and .integrating'
```

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
  "landed": false,
  "integrating": false
}
```

`git pair change tidy --json` — the move, or the move a `--dry-run` would make. `destination` is the
branch the ids were checked for landing against, `commit` is the rename commit this run wrote and is empty
for `--dry-run` and for a run with nothing to move, and each item names the directory it moved from and to.
`action` is `moved` when this call moved it, `noop` when `changesets/.landed/<id>/` was already there, and
`would-move` on a dry run.

```json
{
  "destination": "main",
  "dry_run": false,
  "commit": "91bf204d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
  "changesets": [
    {"id": "booking-transaction", "action": "moved",
     "from": "changesets/booking-transaction", "to": "changesets/.landed/booking-transaction"}
  ]
}
```

`git pair change ready --json` prints `changeset`, `branch`, `base`, `state`, `head`, `ready_commit` (full
SHA), `review_queue_visible` and `acknowledged_survivors`. `surviving_review_artifacts` appears only when a
surviving-additions report existed — which is when `--allow-surviving-review-additions` acknowledged lines.

`git pair change integrate --json` — the declaration, and the destination it is asking for. `was` is the
state the branch was in and `state` the state after, which differ only when a marker was written;
`recorded` is false when this head was already declared, which is a success rather than a refusal, and
`integrate_commit` then names the declaration that was already there. `destination` is the branch somebody
would merge into — `destination_source` says which rule produced it (`base`, `parent` for the branch a
landed parent landed on, `default`), `destination_via` names the parents walked to get there, and
`destination_unreachable` names the base that no longer resolves here when the answer fell back. `reasons`
is an array in both outcomes, so a caller that read a refusal and a caller that read a success handle one
shape.

```json
{
  "changeset": "booking-transaction",
  "branch": "booking-transaction",
  "base": "main",
  "state": "INTEGRATING",
  "was": "APPROVED",
  "head": "0eaad3b18686624cb624722b4e8c348bf03451a7",
  "recorded": true,
  "integrate_commit": "91bf204d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
  "destination": "main",
  "destination_source": "base",
  "reasons": [],
  "next_action": "`git push origin booking-transaction` — main merges what `git pair check --json` reports ready and integrating"
}
```

`git pair change unready --json` — `was` is the state the command found and `state` the state after
it, which differ only when a marker was written. `recorded` is false when there was nothing to
withdraw, which is a success rather than a refusal, and `unready_commit` is empty then. Withdrawing a
declaration (`was: "INTEGRATING"`) is how an author stops a pipeline from merging work that has gone back
into review.

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
  "next_action": "`git pair change feedback`; feedback is non-blocking, `git pair check`, then merge into main with ordinary git"
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
# the merge is somebody else's job? git pair change integrate — a request, in a commit
git pair change integrate           # declares this head ready to be merged; merges nothing
```

Rebasing after an approval invalidates it, and `check` says so in as many words. Re-offer the work with
`change ready` and wait for a reviewer; do not edit the marker's `Review-Head` to make the comparison
line up — that trailer is the reviewer's statement about what they looked at, not the author's to amend.

`check` is the last step an agent runs. `change integrate` is the one step after it that an agent may run:
it is a request written as a marker commit, it refuses anything `check` refuses, and it merges nothing —
so it can be run unconditionally after a passing gate. Landing is still not the agent's: the merge is
ordinary git run by whoever owns the destination branch (§13), and it is the whole of it — the destination
carrying the directory is what makes the change landed, so there is nothing to record afterwards.

Never prompt: `init`, `change ready`, `change integrate`, `change unready`, `change abandon`, `change feedback`,
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

The landing is not an agent's action either, and there is no command left to run after the merge: where the
work landed is read from the destination's tree, which is a fact about a merge someone else performed. An
agent asked whether the work may land reads `git pair check` and stops there.

**As a CI gate.** `check` reads the repository — the branch's commits and their trees — so a plain
checkout of the branch is enough to run it:

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

A merge nobody asked for is a different failure, so the gate has a second field. `integrating` is true while
the newest marker on the branch is a `git pair change integrate` declaration (§9.9) covering this head, and
false again as soon as the work is put back in review, re-offered, or answered: a request is superseded,
not erased. A pipeline that gates on `ready` alone merges every approved changeset the moment it is
approved, which takes the decision out of the author's hands — so the gate for an automatic merge is the
conjunction, from the one command that computed both halves from one read of the branch:

```bash
git pair check --json | jq -e '.ready and .integrating'
```

One thing a gate needs to know: `check` accepts `feedback` only with `--allow-feedback`. Whether a
non-blocking review is enough to land is a policy of the repository, and the flag is the place it is
stated — there is no config key for it, because the command that runs the gate is the only place the
policy is known.

**After the merge.** There is no step after the merge. The destination branch carrying
`changesets/<id>/` is what makes the change landed, and git-pair reads that where it needs to know, so the
person who merged runs nothing and a pipeline records nothing:

```bash
git merge --no-ff booking-transaction
git pair status --changeset booking-transaction   # landed at <merge commit>, chain included
```

What a landing keeps is what the merge carried. A merge keeps the reviewed chain inside itself, so `status`
names the approval, the chain base and the chain head; a squash and a cherry-pick keep the content and
destroy the ancestry, so `status` reports `reviewed: false` with an empty `chain_base` — the honest answer to
a question the destination can no longer ask (PRD §13). A rebase-merge keeps a chain of a kind: the
destination's own line carries the record to its tip, so the range is printed and the verdict in it is read —
and `reviewed` is `false` when the commits that verdict names are the ones the replay left behind. Nothing
refuses a squash landing; what it costs is the review history behind the directory, and `change tidy` is how
a squashing repository keeps it: move the directory into `changesets/.landed/<id>/` from a branch that still
holds the chain, before the branch and the chain are gone. That merged move is also what ends the
`LANDED UNREVIEWED` report for the changeset (§10.6), which is why the recommendation and the notification
point the same way.

Once the destination carries the directory, `check` refuses the changeset as already landed and the
commands that write markers refuse it too. A landing on a branch that is not the changeset's destination is
not a landing for these rules — see Concepts.

**What a CI checkout needs.** `check`, `status` and `queue` read the destination's tree, so a checkout that
holds only the branch under review is missing the branch the question is asked against:

```bash
git clone --branch "$BRANCH" --single-branch "$REPO" work && cd "$work"
git fetch origin --no-tags main:refs/remotes/origin/main          # the integration branch
git pair check
```

The default branch is not the optional one. The rule that decides which changeset a revision is working
on compares its tree against trunk's, and landings are read from it, so a checkout holding one branch refuses
to invent a trunk (exit 2) rather than read every directory as work in progress — and
`--default-branch origin/main` states it directly instead of fetching. A `--single-branch` clone records no
`origin/HEAD`, which is why the second line above names the branch. A clone that fetches everything needs
neither line.

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

## Landing a declared change from CI

`git pair change integrate` asks for a merge. Somebody still performs it, and a repository can decide that
its pipeline performs it on the destination branch's behalf. This repository carries one shape of that, as
an example — no git-pair command merges anything, and none of this is one:

| file | role |
| --- | --- |
| `.github/workflows/git-pair-integrate.yml` | the triggers, the permissions, the build — nothing else |
| `scripts/ci/git-pair-integrate.sh` | the sequence, in shell, so it can be replayed without a runner |
| `scripts/gates/ci-integrate.sh` | the replay: that job against scratch bare remotes, part of `mise run gates` |

Run it by hand from any clone, on one branch or on everything the queue reports:

```bash
scripts/ci/git-pair-integrate.sh feat/my-branch
scripts/ci/git-pair-integrate.sh --dry-run feat/my-branch   # says what it would do, writes nothing
scripts/ci/git-pair-integrate.sh                            # every changeset awaiting integration
```

The sequence is §29's landing contract, and the order is the point:

1.  `git pair check --json` — merge only when `.ready and .integrating`. `ready` alone merges work the moment
    a reviewer approves it; `integrating` alone merges a request whose approval has since been rewritten,
    withdrawn, or answered.
2.  With `--require-ci`: prove the head green (below).
3.  `git merge --no-ff <declared head>` into the destination the **queue** names.
4.  `git push` the destination — the landing exists when the branch says it does, and there is nothing to
    run afterwards: the destination carrying `changesets/<id>/` is the record.

Three details a first attempt usually gets the wrong way round:

-   **The merge commit is the landing**, the commit that added `changesets/<id>/` to the destination — which is
    the destination's *new* tip, not the tip it had before the merge. Nothing has to name it; `status` reads it
    out of the destination's history when somebody asks.
-   **The reviewed head is the head that was declared**, which `check --json` prints as `.head`: the declaration
    commit itself, whose history carries the approval underneath it. That is what the merge carries, and what
    makes the landed chain readable afterwards.
-   **The destination comes from `queue --json`'s `awaiting_integration[].destination`,** not from `base:`.
    For the child of a landed parent, `base:` is a commit rather than a branch anything can merge into.

What the job will not do: merge a change the gate refuses (it says so and exits 0, because a job triggered by
an event usually runs before anybody has declared anything, and a red build for "not yet" teaches nobody
anything); merge a head it cannot prove green; leave a conflicted merge in the tree (`git merge --abort`,
exit 1); call `change integrate` itself, since a pipeline that writes
the declaration is asking for its own merge; or force anything.

### Green before the merge

Reviewed and handed over is git-pair's answer. Whether the branch's own tests pass is a second question, and
it belongs to the forge, so it is asked there:

```bash
scripts/ci/git-pair-integrate.sh --require-ci --expect-head "$TESTED_SHA" feat/my-branch
```

-   `--require-ci` consults a probe before merging, and merges on a yes only. A probe is any command: it is
    handed the sha, prints one line of reason, and exits `0` green, `1` not green, `2` cannot tell. The
    default is `scripts/ci/gh-head-green.sh`, which reads every check run and commit status GitHub reports
    for that commit — not branch protection's list of *required* checks, because "everything GitHub can see
    is green" is one rule instead of two, and a repository whose required set is narrower passes its own
    `--ci-probe`.
-   `2` is not `0`. A head with nothing running for it has not been tested, so it is not merged; the poll
    asks again after the next run. The replay covers all three answers.
-   `--expect-head <sha>` names the commit the trigger was told CI finished with, so a branch that moved
    while the job was starting is skipped rather than merged on the strength of a run that tested other
    work. It also fixes *which* commit the probe is asked about: the declared head, never whatever the branch
    tip became in the meantime.
-   The workflow triggers on `workflow_run` — the test workflow completing — and on the schedule, not on
    `push`. A merge job that ran on the feature branch would put its own check run on the very commit it has
    to certify, and then wait for itself.

The guarantee is about the head, not about the merge. `main` runs its own CI after the push; a repository
that wants the *merge result* proven before it lands wants a merge queue, which is a different mechanism and
outside this example.

From one run of the replay:

```text
### flow/merge
flow-merge: declared by ae51ffb for merge into main
Merge made by the 'ort' strategy.
  merged as 346a18a
  flow-merge: merged
```

What the runner has to provide: `contents: write` for the merge commit, `checks: read` and
`statuses: read` for the probe (a token that cannot read them answers "cannot tell", and the job merges
nothing on that answer, so a missing scope stalls it rather than failing it), `fetch-depth: 0` (the gate
reads the approval out of history, and a shallow checkout cannot see it), and the integration branch named on
every call: `--default-branch <ref>`, because a checkout that fetched one branch has no recorded remote
default to compare against. `GIT_PAIR_DEFAULT_BRANCH` is the same value for a whole process, and it is the
form the shipped merge job uses: it takes the variable from the `GIT_PAIR_BASE` repository variable (default
`main`) and every `git pair` the job runs gets that destination, including one added to the step later,
without the flag being threaded through each call. The flag wins where both are given, and a run that took
its destination from the environment says so on stderr and reports `default_branch_source: "env"`. And the
destination branch has to accept the push: `GITHUB_TOKEN`
cannot be a branch-protection bypass actor, so an unprotected trunk works as-is and a protected one needs a
Ruleset bypass actor of its own (a GitHub App or a deploy key) with its token on the push remote.

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

The wheel scrolls the preview, when the terminal is willing to report it. The session asks for wheel
events on the strength of `git-pair.mouse`, which is on unless you turn it off with
`git config git-pair.mouse false` — the cost is not nothing, since a terminal reporting the wheel
to the program stops using click-drag to select text until you hold Shift. Two facts about where
the wheel comes from are worth knowing before concluding the setting did nothing. Under tmux,
wheel events reach a program only with `set -g mouse on`; and tmux hands the wheel to whatever is
on the alternate screen, which is where a session lives, so a program that never asked for the
mouse is not preserving your scrollback, it is dropping the events. Inside an editor or difftool a
handoff opened, scrolling is that program's own decision: git-pair releases the mouse before the
child starts and asks for it back when the child exits, and vim rolls its windows only with
`set mouse=a` set in vim.

`git pair review` is an orchestration screen, not an editor (`git pair review open` is the same command
under a longer name). What the review is made of is at the top, the changed files and their marks below it,
and the reviewed counter under those:

```text
╭ tuishow ─────────────────────────────╮
│ base   main                          │
│ span   unreviewed ▸                  │
│ ABOUT.md                             │
│ ▾ Threads                            │
│     does-the-lock-cover-the-map.md   │
│     + new thread…                    │
╰──────────────────────────────────────╯
═══════════════════════════════════════
▾ ○ src/
  ▾ ○ ui/
        ✱ picker.ts
    ○ a.ts +
═══════════════════════════════════════
0 / 2 reviewed
────────────────────────────────────────
j/k move  gg/G ends  ctrl-d/u half page  ctrl-f/b page  h/l fold  c fold all  enter open  d diff
space reviewed  e edit  a about  t threads  T new thread  v spans  V picker  s submit  p preview
z full  f files  tab preview  q quit
```

The box's left column is one width for its three labels, so the values under them line up. The base row
names what the span is measured against, and it names a ref rather than a commit — including for the child of
a parent that has landed, where the measurement starts at the run the child shares with the destination and
the destination is the name you can read. The line under it says what that name cannot: `parent reporting
landed`. It is a line of the frame rather than a row you press, and it appears only for a changeset in that
shape, so no other box pays a row for it — and it is how a parent that landed is told apart from a parent
whose branch simply vanished, which read the same in the base row.

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
Each level is indented four cells. A row spends two cells on its mark gutter before its name, and a
directory two more on its fold arrow — but those two come out of the indent rather than sitting after it,
so at any depth below the top a directory and the files beside it name themselves in one column, which is
what a flat list with arrows in it is supposed to look like. A child's name sits four cells right of the row
holding it, and two under a top-level directory, which has no indent to spend its arrow on.
`h` and `l` fold and unfold the directory under the cursor — arrows do the same, and `Enter` does
both, the way it does for the thread heading — and `c` folds the whole tree and opens it again,
which is how a changeset of a hundred files gets read for shape before it gets read for detail.
Folding a directory that was hiding the cursor leaves the cursor on the directory, not on whatever
row its old index now points at.

One character goes after a file's name for what the span did to that file. git's own status answers it: `+`
for a file the span created, `-` for one it deleted, `~` for one it moved. No sign means the span only changed
the file. That is what a span usually does, so a sign marks the exception a reviewer came to find.

The character is coloured, and it sits after the name the way a directory's count sits after its name. It is
a fact about the file rather than part of its name, and colour carries that where faint used to. Each sign
wears the colour git's own diff wears for the same fact — green for a file the span created, red for one it
deleted, blue for one it moved — so the tree and the diff column beside it say one thing in one colour. The
name of a deleted file goes faint with its red sign, because that is the one row whose subject is not there to
read. A terminal with no colour to give gets the character alone, which is what every terminal got before.
The move is git's rename detection, so a repository with `diff.renames` off gets `-` and `+` for the pair git
called two files.

A row also says when the working tree holds a change the commit under review does not: a file written into
since the span ended, or a folded directory hiding one. No span can say it, because both of its ends are
commits, and the pane already prints those bytes under a caption naming who wrote them. The name of such a
row goes magenta — the colour this screen already spends on what you wrote — and bold with it, so a terminal
with no colour to give still sees the difference, and the gutter wears `✱` wherever the row is not reviewed
all the way through. A file you have both marked read and written into keeps its tick: the tick is what the
reviewed counter counts, and the name is what says the change is yours. Only a *folded* directory wears it;
open, its own children say it, and the `2/7` count beside its name goes on saying what has been read. Git
answers the question — `status --porcelain -z --untracked-files=all`, so staged, unstaged and untracked all
count and a name with a space in it still prints as itself — and a historical span shows none of it, because
the working tree is not inside a span that ended at a commit. The list is re-read when the span changes, when
an editor or difftool hands the terminal back, and when you press `r`.

A file you changed that this changeset never touched is on the tree as well, in path order beside the files it
did. It is in no span, so it is in no diff of the span, and a change of yours with no row is a change you come
back for and cannot find: `git status` says the path is uncommitted, so the tree holds a place for it — the same
magenta name, the same `✱`, and none of the counts. The reviewed counter says how much of the *changeset* you
have read, so neither half of it moves for a file with no patch here to have read, and `Space` says so rather
than tick a row the counter will not count. A directory that exists only because you wrote something under it
carries no mark and no count either, for the same reason. Files git was told to ignore stay off the list, since
git's own status is the answer, and a historical span shows none of these rows: the working tree is not inside a
span that ended at a commit.

A file you created — in no commit and no index entry — is the one case that needs more than a row, because
`git diff <rev> -- <path>` compares only what git tracks and prints nothing at all for a path it has never seen.
The pane asks git the other question (`--no-index` against `/dev/null`, which is how git itself writes the empty
side of a created file) and prints the whole file as the addition, and `Enter` opens it in the editor rather than
in a difftool that would open a window on nothing. A file you edited that git does know keeps the difftool:
there the comparison is your typing against the revision under review. `d` still means "ask git for the diff",
whatever the row is, and a directory holding only files git has never seen says so rather than showing a blank
column beside rows that do not.

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
The wheel does the same job without the moving into it: one notch is three rows of the diff on
screen, wherever the pointer is standing — over the list, over the divider, over the overlay —
because the pane is what you scroll while reading, and a pointer that has to be held over a column
to move it is a second thing to aim at. Nothing else comes from the mouse: a click marks nothing.
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
presets; `Esc` leaves the span exactly as it was. Each column is one timeline rather than two lists:
the review submissions — the newest as `Last Review`, then `Review -2` and `Review -3`, older ones by
the index you would type — with the changeset's own commits written between them where they happened,
so walking `j` from `Last Review` reaches the commits that followed it. That is usually the question
this screen is opened to answer, and it reads better as one line than as two lists you have to
transpose in your head. The commits are the ones the base does not already hold, and only the ones
that change files: a marker commit holds no content to review, and it is on the list by alias already.
A merge you made to catch the branch up with the base is one of them too — read against its first
parent, it names the files it brought in — because that merge is where history you did not write
enters `last review..current`, and the row is what you set as the boundary to keep it out. The commits
that arrived with it are not rows: the base holds them.
Only the base offers the changeset base, and only the head offers `Current`
(the live end, labelled `latest + edits`). `HEAD` appears nowhere in either list: beside `Current` it
would be two similar-looking live targets when only one of them can be edited. Wider history is `c`
and `r`, which open a commit and a ref drill for whichever column is active. They are keys rather
than rows because pointed at a row, `Enter` — the key you press on the thing you are pointing at —
applied the pair you had come to the screen to change instead of descending into the drill.

The commit drill is a searchable list of subjects and short ids that also takes a typed
revision, so history past the window the columns read is one keystroke away, and it refuses an id git does not know while
the list is still on screen. The ref drill groups branches, remote refs, tags and other refs under headings,
showing `main` and `origin/main` while the checkpoint keeps `refs/heads/main` — a branch and a tag
with the same name are two different choices, and drift has to be watched on the one you meant.

Inside either drill the keys are in one of two modes, and the shortcut bar names the keys of the mode
you are in. The list has them first — `j`/`k`, `gg`/`G`, `d`/`u` or `ctrl-d`/`ctrl-u` for half a page,
and `f`/`b` or `ctrl-f`/`ctrl-b` for a whole one — with `Space` or `Enter` picking and `esc` or `q`
stepping back to the
columns. `/` puts the keys on the filter instead, starting a fresh one as `less` does, because the text
you are replacing is the reason you typed `/`; there what you type is the filter — a space included,
since `response 0` is a thing to search for — with `Enter` picking and `esc` handing the keys back with
the filter kept, so the rows you filtered to are still the rows you can walk. The block after the
filter is the caret, so it is
where your typing goes; navigation mode drops it. `Backspace` deletes a character and nothing else —
with an empty filter it does nothing, because it used to throw the whole drill away. A drill also says
which end it is choosing for (`for BASE`), since the columns that would otherwise say it are off screen.
A checkpoint the timeline has no row for — a ref, or a commit older than the window the columns read —
gets a row of its own at the bottom of the column, wearing the asterisk, and `V` reopens with the
cursor there: with the drills as keys rather than rows, that row is the only thing left that can say
which end holds the pick. A short terminal shrinks the candidate lists
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
whose diff changed stop applying, and the count in the report says how many. It re-reads the working tree
whether or not anything moved, because the tree is the other half of what the screen compares and an edit
you make in another window sends this one no message. Ignoring the banner is a legitimate answer too — the
span does not move until asked. The warning is derived from the span rather
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

The `ABOUT.md` scaffold is the repository's to choose. `git pair init`, `git pair review about`, and
the check that reports an author who never described the change all read
`.git-pair/about-template.md` when the repository has one, and the `Summary`, `What changed`,
`Design decisions`, `Validation`, `Known limitations` and `Open questions` headings above otherwise.
`{{slug}}` is replaced with the changeset id wherever it appears, so the title can be `# {{slug}}` or
`# Booking locks in {{slug}}`. Commit the file, because the value of a template is that the next
checkout starts from it; `init` never commits it, since it is an input to a changeset rather than part
of one, and an author can try one out before staging it.

Two decisions are worth stating, because both were taken to keep a mistake visible. A template file
that is empty or whitespace-only counts as no template at all: the built-in scaffold is written, and
`change ready` then says the file is still empty, rather than the reviewer being handed a blank
`ABOUT.md` with no way to tell whose setting failed. A file that exists and cannot be read is an error
that names it, not a quiet return to the built-in — an author who committed a template expects their
template. And a repository that has adopted a template still gets the warning for a changeset holding
the built-in scaffold, which was written before the template arrived and is just as undescribed.

## Development

`mise run check` is the gate: gofmt, `go vet`, then the Go test suite. Run it before you call a
change done.

The suite runs in several `go test` processes (`scripts/test-sharded.sh`) rather than one. Most of a
test's span is a git subprocess, and one process cannot overlap them: `internal/cli` and
`internal/tui` drive the product in-process through the process working directory and the process
standard streams, so their tests take turns. Splitting a package's tests across processes by name
keeps each process single-threaded exactly as it is today, and reclaims the overlap. `mise run
test:serial` is the same suite in one process, with the output `go test ./...` gives.

Three scripted replays sit above it. Each one runs the installed binary as a subprocess, so it reaches
what a Go test cannot:

| Gate | What it proves | Needs |
| --- | --- | --- |
| `scripts/gates/e2e-29.sh` | The PRD §29 loop end to end in a scratch repo: review, approve, `check`, a merge into the destination and a merge into a release line, `LANDED UNREVIEWED`, and `change tidy` — including that a filing ends the report only once it reaches the destination | `git` |
| `scripts/gates/pty-walkthrough.sh` | The review TUI under a real pty: first paint, the file tree, marks, the span walk, the difftool handoff | `git`, `python3` |
| `scripts/gates/ci-integrate.sh` | The CI merge job against scratch bare remotes: the gate, the merge, the push, and every refusal in between | `git`, `jq` |
| `scripts/gates/install-sh.sh` | The release is installable: `packaging/install.sh` against a `dist/` directory served over http — the newest release, a pinned version, a release that is not there, a tampered archive, an archive missing from `checksums.txt`, a file in the way, a directory in the way, an unwritable directory, and a machine nothing is published for | `git`, `python3`, `curl` |

`mise run gates` builds the binary, then runs the first three. Each also takes a binary path as its first
argument. A pipeline that installs the build elsewhere passes its own path.

Those three resolve the default binary by the rule `mise run build` uses. A branch installs and tests
its own namespaced name, rather than a build another worktree left behind. See
`scripts/install-name.sh`. `scripts/gates/install-sh.sh` takes a `dist/` directory instead, and defaults to
the one `mise run release:snapshot` writes.

`.github/workflows/ci.yml` runs `mise run gates` on every push and pull request — one line, because the
task is the definition. `.github/workflows/git-pair-integrate.yml` then triggers on that workflow finishing
and performs the landing of a declared changeset (§Landing a declared change from CI).

The installer replay is not in `gates`. That ladder is what every change pays for, and the replay needs
goreleaser, a cross-compile of four targets, and a listening socket — none of which matters until a tag is
cut. `.github/workflows/release.yml` runs it between the build and the publish, which is the point where it
pays: a release already published is a release people can install.

Cutting a release is a tag: `git tag v0.2.0 && git push origin v0.2.0`. That workflow builds the four
published targets, replays the installer against them, and publishes only if the replay passed — which is
why it builds twice. Everything short of the tag has a task, so a release fails on a laptop rather than
four minutes later on a runner with a version already spent:

| Task | What it checks |
| --- | --- |
| `mise run release:check` | `.goreleaser.yaml` against the pinned goreleaser; `sh -n` on the installer; the `release.extra_files` globs resolving, which snapshot mode does not check; the release URL agreeing across `packaging/install.sh`, README and the release header; `packaging/THIRD-PARTY-NOTICES.md` matching the linked dependencies |
| `mise run release:snapshot` | the four archives and `checksums.txt` into `dist/`, with nothing published |
| `mise run release:gate` | the installer replay above, against whatever is in `dist/` |

Work here is planned in `docs/plans/<name>/plan.md`, and a finished plan moves to
`docs/plans/completed/`. Each change carries its own directory under `changesets/`. The tool reviews
itself. `git pair change ready` offers the change, and `git pair review open` opens it for a
reviewer.

## Troubleshooting

`changeset booking is not landed on main, so there is nothing to move` (exit 1, `change tidy`) — the move
follows the landing, and the landing is a read of the destination's tree. `git pair status --changeset booking`
says where it stands; if the work reached a branch git-pair is not calling the destination, that is the
finding, and the remedy is to name the destination (`--default-branch <ref>`, or the changeset's `base:`), not
to force the move.

A tidy that moves a parent whose child is still open prints `note: the changeset on booking-follow-up still
records it as its base; the move is safe, and the child's diff is measured against a commit`. It is a note and not
a refusal, because the child is measured against a commit rather than a path, and both spellings of a landed
directory are read: the child keeps its base, its state, and its span, and a child that edited the parent's own
record sees that edit reported at `changesets/.landed/booking/ABOUT.md`.

`changeset booking is landed and this branch does not carry changesets/booking: nothing here to move — run
`git pair change tidy` on the branch that still has it` (exit 1) — the move is a commit on *this* branch, so
it needs the directory here. A clone that has only trunk says this even though the changeset landed.

`your working tree has changes outside this command; commit or stash them before tidying` (exit 1) — the
rename commit must contain renames and nothing else.

`LANDED UNREVIEWED` in `queue`, or the same heading in `status` on a branch with no changeset of its own
(exit 2) — a directory is in the destination, and the destination holds no approval of what it carries. The
`reason` says which of three readings applies. `the landing carried the directory in one commit, and the chain
carries no review markers` is a squash or a cherry-pick: the content arrived and the history stayed on a
branch this clone can no longer reach. That sentence is a pair, and both halves come from the landing rather
than from where the destination's tip has since moved to, so it does not turn into the next one when an
unrelated commit lands. `the chain carries no review verdict`, or a newest verdict of feedback
or a block, is a merge that went in without an approval. `the approval (<sha>) names <commit>, which the
destination does not carry` is the strict one: the landing replayed the run, so what the destination
records as approved is not what the destination holds. None of them has a command that closes it — there is
nothing to write — so read the changeset with `git pair status --changeset <id>` and decide whether the
review happened somewhere the destination cannot see. The report does have an end, and it is a commit: once a
`change tidy` moves the directory into `changesets/.landed/` on the destination, the changeset is out of the
heading, because somebody reviewed a commit that filed the record away (§12). A tidy on an unmerged branch
does nothing of the kind — the destination carries the directory in place until the move arrives — and
`git pair status --changeset <id>` keeps answering `reviewed: false` either way. `check` refuses to call an unreviewed head integration-ready; a landing it cannot see is a merge it was
never asked about.

`cannot tell which branch is the integration branch` (exit 2) — the destination is what every landing question
is asked against, and this checkout cannot name one. `--default-branch <ref>` answers it directly;
a `--single-branch` clone with no `origin/HEAD` is the usual cause.

`cannot resolve changeset base "main": unknown revision: main` (exit 1) — the `base` in
`CHANGESET.yaml` is not a ref in this repository (renamed trunk, fresh clone, merged stack).
git-pair never guesses a base: edit the file, or `git pair init --base <ref> --set-base`.
From `init` with no `--base`: `cannot infer a base: no main or master branch exists;
pass --base <ref>` (exit 2).

`base:` is written as a branch name — `main`, not `refs/remotes/origin/main` — because `git clone` records
the remote's default branch in `refs/remotes/origin/HEAD` and that is the answer `DefaultBranch` prefers,
so the ref it reaches is usually a fetch ref even in a clone with a local trunk. The file is read on machines
that have fetched different things, so the name is what belongs in it. Where the name resolves to nothing — a
clone holding the integration branch only under the fetch root — the qualified ref is recorded instead,
because a base that does not resolve fails every command.

What a diff is measured against is a second question, and where the base names the integration branch it is
answered with the fetched copy. A bare name resolves under `refs/heads/` first, and `git fetch` moves
`refs/remotes/origin/main` without touching the local branch, so the local copy is where trunk stood the day
the branch was cut — measuring from it puts work the destination already has into this changeset's diff.
`git pair status` prints the base it measured against (`Base: origin/main — the base names the integration
branch, so the copy of it this clone has fetched`, with `base_ref` and `base_why` in `--json`); `base` stays
the recorded name, and a stack's parent branch is left alone. Where this clone has no fetch ref, the recorded
name is the whole answer. To measure from the local copy instead, name it: `--default-branch main` (or
`refs/heads/main`) states which ref *is* the integration branch for that command, so the diff and the landed
test move together — one knob, one answer. A single span can be pinned to any ref without touching either:
`git pair diff --base-ref=main`. Where the local copy of that branch and its remote copy are different commits,
`init` says so and gives the counts, because measured from origin's copy what only this clone's trunk has
reads as part of the change until it is pushed: `note: main is not the same commit here and on origin: 1 here
that origin does not have, 0 on origin that is not here`. It is a note; pushing trunk is yours.

`cannot tell which branch is the integration branch: ...` (exit 2) — there is nothing to compare
against, so "has this landed?" has no answer and every changeset directory on the revision would
look like work in progress. Pass `--default-branch origin/main` (a CI job that fetched one branch
has no recorded remote HEAD, and a repository may name trunk something else), set `GIT_PAIR_DEFAULT_BRANCH`
to the same ref for a whole job, or record git's own answer once with `git remote set-head origin --auto`.
`init` reports the same problem as
`cannot infer a base: ...; pass --base <ref>`, because the recorded base and the landed test come
from the same resolution. In CI the missing answer is usually the checkout: a job that fetched one
branch has neither a remote HEAD nor `origin/main` to compare against, which says nothing about the
changeset it was asked about — the refusal names the fetch shape
(`git fetch origin '<branch>:refs/remotes/origin/<branch>'`) beside the flag.

```text
this revision contains more than one changeset: aaa and bbb; name the one you mean with
--changeset <id>, or record the shape with `git pair change stack --base <branch>` or
`git pair change combine --into <id>`
```

`git pair change stack --base <branch>` records the link when the directory beside yours is the work yours sits on
top of; `git pair change combine --into <id>` folds the two when they are the same work under two names.

(exit 2) — the branch carries two unlanded changeset directories and nothing in the branch's own
history orders them: no commit touched one directory more recently than the other, and neither names the
other as its `base:`. That is what a sibling merged in looks like, and what a branch created off a sibling looks
like once it starts its own work. `git pair change stack --base <sibling>` settles it for the branch, by recording
the base branch and the changeset on it in the child's `CHANGESET.yaml`; `--changeset <id>` answers for one command;
and `git pair init --base <sibling>` at creation time records the stack from the start.

`main is the integration branch, so a changeset started on it can never contain anything` (exit 2
from `init`) — a changeset is measured against the integration branch, so one started on it
can never contain anything. `git switch -c <branch>` first.

A changeset that reads as uninitialised on its own branch, or that has vanished from
`queue`, usually means its directory reached the integration branch — commonly because a
sibling merged your unlanded branch and *that* landed. Your directory is in trunk's tree, which is
precisely what the rule tests, so the cure is to land your own branch rather than someone else's
merge of it. `git ls-tree <integration-branch> changesets/` shows whether the directory is there — and
when it is, `queue` does not leave you to work out what came with it: it prints the changeset under
`LANDED UNREVIEWED` unless the chain behind it carries an approval of the commits that arrived, or unless the
destination carries the directory filed away under `changesets/.landed/` (§12).

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
block; surviving `ABOUT.md` and thread text is listed as non-blocking. Resolving a blocking
addition means applying it and then removing the reviewer's inline comment; an edit the author keeps
is what the flag is for (PRD §19.5). It is the only hatch left:
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

A fourth bullet, `changeset is already landed at …`, is not a problem to fix: the integration branch holds
the directory, so the review is over, and re-running the gate after a landing reports that rather than a
second opinion. A merge into a branch that is *not* the integration branch produces no such bullet, and the
work stays offerable: the release line can revert the merge, and the changeset may still have to reach the
integration branch on its own.
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
integration branch carries, with no approval it can show, is work that reached the destination without a
review licensing it, and `queue` gives it its own `LANDED UNREVIEWED` heading; `git pair status`
on a branch carrying no changeset of its own says the same inside its exit-2 answer. The heading prints a
read (`git pair status --changeset <id>`) rather than a command, because no command closes it: the reasons it
prints — a chain that carries no verdict, a landing that carried the directory in one commit and kept none of
the history, and an approval naming a commit the destination does not hold — are all facts about git's
history, read from the landing and the chain it brought rather than from where the destination's tip happens
to be, and only the first is a complaint about the review. One act ends the report, and it is a commit with a
reviewer behind it rather than an invocation: `change tidy`, once the destination carries it (§12).

`reviewed` and `check` ask the same question of the same trailer, which is the only reason the two surfaces
agree. `check` asks whether an approval still licenses the branch in front of it, and refuses a live branch
whose approval names a commit the branch no longer holds. `reviewed` asks whether the destination holds an
approval of what the destination holds, and fails a landing whose approval names a commit the destination
never received — the run was replayed between the approval and the landing, by a rebase merge or by the
author rewriting under an approval. Those two replays leave identical commits behind, so nothing in the
destination separates the merge that did the rewriting from the author who rewrote and was merged anyway;
the strict answer covers both, and it is also why the answer cannot be taken before the merge, when the
difference was still visible. One false finding comes with it, and it is worth naming: an approval written
before `Review-Head` existed names no commit, so `reviewed` says false for work that was reviewed. Two
changesets in this repository's own trunk are in that shape.

A hand-written ready marker counts only if `Review-State: ready` and `Review-Changeset: <slug>`
sit in a real trailer block, separated from the subject by a blank line and from each other by
no blank line.

A head whose newest review does not permit integration is `check`'s problem, and it reports it as a
`NOT READY:` bullet with exit 1 — the gate working, and every other failed condition in the same run.
A changeset the integration branch holds is refused by the commands that would move it instead:
`change ready`, `change unready`, `change abandon` and `review submit` all say
`changeset <id> is landed on <branch> at changesets/<id>, so git-pair records nothing further for it`
(exit 1) and write nothing.

## License

MIT — see [LICENSE](LICENSE). The published binary is a statically linked Go binary, so each release also
carries `THIRD-PARTY-NOTICES.md`: every dependency linked into it, with the licence text each is
distributed under. It is generated by `scripts/gen-third-party-notices.sh` from the module cache, and
`mise run release:check` regenerates it and fails if the committed copy no longer matches the dependency
graph.
