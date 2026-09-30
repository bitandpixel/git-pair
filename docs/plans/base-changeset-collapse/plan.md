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
- A branch carries at most one unlanded changeset, plus the directories of the changesets it is stacked on. Two
  unlanded changesets that do not form a chain cannot be offered, and cannot be merged.

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

**Status:** done. This branch carries the document and nothing else.

#### Tasks

- [x] Nothing beyond the document. Every later milestone is its own changeset, and this stack's order is the
      order above.

#### Verification

- `mise run gates` green, for what that covers. `docFiles` in `internal/cli/docs_contract_test.go` reads the PRD,
  the README, and every skill page, and does not read `docs/plans/`, so a plan is not gated like code and this
  document's first draft claimed it was. The exclusion is right: `TestCommandsNamedInTheDocsExist` fails a document
  that names a command the code does not have, and a plan exists to name commands that do not exist yet. What a
  milestone promises has to reach the PRD and the README when it is implemented, which is M9's job for this stack.
  What gates this branch is the ordinary thing - the build, the suite, and the scripts - and the reviewer.

### M2 - `ignores:` outranks every inference

**Status:** landed in main at `71c55bc`, as `feat-ignores-before-inference`. The order is asserted by
`internal/changeset/ignores_precedence_test.go`, whose first fixture fails with the passes in their old order.

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
a branch that already carries one is subtracted from unless the author says otherwise. M6 removes this shape
altogether, so the precedence rule here is what keeps the tool honest while the cohabitant shape is still
possible, and for as long as any file still carries the key.

#### Tasks

- [x] In `choose` (`internal/changeset/resolve.go`), move the `dropNamed` pass over `Candidate.Ignores` ahead of
      everything else that reorders or removes candidates.
- [x] Keep the mutual-declaration behaviour: two files declaring each other must not empty the list. The guard
      inside `dropNamed` that returns the original list when the drop would empty it stays.
- [x] Note in the comment above `choose` that the order is declaration, then subtraction, then ranking, and that
      each tier is allowed to decide only what the tier above left open. The comment names declaration before the
      stack link today; subtraction joins the sentence in M5.

#### Verification

- Fixture: branch carries `booking` (recording `ignores: booking-tests`) and `booking-tests` (recording
  `base-changeset: booking`). The answer is `booking`. Today it is `booking-tests`, and the reason it is not is
  worth a sentence in the commit message: the author's declaration was consulted last.
- Existing `ignores:` tests in `internal/changeset/resolve_test.go` stay green.

### M3 - rank by the commit that added the directory, and only when ranking is needed

**Status:** landed in main at `b4b5c6f`, as `feat-rank-by-add-commit`. The two ranking fixtures fail under the
last-edit measurement and pass under the add-commit one, with and without the recorded id in the child's file.

#### Deliverables

- Candidate order follows the commit that added each changeset directory, not the commit that last edited one, so
  editing a parent's `ABOUT.md` on a child branch no longer re-points the child's answer.
- No history walk happens unless more than one candidate is still standing.
- Two directories added by the same commit are a tie, and the tie is reported instead of being broken by whoever
  edited something afterwards.
- Where the records contradict each other the answer is no selection and the candidate list, not whichever directory
  the ordering happens to like. This is new behaviour, found by a test that used to pass by accident:
  `TestResolveMutualIgnoresKeepsBothCandidates` asserted ambiguity because both directories happened to be last
  edited by one commit. Under the add-commit measurement that tie disappears and the ordering would have answered a
  contradiction the author wrote. Creation order is not what they meant.

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
- [ ] Deferred, and recorded here rather than dropped: cache the add commit per id for the life of one command, in
      the same place `changeset.Reads` caches tree reads. Two things make it not this milestone's work.
      `changeset.Reads` is on `feat/destination-from-tree` and not on main, so a cache written now lands in a second
      place and moves when that branch merges. And no command walks the same revision twice today: `init` resolves
      the parent branch, which is a different revision, and `Resolution.WithIgnores` is a simulation that ranks
      nothing. The cache has no consumer until a caller asks about one revision through two paths.

#### Verification

