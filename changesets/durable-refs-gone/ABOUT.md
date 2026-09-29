# durable-refs-gone

## Summary

Milestones M5 and M6 of `docs/plans/completed/simplify-architecture/plan.md`, in one changeset because they are
one change told twice: M5 deletes the durable-ref subsystem, and M6 is the sweep that stops the documents
promising it. git-pair writes no ref at any point in a lifecycle, and `refs/git-pair/` becomes a namespace the
code neither reads nor writes — and now no document names it either, which `TestNoDocNamesADurableRef` holds
with no whitelist. M6's own verification item, following the skill top to bottom against a scratch repository,
is the run described under Validation rather than a diff. Its base is trunk, so the review diff is the changeset itself: 80 files, +2130/−9085. The deletion proper is
`5abeb3a` — 46 files, 6521 of those deletions — and what follows it is the rest of the system being told.

## What is gone

`internal/reviewref`, `internal/cli/{integration,publish,configure,published,fetch}.go`,
`internal/git/push.go`, and their tests — the recorder that verified a claim, the publisher, the consent
command for refspecs, the mirror namespace, the published/unpublished views, and the audited push call site.
Commands: `integration`, `integration record`, `integration publish`, `integration configure`. Flags:
`--fetch` on `status`, `queue` and `check`; `git pair change wait --fetch` stays, because it fetches commits,
not a namespace.

`internal/cli/landing.go` is the read that replaces them: one batch of destination reads — does the
destination carry `changesets/<id>/` or `changesets/.landed/<id>/`, which branch, is it the integration
branch, what chain does that history carry.

## What a reader sees instead

- `LANDED, UNRECORDED` is now `LANDED UNREVIEWED`, and it is a different finding: work reached the
  destination with no approval the destination can show. The licence is two conditions, both read from the
  destination — its chain carries a permitting verdict, and that verdict names a commit the destination holds
  as well — so a squash that brought no markers, an approval whose commits the landing left behind, and an
  approval that names no commit are all reported, while a merge commit that carried the run is not. Nothing
  closes it with a command, so there is no closing command to print; the fix printed is
  `git pair status --changeset <id>`.
  The findings ride `status`'s exit-2 error on stderr, and `queue --json` carries `landed_unreviewed`.
- `next_action` for a ready head is one sentence — `` `git pair check`, then merge into main with ordinary
  git `` — because there is no step after the merge. `contract_test.go` pins that string, and
  `TestNoActionNamesADeletedCommand` pins the general rule.
- `status`'s `Stack:` block reads each ancestor's landing from the destination and prints `landed:` rather
  than `record:`; `stack[].integration` in `--json` is `stack[].landed_commit`. `parent.landed_in_default_branch`
  is deleted: it was true whenever `parent.landed` was, and the index it came from is gone.
- `change integrate` refuses a stacked child whose parent is not landed, and the reason names what to do
  (`git pair check`, then merge with ordinary git) instead of a record to write.
we had discussed the mechanism being a "queued" integration, where the integration intent is recorded and when the
parent lands, if the git pair check still passes, the child would be landed automatically. Can we support this?
- A changeset whose branch is gone is read from the destination's chain, and that read names its base as
  derived (`base_why`: the run the chain carries) rather than printing a bare object id. It reports no
  parent when `base:` is the integration branch: the commit the chain sits on is a measurement point, not a
  branch that went away, and the reader of a changeset that was never stacked should not be told to go and
  choose one.

## The rule that keeps it gone

`internal/hygiene/hygiene_test.go` loses the audited-push exception and the single-`update-ref` rule and
gains `TestShippedCodeNeverWritesARef`: no shipped file passes `update-ref`, `symbolic-ref` or `git tag` to
git. `symbolic-ref` has exactly one exception — `CurrentBranch` reading the checked-out branch in
`internal/git/git.go` — allowed by file and checked by flag, so the read cannot become a write. The fixture
self-test gained a second fixture scanned with that rule set, which is what proves the rule catches an
injected write and ignores `for-each-ref`.

## Gates, which are contracts

`scripts/gates/e2e-29.sh` loses its record and publish steps and gains what the tree model promises: a
merge into `release/2.x` leaves the source branch live, and naming that branch as the destination
(`--default-branch release/2.x`) makes the same history report the changeset landed, reviewed, with the
chain the merge carried — after the branch is deleted as well as before. A clone given only branches reads
the landing without being told to fetch. `scripts/gates/ci-integrate.sh` now asserts the shared remote holds
branches and nothing else, and that the directory entered the destination exactly once.
`scripts/gates/pty-walkthrough.sh` moves its "a command that writes asks for nothing at a terminal" subject
from `integration configure` to `change tidy`. 113 `ok:` steps in e2e-29 where there were 72.

