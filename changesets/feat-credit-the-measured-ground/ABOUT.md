# feat-credit-the-measured-ground

## Summary

A stacked child that merges its approved-but-unlanded parent into its own branch keeps its approval. The gate's
content condition credited only the integration branch, so a child could follow trunk for free and could not
follow the branch it is actually built on: merging the parent in read as "content the review never saw" and cost
a re-review. The condition now credits the ground the branch is measured on — the integration branch, as
before, and the child's parent branch while that parent has not landed, cited at the commit the parent's own
approval covered.

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

The gate asks for the citations rather than computing them up front: `lifecycle.ReconcileStaleness` takes a
`CreditLookup` and calls it only after the tree comparison has found paths above the marker. The old
single-destination helper `creditDestination` is gone, since `creditContent` is the one rule now.

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

**Each ref gets both readings.** The whole-branch `merge-tree` reading is asked of every cited ref before any
of them is asked the file-by-file one, because a clean merge against the parent should be credited wholesale
even where trunk's merge would conflict; the file-by-file reading then attributes each path to the first ref
that accounts for it, which is what lets one merge credit one file and the other ref credit the next. The
lifecycle rule reads each cited commit live rather than from a cache of the merge, which is what takes the
credit off a ref that no longer carries the content — `TestContentTheParentHasSinceDroppedStopsBeingCredited`.

**The parent is cited at its approval, not at its tip.** The credit exists because a reviewer read the
content, and the only reviewer of the parent's own files is the parent's — the child's diff is measured from
the parent, so it never contained them. Citing the branch tip would take whatever that branch happens to hold
as ground, which an author can arrange: commit a file to the parent, or copy the child's own post-approval
commit onto it, then merge the parent back and the file arrives as somebody else's content with no reviewer
anywhere. So the parent is cited at the commit its newest approving review spoke about, read out of a walk of
the parent's own history from its tip. Three cases therefore cite nothing: a parent with no approval of its
own, a record that names the parent as a branch alone (with no `base-changeset:` there is no changeset to look
an approval up by, and this is the same "nothing is claimed from a name" rule `parentLanded` and
`parentAbandoned` follow), and a parent whose branch was rewritten since the approval, whose approval is no
longer in the walk at all. The trunk citation is resolved to a commit at the same moment, so both halves of
the credit are the ones this decision was about.

> is this true? if the parent has a "false commit" on it, and it gets merged into the child, it would be non-stale while the parent is unlanded. but if the parent lands without the false commit, then when the child starts crediting trunk, that commit wouldnt be present anymore and show stale right?

Yes, on both halves, and which test refuses depends on where the parent's newest approval sits relative to
that commit.

If the commit arrived on the parent after that approval, the child never passes at all: the parent is cited at
the approved commit, so the file matches no cited ref and the merge is refused the moment it lands on the child
(`TestCheckRefusesWhatTheParentGainedAfterItsApproval`, both ways of producing it). The case you describe —
credit while unlanded, refusal after the landing — is the one where the parent's approval _did_ cover the
content and the parent then drops it. While the parent is unlanded the child is credited, because the parent's
reviewer read that content and the parent's own deletion is unreviewed work on the parent's branch
(`TestCheckStillCreditsContentTheParentHasSinceTakenBack`). Once the parent lands the smaller state, the parent
is a landed name that vouches for nothing and the destination never received the file, so the path differs from
trunk and the child refuses (`TestCheckRefusesWhatTheParentLandedWithout`). That is the credit doing its job:
the content stops being citable exactly when no live approval and no destination carries it, and the child is
sent back rather than landing a file nobody's tree has.

One refinement worth having beside it. On the declared path the refusal arrives earlier than the landing: a
parent cannot be declared in a state whose newest approval does not cover what it now holds, so the parent is
re-approved on the smaller head, and it is that re-approval — not the merge into the destination — that takes
the citation away from the child. The landing is what removes it when the parent is merged by hand instead.

**Why the widening is safe.** Two things, not one. A child cannot be declared until the parent has landed
(`unlandedParentReason`), so no child's verdict lands a parent's work, and nothing is credited that a reviewer
of the parent did not cover. The alternative reading — that `base:` is author-controlled and so must not be
trusted at all — was investigated and does not hold on its own terms: content inherited below the measurement
ground is already invisible to both content rules, and a merge done one commit earlier, before `change ready`,
is not refused at all. A rule that refuses the merge after the approval and permits it before is an ordering
tax on the honest workflow rather than a boundary; what the boundary has to be is about _content_, which is
what the approval-bounded citation makes it.

