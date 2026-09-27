# `change integrate`: the author's declaration that a change may be merged

Executing `docs/plans/change-integrate-cmd/plan.md`. An approved changeset still has a person standing
between the approval and the merge button: the gate, the merge, and the record are three steps one
author runs in sequence, and CI can only take the merge over by ignoring the author. This changeset adds
the sentence the author was missing — one commit that says *this is approved and I want it merged* — and
keeps every part of the merge on the side of git and of the person who owns the destination branch.

**All five milestones are done.** The marker and the state (M1), the command and the gate it shares with
`check` (M2), the destination of a child whose parent has landed (M3), the surfaces that read a state
(M4), and the docs plus the scripted replay (M5).

The one line that says what this is for:

```bash
git pair check --json | jq -e '.ready and .integrating'
```

`ready` is "may this merge", `integrating` is "did the author ask for one", and the conjunction is CI's
gate. git-pair still writes no merge, pushes nothing, and writes no ref while work is in flight (§26).

## What changed (M1 — the marker and the state)

- `model`: `StateIntegrating` / `StateValueIntegrating`. `INTEGRATING` is the sixth state, and it is a
  state because it is a marker: the safety property in §12 admits no other kind, and "approved" versus
  "approved and handed over" is the distinction an agent already branches on.
- `lifecycle`: `KindIntegrate` — `String()`, parsing `Review-State: integrating`, `Event.State()`,
  `markerLabel`/`markerReason` — plus `Summary.Integrating *Event`, the newest declaration in the range,
  beside `Abandoned`. That field answers "was one ever made, and where" and survives being superseded;
  the gate's question is different, and M2 reads it differently.
- `marker.IntegrateMessage(slug, head)`: subject `git-pair: integrate <slug>`, trailers
  `Review-State: integrating`, `Review-Changeset`, `Review-Head`. `Review-Head` is the head the
  declaration covers, written for the reason a review writes it: a rebase rewrites the marker and keeps
  the message, and the stale name is what makes the gate refuse the rewrite instead of blessing it.
- `gittest`: `IntegrateMessage`, `CommitIntegrateMarker`, `CommitIntegrateMarkerOn`.

## What changed (M2 — the command and the gate)

- `internal/cli/integrate.go`: `git pair change integrate`, `--allow-feedback`, `--json`. One empty
  commit and nothing else — no ref, no config, no network. Human output names the head, the declaration,
  the branch it is asking to land on, and the push that makes the request visible; `next_action` spells
  push as a command the author runs and never as one git-pair ran.
- `internal/cli/check.go`: `runCheck` now computes an `integrationGate` — the summary, the verdict, the
  abandon marker, the record and where it landed, the reviewed head, the parent reading, and every
  reason — and `change integrate` asks for the same struct. Two implementations of "is the reviewed
  content still here" would be two answers waiting to disagree about what drift is.
- `integrationVerdict` is the event whose outcome licenses the merge: the newest marker, or the newest
  review under it when the newest marker is a declaration. `integrationReasons`, `lineageReason` and
  `parentSinceApproval` all read the verdict, so a rebase after a declaration cannot slip past the
  ancestry check while the outcome check passes.
- `integrationGate.Declared()` is what `integrating` reports: the declaration **while it is the newest
  marker**. A re-offer, a re-review or a retraction supersedes a declaration without erasing it, and a
  pipeline that merged on a superseded request would perform the merge the author has just taken back.
- `unlandedParentReason` is the command's own rule, not the gate's: a child cannot be declared while its
  parent branch has no integration record, and a parent that records no `parent-changeset:` is refused
  because there is no record to look for. `check` keeps answering "may this merge" for a person who can
  merge onto an unlanded parent and record that.
- Idempotency is `alreadyDeclared`: HEAD *is* the declaration commit. Not "the state is INTEGRATING",
  which survives a commit — the claim does not.

## What changed (M3 — where a child lands)

