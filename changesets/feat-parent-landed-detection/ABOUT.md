# feat-parent-landed-detection

## Summary

A parent landed, its branch stayed, and three commands said nothing. `git pair status` on
`fix/legacy-refs-and-remote-branches` printed `Base: fix/for-each-ref-glob` and no `Stack:` section while
that parent sat recorded at `4c89685` — published, and current trunk — with the branch still at `851df62`
locally and on origin.

Both questions the code asks of a parent stop before reading the record. `parentSinceApproval` returns the
zero value for a child with no approval (`internal/cli/stacked.go:55-57`), so a `READY` child is never
asked; and for an approved child `st.Recorded == st.Tip` returns clean (`stacked.go:87-89`) before anything
touches `refs/git-pair/*`, which a landing never moves. `queue`'s `behindParent` (`queue.go:307-322`) is
silent for the same shape, because a landed, untouched parent is an ancestor of the head.

This changeset is the plan for the fix: `docs/plans/parent-landed-detection/plan.md`. It still changes no
behaviour, and it was offered for review before any code was written because the rejected options are the part
worth arguing with — a `landed:` key in `CHANGESET.yaml`, a restack onto the integration ref, and treating a
landing as parent movement are each considered and each refused, with the reason written next to the milestone
that could have done it.

The first round argued with the fourth one. Review `a87ae0b` objected that the relink trigger only decides
*when* the base moves, since deleting the parent branch moves it regardless, and measuring it showed the delay
costs more than a label: a child rebased onto trunk, read against the parent's branch tip, reports the
parent's own work and unrelated trunk work as the child's change. That non-goal is withdrawn, the argument is
in the thread, and the trigger is milestone 4.

The second round settled what milestone 4 was waiting on (`f443b7c`): an approval survives the relink when
`base...head` is identical under both bases, and two clones printing different `Base:` values is accepted
because they agree deterministically given the same refs. Answering "what breaks when they disagree" produced
two guards and one fix — `Review-Parent-Head` keeps naming the branch tip while the branch exists, a landing is
recorded beside it rather than replacing it, and `integration record`'s derived `--target` never offers a base
under `refs/git-pair/` as a destination.

## What changed

- `docs/plans/parent-landed-detection/plan.md` — the derivation to add, the four milestones, the state
  matrix to write as tests first, and the real-repository data (`4c89685`) every milestone is checked
  against.
- `changesets/feat-parent-landed-detection/measurement-base-when-the-parent-lands.md` — the thread review
  `a87ae0b` asked for, on whether the relink should wait for the parent branch to be deleted.

## Design decisions

**Detection first, measurement second, and the second one is now open.** Milestones 1–3 change no base and
keep `relinkStacks` (`internal/changeset/resolve.go:535-549`) on its current trigger — the branch is gone —
so the notes, the JSON fields and the advisory land without touching what a reviewer reads. The reviewer
pointed out that deleting the branch moves the base anyway, and measuring it showed the delay is not neutral:
a child rebased onto trunk, read against the parent's branch tip, prints
`c.txt p.txt trunk.txt` where the integration ref prints `c.txt`. The relink trigger is now milestone 4, with
the PRD §21 question it cannot avoid written into the thread.

**Nothing new is written down.** The landing is already durable in `refs/git-pair/integrations/<parent>`,
and the two signals already have fixed jobs: the ref answers "landed as what", and the directory in the
destination answers "reached trunk" and "landed unrecorded" (`internal/cli/landed.go:65-173`). Copying
either into `CHANGESET.yaml` creates a second source of truth, and a `parent:` that names an integration ref
would break the case `parent:` is defined as a branch and destroy the tellability PRD §21 protects.

**A landing is not parent movement.** PRD §21's conservative rule is about the parent *branch* moving, and a
landing does not move it. Applying the rule to landings would drop approvals whose diff content is identical.

**Notes, not refusals.** New statements go to `a.warn` and to JSON fields. `reasons` means "do not integrate
this", and a child whose parent landed with an unchanged diff integrates fine.

**The advisory prints the command and stops.** git-pair runs no `rebase` and no `branch -D`, so the note
names the command, names the worktree blocker when another worktree holds the branch, and the human runs it.

## Validation

No code changed, so nothing to run beyond `mise run check` for the docs-contract test over the new plan file.

Reviewers who want to reproduce the gap can run, in `/home/david/dev/worktrees/git-pair/legacy-refs`:

```bash
git pair status --json | jq '.parent, .base, .span'   # null today
git for-each-ref | grep for-each-ref-glob             # archive 851df62, integration 4c89685
```

## Known limitations

- The plan assumes `parent-changeset:` is present on the children that matter. A child without it has no
  ref name to resolve and stays silent rather than guessing.
- Whether the advisory belongs on the three existing surfaces or on a dedicated `git pair stack` command is
  left to the reviewer; milestone 3 is where it would go, and the derivation, notes and tests below it do
  not change either way.

## Open questions

- Is the stale-branch note worth printing at all once the child has been rebased onto the landing commit
  and only the branch deletion is left? The plan says yes, at `a.warn` severity, and it is cheap to drop.
- `fix-legacy-refs-and-remote-branches` is in review and edits `landed.go`, `published.go`, `reviewref.go`
  and `integration.go`. This plan reads through `ResolveIntegration` and `indexDurableRefs` rather than
  `List`, so the two overlap in file names only. Should this branch stack on that one instead of trunk?
