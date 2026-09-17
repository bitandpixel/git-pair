# Anchored lifecycle

## Goal

Make every changeset state the result of a command somebody ran, and make `review queue` describe
work that is actually waiting for a reviewer. Today two things are wrong in opposite directions:
readiness can be taken away silently by a commit, and a finished changeset keeps appearing in the
queue forever. Both come from deriving intent from content instead of from acts.

## Success criteria

Observable outcomes, not implementation details:

- An author who has readied a changeset and then wants to keep working has a command for it, and
  the changeset leaves the queue immediately.
- `status` and `review queue` never change a changeset's state because of a commit. State changes
  when `change ready`, `change unready`, `review submit`, `change complete` or the terminal command
  runs, and at no other time.
- A squash-merged changeset does not appear as READY, and does not produce a skip note, on the
  deployment branch or anywhere else.
- The head recorded by `change complete` is a head that was reviewed. The tool refuses rather than
  archiving a head whose content nobody approved.
- The unsquashed history of a changeset is protected from the first handoff, not from the first
  review.
- `git pair status` and `git pair review queue` answer correctly regardless of which branch is
  checked out.

## Context

Merged work (`882100c`) replaced `review close` with `change complete`: archival, ref-only, owned
by the changeset's author, recording nothing on the branch. That left `done` as a fact git-pair
does not derive, which is correct, and exposed what the queue does with a changeset that has
finished. `research/2026-09-17-queue-after-landing.md` records it happening live in this repository:
while the branch existed the queue listed the merged changeset under READY FOR REVIEW, and after
`git branch -D` the same changeset became a permanent `note: skipped ... (no branch matches this
changeset directory)`.

The design conversation that produced this plan travelled through driving the queue off
`refs/reviews/*` instead of off the working tree. That path was rejected for reasons worth
keeping: a review ref is written only by `review submit` (and, here, by the new anchors), so a
changeset waiting for its first review has no ref at all; and a ref is only as fresh as the last
git-pair command that ran, which makes it a poor oracle for state. The decision recorded below —
that readiness is sticky and explicit — is what makes the ref-driven queue unnecessary: if state
only moves on commands, then deriving it from the branch is both correct and cheap, and refs can
go back to being durability anchors and terminal records.

## Constraints

- PRD §26: git-pair does not rewrite history, switch branches, or reset the working tree or index.
  No milestone may `checkout`, `reset` or `rebase`. Marker commits therefore keep going through
  `Repo.Commit`/`CommitPaths` on a checked-out branch; nothing here introduces a plumbing path that
  fabricates commits onto a ref without a working tree, and `internal/git` keeps its two mutating
  verbs (`commit`, `commit --only`).
- The hygiene test (`internal/hygiene/hygiene_test.go`) forbids `update-ref -d`, `branch -D`,
  `reset`, `rebase` and `merge` in shipped code, so refs are permanent. Whatever retires a
  changeset does it by adding a record, not by deleting a ref.
- `--json` output is an agent contract. Adding state values is expensive; this plan adds none.
- The project dogfoods `git-pair`: each milestone is its own changeset, handed off with
  `git pair change ready`, and `mise run check` is the gate.
- Archived plans under `docs/plans/completed/` keep their historical wording, except
  `gitpr-mvp/artifacts/e2e-29.sh`, which is a live verification gate and has to keep passing.

## Assumptions

- Squash-merge is the merge policy, and the deployment branch is a changeset's `base`. Landing
  detection is designed against that, not against `git merge --no-ff`.
- Agents are the primary audience for `--json` and for the agent contract in PRD §22, so an
  explicit two-verb contract (`ready` / `unready`) is a better guarantee than content-derived
  staleness, because an agent follows a documented contract whereas a human does not reliably
  notice a state flip.
- One branch per changeset slug. `change init` derives the slug from the branch name, and two
  branches matching one slug (`feature/x`, `feature-x`) already tie-break to the newest; nothing
  here makes that worse.

## Decisions

Settled with the reviewer on 2026-09-17, recorded here because each was close enough to go the other
way:

1. **The completion anchor check stays (milestone 3).** Deleting content-derived staleness also deletes
   the only thing that stops `change complete` archiving a head newer than the one approved, so that
   command keeps a narrow tree check and refuses. Rejected: relying on a human to compare the archive's
   sha with the review's, since an agent that trusts the archive is the assumed reader and the tool can
   answer the question itself.