## Known limitations, stated where the refs used to promise otherwise

- A squash or cherry-pick landing that carries no markers keeps nothing: `chain_base`, `chain_head` empty
  and `reviewed: false`. PRD §13.3 says so, and `change tidy` is the mitigation for the clutter it leaves. A
  rebase merge is the second shape, and it fails the other way: the marker commits come across and the
  commits they approved do not, so the record survives — `state` stays `APPROVED` — while `reviewed` is false,
  because an approval of commits the destination does not hold licenses nothing. A `--no-ff` merge keeps the
  chain and the approval together, which is how this repository lands and what the gates replay.
- A landing on a branch that is not the changeset's destination is not landed. That is D1, not a bug: the
  answers come from the destination, and `--default-branch` names the destination for a read.
- An abandoned changeset's history lives on its branch. Deleting the branch deletes the finding, which is
  what `terminalRecord` now reads from the derived chain.

## Two things this changeset deliberately does not do

`changesets/*/ABOUT.md` on trunk still describes the design this deletes, including
`changesets/feat-publish-the-records/`. Those directories are the tree's record of landings that happened;
the tool's answer to their clutter is `git pair change tidy`, in its own changeset, not a deletion here.
Deleting a landed changeset's directory would delete the statement the destination carries, which is the
thing this plan exists to protect.

`docs/plans/simplify-architecture/handoff.md` is deleted rather than moved. It was written by the session that
executed M1-M4 as a handoff: verified state, then the next concrete action, in order. The actions it lists are
M5 and M6, which is what this changeset does, so filing it under `completed/` would keep a stale instruction
list beside a finished plan. Nothing reads it — no code, no test, no document, no skill — and the two other
completed plans in this repository keep `audits/<date>-completion.md` rather than a handoff note. What the file
recorded survives in the plan's as-built section and in each changeset's `ABOUT.md`.

The exception, so the diff is not a surprise: this changeset moves the plan into `docs/plans/completed/`, and
three `ABOUT.md` files outside its own point at the old path — M2's (already on trunk), M3's and M4's. Each
gets that one line, the path, and nothing else. Their design claims are left exactly as their own changesets
wrote them, including the ones that describe the durable layer as if it were still there.

## State of the stack

Everything below this changeset has merged: M1 `81f01e2`, M2 `4ccc9c7`, M3 `dad07da`, M4 `a230d60`. The branch
is stacked on trunk directly, and `base: main` says so.

That field read `feat/change-tidy` until M4 merged, and it is not only where the review diff starts —
`change integrate` takes the declaration's destination from it (`destination_source: "base"`). Left naming a
branch whose work is already in trunk, the declaration would have asked CI to merge this changeset onto a
branch nobody owns, which is what M4 needed the same edit for one hop below. The durable fix is a follow-up
rather than something this changeset can do: derive the destination from the tree instead of trusting the
authored field. That derivation already exists behind `parent-changeset:`, and this stack cannot reach it
because these changesets were created with `init` and no `--parent`, so the field was never written.

The branch has been restacked as each parent merged, and every id in this file is from the current history, not
the one it was first offered with. Two replayed gate commits are absent by decision rather than by accident:
the pipefail fix, which M2 hardened further while it was in review by matching the painted transcript in bash
instead of piping it to `grep -q`, and the bash-matching fix, which was cherry-picked into M2 outright. The
first survives for its e2e half alone; the second is gone.

One hunk was resolved wrongly during the restack, and it is worth naming because the gate caught it rather
than the reviewer having to: the tidy step asserted the landing message `check` returns for a branch that
still carries the changeset, and after a tidy moves the directory away no branch does — the answer comes from
the path that reports no work in progress and names the destination as the holder. The assertion reads that
message now.

## Validation

- `mise run gates` green: the sharded suite, e2e-29 (113 steps), pty-walkthrough, ci-integrate (60 checks).
- The strict licence is tested at its three edges — a landing that replayed the run, a merge commit that
  carried it, and an approval naming no commit — in `internal/cli/landed_test.go`. `cost_test.go` measures a
  destination whose landings carry markers as well as one whose do not, because the ancestor question is only
  asked in the first, and a bound measured on the second would have been lifted in silence.
- `go test ./internal/cli/ -run 'TestCommandsNamedInTheDocsExist|TestEveryCommandIsNamedInTheDocs|TestNoDocNamesADurableRef'`
  green, so no document names a path under the retired namespace and no help text names a deleted command.
- `git grep -n 'refs/git-pair' -- '*.go'` is empty outside the test fixtures that plant inert refs to prove
  they are ignored.
