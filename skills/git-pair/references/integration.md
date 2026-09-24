# The landing contract, for an agent who does not run it

Landing is four steps, in this order, and nothing else. You take part in the first one and report on the
rest; the merge and the record belong to the person who owns the destination branch and to CI.

```bash
git pair check                       # 1. the gate — yours
# 2. the landing, with ordinary git: merge, squash-merge, or the forge button this repository uses
git pair integration record          # 3. the only command in git-pair that writes a ref
git pair integration publish         # 4. those refs sent to the shared remote, unforced
```

git-pair writes no merge and no ref at all while work is in flight. There is no `git pair merge`, no
`git pair land`, no `git pair push`, and none is coming: integration stays ordinary git, decided by the
human.

## 1. The gate you run

```bash
git pair check                # verdict in the exit code: 0 ready, 1 not ready
git pair check --json | jq -e '.ready'      # verdict in `ready`, exit 0 either way
git pair check --allow-feedback             # when this repository's policy says feedback is enough
```

`check` asks what a merge would act on: the newest marker is a review whose outcome permits integration,
nothing unreadable came after it, the changeset has not ended, it has not already been recorded as
integrated, and the tree still matches what the review looked at — ignoring `changesets/<id>/`. It reads
no other ref, and every failed condition is reported in one run.

It is the last step an agent runs. An agent asked "may this land?" runs `git pair check`, reports
`reasons`, and stops.

`check` accepts a `feedback` outcome only with `--allow-feedback`. Whether a non-blocking review is
enough to land is a policy of the repository, and that flag is the place it is stated — there is no
config key for it, because the command running the gate is the only place the policy is known.

## What invalidates an approval

- Any commit outside `changesets/<id>/` after the approval. The tree comparison is what `check` reports
  as `content outside changesets/<id>/ changed since <sha>`.
- A rebase, amend, or squash of reviewed history. `check` compares the commit the review named in its
  `Review-Head` trailer against this history and refuses when it is not an ancestor.

Recover by re-offering: `git pair change ready`, then `git pair change wait`. Never edit a review
marker's `Review-Head` to make the comparison line up — that trailer is the reviewer's statement about
what they looked at, not the author's to amend.

## 3-4. Record and publish, which you do not run

`git pair integration record` writes the durable pair for one changeset, create-only:

| Ref | Holds |
| --- | --- |
| `refs/git-pair/archive/<id>` | the real unsquashed, unrebased tip: implementation and review history together |
| `refs/git-pair/integrations/<id>` | the commit that introduced the changeset into the destination branch |

Those two refs are why a branch can be deleted without losing the review. They are written after the
merge, never before, and never moved: the same pair again is a no-op success, and a different pair is a
refusal rather than a race to win.

**Record before tidy.** The record is asked of the branch that still carries the reviewed head and the
changeset directory, so with the branch present it needs no flags at all. After the branch is gone it can
only be told (`--source` and `--commit`), and if the reviewed head was never pushed it cannot be derived
at all. Publish in the same breath: once the branch is deleted, the refs on the remote are the only copy
of the archive chain.

## 2 findings you will be asked about

Both are somebody's unfinished housekeeping, and both print the invocation that closes the gap. Report
them; do not run them.

`LANDED, UNRECORDED` — a `changesets/<id>/` directory is in the integration branch's tree and no
integration ref exists. The merge happened and step 3 did not. The tree rule makes this invisible in the
ordinary way: the directory is in trunk, so the rules that find work in progress stop seeing it, and the
branch may already be deleted. `git pair queue` prints it under its own heading and `git pair status`
prints it on a branch that carries no changeset of its own.

`RECORDED, NOT PUBLISHED` — this clone holds the pair and the remote, as last fetched, does not. Step 4
has not run, or has not reached here. `--fetch` is how a record written where the merge ran becomes
visible here; a record is a claim, and a clone acquires claims by asking.

Half a pair on the remote is the louder case: the remote has a hint and no way to reconstruct the record
from it. `status --json` reports that as `unpublished`, with `unpublished_note` when there was nothing to
compare against.

Neither finding changes `check`'s verdict. A record that has not travelled is a durability risk, not a bad
review.

## Reading a changeset that is no longer on a branch

`git pair status --changeset <id>` resolves a slug from whichever branch carries it, and falls back to
the durable refs when no branch does. That is the read for "what happened to that change", and it is
read-only: it reports `integrated`, `integrated_commit`, `integration_ref`, and `integrated_in_default_branch`
with the branch it measured against. A landed child whose parent branch has been tidied away gets its
`base:` relinked to the parent's integration ref, and `stack` walks the ancestors that remain.
