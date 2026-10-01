# Tidied changesets stop the `LANDED UNREVIEWED` report

## Summary

`queue` and `status` reported a landed changeset as `LANDED UNREVIEWED` forever, including one whose
directory had been filed away by `change tidy`. There was no way to stop it: the heading printed a read and
named no command, so the only end it had was a review that by definition could not be written.

A directory the destination carries only under `changesets/.landed/` is now asked about and not reported.
`git pair change tidy` moves it there in a changeset of renames, and the move reaches the destination through
review like any other change, so the filing is the repository acknowledging the record — in a commit, with an
author, on the destination's own line. The finding survives it: `git pair status --changeset <id>` reads the
same chain and still answers `reviewed: false`.

## What changed

- `internal/changeset/tidy.go`: `TidiedIDs(ctx, repo, rev)` — the ids a revision carries under
  `changesets/.landed/` **and not** under `changesets/`. The second half is the definition: a revision with
  both spellings of one id has the directory still in place, so the union in `LandedIDs` needs no dedupe and
  is now written as `ActiveIDs ∪ TidiedIDs` (`internal/changeset/changeset.go`) instead of merging two
  overlapping listings through a `seen` map.
- `internal/cli/landed.go`: `unreviewedLandings` asks `TidiedIDs` once per command and skips those ids, before
  the cache lookup. Nothing about `unreviewedRecord`, `landingLicence`, or the cached record changed — a
  filed directory is dropped by a destination read, not by a new cached field, so no cache format bump and no
  stale entry that still reports a filed landing.
- `internal/cli/status.go`: `landingsOnNoChangeset` enumerates the destination with `LandedIDs`, the same
  listing `queue` passes from `scan.TrunkIDs`. Before this change the two surfaces differed by construction:
  `status` used `ActiveIDs`, which excludes tidied directories, and `queue` used both spellings. Neither said
  which rule it was applying. They now hand the detector the same set and the rule lives in one place.
- `internal/cli/tidy.go`: the command's own help says what filing changes, and says plainly that the finding
  is not thereby closed.
- Docs: PRD §4 (a third property of the heading's wording), §9.10, §10.6, §11.1, §13.3, §13.5, §22; the
  README tidy sections, the `queue` table row, the `landed_unreviewed` section, two troubleshooting entries,
  the queue section, and the gate table row; `skills/git-pair/SKILL.md` and
  `skills/git-pair/references/integration.md`. PRD §13.5 also dropped a refusal that no longer exists — the
  stack guard removed by `tidy-does-not-guard-the-stack` — which had been left in the prose.
- `scripts/gates/e2e-29.sh`: two new checks in the tidy step (below).

## Design decisions

**Acknowledgement, not configuration.** The alternative was a knob: `pair.landedUnreviewed = all|tidied|off`
in git config, plus a flag on the two commands. It is a bigger surface, and it makes silencing an opinion the
holder of a clone can state to nobody. What was asked for was an end to the nagging, and git-pair already had
one: a merged rename commit. The rule is therefore a fact about the destination's tree, readable by every
clone and every CI job the same way, with no new flag and nothing to keep in sync.

**The rule is a destination fact, so an unmerged tidy silences nothing.** This is the half that keeps the
rule from being a way to shout a finding down: the move has to be merged, and merging is where review
happens. `TestATidyThatHasNotReachedTheDestinationIsStillAFinding` and the first new gate check pin it.

**The finding is untouched, which is what makes the rule safe.** `status --changeset <id>` on a filed landing
still reports `landed: true` with `reviewed: false` and the chain it read. The report stops; the record does
not become true. `TestATidiedLandingIsNotReportedUnreviewed` asserts both halves, and the second gate check
does the same against an installed binary.

**One listing per command, not one per id.** `TidiedIDs` is called once by `unreviewedLandings`, and the
repeat `ls-tree` inside it (the active listing, to subtract) is served by the memo in `internal/git`.

## Validation

Two Go fixtures in `internal/cli/landed_test.go`:

| Fixture | Asserts |
|---|---|
| `TestATidiedLandingIsNotReportedUnreviewed` | the heading is there before the tidy; after the tidy reaches `main` it is gone from `queue`, from `queue --json`, and from `status` on the destination — which keeps its exit 2 — while `status --changeset` still says `landed: true`, `reviewed: false` |
| `TestATidyThatHasNotReachedTheDestinationIsStillAFinding` | the tidy commit exists on `tidy-up`, unmerged, and `queue` still reports the landing in both modes |

`internal/changeset/landed_test.go` gains `TestTidiedIDsNamesOnlyWhatHasBeenFiledAway`, which builds the
three cases the definition turns on — moved, never moved, and moved *while the directory in place stays* —
plus a directory nested under the namespace under a name `ValidateID` refuses, and
`TestTidiedIDsOfARevisionWithNothing`. `TestLandedIDsCountAChangesetOnceInEitherSpelling` still passes
against the rewritten `LandedIDs`.

`scripts/gates/e2e-29.sh` now covers the same rule end to end against an installed binary: the filed landing
stays in the report while the tidy sits on `tidy-up`, the merge of `tidy-up` takes it out, the two unfiled
landings from earlier in the script (`unreviewed-merge`, `unrecorded-landing`) are still reported, and
`status --changeset tidied-landing --json` still says `"reviewed": false`. `mise run gates`: `E2E: all checks
passed`, `PTY`, `CI-INTEGRATE`. `mise run check` (gofmt, vet, full suite) green.

The rule was also replayed by hand in a scratch repo against a squash landing, which is the shape that made
the noise worst: before the tidy, `queue` printed the heading with the squash reason; after the tidy reached
`main`, both surfaces were quiet.

## Known limitations

**A squash's reason was already lost at the destination tip, and this change does not fix it.**
`LandedChain` sets `Head` to the destination's tip for a linear chain, so `Squash` (`Head == Landing`) is true
only until trunk moves again. A squash landing reported `the chain carries no review verdict` with a chain
ending at the destination tip rather than `the landing carried the directory in one commit`; tidying made that
visible rather than causing it. It is unchanged here, and the filed directories this change removes from the
report were the common carriers of the wrong wording. Worth its own change: `Squash` should be decided by the
shape of the landing commit, not by where trunk's tip happens to be.

**Filing is not recorded as an acknowledgement.** The rule reads the tree, not the tidy commit. A repository
that wants to see *who* filed what reads the rename commit itself — `git log --changesets/.landed/<id>`. No
marker was added: a marker would be a new claim about a landing, and PRD §13.4 keeps git-pair out of the
business of writing durable statements about finished work.

## Open questions

Should `status --changeset <id>` on a filed landing say that it is filed, so a reader can tell why the
surfaces are quiet? It would be one line beside `review: the chain carries no approval`. Left out here to
keep the surface unchanged, since nothing in the finding itself changed.
