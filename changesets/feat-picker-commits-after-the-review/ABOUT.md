# feat-picker-commits-after-the-review

## Summary

`V` leaves out a commit the author made after the last submission when that commit is a merge. The
case is a changeset that catches up with the integration branch: `git merge main` puts commits the
reviewer never wrote inside `last review..current`, and the columns have no row for the merge, so
there is nothing on the list to set as the boundary. The reviewer sees the span grow and no event that
explains it.

Merges were left out deliberately before this. `RecentNonEmptyCommits` read
`git log --no-merges --raw` and kept the records that named a file, and git names no files for a merge
in that form, so a merge could only ever be "empty". Asking for the merge's diff against its first
parent (`--diff-merges=first-parent`) makes it report the files it brought in, which is both what a
merge changes and what the filter was already looking for. The filter itself is unchanged.

Two tests pin the property the report was about — the columns carry everything the author did after the
newest submission — one for ordinary commits, one for the merge.

## What changed

- `internal/git/git.go` — `RecentNonEmptyCommits` asks for `--diff-merges=first-parent` instead of
  passing `--no-merges`. The doc comment says why a merge is a row now, and `changesFiles` says what a
  merge's `--raw` block holds.
- `internal/tui/picker.go` — `inlineCommits`' comment names the merge as a row and what it is for.
- `internal/git/git_test.go` — `TestRecentNonEmptyCommitsSkipWhatChangesNothing` now expects the
  `--no-ff` merge in the result and still expects the empty marker out.
- `internal/tui/picker_internal_test.go` — `TestCommitsAfterTheLatestReviewAreRowsOfBothColumns` and
  `TestAMergeIntoTheChangesetIsARowOfTheColumn`.
- `PRD.md` §14 and the README's review section — the timeline's commit rule, stated with the merge in it.

## Design decisions

**The merge is kept by giving it a diff, not by exempting it from the filter.** The alternative was to
carry the parent count (`%p`) through the record and let a merge through regardless of what it reports.
That needs a new field on `CommitTip`, a second rule inside the filter, and a row for a merge that
brought nothing — a merge of an ancestor, which is a commit whose tree is already on the list under
another name. The first-parent diff answers both cases with the one rule the function already had: git
reported files for it, so it is a row.

**What arrived with the merge stays out.** The range is `base..HEAD`, and the commits that came in with
the merge are reachable from the base, so git excludes them. The test asserts that: `work someone else
landed` is on the branch's ancestry after the merge and is not a row, because the list says "this
changeset's own history" and `Changeset Base` is where the base's story is on this screen.

**No "merge" marker in the row.** The row could say `merge` in its detail column, and the first draft
did. It reads `Merge main into feat/x` in the subject already for anything git merged itself, and for a
merge with a hand-written subject the reviewer can read the subject; the picker's job is naming
checkpoints, and the drill list — which is the unfiltered one — would then be inconsistent with the
columns about the same commit. The detail stays `short id + age`.

**Picking the merge is what makes the row worth having.** As BASE it gives the span from the catch-up to
wherever HEAD is: the work after the merge, with the incoming history behind the boundary. As HEAD it
gives the merge itself, which is the incoming history and the reviewer's own work up to that point. Both
readings come out of `git diff` over trees, which is what the span already does; no new resolution code
is involved.

## Validation

- `go test ./internal/git/ -run TestRecentNonEmptyCommits` — the merge in, the empty marker out, the
  range respected, the window honoured.
- `go test ./internal/tui/ -run 'TestAMergeIntoTheChangeset|TestCommitsAfterTheLatestReview|TestColumnsInterleave'`
  — the two new tests and the interleave test the change could have disturbed.
- Checked against a scratch repository that `--diff-merges=first-parent` prints the incoming file for a
  `--no-ff` merge and nothing for an `--allow-empty` commit, and that `main..HEAD` does not list the
  commits that arrived with the merge.
- `mise run check` (gofmt, `go vet ./...`, the suite sharded) clean.

## Known limitations

- A merge that changed nothing against its first parent reports no files and stays out, as an empty
  commit does. It brings nothing, so there is nothing for the row to say; typing its sha in the `c` drill
  still picks it.
- The columns read 50 commits (`pickerCommits`), and merges now take places in that window alongside the
  rest. A branch that merges the base daily spends rows on merges; `c` is where the rest of the history
  is, and it is unfiltered.
- The range excludes what the base holds, which is right when the base ref names what was merged. Merge
  something newer than the ref the session measures against and those commits land in the range and read
  as rows of this changeset. That is the range's existing behaviour, not something this change introduces,
  and the same commits were already listed before it.

## Open questions

- Should a merge row be reachable from `u` (the unreviewed preset)? `u` sets the newest submission as
  BASE, which is what puts the incoming history inside the span in the first place; the merge being a row
  means the fix is two keystrokes rather than a preset.
