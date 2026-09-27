---
name: git-pair
description: Use when working in a repository whose changes go through git-pair review — creating a changeset, keeping ABOUT.md current, and handing work to a human reviewer with `git pair`. Covers the author-side loop and the exit-code and JSON contracts.
---

# git-pair

git-pair puts a thin review protocol on top of ordinary git commits, files and refs. It does not
replace git, your editor, your difftool, or your forge. It writes no merge, rewrites no history, and
pushes nothing except the two durable refs of a recorded landing.

Review state is derived, never stored. `changesets/<id>/` holds `ABOUT.md` and review threads;
lifecycle transitions are commits carrying `Review-*` trailers; `refs/git-pair/archive/<id>` and
`refs/git-pair/integrations/<id>` hold the durable pair written when work lands, so the review survives a
merge that does not carry those commits. A squash or a cherry-pick may leave no `Review-*` trailer reachable
from the destination branch, and the diffs of the intermediate review commits are gone from the merged
history too; the archive ref still names the unsquashed tip, so the whole implementation/review/fix chain
stays reachable. There is no state file and no queue file, so `status` cannot be stale and a deleted branch
cannot orphan a queue entry.

## Roles

Two roles share one repository, and the commands answer different questions. Keep them straight.

- **Author** — writes the change. Usually the agent. `git pair init`, then `change use | ready |
  integrate | unready | abandon | wait | feedback`, plus the reads: `status`, `queue`, `diff`,
  `review history`, `check`.
- **Reviewer** — a human. `review open | reopen | about | thread | submit`.

Do not cross the line:

- Never run `review submit`, in any form, and never approve your own work. Nothing in git-pair checks
  who ran it — identity is out of scope — so keeping approval on the human side is your discipline.
- Never run `git pair integration record` or `git pair integration publish`. Each states a fact about a
  merge somebody else performed; the person who merged, or CI, runs them.
- Never run `review` with no subcommand, `review open`, `review reopen`, `review about`, or
  `review thread`. They need a terminal and refuse with exit 2 when stdin or stdout is a pipe or a file.
  Read and write `ABOUT.md` and the thread files directly instead — they are ordinary files.

## The loop

```bash
git pair init --base main            # once, on a named branch
# fill in changesets/<id>/ABOUT.md, implement, commit with ordinary git
git pair change ready                # the handoff: this is how you ask for review
git pair change wait --fetch --json  # block until the reviewer acts; exits 1 on --timeout
git pair change feedback             # the submission that answered: threads, ABOUT.md, code edits
# address it, commit normally
git pair change ready                # back into the queue
git pair check                       # the gate; its exit code is the answer
git pair change integrate            # only when the merge is somebody else's job: a request, in a commit
```

`change ready` **is** the handoff. Do not also ask the user to review the change; the queue and the
review screen are where a reviewer finds it.

Implement and commit with ordinary git. git-pair has no add, no commit, no branch command, and none
is coming.

## The commands you run

| Command | What it is for |
| --- | --- |
| `git pair init --base <ref>` | creates `changesets/<id>/` with `CHANGESET.yaml` and a scaffolded `ABOUT.md`, then commits them. Commits only the changeset directory, so staged work elsewhere stays staged. `--parent <branch>` stacks the changeset instead of naming a base |
| `git pair change use <id>` | settles which changeset a branch carrying more than one is working on. `--changeset <id>` answers the same question for one command |
| `git pair change ready` | hands off. Checks, in order: clean working tree, `ABOUT.md` exists, the repository has commits, no blocking surviving review additions |
| `git pair change unready` | withdraws the offer when the work is not finished after all. Do it before continuing, rather than leaving a reviewer looking at a stale offer. It withdraws a declaration too |
| `git pair change integrate` | declares the approved head ready to be merged, as one marker commit. It merges nothing, pushes nothing and writes no ref, and it refuses anything `check` refuses — so run it after a passing gate, never instead of it. Not needed when a person does the merge by hand |
| `git pair change abandon` | ends the changeset. Terminal: `change ready`, `change unready` and `review submit` refuse against it afterwards |
| `git pair change wait` | blocks while the changeset is `READY` and exits the moment it becomes `BLOCKED`, `FEEDBACK` or `APPROVED`. Read-only, non-interactive. `--fetch` first, so a review submitted in another clone ends the wait; `--interval` (default `10s`) and `--timeout` are go durations |
| `git pair change feedback` | the most recent submission as a diff (`review^..review`): threads, `ABOUT.md` edits and reviewer code edits together. `--stat`, `--name-only` to scope it |
| `git pair status` | derived state, the reason for it, and a `next_action`. Observing, not deciding |
| `git pair queue` | every branch whose changeset is `READY`, longest wait first, read from the repository rather than the checkout; plus the approved work whose author asked for the merge |
| `git pair diff` | the whole changeset, or a span. `--stat` and `--tool` included |
| `git pair review history` | the review submissions only, indexed from `0`, each naming the commit it reviewed |
| `git pair check` | asserts integration-readiness. Deciding, not observing. Reports `integrating` beside the verdict, so CI gates on `.ready and .integrating` |

