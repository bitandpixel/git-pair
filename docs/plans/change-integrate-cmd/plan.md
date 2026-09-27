# `change integrate`: the author's declaration that a changeset may be merged automatically

## Goal

An author who has an approval today still has three manual steps between that approval and a record:
run `check`, perform a merge with ordinary git, run `integration record`. In a repository where CI owns
the merge, the author has no way to say _"this is approved and I want it merged"_ that CI can read as a
machine answer — so CI either merges on the approval alone (overriding the author) or waits on a human
who has already said yes.

This plan adds the missing declaration:

```bash
git pair change integrate     # a lifecycle commit: approved, and ready for the automatic merge
git push origin feat/x        # the marker reaches the remote branch; git-pair does not push it
```

CI waits for `INTEGRATING`, runs one gate command, merges into the destination with ordinary git, then
runs `git pair integration record` and `git pair integration publish` — with everything it needs from
git-pair's own output, for a fast-forward, a merge commit or a squash.

git-pair still writes no merge and no ref while work is in flight. The new marker _requests_ a merge;
the merge stays git's and the record stays `integration record`'s.

## Success criteria

Observable outcomes:

- `git pair change integrate` writes exactly one empty commit, subject `git-pair: integrate <slug>`,
  trailers `Review-State: integrating`, `Review-Changeset: <slug>`, `Review-Head: <head it covers>`. It
  writes nothing else: no ref, no config, no network.
- It refuses with exit 1 — every failed condition named in one run — when the working tree is dirty, no
  approval stands, the approved commit is no longer in this history, content outside `changesets/<id>/`
  moved since the approval, the stacked parent moved/landed/ended in a way that invalidates the approval,
  **the parent branch has not landed**, the changeset was abandoned, or it already has an integration record.

> I'm wondering about this, ("the parent branch has not landed") - does it make sense to enforce this at
> integration-ready-time? should instead we allow the integration record be made, and then it isn't actually
> "integratable" until it's parent changeset(s) are integrated?

- `git pair status --json` reports `state: "INTEGRATING"` beside `integrating` and `integrate_commit` —
  the same two fields `check --json` answers with — and `next_action` names the push and the destination
  rather than the manual merge.
- `git pair check --json` reports `integrating` and `integrate_commit` beside `ready`. For CI the whole
  gate is `.ready and .integrating`, and it is still true after the marker is written: the approval
  underneath licenses the merge.
- The gate does not weaken. A rebase, an implementation commit, a moved parent, or a re-review after
  `change integrate` all make `check` refuse again, and `change integrate` refuses them up front.
- Re-running `change integrate` at the same head records nothing and exits 0; at a new head (a
  `changesets/<id>/`-only commit since) it records a fresh declaration naming that head.
- A stacked child can be declared only once its parent has a record. Its destination resolves to where
  the parent landed — `main` in the ordinary case, the release branch when the parent landed there — and
  `integration record` derives the same destination with no flags.
- A scripted replay shows a CI clone taking the marker, gating on `check --json`, landing with ordinary
  git, and finishing with `integration record` + `integration publish` and a published pair. The three
  landing shapes (ff, merge, squash) are covered by Go tests rather than by the replay: the replay proves
  the part only a second clone can prove — that the request travels as a commit and the recorder needs no
  flags — and one bash job staging three merges of one changeset would prove nothing the fixtures do not.
- `mise run check` and `mise run gates` pass; PRD, README and the skill name the command, and
  `TestEveryCommandIsNamedInTheDocs` is what enforces it.

## Context

What exists when this plan starts (`feat/change-integrate-cmd`, on top of `feat/integration-configure`):

- State is derived from markers, and markers are the only thing that moves it (PRD §12). Five states
  exist; `Review-State: working` (`change unready`) and `Review-State: abandoned` (`change abandon`)
  are precedent for a `Review-State` value that is _not_ a new verdict.
