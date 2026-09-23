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

This changeset is the plan for the fix: `docs/plans/parent-landed-detection/plan.md`. It changes no
behaviour. It is offered for review before any code is written, because the rejected options are the part
worth arguing with — a relink while the branch exists, a `landed:` key in `CHANGESET.yaml`, a restack onto
the integration ref, and treating a landing as parent movement are each considered and each refused, with
the reason written down next to the milestone that could have done it.

## What changed

- `docs/plans/parent-landed-detection/plan.md` — the derivation to add, the three milestones, the state
  matrix to write as tests first, and the real-repository data (`4c89685`) every milestone is checked
  against.

## Design decisions

**Detection, not measurement.** The base keeps pointing at the parent branch while that branch exists.
`relinkStacks` (`internal/changeset/resolve.go:535-549`) keeps its trigger — the branch is gone — because
moving the base moves the span a reviewer reads and changes what `Review-Parent-Head` is compared against.

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
