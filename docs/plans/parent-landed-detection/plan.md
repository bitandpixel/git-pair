# The parent landed while its branch stayed

## Goal

A stacked child can tell its author that the parent landed, while the parent's branch is still sitting
there. Today it cannot, and the two places that could see it both stop before reading the record.

Observed on 2026-09-23 in `fix/legacy-refs-and-remote-branches`, stacked on `fix/for-each-ref-glob`:

```
parent fix-for-each-ref-glob: archive 851df62, integration 4c89685 — recorded, published, and
                              4c89685 is current trunk
parent branch:                fix/for-each-ref-glob, still at 851df62, locally and on origin
child:                        READY, never reviewed, `Base: fix/for-each-ref-glob`, no `Stack:` section
```

`git pair status` printed nothing about the parent. The branch is present, its tip never moved after
the approval, so both questions the code asks of a parent answer "nothing happened":

- `parentSinceApproval` returns the zero value for a child with no approval
  (`internal/cli/stacked.go:55-57`), so a `READY` child is never asked.
- For an approved child, `st.Recorded == st.Tip` returns clean (`stacked.go:87-89`) before anything
  reads `refs/git-pair/*`. A landing does not move the parent's branch tip.
- `queue` notes a READY row sitting on a parent that moved ahead, via `behindParent`
  (`internal/cli/queue.go:307-322`), which returns 0 when the parent tip is an ancestor of the head.
  A landed, untouched parent is exactly that.

The goal is that `status`, `check` and `queue` name the landing and the step it leaves, in every clone,
and that no command moves a base, writes `CHANGESET.yaml`, rebases, or deletes a branch.

## Success criteria

- With the parent branch present and its landing recorded, `git pair status` on the child prints that
  the parent landed, the commit it became, and one of two advices chosen from the child's own history:
  the head already contains that commit → the branch is stale and can go; it does not → rebase onto it.
- The same statement appears for an unapproved (`READY`) child as a note, and for an approved child
  without changing whether its approval stands.
- `git pair check` passes a child whose parent landed and whose diff is unchanged, prints the same
  advice beside the verdict, and does not add a reason. A genuine post-approval parent move still
  refuses with today's wording.
- `git pair queue` prints a note for a READY child sitting on a landed parent whose branch is still
  there — the case it is silent about today.
- `--json` is additive: `parent` gains `landed`, `landed_commit`, `landed_in_default_branch` and
  `stale_branch`; no existing key changes meaning; no array is null.
- The measurement base, the printed `span`, and every existing relink and parent-movement test are
  unchanged. `Base:` still names `fix/for-each-ref-glob` while that branch exists.
- `mise run check`, `scripts/gates/e2e-29.sh` and `scripts/gates/pty-walkthrough.sh` pass at every
  milestone, and `PRD.md`/`README.md` move in the same commit as the behaviour they describe.

## Non-goals

Each of these was considered and rejected; keeping them out is part of the design.

deleting the branch changes the measurement base though too right? so if a parent branch is integrated I dont see a ton of benefit to keeping the measurement pure as the base/parent is going to change anyhow. lets move this to a thread to discuss.
- **No relink while the branch exists.** Relinking moves the measurement base, which changes the span a
  reviewer reads and changes what `Review-Parent-Head` is compared against. `relinkStacks`
  (`internal/changeset/resolve.go:535-549`) keeps its current trigger: the parent branch is gone.
- **No new `CHANGESET.yaml` field, and no restack to the integration ref.** `base:` and `parent:` are the
  only two shapes (`internal/changeset/changeset.go:474-482`); a `landed:` key would be a state file, and
  pointing `parent:` at `refs/git-pair/integrations/<id>` would destroy the tellability PRD §21 protects
  ("the branch name stays recorded in the changeset, so the two cases remain tellable apart") and would
  make the child unresolvable in a clone that never fetched the namespace.
- **No approval invalidation.** PRD §21's conservative rule is about the parent *branch* moving. A landing
  is not that, and treating it as one would drop approvals whose diff content is identical.
- **No git operations.** git-pair runs no `rebase`, no `branch -D` and no `push` outside the audited
  `internal/git/push.go`; the advisory prints the command, the human runs it.

## Context

The landing facts already exist and are already read elsewhere:

| question | answer comes from | already used by |
|---|---|---|
| did the parent land, as what | `refs/git-pair/integrations/<parent>` (`reviewref.ResolveIntegration`) | `parentGone` (`stacked.go:110-141`), `check` (`check.go:140-143`) |
| did that reach trunk, and where does it sit | `describeLanding` (`internal/cli/integration.go:761`) | `status`' own integration fields (`status.go:412`) |
| is it in trunk with no record at all | directory in the destination tree, no integration ref | `indexDurableRefs`/`unrecordedLandings`/`unrecordedHedge` (`internal/cli/landed.go:65-173`) |
| is the child already sitting on it | `git merge-base --is-ancestor <landing> <head>` | nothing yet |