- The regression fixture from the probe: stacked child, then a commit touching the parent's directory last. The
  child is selected with the recorded id present, and also with `base-changeset:` removed from the file, which is
  the case where distance is the only evidence left.
- A fixture where one commit adds two changeset directories: the result is ambiguous, both ids named.
- A cost assertion: a revision carrying one unlanded changeset performs no candidate ranking read at all. The
  assertion counts both shapes the read takes - the commit that added the directory, and the walk from it to the
  revision - because a lazy path makes neither, and counting one of the two would let a stray read through.
- The same assertion for a stack whose recorded link decides the branch. This is the case that used to pay: the
  walk ran over both directories before the filter reduced them to one.
- `internal/gittest/spawn.go` gains `SpawnShimLines`, which returns the invocations in order as well as the count.
  Counting cannot tell a command that skipped a read from one that made a read and did not need it.
- Measured cost recorded in the commit message: 2 to 3 ms per directory at 484 commits, one walk per surviving
  candidate.

### M4 - `base:` with an always-recorded `base-changeset:`

**Status:** offered as `feat-base-changeset-always-recorded`. One thing beyond the tasks below: `check` now also
recommends when the recorded id and the `base:` name different changesets, which is the case the id makes possible -
before this milestone an id recorded beside a `base:` was read by nothing.

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

- [x] Add `BaseChangesetKey = "base-changeset"` beside `ParentChangesetKey`, and read either spelling into
      `Stack.ParentChangeset`. Rename that struct field to say what it now holds, and rename its accessor to match.
- [x] `renderMetadata` (`internal/changeset/changeset.go`) writes the id whenever it writes a `base:` that names
      stacked work, and never writes `parent:`.
- [x] `init` records both from the base it can see, reusing the helper that was `parentChangesetOn` and is now
      `baseChangesetOn`. Keep the refusal to guess when
      the base carries two or more active changesets, and keep the warning that names every candidate.
- [x] `applyBases`: a candidate with `base:` and a recorded id is stacked, so it gets the derived measurement
      base. Check the tests that assert a recorded stack does not move when the parent branch gains a changeset;
      that behaviour is now reached by `base:` plus an id rather than by `parent:`, so the fixtures change shape
      and the assertion must stay.
- [x] Leave every landed file on trunk untouched. Add a fixture, not a migration: a landed `parent:` file must
      still resolve.

#### Verification

- The probe's linear-stack fixture, as a real test: candidates shrink to `[child]`, selected with no ambiguity,
  where today they stay `[child, parent, grandparent]`.
- Fixtures for the two spellings of the id key and for the mixed-error case (`base:` with `parent:`).
- `git pair check` on a stacked changeset whose `base:` names a branch carrying a different changeset id than the
  recorded one: a recommendation naming both, printed through the `recommend:` surface, never a reason that gates
  the merge.
- `mise run gates` green. The docs contract tests check command names and ref paths in the PRD, README and skill,
  not key names, so nothing in them notices a changed spelling of a field. The fixtures that read the old spellings
  are the check that matters here, and M9 adds the key-name assertion this one cannot have.

### M5 - the set operation is the primary rule

**Status:** landed as `3764473` from `feat-set-operation-primary-rule`. The review decided the one open question: the set
operation is the *only* subtraction. The older pass also removed whatever a candidate's `base:` looked like it
named, which decided a stack only when a branch happened to be spelled like its changeset; a file with no recorded
id now falls through to the ranking instead, which answers - usually the same way - and says so by leaving both
candidates in the list.

#### Deliverables

- `choose` decides in this order: landedness (already applied in `Resolve`), then `ignores:`, then subtraction of
  ids that other candidates record as their base changeset, then ranking by the add commit, then a refusal that
  names every candidate still standing.
- One candidate remaining after subtraction is selected without any history read.
- A candidate naming itself is not a parent edge. Subtraction of everything, which a mutual record causes, leaves
  the candidates in place and reports them.

#### Tasks

- [x] Port `activeBySubtraction` from `probe/setop-selection` into `choose`, and place it after the `ignores:`
      pass from M2. It arrives as `subtractRecordedParents`, over the one field the rule reads.
