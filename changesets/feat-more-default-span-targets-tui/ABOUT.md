# feat-more-default-span-targets-tui

## Summary

`V` listed two things separately: the review submissions in the column, and everything else behind
`Commit…`. So the span a reviewer usually wants — "the work between these two submissions" — was not a
thing the screen could show. You read `Review -2`, then left the screen for the drill, found the commit
there, and carried the pair back in your head.

The drills had a second problem, which is what made the screen feel wrong under the hands. `Commit…` and
`Ref…` were rows, and `Enter` — the key a reviewer presses on the row they are pointing at — applied the
pending pair instead of descending into the drill. Pressing Enter on `Commit…` closed the picker on the
span you had come to change.

The columns are now one timeline: the submissions by alias with the changeset's own commits written between
them where they happened. Wider history is `c` and `r`, and inside a drill the keys belong to the list by
default, with `/` asking for the filter.

## What changed

- `internal/git/git.go` — `RecentNonEmptyCommits`, the commits in a range that change at least one file,
  newest first, read from `git log --no-merges --raw` in one call.
- `internal/tui/picker.go` — `inlineCommits` reads the changeset's own history once, when `V` opens;
  `timeline` merges it with the submissions by `when`; `pinnedRow` gives a checkpoint the timeline does not
  reach a row of its own. The two drill rows are gone, `c` and `r` open the drills instead, and the drill's
  modes are navigation-first with `/` for the filter. `nav` is now `filtering`, which says which way the keys
  go.
- `internal/tui/picker.go`, `internal/tui/tui.go` — the three shortcut bars: the columns name `c` and `r`,
  the drill names its own mode's keys, and the filter mode's bar ends `esc navigate`.
- `internal/tui/picker_internal_test.go` — the picker's fixture commits now carry fixed, increasing dates,
  because the merge the columns do is by when things happened and a fixture whose neighbours land in one
  second cannot say which order that was.
- `PRD.md` §14 and §28, and the README's review section — the columns as one timeline, `c` and `r` for the
  drills, and the drill's two modes.

## Design decisions

**The timeline is the changeset's own history, not everything recent.** `base..HEAD` is the range. Commits
below the base are the base's own story: they are on this screen already as `Changeset Base`, and reaching
them is what `c` is for. Merging the whole of `HEAD`'s history would have pushed the submissions out of a
window ten-odd rows tall in any long-lived repository, which is the opposite of the point.

**Empty commits and merges are left out, at the git layer.** A git-pair changeset carries commits that hold
nothing but a marker — `review: approve`, `git-pair: ready` — and those are lifecycle events rather than
work: the screen lists the submissions by alias, so a sha row for the same commit would be the same thing
twice with two names. A merge reports no files of its own and is nowhere a reviewer wants a span to end.
Both are still reachable: the commit drill is unfiltered, and any of them can be typed.

**`c` and `r` are keys, not rows.** The alternative was keeping the rows and making Enter mean "descend"
when the cursor was on one of them, which leaves Enter meaning two things depending on where the cursor
happens to be — the exact shape of the complaint. With the drills as keys, `Enter` means apply, everywhere,
and the columns stay about checkpoints rather than about navigation. Losing the drills from the cursor's
path costs nothing they could do from a row: both take typed text, so neither was ever reached by arrow keys
alone.

**The drill opens on the list, and `/` asks for the filter.** Typing was the default when the list was the
only way to a commit at all. Now the columns carry the recent history, so what a reviewer does inside a drill
is mostly move, and `j` should move. `/` is vim's own key for the filter, and it is free of `f`/`b`, which
are now page down and up beside `ctrl-f`/`ctrl-b`. `esc` hands the keys back with the filter kept — the rows
you filtered to are the rows you wanted — and `esc` again leaves the drill. `Tab` is gone rather than kept as
an undocumented second way in.

**A checkpoint outside the timeline gets a row rather than a mark on a key.** A ref, or a commit older than
the window, has no row to wear the asterisk, and with the drills no longer rows there was nothing left to put
it on. It gets a row at the bottom of the column, labelled by the endpoint's own spelling. This is what the
cursor then opens on, so `V` reopens with the mark and the cursor agreeing.

**One git call per `V`.** The columns are redrawn on every keystroke, so the history is read when the picker
opens and held in its state, not fetched by `endpointsFor`. The read failing is shown (`cannot read this
changeset's commits: …`) rather than answered with a shorter list.

## Validation

- `go test ./internal/git/` — `TestRecentNonEmptyCommitsSkipWhatChangesNothing` pins the two exclusions and
  the range, on a fixture with an empty marker and a `--no-ff` merge.
- `go test ./internal/tui/` — the picker's tests. New: `TestColumnsInterleaveTheChangesetsCommitsWithItsReviews`
  (the order, the empty marker absent, the base's own commit absent), `TestEnterAlwaysAppliesThePair`,
  `TestDrillOpensOnTheListAndSlashStartsTheFilter`, `TestChosenCheckpointKeepsItsMark`. Rewritten: the drill
  mode tests, the bar test, the drill-in helpers (`drill(t, m, "commit")` presses the key).
- `go test ./...` clean, `gofmt` and `go vet` clean.
- Read the rendered screen back at 96x24 for the columns, both drill modes, and the ref list, to check the
  bars, the caret, and the pinned row.

## Known limitations

- The columns read 50 commits (`pickerCommits`). A changeset with more recent commits than that shows the
  newest 50 and `c` for the rest; nothing says on screen that the window ended, since the drill is where
  older history is, and typing a name reaches anything at all.
- Equal commit timestamps keep the submission before the commit that shares its second. That is a stable
  answer rather than a correct one, and it is only visible in a history where two commits share a second.
- The commit drill still lists marker commits and merges, which is what makes it able to name them. The
  columns and the drill therefore answer slightly different questions about the same range.
- A base ref this clone cannot resolve makes the inline read fail and puts the git message in the picker's
  error line. The drill over the same range fails the same way, so nothing is hidden; the wording is git's.

## Open questions

- Should the columns mark which commits are already inside the span on screen, so walking to a submission
  shows what it would cover? The `Selected:` line answers that once the pair is chosen, and marking rows
  would need the span resolved per row.
- Should `u` and `f` move to the columns' timeline as `g`/`G`-style jumps instead of presets? They are the
  two spans most people want, and `j` now reaches both ends of the history more cheaply than it used to.
