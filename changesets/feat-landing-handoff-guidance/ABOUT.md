# feat-landing-handoff-guidance

## Summary

The landing guidance was written by hand in too many places, and it had drifted in three ways that a reader
could see.

The first was a child's next step restating its parent's work:

```console
$ git pair status        # beta, APPROVED, stacked on alpha, alpha merged nowhere
Next: waiting on parent alpha: land it first (`git pair check`, then merge into main
      with ordinary git), then `git pair check` and merge into main
```

That says "do the review" to two people. The parent's landing is its author's step, spelled by the parent's
own screen; the child's screen owns the child's step and nothing else. `git pair change integrate`'s refusal
repeated the same sentence a second time, in a third wording, so a change to the landing contract had to be
made in five places or those places began to disagree.

The second was a sentence with one actor where there are two. "then merge into main with ordinary git" is the
author merging. The other exit — write the request, push it, let whoever owns `main` merge it — was nowhere in
the advice, so an author whose repository merges declared work was still told to merge by hand, and an author
whose repository has nobody with merge rights had no line saying the merge is not their job.

```console
$ git pair status        # alpha, APPROVED
Next: `git pair check`, then merge into main with ordinary git
      or hand it to main's owner: `git pair change integrate`, then `git push origin alpha`
```

The third was advice that names a command this build does not answer to. Two sentences told the reader to run
`git pair change ready --changeset <id>`, and `change ready` has no `--changeset` flag — it takes the
changeset from the branch you are standing on. Copying the flag from `status --changeset` is a mistake a
reviewer cannot catch by reading, because both forms look right.

```console
$ git pair change integrate      # beta, stacked on the unlanded alpha
Cannot declare beta ready to integrate:
  - the parent alpha is not landed on the integration branch, so declaring beta would ask for a merge onto
    alpha — a branch review can still rewrite. Land the parent first: `git pair status --changeset alpha`
    names its step. Merging beta onto alpha by hand stays open; git-pair declines to queue it, not to allow it
    ...
```

## What changed

- `internal/cli/landing_advice.go` — the wording now has three named builders instead of one:
  `landingNextAction` (a changeset's own step), `waitingNextAction` (a child whose parent's landing is owed)
  and `handoffLine` (the second exit). `declarable` is the screen's half of `change integrate`'s test. All
  four are read by `status` and `check`, so both screens say the same thing about the same facts.
  `landingContract.ParentGone` — read from the parent tip `ParentOf` already returned, so it costs no git
  invocation — decides whether the waiting sentence points at the parent's screen or stops pointing.
- `internal/cli/status.go` — `statusView` gained a `handoff` field, computed after the three overrides that
  replace the next step (wrong checkout, base-is-own-branch, already landed) so a screen that is not
  offering a merge offers nothing else either, and printed indented under `Next:`.
- `internal/cli/check.go` — the landing contract is read once for the passing verdict and carried to the
  human print, so the merge line and the hand-off line cannot name different destinations. The refused
  verdict returns before either is printed, and the read stays off the path CI takes when the answer is no.
- `internal/cli/integrate.go` — the unlanded-parent refusal refers to the parent's screen instead of
  spelling its step, and keeps the part only it can say: a declaration asks for an unattended merge, and
  the human path stays open.
- `internal/cli/stacked.go` — the two sentences that named a flag the command does not have: the
  already-in-destination re-offer step and the conflicting-merge step now say `git pair change ready`, which
  is what the command is, with no selector. The conflicting-merge step also became `conflictStep`, a
  function rather than two copies of a `fmt.Sprintf`, so the advice contract test can hold it to account.
- Tests — `advice_contract_test.go` (new): every sentence a command prints is scanned for the commands it
  names, each resolved against the command tree, with flags resolved against **that** command and not
  against the union. It is the check-prose contract test extended to advice assembled with `fmt.Sprintf`,
  which that test cannot see. `landing_advice_test.go`: the hand-off appears in exactly the states where
  `change integrate` records a declaration — asserted per state from both the screen and the command's exit
  code and `recorded` field — stays out of the states where it refuses, and never enters `next_action`.
  `landing_docs_test.go` (new): the documents' quotes are compared with what the builders produce.
