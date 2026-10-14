# Name the step the declaration leaves behind

## Goal

Once a head has been handed over, the screens stop telling the author to merge it. `git pair check`,
`git pair status` and the re-run of `git pair change integrate` say the same thing about a declared head:
push it if it is not on the remote yet, and once it is, the merge is the destination owner's turn — with the
author's own merge offered as the alternative, never as the only exit.

The report that started this was a real session:

```console
$ git pair check            # feat-ci-verify-cmd, APPROVED, declared at the head
OK: feat-ci-verify-cmd is integration-ready
head:  ff79a41
declared: ff79a41 (`git pair change integrate`)
next:  `git pair check`, then merge into main with ordinary git
```

The head *was* the declaration commit — `Review-State: integrating`, `Review-Head` naming it — so the request
had already been written. The next step still read as if nothing had been asked for, and the screen that
printed `declared:` underneath had no line telling the author what a declaration obliges them to do next.

## Success criteria

Observable, not implementation details:

1. For a changeset whose head is covered by its own declaration and is **not** on `origin`, `check` and
   `status` print, as the next step, the push sentence — the same words `git pair change integrate` prints —
   and no landing sentence. The test is an absence: `merge into <destination> with ordinary git` appears
   nowhere in the next step.
2. For a declared head that **is** on `origin`, the next step is the waiting sentence, in the shape the
   `READY` state already uses (`waiting for …: <command that reads the state>`). The author's own merge is
   still available as a second line on the human screens, and is not the only thing offered.
3. `next_action` in every `--json` stays one instruction, and carries no human-only line.
4. A declaration that no longer covers the head — the newest marker is `integrate`, and a commit has come
   under it — is named as such on both screens, in the words `status` already uses. It is not reported as a
   merge to run or a push to make.
5. One builder spells each of those three sentences, and all three surfaces read it, so the push sentence is
   not spelled twice again. The quote test pins the sentences the documents carry.

## Context

Three facts the last changeset established, which this one builds on rather than repeats:

- `alreadyDeclared(s)` is the test "HEAD is the declaration commit itself". It is the condition
  `git pair change integrate` uses to write nothing on a re-run, and the condition `declarable` uses to keep
  the hand-off line off a head that is already handed over.
- The hand-off line appears exactly where `change integrate` would record a declaration, and is silent
  exactly where it refuses. In the reported session it was silent **correctly**: a second `change integrate`
  records nothing, so offering it would offer a no-op. What was missing was not the hand-off line but any
  line at all pointing at the push.
- `next_action` is a machine contract, one instruction per field. Anything with two exits is a human-screen
  line, and the pattern is established: `or hand it to <destination>'s owner: …`.

`status` already half-knows this. Its `StateIntegrating` case says *"push the branch so whoever merges can see
the request; then <landing sentence>"* — which is right about the push and wrong twice over: it says it
whether or not the branch is pushed, and it ends by naming the author's own merge as the only way the work
gets taken. `check` does not handle the declared state at all: its next step is `landingNextAction` whatever
the marker says.

## Constraints

- **git-pair ran nothing, and says so** (PRD §26). The push is named as a command the author runs, never as
  one that has been run — the rule `integrateNextAction` already states in its own comment.
- **The waiting sentence names no actor beyond the destination's owner**, and detects nothing about who
  merges: no workflow file, no check run, no runner (PRD §26, and the same rule the hand-off line follows).
- **`next_action` is one instruction.** The alternative exit for the pushed case is a human-screen line, so a
  consumer never parses a fork out of advice.
- **Screens stay cheap.** The pushed test is at most two git reads, taken only on the declared path, and flat
  rather than per ancestor — `TestStatusStackChainCostsABoundedReadPerStep` bounds the stack walk, and this
  adds nothing to it.
- **Prose in `PRD.md`, `README.md` and `skills/` is checked material**, and the push sentence is quoted in
  `README.md` twice. Moving it into a shared builder is what keeps those two quotes equal to each other and to
  the command's output — the quote test pins them to the builder, so a wording change moves the code and the
  documents together or fails. Review 773b481 changed the wording of this sentence, which is the case the test
  is there to catch rather than a case it forbids.

## Assumptions

- "Pushed" means on `origin`, under the branch name the changeset records — because that is the remote and the
  ref the sentence itself names (`git push origin <branch>`). A repository that publishes to a different
  remote gets the not-pushed sentence; the alternative is to read a remote the advice does not name.
- The destination owner is expected to act on the declaration. Where a repository has nobody who does, the
  alternative line is the exit, and it is on the screen for exactly that reason.

## Milestones

### M1 — One builder for the step a declaration leaves behind

#### Deliverables

- `pushNextAction(branch, destination)` holds the words `integrateNextAction` spells, in one place, and
  `integrateNextAction` calls it. It said `<destination> merges what \`git pair check --json\` reports ready and
  integrating` when this plan was written; review 773b481 asked for the merge to be described as the owner's
  decision rather than as something the destination does, and it now says the push queues the work for the
  destination's owner and names the two fields an automated owner gates on. The README quotes moved with it in
  the same commit, which is the quote test doing its job.
- `mergeWaitNextAction(lc, slug)` spells the waiting sentence for a declared, pushed head.
- `selfMergeLine(lc)` spells the human-screen alternative for that state.
- `declaredPathNextAction(...)` picks between those and the "declare this head" sentence, from
  `alreadyDeclared(s)` and the pushed fact.

#### Tasks

