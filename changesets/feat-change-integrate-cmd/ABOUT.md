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
- `TestEveryCommandIsNamedInTheDocs` passes, which is what makes PRD, README and `cli.md` name the
  command rather than merely mention it.

## Known limitations

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

- Should `queue`'s `awaiting_integration` row carry the parent chain (`destination_via`) too, or is
  `destination` plus `status`'s `Stack:` enough for the actor who merges? Left as-is: one field, one
  question.
- Should a repository be able to state in config that `feedback` licenses the automatic path, so CI need
  not pass `--allow-feedback`? §11.3's reasoning for refusing that key applies here unchanged, so the
  answer stays "no" until a repository needs it twice.