- `internal/changeset/destination.go`: `DestinationFor` returns `Destination{Ref, Why, Via, Unreachable}`.
  A base that is a branch is the answer; a base under `refs/git-pair/` is a measurement, not a
  destination, so the walk reads the parent's own `CHANGESET.yaml` out of the commit its integration ref
  names — the branch the parent was measured against — and applies the resolver's relink rule at each
  hop so a three-deep stack ends on the branch under all of it. Bounded and cycle-guarded, because that
  yaml is committed content. A walk that cannot resolve falls back to the default branch and says so in
  `Why` and `Unreachable` rather than reporting a guess as a fact.
- `reviewref.IntegrationID(ref)`: the family-aware reading, which is what keeps an archive ref from being
  read as a landing because both end in the same slug.
- `integration record` asks it in both places that derive a destination — `derivationDestinations` and
  `verifyLandingReachable` — so a flagless record of a stacked child verifies against the branch the
  parent landed on and prints "(the branch its landed parent was based on)" instead of falling back to
  the default branch and calling that the changeset's own answer. The recorded base and the default
  branch stay behind it as candidates, so a landing nobody chose is still refused.
- The recorder also skips `Review-State: integrating` when picking the newest verdict in a source's
  history. A declaration is the author adding "and merge this" to a verdict, not a second opinion about
  it; reading it as one would mean the head a declaration names can never be recorded.

## What changed (M4 — the surfaces)

- `status --json`: `integrating` and `integrate_commit`, read by `Declared()`'s rule so one branch cannot
  answer "did the author ask" two ways. A `Declared:` block beside `Terminal:`, and a `nextAction` for
  `INTEGRATING` that names the push, keeps the landing contract attached, and switches to "the head moved
  since the declaration" when a commit has landed on the declared one.
- `queue`: `AWAITING INTEGRATION` / `awaiting_integration` — never null — with the branch it is asking to
  land on and the age of the declaration, oldest request first. A second list and not more review rows,
  because the two answer different people, and a branch carrying a declaration is not `READY` anyway.
  Both lists come from one `Summarize` per branch (`branchQueueEntries`), so the extra list costs no
  extra scan.
- `change unready` withdraws a declaration (`inReview` gains `StateIntegrating`), with the answer saying
  plainly that the merge request is gone. Stopping a pipeline takes one more marker, not a rebase.
- `internal/tui` has no state switch: it prints the derived state, so `INTEGRATING` reaches it without a
  change. Checked rather than assumed.

## What changed (M5 — docs and the replay)

- PRD: new §9.9; §12's state list and the reason `INTEGRATING` is a state; §11.1/§11.3 for the fields;
  §10.6 for the second queue list; §21 gains "Where a child lands is not what it is measured against"
  beside the relink rule it already documented; §22 gains the step an agent may run after the gate; §26
  re-read and extended rather than amended — the declaration is inside the no-merge rule, and the section
  now says a future command that *performs* a merge would need a second carve-out; §29's landing contract
  gains the request as a step.
- README: the command table, the quickstart, the state list, the JSON contracts, and the CI section
  spelled as the conjunction. `references/cli.md` names the leaf command (the docs-contract test holds it
  to the code), `SKILL.md` says which steps are the agent's, and `references/integration.md` keeps "there
  is no `git pair merge` … and none is coming" true in the same breath as the new step.
- `scripts/gates/e2e-29.sh`: the declaration from the branch to a second clone — the marker's shape, no
  durable ref, both gate answers, the queue's second heading, idempotence, drift taking `ready` away while
  `integrating` stands, the withdrawal, then a clone that never met the author gating on the same two
  fields, merging with ordinary git, recording with **no flags**, publishing, and the author's clone
  fetching the record back and stopping the listing.

## What changed (M6 — the merge somebody has to perform)

Added after M5, from the question the feature leaves open: the command asks, so who answers? Nothing here is
a git-pair subcommand, and §26 stays true — this is the example a repository can copy, plus the replay that
keeps it honest.

