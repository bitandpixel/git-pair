# feat-diff-digest-plumbing

## Summary

A review submission now records the identity of the diff it reviewed, in a new `Review-Diff-Id` trailer,
alongside the head (`Review-Head`) and the measured base (`Review-Base-Head`) it already recorded.

Nothing reads the value yet. It is written so that the gate's content question — is the diff under test the
diff that was approved? — can become one comparison instead of an inference from how far the parent and the
destination have moved since the approval, and so that the rule that makes the comparison can be written
against real markers rather than invented ones. The reading lands in the changeset that follows this one.

Three things moved with it, because they are the same measurement:

- `git.Repo.DiffRawDigest` hashes a diff's raw name-status description with its flags pinned.
- `changeset.Measurement` and `changeset.MeasureSubmission` measure the three recorded ends of a diff in
  one place, so `review submit` and the review screen cannot record different things about the same head.
- `reviewops.Submit` and `marker.ReviewMessage` take that one value instead of two positional strings that
  were one commit apart and could be passed in either order.

## Design

**Why a digest of the file list and not of the patch.** A digest of patch text is a digest of a rendering.
Measured on one commit in one repository, `git patch-id --stable` gave three different values under the
default, `-U10` and `-U1` (`89428fae…`, `df930f38…`, `8e490fa5…`), because the context lines the diff
happens to print are part of the bytes it hashes — so `diff.context` in a reviewer's `~/.gitconfig` would
decide what an approval recorded, and changing it would silently invalidate every approval on the branch.
`--raw` reads the object database instead: modes, pre- and post-image blob OIDs, statuses, paths. Measured
unchanged across `-U3`, `-U10`, `-U1`, `--ignore-all-space` and `--diff-algorithm=patience`, and still
sensitive to a trailing-whitespace change and to a mode change, which is what a reviewer read.

The two claims are about different things, and the split is the whole design: what is insensitive is how git
*renders* a diff, and what is hashed is the *content*. `--raw` prints no hunk text, so an entry for a file
that was already in the diff looks the same when one line inside it changes — same modes, same status, same
path — and what differs is the post-image blob OID, which is one of the hashed fields. A change to content
is therefore never a non-change, including in a file the reviewer had already read; the digest does not say
which line moved, and it does not need to, because the only verdict it carries is "not the diff that was
reviewed". The single deliberate blindness is the review record, which is excluded by name.

**The load-bearing invariants, and where each lives.**

- *Configuration cannot move the answer.* `internal/git/diffdigest.go` pins `--no-renames` (rename
  detection is a shape difference in the raw form: one `R` entry, or a `D` and an `A`), `--no-ext-diff` and
  `--no-textconv`, and builds no `-c` arguments so `classify` keeps the invocation a pure read in the memo.
  `core.quotePath=false` comes from `spawn` for every invocation. Enforced by
  `TestDiffRawDigestIsNotMovedByDiffConfiguration`, which sets `diff.context`, `diff.algorithm`,
  `diff.renames` and `diff.noprefix` in the repository config and asks again.
- *A recorded value is a fact about two named commits.* Neither end moves, so nothing that happens
  elsewhere — another branch, another file, or another line of the same file — can move the answer. This is
  what separates the digest from a reading of how far the world has moved, which would expire on somebody
  else's merge. Enforced by `TestDiffRawDigestIsNotMovedByWorkOnAnotherBranch`, which also measures against
  the branch that gained the content to prove the value is pinned rather than inert.
- *Content cannot hide from it.* Enforced by `TestDiffRawDigestSeesEveryChangeAReviewerRead` (whitespace,
  mode, two different contents, and a line changed inside a file already in the diff — the last of those
  asserting that the raw entry's shape is *unchanged* once its blob OIDs are hidden, so the digest moved on
  content and not on shape) and by the digest being over blob OIDs.
- *The changeset's own directory is outside the identity.* The review record — ABOUT.md, and the threads
  inside it — lives in it, so without the exclusion a reply to a review thread would change the identity of
  the approval that reply is written into. Enforced by the caller
  (`changeset.MeasureSubmission`, passing `ActiveDirPath` and `LandedDirPath`) and pinned at both levels:
  `TestDiffRawDigestExcludesWhatTheCallerNames` at git, and
  `TestAReplyToTheReviewThreadKeepsTheRecordedIdentity` /
  `TestAReplyToTheReviewThreadLeavesTheRecordedDiffAlone` through the command and through `Submit`.