2. **The marker value is `Review-State: working`.** It reuses the derived default, which keeps the
   `--json` vocabulary at five states and needs no new `model.State`. Rejected: `retracted` and friends,
   more explicit in a log and wider in a contract.
3. **The verb is `change unready`.** It pairs with `change ready`, which makes the agent contract two
   symmetric verbs rather than a verb and its antonym from a different register. Rejected: `retract`,
   which reads better in prose and would leave room for a later `pause` that is not the same act.
4. **`change unready` on a changeset that is not ready succeeds and records nothing.** There is no state
   to refuse from, the same reasoning that made a repeated `change complete` a success, and a refusal
   would make an idempotent script awkward. The side benefit is that flip-flopping cannot produce
   consecutive markers.

Still open, both small: the name of M6's terminal command, and whether the anchor-versus-head
relationship in `status` is its own field or part of `reason`.

## Milestones

### M1 — `git pair change unready`

#### Deliverables

- `change unready` takes a readied changeset out of review, explicitly, and records why on the
  branch.
- `status`, the TUI and `review queue` reflect it.
- `review submit` does not refuse an unreadied changeset. It accepts every state today, and an unready
  changeset is indistinguishable in state terms from one that was never readied, so refusing one and
  not the other would be an asymmetry no reviewer could predict. What matters is the way back in:
  `change ready` still enforces surviving review additions, so a reviewer who acts on an unready
  changeset has their submission counted. The plan asked for this refusal before that was thought
  through; it is dropped rather than implemented. (The TUI takes no change either: it renders the
  derived state, and `WORKING` was already one of the states it could show.)

#### Tasks

- [x] Teach `marker.Decode` the value `working`, as its own kind rather than a review outcome, and give
  it arms in `markerLabel` and `markerReason`.
- [x] No state machinery is needed beyond that: `derive` (`internal/lifecycle/lifecycle.go:174`) takes the
  newest marker and asks it `Event.State()`, which already falls back to `model.StateWorking`
  (`internal/lifecycle/lifecycle.go:323`). What must not happen is `working` being left unrecognised,
  because an unrecognised marker counts as an implementation commit, so the retraction would be
  invisible to the newest-marker rule instead of ending readiness. `Event.State()` names `KindUnready`
  explicitly rather than relying on that fallback.
- [x] Command in `internal/cli/change.go`, next to `runChangeComplete`: clean tree, derive state, write
  the marker iff state is READY, APPROVED or FEEDBACK, otherwise succeed without recording. The refusal
  for a terminal changeset waits for M6, since no terminal state exists to refuse yet.
- [x] Tests mirroring `internal/cli/complete_test.go`: `internal/cli/unready_test.go`, seven cases,
  including the two that are easy to get wrong — `recorded: false` on a BLOCKED changeset must still
  report `state: BLOCKED`, and the working marker must be recognised rather than listed under
  `unrecognised_markers`.
- [x] README command table, marker table, JSON contract, agent contract, troubleshooting; PRD §8 tree,
  new §9.6, §12, §22.

#### Verification

Run and green, as tests in `internal/cli/unready_test.go` and once by hand against the installed
binary: `change ready`, `change unready`, then `review queue` shows `nothing is ready`; `status` says
`WORKING` with reason `marked unready by <sha>` and no `unrecognised_markers`; a second `unready`
records nothing and moves no commit; a `BLOCKED` changeset stays `BLOCKED`; `change ready` after an
unready re-runs the survival gate (`TestChangeUnreadyDoesNotSkipTheReviewGate`, which is also where
"a reviewer may submit against an unready changeset" is pinned); and completion after an unready
refuses (`TestChangeUnreadyFromApprovedRequiresAFreshReview`).

### M2 — State changes only on command

#### Deliverables

- A commit never changes a changeset's state. READY survives any number of implementation commits
  and ends only at `change unready` or a review submission.
- `ReconcileStaleness` stops being something `Summarize` does for every caller and has exactly one
  call site left, `change complete`, with the tests that pinned the queue-side flip removed.

#### Tasks

- [x] Staleness was applied inside `Summarize` itself: `derive` only counts commits and marked the
  verdict provisional, and `Summarize` handed the result to `ReconcileStaleness` before returning
  (`internal/lifecycle/lifecycle.go:131`). It is opt-in now: `Summarize` returns the marker verdict,
  and `SummarizeAgainstTree` / `SummarizeAgainstTreeHEAD` add the tree question. No flag on the
  shared function, because only one caller wants it and a boolean that six callers pass `false` to is
  a trap waiting for a seventh.
