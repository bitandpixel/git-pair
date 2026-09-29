# feat-landed-tree-model

## Summary

Milestone M1 of `docs/plans/simplify-architecture/plan.md`: `.landed/` enters the tree model.

`changesets/.landed/<id>/` becomes a recognised place to keep a landed changeset's metadata, and is never
mistaken for an active changeset. Two named predicates exist for tree reads, so every reader asks the same
question the same way:

- `changeset.ActiveIDs(rev)` — what work does this revision carry.
- `changeset.LandedIDs(trunk)` — has this reached the destination, in either spelling.

No command changes what it prints. This milestone builds the read that later milestones move onto, and fixes
the listing bug that would otherwise make the first `tidy` break every branch that carries the directory.

## What changed

- `internal/changeset/changeset.go`
  - `DirsAt` is now `ActiveIDs`, and excludes the landed namespace. The private `dirsUnder` behind it lists
    the immediate child directories of any path in a revision's tree, which is what `LandedIDs` needs for
    `changesets/.landed/`.
  - `LandedIDs(trunk)` is `ActiveIDs(trunk)` plus the children of `changesets/.landed/`: one fact, two
    spellings, both read from the destination's own tree.
  - `changesetDir`, `worktreeDirs` and `ValidateID` each refuse the reserved name, so the namespace cannot
    become a changeset id through a tree listing, a working-tree listing, or `git pair init --id .landed`.
- `internal/changeset/tidy.go` (new): `LandedDir`, `Reserved`, `ActiveDirPath`, `LandedDirPath`,
  `DirPathspecs`. The literal and the exclusion rule live here, with the trap written down next to them.
- `internal/changeset/resolve.go`: `onTrunk` is built from `LandedIDs`, so a directory the destination
  carries under `.landed` counts as landed exactly as one it carries directly.
- `internal/cli/{status,queue,integration}.go`: the six `DirsAt` call sites are renamed. No logic change.
- `docs/plans/simplify-architecture/plan.md`: M1's four tasks ticked, the two fixture measurements recorded,
  and a base rule added to the Milestones preamble. The rule: every milestone branches from a fetched
  `origin/main`, never from the local `main`, and a milestone stacked on an unlanded predecessor moves onto
  the new `origin/main` before `change ready` rather than after an approval. The local `main` was six commits
  behind `origin/main` while this changeset was written, which is the case it is written against.

## Design decisions

- **The exclusion sits in the listing, not in the callers.** `DirsAt` returned every immediate child of
  `changesets/` with no dotfile exclusion, so `changesets/.landed/` would have become a changeset id named
  `.landed`, and `candidateFor` would have failed the branch over the `CHANGESET.yaml` that namespace does
  not have. One fix inside the one primitive covers all six call sites; `Reserved` is how the three other
  readers ask the same question.
- **Two predicates, named after the two questions.** `ActiveIDs` and `LandedIDs` differ by one question —
  "what work is here" versus "has this landed" — and a caller that picks the wrong one gets a wrong answer
  it cannot see. Naming them after the questions is cheaper than a comment on a shared function.
- **`onTrunk` reads `LandedIDs`.** Today the two are identical, because nothing is in `.landed/` yet; after
  `tidy` exists they are not, and answering "is this id already on the destination" from one spelling would
  offer a tidied changeset as work in progress on every branch that still carries its directory.
- **`LandedIDs` has no production reader yet.** That is the milestone boundary, not an oversight: M1 ships
  the read, M2 and M4 move callers onto it. Its unit tests are the only readers today, and the two places
  that must switch are named under Known limitations so the next commit cannot lose them.
- **`DirPathspecs` returns both spellings with a trailing slash**, for `git log --` and `git diff --`. M4's
  move and M2's chain walk both need the pair: the move puts the original path in the history of a directory
  that now lives at the other, so neither spelling alone names the work.
- **`init --id .landed` refuses.** `ValidateID` allowed a leading dot, so the namespace was a legal id and
  `init` would have written a working changeset over the directories of landed ones.

## Validation

- `internal/changeset/landed_test.go`:
  - a destination holding `changesets/.landed/done/` plus a stray `changesets/.landed/CHANGESET.yaml` whose
    `id:` parses and matches: `ActiveIDs` on a branch off it lists only the active changeset, `LandedIDs` on
    the destination lists `done`, and the branch resolves to one candidate instead of erroring or turning
    ambiguous. That stray file is the sharpest form of the trap — it is a metadata file whose directory name
    is the namespace.
  - a destination holding both spellings at once (`changesets/ui/` and `changesets/.landed/booking/`):
    `LandedIDs` reports both, once each.
  - a revision with no `changesets/` directory: empty, not an error, for both predicates.
  - `ValidateID` refuses `.landed` and still accepts ordinary ids; `DirPathspecs` names both paths.
- `mise run check` per commit, `mise run gates` at the milestone boundary. Nothing reads `LandedIDs` in
  production code, so the replays must be unchanged; they are the evidence that this commit is a read-only
  change to the tree model.
- Two findings from building the fixtures, both written into the code that met them:
  - `git mv` will not create the destination's parent directory, so a move into `changesets/.landed/` needs
    that directory to exist first. `landed_test.go` creates it deliberately; `tidy` (M4) has to as well.
  - `ls-tree` reports an unresolvable revision as `Not a valid object name`, which
    `git.IsUnknownRevision` does not match. The unknown-revision tolerance in the tree read is therefore not
    reachable through this path, and the test says so rather than asserting a tolerance that does not fire.

## Known limitations

- `internal/cli/integration.go:473` and `:945` still read `ActiveIDs` where the question is "what does the
  destination hold", which is `LandedIDs`. They are behaviour-identical until something is tidied. Switching
  them now would decide M4's order of operations (record before tidy, or after) from inside M1.
- `LandedIDs` costs two `ls-tree` calls per destination instead of one. It replaces a call in the resolver,
  which runs once per command, and the destination walk in `internal/changeset/destination.go` already reads
  more trees than that.
- A repository whose `changesets/.landed/` already exists for other reasons is treated as landed work.
  Nothing in this repository has created that directory, and `init` now refuses to.

## Open questions

1. Should `status` say anything about tidied directories — a count, or nothing at all? M1 deliberately says
   nothing, and M2's finding work is where that gets decided.
2. `LandedDir` is exported because M4 writes it and the tests read it. If `tidy` turns out to be its only
   writer and the path helpers the only readers, the constant can go private and the surface shrinks.
3. `dirsUnder` filters reserved names only for `changesets/` (`ActiveIDs`), not inside
   `changesets/.landed/`. A `changesets/.landed/.landed/` directory would be listed as landed work. Harmless
   today; whether to make `dirsUnder` filter unconditionally is a one-line decision for whoever next touches it.