**The citation is asked for, not computed.** Deciding it costs the stack reads that locate the parent, ask the
destination whether it landed, and walk the parent's history. `scripts/gates/ci-integrate.sh` counts the git
invocations `check --json` makes on a two-level stack — 43 recorded, ceiling 50 — and resolving the citations
eagerly measured 52 for a change whose common path is a branch with nothing above its marker. That is why the
lookup is lazy and why `TestTheCreditSourceIsAskedOnlyWhenContentMovedAboveTheMarker` pins the three shapes:
nothing after the marker, a changeset-only commit, and the parent's work arriving.

**What did not change.** Lineage: `Review-Head` ancestry still refuses a rebase, so the approval stays a claim
about a commit. A conflict resolved by hand is still drift, because the whole-branch reading produces no clean
tree and the resolution is content nobody read. A branch that is neither trunk nor the cited parent is still
refused — that refusal is what makes the widening safe, and it is tested beside the pass.

## Validation

`go test ./internal/...` and `mise run check` (format, gofmt, vet, tests) from this branch. New cases:

- `TestMergingAnUnlandedParentInIsNotContentTheReviewNeverSaw`,
  `TestCreditIsAttributedToTheRefThatSuppliesEachFile` (including a file both cited branches carry, which goes
  to the integration branch), `TestAnEditOnTopOfAParentMergeIsStillDrift`,
  `TestContentTheParentHasSinceDroppedStopsBeingCredited`,
  `TestTheCreditSourceIsAskedOnlyWhenContentMovedAboveTheMarker`
- `TestCheckKeepsTheApprovalWhenAChildTakesItsParentIn`, `TestCheckStillRefusesContentNoCitedBranchCarries`,
  `TestCheckRefusesWhatTheParentGainedAfterItsApproval` (the parent's own new work, and the child's own commit
  copied onto the parent and merged back), `TestCheckRefusesAParentThatHasNeverBeenApproved`,
  `TestCheckStillCreditsContentTheParentHasSinceTakenBack`,
  `TestCheckRefusesWhatTheParentLandedWithout` (credited while the parent holds the file, refused once the
  parent lands without it),
  `TestCheckRefusesContentTheParentReApprovedWithout` (the same refusal arriving at the parent's re-approval,
  which is where the declared path meets it),
  `TestCheckDoesNotCreditALandedParentsLaterWork` — the gate end to end, including the refusals
- `TestCreditRefs` — the citation policy alone, because a wrong list reads as a correct refusal and a too-wide
  list reads as a correct pass

`TestMergingTheDestinationInIsNotContentTheReviewNeverSaw` was updated for the reason line, which now names the
branch instead of saying "the destination". `TestAnAncestorsFileIsCreditedOnlyWhenTheDestinationCarriesIt` is
renamed to `…OnlyWhereARefCitesIt`: with the integration branch as the only cited ref it still refuses, which
is what the case now demonstrates.

## Known limitations

`git pair status` still answers "commits since the marker" with `the head moved since the review: git pair
change ready to offer it for review again`, including where this change makes `check` pass. Those screens count
trailing commits and never ask the tree question, and the reason they do is a deliberate cost decision. Acting
on that advice discards a passing approval, so it is worth its own change; it is not in this one.

Two readings are accepted here rather than left to fall out of the code. A parent that deletes a file after
approving it does not unreview what its approval covered: that deletion is unreviewed, unlanded work on the
parent's own branch, and it is not the state that decides whether the child's copy was ever read
(`TestCheckStillCreditsContentTheParentHasSinceTakenBack`). And a path that neither cited ref carries is
credited today because the file-by-file reading asks only whether a ref differs from head — so a reviewed file
the author deletes after the approval passes, and `drift_credited_by` has nothing true to name. That half is
pre-existing (`creditDestination` on `main` behaved the same way), it needs a presence check rather than a
difference check, and it is worth its own change.

The parent is cited whenever `base:` names a branch and `base-changeset:` names its changeset. A record with
no `base-changeset:` cites nothing, which is the conservative direction: such a child cannot be declared in
any case (`parentLanded` needs the same record).

## Open questions

Whether `status` should print the credit at all once it stops asking the cheap question — a branch credited to
a parent is a fact a reviewer may want before opening the diff.