- *A value is only worth its trailer: it must be reproducible from the marker that carries it.* Pinned by
  recomputing with git from the base the same marker names, in
  `TestSubmissionRecordsTheIdentityOfTheDiffItReviewed` and `TestSubmitRecordsTheIdentityOfTheDiffItReviewed`.
- *An unreadable value is an absence, never a difference.* `model.ParseDiffID` and `lifecycle.Event.DiffID`
  are the only readers of the format; a wrong version or a malformed value reads as "no identity recorded",
  which is the same reading an absent trailer gets, so no approval is refused on a mismatch nobody can
  explain. Pinned by `TestAFutureVersionReadsAsNoDigest` and `TestAMalformedDiffIdentityReadsAsNoDigest`.
- *An unmeasurable value is not recorded.* `MeasureSubmission` does not fail: a git read that does not
  resolve leaves the trailer absent, which is a fact a reader can see, rather than a fabricated digest or a
  refused verdict. A revision named to `DiffRawDigest` that it cannot read is an error, not an empty digest.
- *A record describes the moment it was written.* Each submission records its own head's identity and an
  earlier marker keeps its own, pinned by
  `TestSubmissionsOverDifferentWorkRecordDifferentIdentities` and `TestEachSubmissionRecordsItsOwnDiff`.

**Why the value is versioned.** The identity is defined by the rules that produced it: the pinned flags and
the directory exclusion. A rule change is therefore a version bump rather than a silent edit that mismatches
every approval ever written, and a marker from another version falls back to the comparison this build can
still make.

## How verified

```shell
gofmt -l internal cmd      # clean
go vet ./...               # clean
go test ./...              # all packages
mise run gates             # build + scripts/gates/e2e-29.sh + pty-walkthrough.sh + ci-integrate.sh
```

The new coverage: `internal/git/diffdigest_test.go` (configuration insensitivity, whitespace, mode,
exclusion, unreadable revision, empty diff), `internal/lifecycle/diffid_test.go` (reading the trailer: the
version check, a future version, six malformed values), `internal/reviewops/reviewops_test.go` (three
submissions), and `internal/cli/submission_diff_id_test.go` (the same through `review submit`, which is the
surface a reviewer uses).

## Alternatives & rejected

- **`git patch-id --stable`.** Rejected on measurement, above: the value depends on how the diff was
  rendered, and configuration is not part of what was reviewed.
- **Hash the patch text ourselves with pinned flags.** Rejected for the same reason with more surface: every
  diff option, present and future, becomes part of the identity, and a change to one is a mass silent
  mismatch. The raw form has two such settings and both are pinned.
- **Record the destination as well** (`Review-Trunk-Head`, say). Rejected: the destination is resolved live,
  and a second recorded commit invites the question of which one the rule should trust when they disagree.
- **Compute the digest at read time only, record nothing.** Rejected: the point of the record is that the
  comparison stays askable after the base's branch is deleted and after the head has been rewritten by a
  rebase that a reviewer never saw.
- **Leave `Submit` taking positional strings.** Rejected: a third adjacent revision-shaped string is a
  mistake waiting to be made silently, and the three values are one measurement. `changeset.Measurement` is
  now where their meaning is written, once.

## Open questions

- Should `status` and `check --json` surface the recorded identity beside `parent.measured_base`? Deferred
  with the changeset that reads the value, so the JSON reports only what the gate actually compares.
- The digest is sha256 of the raw output. Nothing in git-pair has an opinion about the algorithm, but a
  change to it is a version bump, not an edit.

## Known limitations

- Nothing reads `Review-Diff-Id` in this changeset. A marker carrying it behaves exactly as one without it,
  which is the point of plumbing first: it keeps the rule that compares digests a separate, reviewable
  decision about when an approval may stand.
- The digest is of committed content between two named commits. It says nothing about whether that content
  merges cleanly into a destination, and no claim is made that it does.

## Addressed feedback

### A line changed inside a file the reviewer had already read

