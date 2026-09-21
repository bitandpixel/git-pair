# fix-exit-code-wording

## Summary

Two places told the truth about an exit code and one didn't. `git pair check --json`
exits 0 when the verdict is `ready: false`, and `git pair change wait --help` said it
exits non-zero for a changeset "not ready to begin with" when only `WORKING` is
refused. Both behaviors are what the tool intends; the prose around them was the
defect, so this changeset corrects the prose and pins each behavior with a test.
Nothing changes what any command does.

## What changed

- `README.md` — the `check --json` entry in the JSON contracts section now says the
  verdict travels in `ready` and that a not-ready run exits 0, and the CI-gate
  paragraph reads "In the human form the exit code is the verdict" instead of claiming
  it for both forms. The `jq -e '.ready'` idiom is presented as the reason, not as an
  aside.
- `PRD.md` §11.3 — the same qualification beside the `--json` key list, because that
  section's opening "exits non-zero when it is not" and its exit-code table both read
  as covering either form.
- `internal/cli/change.go` — the last paragraph of `change wait`'s help names `WORKING`
  as the state it refuses and says plainly that an already `BLOCKED`/`FEEDBACK`/`APPROVED`
  changeset reports at once and exits 0.
- Tests: `TestCheckJSONContract` asserts the not-ready `--json` run exits 0; a new
  `TestCheckUsageErrors` subtest asserts `--json` does not soften a usage error;
  `TestChangeWaitHelpNamesTheStateThatRefuses` pins the help paragraph.

## Design decisions

`check --json` keeps exiting 0. The alternative — running the `errSilent` path after
`emitJSON` — would make the exit-code table uniform across forms but would put
git-pair's verdict into the exit status of a pipeline that is already reading the
verdict from `ready`, and `README`'s documented gate (`git pair check --json | jq -e
'.ready'`) takes its status from jq. The decision is recorded in the two places a gate
author reads, not in a changelog entry.

`change wait` needed no behavior change and no behavior test. PRD §9.4 already asks for
"report immediately, without waiting, when the changeset is already in one of those
states", and `TestChangeWaitRefusesAnUnqueuedChange` and
`TestChangeWaitReturnsAtOnceWhenAReviewIsAlreadyIn` already pin both halves. The help
was the only thing disagreeing, so it is pinned the way `--tool`'s help is pinned by
`TestToolFlagHelpNamesBothKindsOfSpan`: a future edit to that paragraph has to disagree
with a test rather than with a sentence.

## Validation

- `mise run check` green: gofmt, `go vet`, all 14 packages.
- Both new assertions falsified rather than merely observed. Making `internal/cli/check.go`
  return `errSilent` after `emitJSON` turns `TestCheckJSONContract` red; restoring the
  old `change wait` sentence turns `TestChangeWaitHelpNamesTheStateThatRefuses` red.

## Known limitations

`check` now documents two different exit-code contracts, in README's JSON contracts
section, in its agent-contract section, and in PRD §11.3. That is accurate and it is
also the kind of thing a later command gets wrong: nothing states as a rule that JSON
mode carries a verdict in a field and reserves the exit code for usage and git
failures, so the next `--json` gate has to rediscover it from `check`'s prose.

## Open questions

`check --json`'s exit 0 is the maintainer's call, taken here and reversible in two
lines. If symmetry across forms is worth more than the `jq` pipeline argument, the
change is to run the not-ready path after emitting, and the tests named above are the
ones that will disagree.

Should the general rule — JSON mode reports a failed verdict in a field, exit codes 2
and 3 only — live somewhere above §11.3, next to the exit-code table, rather than only
beside the one command that works that way today? `change wait --json` exits non-zero on
timeout, so the rule is not yet a rule.
