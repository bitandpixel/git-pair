# feat-rebase-review-ordering

## Summary

The span picker (`V`) ordered its two columns by commit dates, and a rebase takes the dates away from
that question: `git rebase` replays every commit it touches, so a rebased stack carries the minute of
the rebase as the committer date of all of it. The review submissions all tied, the sort kept them in
the order the records came back, and a reviewer of a rebased changeset read:

```
Review 0
Review 1
Review -3
Review -2
Last Review
```

— oldest submission at the top. The work commits sank below all of them, because the commit rows were
read from a different date column (`%at`) than the submission rows (`%ct`) and kept their original
ages. The columns now order by git's walk of the range, and the dates only say how long ago a row was.

Reported against a rebased `m4-generate`: `git log` looked right because its default format prints the
author date, while the five submissions shared one committer date (`1791414102`) and the tip commit,
made after the rebase, had its own.

## What changed

- `internal/git`: `RecentNonEmptyCommits` becomes `RangeCommits`. One walk of the range returns every
  commit oldest-first with `Position` — its index in that walk — and `ChangesFiles`, so the caller
  places submissions among commits from a single ordering source instead of comparing two date reads.
  The `limit` moved out of git and into the caller, which now windows the rows it keeps.
- `internal/git`: `LogFields` asks for `--topo-order`. Its callers read the list as the order the
  commits happened and `lifecycle.derive` takes its last entry as the newest marker, so the walk has to
  keep a commit after the one it marks. git's default order promises only reverse chronology, and the
  new test's fixture met it: with the dates tied, a marker came back *before* its own parent.
- `internal/tui`: `timeline` sorts by `Position`; `pickerItem.when` is gone. A submission the walk did
  not see goes above the rows it did see.
- `internal/gittest`: `WithAuthorDate` and `WithCommitterDate`, so a fixture can build the history a
  rebase leaves behind. `WithDate` still fixes both, and is still what the age-reporting tests use.
- `PRD.md`: the picker paragraph says where the order comes from.

## Design decisions

**Position over dates.** The two clocks disagree by construction after a rebase, and asking either one
is a coin toss on a rebased stack. git's walk is the answer to "which came first" that survives the
replay, and it is also what the reviewer means: the order the changeset's history is in now.

**One walk, not two aligned ones.** `Position` is comparable only within a single walk, so the picker
takes both the commit rows and the ordering from one `RangeCommits` call rather than assuming two
independent walks number the same commits the same way.

**Uncapped walk.** A `--max-count` would drop the commits outside the window, and a caller ranking
against a window would put the oldest submissions in the wrong place. The walk reads the range, which
is a changeset's own history and the same range `lifecycle.Summarize` walks uncapped with a larger
format; the window is cut from the rows afterwards. Side effect, in the documented direction: the
window now counts the rows that change files, which is what `RecentNonEmptyCommits` always claimed to
return.

**An unseen submission goes on top.** The only way a submission is missing from a live walk of
`base..HEAD` is that the summary outlived the history it was derived from. The rows with no position
would otherwise sort to the bottom with the zero value, burying the newest submission.

**The ages are unchanged.** `Event.When` in `lifecycle` stays `%ct`, so `status`, `queue` and
`review history` keep reporting the marker's committer date as they always have. See limitations.

## Validation

- `go vet ./...` and `gofmt -l .` clean; `go test ./...` passes.
- `TestRebasedStackStillReadsNewestFirst` (tui) builds the reported history — author dates days apart,
  every committer date one instant — and fails on the previous code with the reported reading
  (`Review 0` above `Last Review`, commits sunk below the submissions). It passes now.
- `TestRangeCommitsGiveTheRangeAnOrderDatesCannot` (git) pins the walk: dense positions, the empty
  marker counted but flagged as changing no file, the first-parent merge kept, and every parent below
  its child.
- `TestLogFieldsKeepsAMarkerBelowTheCommitItFollows` (git) pins the same ancestry property on the walk
  that feeds `derive`.
- `TestTimelinePutsASubmissionTheWalkMissedOnTop` (tui) pins the unseen-submission rule.
- Two existing tests were re-pinned because they pinned the date rule itself:
  `TestColumnsInterleaveTheChangesetsCommitsWithItsReviews` asserted that a commit dated 2020 belongs
  at the bottom, and now asserts that the history puts it at the top; `TestPickerNamesReviewsByAliasThenIndex`
  drew the columns without opening the picker, which no longer has anything to draw from — it opens it now.
- Manual: the picker's BASE column over the rebased `m4-generate` (5 submissions, 20 commits) read
  `Last Review`, then its commits, then `Review -2`, `Review -3`, `Review 1`, `Review 0`, oldest
  submission last, with the work commits interleaved. That check ran through a throwaway test harness
  that read the real repository, not through the terminal; the TUI itself was not driven.

## Known limitations

- The age beside a submission row still comes from `%ct`, so on a rebased stack those ages read as the
  minute of the rebase (`17m` for submissions reviewed days apart). The ordering no longer depends on
  it, and changing `lifecycle`'s clock would move `status`, `queue` and `review history` too — the
  queue sorts by the ready marker's age, which is a decision this change deliberately leaves alone.
- `RecentCommits`, used by the `c` drill, still lists by commit date, so its rows are in git's default
  order rather than a topological one. For a linear changeset the two agree; the drill is a lookup list
  that also takes a typed id, not the timeline.

## Open questions

- Should the age column switch to the author date for submissions, or should `lifecycle` carry both
  dates and let each reader pick? That is a CLI-wide question about `status`, `queue` and
  `review history`, so it is left out of this change rather than answered halfway here.
