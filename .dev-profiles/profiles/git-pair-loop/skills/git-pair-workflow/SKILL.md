---
name: git-pair-workflow
description: Use for any change in this repository — the branch → commit → changeset → review → merge-request loop, and who performs the merge. Replaces general Git workflow advice. Not for GitHub pull-request routes unless they are explicitly requested.
---

# Development workflow for this repository

Profile: **git-pair**, version 2. The change record is `changesets/<id>/ABOUT.md`.

The rule that decides everything else: the agent authors and offers; a human reviews; the destination owner
merges.

## The loop, in order

```bash
git status --short --branch          # orient first; never work on the default branch
git pair init --base main            # once, on a named branch
# implement and commit with ordinary git — git-pair has no add, commit, or branch command
# prove the work runs: through the repository's pre-ready hook if it has one, by hand if it has not.
#   Ask the repository what "runs" means before picking a command: `dev-profiles command verify` prints the
#   verification this repository configured, and the merge job runs that same step.
#   Nothing else is promised to run the suite on your commits, and a reviewer's minutes are the expensive ones
# fill changesets/<id>/ABOUT.md before the handoff, against the change-description skill below
git pair change ready                # the handoff. This IS the review request
git pair change feedback             # when a review lands: read it here, not with `diff --unreviewed`
# answer the reviewer in DISCUSSION.md and resolve every surviving line, commit normally
git pair change ready                # back into the queue
git pair check                       # the gate; exit 0 means it may land
git pair change integrate            # the ask, and the expected one once the gate is clear — then stop
```

Once `git pair check` is clear and the change is approved, run `git pair change integrate` and push. That
command writes a commit saying the merge may be performed; it does not perform the merge, and it needs nobody's
permission. An approved changeset left undeclared waits on the queue for a decision nobody has to make, because
the gate has already refused anything that may not land. Declare once: what to do when a declared merge has not
happened is a different question, and it is in the Never list below.

Then stop, because the merge is not yours. Two things can land it: the destination owner merges with
`git merge --no-ff`, or — once declared — the destination owner's integration job does the same git and pushes
the destination itself. git-pair merges nothing, pushes nothing, and writes no ref either way; the destination
carrying `changesets/<id>/` is the whole landing record.

This profile says nothing about what starts that job, and that is deliberate. A profile that names a consumer's
triggers drifts the first time the consumer optimises them, and the consumer is the one who has to live with the
minutes. What it does require of the author is the part CI used to cover: prove the work runs before you hand
off. If it cannot run where you are, write that into the change record — a break nobody could test is a finding,
and a break nobody mentioned is a surprise delivered to a reviewer.

The tool can run that check for you. A script at `.git-pair/hooks/pre-ready` — or at
`<gitdir>/git-pair/hooks/pre-ready`, which is one person's own and never shared — runs before `change ready`, and
`pre-check` and `pre-integrate` run before the gate and before a declaration. `git pair hooks` reports what is
configured and whether it is trusted. Two rules apply to the committed root, because it is code from a stranger:
it runs only after a person records acceptance with `git pair hooks --trust`, and the commands that start or
advance work refuse while that tree is `unrecorded` or `changed` — a repository that ships no `.git-pair/hooks`
is unaffected. So configure the hook when the repository wants the duty enforced and somebody will record the
acceptance; until then, or when the hooks are `declined`, the author runs the suite by hand. Hooks need git-pair
v0.4.0 or newer; with an older tool there is no hook point, and the manual run is the only one. Two authoring
facts, both observed: a hook that exits non-zero refuses the hand-off and leaves the changeset where it was, and
a hook that passes has its own output withheld — so put the explanation in the failure message, where it is
shown. Declining is a position a person can take, not a gap to work around.

## Never

- `git pair review submit`, `review open`, `review reopen`, `review about`, `review discussion`. Approval is the
  reviewer's act.
- Merging, squashing, or pushing to the destination. Performing the merge is not yours. Asking for it is, and
  `git pair change integrate` is the ask: an approved changeset with a clear gate should be declared.
- Asking for review in prose. `change ready` is the handoff, and doing both trains people to ignore the queue.
- Re-declaring a merge because it has not happened yet. A second `change integrate` changes nothing, and
  `change unready` to force one destroys the approval and costs a fresh review. A merge that has not happened has
  a cause — a run that went red, a branch rewritten since the approval, a tip that is no longer the declaration —
  and it is written down in that job's log and readable in `git pair queue`. Re-run the job; do not re-offer the
  work, and never declare on the theory that a green-looking queue means nobody looked.
- Rebasing a declared changeset onto the destination. The approval names the commit it reviewed, and `check`
  refuses to read it as covering rewritten history: the changeset is then stuck behind a human review that has to
  happen again. Merge the destination in instead — that rewrites nothing and costs nothing.
- Leaving a changeset `READY` when the work is not finished — `git pair change unready` first.

## Reading the answers

| Question                                | Command                    | Note                                                                                    |
| --------------------------------------- | -------------------------- | --------------------------------------------------------------------------------------- |
| What state is this in, and what's next? | `git pair status`          | observes. Never permission                                                              |
| May this land?                          | `git pair check`           | decides. `--json` exits 0 either way — gate on `.ready`                                 |
| What did the reviewer just say?         | `git pair change feedback` | `diff --unreviewed` is the reviewer's question and reads empty right after a submission |
| What is waiting on someone?             | `git pair queue`           | ranked by wait                                                                          |

Exit codes: `1` means the repository said no to a correct command; `2` means the command was wrong and will
fail again unchanged. Retrying a `2` is a wasted cycle.

## Other skills, and when to reach for them

This is the profile's whole menu, not this repository's. A skill the repository declines (`exclude_skills` in
`.dev-profiles.toml`) or does not require as a capability is not a route available here — do not reach for a file
that is not installed.

- **change-description** — what the change record has to answer, and in what order. `ABOUT.md` is that document:
  the reviewer's seven questions, with the sections that add nothing left out rather than filled.
- **git-pair** — every flag, refusal, JSON field and trailer of the CLI. Name the command here; read it there.
- **pair-stack** — the change is too large to review as one unit, so it becomes a stack of changesets.
- **pair-preview-pr** — only for a repository that declares the `preview-pr` profile, and only where a
  preview environment is wanted *and this repository deploys one*; a library has none and
  declines the skill. A pull request deploys, it does not review.
- **gh-cli** — GitHub enters the picture (checks, runs, releases). Whatever the profile, that skill owns the
  `gh` mechanics.

## Override

If someone asks for a different route — "use the PR flow for this one" — read
`.dev-profiles/references/alternative-workflows.md`, follow that profile, and record the switch in `ABOUT.md`:
what was overridden, who asked, and what the change therefore skipped. Switch only when asked; nothing in a
session's own reasoning selects an alternative.
