# feat-parent-landed-impl

## Summary

Implements `docs/plans/parent-landed-detection/plan.md` in six milestones, one commit each. The plan came
from a session where a parent changeset landed, its branch was deleted, and nothing in git-pair said so: the
child reported a diff against a branch that no longer existed, `integration record` asked for a `--source`
the command derives itself, and `status` on the destination branch — the branch every changeset eventually
lands on — was the one place that never asked whether a record had left the clone.

Two halves. Milestones 1–3 report a landed parent in `status`, `check` and `queue`, and print the command
that settles the child. Milestones 4–6 fix what that state breaks: the measurement base moves to the
parent's record, `git pair integration record` answers from the landing rather than from a branch a tidy-up
deletes, and `git pair init` records a base that reads the same on every machine.

## What changed

- **M1 `76017e7` — `status`.** `parentStatus` gained `Landed`, `LandedInDefaultBranch`, `LandedReach`,
  `StaleBranch` and `ParentWorktree`; the parent read now runs for every state instead of only an approved
  child, so a refusal for an unapproved child became a note. Split into `parentLive`, `parentLanded` and
  `parentInTrunkUnrecorded`. `parentJSON` carries `landed`, `landed_commit`, `landed_in_default_branch`,
  `landed_reach`, `stale_branch`; the line prints in the `Stack:` section.
  Tests: `internal/cli/status_parent_landed_test.go`.
- **M2 `5776851` — `check`, `queue`, and the destination branch.** `check` prints
  `parent: <id> landed as <sha> — <step>` beside a passing verdict and extends `next_action`, with
  `parent_landed`, `parent_landed_commit`, `parent_stale_branch` in JSON; `queue` notes the same state through
  `landedParentNote` (`parent_notes` in JSON), the case `behindParent` cannot see because the parent's tip is
  an ancestor of the child's head. `status` on the destination branch now runs the published comparison
  through `publicationReport` before its usage error, so `RECORDED, NOT PUBLISHED` is printed where it is
  most likely to matter. Tests: `internal/cli/parent_landed_surfaces_test.go`.
- **M3 `56a79c2` — the remedy is printed.** `landedParentStep` prints
  `git rebase --onto <landing> <parent-branch> <child-branch>` while the head is off the landing and
  `git branch -D <parent-branch>` once it is on it, naming the worktree when another worktree holds the
  branch. Nothing is run. README's walkthrough says why an unrecorded landed parent is urgent; the e2e gate's
  header records why its own replay cannot show these notes.
- **M4 `c679e25` — the base moves to the record.** `relinkStacks` triggers on "the parent's record resolves"
  rather than on the branch being absent, so the child is measured against
  `refs/git-pair/integrations/<parent>` whether or not the branch is gone. Approval survival is decided by
  content: `landedBaseIsTheSameWork` compares the two `base...head` diffs and keeps the approval only when
  they are identical, and drops it when the comparison cannot be made. `derivationDestinations` refuses to
  derive a `--target` under `refs/git-pair/`, and `Review-Parent-Head` still records the parent's branch tip.
  PRD §21 rewritten. Tests: `internal/cli/parent_relink_test.go`.
- **M5 `8867a02` — the record derives from the landing.** `deriveSource` layers: a merge landing names the
  reviewed chain as the side it introduced (recordable after `git branch -D`); a one-parent landing is read
  as the fast-forward only when a permitting marker for the changeset sits on the destination's own
  first-parent line and that marker's `Review-Head` is an ancestor of it (`ffChainTip`); otherwise the branch,
  then `--source`. A branch downstream of the landing is never a source candidate. The two discovery passes
  now answer one question — pass 1's settled id is threaded into pass 2, which honours `--target` and
  `--default-branch` and subtracts landed *and* already-recorded ids. "Unfinished" means a missing integration
  or a half-pair. A flagless run where everything is recorded exits 2 with `nothing to record` and names no
  candidates; a run that can name one changeset still exits 0 with `already_recorded`. README and PRD §11.4
  changed in the same commit. Tests: `internal/cli/record_derivation_test.go`.
- **M6 `9effa7c` — the base is a branch.** `init` records `base: main` where that name resolves, and the
  fetch ref where it does not; `git clone` records `refs/remotes/origin/HEAD`, which the resolution prefers
  over a local trunk, so an ordinary clone used to write `base: refs/remotes/origin/main` and `status` read
  that back as a fetched ref for a change that had never been pushed. `init` also notes a base whose local
  copy and remote copy differ, with the counts. Tests: `internal/cli/init_base_test.go`.

## Design decisions

- **Detection first, then the base move.** M1–3 change no measurement; M4 changes the measurement and is the
  commit that can change a verdict. Keeping them separate is what makes the second reviewable.
