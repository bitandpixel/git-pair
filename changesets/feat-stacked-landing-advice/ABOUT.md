# feat-stacked-landing-advice

## Summary

An approved child of a parent that had not landed was told to merge into the parent's branch — the one
destination `git pair change integrate` refuses to declare it onto.

```console
$ git pair status            # beta, APPROVED, stacked on alpha, alpha not merged anywhere
Next: `git pair check`, then merge into alpha with ordinary git
$ git pair change integrate
Cannot declare beta ready to integrate:
  - the parent alpha is not landed on the integration branch, so declaring beta would ask for a merge onto
    alpha — a branch review can still rewrite. Land the parent first …
```

The sentence came from the changeset's recorded base, which for a stacked child names the parent's branch.
That is the right answer for a diff and the wrong answer for a merge: a child lands where its parent landed,
and until the parent has, the child's own merge is the step the declaration refuses. Four surfaces printed it —
`status`, `check`, `change wait` and `review submit` — so all four now read the same two facts the refusal
reads: where the work is going, and whether something below it has to land first.

```console
$ git pair status
Next: waiting on parent alpha: land it first (`git pair check`, then merge into main with ordinary git),
      then `git pair check` and merge into main
```

This is the rough edge reported while landing the layer below it (`feat-next-action-fixes`), which fixed the
other half of the same sentence: what a child is told once the parent _has_ landed.

## What changed

- `internal/cli/landing_advice.go` (new) — `landingContract` holds the two facts a landing sentence states;
  `landingContractFor` reads them; `stackBelow`/`stackUnder` ask the one stack question (is there a parent,
  and does the destination carry its record); `landingNextAction` spells the sentence. It is the same rule
  `git pair change integrate` refuses on, read the cheap way.
- `internal/cli/status.go` — `landingNextAction` moved out of this file, and `nextAction` now takes the
  contract rather than `s.cs.Base`. Its three call sites (feedback, approved, integrating) are otherwise
  untouched, so the unstacked sentence is byte-for-byte what it was.
- `internal/cli/check.go`, `internal/cli/change.go`, `internal/cli/review.go` — the verdict's `next:`,
  `change wait`'s `next_action` (human and `--json`) and `review submit`'s `next:` now build the sentence
  from `landingContractFor` instead of the base. `change wait` and `review submit` read it once per
  invocation, not per poll.
- The child of a parent that _has_ landed was reading the same base, and printing the commit its diff was
  measured against as if it were a branch: `merge into refs/heads/main with ordinary git`, where `main` came
  from nowhere and no note said. It now prints the destination the destination rule resolves, with the note
  that names the parent it was walked through — `merge into main with ordinary git (where alpha landed)` —
  which is the line `change integrate` prints on its `merge into:`.
- Tests — `landing_advice_test.go`: the waiting sentence on all four surfaces, human and JSON; the unstacked
  control; the same child after the parent lands, asserted against `change integrate`'s own `merge into:`
  line; a record with `parent:` and no changeset id; a child with feedback rather than an approval; a
  three-deep stack; and a parent branch deleted before its work landed.
- Docs — the PRD's stacked-support section gained "The next step says which landing is owed", and the
  `next_action` paragraphs in §11.1 and §11.3 point at it; the README's stack prose and its `next_action`
  description; the skill's agent-facing rules (`SKILL.md`'s landing steps, `references/cli.md`'s two
  stacked-parent readings).

## Design decisions

**The recorded base is a measurement point, not a destination.** It is right for the diff and wrong for the
merge, and the changeset already knows this — `BaseDerived` says so for the case where the base was derived
from the destination. The new reading covers the other case: a base the changeset recorded itself, naming a
branch that is still there and still reviewable.

**The advice and the refusal are one rule.** `stackBelow` reproduces the condition `unlandedParentReason`
tests — a parent branch recorded, no landing commit in the destination — so no author is told to merge what the
declaration would refuse, and no author is refused a merge the sentence promised. It is checked, not assumed:
the landed-parent test asserts the next step and `change integrate`'s `merge into:` line name the same branch.

**The parent is asked where it is going, rather than walking the child's chain.** The child follows the parent
there, so it is the same answer, and the walk is what would make the cost grow with the depth of the stack.
`status` has a test that bounds what it may add per ancestor
(`TestStatusStackChainCostsABoundedReadPerStep`); this adds none, and `change wait` polls.

**The reading that advises does not fail the command; the readings that refuse do.** A next step is advice, so
a git error costs the detail and not the screen — the sentence falls back to what it said before the stack was
asked about. `check`'s gate and `change integrate`'s refusal ask the same questions of the same tree and return
the error, because a merge that goes ahead on an unreadable destination is the expensive direction.

**A parent whose landing cannot be shown keeps the waiting form.** A record that names a branch and no
changeset is the shape `init` no longer writes; git-pair cannot show that branch has landed, so the sentence
names the branch and says what is waiting. The alternative — assuming the parent is finished and handing out a
merge onto it — is the direction this changeset exists to remove.

**The gate still does not refuse an unlanded parent.** `check` answers "may this merge" for a person who can
merge a child onto an unlanded parent and stand behind the result (PRD §21). The refusal belongs to the
declaration, and the automation that cannot be talked into a bad merge is the one that asks for a declaration:
CI's gate is `jq -e '.ready and .integrating'` (§11.3), and an unlanded child cannot be given the declaration
that would make `integrating` true — `change integrate` refuses it. Verified on the fixture: `ready` true,
`integrating` false, before and after asking.

**git-pair performs none of it.** The parent's landing and the child's are ordinary git, run by whoever owns
the branches (PRD §26). What moved is that the sentence now says whose step is next.

## Validation

- The four surfaces for an approved child on an unlanded parent, human and `--json`, each asserting the
  waiting sentence and each asserting the absence of `merge into alpha`.
- The control that must not move: an unstacked approved changeset still ends its next step with
  `` `git pair check`, then merge into main with ordinary git ``, with nothing waiting.
- The pair that must agree: after the parent lands, `status`/`check`/`change wait` say
  `merge into main with ordinary git (where alpha landed)` and `change integrate` prints
  `merge into:  main (where alpha landed)`.
- The shapes that used to print a bare branch: `parent:` with no `parent-changeset:`, feedback instead of an
  approval, a three-deep stack, and a parent branch deleted with its work unmerged.
- `mise run check` (gofmt, vet, the sharded suite including the docs contract and the hygiene scan) and the
  scripted gates, `mise run gates`.

## Known limitations

- A stack three deep with both parents unlanded names the grandparent's branch as the parent's target: the
  sentence is correct about whose turn it is (the parent below) and reads that parent's own record for where
  it is going, which for an unlanded middle changeset is a branch. The parent's own advice, read while
  standing on the parent, is the waiting form.
- `git pair queue` keeps its own next-step lines. Its `parent_notes` already carry the landed-parent
  housekeeping, and no queue row printed the merge sentence, so nothing here changed it.
- The waiting sentence names one parent. A reader with a long stack learns the step below and can ask for the
  rest one screen at a time; the queue is the surface that lists a whole stack, and it is unchanged.

## Open questions

- Whether the waiting sentence should reach `change ready`'s acknowledgement as well. `change ready` prints
  an author-facing ack rather than a landing step today, and adding one there would put a merge sentence in
  front of an author at the moment they are handing work to a reviewer — which is not the order the sentence
  is about.
