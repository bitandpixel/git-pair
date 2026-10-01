# The command tree is the command set

## Summary

PRD §8 draws the CLI as a tree. The drawing was three commands stale in one direction and five missing in the other,
and the drift guard that catches a document naming a command that does not exist cannot see it. The tree is now
correct and a check keeps it that way.

## What this changes

- **PRD §8's tree**: removes the `integration` group with `record` and `publish`, and `change use` (deleted in this
  plan's M8). Adds `change integrate`, `change tidy`, `change stack`, `change combine`, and the `skill` command with
  its four subcommands. The sentence above it said "Four commands are top-level"; six sit outside the two groups now
  that `skill` is counted, so it names all six and says why each is where it is.
- **README's gate table**: the CI job's steps were "the gate, the merge, the record, the publish" — the job merges
  and pushes, which is what its own header says, and that is what the row now says.
- **`TestTheCommandTreeIsTheCommandSet`** (`internal/cli/docs_contract_test.go`): finds the fenced block whose body
  starts with `git-pair`, parses the drawn entries into command paths (depth from the column the branch marker sits
  at, four characters per level), and compares them with `commandPaths(newRootCommand(...))` — the set `--help`
  builds, minus cobra's own `help` and `completion`.

## Why both directions

A ghost entry tells a reader to run something that does not exist. An omission tells them the tool cannot do
something it can. The old tree did both at once: it listed `integration record` and `integration publish`, which
were removed with the ref model, and omitted `change tidy`, `change stack`, `change combine`, `change integrate`
and `skill`, which are the commands people use most. A check that only looked one way would have fixed half of it.

A document set that contains **no** tree fails too. Without that, deleting the block would turn the check off
silently, which is the same failure with better manners.

## Evidence

| Break | Result |
|---|---|
| `│   ├── use` added back under `change` | "PRD.md's command tree lists `change use`, which the CLI does not have" |
| `│   ├── stack` removed | "PRD.md's command tree omits `change stack`" |
| the block's `git-pair` line renamed | "no command tree in the reference documents: the tree is a checked surface, and a document set that lost it would stop being checked without saying so" |

The tree's contents were read from the binary, not remembered: `git pair --help`, `git pair change --help`,
`git pair review --help`, `git pair skill --help`.

`git grep` over PRD, README and `skills/` now finds `integration record` in exactly one place — PRD §13's "A landing
is not a command. There is no `integration record`, nothing to publish, and no ref to fetch", which is the sentence
that documents its absence and stays.

## Not included

- `docs/plans/lineage-in-the-surface/plan.md` and `docs/plans/review-architecture-v2/plan.md` still speak of
  `integration record` throughout. They are dated plans describing the design that was later removed; editing them
  would falsify the record. If they should carry a superseded note, that is a separate decision.
- No change to the existing prose checks. They do their job on prose, which is where commands are named in
  sentences; a tree is a different shape and needed its own.