- [x] Exclude self-edges when collecting the ids to subtract, the way `dropNamed` does.
- [x] Keep the existing guard that refuses to empty the list.
- [x] Port the probe's fixtures under names that say what each one proves, and delete the probe branch once they
      are in. They went into `internal/changeset/set_operation_test.go` rather than `resolve_test.go`, which is
      already the largest file in the package; the eight shapes are one rule's tests and read better together.
- [x] State in the comment above `choose`, and in the PRD, that `Candidates` is the set that remains after
      parents are subtracted. `dropNamed` has always shrunk that list; after this milestone it shrinks it for
      every recorded stack, so `check --json` and `status` report fewer ids on a stacked branch, by design.

#### When the tie-break is still reached

M4 records the id on every file the tool writes, and M6 leaves one legal shape: a chain. Subtraction reduces a legal
branch to a single candidate, so the ranking does not run. Two populations still reach it, and both are records
subtraction cannot read rather than rules that disagree:

- An unlanded file whose link is in a spelling subtraction cannot match: `base:` naming a branch, or no link at all.
  M4 changes the writer, and no milestone rewrites a file that is still unlanded, so every changeset created before
  M4 keeps that shape until it lands. On such a branch the ranking is the only rule that decides anything.
- A branch in the shape M6 refuses. `change ready` and `check` refuse it, but `status`, `ls` and `diff` still read
  it, and a guess from the add order keeps those commands usable while the author decides which exit to take. Where
  the add commits tie, these branches answer "ambiguous" and name the exits.

So the tie-break outlives the invariant by design, and the condition for deleting it is stated with it: it goes when
no unlanded file can lack a subtraction-readable id, which means the pre-M4 corpus has landed or a rewrite of
unlanded files has been added. Deleting it earlier turns those branches from a guess the author can see through into
a refusal naming two ids. That is the honest end state, and it is one this plan does not prepare the tree for.

#### Verification

- All eight probe shapes as assertions, each with the candidate list checked as well as the selection.
- A test that the ranking read does not happen when subtraction decides: assert the invocation count, next to the
  existing stack-chain cost test.
- The full suite green with no fixture changed to accommodate the new order, except where a fixture was asserting
  the defect fixed in M3.

### M6 - a branch carries one unlanded changeset, plus the ones it is stacked on

**Status:** landed as `020d7dc` from `feat-one-changeset-per-branch`. The invariant. M7 is what the author does
when it is violated.

Three readings of this milestone's text changed while it was being written, and each is the shipped behaviour:

- **The rule runs on the set the filters leave, not on the set before them.** `Resolution.Candidates` is what
  survives `ignores:` and the set operation, and what survives those two is exactly the set of things nothing else
  claims to be stacked on - the tops. One top is one stack. The ancestors subtraction removed are recovered by
  reading when the answer needs them, because a chain can pass through a changeset that landed and left the candidate
  list. A rule written over the pre-filter set would have to redo the subtraction to ask the same question.
- **A file with no recorded id falls back to a read of the branch its `base:` names**, not to the add commit: the
  reader asks which changeset directory that branch carries, and calls that the parent. The add commit cannot answer
  this question - two siblings joined the line in a different order and are still two siblings. The read runs only on
  the path about to refuse, so a branch whose records say it is one stack touches no extra history, and it is what
  keeps `feature/auth` carrying `feature-auth` from being refused for an id not spelled like its branch.
- **The self-referential base is refused where the branch is known**, which is the session the commands read: it is a
  gate reason in `check` and `integrate` as well as a refusal in `status` and `change ready`. `stackOf` has no branch
  to compare against, so the same claim written there would need the branch passed down and would answer a question
  about a revision nobody asked about. `init`'s write-time refusal is unchanged.

The refusal text named only commands that existed at the time, so `change combine` and `change stack` were described
rather than named: a reader sent to a command that does not answer yet learns nothing. M7 landed both, and the
message names them now. The list was written in two changesets for that reason, and the fixtures assert the names -
a refusal that names no command is the failure this milestone exists to prevent.

