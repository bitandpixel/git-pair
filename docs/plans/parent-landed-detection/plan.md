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
- Milestones 1–3 change no measurement base, no printed `span`, and no existing relink or parent-movement
  test. `Base:` still names `fix/for-each-ref-glob` while that branch exists.
- Milestone 4, if the thread decides it, changes that deliberately: `Base:` names the parent's integration ref
  once a record exists, and `Span:` names the child's own work rather than the parent's plus trunk's.
- Milestone 5: a landing of any shape — fast-forward, `--no-ff` merge, squash, cherry-pick, rebase-merge — is
  recordable correctly, and a re-run of `integration record` on a changeset that already has its pair exits 0
  saying so without needing `--source`.
- Milestone 6: a changeset created by `init` on a clone that has fetched records `base: main`, not
  `base: refs/remotes/origin/main`, and that base still resolves in a clone which has only the remote ref.
- `mise run check`, `scripts/gates/e2e-29.sh` and `scripts/gates/pty-walkthrough.sh` pass at every
  milestone, and `PRD.md`/`README.md` move in the same commit as the behaviour they describe.

## Non-goals

Each of these was considered and rejected; keeping them out is part of the design. One of them was rejected
over the reviewer's objection and is no longer a non-goal — see the thread
`changesets/feat-parent-landed-detection/measurement-base-when-the-parent-lands.md`.

- **The relink trigger is open, not settled.** The reviewer's finding (review `a87ae0b`) is that the branch
  deletion moves the base anyway, so the current trigger decides only *when*, and the delay is what makes a
  rebased child's diff show the parent's work and trunk's work as the child's own. Measured in a scratch
  repository: `c.txt p.txt trunk.txt` for a base of the parent's branch tip against `c.txt` for the
  integration ref. Milestone 4 carries it; `relinkStacks` (`internal/changeset/resolve.go:535-549`) is
  untouched until that decision is made.
- **No new `CHANGESET.yaml` field, and no restack to the integration ref.** `base:` and `parent:` are the
  only two shapes (`internal/changeset/changeset.go:474-482`); a `landed:` key would be a state file, and
  pointing `parent:` at `refs/git-pair/integrations/<id>` would destroy the tellability PRD §21 protects
  ("the branch name stays recorded in the changeset, so the two cases remain tellable apart") and would
  make the child unresolvable in a clone that never fetched the namespace.
- **No approval invalidation in milestones 1–3.** PRD §21's conservative rule is about the parent *branch*
  moving. A landing is not that, and treating it as one would drop approvals whose diff content is identical.
  Milestone 4 reopens exactly this question, because relinking changes what `Review-Parent-Head` is compared
  against, and it has to be answered before that trigger moves.
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

### M2 — The same finding in the two gate surfaces, and the half the destination branch never asks

Deliverables

- `git pair check` prints the landing beside a passing verdict, and its `next_action` names the stale
  branch or the rebase; a child whose parent landed with an unchanged diff still exits 0.
- `git pair queue` notes a READY child sitting on a landed parent, which is the case `behindParent`
  cannot see.
- Both surfaces still refuse nothing new.
- `git pair status` on the destination branch reports the namespace-wide findings even though it fails.
  Half of that already happens: `landingsOnNoChangeset` (`internal/cli/status.go:237-256`) prints the
  directories that landed with no record beside the `no changeset for this branch` failure. The
  published-or-not comparison does not run on that path at all (`status.go:198` is on the success path), so
  the one branch every changeset eventually lands on is the one branch where a record that never left the
  clone is invisible. Measured on 2026-09-23: three directories recorded, two of them unpublished, and the
  output is one line — `no changeset for this branch: changesets/main`.

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
- `landingsOnNoChangeset` reuses the `indexDurableRefs` result it already reads and calls
  `publicationReport` (`internal/cli/published.go:64`) with an empty branch — which `lookupDurableRemote`
  (`internal/cli/fetch.go:51-62`) already defines as "no particular branch", so origin is the answer — then
  prints through `printUnpublished`. The cost is one local `for-each-ref` over the mirrors and no network.
