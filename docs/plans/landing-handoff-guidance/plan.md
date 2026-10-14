# Landing hand-off guidance

## Goal

A changeset that may be merged says so once, in one place, and a surface that does not own a step refers the
reader to the surface that does.

Three lines of guidance are currently written by hand in more than one place, and they have drifted:

- the child's waiting sentence spells the parent's landing step in full;
- `git pair change integrate`'s refusal spells the same step a second way;
- two pieces of advice name a flag that this build does not answer to.

This plan makes one builder spell a landing step, makes every other surface name the changeset and point at
the screen that prints it, adds the hand-off alternative (`git pair change integrate`, then push) where a
human reads a next step, and gets a test that keeps the documentation's quoted sentences true.

## Success criteria

Observable, not implementation details:

1. `git pair status` and `git pair check`, in their human forms, name a second exit next to the author's own
   merge — the hand-off — for a changeset that `git pair change integrate` would accept, and do not name it for
   one it would refuse.
2. `git pair change integrate` accepts exactly the changesets those screens offered the hand-off for. One test
   asserts both from one fixture, so the offer and the acceptance cannot drift.
3. `next_action` in every `--json` document stays a single instruction: one string, no fork, no second
   sentence appended.
4. A stacked child's next step names the parent and points at the parent's own advice. It contains no
   `git rebase`, and no spelled-out landing steps for the parent.
5. Every command string git-pair emits as advice — `next_action`, `next:`, `step:`, `note:`, a refusal
   `reason` — names a command and flags this build answers to. A test proves it, and it fails today on
   `git pair change ready --changeset`.
6. The PRD states the landing contract once. The README and the agent skill quote each emitted sentence at
   most once, and a test compares those quotes with what the builders print, so a sentence that changes in
   code fails a documentation test rather than rotting quietly.

## Context

Landed last week, and the reason this is now visible as one problem rather than three:

- `feat-next-action-fixes` — a passing verdict no longer advises the restack that would invalidate the
  approval it rests on, and the fact (`parent.note`) and the command (`parent.step`) became separate fields so
  a surface can print one without the other.
- `feat-stacked-landing-advice` — the landing sentence is read from two facts (where the work is going, and
  whether something below has to land first), and a child of an unlanded parent is told whose step is next.

Both were verified against a real stacked pair in this repository, and the readback surfaced the remaining
rough edges this plan picks up.

### The inventory

Every place a landing or a restack step is spelled today. "Restates" means the surface writes out a command
belonging to another changeset or another screen; "owns" means it is the one place.

| Where | What it spells | Today |
| --- | --- | --- |
| `landingNextAction` (`landing_advice.go`) | this changeset's own merge, with its destination and provenance note | owns it |
| `landingNextAction`, waiting form | the **parent's** `check` and merge, spelled in full | restates |
| `unlandedParentReason` (`integrate.go`) | the **parent's** "Land the parent first: `git pair check`, then merge with ordinary git" | restates, differently |
| `stackUnderStep` (`stacked.go`) | the child's restack command | owns it |
| `parentLanded` notes and reasons (`stacked.go`) | "parent landed as …; rebase onto …", the comparison finding | owns the finding |
| `queue` parent note (`queue.go`) | the landed-parent housekeeping line | owns its housekeeping voice |
| PRD §21 "The next step says which landing is owed" | the waiting sentence, quoted in a fenced block | quotes it |
| README's stack prose | the waiting sentence, inline | quotes it |
| `skills/git-pair/references/cli.md` | the waiting sentence again | quotes it |
| `skills/git-pair/SKILL.md` | the order ("land the bottom of the stack first") | prose, no command |

Two consequences. The parent's step exists twice, in two wordings, so a change to the landing contract has to
be made in both or it silently disagrees with itself. And the waiting sentence's quote is repeated three times
in documentation, which is the verbosity the consolidation is meant to remove: with the hand-off exit added,
each of those quotes would have to carry both forms of "how this lands".

### The two variants, decided

The hand-off line appears **only when the changeset is declarable** (decided by demonstration, not taste). The
alternative — always printing it — was mocked up on real output and refused twice: for a child whose parent has
not landed, and for a `READY` changeset that has never been reviewed, where the screen said "wait for a
reviewer" and two lines below it said the merge was available. `git pair change integrate` exits 1 in both,
with a reason that explains what the screen should have said. See
`research/2026-10-14-handoff-line-when-declaration-refused.md`.

## Constraints

- **git-pair runs no merge and names no actor** (PRD §26, §13.4). The hand-off line names the destination's
  owner as the one who merges; it must not name a forge, a workflow file, a runner, or a job. Any guidance that
  depends on reading CI configuration is out of scope, and the reason belongs in this plan, not in a code
  comment someone deletes.
- **`next_action` is a machine contract.** It stays one string with one instruction. The hand-off goes to the
  human screens, where a second line costs nothing and a parser nothing.
