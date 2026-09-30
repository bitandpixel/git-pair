# Decide the active changeset from the manifests

## Goal

The changeset a branch is working on is decided from what the changeset files on that branch record, and the
commit history is consulted only when the records do not decide it. One authored field carries the stack link
(`base:` plus an always-recorded `base-changeset:`), one rule reads it, and an author's `ignores:` declaration
outranks every inference.

## Success criteria

- On a branch carrying a linear stack, the candidate list is reduced to the one changeset that no other
  candidate names, and the answer does not depend on how a branch name normalises into an id.
- A commit to a parent changeset's directory on a child branch does not move the child's answer. This is a
  present defect: today the answer follows whichever directory was edited last, which can name the parent.
- An `ignores:` declaration removes the named candidate before any ranking or subtraction, so no inference can
  outrank it.
- The distance measurement is read only when the earlier rules leave two or more candidates. A branch carrying
  one changeset makes no history walk.
- `CHANGESET.yaml` for stacked work reads `base:` and `base-changeset:`, always both, and the id is never
  re-derived from a branch name at read time.
- The PRD states the rule in that order, including what happens when nothing decides.

## Context

Two things started this. The first is the schema question: `parent:` and `parent:`-with-`parent-changeset:`
duplicate what `base:` already says, and `applyBases` (`internal/changeset/resolve.go`) overwrites
`Changeset.Base` with a computed measurement, so an authored link and a computed value share one struct field.
The decision taken in review is to collapse them: keep `base:` as the authored link, always record the changeset
id it points at as `base-changeset:`, and drop `parent:`/`parent-changeset:` from the writer. Always-record was
chosen over record-when-it-differs so that a future change to slug derivation cannot silently re-point a stack,
and so the file reads as a stacked changeset rather than implying it.

The second is a measurement, taken on `probe/setop-selection` (`/home/david/dev/worktrees/git-pair/setop-probe`),
which is an experiment branch and will not be offered. `choose` was given the set operation - candidates are the
changesets on the revision that the destination does not carry, subtract the ids that other candidates record as
their base changeset, and what remains is the work - and the suite passed in both modes. The fixtures that
differed:

| shape | last-touch today | set operation | add-commit ranking |
| --- | --- | --- | --- |
| three-level stack, ids recorded | child, candidates keep all three | child, candidates `[child]` | child |
| stacked sibling, then a commit to the parent's directory | **parent** | child | child |
| legacy file: `base:` naming a branch, no id | child, via distance | child, via distance | child |
| two files naming each other | ambiguous, both named | ambiguous, both named | ambiguous |
| a file naming itself | itself | itself | itself |
| middle level landed alone, grandparent still carried | child, via distance | child, via distance | child |
| `ignores:` on the parent, sibling stacked on it | parent | **parent loses** | parent |
| two unrelated changesets on one branch | the later one | the later one | the later one |

Three conclusions, each of which became a milestone.

The set operation fixes a defect nobody had noticed. In the second row the branch is the child, the child
records that it is stacked on the parent, and the last commit edits the parent's `ABOUT.md`. Today the answer is
the parent. No existing test covers that shape, which is why the suite stayed green while the rule was wrong.

The set operation needs a comparable id, and today's drop cannot get one. `stackParentID` returns
`Changeset.ParentBranch`, a branch name, and `parentID` returns `base` with `refs/heads/` trimmed, so for
`base: feature/a` the value is `feature/a` while `dropNamed` compares against the slug `feature-a`. The drop
only lands when the parent is spelled as an id. That is the gap `feat/infer-parent-changeset` worked around with
`withoutRecordedAncestors` in `internal/cli/init.go`; recording the id closes the gap and makes that walker
redundant.

Ranking by creation is steadier than ranking by edit. Measured in this repository (484 commits), both
primitives cost 2 to 3 ms per directory: `git log -1 --format=%H rev -- changesets/<id>` for the last touch, the
same with `--diff-filter=A` for the commit that added the directory. The add-commit value does not move when
someone commits to a changeset the branch is not working on, which is the property the second row needs, and it
still answers the legacy and dangling-ancestor shapes the way distance does today.

## Constraints

- **Cost contracts hold.** `scripts/gates/` and the cost tests in `internal/cli` are extended with behaviour,
  never loosened. A new read needs a new assertion beside it, as `feat/destination-from-tree` did for
  `CI-INTEGRATE`.
- **Old spellings are read for as long as they exist.** Landed changesets on trunk are history: 26 directories
  carry `base:`, 2 of them also carry `parent:`. The reader keeps accepting `parent:` and `parent-changeset:`;
  only the writer changes. Nothing here rewrites a landed file.
- **`SlugFromBranch` is many-to-one** (`internal/changeset/changeset.go`): `feature/x` and `feature-x` collapse,
  and `git branch -m` leaves the directory behind. Nothing in the new rule may derive an id from a branch name
  when the id is recorded.
- **Stable across clones.** The commit facts are read from history that a fetch brings, so a clone that holds the
  branch sees the same add commit. Refs a clone may or may not hold stay out.