- `check` (`internal/cli/check.go`) already computes every condition this command needs, in one
  function (`integrationReasons`) that lists all failures rather than the first, plus
  `lineageReason` for the rewritten-history test and `parentSinceApproval` for the stack.
- `integration record` takes `--source`/`--commit`/`--target`/`--changeset`, derives all of them from
  the graph when they are not named, and handles the fast-forward (`ffChainTip`) and the carried
  stacked landing (`recordCarried`). What it cannot do is name the destination of a stacked child whose
  base was relinked: `derivationDestinations` excludes any base under `refs/git-pair/` on purpose
  (`internal/cli/integration.go:404`) and falls back to the default branch.
- `relinkStacks` (`internal/changeset/resolve.go:574`) re-points a child's _measurement_ base at the
  parent's integration ref at read time. The committed `CHANGESET.yaml` keeps the branch base, and
  `BaseAt` reads that committed value.
- `internal/hygiene/hygiene_test.go` greps git invocations for `push`, `merge`, `rebase`, `reset`.
  `change integrate` prints those verbs as advice and invokes none of them.
- `scripts/gates/e2e-29.sh` is the scripted replay of the landing loop; it is where the CI half of this
  contract gets proven, because a Go test in one process cannot show a second clone reading a marker.

## Constraints

- **PRD §26 stays intact.** No merge, no push, no ref write while work is in flight. The marker is a
  request; CI performs the merge.
- **PRD §22: the CLI is the agent surface.** Non-interactive, additive JSON, arrays never null, exit
  codes 0 success / 1 business refusal / 2 usage / 3 git failure.
- **`INTEGRATING` is a state a marker moves** (decided below), so every consumer that switches on
  `state` has to answer for it: `nextAction`, `inReview`, `queue`, `check`. That is the cost of the
  decision and M4 pays it in full rather than leaving states that read as `WORKING` fallbacks.
- The marker stays minimal: changeset id and the head it covers. No new trailer for the destination —
  the destination is derived, and a second source of truth about where work goes is how a record gets
  pointed at the wrong commit.
- Nothing in this plan makes `git pair merge` exist. PRD §26's sentence "there is no `git pair merge`,
  no `git pair land`, no `git pair push`" stays true, and the skill's landing contract says so in the
  same breath as the new command.

## Assumptions

- The automatic merge is triggered by a clone that has fetched the feature branch and the durable
  namespace (`integration configure`, or `--fetch`). A CI clone that cannot see the parent's record
  refuses, because it cannot prove the parent landed.
- A repository that wants feedback (rather than approval) to license the automatic path says so with
  `--allow-feedback` at the same place it says it for `check` and `integration record`.

---

## M1 — The marker and the state

**Deliverables**

- `Review-State: integrating` is a lifecycle marker git-pair recognises, classifies, and reports; an
  unrecognised-marker reading is what it used to be and is now a test case.
- `INTEGRATING` is a derived state: a changeset whose newest marker is the declaration reads
  `INTEGRATING` in `status`, and the declaration's commit is addressable from the summary.
- `git log` alone still reconstructs the whole lifecycle: the marker is an ordinary commit with
  trailers, and the subject is a stable string.

**Tasks**

- [x] `model`: `StateIntegrating = "INTEGRATING"`, `StateValueIntegrating = "integrating"`. The package
      doc's "five states" prose and the `State` doc comment (which currently says nothing about a
      changeset waiting to be merged) move with it.
- [x] `lifecycle`: `KindIntegrate`, `String()`, `parseEvent` recognising `integrating`, `Event.State()`
      → `INTEGRATING`, `markerLabel`/`markerReason` wording.
- [x] `lifecycle.Summary.Integrating *Event` — the newest declaration, beside `Abandoned`, so surfaces
      get the commit sha without switching on `Kind`. Newest wins, same rule as `Abandoned`.