- Exit 2 and the first line stay as they are: the branch really does hold no work in progress, and `queue`
  and CI read that status. The findings are notes printed beside the answer, never the answer.
- `--json` on that path emits a document with `unrecorded`, `unpublished` and their notes instead of nothing
  but the error, with empty lists rather than missing keys — the shape `statusJSON` already commits to for
  `unpublished` (`status.go:151-157`), for the reason stated there: a missing key reads as "this build does
  not know how to look".
- Docs travel with the behaviour, as always. `README.md:242` already says `status` and `queue` print
  `RECORDED, NOT PUBLISHED` — true standing on a changeset branch, false on the destination branch today. Spell
  where in that sentence, and let `internal/cli/docs_contract_test.go` check the names the prose uses.
- Tests: trunk with no work in progress, two changesets recorded locally and one of them mirrored, prints
  `RECORDED, NOT PUBLISHED` naming both and still exits 2; `--json` on the same repository carries both keys
  with both ids; a clone with no remote prints the sentence saying the question cannot be asked, not an empty
  list that reads as "all published".

Verification

- `mise run check`, then `scripts/gates/e2e-29.sh`: the §29 loop's exit codes are unchanged for an
  unstacked changeset and for a stack whose parent is still live.
- The real repository: `check` on `fix/legacy-refs-and-remote-branches` still exits 1 only for the
  existing reason ("marked ready and has not been reviewed since"), and mentions the parent landing.
- The real repository, destination branch: `git pair status` on `main` prints the two unpublished ids under
  `RECORDED, NOT PUBLISHED` where today it prints only the `no changeset for this branch` line, and
  `git ls-remote origin 'refs/git-pair/*'` is the check that says which half is right.

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

### M4 — The relink trigger (decided in the thread)

Decided in review `f443b7c`: a landing keeps an approval when the diff is identical, and two clones printing
different `Base:` values is acceptable because they agree deterministically given the same refs. The three
practical issues that disagreement produces, and the two guards it needs, are written up in
`changesets/feat-parent-landed-detection/measurement-base-when-the-parent-lands.md`. Tasks are still written as
tests first, so the §21 wording lands against passing expectations rather than prose.

Deliverables

- A child whose parent is recorded measures against `refs/git-pair/integrations/<parent>` while the parent's
  branch is still present, and its `Span:` names only the child's own work — including the rebased-onto-trunk
  case where the parent's branch tip makes the parent's work and trunk's work appear as the child's.
- An approval survives the relink when `base...head` is identical under both bases, and does not survive it
  when the content differs. PRD §21 says so in the same words `status` and `check` use, replacing the reading
  that treats all parent movement alike.
- A clone that has not fetched the namespace keeps the branch base and prints the fetch remedy on the line
  where the base is reported, instead of refusing with `unknown revision` or letting a reviewer read the noisy
  diff unknowingly.
- `Review-Parent-Head` keeps naming the parent's branch tip while that branch exists, so two clones submitting
  the same child record the same value. The landing is a separate recorded fact, not a substitute tip.
- `integration record`'s derived `--target` never offers a base under `refs/git-pair/` as a destination.

Tasks

- Write the two probe rows as fixtures first: child not yet on the landing commit, and child rebased onto
  trunk with trunk having advanced independently since the parent branched. Expect the second to print the
  child's own files only after the trigger moves.
- Change the trigger from "the parent branch is gone" to "the parent's record exists and resolves", keeping
  the `RevParse` guard, and keep the deleted-branch path's existing expectations passing unchanged.
- Compare `base...head` under the old base and the new one at the point of the decision, and gate the
  approval's survival on that comparison. Give the two outcomes separate tests, since the whole rule is the
  difference between them.
- Keep `changeset.ParentOf`'s reported tip as the branch while the branch exists (`internal/changeset/changeset.go:295-330`)
  and carry the landing beside it, so a submission never writes a value another clone would not write.
- Skip a `refs/git-pair/` base in `derivationDestinations` (`internal/cli/integration.go:252-275`) so a
  durable ref is never named as a landing destination, and update the refusal text's candidate list.

Verification