- **An empty result is never an answer.** A hand-edited pair can name each other; the rule reports the
  candidates it could not order rather than reporting no changeset.
- **`ignores:` is authored intent** (`IgnoresKey`, `SetIgnores`), and intent outranks inference. This is the order
  the probe measured against.

## Assumptions

- `base-changeset:` is always recorded for stacked work, not only when it differs from what a branch name would
  imply.
- No authored `destination:` key is introduced. Where work lands stays a computed fact; a per-repository
  integration branch belongs in config, not in one changeset's file.
- The stack this plan describes is built on `feat/destination-from-tree` (`61c9454`), which supplies
  `changeset.Reads`, the destination rule and the `recommendations` surface that `check` prints. Positions in
  this plan were read against that branch. If that stack changes shape, re-read `choose`, `stackOf`,
  `renderMetadata`, `stackParentID` and `applyBases` before starting.

## Milestones

### M1 - the plan itself

#### Deliverables

- This document, on a branch, reviewable on its own.

#### Tasks

- [ ] Nothing beyond the document. Every later milestone is its own changeset, and this stack's order is the
      order above.

#### Verification

- `mise run gates` green: the docs contract tests scan documents for command names and ref paths, so a plan is
  gated like code.

### M2 - `ignores:` outranks every inference

#### Deliverables

- A declaration that one changeset merely shares a branch with another removes the named candidate before any
  ordering happens, so it cannot be overruled by nearness or by the set operation added in M5.
- The probe's `ignores:` fixture becomes a real test asserting the declaring changeset is selected.
- The reason the declaration survives always-recorded ids is written into the milestone: it is the only input to
  selection that is not an inference, and recording the link makes the shape it decides more common rather than
  rarer.



`ignores:` is written by `git pair change use` (PRD section 9.8), which records that its changeset is the one the
branch is working on and that the others named merely share the branch. Two shapes survive the set operation and
need it. The first is cohabitation: candidates with no stack edge between them, where subtraction has nothing to
subtract and the alternative is an ordering guessed from commit history. The second is a stack recorded on the
branch it is stacked on - `booking-tests` naming `booking` while both live on `booking-tests` - where subtraction
does answer, and answers for the stacked one. Always-recorded ids make that second shape more common, because
`init` records the link whenever the base branch carries exactly one unlanded changeset, so a changeset created on
a branch that already carries one is subtracted from unless the author says otherwise.

#### Tasks

- [ ] In `choose` (`internal/changeset/resolve.go`), move the `dropNamed` pass over `Candidate.Ignores` ahead of
      everything else that reorders or removes candidates.
- [ ] Keep the mutual-declaration behaviour: two files declaring each other must not empty the list. The guard
      inside `dropNamed` that returns the original list when the drop would empty it stays.
- [ ] Note in the comment above `choose` that the order is declaration, then subtraction, then ranking, and that
      each tier is allowed to decide only what the tier above left open.

#### Verification

- Fixture: branch carries `booking` (recording `ignores: booking-tests`) and `booking-tests` (recording
  `base-changeset: booking`). The answer is `booking`. Today it is `booking-tests`, and the reason it is not is
  worth a sentence in the commit message: the author's declaration was consulted last.
- Existing `ignores:` tests in `internal/changeset/resolve_test.go` stay green.

### M3 - rank by the commit that added the directory, and only when ranking is needed

#### Deliverables

- Candidate order follows the commit that added each changeset directory, not the commit that last edited one, so
  editing a parent's `ABOUT.md` on a child branch no longer re-points the child's answer.
- No history walk happens unless more than one candidate is still standing.
- Two directories added by the same commit are a tie, and the tie is reported instead of being broken by whoever
  edited something afterwards.

#### Tasks

- [ ] Replace `distanceFromTouch` with the add-commit form: `git log -1 --format=%H --diff-filter=A <rev> --
      changesets/<id>`, then `rev-list --count <add>..<rev>`. Keep the `-1` meaning: a directory the revision's
      tree carries but this line never added, which is what a graft or a shallow boundary gives, ties with other
      absences rather than inventing an order.
- [ ] Make the read lazy. `nearness` currently fills `Distance` for every candidate whenever there are two or
      more, before `choose` decides anything. Move the call so it runs only for the candidates that survive the
      earlier tiers, and only when more than one of them survives.
- [ ] Update the prose that describes the measurement. The comment above `nearness`, the PRD sentence about the
      commit that last touched a directory, and any test name that says "nearest" all describe an edit fact that
      is going away.
- [ ] Cache the add commit per id for the life of one command, in the same place `changeset.Reads` caches tree
      reads, so a command that asks twice does not walk twice.

#### Verification

- The regression fixture from the probe: stacked child, then a commit touching the parent's directory last. The
  child is selected with the recorded id present, and also with `base-changeset:` removed from the file, which is
  the case where distance is the only evidence left.
- A fixture where one commit adds two changeset directories: the result is ambiguous, both ids named.
- A cost assertion: a revision carrying one unlanded changeset performs zero `git log` calls for candidate
  ranking.
