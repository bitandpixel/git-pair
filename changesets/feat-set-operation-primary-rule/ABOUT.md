# The set operation is the primary rule, and the only subtraction

## Summary

Candidates are decided in this order: landedness (in `Resolve`), then `ignores:`, then subtraction of the ids other
candidates record as their `base-changeset:`, then the add-commit ranking, then a refusal that names what remains.
The subtraction reads one field and no history. The probe branch it came from is deleted, its eight shapes now being
tests in the package.

## What this changes

`filterCandidates` runs `ignores:` first, then `subtractRecordedParents` — the candidates minus the changesets other
candidates record below them. One pass is enough for a chain because every level records its own parent. A candidate
naming itself is not an edge, and subtracting everything, which mutual records cause, keeps the list and answers
with the refusal rather than "no changeset here".

`stackParentID` is gone. Its job — turning a candidate into the thing to subtract — is now the recorded id and
nothing else, so there is no fallback chain to keep straight, and `Resolution.Candidates` means what survives the
two filters. `check --json`, `status` and the queue print that remainder, which is why a stacked branch now names
one id where it used to name the whole ancestry.

**The review decided the one open question: the older branch-name subtraction goes rather than becoming a lower
tier.** That pass removed whatever a candidate's `base:` looked like it named, so it decided a stack only when a
branch happened to be spelled like its changeset — this repository's own convention, which is how a rule can look
load-bearing while testing nothing — and it said nothing at all about a file that recorded no id. Such a file now
falls through to the ranking, which answers correctly (the child's directory joined the line later) and says so by
leaving both candidates in the list.

PRD §4 and §9.8 state the rule in that order, including the sentence that the reported candidates are the
subtraction's remainder.

## Evidence

`internal/changeset/set_operation_test.go`, the eight shapes from `probe/setop-selection`:

| Fixture | Result |
|---|---|
| `TestTheSetOperationDecidesALinearStack` — three levels, each naming the one below | `[feature-c]`. **Fails without the subtraction** |
| `TestTheSetOperationIsNotMovedByALaterEditToTheParent` — child stacked, parent edited last | `[booking-tests]`. **Fails without the subtraction** |
| `TestMutualBaseChangesetRecordsLeaveTheCandidatesStanding` | ambiguous, both standing. **Fails without the subtraction** |
| `TestALegacyStackWithoutARecordedIdFallsToTheRanking` | child selected, both directories reported |
| `TestAStackOnALandedParentLosesNothing` — the middle level landed, the child records its id | child selected, child and grandparent reported |
| `TestACandidateNamingItselfIsNotItsOwnParent` | `[selfish]`, selected |
| `TestTheDeclarationStillOutranksTheSetOperation` — `ignores:` on the parent, the child records it below | `booking` selected, one candidate |
| `TestSiblingsWithNoRecordsAreOrderedByCreation` | later-created selected, both reported |

Three of the eight catch the subtraction's absence; the other five are the guards and the cases the rule must not
break, and they hold with the rule removed as well as with it in. Saying which is which is the point of porting all
eight rather than the three that fail.

No existing fixture needed changing — not one in `internal/changeset`, none in `internal/cli`. Nothing in the suite
depended on the branch-name tier, which is worth stating plainly: its removal is invisible to the tests and visible
only to a branch whose stack predates the recorded id.

The cost contract from M3 still holds and now names the rule that earns it: a stack whose recorded link decides
performs no history read at all.

## Deviations from the plan

- The fixtures went into `internal/changeset/set_operation_test.go`, not `resolve_test.go`, which is already the
  largest file in the package. The plan text says so too.
- The probe's `activeBySubtraction` returned a decision alongside the list. Here the subtraction returns the list and
  whether the records contradicted each other, because `choose` already owns the deciding and the empty-set guard
  already reports the contradiction.

## Not included

- No migration of unlanded files, so the legacy shape stays in the ranking's population until those changesets land.
  The plan's own note on the tie-break is where that is recorded.
- No change to what M6 will refuse. Two of these fixtures (siblings, mutual records) are shapes the invariant
  refuses at offer time; `status`, `ls` and `diff` still read them, which is why the ranking stays.
- No new surface for the smaller candidate list. The commands print what the resolver returns.
