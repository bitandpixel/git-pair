# feat-next-action-fixes

## Summary

`git pair check` stopped printing, on a passing verdict, a command whose only effect on that verdict was to
make it fail.

An approved child of a freshly-landed parent used to be told, on the same line that said it was
integration-ready, to rebase onto the landing. The author ran it, the tree came out byte-identical, and the
gate then refused: `review af7ffc8 reviewed 7c3e3b4, which is no longer in this history`. Both rules are right
separately — an approval certifies a commit, and a child sitting on a landed parent's branch does have a
restack available — but the advice never asked whether following it costs the approval the verdict is resting
on.

The comparison that answers that question already existed and already ran when the parent's branch had been
deleted. It now decides the advice on both sides, and the surfaces that print it are told apart: a verdict
withholds a rewrite it would have to refuse afterwards, and an observation keeps it.

This is `docs/plans/completed/landed-parent-restack-advice/plan.md`, implemented as written. That plan was
landed plan-only by `feat-landed-parent-advice`; this changeset is its M1 and M2, and it closes with M3
deleted for want of a spike that earned it.

## What changed

-   `internal/cli/stacked.go` — `parentStatus` gained `Step` and `ApprovalSurvivesLanding`. A landed parent now
    writes the fact into `Note` and the command into `Step` instead of gluing them into one sentence, so a
    surface can print one without the other. `landedApprovalStillStands` records the comparison's outcome when
    the content came out equal and the branch merges cleanly, and `advisesRestack()` beside `owesLandingStep()`
    decides whether the command is put in front of the author. On the branch-present side, an approval that
    recorded no measurement is now said out loud when its step is given.
-   `internal/cli/check.go` — the verdict's `parent:` line and `next_action` consult `advisesRestack()`;
    when it withholds the step the line carries the comparison sentence instead, and when it gives the step
    without having compared, it says so. `check --json` gained `parent_approval_survives_landing`.
-   `internal/cli/status.go` — `parent.step` is the command and `parent.note` the fact, printed as `step:` and
    `note:`. `status` keeps advising the step wherever `owesLandingStep()` says one is owed, which is what
    keeps the command readable for anyone who wants the tidier history.
-   `queue` is unchanged: its housekeeping voice carries no verdict, so it advises on the fact alone.
-   Tests — `restack_advice_test.go` covers the six end-to-end cases the plan's M1 asked for; the two
    predicates are pinned separately in `stacked_internal_test.go`; and three tests that asserted the old
    advice were re-pointed at what they now describe. The plan is updated with the spike results and its
    execution status, and moved under `docs/plans/completed/`.
-   Docs — the PRD's parent-landing section, the README's stack prose and `check --json` field list, and the
    skill's agent-facing rule.

## Design decisions

**A fact and an instruction are two fields.** `owesLandingStep()` is a statement about where commits sit, and
`advisesRestack()` is a statement about what following the command would cost. Collapsing them is the mistake
that made the bug, so they are separate methods with separate tests, and the case where they disagree — an
approval standing on content the rewrite would invalidate — is named in both.

**Withholding the step does not withhold the finding.** The `parent:` line on a passing verdict prints the
comparison sentence the read already composed ("parent alpha landed as …; measured above the landing, this
branch still contributes the content review … approved"), so the missing command is an answer rather than a
gap. `check --json` carries the same fact as `parent_approval_survives_landing`.

**Where the comparison could not be reached, the step is still given, and says it is unverified.** Silence
about a step is worse than a step the reader can decline; destroying an approval by accident is worse than a
step the reader had to decline. The direction of each failure is written into the PRD so the next editor does
not "improve" it into quiet.

**A head already on the landing keeps its step.** What is left there is deleting a branch that holds nothing
the destination does not, which invalidates nothing — and `git branch -D` is the step the parent-landing work
was written to surface.

**The JSON companion decision the plan asked for.** `parent_comparison` plus `next_action` cannot tell
"withheld because the comparison came out equal" from "not offered because there was nothing to compare": the
first is `comparison: "contribution"` with no `git rebase` in the sentence, and the third is no landing at all,
which `parent_landed` already answers — but a reader would have to reconstruct the predicate to be sure. So
`check --json` gained one boolean, `parent_approval_survives_landing`, rather than a second sentence.
`status --json` gained `parent.step` for the same reason in the other direction: `status` prints the command
and the note apart, and a JSON reader needs both halves to reproduce what the human sees.

**Two sentences that misadvised were corrected on the way.** A refusal for a conflicting merge now names its
own step in the reason, as every other parent refusal does — the step was being computed and thrown away. And
a child already standing on the landing, whose content the comparison found changed, is told to re-offer
itself with `git pair change ready --changeset <id>` rather than being handed a branch deletion glued to "and
have the result reviewed again".

**git-pair runs none of it.** The rebase, the delete and the merge stay ordinary git performed by whoever owns
the branches (PRD §26); what moved is which of them a verdict is willing to name.

## Validation

-   The six end-to-end cases from the plan: approved with a plain-merge landing (ready, no `git rebase` in
    `next_action`, the comparison on the `parent:` line, exit 0); the same fixture after running the withheld
    step (refused for the rewritten history, which is the choice the author can still make); a rewritten child
    over a squashed landing (refused, with the re-offer and the re-review); a child that would conflict with
    the destination (the conflicting path named, and `git merge main && git pair change ready --changeset …`);
    a child with no approval (the step on `status`, `queue`, and `check` under `--allow-feedback`); and an
    approval with no recorded measurement (the step given, with the unverified clause).
-   Unit tests for the two predicates, so a housekeeping fact and an instruction cannot be merged back
    together by the next simplification.
-   `mise run check` (gofmt, vet, the sharded suite including the docs contract and the hygiene scan), and the
    scripted gates: `mise run gates`.
-   One behaviour change was made deliberately rather than discovered: `TestCheckPassesAChildOnALandedParent`
    and `TestChildOfALandedParentIsToldWhereTheWorkWent` asserted the step on an approved child. Those tests
    encoded the bug; their expectations were re-pointed and their comments say which case they now cover.

## Known limitations

-   The withheld step is withheld from `check` only. A reader who wants it finds it in `status` and `queue`;
    there is no flag to ask the verdict for it, because a verdict that can be talked into printing a
    self-invalidating command is the thing that just shipped.
-   A child whose parent's branch is gone and whose head is not on the landing gets no command from `check`.
    Its prose lives in the reason, and the command the old code printed named a deleted branch in
    `git rebase --onto`.

## Open questions

-   Plan spike 2 — whether carrying a parent's commits through to the landing misleads `change tidy`, the
    queue's wait measurement, `history`, or the review span — was not asked, because nothing here depends on
    the answer. It is recorded as open in the plan rather than assumed away.
