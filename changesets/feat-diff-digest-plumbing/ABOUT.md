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

> wait so changing a line in a file that was already reviewed would appear as a non-change?

No. That reading comes from the sentence above it, which lists what the digest is measured *unchanged*
across, and the answer is the distinction now written into that paragraph: `--raw` is insensitive to how git
renders a diff (`-U`, whitespace-normalising options, the diff algorithm, rename detection) and hashes
content — modes, pre- and post-image blob OIDs, statuses, paths. Editing a line inside a file already in the
diff rewrites that file's post-image blob, and the OID is a hashed field, so the digest changes.

What `--raw` genuinely cannot tell you is *which* line moved: the entry for that file has the same modes,
status and path before and after the edit. That is enough for this value, because the only verdict it carries
is "this is not the diff that was reviewed" — and the refusal that explains itself is the `Review-Head` and
`Review-Base-Head` pair beside it, which name the commit and say which side moved.

Pinned now by the "a line changed in a file already in the diff is a change" case of
`TestDiffRawDigestSeesEveryChangeAReviewerRead`, which asserts both halves: the digest differs, and the raw
entry is byte-identical once its blob OIDs are hidden — so the change it detected is content and not shape.

> if the file was changed at all in the destination branch, will that digest change? let's say on a far away unrelated line?
