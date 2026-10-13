# feat-landed-parent-advice

## Summary

`git pair check` can print, on a passing verdict, a command that makes that verdict fail. This changeset is
the plan that stops it.

`feat-hooks-runner` was APPROVED with its parent freshly landed. `check` said `ready: true` and, in the same
answer, `next_action` said to rebase onto the landing. I ran it — the tree came out byte-identical — and the
gate then refused: `review af7ffc8 reviewed 7c3e3b4, which is no longer in this history: the branch was
rewritten since the review, so the approval does not license integration`. Restoring the approval meant
resetting the branch back.

Both rules are right separately: an approval certifies a commit, and a child still sitting on a landed
parent's branch genuinely has a restack to do. The advice just never asks whether following it costs the
approval the verdict is resting on. The plan is that condition, and it is small because the comparison that
answers it already exists — it runs when the parent's branch has been deleted, and not when the branch is
still here, which is the ordinary case right after a landing and the one that fired.

Plan-only. No behaviour changes in this changeset.

## What changed

-   `docs/plans/landed-parent-restack-advice/plan.md` — the plan: goal, success criteria, the code map, two
    implementation milestones and a third that exists only if a spike earns it, four spikes, risks,
    verification strategy.
-   `docs/plans/landed-parent-restack-advice/research/2026-10-13-approved-child-advised-to-rewrite.md` — the
    transcript, the two colliding rules, and the table of which function holds which half.

## Design decisions

**The plan changes the condition on the advice, not the gate's refusal.** The refusal is the correct half of
the incident: an approval certifies a commit, and accepting a rewritten commit silently would accept a state
nobody read. The advice is the wrong half, because a tool that hands you a command whose effect on the
verdict is to break it has made the two halves of its own answer disagree.

**One decision for a landed parent, reached from both parent paths.** When the parent's branch is gone, the
code already measures the child's contribution above the landing, compares it with the identity the approval
recorded, checks the branch still merges cleanly, and then chooses: note or reason, step or no step. When the
branch is still present, the step is printed on a predicate about where the head sits. The plan routes both
through the comparison, so the presence of a branch cannot change advice about identical content. That is a
small diff with a real effect: it removes the asymmetry rather than special-casing the side that was reported.

**Advice is a function of consequence, not of geography.** `owesLandingStep` stays a statement about commit
positions, because that is the housekeeping fact `status` and `queue` need. The instruction to rewrite gets its
own predicate — false exactly when an approval is the newest marker, the contribution above the landing equals
what it recorded, and the merge is clean. The plan pins the two separately in tests, since a fact and an
instruction will otherwise be edited apart.

**Withholding the step must not withhold the finding,** and today it does: `check` prints the step on the
`parent:` line and thereby drops the sentence the read composed about the comparison coming out equal. The plan
puts the comparison back on the verdict and appends the step only where the predicate allows. `status` and
`queue` keep the step — those are the surfaces where a stale parent branch is the subject, and no verdict can
be hurt by what they print.

**When the content question cannot be answered, advise and say so.** The fall-throughs are already named in the
code: no recorded identity, an approval older than the trailers, an unreadable measurement. There the step is
printed with the clause saying it is unverified. The plan states the direction of each failure explicitly —
silence about a step the reader needed is worse than a step they can decline, and destroying an approval by
accident is worse than a declined convenience — so the next editor does not "improve" the fallback into
quietness.

**No automatic restack, no new flag, no change to what an approval licenses.** git-pair rewrites nothing and
moves no ref (§26), so the step remains a command the human runs; the exit-code contract and the JSON shape
stay, with the plan requiring the `--json` key set to be compared before and after rather than quietly grown.

## Validation

-   Documentation only, so the risk is a plan that does not survive contact with the code. Every file,
    function and line the plan names was read in the tree it lands on: `parentSinceApproval`, `parentLive`,
    `parentGone`, `parentLanded`, `landedApprovalStillStands`, `landedWorkUnchanged`, `landedMergeIsClean`,
    `landedParentStep`, `owesLandingStep`, and `check.go`'s two print sites.
-   The transcript in the research note is the real output of the real sequence, including the tree hash that
    did not change and the refusal that followed.
-   Success criterion 2 (squash landing still refuses and still advises) is stated as a non-regression
    requirement precisely because it is the behaviour that already works and is easiest to break with a
    suppression rule aimed at the wrong case.
-   The plan's M1 task 5 includes the case where the advice is followed, expecting the refusal: the point is
    that the author sees the command as a declinable choice, not as a trap.
-   `mise run check` — result recorded below.

## Known limitations

-   The plan asserts that the measured contribution above the landing is what the gate would compute with the
    destination as base. Spike 3 is written to find a counterexample rather than to confirm; if one exists,
    M1's predicate consults the gate's own measurement instead of a second one.
-   Whether anything actually needs the child to take the destination in before merging is open (spikes 1 and
    2). If the answers are all "no", the third milestone is deleted, and the stale-branch tidiness is left to
    `status` and `queue` — which may leave landed parent branches alive longer than they are today.
-   This plan does not touch the deleted-branch side's own wording; it unifies behaviour under one decision
    function, and the strings on that side may still be reworded by whoever lands M1.

## Open questions

-   Should the withheld step be recoverable from `check` on request rather than living only in `status` and
    `queue`? The plan says no new flag; a reader who wants it has a surface that prints it. If that turns out
    to be friction, the answer is a wording change in the note, not a flag.
-   Does the same self-invalidating-advice shape exist anywhere else — any `next_action` whose execution
    changes the verdict that carried it? Worth one pass over the other producers during M1 rather than a
    separate plan.