- **Screens stay cheap.** `status` must not grow a gate run to decide whether to print a line:
  `TestStatusStackChainCostsABoundedReadPerStep` bounds what it may add per ancestor, and the hand-off line is
  not worth a content comparison.
- **The refusal is the teacher.** Where a step is withheld, the surface that would have performed it says why
  (`change integrate`'s reasons, `check`'s `reasons:`). Withholding must not mean silence.
- Prose in `PRD.md`, `README.md`, and `skills/` is checked material (`docs_contract_test.go`); a change here
  that quotes a sentence must be able to prove the quote.

## Assumptions

- Declarability for a *screen* is the state the screen already has (a verdict that permits integration, the
  head not stale, nothing below unlanded, no declaration covering this head) and not a fresh gate run. The
  hand-off line follows the `git pair check` the same sentence already names, so a check that would refuse is
  covered by the step in front of it. If that proves too subtle in use, the fallback is to print the line only
  on `check`'s verdict, where the gate actually ran.
- The right advice where a branch carries more than one unlanded changeset remains what `change ready` already
  says about that shape; M1's fix to the two `--changeset` strings must not invent a selector that does not
  exist. Verify against `change ready`'s own multi-changeset behaviour before choosing wording.

## Milestones

### M1 — Advice this build answers to

#### Deliverables

- Every `git pair …` string git-pair emits as advice runs.
- A test that fails if a future sentence names a command, subcommand, or flag the command tree does not have.
- The two `git pair change ready --changeset <id>` strings corrected to a form the command accepts.

#### Tasks

- [x] Spike: run the two broken paths and record the emitted text and the failure —
      `stacked.go:358` (head already on the landing, contribution never read) and `stacked.go:600` (the
      conflicting merge with the destination). The command itself is already verified in the research note;
      what is left is the end-to-end fixtures, so the fix is tested where it is emitted rather than at the
      builder.
- [x] Fix the wording in both places, choosing the command from what `change ready` actually accepts, and
      saying what the reader must do about a second changeset on the branch if that is the shape they are in.
- [x] Add the contract test in the `cli` package: collect the emitted advice strings from a fixture set (or
      from the builders directly, where they are reachable), extract each `git pair …` token, and validate it
      against `commandPaths(newRootCommand(&app{}))` and that command's declared flags — the machinery
      `docs_contract_test.go` already uses for prose.
- [x] Decide where the strings live well enough to be collected: an exported-for-test registry is not worth it;
      a fixture-driven sweep of `status`, `check`, `queue`, `change integrate` and `change wait` output is,
      because it also covers strings assembled at runtime.

#### Verification

- The new test fails on the pre-fix tree and passes after (prove both, in that order, in the commit message).
- `mise run check`.

### M2 — Refer, do not restate

#### Deliverables

- One builder owns the wording of a landing step; no other surface spells another changeset's steps.
- The child's waiting sentence names the parent and points at the parent's own advice.
- `git pair change integrate`'s unlanded-parent refusal refers the same way instead of spelling `check` and
  the merge.

#### Tasks

- [x] Rewrite the waiting form in `landingNextAction` to
      `waiting on parent <name>: its landing is owed first (`git pair status --changeset <name>` names the step),
      then `git pair check` and merge into <destination>`. Keep one clause for the parent, one for this
      changeset, and no `git rebase`.
- [x] Rewrite `unlandedParentReason` to name the parent and point at its advice, keeping the two things the
      refusal owns and the sentence does not: why a declaration is refused where `check` is not, and that the
      human path stays open. Keep the `--set-parent` clause for the parent-with-no-recorded-changeset case,
      where there is no parent screen to point at.
- [x] Land a decision, in a code comment, on what "its advice" means when the parent's own screen is not
      readable from here (a parent branch deleted with no landing): point at the reason the reader does have.
