# fix-declared-next-step-names-the-push

## Summary

A head that has already been asked to be merged was still told to merge it. Three screens, three answers, none
of them the step actually owed:

```console
$ git pair check                     # approved, declared at this head, branch not pushed
OK: feat-ci-verify-cmd is integration-ready
head:  ff79a41
declared: ff79a41 (`git pair change integrate`)
next:  `git pair check`, then merge into main with ordinary git     ← the merge, as if nothing had been asked

$ git pair status
Next: push the branch so whoever merges can see the request; then `git pair check`, then merge into main
      with ordinary git                                             ← the push whether or not it was pushed,
                                                                      ending on the author's own merge

$ git pair change integrate          # again, at the same head
feat-ci-verify-cmd is already declared ready to integrate at ff79a41 (head ff79a41): nothing recorded
  merge into: main                                                    ← no next step at all
```

The report was "that looks like the old note" — the hand-off line added last changeset was missing. It was not
an old build (`0.0.0-dev+ef5fa30` carries the merge that added it, verified by ancestry), and the silence was
correct: the head _is_ the declaration commit, which is the state `declarable` exists to keep the hand-off off,
because a second `git pair change integrate` records nothing. What was missing was any line saying what a
declaration obliges next.

All three screens now read one builder, which picks between three sentences in a fixed order — declare this
head, push the branch, wait for the merge — and the author's own merge stays printed beside the wait as the
other exit, never as the only one:

```console
$ git pair check                     # declared, not yet pushed
next:  `git push origin feat-ci-verify-cmd` — queues it for merging with main's owner; `git pair check --json` reports ready and integrating
> I think we should avoid saying definitively that "main" will merge it. We could say this will queue it for merging with the owner of <trunk/destination>?

Adopted. The sentence no longer states what the destination does — the destination's owner decides that, and a
repository whose owner merges nothing, or merges next Tuesday, was being told a future had been fixed. It says
what the push does (the request becomes visible to that owner) and names the two fields an automated owner
gates on, which is the pointer the earlier clause carried. One consequence worth naming: this is the sentence
`git pair change integrate` prints, so the change moved that command's output as well, and the two `README.md`
transcripts moved with it in the same commit — the quote test pins the documents to the builder, so code and
prose change together or the run fails.

$ git pair check                     # declared and pushed
next:  waiting on main's owner to merge it: `git pair status --changeset feat-ci-verify-cmd` reports the landing
       or land it yourself: `git pair check`, then merge into main with ordinary git

$ git pair change integrate          # again, at the same head, pushed
feat-ci-verify-cmd is already declared ready to integrate at ff79a41 (head ff79a41): nothing recorded
  merge into: main
  next:        waiting on main's owner to merge it: `git pair status --changeset feat-ci-verify-cmd` reports the landing
               or land it yourself: `git pair check`, then merge into main with ordinary git
```

## What changed

- `internal/cli/landing_advice.go` — the declared path, beside the landing sentence it sits next to:
  `pushNextAction` (the words `integrateNextAction` printed, now spelled in one place; review 773b481 reworded
  them), `mergeWaitNextAction` (the wait), `selfMergeLine` (the human-screen alternative, "or land it
  yourself" on review), `undeclaredHeadLine` (a declaration that names
  a commit the branch left behind), `declaredNextAction` (the choice), and `declarationPushed` (whether
  `origin/<branch>` carries the head). `session.declaredPath` reads the two facts and asks neither unless the
  newest marker is a declaration.
- `internal/cli/check.go` — where the gate passes and the screen prints `declared:`, the next step comes from
  `declaredNextAction`; the human print carries the alternative line where the hand-off line would have been.
- `internal/cli/status.go` — the `StateIntegrating` case calls the same path instead of spelling its own
  sentence, and the human screen gains the alternative line beside the wait.
- `internal/cli/integrate.go` — `integrateNextAction` calls the shared push builder, and the already-declared
  path prints the next step it computed and dropped. `reportIntegrate` takes `ctx`, because the pushed test is
  a git read.
- Tests — `integrating_surfaces_test.go`: the push for an unpublished declaration on `check`, the wait plus
  the alternative on `check` and `status`, the re-run of `change integrate` in both pushed and unpublished,
  and `check` naming the re-declaration when a commit came under the declaration. `advice_contract_test.go`
  exercises the three declared answers and every new builder, so a flag on any of them is validated.
  `landing_docs_test.go` pins the new sentences into the documents.
- Docs — PRD §21 gained the declared path beside the two exits it already carried; §9.9 says what each path of
  `change integrate` prints; §11.1 and §11.3 point at §21; the README's `next_action` paragraph mentions it in
  one sentence; `references/integration.md` tells the agent that after step 2 and the push the report is
  "waiting", never "merged".

## Design decisions

**The push and the wait are two steps, not one sentence.** The sentence `status` printed fused them — "push the
branch so whoever merges can see the request; then <merge>" — so an author who had already pushed was told to
push, and either way the screen ended by handing them the merge. The order the builder picks is the order the
facts matter in: a declaration about a different commit is not a request waiting on a push, and a request
nobody can see is not something to wait on.