The review asked whether one line changed inside a file that was already in the diff would read as a
non-change. It would not. That reading comes from the sentence above it, which lists what the digest is
measured *unchanged* across, and the answer is the distinction now written into that paragraph: `--raw` is
insensitive to how git renders a diff (`-U`, whitespace-normalising options, the diff algorithm, rename
detection) and hashes content — modes, pre- and post-image blob OIDs, statuses, paths. Editing a line inside
a file already in the diff rewrites that file's post-image blob, and the OID is a hashed field, so the digest
changes.

What `--raw` genuinely cannot tell you is *which* line moved: the entry for that file has the same modes,
status and path before and after the edit. That is enough for this value, because the only verdict it carries
is "this is not the diff that was reviewed" — and the refusal that explains itself is the `Review-Head` and
`Review-Base-Head` pair beside it, which name the commit and say which side moved.

Pinned now by the "a line changed in a file already in the diff is a change" case of
`TestDiffRawDigestSeesEveryChangeAReviewerRead`, which asserts both halves: the digest differs, and the raw
entry is byte-identical once its blob OIDs are hidden — so the change it detected is content and not shape.

### Work that lands elsewhere, including in the same file

The review then asked whether a change to that file on the destination branch — a far away, unrelated line —
would change the digest. It does not change the value an approval carries, and the reason is what the value
is: a digest of the diff between **two named commits**, both of them recorded in the marker. Another branch
committing to the same file on a line nowhere near the one this branch changed touches neither of those
commits, so the answer is the same bytes forever. That is the difference between this and a reading of "how
far has the world moved since the approval", which expires on somebody else's merge in an unrelated file.

What can move a comparison is naming a different pair of commits, and that is the trap the next changeset
avoids by construction: the gate that reads this value measures the branch from the ground its work sits on,
not from the destination's tip. Measured against the tip, everything trunk gained since the branch forked
arrives inside the child's diff as reverse changes, and the case asked about here is exactly the one that
then bites — an approval dying on a far away line nobody reviewed. Its measurements are in that changeset's
`ABOUT.md` case table rather than repeated here.

The case trunk movement *does* legitimately create is not visible in any digest: the two changes needing
each other. That is a conflict, and the answer to it is the merge probe in the changeset that reads this
value — a digest of committed content between two commits says nothing about whether they merge cleanly, and
`Known limitations` above says so rather than leaving it to be inferred.

Pinned by `TestDiffRawDigestIsNotMovedByWorkOnAnotherBranch`, which commits to the same file on another
branch and asserts both halves again: the digest between the two named commits is unmoved, and the digest
to the branch that gained content is not — so the value is pinned to its commits rather than inert.

### A trunk change to the same file, on lines the child does not touch

The review asked whether a change that landed in trunk — same file the child changed, lines nothing in the
child touches — forces a re-approval. Two cases, and the digest asks for one in neither.

-   **The trunk commit is not in the child's history**, which is the ordinary case: a branch does not absorb
    trunk by osmosis, and "between" in time is not "between" in ancestry. Such a commit cannot appear in a
    two-dot diff between the recorded base and the recorded head, because neither of those commits contains
    it. That is the case the test above pins.
-   **The child merged trunk in**, so the commit is an ancestor of the head. The answer then depends on
    whether the two edits are in the same file. If trunk's change is in a file this branch does not touch,
    the ground the gate measures from is that trunk commit — it already carries the change, and the identity
    is unchanged. If it is the *same* file, the identity moves even when the line is nowhere near the child's,
    because the value hashes post-image blob OIDs and a blob is a whole file: the child's post-image now
    contains trunk's line as well. A far away trunk line in a shared file costs a re-read of that file.

For the second case this is not a new cost, because a different rule refuses first: the one that counts
content the review never saw refuses on any content arriving from another branch, near or far. Its boundary
is `TestMergingTheDestinationInIsStillUnreviewedWork`, and PRD §27 names teaching it about the destination as
the remaining half — which this identity cannot do on its own, since it tells you what a file contains and
not whose line is whose. The limit is stated in `Known limitations` above, and pinned by
`TestContributionCountsATrunkEditToAFileTheChildAlsoChanges` in the changeset that reads this value.

And the case where the two changes need each other is not a content question at all. Same file, lines that
depend on each other, is a conflict: what would land is a resolution somebody writes later, over content no
reviewer saw, and the answer to that is the merge probe in the changeset that reads this value — not a
digest, which can only ever say what two commits contain.