#### Why the shape arises

There is one cause: work from another branch arrives on this one. A plain merge or a pull of a shared branch brings
the directory and its history; a squash merge or a rebase-and-squash brings the directory in one ordinary
single-parent commit; a cherry-pick of the commit that added it brings it alone; a revert of the commit that deleted
it brings it back. The first is visible in the history and the rest are not.

Three things that look like causes are not. Branching from a branch that carries two only propagates the shape, and
that branch can only have acquired it from the cause above - propagation is still why enforcement belongs at
`change ready` and `check` and not only at `init`, but it is not something to solve. A landedness disagreement is
temporary: a changeset that landed after the branch point reads as active until the clone fetches, and the fetch
answers it. A directory left behind by landed work is not a second active changeset at all, because a changeset the
destination carries is landed and the resolver never offers it.

#### Deliverables

- `git pair init` on a branch that already carries an unlanded changeset refuses, naming the id it found and the
  ways out. The warning at `init.go:175` becomes this refusal.
- `git pair change ready` refuses a branch whose unlanded directories do not form one chain, naming every id and
  printing the fixes: declare them a stack (M7), combine them (M7), take the path back out, or `git fetch` when the
  second directory may simply have landed since the branch point.
- `check` reports the same condition as a reason that gates the merge, so the CI job cannot land an implicit stack
  where a second changeset arrives in the destination with no approval of its own.
- The self-referential base is refused where the file is read, not only where it is written.
  `BaseIsOwnBranch` resolves the base through `rev-parse --symbolic-full-name`, so every spelling of the branch
  counts, and `init` already refuses it: "The base would move with every commit, so the changeset could never
  contain anything." Nothing checks a file edited afterwards, where the same shape makes the comparison the branch
  against itself and leaves the reviewer an empty diff.

#### Tasks

- [x] State the shape once, in the resolver. Let U be the unlanded directories the set operation leaves. The branch
      is offerable when U has one member, or when one member reaches every other through recorded
      `base-changeset:` edges.
      A stack on one branch is not a shape the tool can write: `init` refuses a `base:` naming the branch the
      changeset lives on. An ancestor therefore either arrived by branching from the branch that carries it, which
      is a stack, or it came from an edit after the fact, which the read-time guard below answers. The clause is a
      check on hand-edited files, not the rule that keeps ordinary use honest.
- [x] Reuse the chain the set operation already walked, so the rule adds no read. When a file has no recorded id,
      the add commit from M3 is the fallback evidence.
- [x] Key the refusal on the candidate set, not on the selection. The warning at `init.go:173-176` reads
      `res.Selected`, so when the branch already carries two unrelated changesets the selection is nil, the condition
      does not fire, and `init` of a third is silent. The case that needs the message most is the one it currently
      says nothing about.
- [x] `init`: refuse, and point at the flag that declares the stack, which after M4 writes `base:` together with
      `base-changeset:`.
- [x] Move the `BaseIsOwnBranch` check into the reader, beside `ErrParentWithBase` in `stackOf`, keeping the
      message `init` gives and adding what a reader needs: the file the value came from.
- [x] Do not guess where the directory came from. The parent count of the commit that added it distinguishes a plain
      merge from work made here, but not from a squash merge, a rebase-and-squash, or a cherry-pick, all of which
      arrive as ordinary single-parent commits. The message states both exits and the way back, and the author knows
      which one applies.
- [x] Name the way back out in the reason text, with the command shape: take the path back to where it was before the
      work arrived (`git restore --source=<ref> -- changesets/<id>`, then commit). That is the answer when the
      directory is somebody else's work, and it is cheaper than either combine or stack.
- [x] Suggest `git fetch` first when the second directory might have landed since the branch point, because a stale
      destination ref imitates this shape exactly and a fetch dissolves it.

#### Verification

- A negative fixture for the transient case: a second directory the destination already carries is landed, so the
  resolver never offers it and no refusal fires. The test asserts the absence of the refusal, because a fetch is the
  author's answer and a message telling them to combine would be wrong.