- The real repository: `git pair status` in `fix/legacy-refs-and-remote-branches` prints
  `Base: refs/git-pair/integrations/fix-for-each-ref-glob` and `Span:` naming `4c89685...current`, with the
  branch still present.
- A clone with the namespace emptied by fixture setup, not by deletion, still answers with the branch base.
- `mise run gates` passes, and the relink tests added by `feat-lineage-in-the-surface` still pass untouched.

### M5 — The destination is a branch: record derivation layers

From the `pi-heartthrob` session on 2026-09-23, where `git pair integration record` refused to record the one
changeset that needed it while asking for a `--source` that the command never uses. The four landing shapes
come out of one table, and every layer answers "which commits" from graph facts.

```
1  tip introduced the directory over its first parent and has two parents   archive = tip^2, integration = tip
2  introduction on the tip's first-parent line, strictly below the tip,
   the tip's marker walk permits integration, and that marker's
   `Review-Head` is an ancestor of the tip                                  archive = integration = tip
3  neither, and a branch carries the directory                              archive = branch tip
4  none of the above                                                        --source / --commit
```

Layer 2 is the fast-forward and the rebase-merge, where trunk holds the chain itself. It is licensed by a
marker and checked by `Review-Head` ancestry, because a squash can carry the marker *text* into its own
message — `parseTrailers` (`internal/lifecycle/lifecycle.go:552-569`) matches any `Review-*:` line and
`git merge --squash` copies the branch's marker commit bodies — while it cannot carry the reviewed head, which
is the commit a squash deliberately keeps out of trunk.

The layers fix *which commits*. A second defect in the same command fixes *which changeset*, and it is what made
the reported session need five invocations. The recorder asks that question twice, with different filters, and
the weaker answer wins:

```go
// resolveIntegrationRecord — internal/cli/integration.go:154-183
in = deriveMissingTips(...)   // pass 1: changesetToDerive (:292-300) drops ids that already have a record
}                             //   and ids in RecordedPair, settles ONE id, feeds deriveLanding + deriveArchiveTip
  cands, _ := changesetsAtSource(source, in)              // pass 2 (:644-666) re-reads the changeset from the
  id, err := chooseChangeset(cands, source, in.changeset) //   SOURCE tree, subtracts only the directories the
                                                          //   default branch carries — via
                                                          //   DefaultBranch(ctx, repo, ""), so no --target and
                                                          //   no --default-branch — and returns EVERY directory
                                                          //   when that subtraction empties the set
```

Pass 1 resolved `fix-label-word-timer` in the reported repository, derived both commits from it, and then pass 2
re-opened the choice and refused. The rule pass 2 implies: one directory at the source works, because
`len(here) < 2` skips the subtraction; two where the default branch carries one works; two where it carries both
refuses. Landing is what puts a directory in trunk, so from the second landing onward a branch cut after the
first carries two directories and trunk holds both — the flagless command stops working for every later
changeset in the repository, whatever is published or fetched. Publishing fixes none of this: the four records
were in the local namespace and pass 1 read them. Pass 2's own comment states the assumption that breaks here —
"over-reporting candidates is safe because `--changeset` decides between them" — which holds for a caller who
named a `--source` and not for the flagless flow, where nothing tells the caller they are being asked to choose.

Deliverables

- A fast-forward landing records with no flags and says it derived a fast-forward, with `archive ==
  integration` naming the reviewed chain's tip. `reviewref.CreatePair` already permits equal shas.
