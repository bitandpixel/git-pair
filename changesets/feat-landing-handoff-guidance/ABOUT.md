# feat-landing-handoff-guidance

## Summary

The landing guidance is written by hand in too many places, and it has drifted.

Two surfaces spell the parent's landing step in two different wordings — the child's waiting sentence and
`git pair change integrate`'s refusal — and three documents quote the waiting sentence, so any change to the
landing contract has to be made in five places or those places begin to disagree with each other. This
changeset makes one builder own the wording of a landing step, makes every other surface name the changeset
and point at the screen that prints it, and adds the second exit — hand the merge to the destination's owner
with `git pair change integrate` — to the screens a human reads.

The consolidation is the substance; the hand-off line is what makes it necessary. With one exit, repeating a
sentence three times is merely wasteful. With two, every repetition has to carry both.

Committed so far: the plan and its two research notes. No behaviour has changed yet.

## What changed

-   `docs/plans/landing-handoff-guidance/plan.md` — four milestones: advice that names commands this build
    answers to; refer-don't-restate; the hand-off line gated by the same predicate `change integrate` uses;
    and one canonical PRD statement with the documentation's quotes pinned by a test.
-   `docs/plans/landing-handoff-guidance/research/2026-10-14-handoff-line-when-declaration-refused.md` — the
    two variants written out against real output from the landed pair, which is what decided the narrow rule:
    the wide one offers `git pair change integrate` in two states where that command exits 1, including a
    `READY` changeset that has never been reviewed.
-   `docs/plans/landing-handoff-guidance/research/2026-10-14-advice-that-names-a-missing-flag.md` — two
    emitted sentences name `git pair change ready --changeset <id>`, and `change ready` has no `--changeset`
    flag. The note records which commands do carry it, why `change ready` deliberately does not, and the
    correction (plain `git pair change ready`; the two-changeset shape already has its own refusals).

## Design decisions

**A surface that does not own a step refers the reader to the one that does.** The child's waiting sentence
will name the parent and point at `git pair status --changeset <parent>` instead of spelling the parent's
`check` and merge; `change integrate`'s refusal will do the same, keeping the part only it can say — that a
declaration asks for an unattended merge, and the human path stays open. The test for the change is an
absence: the refusal contains no `git pair check`.

**`next_action` stays one instruction.** The hand-off goes to the human forms of `status` and `check`, where a
second line costs a parser nothing. Machine consumers already branch on `state`, `ready` and `integrating`; a
fork inside a sentence would make them parse advice.

**The offer and the acceptance read one predicate.** The screens print the hand-off exactly when
`change integrate` would accept the changeset, by calling the test that command already applies before writing
a marker. The equivalence is the milestone's acceptance test, run over four states.

**The line sits after the `git pair check` the sentence already names,** so a gate refusal is covered by the
step in front of it. That is what lets `status` decide from the facts it holds — verdict, staleness, what is
below, whether the head is already declared — instead of paying for a content comparison it does not currently
run, which the per-ancestor cost test would notice.

**No coupling to whoever merges.** git-pair names the step and the destination, never the actor: the hand-off
line says "main's owner", not a forge, a workflow file, or a job. Detecting the automated path — reading a
workflow, a repository variable, a check run — is excluded deliberately, because it would be wrong in forks,
self-hosted runners and monorepos, and because a workflow existing is not a workflow merging.

**Advice gets the check prose already has.** `docs_contract_test.go` proves the PRD, the README and the skill
name commands that exist, both directions. Emitted advice is assembled with `fmt.Sprintf` and is invisible to
it, and a test that pins the string verbatim will pin a command that fails — which is how the `--changeset`
sentences survived. The new test resolves each command against the tree and its flags against *that* command,
not against the union of all of them, since `--changeset` existing somewhere is precisely the mistake made.

## Validation

Nothing to validate yet beyond the plan's own claims, which the two research notes support with captured
output and `--help` transcripts. Each milestone states its verification; the two that matter most are
cross-checks rather than unit tests — offer ⇔ acceptance for M3, and quote ⇔ builder for M4 — because both
exist to stop two sources drifting, and a test on one source alone cannot see that.

## Known limitations

-   The cheap declarability predicate cannot see a content-comparison refusal, so a green-looking `APPROVED`
    whose content drifted could show the hand-off line before `check` refuses it. Mitigated by the ordering
    (the hand-off comes after the `check` the same sentence names); the fallback, if it proves confusing, is to
    print the line only on `check`'s verdict, where the gate actually ran.
-   M4's quote test protects four sentences. A fifth added later by copying prose rather than referencing the
    builder is outside its reach; the mitigation is the rule itself — a surface refers, or it owns.

## Open questions

-   Whether `git pair queue` should refer to the parent's advice the same way M2 does for `change integrate`.
    Its `parent_notes` currently own a housekeeping sentence, which is a different voice, so the answer may be
    "no, and the difference is the point".
-   Whether the hand-off line belongs in `change ready`'s acknowledgement after all. The plan says no — the
    predicate is false at that moment by construction, since a changeset just offered has no verdict — and
    that reasoning is recorded in M3's tasks so it is not re-litigated by the next editor.