- The refusal text is asserted, not merely its presence, so it cannot rot back into a bare "two changesets" error.
  It has to name both exits by command name, since an author who is not told the two answers cannot pick one.


### M7 - the two exits: `change combine` and `change stack`

**Status:** offered as `feat-two-exits`. This is where the M6 refusal stops being a dead end.

Four readings of this milestone's text, each the shipped behaviour:

- **`combine` names the survivor and the pair is the branch.** `--into <id>` picks which changeset survives; the
  disappearing one is the other unlanded directory on the branch. There is no `--from`, because a branch that carries
  the invariant's shape has exactly two candidates and naming one of them fixes the other. A branch carrying three is
  refused with the list and the instruction to fold a pair first.
- **The review-state guard is any marker, read where `check` reads it.** `combine` refuses while either changeset has
  a lifecycle marker or a review, from the same `lifecycle` summary the gate reads, so the two cannot disagree about
  whether a changeset is in review. It also refuses when the two changeset directories are dirty, because the command
  commits them; work elsewhere on the branch is not its business.
- **`stack` does not need a resolved changeset.** It computes both sides itself, because the shape it repairs is one
  the resolver may refuse: a branch carrying two unconnected directories has no selection to load a session from.
- **A thread copy is named by the disappeared id.** `Threads` answers from the repository root, so the copy takes the
  base name prefixed with that id, and the report says replies continue in the copy while the archived original stays
  where the archive holds it.

#### Why two exits

The M6 refusal describes a shape and cannot say which answer fits: the second directory is either this branch's own
work sitting on top of another changeset, or separate work that arrived and belongs somewhere else. Provenance is
not recoverable from history (M6), so the tool states both and lets the author choose. The command is `combine` and
not `merge`, because landing already uses merge language for bringing commits into the destination and the two acts
must not read as one.

#### Deliverables

- `git pair change combine --into <id> [--threads]` folds one changeset into another. The disappearing directory
  moves whole into `changesets/<into>/.combined/<id>/` - nothing is deleted. The survivor keeps its own `base:` and
  `base-changeset:` and prints which of the target's links it kept and which it dropped. Its `ABOUT.md` gains one
  line pointing at the archive rather than a copy of the target's prose. It refuses while either changeset is
  offered or under review, because a reviewer's diff must not change underneath them.
- `git pair change stack --base <branch>` records the stack link between the changeset this branch carries and the
  one `<branch>` carries, writing `base:` and `base-changeset:` into the child's file and nowhere else.
- An archive under `.combined/` is inert to every reader: it answers no id, is neither landed nor active, and is not
  offered as a thread. The current tree already behaves this way and M7 keeps it that way - `changesetDir` accepts
  metadata only at exactly `changesets/<id>/CHANGESET.yaml` and rejects any further slash; `dirsUnder` lists
  immediate children only; `Threads` reads a directory non-recursively and skips directories; landing and tidy move
  whole directories.

#### Tasks

- [x] `combine`: move the disappearing directory whole, and keep the survivor's own two keys. The survivor's
      comparison is its own base, and folding a changeset in is not a licence to move the base underneath a review.
- [x] `--threads` copies, it does not move, and it prefixes each copied file with the disappeared id so two
      changesets that both have a thread called `scope.md` do not collide. Say which copy replies continue in. A
      thread file is not an implementation change (`lifecycle` excludes it from the comparison a review marker
      makes), so the copy cannot move what a reviewer is comparing.
- [x] `change stack --base <branch>` compares two sets that the resolver already computes: the unlanded changesets of
      this branch and of `<branch>`, each read as `ActiveIDs` of the branch minus `LandedIDs` of the destination. Let X
      be their intersection minus the base branch's recorded ancestors, and Y this branch's set minus the other's. One
      member each, or a refusal that prints both sets.
- [x] Exclude the base branch's recorded ancestors, not this branch's. On a third level of a stack this branch carries
      the grandparent as well, and it cannot be told to exclude its own ancestors, because naming them is the fact the
      command is about to write. The base branch already has that chain recorded, so the exclusion is read from there.
      Where the base branch records no chain the intersection has two members, and the refusal says to settle the level
      below first.
