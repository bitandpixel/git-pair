# Do not advise a rewrite that costs the approval

## Goal

`git pair check` stops printing, on a passing verdict, a command whose only effect on that verdict is to
make it fail.

A child whose parent has landed is told so, and is given the step that takes it onto the destination —
**unless** the standing approval still measures the same work above the landing and the branch merges
cleanly. In that case the rewrite is housekeeping with nothing behind it, following it destroys the approval
the verdict rests on, and the answer says so instead of offering the command.

Observed on `feat-hooks-runner` on 2026-10-13; the transcript, the code map, and the two rules that collide
are in `research/2026-10-13-approved-child-advised-to-rewrite.md`.

## Success criteria

Observable outcomes, not implementation:

1. A changeset that is APPROVED, whose parent landed by a plain merge, whose parent branch is still present,
   and whose contribution above the landing measures equal to what the approval recorded: `check --json`
   answers `ready: true`, and `next_action` contains no `git rebase`. The human verdict prints one
   `parent:` line that says the comparison came out equal, rather than a step.
2. The same changeset after a squash or rebase-merge landing, where the content under the approval differs:
   the gate refuses integration, names the landing, and gives the rebase-and-have-it-reviewed-again step.
   This is today's behaviour on the vanished-parent side and it must not regress.
3. The same changeset when it would conflict with the destination: a refusal naming the conflicted paths and
   the `git merge <destination> && git pair change ready --changeset <id>` step. Unchanged behaviour, now
   reachable from the branch-is-present side too.
4. A child with no standing approval — READY and never approved, or WORKING — still gets the step on every
   surface. Following it there destroys nothing, and the stale parent branch is a real thing to clear.
5. Where the content question cannot be answered (no recorded identity, an unreadable measurement), the step
   is still given, with the reason it is given unverified. Silence about a step is worse than a step the
   reader can decline; destroying an approval by accident is worse than a step the reader had to decline.
6. Every surface that prints the step agrees with the others on what it means, and the strings that changed
   are the strings the README and the skill carry.
7. `mise run check` green, including the docs-contract tests and the hygiene scan.

## Non-goals

- **No automatic restack.** git-pair rewrites nothing and moves no ref (PRD §26). The step stays a command
  the human runs; this plan changes when it is offered, not who runs it.
- **No change to what an approval licenses.** The `Review-Head` / `Review-Diff-Id` identity rules, and the
  refusal when the reviewed commit is no longer in the history, stay exactly as they are. The gate's refusal
  is the correct half of the incident; the advice is the half that was wrong.
- **No new command and no new flag.** This is a wording-and-conditioning change on existing output.
- **Not the parent-branch deletion.** `git branch -D <parent>` and the worktree blocker it prints are the
  same plan's other remedy and stay put.
- **Not the queue's housekeeping voice.** A queue row carries no verdict, so nothing it prints can invalidate
  an approval; it keeps advising (criterion 4), and that decision is stated rather than left to drift.

## Context

The parent-landing surface — the `parentStatus` read, `landedParentStep`, the stale-branch and worktree
findings — shipped on 2026-09-24 as a six-milestone plan (`docs/plans/parent-landed-detection/plan.md`,
implemented on `feat/parent-landed-impl`). Its M3 deliberately spelled the remedy as a command because
"rebase onto it" was being mistranslated. That plan predates the approval-survival comparison, and the two
have never been joined on the side where the parent's branch is still present.