- [x] Update the tests that pin the current waiting text (`landing_advice_test.go`,
      `restack_advice_test.go`, `parent_landed_surfaces_test.go`) to the new sentence, keeping every assertion
      that names an absence (`no "merge into alpha"`, no `git rebase` in a verdict's `next_action`).

#### Verification

- One fixture, four surfaces (`status`, `check`, `change wait`, `review submit`), the waiting sentence
  identical, and none of the four naming a parent's branch as a merge target or spelling the parent's merge.
- `change integrate` on the same fixture refuses, and the reason contains no `git pair check` — the proof that
  the restatement is gone rather than moved.
- `mise run check`.

### M3 — The hand-off line

#### Deliverables

- `status` and `check` name the second exit for a declarable changeset, in their human forms only.
- The screens and `change integrate` agree by construction: one predicate decides declarability, both read it.
- `--json` output unchanged, asserted.

#### Tasks

- [x] Extract the predicate `change integrate` already applies before writing a marker — verdict permits
      integration, head not stale, nothing below unlanded, no declaration covering this head — and have both
      `change integrate` and the screens call it. Keep it cheap: the facts a screen already holds, no gate run.
- [x] Print, as a second `Next:` line on `status` and a second line under `check`'s `next:`, the hand-off form:
      `or hand it to <destination>'s owner: `git pair change integrate`, then `git push origin <branch>``.
      The destination's name comes from the same contract the merge line uses, so the two cannot name different
      branches.
- [x] Print nothing in the states the demonstration excluded: never reviewed, stale head, unlanded parent,
      already declared.
- [x] Do not add the line to `change ready`'s acknowledgement, and record why in the commit: the predicate is
      false there by construction, since a changeset that was just offered has no verdict to integrate on.
- [x] Tests: the offer and the acceptance from one fixture (offer present ⇔ `change integrate` accepted); the
      four excluded states; `--json` byte-unchanged on `status` and `check`; and the hand-off line passing the
      M1 command test.

#### Verification

- One fixture each for the four states, with `change integrate` run against every one: the screens offer the
  hand-off exactly where it exits 0 and stay silent where it exits 1. That single equivalence is the
  milestone's acceptance test.
- `mise run gates`, which includes the pty walkthrough — the human screen is what changed.

### M4 — One statement, checked quotes

#### Deliverables

- The PRD states the landing contract once, actor-neutral, with both exits.
- The README and the skill quote each emitted sentence at most once.
- A test compares the quoted sentences in the documents with what the builders print.

#### Tasks

- [x] Make PRD §21's "The next step says which landing is owed" the one canonical statement: the two facts, the
      two exits, "git-pair names the step and the destination, never the actor or the tooling", and the rule
      that a surface which does not own a step refers the reader to the one that does. Fold in the sentences
      that currently repeat it — §9.5, §11.1, §11.3 keep their local claims and link instead of restating.
- [x] Update the three quotes of the waiting sentence (README's stack prose, `references/cli.md`, the PRD's
      fenced block) to M2's wording, and add the hand-off line where the README documents `next_action`.
- [x] Add the quote test: for each sentence a document is expected to carry, build it from the builders and
      require the document to contain it. Name the documents the way `docFiles` does, so a new reference page
      is covered without anyone remembering.
- [x] Record in `references/integration.md` that the automated path is one shape of the destination owner's
      decision, with the pointer to `scripts/ci/git-pair-integrate.sh` — and no new claim about what a runner
      does.

everything is marked complete in this plan, are all the milestones in this single changeset?
#### Verification

- The quote test fails when a builder's wording changes and the document does not (prove it by editing one
  word and re-running).
- `mise run check`, which runs the docs contract tests.

## Spikes and research

| Question | Where | Status |
| --- | --- | --- |
| What does a screen that always offers the hand-off do when the declaration refuses? | `research/2026-10-14-handoff-line-when-declaration-refused.md` | answered — it offers a step that exits 1 in two states; "only when declarable" chosen |
| Do the two `change ready --changeset` strings actually fail, and what should they say? | `research/2026-10-14-advice-that-names-a-missing-flag.md` | answered — `--changeset` is not a flag on `change ready`; plain `git pair change ready` is the correction |
| Does `change ready` have wording for a branch carrying more than one unlanded changeset, that M1 can reuse? | same note | answered — `change stack` and `change combine` already refuse that shape with their own exits, so the advice does not need any |

## Risks

- **The screen offers a hand-off the gate then refuses.** Mitigated by phrasing the line after the
  `git pair check` the sentence already names, and by the M3 equivalence test for the four states the predicate
  can actually see. Residual: a content-comparison refusal inside a green-looking `APPROVED` is not visible to
  the cheap predicate.
- **Cost creep on `status`.** Mitigated by the existing per-ancestor bound; M3 adds no reads, only a decision
  over facts the screen holds.
- **The quote test becomes a chore** and someone edits the test instead of the document. Mitigated by quoting
  each sentence at most once, so there are four quotes in total to keep, and by making the failure message name
  the builder that produced the sentence.
- **Losing the reason from `unlandedParentReason`.** The refusal carries a rule the sentence does not (a
  declaration asks for an unattended merge; the human path stays open). The M2 assertion is that it stops
  restating a step, not that it stops explaining the rule — keep the sentence about the merge, drop the command.

## Verification strategy

Unit tests for the builders and the declarability predicate. End-to-end tests on fixtures through the real
commands for every state, since these are screens an agent reads. Two cross-checks that matter more than the
unit tests: offer ⇔ acceptance in M3, and quote ⇔ builder in M4. `mise run check` for the suite including the
docs contract, and `mise run gates` — including the pty walkthrough, because the human form of `status` is what
this plan changes.

## Audit History

| Date | Audit | Summary |
| --- | --- | --- |
| — | — | none yet |