- Measured cost recorded in the commit message: 2 to 3 ms per directory at 484 commits, one walk per surviving
  candidate.

### M4 - `base:` with an always-recorded `base-changeset:`

#### Deliverables

- A stacked changeset's `CHANGESET.yaml` records `base:` and `base-changeset:` together, always, written by
  `init` without the author naming the parent changeset.
- `stackOf` keeps the recorded id when only `base:` is present, so the id reaches `choose` instead of being
  discarded.
- `stackParentID` returns that id, which makes the ancestor drop in `choose` fire for `base:`-recorded stacks for
  the first time, and `withoutRecordedAncestors` in `internal/cli/init.go` is deleted along with the test written
  only for it.
- `parent:` and `parent-changeset:` stay readable. `base:` together with `parent:` stays an error, as it is now.

#### Tasks

- [ ] Add `BaseChangesetKey = "base-changeset"` beside `ParentChangesetKey`, and read either spelling into
      `Stack.ParentChangeset`. Rename that struct field to say what it now holds, and rename its accessor to match.
- [ ] `renderMetadata` (`internal/changeset/changeset.go`) writes the id whenever it writes a `base:` that names
      stacked work, and never writes `parent:`.
- [ ] `init` records both from the base it can see, reusing `parentChangesetOn`. Keep the refusal to guess when
      the base carries two or more active changesets, and keep the warning that names every candidate.
- [ ] `applyBases`: a candidate with `base:` and a recorded id is stacked, so it gets the derived measurement
      base. Check the tests that assert a recorded stack does not move when the parent branch gains a changeset;
      that behaviour is now reached by `base:` plus an id rather than by `parent:`, so the fixtures change shape
      and the assertion must stay.
- [ ] Leave every landed file on trunk untouched. Add a fixture, not a migration: a landed `parent:` file must
      still resolve.

#### Verification

- The probe's linear-stack fixture, as a real test: candidates shrink to `[child]`, selected with no ambiguity,
  where today they stay `[child, parent, grandparent]`.
- Fixtures for the two spellings of the id key and for the mixed-error case (`base:` with `parent:`).
- `git pair check` on a stacked changeset whose `base:` names a branch carrying a different changeset id than the
  recorded one: a recommendation naming both, printed through the `recommend:` surface, never a reason that gates
  the merge.
- `mise run gates` green, including the docs contract tests.

### M5 - the set operation is the primary rule

#### Deliverables

- `choose` decides in this order: landedness (already applied in `Resolve`), then `ignores:`, then subtraction of
  ids that other candidates record as their base changeset, then ranking by the add commit, then a refusal that
  names every candidate still standing.
- One candidate remaining after subtraction is selected without any history read.
- A candidate naming itself is not a parent edge. Subtraction of everything, which a mutual record causes, leaves
  the candidates in place and reports them.

#### Tasks

- [ ] Port `activeBySubtraction` from `probe/setop-selection` into `choose`, and place it after the `ignores:`
      pass from M2.
- [ ] Exclude self-edges when collecting the ids to subtract, the way `dropNamed` does.
- [ ] Keep the existing guard that refuses to empty the list.
- [ ] Port the probe's fixtures into `internal/changeset/resolve_test.go` under names that say what each one
      proves, and delete the probe branch once they are in.
- [ ] State in the comment above `choose`, and in the PRD, that `Candidates` is the set that remains after
      parents are subtracted. `dropNamed` has always shrunk that list; after this milestone it shrinks it for
      every recorded stack, so `check --json` and `status` report fewer ids on a stacked branch, by design.

#### Verification

- All eight probe shapes as assertions, each with the candidate list checked as well as the selection.
- A test that the ranking read does not happen when subtraction decides: assert the invocation count, next to the
  existing stack-chain cost test.
- The full suite green with no fixture changed to accommodate the new order, except where a fixture was asserting
  the defect fixed in M3.

### M6 - the PRD says the rule in this order

#### Deliverables

- PRD §21 describes the stack link as one authored pair, `base:` plus `base-changeset:`, with the older
  spellings readable and no longer written.
- PRD §13.1 states which rules decide the active changeset and in what order, that a tie is reported rather than
  broken, and that an author's declaration outranks an inference.
- The gate scripts count the git invocations a command makes for a stacked branch, so a future change that adds a
  read has to say why.

#### Tasks

- [ ] Rewrite the passages in place rather than appending. The old text names `parent:` as the authored link and
      describes selection by the most recently touched directory; both are wrong after M4 and M5.
- [ ] Extend the docs contract tests to require the new key name wherever a document shows a stacked
      `CHANGESET.yaml`, so a stale example fails the build.
- [ ] Add the invocation-count check to `scripts/gates/ci-integrate.sh` in the shape `feat/destination-from-tree`
      used for its trap replay: a check that fails for the right reason, verified by breaking it once.

#### Verification

- `mise run gates` green, and the CI job green on the offered branch.
- Reading the PRD from §13 forward, a person can reproduce the order in `choose` without reading Go.