- A merge landing records with `archive` at the introduced side's tip (`tip^2`, or `git rev-list tip --not
  tip^1`) so a landing can be recorded after its branch is deleted — for merge landings only. Squash and
  cherry-pick still need the branch or `--source`, because nothing else holds the chain.
- Re-running `integration record` on a recorded changeset is a no-op that exits 0, whatever the branch set
  looks like.
- The flagless command records the one unrecorded changeset when trunk carries every directory: three landed,
  two recorded, and the third is named and written with no flags at all.
- A named `--source` and a flagless run answer which-changeset the same way, so the recorded ones are not
  candidates in either.
- A branch downstream of the landing is never a source candidate, so a landed directory does not turn every
  trunk-descended branch into a claim.
- The recorder refuses, with the same wording, an unreviewed head, a superseded verdict, a blocked changeset,
  an abandoned changeset, and a child recorded at its parent's head.
- PRD §11.4's four checks state the fast-forward shape and the `Review-Head` requirement; README's "record
  before tidy" sentence names which shapes still need the branch; §13.1's archive definition is unchanged and
  says why the archive never names a merge commit.

Tasks

- Tests first, fixtures for each shape: fast-forward (single commit and multi-commit), `--no-ff` merge, merge
  plus a later trunk commit, squash, squash plus a commit on trunk, rebase-merge, `init --no-commit` chain.
- Give `verifyIntegrationRecord` (`internal/cli/integration.go:486-492`) the fast-forward carve-out, gated on
  layer 2 having been established, so the "commit does not add the directory over its first parent" refusal
  stays for a follow-up commit on the destination.
- Add to `verifyReviewedSource`: the newest permitting marker's `Review-Head` must be an ancestor of the
  source. Absence is a decline to layer 3, not a refusal, per §21's treatment of a missing
  `Review-Parent-Head`.
- `deriveArchiveTip` (`internal/cli/integration.go:371-403`) drops candidates that are descendants of the
  derived landing commit — one `merge-base --is-ancestor` each — and keeps the destination exclusion. Measured
  on the reported repository, that leaves `feat/more-labels` alone from four candidates, and
  `fix/thinking-verb` alone from three.
- Reorder `integration record`: consult `reviewref.RecordedPair` (`integration.go:846`) after the landing is
  derived and before `deriveArchiveTip` (`integration.go:238`), reusing the two existing messages at
  `:1026` and `:1031`. `changesetToDerive`'s own comment already promises the CI re-run this ordering breaks.
- `derivationDestinations` (`integration.go:252-275`) asks both spellings of the integration branch —
  `refs/heads/main` and `refs/remotes/origin/main` — deduplicated by commit as it already does, and skips a
  base under `refs/git-pair/`.
- `deriveMissingTips` returns the id pass 1 settled on, and `resolveIntegrationRecord` passes it as `named` to
  `chooseChangeset` (`integration.go:671`) instead of only `in.changeset`. A resolved id is not re-opened
  downstream; this one line of plumbing is what makes the flagless case work.
- `changesetsAtSource` (`integration.go:644-666`) subtracts recorded ids as well as directories the destination
  carries, using the same `reviewref.List` read pass 1 uses, and keeps the guard that the subtraction must never
  empty the set.
- The same call stops hard-coding `changeset.DefaultBranch(ctx, repo, "")`: it honours `--target` when the
  caller named one and `--default-branch` when they overrode it, and subtracts across both spellings of the
  branch.
- Cost stays bounded: one `rev-list`, one `merge-base` per candidate, and the marker walk the recorder
  already runs. Nothing per-branch enters `queue`.

Verification

- The reported failure reproduces from fixtures: a destination ref behind local trunk, one recorded directory
  and one unrecorded one, three trunk-descended branches carrying the landed directory. Flagless
  `integration record` records the unrecorded changeset and exits 0.
- The which-changeset rule is pinned at its three boundaries: one directory at the source, two where trunk
  carries one, two where trunk carries both. The third is the reported refusal today and a recording after the
  fix. Measured in `pi-heartthrob` on 2026-09-23, flagless lists `feat-more-labels`, `fix-label-word-timer` and
  `fix-thinking-verb` from `8ffdb3a`; the same command with `--changeset fix-label-word-timer` writes archive
  `8ffdb3a` and integration `acd1fd7` and exits 0 — so the fix is the plumbing, not the verification.
- Each of the five refusals still fires, with its existing wording.
- `mise run gates` passes, and `e2e-29.sh`'s landing loop is unchanged for a merge landing.

### M6 — `base:` names a branch, not a ref (separable)

Kept separate because it changes what git-pair *writes* into committed content, which reaches every future
changeset, every fresh clone and every CI job, while milestone 5 changes only reads.

Deliverables

- `init` with no `--base` records the branch name (`base: main`) rather than the ref that happened to resolve,
  so a changeset does not pin one clone's remote-tracking ref.
- A bare name in `base:` resolves through the `DefaultBranch` ladder trying `refs/heads/<name>` then
  `refs/remotes/origin/<name>`, deterministically and documented; `--base <ref>` stays literal, because a
  caller who names a ref means that ref.
- When the local branch and its remote-tracking counterpart differ, `status` and `queue` say so: landings
  since the last fetch read as work in progress.

Tasks

- `changeset.DefaultBranch` (`internal/changeset/resolve.go:100-146`) keeps its ordering for the read-time
  integration branch — remote-first is principled, since `landed` means landed upstream — and gains a
  name-to-ref resolution used when a recorded `base:` is a bare branch name. `git rev-parse main` does not
  resolve to `refs/remotes/origin/main`, so this is explicit logic rather than `RevParse`.
- `init`'s default records the short name. Explicit `--base` values are untouched, and values already
  committed keep resolving.
- The divergence note is one `rev-list --count` printed to the notes stream. It is the line that would have
  made the reported confusion self-explanatory.
- Tests: a clone with only `refs/remotes/origin/main` resolves `base: main`; a clone with only local `main`
  resolves it; `--default-branch` still overrides both, which is what a single-branch CI checkout needs.

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
- **Two clones, two bases** — the cost milestone 4 buys. `refs/git-pair/integrations/<id>` is not fetched by
  default, so a relink keyed on the record prints one `Base:` in a fetched clone and another in a clone that
  has not fetched, against README's rule that two clones of the same commits cannot disagree. Mitigation:
  keep `relinkStacks`' existing `RevParse` guard so the unfetched clone keeps the branch base, and print the
  fetch remedy beside the line rather than pretending the two agree.
- **A derivation layer weakening a refusal.** Milestone 5 relaxes the transition check, which is one of the
  four checks that makes a record worth reading later. Mitigation: the carve-out fires only after layer 2 is
  established, and the five refusals — unreviewed, superseded, blocked, abandoned, wrong head — are pinned by
  tests this milestone may not edit.
- **Prose as evidence.** `parseTrailers` matches any `Review-*:` line, so a squash message can read as a
  marker. Mitigation: every layer keeps at least one graph or tree fact, and `Review-Head` ancestry is the one
  a squash cannot fake.
- **A stale destination ref choosing the changeset.** `changesetToDerive` answers from the directories the
  destination carries, so a `refs/remotes/origin/main` behind local trunk hides an unrecorded landing and
  turns the retry heuristic into the wrong answer. Mitigation: milestone 5 asks both spellings, milestone 6
  notes the divergence, and neither makes a local-only merge count as integrated.

## Verification strategy

Unit and integration tests in `internal/cli` with `gittest` fixtures carry the state matrix; the real
repository at `4c89685` is the check that the fixture states are the ones people actually have. The two
gate scripts are the contract for exit codes and wording, so they run at every milestone rather than at
the end. `docs_contract_test.go` is the guard that PRD and README name commands and refs that exist.
Manual verification is the copy-paste test in M3: the printed commands must be the commands that work.

## Audit history

| Date | Audit | Summary |
|---|---|---|
| 2026-09-23 | review `a87ae0b` | The reviewer rejected the "no relink while the branch exists" non-goal: deleting the branch moves the base anyway. The probe showed the delay is what makes a rebased child's diff carry the parent's and trunk's work. Non-goal withdrawn, thread opened, milestone 4 added. |
| 2026-09-23 | review `f443b7c` | PRD §21 settled in the thread: an approval survives a relink when the diff is identical. The clone disagreement is accepted, with two guards — `Review-Parent-Head` stays the branch tip, and a base under `refs/git-pair/` is never a derived `--target`. Milestone 4 unblocked. |
| 2026-09-23 | session, `pi-heartthrob` | A fast-forward landing and a stale `refs/remotes/origin/main` produced an `integration record` failure that asked for a `--source` the command never uses. Milestones 5 and 6 added from the findings: four derivation layers, the not-downstream candidate rule, the recorder ordering, and `base:` naming a branch. |