- `scripts/ci/git-pair-integrate.sh`: gate → `git merge --no-ff` → push the destination → `integration
  record` → `integration publish`, with the destination read from `queue --json` rather than `base:`, the
  head cross-checked between the two reads so a branch that moved mid-run is not merged, `merge --abort` on
  a conflict, no record for a refused push, `--dry-run`, and `--require` for a hand-run where a refusal
  should be red. It never calls `change integrate`: a pipeline that writes the declaration would be asking
  for its own merge.
- `.github/workflows/git-pair-integrate.yml`: one thin workflow — the repository's test workflow finishing
  (`workflow_run`), a 15-minute poll of the queue, and `workflow_dispatch`, all calling that script.
  `contents: write`, `fetch-depth: 0`, one concurrency group per ref, built from the checkout. No `push`
  trigger: a merge job running on the feature branch would have to certify its own in-progress check run.
- The green gate, on request: `--require-ci` consults a probe before merging anything, and merges on a yes
  only. A probe is any command handed the sha — exit `0` green, `1` not green, `2` cannot tell — and
  `scripts/ci/gh-head-green.sh` answers it from GitHub's check runs and commit statuses for that one commit.
  `2` is not `0`, so "nothing has run for this" is not a pass. `--expect-head <sha>` ties the merge to the
  commit the trigger's CI finished with, so a branch that moved mid-job is skipped rather than merged on an
  older run's reputation.
- `scripts/gates/ci-integrate.sh`: the job replayed against scratch bare remotes, in `mise run gates`.
  Merges what is declared and ready; leaves an approved-but-undeclared branch and a drifted declaration
  alone; a re-run does nothing twice; the poll finds a declaration with no event behind it; a dry run writes
  nothing; a conflicted merge is aborted, unrecorded and red.
- `.github/workflows/ci.yml`: the repository's own CI — `name: CI`, `mise run gates` on every push and on
  pull requests, through mise so the pinned Go and the task list stay in `.mise.toml`. It is here for two
  reasons. It is the workflow `git-pair-integrate.yml`'s `workflow_run` names, which until now pointed at a
  placeholder. And it triggers on `push` to every branch, because the merge job reads the checks on a
  *branch head* while a pull-request run reports them against the PR's merge ref — a PR-only CI would leave
  every declared head looking untested, correctly and silently.
- One host dependency, caught before the new CI ran once: `e2e-29.sh` commits a merge in a clone it creates,
  and a clone has no committer of its own — every pass it had returned here came from the machine's global
  git config. Each clone the replay creates now sets an identity, and both replays were re-run with
  `GIT_CONFIG_GLOBAL=/dev/null`.
- README's "Landing a declared change from CI", including the two derivations that are easy to get wrong:
  `--commit` is the merge commit rather than the destination's tip before the merge, and the destination
  comes from the queue rather than from `base:`.
- One fix the replay caught on its first run. A clone that knows trunk only as `refs/remotes/origin/main`
  compared that ref against `base: main`, concluded the changeset was stacked on a branch called `main`, and
  — because an unlanded parent is a refusal — `change integrate` refused to declare in the ordinary clone.
  `changeset.DefaultBranchRef.IsBranch` now answers for every spelling, used by `ParentOf`, `parentOfBranch`
  and `init`'s two trunk checks. `status` also stops printing a bogus `Stack: parent: main` there.

## Design decisions

- **A state, not a fact beside it.** The author's distinction is the CI trigger and `status --json` is
  what an agent branches on. Accepted cost: every consumer that switches on state answers for it — which
  is M4, done in full rather than left to a `WORKING` fallback.
- **The gate is shared as code, not imitated.** `integrationGate` is what `runCheck` computes, so the
  command that declares cannot drift from the command that licenses.
- **The unlanded-parent rule belongs to the request.** `check` answers "may this merge"; a declaration
  asks for a merge nobody will be asked again about, onto a branch review can still rewrite, where a
  create-only ref can end up naming history that stopped existing. The human path stays open.
- **The destination is derived, never recorded.** No `Review-Target` trailer: a second claim about where
  work goes is unfalsifiable once written, and the record already refuses a landing that cannot be
  reached from the destination it derived.
