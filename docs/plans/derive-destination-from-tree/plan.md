# Derive the destination from the tree

## Goal

`git pair` answers "where does this work land" from the destination's tree even when the changeset's own
authored `base:` has gone stale, and the sentence a human reads is the same answer the declaration asks for.

## Success criteria

- A changeset whose `base:` names a branch whose work the destination already carries is declared against the
  destination, and the answer names the rule that overrode the authored field rather than claiming the field.
- A changeset whose `base:` names a branch that does not resolve here says so and falls back to the integration
  branch, instead of naming a destination that does not exist.
- `git pair check`'s next step, `status`'s landing line, and `change integrate`'s "merge into" are one string
  computed once, not two readings of two fields.
- `git pair init` records `parent-changeset:` when it can see exactly one unlanded changeset on the base it is
  recording, and the value is written into `CHANGESET.yaml` where a reviewer reads it.
- Both rules are written in the PRD beside `DestinationFor`, including the tie-break for a base that is
  simultaneously live and landed.

## Context

The trap fired twice on one stack. M4 and M5 each recorded `base:` naming their parent branch, because they
were created with `git pair init` and no `--parent`, so `parent:` and `parent-changeset:` were never written.
Each time the parent merged, the child's `base:` named a branch whose work was already in trunk. `check` then
printed "merge into `<merged branch>` with ordinary git", and `change integrate` would have asked CI to merge
the child onto that branch. Both were corrected by hand, one review round each.

Two facts about the code shape the plan:

- **The asymmetry is inside one package.** `BaseFor` (`internal/changeset/base.go`, rules 1-3) treats a stale
  base as stale: once the parent's work reaches the destination it measures from `merge-base(child, destination)`.
  `DestinationFor` (`internal/changeset/destination.go`) does not. Its existence proof (`RevParse`) and its
  landedness proof (`LandedIDs`) are both inside `if hops > 0`, so the changeset's own authored value is
  returned verbatim at hop 0.
- **The plan is written against the post-deletion tree.** On trunk, `DestinationFor` opens its loop with a
  durable-ref rewrite (`reviewref.ResolveIntegration`). `feat/durable-refs-gone` deletes that layer. Line
  numbers here refer to that branch, because work starting from trunk would aim at code that is about to go.

Positions on `feat/durable-refs-gone`: `destination.go:60-125` - the `BaseDerived` exclusion at `:63-66`, the
comment justifying the guards at `:72-75`, the guards themselves at `:71` and `:88`; `integrate.go:161`
(`changeset.DestinationFor`); `check.go:162`, which passes `s.cs.Base` to `landingNextAction` at
`status.go:659`.

## Constraints

- **Stable across clones.** Two clones of one repository that fetched at different times must give the same
  destination. Any read that depends on which refs a clone holds is out; the tree reads are in, because the
  destination's history is what a fetch brings.
- **No silent substitution.** An authored field that is being overridden must be reported. The override gets
  its own `DestinationSource` value, not a reuse of `"base"`.
- **Cost.** `LandedIDs` is already called on the walked side; asking at hop 0 costs two directory listings.
  Gate scripts and the cost tests in `internal/cli/cost_test.go` are contracts: they are extended with
  behaviour, never loosened.
- **`SlugFromBranch` is many-to-one** (`internal/changeset/changeset.go`): only branch to id is available, and
  `feat/tidy` and `feat-tidy` collapse together. Anything the rule prints names the id it matched, never a
  normalised spelling presented as the author's.
- `destination.go:63-66` stays as it is. A derived base is a commit - a measurement point, never a destination.

## Assumptions

- `parent-changeset:` stays authored. Read-time derivation of lineage is a warning, not a substitution, because
  a base branch can later gain a second active changeset and a child's destination must not shift as a side
  effect.
- Both a branch-shaped and an id-shaped `base:` must be recognized (test 4 below).

## Milestones

### M1 - `init` records the stack link it can already see

#### Deliverables

- A changeset created from a branch that carries exactly one unlanded changeset has `parent-changeset:` in its
  YAML, without the author passing `--parent`.
- A stacked changeset that has no `parent-changeset:` is reported by `check` with the candidates it found, and
  the fix it prints is the flag to declare one.
- Nothing at read time derives lineage: the answer a command gives about a stack does not change when a second
  changeset appears on the base branch.

#### Tasks

- [ ] `init`: when the base is not the integration branch (already refused at `init.go:190`) and the branch it
  names carries exactly one unlanded changeset, record that id as `parent-changeset:`, reusing
  `parentChangesetOn` (`init.go:193`) rather than writing a second lookup.
