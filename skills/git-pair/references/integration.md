# The landing contract, for an agent who does not run it

Landing is three steps, in this order, and nothing else. You take part in the first two and report on the
last; the merge belongs to the person who owns the destination branch.

```bash
git pair check                       # 1. the gate — yours
git pair change integrate            # 2. the request that this head be merged — yours, when the merge is
                                     #    somebody else's job; skip it when a person merges by hand
# 3. the landing, with ordinary git: merge, squash-merge, or the forge button this repository uses
```

git-pair writes no merge and no ref at all. There is no `git pair merge`, no `git pair land`, no
`git pair push`, and none is coming: integration stays ordinary git, decided by the human.
`git pair change integrate` is not an exception to that sentence — it is a marker commit that asks for the
merge, and the merge is still performed by somebody else. What it changes is who waits: instead of an
author standing between an approval and the merge button, the request sits on the branch and whoever owns
the destination acts on it.

Nothing about a landing is written down separately either. A changeset is landed when the destination
branch carries `changesets/<id>/`, and that directory is the whole record. There is no fifth step to
forget, no ref to publish, and no configuration that makes the answer better.

## 1. The gate you run

```bash
git pair check                # verdict in the exit code: 0 ready, 1 not ready
git pair check --json | jq -e '.ready'      # verdict in `ready`, exit 0 either way
git pair check --allow-feedback             # when this repository's policy says feedback is enough
```

`check` asks what a merge would act on: the newest marker is a review whose outcome permits integration
(or a `change integrate` declaration standing on such a review, in which case the review underneath is the
verdict), nothing unreadable came after it, the changeset has not ended, the destination does not already
carry it, and the tree still matches what the review looked at — ignoring `changesets/<id>/`. It reads
branches and commits and nothing else, and every failed condition is reported in one run.

It reports `integrating` beside the verdict, which is why the gate for an automatic merge is one command
and two fields: `jq -e '.ready and .integrating'`. `ready` is "may this merge"; `integrating` is "did the
author ask for one", and it goes back to false the moment the work is re-offered, answered by a reviewer,
or withdrawn with `change unready` — a request is superseded, not erased, and a pipeline must not merge the
one the author just took back.

It is the last step an agent runs, unless the repository's flow asks for a declaration, which an agent may
run after a passing gate because the declaration refuses everything the gate refuses and merges nothing. An
agent asked "may this land?" runs `git pair check`, reports `reasons`, and stops.

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

## What survives a landing, and what does not

A merge landing keeps everything: the branch's commits are in the destination's history, so the approval,
the responses and the thread replies are all readable from the destination, and `status --changeset <id>`
reports them.

A squash, cherry-pick, or forge button keeps only the content. The directory arrives, so the changeset is
landed, and the chain behind it is gone: `status` reports `reviewed: false` with an empty `chain_base` and
`chain_head`, because nothing in the destination says the work was approved. PRD §13 states this limit.
Where the review has to survive a squash, the repository merges with `--no-ff` instead, or tidies the
changeset (`git pair change tidy <id>`, which moves the directory to `changesets/.landed/<id>/` on a branch
whose history still holds the chain) before it deletes the branch.

## 1 finding you will be asked about

`LANDED UNREVIEWED` — a `changesets/<id>/` directory is in the integration branch's tree and the chain
behind it carries no approving verdict. Either the work reached the branch without a review, or it arrived
by a route that rewrote the review away (the squash above). `git pair queue` prints it under its own
heading and `git pair status` prints it on a branch that carries no changeset of its own, in human output
and in `landed_unreviewed`.

Report it; do not try to close it. No command closes this one — the answer is a review, and running
something is not one. The old finding this replaced (`LANDED, UNRECORDED`) reported a step that had not
been run and printed the command that ran it; there is no step left to run.

## Reading a changeset that is no longer on a branch

`git pair status --changeset <id>` resolves a slug from whichever branch carries it, including the
destination's own tree and `changesets/.landed/`. That is the read for "what happened to that change", and
it is read-only: it reports `landed`, `landed_commit` and `landed_branch`, the chain it could read
(`chain_base`, `chain_head`, `reviewed`), and `stack`, which walks the ancestors that remain.