- [x] `derive`'s provisional flip (`s.Stale = true; s.State = model.StateWorking` when `trailing > 0`)
  no longer overwrites `State`; it keeps `Stale` as the observation and puts the count in the reason,
  so `marked ready by 8065dae (2 commits since)` tells a reviewer the branch moved without the state
  claiming anything about it.
- [x] `change complete` is the one caller of `SummarizeAgainstTreeHEAD`. `ReconcileStaleness` now
  sets `State = WORKING` itself in both invalidating branches, since it can no longer rely on `derive`
  having done it.
- [x] Rewrite the sentences that promise the old behaviour. README's "a commit that only touches
  ABOUT.md or a thread does not invalidate the marker" and PRD §12's named safety property described a
  rule that no longer exists; both are rewritten, and PRD §23's "any implementation commit after an
  approval invalidates approval" follows. Grep for `invalidat`, `stale` and `to WORKING` over README
  and PRD comes back with two unrelated hits.
- [x] Keep `survival.Check` where it is: it gates `change ready` and `change complete`, and a BLOCKED
  changeset still cannot return to READY without running `change ready`. Unchanged, and now the only
  content gate a ready marker has to pass.
- [x] `nextAction` names the drift for APPROVED and FEEDBACK. Without this it points an agent at
  `change complete` for a head that command will refuse, which is the one thing the field exists to
  prevent.
- [x] An unrecognised `Review-*` commit no longer moves state, but it still blocks completion: the
  tree cannot speak for trailers git-pair cannot read, so `TrailingUnrecognised` keeps that in
  `ReconcileStaleness`. It establishes nothing and clears nothing, and `status` still lists it under
  `unrecognised_markers`. Covered by the rewritten cases in `lifecycle_internal_test.go` and
  `TestStatusReportsUnrecognisedMarkers`.
- [x] `e2e-29.sh` gains a withdraw-and-re-offer step, because the queue's behaviour across an
  implementation commit is now the opposite of what that script asserted; the completion refusal it
  checks is labelled for the drift rather than for `WORKING`.

#### Verification

Run and green: the marker verdict survives a code commit, an `ABOUT.md` commit and a thread commit
(`lifecycle/history_test.go`, `internal/cli/statusdiff_test.go`); the queue keeps a changeset whose
branch moved and drops one that was unreadied
(`TestReviewQueueGainsAndLosesChangesetAcrossReadyAndUnready`); completion still refuses over the
same drift (`TestChangeCompleteRefusedAfterImplementationCommitFollowsApprove`,
`TestSummarizeImplementationAfterApproveBlocksCompletionButNotState`); a BLOCKED changeset stays
BLOCKED through an author's fix commit. `mise run check`, `e2e-29.sh` and `pty-walkthrough.sh` green.

### M3 — Completion cannot archive unreviewed content

#### Deliverables

- `change complete` refuses when the head it would archive has non-changeset content beyond the
  newest marker, with a message naming both shas, plus an explicit escape hatch.

#### Tasks

- [x] One call, one place: `runChangeComplete` derives with `SummarizeAgainstTreeHEAD` and refuses
  when it reports the reviewed content gone. Landed with M2, because deleting the drift flip without
  it would have left a hole in the same commit that opened it.
- [x] That is the surviving piece of the deleted machinery, including its changeset-directory
  carve-out, which is what lets an author reply in `ABOUT.md` after approval without breaking the
  anchor. The function's own comment is the argument for keeping it: the verdict is taken from the
  tree, so a base merge, a rebase, and a change followed by its revert all get the same right answer.
- [x] Document in PRD §9.5 and README's agent contract that the archive names a head that was
  reviewed. Done with M2's rewrite of §12 and the derived-state section; §9.5's own wording checked
  against the new behaviour.
- [ ] An escape hatch for the author who committed something outside the changeset directory after
  approval and does not want a second review — a README typo is the canonical case. Name it against
  `--allow-surviving-review-additions` rather than inventing a style; the flag prints what it is
  archiving over.
- [ ] Mutation-check the guard: reverse the diff range, typo the exclusion pathspec, point the check
  at the movable ref instead of the newest marker. Each has to turn a test red.

#### Verification

Ready, approve, add `func G()`, `change complete` refuses naming both shas and exits non-zero; with
the escape hatch it archives and says so. Approve, edit `ABOUT.md` only, complete succeeds.
Mutation-check the three ways this can break: diff range reversed, exclusion pathspec typo'd, check
applied to the movable ref instead of the newest marker.

