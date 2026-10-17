# feat-credit-the-measured-ground

## Summary

A stacked child that merges its approved-but-unlanded parent into its own branch keeps its approval. The gate's
content condition credited only the integration branch, so a child could follow trunk for free and could not
follow the branch it is actually built on: merging the parent in read as "content the review never saw" and cost
a re-review. The condition now credits the ground the branch is measured on — the integration branch, as
before, and the child's parent branch while that parent has not landed.

## What changed

```console
$ git pair status            # beta, approved on alpha, after `git merge alpha`
APPROVED   review 41d56e4 (approve) is the newest commit (1 commit since)

$ git pair check             # before
NOT READY:
- content outside changesets/beta/ changed since 41d56e4: roundtwo.go

$ git pair check             # after
OK: beta is integration-ready
```

`check --json` gains `drift_credited_by`: the branch whose content accounted for each excused path.

```json
{ "ready": true, "drift_credited": ["roundtwo.go"], "drift_credited_by": { "roundtwo.go": "alpha" } }
```

`drift_credited` said a path was excused and nothing about who vouched for it, which was enough while exactly
one branch could be cited. The reason line names the branch too: `… (1 commit since, all of it content alpha
carries)`. PRD §11.3, README's `check --json` section and the `git-pair` CLI reference say the same thing.

## Design decisions

**Credit the ground, not the destination.** The refs that qualify are the ones the changeset's own diff is
measured from: the integration branch, and the branch `base:` records as its parent. `changeset.Destination` —
the branch someone would merge into — is not used, because it answers a different question and can name a
finished branch whose content is already in the destination.

**A landed parent is not cited.** A branch keeps its name after the work it carried was merged, so the name
vouches for nothing; once the destination carries the parent's landing, the destination is what is cited. This
is the same refusal `changeset/destination.go` makes when it declines to offer a landed parent's branch as a
child's destination, and `TestCheckDoesNotCreditALandedParentsLaterWork` pins the consequence: content the
parent gained after landing is drift again.

**Refs are read live, never as a snapshot.** The credit is re-taken against where each ref stands now, so
content the parent later drops — the parent's own review asking for it to go, say — stops being credited and
refuses the child. That is `TestContentTheParentHasSinceDroppedStopsBeingCredited`.

**Each ref gets both readings.** The whole-branch `merge-tree` reading is asked of every cited ref before the
file-by-file one, because a clean merge against the parent should be credited wholesale even where trunk's
merge would conflict; the file-by-file reading then attributes each path to the first ref that accounts for
it, which is what lets one merge credit one file and the other ref credit the next.

**Why the widening is safe.** The parent's content has its own changeset, its own approval and its own gate,
and a child cannot be declared until the parent has landed (`unlandedParentReason`), so no child's verdict
lands a parent's work. The alternative reading — that `base:` is author-controlled and so must not be trusted —
was investigated and does not hold: content inherited below the measurement ground is already invisible to both
content rules, and a merge done one commit earlier, before `change ready`, is not refused at all. A rule that
refuses the merge after the approval and permits it before is an ordering tax on the honest workflow rather
than a boundary.

**What did not change.** Lineage: `Review-Head` ancestry still refuses a rebase, so the approval stays a claim
about a commit. A conflict resolved by hand is still drift, because the whole-branch reading produces no clean
tree and the resolution is content nobody read. A branch that is neither trunk nor the cited parent is still
refused — that refusal is what makes the widening safe, and it is tested beside the pass.

## Validation

`go test ./internal/...` and `mise run check` (format, gofmt, vet, tests) from this branch. New cases:

- `TestMergingAnUnlandedParentInIsNotContentTheReviewNeverSaw`, `TestCreditIsAttributedToTheRefThatSuppliesEachFile`,
  `TestAnEditOnTopOfAParentMergeIsStillDrift`, `TestContentTheParentHasSinceDroppedStopsBeingCredited`
- `TestCheckKeepsTheApprovalWhenAChildTakesItsParentIn`, `TestCheckStillRefusesContentNoCitedBranchCarries`,
  `TestCheckDoesNotCreditALandedParentsLaterWork` — the gate end to end, including the refusal
- `TestCreditRefs` — the citation policy alone, because a wrong list reads as a correct refusal

`TestMergingTheDestinationInIsNotContentTheReviewNeverSaw` was updated for the reason line, which now names the
branch instead of saying "the destination". `TestAnAncestorsFileIsCreditedOnlyWhenTheDestinationCarriesIt` is
renamed to `…OnlyWhereARefCitesIt`: with the integration branch as the only cited ref it still refuses, which
is what the case now demonstrates.

## Known limitations

`git pair status` still answers "commits since the marker" with `the head moved since the review: git pair
change ready to offer it for review again`, including where this change makes `check` pass. Those screens count
trailing commits and never ask the tree question, and the reason they do is a deliberate cost decision. Acting
on that advice discards a passing approval, so it is worth its own change; it is not in this one.

The parent is cited whenever `base:` names a branch, including a stack that records no `base-changeset:`. The
ordering rule — a child is not declarable until its parent is shown landed — is what keeps that from mattering,
and it is the rule to re-read if that refusal is ever relaxed.

## Open questions

Whether `status` should print the credit at all once it stops asking the cheap question — a branch credited to
a parent is a fact a reviewer may want before opening the diff.
