# docs-derive-destination-from-tree

## Summary

A plan for two changesets, written from a trap that fired twice in one week. Both times a stacked changeset's
authored `base:` named its parent branch, the parent merged, and the field was left naming a branch whose work
was already in trunk. `git pair check` then told the author to merge into that branch, and `git pair change
integrate` would have asked CI to do exactly that. Each was corrected by hand, one review round each.

`docs/plans/derive-destination-from-tree/plan.md` holds the plan. It is two milestones: `init` recording the
stack link it can already see, and `DestinationFor` asking its two questions at hop zero as well as further up
the walk.

## Why this is a plan and not a fix

The change is small in lines and wide in consequence. Recognition at hop 0 changes where an existing changeset
asks to be merged, and the fix has to keep four properties at once: stable across clones, visible in
`--json`, one string shared by the five surfaces that print a destination, and inside the cost budget the
queue tests hold. One case - a base whose branch is still live while a same-slug changeset has landed
elsewhere - needs a decision written into the PRD rather than a conditional added quietly, so the plan names
it as the thing to settle before the second milestone lands.

## What the plan deliberately does not do

It does not derive lineage at read time. `parent-changeset:` stays authored: a base branch can gain a second
active changeset later, and a child's destination must not shift as a side effect of someone else starting
work. The read-time answer is a `check` recommendation naming the id it would have recorded, which is a
suggestion a person can refuse.

It also does not touch the `BaseDerived` exclusion in `DestinationFor`. A derived base is a commit, and a commit
is a measurement point, never a destination.

## Where the line numbers point

Against `feat/durable-refs-gone`, not trunk. Trunk's `DestinationFor` still opens its loop with a durable-ref
rewrite that the pending stack deletes, and a plan written against trunk would point at code that is about to
be removed. The positions are listed in the plan's Context section.

## Validation

- `mise run gates` on this changeset: the plan is prose, so the bar is the docs contract tests and the suite.
- The plan's own verification sections are the acceptance criteria for the two changesets it describes, and
  both are written so they can be checked without the author: five destination shapes as unit tests, the
  single-string claim asserted across the surfaces that print it, and one replay of the real trap against a
  scratch repository with a real remote.