- [x] `marker.IntegrateMessage(slug, head)`: subject `git-pair: integrate <slug>`, trailers
      `Review-State=integrating`, `Review-Changeset=<slug>`, `Review-Head=<head>`. `Review-Head` is the
      commit the declaration covers, written for the same reason `ReviewMessage` writes it: a rebase
      rewrites the marker and keeps the message, and the stale name is the evidence.
- [x] Unit tests in `internal/lifecycle`: parse, newest-wins, `Trailing`/`Stale` around the declaration
      (it is empty, so the marker underneath it stays the newest _verdict_), and a marker naming no head.

**Verification**

- `go test ./internal/lifecycle` passes; a fixture branch with approve-then-integrate derives
  `INTEGRATING` and names both commits.

## M2 — The gate and the command

**Deliverables**

- `git pair change integrate` exists, is non-interactive, prints `--json`, and refuses through the
  existing exit-code contract.
- `check` keeps licensing the merge under the declaration and still refuses everything it refused
  before, including a rewrite after the declaration.
- The parent rule: a child whose parent branch has not landed cannot be declared.

**Tasks**

- [x] `internal/cli/integrate.go`: `newChangeIntegrateCommand`, registered in `newChangeCommand`.
- [x] Run the gate as `check` runs it — `SummarizeAgainstTreeHEAD`, `terminalRecord`,
      `ResolveIntegration`, `lineageReason`, `parentSinceApproval`, `integrationReasons` — so one
      implementation answers both commands and the two cannot disagree about what drift is. Add the
      parent-not-landed condition to the list in `integrationReasons`, shared by both commands: it is a
      condition on the automatic merge, and `check` refusing it too is the conservative direction.
      _Done as `a.integrationGate`, shared by both commands. The parent-not-landed rule did **not** go into
      `integrationReasons`: it lives in `unlandedParentReason`, called only by `change integrate`. `check`
      answers "may this merge" for a person who can merge a child onto an unlanded parent and record that
      (`recordCarried`), and a gate that refused the merge would remove a landing shape the recorder exists
      to support. The two questions differ, so the condition belongs to the request. See the Decisions
      table._
- [x] `integrationVerdict(s)`: the event whose outcome licenses integration — the newest marker, or,
      when the newest marker is the declaration, the newest review underneath it. `integrationReasons`
      and `lineageReason` both ask it, so the outcome test and the lineage test move together and a
      rebase after the declaration cannot slip past the ancestry check.
- [x] Refusals: dirty tree, `base` is this branch, abandoned (`refuseIfAbandoned`), recorded
      (`marker.RefuseIntegrated`), plus the gate's reasons. Every reason printed in one run.
- [x] Idempotency: newest marker is a declaration naming `HEAD` → record nothing, exit 0,
      `recorded: false`. Naming a different head → a fresh declaration for that head.
- [x] `--allow-feedback`, `--json`; human output names the destination and the push.
- [x] Tests: one refusal per condition with its exit code; the JSON contract; the rewrite-after-declare
      case; drift after the declaration; `change ready`/`review submit`/`change abandon` after it.

**Verification**

- `go test ./internal/cli -run 'Integrat'` passes; `contract_test.go` gains the exit-1 rows.

## M3 — The destination of a child whose parent has landed

**Deliverables**

- `change integrate` prints the branch CI will merge into, and it is right for a stacked child.
- `integration record` derives the same destination with no flags, instead of falling back to the
  default branch.

**Tasks**