- Docs — PRD §21's "The next step says which landing is owed" is now the single canonical statement: the two
  facts, "names the step and the destination, never the actor", both exits with the screen transcript, and
  the refer-don't-restate rule. The README and the agent reference quote it rather than restating it.
  §11.1 and §11.3 print one exit in their transcripts, so each now points at §21 for the pair; §9.5
  ("Handing the work on") names the other exit and links to §9.9; `references/integration.md` says the merge
  belongs to _whoever_ owns the destination branch — it said "the person", which names an actor the rest of
  the guidance refuses to name — and records the automated path as one shape of that owner's decision, with
  `scripts/ci/git-pair-integrate.sh` named as this repository's own example and no claim about a runner.

## Design decisions

**A surface that does not own a step refers the reader to the one that does.** The child names the parent and
points at `git pair status --changeset <parent>`; `change integrate` does the same and keeps its own argument.
The acceptance test for the consolidation is an absence — no `git pair check` anywhere in the refusal — because
the property is that no second copy of the parent's step survives.

**The pointer is dropped where the screen it points at cannot answer.** A parent whose branch is gone and whose
record the destination never carried is the state where `git pair status --changeset <parent>` exits 2 ("no
branch carries changeset … and main holds no directory for it either"), verified on a fixture rather than
reasoned about. Advice that names a command which cannot answer is the failure M1 exists to remove, so in that
state the sentence says what is missing and names the command that resolves the unreconciled stack,
`git pair init --parent <branch> --set-parent` — the same words `root.go`'s note uses. That command then appears
twice on one screen, once in `Next:` and once in `stale:`, and is not collapsed into a reference: `next_action`
is read on its own in `--json`, so the field has to be actionable without the reader also reading a field
printed beside it.

**`next_action` stays one instruction.** The hand-off is printed on the human forms of `status` and `check`
only, where a second line costs a parser nothing. Machine consumers already branch on `state`, `ready` and
`integrating`; a fork inside a sentence would make them parse advice to decide which exit to take. One test
exists to keep the machine answer single, and it is the cheapest thing to break by accident.

**The offer and the acceptance read the same rule, and the equivalence is tested rather than assumed.**
`declarable` applies each reason `change integrate` refuses before it reaches its gate — verdict, staleness,
abandonment, nothing below unlanded, head not already declared — and shares that command's own `alreadyDeclared`
outright, because it is the condition that reads a marker rather than a state and two copies would drift the
first time a re-offer superseded a declaration. Each state then asserts both halves from one fixture: the line
printed, and `change integrate` exiting 0 with `recorded: true`.

**It is not a gate run.** The gate is the step the sentence in front of it already names — "`git pair check`,
then …" — and paying for a content comparison to decide whether to print one more line would move the gate's
cost into the cheap screen, which is what `TestStatusStackChainCostsABoundedReadPerStep` exists to bound. The
residual is stated under Known limitations.

**Variant A over variant B, on real output.** The wide alternative — print the hand-off whenever the changeset
is approved — was mocked up against the landed pair and rejected: it offers `git pair change integrate` on a
`READY` changeset that has never been reviewed, two lines under "wait for a reviewer", and on a child whose
parent has not landed. Both are exit-1 offers. The narrow rule is the one that never offers a refusal, and the
reasoning is in the plan's research note so the next editor does not re-litigate it.

**git-pair names the step and the destination, never the actor.** The hand-off says "main's owner". Nothing
here reads a workflow file, a repository variable, a check run, or a runner name: that would be wrong in
forks, self-hosted runners and monorepos, and a workflow existing is not a workflow merging. Whoever the
owner is, person or job, satisfies the same three facts — approved, declared, green on the declared head —
and git-pair asks nothing about how they merge (PRD §26).

**Advice gets the check prose already has, checked against one command.** `docs_contract_test.go` proves the
PRD, the README and the skill name commands that exist, both directions. Emitted advice is invisible to it, and
a test pinning a sentence verbatim pins whatever mistake the sentence contains — which is how the
`--changeset` sentences survived review. The new test resolves flags against the command the advice actually
names, since "`--changeset` exists somewhere in git-pair" is precisely the mistake that was made.

**The documents quote, and the quotes are pinned to the builders.** `landing_docs_test.go` builds the waiting
sentence, the landing sentence and the hand-off line from the builders and requires the PRD, the README and
`references/cli.md` to contain them, normalising markdown spans and line wrapping so presentation cannot be the
reason a quote test fails. It also keeps the removed wording out: any document that spells "land it first
(`git pair check`…" inside the child's step fails. Changing a builder's wording now fails three document
assertions, which is the drift this changeset exists to stop.

## Validation

- The hand-off's equivalence, per state: approved (line printed, `change integrate` exits 0 and records),
  never reviewed, head moved since the approval, child of an unlanded parent, feedback instead of an
  approval, head already declared (no line, and a second `change integrate` records nothing).
- The machine answer: `status --json` and `check --json` carry the landing sentence in `next_action` with no
  hand-off in it and no second instruction in it; the two human lines name one destination, read from one
  contract.
- The advice contract test, proved against the pre-fix tree: it fails on `git pair change ready --changeset
<id>` and passes after the correction; it resolves `status --changeset`, `check --changeset` and the
  builders' own output.
- The absence that is the consolidation's point: `change integrate`'s refusal contains no `git pair check`.
- The quote test, proved by drifting a builder one word ("its landing is owed first" → "its landing comes
  first"): three document assertions fail, and pass again on restore. Its sweep for the removed wording runs
  over `docFiles`, so a reference page added later is covered without anyone remembering to add it.
- The control that must not move: an unstacked approved changeset still ends its next step with
  `` `git pair check`, then merge into main with ordinary git ``, with the hand-off as the only addition.
- `mise run check` (gofmt, vet, the sharded suite including the docs contract and the hygiene scan) and
  `mise run gates` (the CI integration, the PTY walkthrough, and the issue-29 scenarios).

## Known limitations

- `declarable` cannot see a content-comparison refusal, so an `APPROVED` changeset whose content drifted
  since the review can show the hand-off line before `check` refuses it. The mitigation is ordering: the
  line sits after the `git pair check` the sentence in front of it already names, so the step that would
  catch it is printed first. If it proves confusing, the fallback is to print the line on `check`'s verdict
  only, where the gate actually ran.
- Agents do not see the hand-off. `next_action` carries one instruction by design, and the agent guide names
  both exits in prose (SKILL.md, "What is not yours"), so an agent following `--json` alone reads the merge
  and not the alternative.
- `git pair queue` keeps its own next-step lines and its `parent_notes`. Nothing there printed the merge
  sentence, so the consolidation did not reach it; its housekeeping note is a different voice, and the plan
  leaves it alone deliberately.
- In the unreconciled state — parent branch gone, no record in the destination — the child's screen names
  `git pair init --parent <branch> --set-parent` twice, once in `Next:` and once in the `stale:` note. The
  duplication buys the ability to act on `next_action` alone.
- The quote test pins four sentences. A fifth copy added later by pasting prose rather than calling a
  builder is outside its reach; the defence there is the rule itself — a surface refers, or it owns.

## Open questions

- Whether `git pair queue` should refer to the parent's advice the way `change integrate` now does. Its
  `parent_notes` own a housekeeping sentence, which is a different voice, so the answer may be "no, and the
  difference is the point".
- Whether `status` should grow an `--allow-feedback`. It would have to mean the same thing it means to
  `change integrate`, because the screens offer exactly what the command accepts; the answer right now is
  that a reader who wants a feedback-policy head declared runs the command and gets the verdict.