### M4 — Anchors from the first handoff

#### Deliverables

- `change ready` and `change complete` both move `refs/reviews/<slug>` to the head they act on, so
  the unsquashed chain is protected from the first handoff instead of the first review.
- The refs are documented as anchors: durability and identity, never the source of state.

#### Tasks

- Call `reviewref.Update` from `runChangeReady`, next to the existing call in `reviewops.Submit` and
  the one added in `change complete`.
- `status` gains the relationship between the anchor and the branch head as an annotation —
  `anchored` when they agree, `author moved since <sha>` when they do not — and nothing derives a
  state from it. Note that `review_commit` in `--json` is the sha the movable ref points at, so its
  documented meaning moves from "the review" to "the last anchored head"; say so in README's JSON
  contract and check whether any consumer compares it with `latest_review.commit`.
- Report the anchor truthfully. `ReviewRef` is `reviewref.Head(slug)`, a name derived from the slug
  that is never checked for existence (`internal/cli/status.go:100`), so `review_ref` is never empty
  and cannot answer "has this changeset been reviewed", and the human printer prints it under a
  heading that says "Review archive" (`internal/cli/status.go:169`) when it is the anchor, not the
  archive. Gate the section on the ref existing, name the anchor as the anchor, and say in README's
  JSON contract which field an agent should test. See the research note for the captured output.
- Reachability argument in the commit message: a ready marker always descends from the last
  submission on that branch, so an anchor only ever grows the reachable set.

#### Verification

`git pair change ready` in a scratch repo creates `refs/reviews/<slug>` where none existed;
deleting the branch afterwards still leaves `git pair review history <slug>` working.
`mise run check` plus `pty-walkthrough.sh`.

### M5 — The queue enumerates branches

#### Deliverables

- `review queue` answers from branches and their commits, not from the checked-out working tree.
  Running it on `main` is meaningful and a landed changeset is silent.
- Changeset-scoped read commands accept `--changeset <slug>`.

#### Tasks

- New primitive: read `CHANGESET.yaml` from a commit (`git show <ref>:changesets/<slug>/CHANGESET.yaml`)
  alongside `changeset.ForBranch`, so a branch can be examined without checking it out.
- Rebuild `runReviewQueue` (`internal/cli/review.go:404`) as: one `for-each-ref refs/heads`, resolve
  each branch's changeset at the branch's commit, derive `base..branch`, print the READY ones. The
  `os.ReadDir` scan and the per-changeset `for-each-ref` go away.
- Classify a changeset directory that has no branch instead of skipping it: compare its subtree
  between `base` and the anchor (see the research note) and call it landed, or say nothing when it
  is terminal. The skip note becomes reserved for directories that genuinely cannot be explained.
- `--changeset` for `status`, `review history`, `review show`, `diff` and `change complete`,
  resolving by slug. Marker-writing commands (`change ready`, `change unready`) accept it only when
  a branch matches, and refuse otherwise with the reason, because a ready marker asserts something
  about a branch head and a commit written while another branch is checked out would land there.

#### Verification

Reproduce the research note on `main`: the merged changeset is not listed, and no note is printed.
Two branches, two changesets, one checkout: both appear. `git pair status --changeset <slug>` works
from an unrelated branch, and `git pair change ready --changeset <slug>` refuses when no branch
matches. `e2e-29.sh` and `pty-walkthrough.sh` still pass, and `--json` changes are reflected in
README's queue contract — the `skipped` key changes meaning, so call that out in the changeset's
ABOUT.md.

### M6 — A changeset can end

#### Deliverables

- `git pair change abandon` (name pending) records a terminal decision, on the branch and on the
  anchor, and removes the changeset from the queue permanently even after the branch is deleted.
- `review submit`, `change ready` and `change unready` refuse against a terminal changeset.

#### Tasks

- The terminal marker is an ordinary marker commit on the changeset's branch, followed by
  `reviewref.Update` to that head. Requiring the branch is not a limitation but the point: abandoning
  is a decision made while the work is still there, and anchoring the marker is what keeps it
  reachable once `git branch -D` runs. This keeps M6 inside the existing commit and ref primitives.
- Derivation gains a fallback, not an overlay: with a branch, state comes from the branch as it does
  now; without one, state comes from the anchor chain, which can only report what the branch last
  claimed before it disappeared. Say that in PRD §12 next to "state comes from the branch", or the
  two rules read as a contradiction.
