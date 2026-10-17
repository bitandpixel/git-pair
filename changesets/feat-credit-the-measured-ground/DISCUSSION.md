# Discussion

## The parent landing without content the child took in

From review `d7f6cfa`, written into ABOUT.md beside the paragraph that cites the parent at its approval:

> is this true? if the parent has a "false commit" on it, and it gets merged into the child, it would be
> non-stale while the parent is unlanded. but if the parent lands without the false commit, then when the child
> starts crediting trunk, that commit wouldnt be present anymore and show stale right?

Yes on both halves. Which test refuses depends on where the parent's newest approval sits relative to that
commit.

**The commit arrived after that approval.** The child never passes at all. The parent is cited at the approved
commit, so the file matches no cited ref and the merge is refused the moment it reaches the child —
`TestCheckRefusesWhatTheParentGainedAfterItsApproval`, which runs both ways of producing it: writing the file on
the parent, and copying the child's own post-approval commit onto it and merging the parent back.

**The approval covered it, and the parent still holds it.** The child is credited while the parent is unlanded:
the parent's reviewer read that content, and the parent's own later deletion is unreviewed work on the parent's
own branch (`TestCheckStillCreditsContentTheParentHasSinceTakenBack`). Once the parent lands the smaller state,
the parent is a landed name that vouches for nothing and the destination never received the file, so the path
differs from trunk and the child refuses (`TestCheckRefusesWhatTheParentLandedWithout`). That is the credit doing
its job: the content stops being citable exactly when no live approval and no destination carries it, and the
child is sent back rather than landing a file nobody's tree holds.

**Where the declared path meets it earlier.** A parent cannot be declared on a state its newest approval does not
cover, so to land the smaller state it is re-approved on that head — and that re-approval, not the merge into the
destination, is what takes the citation away from the child
(`TestCheckRefusesContentTheParentReApprovedWithout`). The landing is what removes the citation when the parent is
merged by hand instead, which is the sequence in the question.