- **A note is not a reason.** These findings go to `a.warn` and to JSON, never into `check`'s `reasons`,
  because `reasons` means *do not integrate this* and a landed parent means the opposite. Every surface keeps
  its exit code; a child whose parent landed with an unchanged diff still passes.
- **`landingNextAction` is untouched.** It spells the landing contract for four commands and takes only a
  base. The parent's step is a separate note beside it, not a second meaning inside it.
- **Approval survival is content, not bookkeeping** (PRD §21). Identical `base...head` under the old base and
  under the landing keeps the approval; a difference drops it; an unreadable comparison drops it.
- **The layers answer "which commits" from graph facts, and markers license the one layer that needs it.** The
  `Review-Head` ancestry requirement is the half a squash cannot satisfy: a squash can copy a marker's text
  into its own message and cannot copy the reviewed head into trunk. It is also the half that makes a
  rebase-merge decline rather than record a copy.
- **Records are create-only, so "record it again" is never a finding.** That is why the all-recorded flagless
  run has no candidate list to print, and why the exit code is 2 rather than 0: the command could not say
  which changeset it meant, and a landing that sits unrecorded while `tidy` may delete the branch holding the
  child's chain is the state this command exists to prevent. §22's idempotency stays where it was earned —
  with a run that identified one changeset.
- **Deliberate deviations, all recorded in their commit messages.** M1: the `Span:` label keeps the ref name
  (`Span: refs/git-pair/integrations/alpha...current`) rather than the landing commit, and the fetch remedy
  for a clone that lacks the record prints in the `Stack:` section instead of the `Base:` line. M5: the
  `Review-Head` ancestry requirement was *not* added to `verifyReviewedSource`, so a caller who names
  `--source` is not newly refused; the guard is applied in the layer that derives the head, which is where a
  copied marker could otherwise produce a record nobody approved. M2's JSON keys are `unpublished` and
  `landed_unrecorded`, not the plan's `unrecorded`, to match the words the human surface prints.

## Validation

- `mise run check` green: gofmt, `go vet`, every package.
- New tests: `status_parent_landed_test.go`, `parent_landed_surfaces_test.go`, `parent_relink_test.go`,
  `record_derivation_test.go`, `init_base_test.go` — 21 cases over the state matrix (branch present and
  gone, landing in trunk with and without a record, non-default `--target`, worktree holding the parent,
  approval survival and its loss, each derivation layer, the downstream-branch refusal, the base spelling and
  its fallback).
- Real CLI runs against scratch clones, not only fixtures: a fast-forward landing recorded with **no flags and
  no branch present**, archive and integration naming one commit with the reason printed; a merge landing
  recorded after `git branch -D` with the archive read from the merge's second parent; the flagless run over
  three landed changesets recording the one that needed it and refusing with `nothing to record` when all
  three were finished; `init` writing `base: main` and printing the divergence note with `1 here that origin
  does not have`.
- Gate scripts (`scripts/gates/e2e-29.sh`, `scripts/gates/pty-walkthrough.sh`) run at handoff; their output is
  in the review thread.

## Known limitations

- The e2e-29 replay cannot show the landed-parent notes: it lands and records in one order and never leaves a
  parent in the state these notes describe. The header comment says so, and the fixture tests carry that
  state.
- A landing that reached trunk without a record is reported with the hedge in `unrecordedHedge` — git-pair can
  see the directory in the destination's tree and cannot see whether a record exists in a clone it has not
  fetched, so it says "unrecorded here" rather than "never recorded".
- The fast-forward layer declines on a rebase-merge, which is the conservative answer: the marker it replays
  points at a head that is no longer in the destination's history, so the record needs the branch or
  `--source`.
- `base:` is the branch name only where that name resolves. A clone holding the integration branch solely
  under `refs/remotes/` still records the qualified ref, because the alternative is a changeset no command can
  read. Read-time resolution is unchanged and tries `refs/heads/` before `refs/remotes/`.
- `integration record` in a clone with no `refs/git-pair/*` at all prints the empty-namespace warning (§13.4.1)
  on the same run that writes the first pair. Pre-existing, and arguably right — the clone is being told what
  it was missing before it is told what it now has.

## Open questions

- Should the empty-namespace warning be suppressed when the command itself is about to create the first ref?
  Deferred: it is one line of noise on a path where the warning is also the explanation.
- The plan's M1 verification names the real repository at
  `/home/david/dev/worktrees/git-pair/legacy-refs` (`fix/legacy-refs-and-remote-branches`) as the dogfood
  check with a locally built binary. That check is worth repeating with the installed binary once this lands,
  since the installed one predates M1.
