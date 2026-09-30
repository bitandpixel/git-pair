# `ignores:` is read and never written

## Summary

`git pair change use` is deleted, with its writer `SetIgnores` and the `WithIgnores` simulation the command used to
check a write before making it. Reading is untouched: `ignores:` is still parsed, the drop pass still runs, and the
precedence M2 gave it still holds. A branch that needs settling uses `change stack` or `change combine`.

The command existed to decide what the resolver could not. The invariant (M6) refuses a branch carrying two
unconnected directories, and the two exits (M7) are the author's answers, so the shape where the key was the only way
to settle a branch no longer exists to settle.

## What this changes

- `internal/cli/change.go`: the subcommand, its runner, and its entry in the author loop.
- `internal/changeset`: `SetIgnores` and `Resolution.WithIgnores` are gone. `IgnoresKey`, the parse, and the pass in
  `filterCandidates` stay, with their comments rewritten to say why they are still there.
- Five code sites that named the command now name the exits, including the **ambiguity error a reader sees** and the
  author-command list in the root help.
- PRD 9.8 is the two exits. The sections that described the key as live behaviour are cut here, in the same commit as
  the writer, so the documentation and the command move together. README, `skills/git-pair/SKILL.md` and
  `skills/git-pair/references/cli.md` lose the command; PRD keeps one sentence saying the key is read and nothing
  writes it, phrased so the docs contract does not read it as a command reference.

## Evidence

| Fixture | Result |
|---|---|
| `TestADeclaredCohabitantStillSelectsTheDeclaringChangeset` | a file carrying `ignores: booking-tests` still makes `booking` the branch's changeset - asserted from the `Review-Changeset` trailer the offer wrote |
| `TestTheAmbiguityRefusalNamesTheTwoExits` | the undecided-branch error names both ids, `--changeset`, `git pair change stack --base` and `git pair change combine --into` |

Nothing else in the suite changed. `use_test.go` is deleted as the only file whose subject was the removed command;
no other test called `SetIgnores` or `WithIgnores`, and the full run is green with those two files gone.

The docs contract covers both directions and both pass: every command exists, and no document names a command that
does not. That second test is why eleven doc mentions across four files had to go in this commit rather than be tidied
afterwards.

Nothing on `origin/main` carries the key: 37 `CHANGESET.yaml` files, none with `ignores:`, which is the reason a
writer can be removed without a migration and without touching a single authored file.

## Deviations from the plan

- The plan's first verification bullet, "`git pair change use` on a clean branch says why it did nothing", describes a
  command that no longer exists. It is replaced by the two fixtures above: the refusal the reader can still meet names
  the exits, and the record a reader can still meet is still honoured.
- The deliverable said the command would be "refused, or narrowed to clearing a record". The reviewer chose deletion,
  recorded in the plan's M8 status.

## Not included

- No migration and no warning for files that carry the key. A record somebody made is evidence about a decision, and a
  warning would tell the author to delete information that explains their own branch.
- The drop pass stays until no file can carry the key. Removing it now would make the reader agree with the writer
  that nothing writes it, which is the wrong direction: the reader is the reason the key still means anything.