- [x] Refuse, printing both sets, for each shape the two conditions do not accept. Nothing in common means `<branch>`
      is not this branch's base, and the answer is a `base:` naming the integration branch rather than a stack. Two not
      in common is the shape `change combine` answers, so name it. `<branch>` naming this branch is refused by
      `BaseIsOwnBranch`, and it means the child was created on the branch it wants to sit under: create a branch for it
      and run the command there.
- [x] Write the two keys into the child's file and nothing else. The common changeset's own record is not touched - its
      `base:` belongs to its branch, and this branch holding a copy of its directory is what every stacked branch looks
      like. Assert that the command's commit changes one path.
- [x] Print the old value and the new one for both keys, then say that the branch has to be offered again. A base that
      changes is the comparison changing, so the reviewer's diff changes with it. The command does not refuse because a
      ready marker is present; `check` already refuses a merge over a commit that followed the marker, so the drift is
      caught whichever way the author leaves it.

#### Verification

- Two fixtures, one per exit: two changesets where one is the other's recorded ancestor, repaired by the stack exit,
  with a test asserting nothing outside `changesets/` changed; two unrelated changesets combined by `change combine`,
  where the survivor keeps its `base:`, gains the pointing line, and the disappeared directory is whole under
  `.combined/`.
- Tests that an archive cannot become live, one per reader: a `CHANGESET.yaml` at `changesets/x/.combined/y/` answers
  no id to `Resolve`, does not appear in `LandedIDs` or `ActiveIDs`, and is not offered as a thread of `x`. They are
  three rules that have to stay in agreement, so three assertions.
- A fixture for nesting: combining a survivor that already holds an archive keeps both archives inert.
- Fixtures for the conditions: a two-level case, one change in common and one not, recorded in one commit that touches
  only the child's `CHANGESET.yaml`; a third level, where two directories are in common and the base branch's recorded
  chain removes the grandparent, so the deepest link is still written; a base branch with no recorded chain, refused
  with the message naming the level to settle first; nothing in common, refused with the integration branch named; two
  changesets not in common, refused with `change combine` named; and `--base` naming this branch, refused.
- A fixture for `--threads`: the survivor lists the copied thread, the archived copy is still in place, and a colliding
  stem comes out disambiguated by the prefix.
- A fixture for each review-state guard: a ready marker on either changeset makes both exits refuse.
- Each refusal's text is asserted, so neither exit can rot into a message that names no command.


### M8 - `ignores:` loses its remaining case

#### Deliverables

- Nothing writes `ignores:` any more. `git pair change use` is refused, or narrowed to clearing a record written
  before this milestone.
- Files that carry the key are still read, so a record someone made keeps explaining the decision behind it. This
  repository has 29 `CHANGESET.yaml` files and none of them records the key, which is why the writer can go without
  a migration.

#### Tasks

- [ ] Decide with the reviewer, in this milestone rather than earlier, whether `change use` is deleted or kept to
      clear old records. The plan does not assume an answer; the invariant in M6 removes the only shape the command
      could not otherwise decide.
- [ ] Cut the PRD paragraphs that describe the key as live behaviour in the same commit that removes the writer, so
      the documentation and the command move together.
- [ ] Keep the drop pass in `choose` while any file can still carry the key, which keeps M2's precedence rule live
      for as long as the pass exists.

#### Verification

- `git pair change use` on a clean branch says why it did nothing.
- A fixture carrying the key still selects the declaring changeset, so reading it is not lost by accident.


### M9 - the PRD says the rule in this order

#### Deliverables

- PRD §21 describes the stack link as one authored pair, `base:` plus `base-changeset:`, with the older
  spellings readable and no longer written.
- PRD §13.1 states which rules decide the active changeset and in what order, that a tie is reported rather than
  broken, and that an author's declaration outranks an inference.

- PRD states the branch shape rule: one unlanded changeset plus the ancestors it is stacked on, what each refusal
  says, and why a second changeset on the same branch is an implicit stack rather than a second unit of work.
- PRD describes the exits in the same terms the refusal prints them, so a person reading the manual and a person
  reading the error are given the same three ways out.
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