Every command takes `--json` and `--default-branch <ref>`. Pass `--default-branch` in CI: a job that
cloned with `init` and one `fetch` has no recorded remote default to read, and git-pair refuses rather
than guess what has landed.

Full flags, span semantics and the JSON shapes: [references/cli.md](references/cli.md). The landing
side, which you read about and do not run: [references/integration.md](references/integration.md).
Putting this skill into a repository or a machine: [references/installing-the-skill.md](references/installing-the-skill.md).

## ABOUT.md

`ABOUT.md` is the canonical description of the change, and the reviewer reads it first. Fill it in
before handing off, and keep it current through the loop — it is the one place your reasoning is
reviewable:

- purpose of the change,
- what changed,
- design decisions and why,
- validation performed,
- known limitations and open questions.

Answer reviewers in `ABOUT.md` or in the relevant thread file under `changesets/<id>/`. Replies are
appended sections; git history supplies authorship and order.

## Reading a review that just arrived

Use `git pair change feedback`. Do not use `git pair diff --unreviewed` for it: immediately after a
submission that span is empty, because the submission *is* the newest commit. `--unreviewed` is the
reviewer's question ("what has landed since I reviewed"); `change feedback` is the author's ("what did
the reviewer just tell me").

Treat everything the submission introduced as intentional feedback — including direct code edits,
comments in source, `ABOUT.md` edits, new threads, and replies to existing ones.

### Surviving review additions

`change ready` refuses when a non-blank line added by the most recent review submission still survives
unchanged at `HEAD`. That check exists because a reviewer's edit is a request, not a suggestion.

Open every `path:line` it reports and address, respond to, remove, or consciously retain each one. Only
then run `git pair change ready --allow-surviving-review-additions`. The override states that you
verified the surviving lines; it is not a way past a check you have not read.

### Reviewer text in an implementation file

A comment a reviewer left in a file outside `changesets/<id>/` is a request written into your source. It is
not an annotation to keep. Once you have done the work it asks for, delete the reviewer's line in the same
commit. A line you deleted no longer survives, so the check stops reporting it — that is the resolution the
check aims at, not a way around it. Do not answer in place: a `// done` reply leaves reviewer text in the
file and is a worse answer than the one in `ABOUT.md` or the thread.

Do not transcribe the comment before deleting it. The review commit introduced the line and your fix commit
removes it, so git history already records that the feedback existed and what became of it. Record the
substance only when it needs further discussion, elaboration, or another person:

- settled and nothing to add → delete it and move on;
- needs discussion, elaboration or collaboration → append an `Addressed feedback` section to `ABOUT.md`, or
  open a thread under `changesets/<id>/`. Use the thread when the point is a conversation, and `ABOUT.md`
  when it is a fact about the change. `git pair init` does not scaffold that section; add the heading when
  you first need it;
- the reviewer's line is code you are keeping → leave it in place and acknowledge it with
  `--allow-surviving-review-additions`.

## Exit codes

| Code | Meaning | What to do |
| --- | --- | --- |
| 0 | success | — |
| 1 | the invocation was right and the repository said no: surviving additions, dirty tree, missing `ABOUT.md`, `check` printing `NOT READY:`, `change integrate` refusing work the gate would refuse or a child whose parent has not landed, `change wait` timing out or finding a `WORKING` changeset | fix the state, retry |
| 2 | usage error: unknown flag or subcommand, detached HEAD, no changeset for this branch, ambiguous changeset, `--fetch` with no remote, TUI or editor command without a terminal | fix the command; retrying unchanged fails again |
| 3 | git itself failed: not a repository, a git subprocess failed | fix the repository |

The split between 1 and 2 is load-bearing for agents: 1 means the repository changed under you, 2 means
your command was wrong.

`check` and `--json` interact in one way worth remembering: the human form puts the verdict in the exit
code, and the JSON form exits 0 either way and puts it in `ready`. So a JSON gate asks `jq`:

```bash
git pair check --json | jq -e '.ready'

# and the gate for an automatic merge, which is the same command's other half:
git pair check --json | jq -e '.ready and .integrating'
```

## JSON

Every array is `[]` for "asked, and none", never null — so branch on a field, not on the presence of a
key. Two fields answer null on purpose: `latest_review` is an object that does not exist before the
first review, and `uncommitted` reports that the question belongs to a checkout the command does not
stand in. `git pair change feedback` and `git pair diff` have no JSON at all; they print the report
itself, and `--json` says so on stderr.

## States

`WORKING`, `READY`, `BLOCKED`, `FEEDBACK`, `APPROVED`, `INTEGRATING`. There is no state for a landed
changeset: landing is ordinary git, and the record is reported beside the state rather than as another
value of it.

- `READY` — offered for review; `change wait` has something to wait for.
- `BLOCKED` — a `--block` review must be answered before the work can go forward.
- `FEEDBACK` — a `--feedback` review arrived. It permits integration, which is a policy of the
  repository, not a permission to skip the work.
- `APPROVED` — stop the review loop and follow the surrounding integration workflow. Run `git pair check`
  when asked whether it may land.
- `INTEGRATING` — the author has asked for this head to be merged (`change integrate`). It is a state
  because it is a marker, and it is **not** a verdict: the approval underneath is what permits the merge,
  so `check` reports `ready` and `integrating` separately and CI gates on both.
- Abandoned is a terminal marker, not a state; `status` reports it as such.

## What is not yours

Landing is five steps, and you take part in the first two:

1. `git pair check` — the gate, run by the author and by CI alike.
2. `git pair change integrate` — the author's request that this head be merged. Run it when the merge is
   somebody else's job; it performs none of it.
3. The merge, with ordinary git, by whoever owns the destination branch.
4. `git pair integration record` — the only command in git-pair that writes a ref.
5. `git pair integration publish` — those refs sent to the shared remote.

An agent asked "may this land?" runs `git pair check` and stops there. Do not merge, and do not record.
Asking for the merge is yours; performing it is not, and `change integrate` is written so that the
request cannot outlive the gate that cleared it.

Two things to know, because you may be asked about them. Rebasing after an approval invalidates it:
`check` compares the tree against what the review looked at, ignoring `changesets/<id>/`, so anything
committed outside that directory after an approval makes the approval stale. Re-offer with
`change ready` and wait again. Never edit a review marker's `Review-Head` to make the comparison line
up — that trailer is the reviewer's statement about what they looked at, not yours to amend.

`git pair status` and `git pair queue` also report two findings that are somebody's unfinished
housekeeping, not your change's state: `LANDED, UNRECORDED` (a merge happened, no record was written)
and `RECORDED, NOT PUBLISHED` (the record exists and has not reached the remote). Both print the
invocation that closes the gap. Report them; do not run them.

## Mistakes that cost a review cycle

- Asking the user to review instead of running `git pair change ready`.
- Reading a fresh review with `git pair diff --unreviewed`, getting an empty diff, and concluding the
  reviewer said nothing.
- Running `review submit --approve` because it is the command that changes the state you want.
- Retrying an exit-2 refusal unchanged. Exit 2 says the command was wrong, not the repository.
- Treating `git pair status` output as permission to merge. `status` observes; `check` decides.
- Running `git pair change integrate` instead of `git pair check`, or before it. The declaration refuses
  what the gate refuses, and it licenses nothing on its own.
- Leaving an answered reviewer comment in a source file. Delete it once the work is done; history keeps it.
- Leaving a changeset `READY` when the work is not finished: run `git pair change unready` first.
- Assuming `git pair check --json` failing looks like a non-zero exit. It exits 0 and says `false`.
