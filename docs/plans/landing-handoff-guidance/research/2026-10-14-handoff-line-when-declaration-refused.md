# Does the hand-off line belong on a screen whose declaration would be refused?

Question: `status` and `check` tell an author to merge with ordinary git. The alternative is to hand the merge
to the destination's owner with `git pair change integrate`. Should that second exit be printed whenever the
changeset is in review (so the option is discoverable), or only when `git pair change integrate` would accept
the changeset (so the screen never recommends a refused command)?

## Method

Both variants were written out against the real output of the four surfaces, using the fixtures from the
landed pair `feat-next-action-fixes` / `feat-stacked-landing-advice` and the refusal text
`unlandedParentReason` prints. Lines below that are not marked `+` are verbatim from this session's runs; the
`+` lines are the proposed addition.

## State 1 — APPROVED, stack landed (declaration accepted)

Both variants are identical, because this is the state where both print the line:

```console
Next: `git pair check`, then merge into main with ordinary git (where feat-next-action-fixes landed)
+       or hand it to main's owner: `git pair change integrate`, then `git push origin feat-stacked-landing-advice`
```

## State 2 — APPROVED, parent approved but not landed (declaration refused)

Only-when-declarable prints the waiting sentence and nothing else. Always-on adds:

```console
Next: waiting on parent alpha: its landing is owed first (`git pair status --changeset alpha` names the step),
      then `git pair check` and merge into main
+       or hand it to main's owner: `git pair change integrate`, then `git push origin beta`
$ git pair change integrate
Cannot declare beta ready to integrate:
  - the parent alpha is not landed on the integration branch, so declaring beta would ask for a merge onto
    alpha — a branch review can still rewrite. …
```

Exit 1. The screen offered a step the command in the same tool refuses, and the refusal then re-explains the
wait the screen had just named — the same fact twice, one of them wrong.

## State 3 — READY, never reviewed (declaration refused)

```console
Next: waiting for a reviewer: `git pair review open` (author: `git pair change wait` to block on it)
+       or hand it to main's owner: `git pair change integrate`, then `git push origin beta`
$ git pair change integrate
Cannot declare beta ready to integrate:
  - the changeset is marked ready and has not been reviewed since (70e40ea)
```

This is the case that settles it. The next step is "wait for a reviewer" and the hand-off line below it says the
merge is available. To a human that reads as permission to skip the review; to an agent it reads as a branch
worth trying, and `change integrate` is exactly the command an agent is willing to run.

## State 4 — INTEGRATING

Neither variant prints anything: the author has already handed it over. So both need the
"no declaration covers this head" test (`alreadyDeclared`), and always-on needs a second exclusion on top.

## Answer

Only when declarable.

The asymmetry is what decides it rather than a preference for quiet screens. In every state where the narrow
rule hides the line, the command that would refuse it already prints a reason explaining the wait — including
the parent's landing, which is the case a reader most needs explained. So the cost of the narrow rule is one
command run, and the cost of the wide rule is screens that recommend a refusal, in the two states where an
author is least able to tell that the tool is contradicting itself.

It also removes a second copy of the readiness rule. The narrow rule asks one question — would this
declaration be accepted — which is the question `change integrate` already answers, so the two call one
predicate. The wide rule needs "is the state roughly APPROVED", which is a paraphrase of what the gate decides,
and paraphrases are where the two drift.

## Follow-up this implies

The predicate has to be shared, not reimplemented (plan M3), and the line must sit after the `git pair check`
the sentence already names, so a gate refusal is covered by the step in front of it rather than by the
predicate, which cannot see the content comparison without a cost `status` should not pay.
