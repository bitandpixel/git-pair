---
name: pair-stack
description: Use when a change is too large to review as one unit and should be broken down into a stack of smaller changesets — how to choose the layers, the order to build and land them in, and how a layer reacts when the one below it changes. git-pair stacks, not `gh stack`.
---

# Stacked changesets

Sizing comes first. Aim for one concern per changeset: roughly 50–200 changed lines, and treat more than 250
lines or 25 files as a signal to split. Split where concerns change — schema → API → wiring → UI → integration
tests — and self-test each layer with "this layer adds **_ so the next layer can _**". If that sentence needs
more than one "and", it is two layers. Three clear layers beat twelve microscopic ones.

## Ordering

Create layers bottom-up and record the dependency, because a base that is only implied gets guessed wrong
later:

```bash
git pair init --base main                     # bottom layer
git pair init --parent <parent-branch>        # the layer above it
```

## Rules that keep a stack mergeable

- Land the bottom as soon as it qualifies. The whole stack does not need to finish before merging starts.
- A child cannot be declared for integration before its parent has landed; `change integrate` refuses it.
- Fix review feedback in the layer that owns it, then restack what sits above.
- Never close or delete a mid-stack branch while something above it depends on it.
- One stack tells one story. Unrelated work gets its own branch.

## Where the details live

`git pair change stack`, `combine`, `integrate` — flags, refusals and JSON are the **git-pair** capability
skill's. This skill decides the shape of the work, not the syntax.

## Repairing a stack that already exists

Two commands for a shape that went wrong, not for normal work: `git pair change stack --base <branch>` records
a dependency between changesets that already exist, and `git pair change combine --into <id>` folds two
directories into one and archives the other. `git pair` warns when a branch carries more than one unlanded
changeset, and that warning is the cue for either of them.