- [ ] `init`: two or more active changesets on that branch means no guess - leave the field empty and warn,
  naming every candidate so the author can pass the right one.
- [ ] `check`: one reason for a stacked changeset with no `parent-changeset:` whose base names a branch
  carrying exactly one unlanded changeset. Print the id it would have recorded. Non-blocking: it is a
  recommendation, and it must not gate a merge.
- [ ] Confirm no read-time path calls the new inference: the inference lives in the writer, not the resolver.

#### Verification

- Fixtures for the three shapes: one unlanded changeset on the base (recorded), two (empty plus a warning
  naming both), the base is the integration branch (refused, as today).
- A test that adding a second active changeset to a base branch after the child was created leaves every
  already-recorded field of the child unchanged - the stability constraint, asserted.
- Existing `init` and `check` tests green; `mise run gates` green.

### M2 - the destination is derived at hop zero too

#### Deliverables

- A base naming a changeset the destination already carries is not a destination: the walk continues through
  that changeset's own record rather than jumping to trunk, and the answer reports the rule that fired.
- A base that does not resolve here sets `Unreachable` and the answer falls back to the integration branch with
  `why = "default"`, while the measurement base and the diff stay as they were.
- `check`, `status`, `change ready`, `review` and `change integrate` print one destination string.
- The PRD says what `DestinationFor` decides and how it conflicts with `BaseFor`, including the tie-break below.

#### Tasks

- [ ] At hop 0, test the base against both `LandedIDs` and `SlugFromBranch(base)`, keeping the `underNamespace`
  guard the walked side keeps.
- [ ] On a landed match, continue the walk through that changeset's record (`base, parentChangeset = "", base`)
  instead of returning the default branch, so a two-deep stack stops at the first live parent.
- [ ] At hop 0, run the existence proof; on `ErrUnknownRevision` set `Unreachable` and fall back to the default
  branch with `why = "default"`. Keep any other error an error.
- [ ] Give the override its own `DestinationSource` value, distinct from `"base"` and from `"default"`, so the
  substitution is visible in `--json` and in the human surface.
- [ ] Feed `landingNextAction` (`status.go:659`) the computed destination rather than `s.cs.Base`
  (`check.go:162`), so the sentence cannot go stale while the declaration is correct.
- [ ] PRD: one passage naming the asymmetry with `BaseFor`, the two proofs, and the tie-break decision below.

#### Verification

Five shapes, each as a unit test and each with the answer and its reason asserted together:

1. Own base names a landed changeset's branch - destination is the integration branch, the source says the base
   landed, and `Via` names the parent id.
2. Own base names a branch that does not resolve here - `Unreachable` set, fallback to trunk, and the diff
   unchanged.
3. Own base names a live branch carrying one unlanded changeset - answer unchanged. This is the stack case, and
   the one most likely to be broken silently by this change.
4. The same base written branch-shaped and id-shaped - both recognized, and the printed match names the same id.
5. A two-deep stack whose lower parent merged and whose upper parent is live - the walk stops at the live parent.

Plus: the gate script gains the replay of the real trap - create a stack with `--parent`, merge the parent,
then show the child's `check` and `change integrate` naming the integration branch with no hand edit anywhere.
Cost is measured against `cost_test.go`, whose fixtures are extended rather than the bound lifted.

## Risks

- **Slug collisions.** `SlugFromBranch` collapse can match a changeset the author did not mean. Mitigation: the
  rule prints the id it matched, and the hop-0 rule only ever *continues a walk* - it cannot invent a
  destination that no tree fact supports.
- **This changes a merge target for existing changesets.** A changeset whose base names a now-merged branch
  stops declaring against that branch. That is the intent, and it is the kind of change the release note and
  the PRD passage have to state, with `--json`'s destination fields as the reviewable surface.
- **Recognition can be wrong in the live-and-landed case.** See the open decision; do not implement a silent
  conditional there.

## Verification strategy

Unit tests in `internal/changeset/destination_test.go` for the five shapes; `internal/cli` tests for the
single-string claim across the surfaces that print it; the gate scripts for the end-to-end replay of the trap;
one run of the whole flow against a scratch repository with a real remote, because the failure was only visible
where a CI job would act on the answer.

## Open decision, needed before M2 lands

A `base:` whose branch still exists and carries work, while a same-slug changeset has landed elsewhere. The
proposal is that landedness wins and the matched id is printed: every other landing question already answers
from the tree, and the alternative - ref existence wins - restores the property that made the durable layer
unusable, an answer that depends on which refs a clone happens to hold. This needs a decision recorded in the
PRD beside `DestinationFor`, not a conditional added quietly.
