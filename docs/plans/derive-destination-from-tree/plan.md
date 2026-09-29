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

- [x] `init`: when the base is not the integration branch (already refused at `init.go:190`) and the branch it
  names carries exactly one unlanded changeset, record that id as `parent-changeset:`, reusing
  `parentChangesetOn` (`init.go:193`) rather than writing a second lookup.
- [x] `init`: two or more active changesets on that branch means no guess - leave the field empty and warn,
  naming every candidate so the author can pass the right one.
- [x] `check`: one reason for a stacked changeset with no `parent-changeset:` whose base names a branch
  carrying exactly one unlanded changeset. Print the id it would have recorded. Non-blocking: it is a
  recommendation, and it must not gate a merge.
- [x] Confirm no read-time path calls the new inference: the inference lives in the writer, not the resolver.

#### Discovery during execution

The pair is recorded, not the id alone. `parent-changeset:` beside a plain `base:` would break the
measurement base rather than improve it: `BaseFor` rule 1 keeps measuring against a live parent only when
`parent:` names it, so an id without the branch skips straight to the derived base while the parent is still
unlanded - the child's diff would then include the parent's work. `renderMetadata` drops
`parent-changeset:` when `parent:` is absent, which is the same rule seen from the writer. So `init` records
`parent:` and `parent-changeset:` and says out loud that it did, which is also the shape PRD §21 already
spells. A `check` recommendation covers the files written before the rule existed.

A deep stack broke the first version, and a reviewer found it. A branch created from its parent's branch holds
the ancestor's changeset directory in its own tree, so a three-level stack presented two unlanded candidates on
the base and `init` refused to pick. What tells a level of the stack apart from a sibling that shares the
branch is the chain the lower changeset records, so candidates that another candidate names in its parent chain
are dropped before anything is chosen. `Candidate.Distance` was considered for this and refused: it orders
candidates by which directory the branch touched last, which says which work is live and not which is a parent.

Review asked whether the `parent:` concept could be removed, leaving `base:` for the ref and
`parent-changeset:` for the relationship. It cannot. `parent:` is what makes a live parent measurable
(`BaseFor` rule 1 diffs against the parent's branch tip) and what makes "push here" and "your parent moved"
sayable; the id is what is still answerable after the branch is deleted. Replacing the pair with an id means
every consumer resolves id to branch by scanning this clone's branches, which is many-to-one and clone-dependent.
A file with `base:` and `parent-changeset:` and no `parent:` is the shape that question came close to wanting; it
parses to a plain base, because `stackOf` has no branch to attach the id to.

#### Verification

- Fixtures for the three shapes: one unlanded changeset on the base (recorded), two (empty plus a warning
  naming both), the base is the integration branch (refused, as today).
- The same pair of shapes three levels down, where the base's tree carries its own parent's directory: the
  recorded chain below the base makes it a stack, and with that chain absent the refusal still names both.
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

- [x] At hop 0, test the base against both `LandedIDs` and `SlugFromBranch(base)`, keeping the `underNamespace`
  guard the walked side keeps.
- [x] On a landed match, continue the walk through that changeset's record (`base, parentChangeset = "", base`)
  instead of returning the default branch, so a two-deep stack stops at the first live parent.
- [x] At hop 0, run the existence proof; on `ErrUnknownRevision` set `Unreachable` and fall back to the default
  branch with `why = "default"`. Keep any other error an error.
- [x] Give the override its own `DestinationSource` value, distinct from `"base"` and from `"default"`, so the
  substitution is visible in `--json` and in the human surface.
- [x] Feed `landingNextAction` (`status.go:659`) the computed destination rather than `s.cs.Base`
  (`check.go:162`), so the sentence cannot go stale while the declaration is correct.
- [x] PRD: one passage naming the asymmetry with `BaseFor`, the two proofs, and the tie-break decision below.

#### Discoveries during execution

- **The tree fact is asked before the ref exists, at every hop.** An authored base may name the changeset by
  its id (`feature-x`), which resolves to no ref at all; proving existence first reported it unreachable. The
  same ordering also removed one `rev-parse` per hop on the walked side.
- **The destination walk duplicated the chain walk, and the cost test caught it.** `status` describes the
  stack (chain surface) and now answers where the work lands, over the same ancestors. The marginal read per
  ancestor doubled, which `status_stack_chain_test.go` refuses. The fix is `changeset.Reads` - a per-command
  memo of the destination's directory listing and each ancestor's record, passed as an argument rather than
  hidden in `git.Repo`, because a command writes markers and moves refs while it runs. The bound stayed where
  it was.
- **`change integrate` refused the shape the fix is for.** `unlandedParentReason` declined a child whose stack
  recorded no parent changeset, "so git-pair cannot show that branch has landed". The destination's tree can
  show it, and does: the gate now asks the tree, and the refusal stands when the directory is not there -
  which is what makes the refusal mean "this is live work" rather than "this file is incomplete". Without that,
  the replay below needed a hand edit, which the plan rules out.
- **Hop-0 recognition only meets plain bases.** For a stack that recorded `parent:`, the resolver already
  replaces the measurement base with the derived one, so the walk starts one level up and answers `parent`.
  Both paths are asserted; they are the two spellings of the same relationship.
- **`Why` carries the override** (`base-landed`) beside `base`, `parent` and `default`, and `Overrode` names the
  field that was refused, in the spelling it was written. A separate `DestinationSource` type would have been a
  second way to say what `Why` already says.

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

## Follow-ups discovered during execution

- **The integrator never fires for a branch outside `feat/**` and `feature/**`.**
  `.github/workflows/git-pair-integrate.yml` triggers on `workflow_run` from `CI` with that branch filter,
  while `ci.yml` triggers on `**`. A changeset on `docs/derive-destination-from-tree` went green at 20:10 and
  no merge job ran for it; the `*/15` schedule poll or a `workflow_dispatch` is the only way it lands. The
  filter also disagrees with what `git pair init` accepts as a branch name. Worth its own changeset: the
  integrator should follow the queue, not a list of branch-name patterns.

## Open decision, needed before M2 lands

A `base:` whose branch still exists and carries work, while a same-slug changeset has landed elsewhere. The
proposal is that landedness wins and the matched id is printed: every other landing question already answers
from the tree, and the alternative - ref existence wins - restores the property that made the durable layer
unusable, an answer that depends on which refs a clone happens to hold. This needs a decision recorded in the
PRD beside `DestinationFor`, not a conditional added quietly.