- **The measurement base stays `refs/git-pair/integrations/<parent>`.** Measured on scratch repos: for
  merge and squash landings the two candidate bases give the same range and the same `base...head` span,
  so retargeting the base buys nothing; naming the destination separately gets what the change was for.
- **The replay lands one shape.** ff / merge / squash stay in Go tests, where a landing can be staged
  without pretending one CI job performed three merges of one changeset.
- **The CI example keeps its logic in `scripts/ci/`.** One thin workflow file, one script. The script is
  replayable by `scripts/gates/`, runnable by hand, and shared by the push trigger and the poll; the same
  steps written in YAML are covered by nothing until a runner reaches them. It also keeps §26 honest at a
  glance: the merge is visibly a repository's own script, not a subcommand with a merge in it.
- **The example records after the push.** A record written before a refused push is a durable claim about a
  landing no destination branch holds. The order also buys the verification, because `integration record`
  checks `--commit` against `--target`, and the push is what makes `origin/<destination>` hold it.
- **A gate that will not answer is a skip, decided by the queue.** `check` on a branch whose changeset
  already landed says "no changeset for this branch", which is about the branch and not the work. The queue
  answers the real question — did anybody ask for this merge — without the script parsing message text.
- **"Green" is a probe command, not a step in the job.** The job must be replayable with no forge present,
  and whether tests pass is the forge's answer rather than git-pair's. Three exit codes rather than two
  exist because "nothing has run for this commit" must not read as a pass — that one mistake is what turns a
  CI gate into a rubber stamp — and the strict rule (every check GitHub can see, not the protection list)
  was chosen over two rules that can disagree.

## Validation

- `go test ./...` and `mise run check` (gofmt, vet, sharded suite) pass.
- New tests: `internal/lifecycle/integrate_test.go` and `TestSummarizeDeclarationOverRealCommits` (M1);
  `internal/cli/integrate_test.go` — one refusal per condition with its exit code, the JSON contract, the
  rewrite after a declaration, drift after a declaration, idempotence, a fresh head, the parent-unlanded
  refusal and the path that clears it, `check`'s two answers moving apart, and the usage codes (M2);
  `internal/changeset/destination_test.go` — six shapes of the walk, including the gone parent base, a
  durable ref that is not a record, and a hand-edited parent loop (M3);
  `internal/cli/integration_destination_test.go` — the flagless record of a stacked child, human and
  machine (M3); `internal/cli/integrating_surfaces_test.go` — status, unready, queue (M4);
  `json_nulls_test.go` now asserts `awaiting_integration` is `[]` and not null.
- `scripts/gates/e2e-29.sh`: 118 assertions, `E2E: all checks passed`, including the second clone.
- Both scripted replays re-run with `GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null`, which is the
  condition a runner starts in: `E2E: all checks passed`, `PTY: all checks passed`. The e2e replay was not
  green in that condition until the identity fix above — it had been borrowing this machine's `~/.gitconfig`.
- `scripts/gates/ci-integrate.sh`: 60 assertions, `CI-INTEGRATE: all checks passed` — the job run as a job
  runs it, in a clone of a bare remote, with the merge, the refs, the skips and the aborted conflict
  checked on the remote rather than on what the script printed; and the probe stubbed so all three of its
  answers, `--require`, the `--expect-head` mismatch, and the sha the probe is handed are all asserted (M6).
- `TestParentOfTreatsEverySpellingOfTrunkAsTrunk`, and `change integrate` in a real clone where trunk is
  only a fetch ref: the pair that covers the bug the CI replay found (M6).
