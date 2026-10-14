# Profile: git-pair

A human reviews every change before it reaches the destination branch. The agent authors the change, offers
it, answers the review, and asks for the merge. The merge belongs to whoever owns the destination.

## When this profile fits

- A change is worth a human read before it lands, and that read should happen on the branch, not in a chat
  window.
- Review should survive as repository history: who approved what, which commit they looked at, what was
  answered.
- The repository may or may not have a forge, and the review loop should not depend on one.

## Roles, and the line between them

| Role              | Who                                        | Does                                                                      |
| ----------------- | ------------------------------------------ | ------------------------------------------------------------------------- |
| Author            | usually the agent                          | implements, `change ready`, answers feedback, `check`, then `change integrate` — the ask is expected, not optional once the gate is clear |
| Reviewer          | a human                                    | submits the verdict. Never the author                                     |
| Destination owner | a human, or the owner's integration job once the change is declared | performs the merge with ordinary git                              |

The agent never runs `git pair review submit` in any form, never approves its own work, and never merges.
Nothing in the tool checks who ran a command — that boundary is discipline, not enforcement.

## The loop

```bash
git pair init --base main            # once, on a named branch
implement and commit with ordinary git        # git-pair has no add, no commit, no branch command
# prove the work runs before handing off — by the repository's pre-ready hook if it has one and somebody
#   recorded acceptance, by hand otherwise. Nothing else is promised to run it for you.
# fill in the change record, then:
git pair change ready                # the handoff. This IS the review request — do not also ask in prose
git pair change wait --fetch --json  # block until the reviewer acts, or use pi's git_pair_wait
git pair change feedback             # what the reviewer said, as a diff
# address it, commit normally, re-offer
git pair change ready
git pair check                       # the gate; its exit code is the answer
git pair change integrate            # the ask, once the gate is clear. Expected: then stop
```

The line between those last two commands is the one people get wrong. `check` answers whether the merge may
happen; `change integrate` says so to whoever performs it, in a commit. Writing that commit is the author's act
and needs nobody's permission. Performing the merge is not. Leaving an approved changeset undeclared adds no
safety — the gate has already refused anything that may not land — and the change waits on the queue for a
decision nobody has to make.

`git pair` flags, exit codes, refusals and JSON shapes are the **git-pair** capability skill's business. This
profile names the commands and the order; it does not restate the CLI, because a second copy of those rules
is a second thing that can drift.

## Landing

Two things can land a changeset. The author declares; the destination owner — a person, or their integration
job — chooses the route. Declaring is not the author choosing to skip a review: nothing here merges an
undeclared or unapproved change.

The owner's own merge, for a changeset that was never declared for integration:

```bash
git switch main && git pull
git merge --no-ff <branch>
git push origin main
```

The destination owner's integration job, for one that was:

```bash
git pair check               # exit 0 first — the gate, not status
git pair change integrate    # the declaration, then stop
```

That job then does the git. It re-reads the queue, builds the destination plus the declared head, asks the
repository's own tests about **that** tree rather than about the branch, merges with `--no-ff` only if they pass,
and pushes. `--no-ff` in both routes keeps the review chain readable from the destination. What starts the job is
the repository's business: the profile promises that a declaration gets merged, not the trigger list that carries
it there.

Neither route has git-pair performing the merge. The job shells out to ordinary git, and the destination carrying
`changesets/<id>/` is the whole record of the landing. When a declared changeset has not landed, something refused
it — read the job's log and `git pair queue` instead of declaring again, since a re-declaration changes nothing and
withdrawing to force one costs a fresh approval. Catch up with the destination by merging it in, never by rebasing:
the approval names the commit it reviewed, and it does not survive a rewrite.

## Housekeeping

`git pair change tidy` moves the directory of a changeset the destination already carries into
`changesets/.landed/`. It is a commit, and a commit is somebody's decision: the repository owner tidies
periodically, and an agent runs it **only when asked**. `dev-profiles doctor` reports candidates and does not
fail on them.

## Exit codes, because they change what you do next

`1` means the repository said no to a correct command — fix the state and retry. `2` means the command was
wrong and will fail again unchanged. Retrying an exit 2 without changing it is a wasted cycle.