The comparison already exists and already answers this question. When the parent's branch is **gone**,
`parentGone` → `landedApprovalStillStands` measures the child's contribution above the landing
(`landedWorkUnchanged`, which prefers the identity the approval recorded and falls back to comparing the two
bases' trees), asks whether the branch still merges cleanly (`landedMergeIsClean`), and writes: a note and no
step when the work is the same, a reason plus the rewrite-and-re-review step when it is not, and a conflict
reason plus the merge step when it would conflict.

When the parent's branch is still **present** — the ordinary case immediately after a landing, and the one
this incident hit — `parentLanded` writes the step unconditionally, gated only by `owesLandingStep`, which is
a statement about where the head sits. `check` then appends it to `next_action` on a ready verdict
(`check.go:210-216`) and prints it as the `parent:` line, which also displaces the note the read composed
(`check.go:280-285`).

## Constraints

- The exit-code contract: 0 success, 1 business refusal, 2 usage, 3 git failed. A gate refusal is exit 1 in
  the human form and exit 0 with `ready: false` in JSON. Nothing here may make `ready` and the exit code
  answer different questions.
- `--json` arrays are `[]`, never null.
- The hygiene scan binds shipped source: nothing may invoke `rebase`, `push`, `merge`, `reset`, `switch`,
  `checkout` or branch management. Printing a command is allowed today because the literal is a sentence, not
  the bare verb — new sentences must keep that property, and a new bare `"rebase"` literal would fail the
  build.
- Documentation is checked prose: every leaf command is named in `PRD.md`, `README.md` and
  `skills/git-pair/references/cli.md`, and the README carries transcripts that must match the bytes the
  commands print.
- One spelling: `landedParentStep` is the single place the step is spelled, read by `status`, `check` and
  `queue`. A change that adds a second spelling of the command is worse than the bug.

## Assumptions

- The measured contribution above the landing is the right test for "would `check` fail with the new
  destination". It is the same measurement the gate itself uses, so it should agree with the gate by
  construction; the spikes check that rather than assume it.
- A plain-merge landing that brought nothing new leaves the child's contribution unchanged. Observed in the
  incident (identical trees) and asserted by existing tests on the vanished-parent side.
- The parent's branch being present is not itself a reason to advise anything; it is the reason the _old_
  code path was taken.

## Design

**Ask the content question once, and ask it regardless of whether the parent's branch survived.** The
landed-parent case has one decision function — the one `landedApprovalStillStands` already is — reached from
both `parentLive` and `parentGone`. Whether the branch is still there decides which _read_ supplies the
landing, not whether the child's content is compared. This removes the asymmetry instead of patching one
side of it, and it means a parent deleted after the landing and a parent branch left in place cannot give
different advice about the same content.

**Advice is a function of consequence, not of geography.** "The head is not on the landing commit" is a fact
about commit positions, and `owesLandingStep` should stay that statement — it is the housekeeping predicate.
The decision to _tell the author to rewrite_ gets its own predicate, and it is false exactly when all of:
an approval is the newest marker; the contribution above the landing equals what that approval recorded; and
the branch merges cleanly into the destination. Where those hold, the step is withheld on the verdict, and the
note says what it is: the parent landed, this head is still measured on it, and nothing is owed until the
merge happens.

**Withholding the step must not withhold the finding.** The incident's second half is that `check` printed the
step and dropped the comparison. The `parent:` line on a passing verdict prints the comparison sentence
(already composed, with the `Comparison` value naming which reading answered), and the step appears only where
the predicate allows it. A reader who wants the command for tidiness still finds it: `status` and `queue` keep
it, because those are the surfaces where a stale branch is the subject rather than the verdict.

**When the question cannot be answered, advise and say the answer is unverified.** The fall-throughs are the
ones `landedApprovalStillStands` already names — no recorded identity, an approval older than the trailers, an
unreadable measurement. In those cases the step is printed with the reason it is being given without the
comparison, because the failure the plan protects against (a self-invalidating command on a ready verdict)
requires knowing that the content is unchanged, and an unknown is not that knowledge.

## Milestones

### M1 — One decision, both parent paths — **done**

Deliverables

- A landed parent produces the same advice on the branch-present side as on the branch-gone side, for the
  same content facts.
- A ready verdict never carries a `git rebase` step that would invalidate the approval it rests on, and says
  why the step is absent.
- The comparison sentence reaches the human verdict.

Tasks

1. Route the landed-parent case in `parentLive` through `landedApprovalStillStands`, with the same advice
   strings the vanished-parent path uses; stop `parentLanded` composing a step of its own.
2. Add the advice predicate beside `owesLandingStep`, with the three conditions above, and make
   `check.go`'s two print sites use it. Keep `landedParentStep` as the only spelling of the command.
3. Keep the note when the parent has landed: the `parent:` line prints the note (which the comparison wrote)
   rather than replacing it with the step, and the step is appended only when the predicate is true.
4. Where the comparison could not be reached, print the step plus the clause naming that gap, reusing the
   wording the vanished-parent path already uses for an approval that recorded nothing.
5. Tests, end to end with `gittest` fixtures that land a parent and leave the branch present:
   - approved, plain-merge landing, contribution equal → `ready: true`, no `git rebase` in `next_action`, the
     `parent:` line states the comparison, and `check --json` exits 0;
   - the same fixture, step taken (`git rebase --onto <landing> <parent> <child>`) → the gate refuses with the
     rewritten-history reason, which is the regression this plan exists for: it must be visible as a choice
     the author can decline, not a trap;
   - approved, squash landing (content differs) → refusal plus the rebase-and-re-review step;
   - would conflict → the conflict reason and the merge step;
   - READY, never approved → the step still printed on `check`, `status` and `queue`;
   - approval older than the trailers (no recorded base) → the step printed with the unverified clause.
6. A unit test beside `stacked_internal_test.go` for the two predicates, so `owesLandingStep` and the advice
   predicate are pinned separately — a housekeeping fact and an instruction are different things and will be
   edited apart.

Verification

- The six CLI cases above, run through the built binary in a scratch repository as well as in fixtures, since
  the incident was a real sequence: parent merged into trunk, `check`, rebase, `check`.
- Existing parent-landing tests stay green without edit wherever their fixtures describe a case the plan does
  not change (deleted branch, head already on the landing, stale-branch deletion). Where a test asserted the
  step on an approved-equal child, the test is the thing that was wrong and its expectation is updated with a
  comment saying which case it now covers.
- `go test ./internal/cli/ -run Docs` and `-run TestTheCommandTreeIsTheCommandSet` green; `mise run check`
  green.

### M2 — The contract says which surface advises what — **done**

Deliverables

- The PRD states the rule: the restack step is offered when following it costs nothing standing, and withheld
  on a verdict that an approval is resting on, with the finding printed in its place.
- `README.md` and `skills/git-pair/references/cli.md` match the new output, including the transcript that
  shows a landed parent.
- A reader who wants the command for tidiness can find where it still prints.

Tasks

1. PRD: the parent-landing section gains the condition and the reason, in the section that already owns the
   approval-is-a-claim-about-content rule, so the two rules are read together rather than two documents
   apart.
2. README: the `check` and `status` rows (and any transcript showing `parent: … landed as …`) updated to the
   new bytes; the wording says plainly that the step is withheld while an approval stands and where to get it
   anyway.
3. Skill `references/cli.md`: the same rule in agent-facing terms — an agent reading `next_action` must not run
   a restack that invalidates the approval; the queue/status surfaces are where the housekeeping is stated.
4. Decide the JSON companion explicitly: prefer keeping the shape and letting `parent_comparison` plus
   `next_action` carry it; add a key only if a machine cannot tell "withheld because equal" from "not offered
   because not applicable". Record the decision either way, in the changeset's `ABOUT.md`.

Verification

- The docs tests pass, and the README transcript is regenerated from a fixture run rather than written from
  the shape of the output.
- `git pair check --json | jq 'keys'` compared before and after, to show what changed in the contract.

### M3 — Only if the spikes find a dependency — **deleted**

Deliverable: the housekeeping has a moment that is actually right — after the merge, or on the surface where
it costs nothing — instead of relying on the verdict to prompt it.

Not taken. Spike 1 answered "no" — nothing requires the child to take the destination in before it merges —
and the design already puts the housekeeping where it costs nothing: `status` and `queue` keep printing it, and
both say it can run after the merge. There was no milestone here to earn.

## Spikes / research

Each answered one question; the conclusions are in `research/`.

1. **Does anything require the child to take the destination in before it merges?** **No.** Merging the
   un-restacked child into the destination and merging the restacked one produced the same tree in every
   landing shape — plain merge, merge over moved trunk, merge with a shared file, squash, and rebase-merge.
   The rebase changed the child's own tree only where trunk had moved, and added trunk's content, which the
   merge brought anyway. See
   [`2026-10-14-does-the-child-owe-the-destination-before-the-merge.md`](research/2026-10-14-does-the-child-owe-the-destination-before-the-merge.md).
2. **Does carrying the parent's commits affect `change tidy`, the queue's wait measurement, `history`, or the
   review span?** **Not asked.** No part of this change reads or writes those surfaces, and the implementation
   does not depend on the answer. It is recorded as open rather than assumed.
3. **Is the measured contribution above the landing always what the gate would compute with the destination as
   base?** **Yes, with one asymmetry that supports the design.** Where the comparison answered equal, the gate
   passed and the merge carried the reviewed content; no fixture was found where the two disagreed about a
   merge that should happen, so M1's predicate consults the measurement it has. The asymmetry: in the squash
   case the same content reads equal _until_ the author takes the step, and reads different afterwards, when
   the recorded base has stopped being an ancestor of the head and the reading falls through to the older
   comparison of two base trees.
4. **How often is the branch-present case the one that happens?** A landing is a merge into the destination and
   no git-pair command deletes the parent's branch, so the branch is present unless deleted by hand — the
   ordinary case, confirmed by the incident and by the fixture built for M1's tests.

## Risks

- **Suppressing the advice leaves stale parent branches alive.** The step stays in `status` and `queue`, the
  note names what is owed, and criterion 4 keeps it for unapproved children. Mitigated by design, tested by
  the READY case.
- **Two predicates drift apart.** `owesLandingStep` (a fact) and the advice predicate (an instruction) are
  pinned by separate unit tests, and the plan says why they are separate.
- **The comparison is unavailable and the tool guesses.** It does not guess: it advises and says the answer is
  unverified. The direction of each failure is stated in the Design section so the next editor does not
  "improve" it into silence.
- **Existing tests encoded the bug.** Some assert the step on an approved child. They are updated deliberately
  and visibly, with the reason in the commit message, rather than relaxed.
- **A reader who wanted the command can no longer get it from `check`.** Accepted, and stated in the docs: the
  command remains where a stale branch is the subject. The alternative — keeping it — is the incident.

## Verification strategy

Layered, in the order that catches a mistake cheapest:

1. Unit: the two predicates, and the note composition, in `internal/cli/stacked_internal_test.go`.
2. End to end: the CLI cases in M1's task 5, in fixture repositories built with `gittest`, asserting the JSON
   (`ready`, `reasons`, `next_action`) and the human lines, with `check --json`'s exit code asserted at 0 as
   the contract requires.
3. Manual, in a scratch clone: the exact incident sequence — parent merged into trunk with the branch left
   present, `git pair check`, the advice read, no rebase run, merge the child, and the queue's view after.
   A scratch run is also how the README transcript is regenerated.
4. The repo gate: `mise run check` (build, gofmt, vet, every package's tests including the sharded CLI and
   TUI ones, the docs contract, and the hygiene scan).

## Audit history

| Date       | Audit                      | Summary                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| ---------- | -------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 2026-10-14 | Spikes 1 and 3, then M1/M2 | Both measurement spikes answered in `research/`; spike 1 says the merge does not need the restack, spike 3 says the contribution measurement agrees with the gate and the one asymmetry argues for withholding. M1 shipped with the two predicates pinned apart in `stacked_internal_test.go`; M2 shipped with the PRD's parent-landing section, the README's stack prose and check-field list, and the skill's agent-facing rule. M3 deleted. |

## Execution status

Plan written on 2026-10-13 from the transcript in
`research/2026-10-13-approved-child-advised-to-rewrite.md`. Implemented on `feat/next-action-fixes` on
2026-10-14 and closed with M2; M3 was deleted for want of a spike that earned it.

What the code ended up with:

- `parentStatus.Step` and `parentStatus.Note` split the landed-parent sentence into the instruction and the
  fact, which is what lets `check` print one without the other. `landedParentStep` is still the only
  spelling of the command, and it is now called from one place.
- `parentStatus.ApprovalSurvivesLanding` is written only by `landedApprovalStillStands`, only where an
  approval is the verdict, and only when the content came out equal and the branch merges cleanly. The
  comparison sentence is the note it leaves behind.
- `advisesRestack()` beside `owesLandingStep()` is the instruction's predicate, tested beside the fact's in
  `stacked_internal_test.go`. `check` prints the step where it is true; `status` prints it whenever
  `owesLandingStep()` is.
- Two sentences that misadvised were corrected on the way, both in the same family: a refusal for a
  conflicting merge now names its own step in the reason, as every other parent refusal does, and a child
  already standing on the landing is told to re-offer rather than handed a branch deletion glued to "and
  have the result reviewed again".
- The branch-is-gone path never set `Step`, so a passing verdict no longer prints a rebase naming a parent
  branch that has been deleted. That command could never run, and `TestOwesLandingStep` and
  `TestLandedParentStepSaysNothingIsOwedWhenTheBranchIsGoneAndTheHeadIsOnTheLanding` already said so.

The JSON decision M2 asked for is recorded in the changeset's `ABOUT.md`: `check --json` gained
`parent_approval_survives_landing`, and `status --json` gained `parent.step`, because neither `parent_comparison`
nor `next_action` can tell "withheld because the comparison came out equal" from "not offered because there
was nothing to compare".
