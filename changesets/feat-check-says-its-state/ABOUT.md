# `check` says the state it refused, and whether a declaration is wanted

## Summary

Two facts the gate knew and did not tell anybody.

A refusal printed `NOT READY:` and the reasons. That cannot tell "a reviewer said no" from "a reviewer said
yes and something else moved" — the difference between going back to the reviewer and going back to the
branch. `check --json` has carried `state` beside `ready` for exactly that reason, and the human screen was
the one surface that dropped it.

The machine form answered "may this merge" (`ready`) and "did the author ask" (`integrating`) but not "is a
declaration wanted at this head", so an agent deciding whether to run `git pair change integrate` paid a
refusal to find out.

## What it does

- The refusing screen prints `state: <STATE>` under the header, from the same value `--json` reports as
  `state` for that run. The passing verdict does not print it.
- `check --json` gains `declarable`: true where `git pair change integrate` would record a declaration.
- `declarable` takes its facts (the parent's landing, this changeset's landing) instead of a landing
  contract, so `check` answers it from the `parentStatus` and destination its gate already read.
- Tests pin the field against `change integrate`'s exit codes, and two existing assertions that counted output
  lines as bullets now count bullets.
- PRD §11.3, README, and the skill's CLI reference carry both answers, including the `check --json` example
  that showed a content-drift reason beside `state: APPROVED` — a combination the tree-reconciled reading
  makes `WORKING`.

## Why

The rule this follows is the one already written on the JSON struct: `ready` is the answer, `state` is why a
reader may want to look further. That rule was applied to the machine form and never to the human one, so the
screen a person reads in the moment of a refusal was the last place it was missing.

`declarable` is the same argument one command further along. The three questions about a hand-off belong in
the run that already computed all three; a fourth field that costs nothing beats a second command whose exit
code has to be guessed at.

## Design decisions

**The printed state is the tree-reconciled one, because it is the one this command acted on.** `check`
computes its verdict from `SummarizeAgainstTreeHEAD`, which demotes an approval to `WORKING` when paths
outside `changesets/<id>/` moved since the marker; `status` reports the newest marker and still says
`APPROVED`. Printing the marker reading would have made one screen disagree with the `--json` of the same run,
which is the drift this codebase keeps closing. The consequence is stated and pinned: a content refusal prints
`state: WORKING` beside a `status` that says `State: APPROVED`, and README's troubleshooting section explains
that pair as two readings rather than a contradiction.

**Only the refusal prints it.** The passing verdict already states everything the line would add — the gate
passed, so whatever the newest marker was, it permits the merge.

**`declarable` is the gate's verdict conjoined with the command's own early conditions, not the cheaper
predicate the screens use.** The screens print the hand-off line by `declarable`, which deliberately skips the
content and history questions so a cheap screen does not pay for a gate. Reusing it outright would have made
the field true on a branch whose approved commit was rewritten out of the history — newest marker an approval,
no file moved — and the reader would follow a `true` into a refusal. `TestCheckReportsWhetherADeclarationIsWanted`
runs the rewritten-history shape precisely because the cheap predicate passes it. What `check` still cannot see
is the declaration's own hooks, and the field comment says so.

**False is three answers, and the field says which.** The gate refused (`ready` false), the parent has not
landed so `change integrate` refuses ahead of the gate, or this head is already declared — where the command
_succeeds_ and records nothing, which is what lets a script declare unconditionally. The third is the one a
consumer will misread, so it has its own test asserting both the field and the exit code 0.

**No new reads, on purpose.** The parent condition is `unlandedParentReason`'s own emptiness — the function
that refuses — and the landing is the destination the gate already read. Both the JSON field and the human
hand-off line read the one value, so the field and the screen cannot drift apart. That keeps `check --json`'s
git-invocation count where `scripts/gates/ci-integrate.sh` records it; the numbers the gate asserts are in
the checklist below.

## Known limitations

- Landed work never reaches the JSON at all: `check` exits 2 with "that changeset landed" before the gate
  runs, so `declarable` is not consulted there. The test asserts that shape rather than pretending the field
  answers it.
- `declarable` cannot see `pre-integrate` hooks. A repository whose declaration hook refuses will see a `true`
  followed by a refusal, and the field comment names that as the limit rather than claiming the command's whole
  behaviour.
- `state` beside a refusal can differ from `status`'s state for the same changeset. That is the reading choice
  above, documented and pinned, not a bug to reconcile.

## How to check it

- `go test ./internal/cli -run TestCheckNamesTheState` — the four refusal shapes and the passing screen.
- `go test ./internal/cli -run TestCheckReportsWhetherADeclaration` — each shape asserted twice: the field, and
  what `change integrate` actually does.
- `git pair check` on an approved changeset with a file added after the approval prints `state: WORKING` above
  the drift bullet, while `git pair status` on the same tree prints `State: APPROVED`.
- `git pair check --json | jq -c '{ready, integrating, declarable}'` on a child whose parent has not landed
  answers `{true, false, false}`, and `git pair change integrate` on that child exits 1 with the parent's
  reason.
- `mise run check`, then `mise run gates`: the cost step must still report the recorded ladder (`27` at one
  level, `46` at two) because the new field asks git nothing.