**"Pushed" is measured against the remote the advice itself names.** `refs/remotes/origin/<branch>` carries the
head, tested by `RevParse` then `IsAncestor`. A repository that publishes to a differently-named remote gets
the push sentence, which is the honest pairing: the sentence says `git push origin <branch>`, so the fact
behind it is about `origin`. Any git error answers unpublished, which costs a re-run of a command that is
harmless to re-run, and the alternative line is on the screen either way.

**The wait names a read, not a loop.** `waiting on main's owner to merge it: \`git pair status --changeset
<id>\` reports the landing`follows the shape`READY`already uses for a reviewer ("waiting for a reviewer:`git pair review open`…"). There is no blocking command for a merge —`change wait` belongs to review (§9.4)
and landing has no command of its own (§13) — so a screen that suggested waiting points at the read that
settles it.

**The alternative exit is a human line, and the hand-off rule did not move.** `next_action` stays one
instruction: the wait is the machine answer, and `or take it yourself: …` is printed only on the human screens,
mirroring `or hand it to <destination>'s owner: …`. The two never appear together — the hand-off belongs to the
state before a declaration, the alternative to the state after — and the existing tests that keep the hand-off
out of `--json` and out of the excluded states were kept, not relaxed.

**`review submit` was left alone deliberately.** `nextActionFor` answers a different question — what the
reviewer just said, phrased for the author — and a head that is approved, declared and then re-reviewed is a
shape where "author: check, then merge into main" is still that answer. The declared path belongs to the
screens that report the changeset.

**No new machine field.** `check --json` already carries `ready` and `integrating`, which is what the CI gate
probes; whether the declaration is also on the remote is a fact an agent has no decision to make with it, and a
field duplicating a sentence is a second thing to keep true. If a consumer needs it, the read is
`git rev-parse origin/<branch>`.

## Validation

- The reported scenario, as a test: an approved, declared, unpushed changeset — `check` names
  `git push origin <branch>`, the merge sentence appears nowhere in the next step, and no hand-off line
  appears (a second declaration records nothing).
- The same fixture with `refs/remotes/origin/<branch>` at the head — `check` and `status` both print the wait
  and the alternative, and neither asks for a push again.
- `change integrate` re-run: writes nothing in both pushed and unpushed cases, and now names the step in
  both.
- A commit under the declaration: `check` and `status` both say "the head moved since the declaration:
  `git pair change integrate` to declare this head", and neither asks for a push.
- `next_action` on `check --json` and `status --json`: one instruction, no newline, no human alternative.
- The push sentence quoted byte-identically in the README's two transcripts, proved by the quote test after
  the wording moved into the shared builder; and the quote test proved to bite — changing "waiting on %s's
  owner to merge it" fails two document assertions.
- `mise run check` and `mise run gates` (the CI integration, the PTY walkthrough, the issue-29 scenarios).
- One `e2e-29.sh` run reported `E2E: FAILURES PRESENT`, and its log is gone: the command was piped through
  `tail`, which kept only the last twenty lines and took the pipeline's exit status with it, so the failure
  was at first read as a pass. Four subsequent full `mise run gates` runs and a direct
  `bash scripts/gates/e2e-29.sh` were clean, and nothing in that script's declared-head region asserts any
  next-step wording — it checks `ready`/`integrating`, the queue's text, the declaration commit, its
  trailers, and that it wrote no ref. Unreproduced, recorded rather than dropped; the CI job runs the same
  gates as the condition for merging, so a real failure stops the merge instead of being noticed after it.

## Known limitations

- A repository that publishes branches somewhere other than `origin` is reported as unpublished, and its
  author is told to push. That matches the command the advice names, and the alternative exit is still on the
  screen.
- The pushed test costs two git invocations (`RevParse`, then `IsAncestor`), read only where the newest
  marker is a declaration and only on a passing verdict. It is flat, not per ancestor, so the stack-walk cost
  bound is untouched.
- An agent reading `--json` sees the wait and not the alternative, by the same rule that keeps the hand-off
  out of `next_action`. The agent guide carries both exits in prose.
- `git pair status` for a **landed stacked** changeset still prints a `step: rebase onto it: …` line beside
  "nothing further to do". Pre-existing, reported separately, and not touched here.

## Open questions

- Whether the queue should carry the declared path too. Its rows report what a person watching the repository
  owes, and a declared changeset asks nothing of them — which may be exactly why it needs the wait, or
  exactly why it stays silent.

> There could be a world in which the merge to trunk is not automated, but the agent still pushes integrating declarations.
> The queue would be a way for the owner to identify anything that needs to be manually merged

That world is the one the queue already serves, and its `AWAITING INTEGRATION` section is the list for it: each
row gives the changeset, its branch, `merge into:` the destination, the declaration and how long ago it was
recorded, and the head the declaration names. A person with no automated merge reads that section and has what
they need to merge by hand — `merge into:` is the destination the walk of PRD §13.1 arrives at rather than
`base`, precisely so the row answers the question an actor asks. What stayed open, and is still open, is only
whether the _sentences_ this changeset adds belong in that section as well; the section answers "what is waiting
and where does it go", and the sentences answer "what do I do next", which is a screen-level question. That
discovery path is already covered rather than assumed: the gates replay asserts `AWAITING INTEGRATION` and
`merge into: main` on a declared changeset, and asserts that declared work does not also appear under
`READY FOR REVIEW`.
