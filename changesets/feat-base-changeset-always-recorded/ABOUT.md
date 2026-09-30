# A stack records `base:` and `base-changeset:` together, and the id is read

## Summary

A stacked `CHANGESET.yaml` now says `base:` plus `base-changeset:`, always both, and the recorded id reaches the
resolver. `parent:` and `parent-changeset:` stay readable and are never written. `init` records the pair without the
author naming the parent changeset, and the CLI's private walk over the recorded chain is gone because the resolver
now does that drop itself.

## What this changes

`stackOf` reads the id from either key. When only `base:` is present it keeps the id instead of discarding it, and
sets the stack's parent to that base — a base recorded together with a changeset id claims the same relationship
`parent:` used to make alone. That is what lets `BaseFor` answer with the parent branch while the branch still
carries the work below, and with the run shared with the destination once that work lands. Before this, a
`base:`-recorded stack had no parent branch at all, so the durable id reached nothing.

`stackParentID` returns the recorded id first. The ancestor drop compares against a changeset slug, so this is the
point where the drop starts firing for `base:`-recorded stacks, and `withoutRecordedAncestors` in
`internal/cli/init.go` — which existed only because the drop could not — is deleted with it.

`renderMetadata` writes one spelling: `base:`, then `base-changeset:` whenever there is a stack. Both older keys are
in the drop list rather than the pass-through, so a rewrite of a legacy file comes out in the new spelling instead
of carrying two answers to one question.

`Stack.ParentChangeset`, `Changeset.ParentChangeset` and `WriteOptions.ParentChangeset` become `BaseChangeset`, and
`parentChangesetOn` becomes `baseChangesetOn`. `ParentBranch` keeps its name: it is the branch the stack sits on,
read from either spelling.

`check` gains one recommendation. A file whose recorded id and whose `base:` name different changesets is read two
ways at once — the branch says one changeset is below, the id says another — so it now says which two it means and
names `--set-parent`. It is advice, not a reason: which half is stale is the author's fact, and a gate reason here
would let a reviewer's diff settle something they cannot see.

## Evidence

| Fixture | Result |
|---|---|
| `TestARecordedStackShrinksToTheChangesetOnTop` — trunk, `booking`, `booking-tests`, `booking-cases`, each on a branch not named after its slug | `[booking-cases]`, selected, undecided never. Fails without M4's read, both for the id key spelling |
| the same fixture's `parent:`/`parent-changeset:` subtest | passes before and after — this is the "stays readable" half, and it needed a fixture of its own |
| `TestALegacyParentFileAnswersWhatTheNewSpellingMeans` | same selection, same measurement base, same `BaseWhy`. Fails without M4: the new spelling lost its id and measured against the wrong place |
| `TestAFileNamingBothBaseAndParentIsRefused` | `ErrParentWithBase` from `StackAt` |
| `TestCheckRecommendsWhenTheRecordedIdAndItsBaseDisagree` | one recommendation naming both ids, no reason carrying it, `recommend:` prefix on the human surface |
| `TestInitLooksPastTheAncestorsAStackedBaseCarries` | unchanged assertion, and it now exercises the resolver's drop instead of the deleted walker |
| full `go test ./...` | green |

The fixture's first draft was not discriminating, and the reason is worth keeping: its branches were named
`booking-tests` and so on, equal to the changeset slugs, so the old slug comparison matched by accident and the test
passed without the fix. Branch names that do not equal their slugs are what make the id the only thing that can
match — which is the same property the plan's first success criterion asks for.

Four existing fixtures asserted the old spelling of a written file and moved to the new one, with the negative
assertion reversed to "no `parent:` in a file written now": `TestChangeInitRecordsRequestedBase`,
`TestInitRecordsTheStackTheBaseReveals`, `TestInitLooksPastTheAncestorsAStackedBaseCarries`,
`TestInitParentWritesTheStackOnce`, and the restack assertion in `TestRestackingTakesAnExplicitFlag`. The behaviour
they cover is unchanged; only the key they read changed, which is this milestone's deliverable.

Two of M3's fixtures changed premise rather than assertion: `twoCandidatesUnordered` now leaves the child's id
unrecorded when the point is that two candidates survive, and the ranking pair names which rule answers each case.
The recorded case is decided by the drop now, and the assertion — the child, not the parent, when the newest edit is
on the parent — is the same one M3 shipped with.

## Not included

- No migration. Nothing rewrites a landed file; `TestALegacyParentFileAnswersWhatTheNewSpellingMeans` is the
  substitute the plan asked for.
- `init --parent <branch>` keeps its flag name. It records the new spelling; retiring the word from the surface is
  M8 and M9's, where the PRD and the remaining `ignores:` writer are dealt with together.
- The PRD still describes `parent:` as the authored link. M9 rewrites those passages, and it is where the key-name
  assertion for the docs contract tests belongs — those tests read the PRD, README and skill only, so nothing in
  them noticed this change of spelling.
- `destination-from-tree` (branch `feat/destination-from-tree`) reads `Stack` and `Changeset` fields that this renames, so its next rebase carries
  the conflicts. Its argument is about landedness, not about which key holds the id, so the conflicts should stay
  mechanical.
