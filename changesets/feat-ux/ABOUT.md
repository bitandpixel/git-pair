# feat-ux

Two fixes to the review screen, both from the same pass over it: `q` threw away the session when the
preview had the whole screen, and the list column's scroll markers cost a row they should not have cost.

## Summary

**`q` over the whole-screen preview ended the session.** That screen is the only form the diff has on a
narrow terminal, and the only form on *any* terminal below the pane's floor, so the key a reviewer reaches
for to get the file list back instead threw away the session they were in the middle of. `q` now gives the
list back there, the way `esc` and `enter` already do. From the preview *column* nothing changes: the list
is drawn beside the diff there, so `q` means leave as it means it on every other screen.

**`gg` and `G` jumped between the two regions.** They were the two ends of the whole list column: `gg` took
the keys from the tree to the box's first row, `G` took them from the box to the tree's last. Paging
already stops at the shared edge, because the two halves are two windows of two heights, and a jump that
crosses the edge has the same problem in a more surprising form. Each key now means the end of the region
that holds the keys.

**The tree's `(hidden above: 8)` note cost a row.** It was drawn as a row of its own below the last file,
and the row area gave one up to pay for it the moment the list was scrolled — which is the row a page had
just landed the cursor on. The counts are now on the rules that bound the region: an up arrow with what is
above the window at the top, a down arrow with what is below it at the bottom, padded with whitespace on
each side. The changeset box, which already put its count in its own border, got the same treatment in
both directions, and no region now has a row that can arrive late.

## What changed

**`q` over the overlay closes the preview** (`internal/tui/tui.go`, `handleDiffKey`). It routes to
`closePreview`, the path `esc` and `enter` already take over that screen: mode back to `modeFiles`, focus
back to the region that handed the keys over, and the place in the file kept, so `q` then `p` returns to
the lines the reviewer had read. In the pane the case falls through to what it did before — `quitting`,
`tea.Quit`. `ctrl-c` still quits from both layouts, and with the search field open `q` still types a `q`.

**The overlay's shortcut bar says so** (`overlayHelp`). Its exit group was `esc enter back` and its tail
was `q quit`; it is now one group, `esc enter q back`, and there is no `q quit` on that bar. The pane's bar
still ends `q quit`, which is the point: the bar of the screen that is up is what names the key.

**`activeTop` and `activeBottom` read the region, not the column** (`internal/tui/tui.go`). With keys in
the box, `gg` is the box's first row and `G` its last; with keys in the tree, the tree's. Neither moves
the other region's cursor. A region with no rows has no ends, so the jump goes to the half that has them —
an empty span or a box-less changeset keeps the behaviour the keys had before.

**The scroll counts live on the rules and borders** (`treeSpine`, `boxEdge`, `boxLines`, `listBlock`,
`hiddenAround`, `scrollHint`). `filesHidden` and `metaHidden` count each region's rows off screen above and
below, read off the same `visibleRows` arithmetic that draws them, so the number and the rows cannot
disagree. `scrollHint` renders one as `  ↑ 3 ` or `  ↓ 11 `; `treeSpine` puts it at the end of the rule it
belongs to, faint while the rule carries the focus light, and `boxEdge` gained a right-hand label so the box
can count both directions from inside its borders. The box's old one-directional `3 more` is gone.

**The row budget no longer depends on the scroll** (`chromeRows`). It grew by one whenever the tree was
scrolled, to pay for the note; that row is now the tree's to page with, and the frame is the same height
scrolled or not.

**Tests** (`internal/tui/*_test.go`). `TestQClosesTheOverlayAndPInertsOnTheOverlay` replaces the test that
asserted `q` quit from the overlay: `q` leaves mode `modeFiles`, does not quit, repaints the list's bar,
keeps the scroll, and returns to the same offset on `p`; `ctrl-c` still quits there.
`TestQQuitsFromThePaneWhereTheListIsStill` (renamed from `…AsItDoesEverywhere`) keeps the pane's half of the
contract. `TestGgAndGMeanTheEndsOfTheRegionHoldingTheKeys` and
`TestGgAndGFallThroughWhenTheRegionHoldingTheKeysIsEmpty` replace
`TestGgAndGMeanTheEndsOfTheListColumn`. `TestTheTreeCountsWhatItHidesInEachDirection` pages a 40-file tree
to the position the note used to cover and asserts both counts, that the window is the same height scrolled
or not, that every row the window has is drawn, and that the cursor's own row is on the screen. The box's
two counting tests now look for the arrows. `newFileListModelWith` is the shared session fixture with the
span's files chosen, which is how a test gets a tree with forty rows in it.

**The documents that state these keys and rules** (`PRD.md`, `README.md`): the PRD's preview prose, both of
its `q` rows and its `gg`/`G` rows in the binding list, and its paragraphs on the two rules and the box's
border; the README's pane paragraph (which now names the overlay as the exception), its overlay paragraph,
its tree-rule paragraph, and its box-border sentence. The pty walkthrough's `q` step
(`docs/plans/completed/gitpr-mvp/artifacts/pty-walkthrough.sh`) drove `p,q` and expected the terminal back;
it drives `p,q,ctrl-c` now, expects the list repainted, and still checks the pane gives the terminal back.
It is a runnable check, so it was corrected rather than left describing the old key.

## Design decisions

**`q` is read by layout, not by focus.** Both layouts hold the keys the same way, so the branch is on
`m.mode == modePreview` — what is drawn — rather than on which region has the keyboard. The difference the
key is sensitive to is whether the list is on the screen to be given back.

**The pane keeps `q` as leave.** Its rationale survives unchanged: the pane is one `p` away, the marks are
on disk, and the list the reviewer would be leaving is visible while they press it. Making the two layouts
agree would have meant either a `q` that quits on the overlay or a `q` that closes in the pane; the first is
the bug, and the second takes the settled meaning off the screen where the reviewer can see what they are
leaving. `q` now means two things in the program, which the previous wording of both documents argued
against; what carries it is the rule this screen already follows for `enter` and `ctrl-d` — the shortcut bar
names what each key means while its screen is up.

**Jumps and pages are counted the same way.** Both are counted in the window the reviewer is standing in,
so the two keys that move by more than a row agree about what a region is. Crossing between regions stays
with the keys that name their destination (`tab`, `f`, `a`, `t`).

**`↑`/`↓` rather than `▲`/`▼`.** The tree already uses `▾` and `▸` for folds, and a filled triangle at the
end of a rule reads as one more fold marker. A plain arrow says "more this way" and nothing else.

**The counts go where the region already says things.** The box had already learned that a note with a row
of its own moves the layout; the tree had not. Both now spend zero rows on it, which is also what keeps the
count from ever sitting over the row the cursor is on.

## Validation

- `mise run check` (gofmt, `go vet ./...`, `go test ./...`) — clean.
- `mise run build`, then the pty walkthrough (`bash docs/plans/completed/gitpr-mvp/artifacts/pty-walkthrough.sh`)
  against the binary this branch installs — all checks passed, including the rewritten `q` step.

## Known limitations

- The overlay's exit group is now three keys wide. At the narrowest width the overlay allows it is still one
  group, and `TestTheOverlayBarNamesAndFitsTheKeysThatCloseIt` re-checks the band fit at each width rather
  than assuming it.
- The counts are per region, so a reviewer who has folded half a tree sees the count of rows the window
  hides rather than a count that also accounts for what the fold hides. That is what the fold arrow already
  says, and the two would be harder to read together than apart.

## Open questions

- None open. If the pane's `q` later turns into a close as well, `esc` becomes the only way out of the
  preview column on a wide terminal, and the bar for that screen has to say so.