- `scripts/gates/pty-walkthrough.sh` under the runner's surroundings — `CI=true NO_COLOR=1
  GITHUB_ACTIONS=true` exported for the whole replay — and under a clean environment: `PTY: all checks
  passed` both ways. Before the driver removed those switches, the same replay with `CI=true` failed the
  pane's one colour assertion, which is what makes the passing pair evidence rather than a weakened check.
- `main` merged in (`5a3a7a3`) for the terminal fix in `feat-editor-term-gate`. The first CI run of this
  repository failed on `TestEditorPrecedenceMatchesGit/VISUAL_beats_EDITOR` — neither this changeset's
  code nor a git-pair defect: `git var GIT_EDITOR` reads `VISUAL` only when `TERM` names a usable
  terminal, and that fixture inherited `TERM` from the developer's shell. After the merge `mise run gates`
  is green end to end (`E2E`, `PTY`, `CI-INTEGRATE`), and `env -u TERM go test ./internal/console
  -run TestEditorPrecedence` passes, which is the condition that run failed in.
- `TestEveryCommandIsNamedInTheDocs` passes, which is what makes PRD, README and `cli.md` name the
  command rather than merely mention it.

## Responses (reviews `906332c`, `17df992`)

Both questions from `906332c` — refuse a child whose parent has not landed, or write the record early and let
the child become integratable later; and whether the merge job should wake when `main` finishes CI or whether
the scheduled poll is enough — are answered in
`changesets/feat-change-integrate-cmd/child-declarations-and-the-main-side-trigger.md`, with each question
quoted as it was written. `17df992` asked for the inline copies to leave the plan and the workflow file, so
both are gone from those files: the rule itself stays in the plan's Decisions table, the standing question
about a main-side trigger is in Open questions below, and the discussion lives in the thread.

The reviewer's markdown pass over `plan.md` (emphasis markers, table alignment) is retained as submitted; new
table rows keep this file's original compact spacing, so the tables are mixed width. Cosmetic, and one
formatter pass would settle it either way.

## Known limitations

- The job proves the *declared head* green, not the merge result green; `main` runs its own CI after the
  push, and proving the result before it lands is a merge queue, which this example deliberately is not
  (README, "Green before the merge"). This is the same fact that makes a deferred child declaration worth
  refusing: a green from before the parent landed is a green about different work.
- The destination is the branch a parent's record *says* it was based on. A parent landed somewhere other
  than its own base makes that answer wrong, and it is wrong in the direction `integration record`
  catches: the record refuses a landing not reachable from the destination it derived, and `--target` is
  the named way to say otherwise.
- The unlanded-parent refusal is the command's, so `check` still says yes to a child stacked on an open
  parent. That is deliberate (see Design decisions), and the distinction is documented in §21 and in
  `unlandedParentReason` rather than left to the reader.
- Declaring, committing, declaring again leaves an empty commit per declaration. The idempotent same-head
  case exists to limit the churn; nothing removes it.
- `change integrate` takes no `--changeset`, for the reason `check` takes none: the declaration is made
  about the revision you are standing on.
- `change wait` has no `INTEGRATING` case in `waitNextAction`. A declaration requires an approval, and an
  approval already ends the wait, so the state is unreachable there today.
- The TUI shows the declaration as the state line and nothing more. A dedicated view of "what has been
  handed over" is a `queue` question, and queue has it.

## Open questions

- Should the merge workflow also wake when `main` finishes CI, so a child that became eligible without a
  commit of its own merges sooner than the poll's next interval? Unreachable while a child cannot be declared
  before its parent lands, and the poll covers it at fifteen minutes' latency; the reasoning and the shape of
  the trigger are in the thread `child-declarations-and-the-main-side-trigger.md`.
- Should the review TUI honour the terminal it has over an exported `CI`? `review open` refuses to start
  without one, so by the time the TUI is drawing, the terminal question is settled — yet termenv returns
  `false` from `isTTY()` for any non-empty `CI`, and lipgloss renders the whole review without colour. The
  gate is pinned around it (see Validation); the product question is open, and answering it means choosing
  between honouring the flag and honouring the descriptor.
- Should `queue`'s `awaiting_integration` row carry the parent chain (`destination_via`) too, or is
  `destination` plus `status`'s `Stack:` enough for the actor who merges? Left as-is: one field, one
  question.
- Should a repository be able to state in config that `feedback` licenses the automatic path, so CI need
  not pass `--allow-feedback`? §11.3's reasoning for refusing that key applies here unchanged, so the
  answer stays "no" until a repository needs it twice.