- [x] `internal/changeset`: `Destination(ctx, repo, cs, trunk)` — the base when it is a real branch;
      when the base is a durable ref, the parent's own base read at the commit that ref names
      (`BaseAt` reads `CHANGESET.yaml` from a tree — the parent's directory landed with it), walked
      upward and bounded; otherwise the default branch. Never returns a `refs/git-pair/*` ref.
      _Done as `DestinationFor` in `internal/changeset/destination.go` — the name `Destination` is taken by
      the return type — reading the parent's whole stack with `StackAt` rather than only its base, because
      whether the walk continues depends on whether the parent was itself stacked on something landed. It
      applies the resolver's relink rule at each hop, so a three-deep stack ends on the branch under all of
      it, and reports `Why` (`base` / `parent` / `default`), `Via` and `Unreachable` so a caller can say
      which rule produced the answer._
- [x] `integration.go`: use it for the derived destination beside `base` and `default`, keeping the
      existing guard and the "which destination was tried" wording in the refusal.
- [x] Tests: parent landed on trunk → `main`; parent landed on a release branch → that branch; a
      three-deep stack; a clone that has not fetched the namespace (base stays a branch, and M2's rule
      refuses rather than guessing).

**Verification**

- `go test ./internal/changeset ./internal/cli -run 'Destination|Record'` passes, and the flagless
  `record` of a stacked child verified against `main` no longer needs `--target`.

## M4 — Surfaces

**Deliverables**

- No surface treats `INTEGRATING` as `WORKING`: `status`, `queue`, `change unready`, and the next-action
  sentences all answer for it.
- A changeset waiting on CI is visible in `git pair queue`, which is the command that says what is
  waiting.

**Tasks**

- [x] `status --json`: `integrate_commit`, `integrate_head`; `nextAction` for `INTEGRATING` — the push
      and the destination, not the manual merge; the human report names the declaration beside `State:`.
      _Done as `integrating` + `integrate_commit`, mirroring `check --json` rather than inventing
      `integrate_head`: the same question gets the same two field names in both commands, and the head is
      already `head`._
- [x] `check --json`: `integrating`, `integrate_commit`. Document `.ready and .integrating` as the CI gate.
- [x] `queue`: `awaiting_integration` (never null) with the changeset, branch, destination and the age of
      the declaration; the review rows stay READY-only, because a reviewer owes nothing to a change whose
      reviewer has already spoken.
- [x] `change unready`: extends to withdrawing a declaration — `inReview` becomes a predicate that
      answers "is there an offer or a declaration to withdraw", the marker and message say the
      declaration is withdrawn, and the case is tested both ways (it must clear `integrating` in `check`).
- [x] Confirm nothing in `internal/tui` branches on the state set (it does not today — it prints the
      derived state), and say so in the plan rather than silently assuming it.
- [x] Tests: `status --json` for a declared changeset; queue's new array; unready clearing the gate.

**Verification**

- `go test ./internal/cli` passes; `pty-walkthrough.sh` still paints the changeset correctly.

## M5 — Docs and the CI replay

**Deliverables**

- PRD, README and the skill describe the command, the state, the gate and the CI contract, and the
  docs-contract test passes in both directions.
- A scripted replay proves ff / merge / squash landings from a second clone, ending in a published pair.

**Tasks**

- [x] PRD: §9.9 `git pair change integrate`; §11.1 and §11.3 for the new JSON; §12's state list gains
      `INTEGRATING`; §21's "When the parent lands" gains the parent rule and the destination rule; §29's
      landing contract gains the declaration as the step that hands the merge to CI; §26 re-read for the
      no-merge claim, which stays true.
- [x] README: command table, quickstart, the CI section spelled as
      `git pair check --json | jq -e '.ready and .integrating'`.
- [x] `skills/git-pair/references/cli.md` (must name every leaf), `SKILL.md`, and
      `references/integration.md` — whose "there is no `git pair merge` … and none is coming" stays, next
      to a sentence saying the new command requests a merge that somebody else performs.
- [x] `scripts/gates/e2e-29.sh`: the declaration, the push, and a clone that gates on `check --json` and
      lands all three shapes; extend the "what this replay does not cover" header instead of leaving the
      gap implicit.
      _Done, with the landing narrowed to one ordinary merge from the second clone and the three-shape
      matrix left to the Go tests — the reason is in the Success criteria and in the script's own comment.
      The replay also fetches the published pair back into the author's clone and asserts the request stops
      being listed once the record is visible there._

**Verification**

- `mise run check`, `mise run gates`; `go test ./internal/cli -run Docs` in both directions.

---

## M6 — The merge a declaration asks for, as an example

Added after the milestone list was closed, from the question the feature leaves open: `change integrate`
asks, so who answers? A repository has to have something to copy, and a claim about CI that no test runs is
a claim that rots.

**Deliverables**

- `scripts/ci/git-pair-integrate.sh`: the gate, the `--no-ff` merge, the push, the record, the publish — in
  shell, in the order §29 gives them, runnable by hand and by a job.
- `.github/workflows/git-pair-integrate.yml`: one thin workflow. The test workflow finishing, a poll of the
  queue, and a manual run all call the same script.
- `scripts/ci/gh-head-green.sh`: the probe that answers whether a commit is green on GitHub, as a command
  with a three-way exit code rather than as a step inside the job.
- `.github/workflows/ci.yml`: the repository's own CI — `name: CI`, `mise run gates` on every push and pull
  request — which is what `git-pair-integrate.yml`'s `workflow_run` names. Until it existed, the merge
  workflow pointed at a workflow that did not.
- `scripts/gates/ci-integrate.sh`: the replay, against scratch bare remotes, wired into `mise run gates`.
- README's "Landing a declared change from CI", and pointers from PRD §9.9 and §29.

**Tasks**

- [x] The script: `check --json` as the only gate, the destination read from `queue --json` rather than
      `base:`, the head cross-checked between the two reads so a branch that moved mid-run is not merged,
      `--no-ff` then push then record then publish, `merge --abort` on a conflict, no record for a refused
      push, `--dry-run`, and no call to `change integrate` (a pipeline that writes the declaration asks for
      its own merge).
- [x] The workflow: `contents: write`, `fetch-depth: 0`, one concurrency group per ref, build from the
      checkout, one call into the script. The logic is deliberately not in the YAML — the replay runs the
      script, and YAML a runner never reached is not covered by anything.
- [x] The replay: merge-and-publish, a re-run that does nothing twice, an approved-but-undeclared branch
      left alone, a drifted declaration refused, the queue-driven poll finding a declaration with no event
      behind it, a dry run that writes nothing, a conflicted merge aborted and reported red, and usage
      errors. 60 assertions.
- [x] Green before merged: `--require-ci` consults a probe (any command with a three-way exit code) about
      the declared commit; `--expect-head` makes the trigger name the commit its CI finished with. The replay
      drives all three answers with a stub probe and asserts the sha the probe was handed.
- [x] CI of its own, so the trigger has a target: `mise run gates` on `push` to every branch and on
      `pull_request`, through mise so the pinned Go and the task list come from `.mise.toml`.
- [x] Hermetic replays, which writing that CI exposed: `e2e-29.sh` passed on a laptop and failed on a
      machine with no global git config, because the clone that performs the merge had no committer
      identity. Each clone the replay creates now sets one, and both replays are re-run here with
      `GIT_CONFIG_GLOBAL=/dev/null` as the check that they need nothing from the host.
- [x] What the replay caught: a changeset based on trunk read as stacked on a branch called `main` whenever
      the clone knows trunk only as `refs/remotes/origin/main`. `changeset.DefaultBranchRef.IsBranch` now
      compares every spelling of the branch, and the refusal that bug produced would have broken the
      command in ordinary clones. Tests: `TestParentOfTreatsEverySpellingOfTrunkAsTrunk`, and
      `change integrate` run in a clone, in `integrate_test.go`.

**Verification**

- `bash scripts/gates/ci-integrate.sh` (60 assertions), `mise run check`, `mise run gates`.

---

## Decisions

| Date       | Decision                                                                                                      | Why                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| ---------- | ------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 2026-09-26 | Gate depth: `change integrate` runs the whole `check` gate, not just the verdict                              | A declaration CI will act on must be one CI can act on. Queueing a change that `check` refuses spends a merge attempt to report drift the author could have seen locally.                                                                                                                                                                                                                                                                                  |
| 2026-09-26 | Policy: approve, with `--allow-feedback`                                                                      | The same switch `check` and `integration record` already offer. Three commands in one path disagreeing about what counts as reviewed is the failure, not the strictness.                                                                                                                                                                                                                                                                                   |
| 2026-09-26 | `INTEGRATING` is a state, not a fact beside it                                                                | The author's distinction — "approved" versus "approved and handed over" — is the CI trigger, and a state is the field an agent already branches on. Accepted cost: every consumer that switches on `state` answers for it (M4).                                                                                                                                                                                                                            |
| 2026-09-26 | Marker stays minimal (id + head)                                                                              | The destination is derivable, and `Review-Target` would be a second claim about where work goes, unfalsifiable once written.                                                                                                                                                                                                                                                                                                                               |
| 2026-09-26 | Measurement base stays `refs/git-pair/integrations/<parent>`; the _destination_ resolves to the parent's base | Measured on a scratch repo: for merge and squash landings the two candidate bases give the same range and the same `base...head` span, so retargeting the base buys nothing; it loses the record's authority about where the parent actually went and collapses `landedBaseIsTheSameWork`. Naming the destination separately gets what the change was for.                                                                                                 |
| 2026-09-26 | A child cannot be declared while its parent branch is unlanded                                                | The automatic merge would land work on a branch a reviewer can still rewrite, and a create-only ref naming a commit on that branch can end up unreachable. The human path — merge into the parent with ordinary git, then `integration record` — stays open, which is what `recordCarried` already exists for.                                                                                                                                             |
| 2026-09-26 | That refusal lives in `change integrate`, not in `integrationReasons`                                         | The plan had it in the shared gate. Implementation showed the two commands ask different questions: `check` answers "may this merge" for a human who can merge onto an unlanded parent and record it, and refusing that in the gate deletes a supported landing shape. A declaration asks for a merge nobody will be asked again about, which is the narrower permission.                                                                                  |
| 2026-09-26 | `check`'s `integrating` reads the newest **marker**, not the newest declaration in the range                  | `Summary.Integrating` keeps the newest declaration even after a re-offer, a re-review or a retraction supersedes it, because "was one ever made, and where" is a question whose answer survives. CI's gate is not that question: a pipeline merging on a superseded request performs the merge the author just took back. `integrationGate.Declared()` is the reading, and `status` uses it too so one branch cannot answer "did the author ask" two ways. |
| 2026-09-26 | The replay lands one shape; ff / merge / squash stay in Go tests                                              | The replay's value is the second clone — a request that travels as a commit, and a recorder that needs no flags. Three merges of one changeset in bash prove a matrix the fixtures already prove, with more ways to be accidentally wrong.                                                                                                                                                                                                                 |
| 2026-09-27 | The CI example keeps its logic in `scripts/ci/`, with one thin workflow file                                  | A script is replayable by `scripts/gates/`, callable by hand, and reusable by a poll and a push trigger. The same steps in YAML are covered by nothing until a runner executes them, and the hygiene rule (§26) stays honest because the merge is visibly an example rather than a subcommand.                                                                                                                                                             |
| 2026-09-27 | The example records after the push, not before                                                                | A record is the durable claim that a landing happened. Written before the push, it survives a refused push, and "integrated at <sha>" then names a commit no destination branch holds. The recorder's own check (`--commit` added `changesets/<id>/` to `--target`) reads the remote-tracking ref the push just updated, so the order buys the verification as well as the honesty.                                                                        |
| 2026-09-27 | A gate that will not answer is a skip when the queue has no row for the branch                                | `check` on a branch whose changeset has landed answers "no changeset for this branch", which is true of the branch and not a fault in the work. Exiting red for it would make every post-landing event red. The queue answers whether anybody asked for the merge, which is the distinction, and it is a read rather than a message to parse.                                                                                                              |
| 2026-09-27 | The CI-green check is a probe command with a three-way exit code, not a step in the job                       | The job has to be replayable without a forge, and "green" is the forge's answer rather than git-pair's. `0` green / `1` not green / `2` cannot tell keeps "nobody tested this" out of the pass column, which is the mistake that turns a CI gate into a rubber stamp.                                                                                                                                                                                      |
| 2026-09-27 | The merge job triggers on `workflow_run`, not on `push`                                                       | A job running on the feature branch puts its own check run on the commit it must certify green, so it waits for itself. `workflow_run` also arrives with the sha CI tested, which is what `--expect-head` needs; the poll stays for declarations that went green before the job existed.                                                                                                                                                                   |
| 2026-09-27 | The probe reads every check, not branch protection's required list                                            | One rule that is written down beats two that can disagree, and the stricter direction is the one that cannot merge untested work. A repository with a narrower required set passes its own probe.                                                                                                                                                                                                                                                          |
| 2026-09-27 | CI runs on `push` to every branch, not only on `pull_request`                                                 | The merge job reads the checks on a _branch head_, and a pull-request run reports its checks against the PR's merge ref, so a PR-only repository would leave every head looking untested — correctly, and silently. The duplicate minutes on branches that also carry a pull request are the price, and dropping `pull_request` is the remedy where a repository takes no forks.                                                                           |
| 2026-09-27 | CI is one line, `mise run gates`, not the commands inside it                                                  | The task is the repository's definition of done; a copy of its contents in YAML is a second definition that can drift, and the drift would show up as CI going green on a change the local gate refuses. mise costs one third-party action, and the alternative is spelled out in the file's comment.                                                                                                                                                      |
| 2026-09-27 | A replay sets an identity in every clone it creates                                                           | A clone has no committer of its own, and whether the host has a global git config is not part of any fixture. The bug lived for as long as it did because the machine running the gate happened to supply one.                                                                                                                                                                                                                                             |

## Risks

- **The verdict-under-marker rule is the sharpest edge.** If `check` reads the declaration as the newest
  verdict it refuses everything; if it reads past it carelessly it licenses a merge after a rewrite.
  Covered by explicit tests in both directions, and by the `lineageReason` change being in the same
  function as the outcome change.
- **`INTEGRATING` leaking into places that switch on state** would show as a change waiting for review,
  or as one that needs implementing. M4 enumerates the switches (`nextAction`, `inReview`, `queue`'s
  READY filter) rather than trusting a grep.
- **A declaration whose parent later moves** is handled by the gate the merge already runs, not by the
  marker: CI re-runs `check` and refuses. This is stated in the docs so nobody builds a second gate.
- **Marker churn** if an author keeps working after declaring: each `change integrate` at a new head is
  an empty commit. Accepted, and it is what the idempotent same-head case exists to limit.

## Verification strategy

Unit tests for the derivation (M1), CLI tests through the harness for every refusal and the JSON
(M2–M4), the docs-contract test for prose (M5), and one scripted replay against a real second remote
(M5) because the claim "CI has everything it needs" is only checkable from a clone that was handed the
branch and nothing else. `mise run gates` runs the replay; `mise run check` runs everything else.

## Audit history

| Date       | Milestone           | Outcome                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| ---------- | ------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 2026-09-26 | M1–M5               | Implemented and committed as six commits: the marker and state, the destination resolver, `record`'s destination wiring, the command and the shared gate, the surfaces, the docs, then the replay. Three deliberate departures from the plan are recorded above: the unlanded-parent refusal lives in the command rather than the gate, `status --json` mirrors `check`'s field names, and the scripted replay lands one shape with the three-shape matrix left to Go tests. Verification surface: `go test ./...`, `mise run check`, `scripts/gates/e2e-29.sh` (118 assertions), and the docs-contract test.                                                                                                                                                                                                                                                 |
| 2026-09-27 | M6                  | Implemented: the CI script, one thin workflow, the scripted replay in `mise run gates`, README's CI section, and pointers from PRD §9.9 and §29. The replay earned its place on its first run — a changeset based on trunk was read as stacked on a branch called `main` in any clone that knows trunk only as a fetch ref, and `change integrate` refused to declare in exactly the clones a pipeline builds. Fixed with `DefaultBranchRef.IsBranch`, which compares every spelling of the integration branch, with a Go test at the comparison (`internal/changeset`) and one at the command in a real clone (`internal/cli`). Verification: `scripts/gates/ci-integrate.sh` (43 assertions), `mise run check`.                                                                                                                                             |
| 2026-09-27 | M6 (green gate)     | Added on request: the merge now waits for the head to be proven green. `--require-ci` asks a probe command (three-way exit code, `scripts/ci/gh-head-green.sh` for GitHub) about the declared commit; `--expect-head` ties the merge to the commit the trigger's CI actually finished with; the workflow's `push` trigger became `workflow_run`, because a merge job running on the branch would certify its own in-progress check run forever. The replay stubs the probe and covers green, not green, cannot tell, `--require`, the expect-head mismatch, and the sha handed to the probe: 56 assertions.                                                                                                                                                                                                                                                   |
| 2026-09-27 | M6 (CI of its own)  | Added `.github/workflows/ci.yml` (`name: CI`): `mise run gates` on every push and on pull requests, so the merge workflow's `workflow_run` names a workflow that exists and the `push` trigger on every branch keeps checks landing on the head the probe asks about. It paid for itself before it ran: the e2e replay depends on the host's global git config for the committer of its merge commit, so it was green here and would have been red on a runner. Fixed in the replay (identity per clone it creates) and verified by re-running both replays with `GIT_CONFIG_GLOBAL=/dev/null` — `E2E: all checks passed`, `PTY: all checks passed`.                                                                                                                                                                                                          |
| 2026-09-27 | merge of `main`     | `main` merged into the branch (`5a3a7a3`) to pick up `feat-editor-term-gate`. The repository's first CI run (`36294210281`) failed on `TestEditorPrecedenceMatchesGit/VISUAL_beats_EDITOR`, which is not this changeset's code: `git var GIT_EDITOR` reads `VISUAL` only when `TERM` names a usable terminal, and that fixture inherited `TERM` from the developer's shell. Nothing here changed for it — the merge is the only way this branch's `mise run check` can be green on a runner. After the merge, `mise run gates` is green end to end (`E2E`, `PTY`, `CI-INTEGRATE: all checks passed` (60 checks)), and `env -u TERM go test ./internal/console -run TestEditorPrecedence` passes, which is the condition that run failed in. `change ready` re-offered, because the head moved.                                                                |
| 2026-09-27 | CI (second failure) | The next runner failure was the pty replay's single colour assertion (`36342901359`), in a step that has been on `main` since `ce65d79` — the replay had simply never run on a runner. Cause: termenv's `isTTY()` returns false for any non-empty `CI` before it looks at the descriptor, so lipgloss renders at the `Ascii` profile wherever `CI` is exported, and Actions exports it for every step. Measured against the gate: `CI=true` alone fails that assertion, `TERM=dumb` alone does not, `FORCE_COLOR=1` cannot rescue it. Fixed in the harness (`pty-tui.py` removes `CI`/`NO_COLOR`/`CLICOLOR`/`CLICOLOR_FORCE`/`FORCE_COLOR` for the child, beside the `TERM` it already pinned) rather than by relaxing the check, which now also names which of its three conditions failed. The product's colour loss under `CI` is left open in `ABOUT.md`. |