`parentGone` already produces the sentence this plan wants ("the parent `%s` landed as `%s` — rebase onto
`%s` and have the result reviewed again"); it is reached only when `refs/heads/<parent>` fails to resolve.
The work is to ask the same question when it resolves, and to say the second thing that is true at the
same time: the branch is stale.

Two surfaces read the durable namespace in one pass and must stay that way: `queue` (`cost_test.go`
asserts the cost follows branches, not changesets) and `landed.go`'s `refIndex`. Any per-changeset
`rev-parse` added to the queue loop breaks that test.

Landed state this plan is checked against, in the repository at `4c89685`:

| changeset | archive | integration | branch still present |
|---|---|---|---|
| `fix-for-each-ref-glob` | `851df62` | `4c89685` (= trunk) | yes, `fix/for-each-ref-glob` |
| `feat-publish-the-records` | `6e1200c` | `816af32` (on its parent branch, carried into trunk by `ba921b0`) | its branch is gone |

## Constraints

- PRD §13: a record answers from `refs/git-pair/*`, never from `refs/remotes/**`. The mirror refs fetched
  by `--configure-fetch` are for the published check (`internal/cli/published.go`) and do not count here.
- PRD §22: the CLI is the agent surface — no prompts, and `--json` contracts only grow.
- `internal/hygiene` stays green: no new `push`/`merge`/`rebase`/`reset`/`switch`/`checkout`/`branch`
  invocation outside the audited files.
- "no record" is not "not landed" in a clone that never fetched the namespace. Any note about an
  unrecorded landing must go through `unrecordedHedge`/`namespaceEmpty` wording rather than assert.
- A note is a note. New statements go to the notes stream (`a.warn`) and the JSON fields; they do not
  enter `reasons`, which means "do not integrate this".

## Assumptions

- The reviewer agrees that the advisory belongs on `status`/`check`/`queue` rather than on a new
  `git pair stack` subcommand. If they want the separate command, milestone 3 is where it goes; the
  derivation, the notes and the tests below are the same either way.
- `parent-changeset:` is present on the children that matter. Where it is missing, the branch alone
  cannot say the parent landed — no ref name is derivable — and the note stays silent rather than
  guessing; `status` already reports a missing parent changeset elsewhere in the `Stack:` section.
- `fix-legacy-refs-and-remote-branches` (in review, READY) changes `reviewref.List`/`Kind`,
  `landed.go` and `published.go`. This plan reads through `ResolveIntegration` and `indexDurableRefs`
  rather than `List`, so the two branches touch the same files at different depths.

## Milestones

### M1 — The read, in `status`

Deliverables

- A child whose parent landed sees it in `git pair status`, whether or not the child has an approval,
  whether or not the parent's branch is still present, and whether or not the landing reached trunk.
- `--json` carries the same finding in `parent.landed`, `parent.landed_commit`,
  `parent.landed_in_default_branch`, `parent.stale_branch`.
- PRD §21 "When the parent lands" says both cases (branch gone, branch present) and README's Concepts
  and Troubleshooting entries name the stale-branch state.

Tasks

- Add `Landed`, `LandedIn` and `StaleBranch` to `parentStatus` (`internal/cli/stacked.go:29`) and a
  `parentLanded` helper that resolves `reviewref.ResolveIntegration(c.ParentChangeset)`, calls
  `describeLanding`, and tests whether the head already contains the landing commit.
- Call it in `parentSinceApproval` before the `st.Recorded == st.Tip` return (`stacked.go:87-89`) and
  make the approval-free early return (`stacked.go:55-57`) run the note path only, so a `READY` child
  gets the note and never a refusal.
- Land a parent whose branch is gone must keep coming out of `parentGone` unchanged: the new call sits
  after the `st.Tip == ""` branch, not before it.
- Write the two note texts and choose them from the child's own ancestry:
  contains → "parent `%s` landed as `%s`; your head is on it, and the branch `%s` is stale — delete it";
  does not contain → "parent `%s` landed as `%s`; rebase onto it".
- Add `parentJSON` fields (`internal/cli/status.go:75-83`) and print the line in the `Stack:` section
  rendered at `status.go:386-399`.
- Tests first, in the `gittest` style of `internal/cli/status_stack_chain_test.go`: recorded + branch
  present + head on the landing commit; recorded + branch present + head not on it; recorded into a
  non-default `--target`; in trunk with no record (the `unrecordedHedge` wording); branch deleted
  (existing expectations unchanged); no `parent-changeset:` (silent).

Verification

- `mise run test`, then the real repository: `git pair status` in `/home/david/dev/worktrees/git-pair/legacy-refs`
  gains the landed line naming `4c89685` and calls `fix/for-each-ref-glob` stale, with `Base:` unchanged
  and `Span:` still `fix/for-each-ref-glob...current`.
- `git pair status --json | jq .parent` on the same branch shows the four new keys and no changed key.

### M2 — The same finding in the two gate surfaces

Deliverables

- `git pair check` prints the landing beside a passing verdict, and its `next_action` names the stale
  branch or the rebase; a child whose parent landed with an unchanged diff still exits 0.
- `git pair queue` notes a READY child sitting on a landed parent, which is the case `behindParent`
  cannot see.
- Both surfaces still refuse nothing new.

Tasks

- In `internal/cli/check.go`, report the parent's landing next to `IntegratedAt` (`check.go:138-143`)
  and carry the stale-branch or rebase step in `next_action` (`check.go:177`).
  Keep `integrationReasons` (`check.go:173`) fed by `parent.Reason` only.
- Do not put the step inside `landingNextAction` (`internal/cli/status.go:631-640`). It is the landing
  contract spelled once and shared by `status`, `check`, `change ready` and `review`; teaching it about
  parents would make the same string mean two things in four commands, and it takes only a base. The
  parent's step is a separate note beside it.
- In `internal/cli/queue.go`, extend the `behindParent` loop (`queue.go:151-159`) so a landed parent is
  reported even when the parent tip is an ancestor of the head, reusing `indexDurableRefs` for the ref
  reads so the cost test keeps its shape, and print through `printBehindParent`'s notes stream.
- Tests: a READY child on a landed parent produces the queue note and no row change; the same child
  approved and un drifted produces a passing `check` whose `next_action` names the stale branch; the
  queue's git-invocation count is unchanged by a repository with 300 extra refs (`cost_test.go`).

Verification

- `mise run check`, then `scripts/gates/e2e-29.sh`: the §29 loop's exit codes are unchanged for an
  unstacked changeset and for a stack whose parent is still live.
- The real repository: `check` on `fix/legacy-refs-and-remote-branches` still exits 1 only for the
  existing reason ("marked ready and has not been reviewed since"), and mentions the parent landing.

### M3 — The step, spelled as a command

Deliverables

- Every note prints the exact git command that settles it — the `git rebase --onto <landing> …` when the
  head is not on the landing commit, the `git branch -D <parent>` when it is — with the worktree blocker
  named when another worktree holds that branch.
- README's landing walkthrough says where this note appears in the "record → publish → tidy" order, so
  the child's stale parent is the ordinary next thing an author hits after landing a parent.

Tasks

- Put the command strings next to the note text in `stacked.go`, so the advice and its reason travel
  together as they already do for `Next` (`stacked.go:38-41`).
- Detect the worktree blocker with `git worktree list --porcelain` read-only, and name the
  `git worktree remove <path>` line only as text.
- Add the note to the §29 gate script's expected output where the walkthrough lands a stack, or record
  in the script why it does not appear there.
- If the reviewer asks for a dedicated surface, add `git pair stack --json` here as a read-only view over
  the same derivation, with no new state and no new git subcommands.

Verification

- Copy-paste the printed commands in a scratch clone: they run, and `git pair status` afterwards reports
  the parent branch gone and the base relinked by the existing `relinkStacks` path.
- `mise run gates` passes, including the pty TUI walkthrough.

## Spikes / research

None planned. Two questions are answered by reading, and the answers belong in the commit that uses them:

- Does `describeLanding` already name "not in trunk" for a landing on a release branch in the wording
  this note needs? Read `integration.go:761-800` before writing M1's fourth test.
- Does `parentJSON`'s existing `Note` field reach the TUI as well as `status`? Read
  `internal/tui/session.go` before deciding where the note prints in M1.

## Risks

- **A note that reads like a refusal.** Every new sentence here is a note; if any of them lands in
  `reasons`, a pipeline starts refusing landings that were fine. Mitigation: the M2 tests assert the
  exit code stays 0 with the note printed.
- **The unfetched-namespace false finding.** A clone with no `refs/git-pair/*` at all would otherwise say
  "unrecorded" about every landed parent. Mitigation: route through `unrecordedHedge` and
  `namespaceEmpty`, as `landed.go` does.
- **Cost growth in `queue`.** Mitigation: reuse `indexDurableRefs`; keep `cost_test.go` green and extend
  it rather than working around it.
- **Conflict with `fix-legacy-refs-and-remote-branches`.** It edits `landed.go`, `published.go`,
  `reviewref.go` and `integration.go`. Mitigation: read through `ResolveIntegration` and `indexDurableRefs`,
  not `List`; rebase this branch onto trunk once that one lands.
- **The note becomes noise** once the child has been rebased and the branch simply has not been deleted
  yet. Mitigation: the stale-branch wording is the low-severity variant, printed by `a.warn`, and the
  two flavours are chosen from ancestry rather than repeated for every state.

## Verification strategy

Unit and integration tests in `internal/cli` with `gittest` fixtures carry the state matrix; the real
repository at `4c89685` is the check that the fixture states are the ones people actually have. The two
gate scripts are the contract for exit codes and wording, so they run at every milestone rather than at
the end. `docs_contract_test.go` is the guard that PRD and README name commands and refs that exist.
Manual verification is the copy-paste test in M3: the printed commands must be the commands that work.

## Audit history

| Date | Audit | Summary |
|---|---|---|
| — | — | Plan written before implementation. |