- [x] Move the push sentence out of `integrate.go` into `landing_advice.go` as the one place it is spelled,
      and have `integrateNextAction` call the moved builder. The words themselves changed once, on review.
- [x] Add the pushed test: `refs/remotes/origin/<branch>` resolves, and the head is an ancestor of it. On any
      git error, answer not-pushed — the sentence then names a command that is harmless to re-run, which is
      the cheap direction.
- [x] Spell the waiting sentence in the `READY` state's shape: `waiting on <destination>'s owner to merge it:
      \`git pair status --changeset <id>\` reports the landing`.
- [x] Spell the alternative line, built from `landingNextAction` so the merge sentence has one spelling.
- [x] Reuse `status`'s existing "the head moved since the declaration" wording from the shared path, so `check`
      can say it too without a second copy.

#### Verification

- The README's two quoted push sentences still pass the quote test, unedited.
- `mise run check`.

### M2 — The three surfaces agree

#### Deliverables

- `check`: on a passing verdict whose head is declared, the next step comes from the declared path.
- `status`: the `StateIntegrating` case calls the same path instead of spelling its own sentence.
- `change integrate`: the already-declared human path prints the next step it already computes and drops.

#### Tasks

- [x] `check.go`: where `out.NextAction` is built, branch on `alreadyDeclared(s)` before the landing
      sentence, and carry the same `lc` to the human print so the alternative line names one destination.
- [x] `status.go`: replace the `StateIntegrating` sentence with the shared path, keeping the stale case.
- [x] `integrate.go`: print `next:` on the `!res.Written` path — that path computes the action and never shows
      it, so the author is told "nothing recorded" and left without a step.
- [x] Keep the hand-off line's rule untouched: it is printed where `declarable` holds, which is nowhere in the
      declared path.

- **Deliberately untouched:** `nextActionFor`, which is `review submit` telling the author what the review
  outcome means ("author: `git pair check`, then merge into main with ordinary git"). It answers a different
  question — what the reviewer just said — and a head that is approved, declared and then re-reviewed is a
  shape where the merge sentence is still the author's own answer. The declared path belongs to the screens
  that report the changeset, not to the one that reports the review.

#### Verification

- One fixture, three surfaces: declared-not-pushed prints the push sentence and no `merge into <dest> with
  ordinary git` anywhere in the next step; declared-and-pushed prints the waiting sentence, and the human
  screens carry the author's own merge as the second line.
- The machine answer: `check --json` and `status --json` carry one instruction with no newline, and neither
  carries the alternative line.
- `mise run gates`, which includes the PTY walkthrough — the human screens are what changed.

### M3 — The documents carry the declared path

#### Deliverables

- PRD §11.3's `check` transcript shows the declared state, or points at the section that does, and §9.9 says
  what a re-run of `change integrate` prints now that it prints a next step.
- The README's `next_action` paragraph mentions the declared path in one sentence and links rather than
  spelling a fourth copy.
- The quote test pins the waiting sentence and the alternative line, and still pins the push sentence.

#### Tasks

- [x] Add the declared-path statement to PRD §21 next to the two exits it already carries — the same section,
      so the rule stays in one place — and point §11.3 at it as the other two screens do.
- [x] Update §9.9 for the re-run's next step.
- [x] Extend `landing_docs_test.go` with the new sentences built from the builders.
- [x] Check `skills/git-pair/references/integration.md`: an agent that declares and pushes needs to know the
      report it gives afterwards is "waiting", not "merged".

#### Verification

- Changing a builder's wording fails the document assertions, proved by editing one word and re-running.
- `mise run check`.

## Risks

- **The pushed test answers wrong on an unusual remote layout** and tells an author to push a branch that is
  already out there. Mitigated by the direction of the error: the push sentence names a command that is
  harmless to re-run, and the waiting sentence's alternative line is still on the screen.
- **Two lines where the machine expects one.** Mitigated by the existing single-instruction assertion on
  `next_action` and by extending it to the declared states.
- **The waiting sentence reads as an instruction to poll.** It names `git pair status --changeset <id>`, which
  is a read, not a loop; `change wait` is the blocking command and it belongs to review, not to landing
  (PRD §9.4, §13).

## Verification strategy

Unit tests on the builders and the pushed fact; end-to-end tests on fixtures through the real commands for
each declared sub-state, because these are screens an agent reads; the two cross-checks that matter most are
the absence of the merge sentence in the declared states and the quote test tying the documents to the
builders. `mise run check` for the suite, `mise run gates` including the PTY walkthrough.

## Audit History

| Date | Audit | Summary |
| --- | --- | --- |
| 2026-10-14 | self-verification before hand-off | `mise run check` green. `mise run gates` failed once inside `e2e-29.sh` with the log lost — the command was piped through `tail`, which also masked the task's exit code, so the failure read as a pass at first. Four later full gates runs and a direct `e2e-29.sh` were clean, and the script's declared-head region pins no next-step wording. Recorded as an unreproduced failure rather than converted into a pass; the box "`mise run gates`" above means the four clean runs plus the CI job that gates the merge on the same script. |
| 2026-10-14 | review 773b481 (feedback, non-blocking) | Two wording requests and one note about the queue. The push sentence no longer states that the destination merges — it queues the work for the destination's owner, and keeps naming the two fields an automated owner gates on; the human alternative reads "or land it yourself" rather than "or take it yourself". The queue note is answered under Open questions: the `AWAITING INTEGRATION` section already lists branch, destination, declaration and head, which is what a person merging by hand needs. Plan claims of byte-identical wording were corrected — the wording did change, by review, with the documents moved in the same commit. |
