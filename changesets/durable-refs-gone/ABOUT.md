# durable-refs-gone

## Summary

Milestone M5 of `docs/plans/completed/simplify-architecture/plan.md`: delete the durable-ref subsystem. git-pair
writes no ref at any point in a lifecycle, and `refs/git-pair/` becomes a namespace the code neither reads
nor writes. Against `origin/main` the branch is 99 files, +4832/−9915 — that number is the whole stack.
This changeset is 79 files, +2067/−9015 against its base `feat/change-tidy`; the deletion proper is
`5e987db`, and what follows it is the rest of the system being told.

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
  destination with no permitting verdict in the chain the destination carries. Nothing closes it with a
  command, so there is no closing command to print; the fix printed is `git pair status --changeset <id>`.
  The findings ride `status`'s exit-2 error on stderr, and `queue --json` carries `landed_unreviewed`.
- `next_action` for a ready head is one sentence — `` `git pair check`, then merge into main with ordinary
  git `` — because there is no step after the merge. `contract_test.go` pins that string, and
  `TestNoActionNamesADeletedCommand` pins the general rule.
- `status`'s `Stack:` block reads each ancestor's landing from the destination and prints `landed:` rather
  than `record:`; `stack[].integration` in `--json` is `stack[].landed_commit`. `parent.landed_in_default_branch`
  is deleted: it was true whenever `parent.landed` was, and the index it came from is gone.
- `change integrate` refuses a stacked child whose parent is not landed, and the reason names what to do
  (`git pair check`, then merge with ordinary git) instead of a record to write.
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
from `integration configure` to `change tidy`. 110 `ok:` steps in e2e-29 where there were 72.

## Known limitations, stated where the refs used to promise otherwise

- A squash, cherry-pick or rebase-merge landing that carries no markers keeps nothing: `chain_base`,
  `chain_head` empty and `reviewed: false`. PRD §13.3 says so, and `change tidy` is the mitigation for the
  clutter it leaves. A `--no-ff` merge keeps the whole chain, which is the shape the gates replay.
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

## State of the stack

M1 `81f01e2` (trunk), M2 `feat/landing-is-a-tree-fact`, M3 `feat/derived-bases`, M4 `feat/change-tidy`.
None of M2–M4 has merged, so `origin/main` is still M1 and this branch stays stacked on M4: the review diff
is against `feat/change-tidy`, and `base: feat/change-tidy` says so.

## Validation

- `mise run gates` green: the sharded suite, e2e-29 (110 steps), pty-walkthrough, ci-integrate (60 checks).
- `go test ./internal/cli/ -run 'TestCommandsNamedInTheDocsExist|TestEveryCommandIsNamedInTheDocs|TestNoDocNamesADurableRef'`
  green, so no document names a path under the retired namespace and no help text names a deleted command.
- `git grep -n 'refs/git-pair' -- '*.go'` is empty outside the test fixtures that plant inert refs to prove
  they are ignored.