- The write gate checks the anchor chain before `ready`, `unready` and `submit` record anything, so a
  terminal changeset cannot be reopened by a command that does not look at where its state came from.
- Abandoning a changeset whose branch is already gone is refused, with a reason pointing at the
  landed classification in M5: post-merge bookkeeping is not a lifecycle act.
- Out of scope and stated as such: removing `changesets/<slug>/` from the deployment branch. That is
  a commit on `main`, which git-pair does not make. `052e529` is the manual form, and the queue
  classification in M5 is what makes the leftover directory harmless until a landing command exists.

#### Verification

Abandon a changeset whose branch still exists, then delete the branch: `review queue` is silent,
`status` reports the terminal state from the anchor chain, and `change ready` on a recreated branch of
the same slug refuses naming the terminal marker — which is also the defence against the branch-name
reuse hazard. Abandoning without a branch refuses, and says why.

## Spikes and research

- `research/2026-09-17-queue-after-landing.md` — the queue's behaviour after a real squash merge,
  measured in this repository, with repro.
- Rejected: driving the queue off `refs/reviews/*`. A never-reviewed changeset has no ref, and a ref
  cannot report state it has no reason to have been moved for. Kept: refs as anchors (M4) and as the
  home of the terminal record (M6).
- Rejected: `ref == head` as the readiness test. It is satisfied by a block review, because
  `review submit` moves the ref to its own commit, and it fails after a changeset-only commit, which
  is the loop the tool is built around.
- Rejected: building the terminal marker with plumbing (`git commit-tree` against a ref) so it can be
  recorded after the branch is gone. Nothing in this repository does that today, it would add a third
  mutating verb to `internal/git` against the file's stated rule, and the case it serves is post-merge
  bookkeeping, which M5 classifies automatically and which is not a lifecycle act.
- Still open, small: whether `status` should show the anchor-vs-head relationship as its own field
  or as part of `reason`.

## Risks

| Risk | Mitigation |
| --- | --- |
| An agent commits implementation work without running `change unready`, so the reviewer sees a changeset in flux. | Accepted and intended: the cost is reviewer attention, not safety. M3 keeps the approval pinned to bytes, `review open` shows live HEAD, and the queue line names the anchored sha so the reviewer can see drift at a glance. Documented in PRD §22 as the author's obligation. |
| Deleting the drift flip removes the check that stopped `change complete` archiving unapproved content. | M3 exists for exactly this, and is the one place the old rule keeps a caller. Decided, not provisional: the archive names a reviewed head or completion refuses. |
| Rewriting README/PRD sentences that promise content-derived staleness. | Listed as tasks in M2 rather than left to discovery; grep the docs for "invalidates", "stale" and "since the ready marker" before M2 is considered done. |
| Deriving a branchless changeset from the anchor chain contradicts "state comes from the branch". | Framed as a fallback, not an overlay, in M6, with its own PRD sentence, and a test that it can only report what the branch last claimed. |
| `refs/reviews/*` are permanent and now written more often. | Intentional: they are the archival record. M6 gives them a terminal end; nothing deletes them. |
| `--changeset` on a marker command writing to the wrong branch. | Guarded in M5: marker commands require a matching branch and refuse otherwise. |

## Verification strategy

`mise run check` (gofmt, vet, tests) on every milestone. Unit tests in the package that owns the
rule — `internal/lifecycle` for derivation, `internal/cli` for command behaviour, following
`internal/cli/complete_test.go` as the shape. `docs/plans/completed/gitpr-mvp/artifacts/e2e-29.sh`
and `pty-walkthrough.sh` against a rebuilt binary, since both drive the real CLI and the queue's
human output. Each milestone ends with the dogfood loop in this repository: land it, run
`git pair review queue` on `main`, and expect silence.

The scenarios measured while writing this plan are the acceptance fixtures: ready/unready cycling,
approve-then-commit refusal, merged changeset absent from the queue, abandon-without-a-branch.

## Audit history

| Date | Audit | Summary |
| --- | --- | --- |
| 2026-09-17 | research/2026-09-17-queue-after-landing.md | Plan written. Landing behaviour measured live in this repo; ref-driven queue rejected; anchor check retained at completion. |
| 2026-09-17 | — | Four pending decisions settled with the reviewer, all as recommended. Pre-existing `status` anchor bug added to M4. |
