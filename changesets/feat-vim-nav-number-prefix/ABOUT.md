# feat-vim-nav-number-prefix

## Summary

Typing a number before a navigation key in the review screen says how far that key goes, the way it does in
vim. `10j` walks ten rows of the file tree, `5k` walks five back, and `2 ctrl-f` pages two pages. Before this
change every navigation key moved exactly one row, half a page, or a page, so reading a changeset of sixty
files meant sixty presses.

## What changed

The digits are read in every place that already takes `j`/`k` and the page keys: the list column (the
changeset box and the file tree), the diff pane, the whole-screen overlay, the span picker's two columns, and
the commit and ref drills under `c` and `r`.

- `j`, `k` and the arrows move that many rows, or scroll the file on show by that many rows.
- `ctrl-f`/`ctrl-b` — and `f`/`b` in the drill, where those are the page keys — move that many pages of the
  window holding the keys.
- `ctrl-d`/`ctrl-u`, and `d`/`u` where they page half a screen, move half a page whatever number precedes
  them, and `gg`/`G` stay the two ends of the region.
- Any key that is not one of the counted keys spends the number, so a count typed and then left cannot move
  the row the following key lands on.
- Digits stay text where the screen takes text: the diff's `/` search field and the drill's filter.

The shortcut bars name it — `10j 10 rows` out in the list, `10j 10 lines` in the diff and the drill — because
the bar is the only place this screen names its keys, and a key nobody has heard of is a key nobody tries.

## Design decisions

**One count, not one per region.** `reviewModel.count` holds the digits, and `countPrefix` hands the number to
the key that follows and clears it. The two `g` leaders are kept per-region because half of a pair can be left
on one screen; a count is never split across two, it is only left over, and the key that hands the keys to
another screen is itself a key, so it spends the count on its way out. That is why `5` then `tab` then `j`
moves one row.

**The half-page keys take no count.** vim is not uniform here: `:help scroll` gives `CTRL-F`/`CTRL-B` a count
in pages but `CTRL-D`/`CTRL-U` a count in lines (it sets the `scroll` option to the count), so a counted
`5 CTRL-D` in vim moves five lines — the same distance as `5j`. Copying that would give this screen a paging
key that duplicates `j`, so the two keys that promise a half page were left alone: half a page is the distance
that keeps a line of context across the break, and the key for two pages is `ctrl-f`. The whole-page keys take
the count in pages, which is what vim does with them.

**`gg`/`G` ignore the count.** vim's `3G` goes to line three, and a counted jump to the Nth row of the region
is the obvious next feature. It is not this one: it needs its own rules for each of the five lists, and the
change reads better as one idea — a count says how far a step goes.

**A digit draws nothing while it waits**, the way vim draws nothing between a count and its motion. The status
line is where a note would have to go, and the bottom band counts the tallest bar it can be asked to draw, so
an echo there is a layout decision rather than a cosmetic one.

The typed count is capped at 9999 to keep a held digit from overflowing the accumulator. Every counted motion
clamps at both ends of its window anyway, which is what makes a count larger than the list harmless.

Two places deserve the reviewer's attention. The half-page keys are the one place this change leaves vim on
deliberate grounds, so the argument above is the thing to disagree with if it is wrong. And the single
`count` field is what a future screen would have to change first: it holds because every way the reviewer
moves the keys is itself a keystroke that spends the count, and the mouse wheel is the one input that is not,
which is why a count typed and then scrolled past is still there for the next key.

## Validation

- `go test ./internal/tui/` — `internal/tui/count_internal_test.go` drives keys through `m.Update` one
  keystroke at a time, which is what `10j` is: three messages. Sixteen tests cover the counted row keys and
  counted pages in each of the five lists (tree, changeset box, diff pane, overlay, picker columns, drill);
  `5 ctrl-d`/`5 d` moving exactly what the plain key moves; `9G` and `9gg` still being the two ends; a digit
  that starts no command, leaves every cursor, mode and offset alone, leaves `View()` byte-identical, and is
  not swallowed; `5` then `space` marking the one file under the cursor and the `j` after it moving one row;
  the leading zero (`0j`, `00j`, `007j`) and a held digit clamping at both ends of every list; a count that
  does not follow the reviewer across `tab`, `esc`, `z`, `a` or `V`; digits typed into the diff's search field
  and the drill's filter staying text; and each region's bar naming the count in the unit that region moves
  in, with the drill's filter bar naming none of it. Expected positions come from the fixture
  (`m.metaStart`, `activeWindow()`, `previewRowsTouched()`, `listRows()`), not from numbers typed into the
  test.
- Seven deliberate breakages of the implementation — `m.move(count)` → `m.move(1)`, the page keys and the
  drill's page step losing the count, the pane's `scrollPreview(1, count)` → `(1, 1)`, the picker column's
  `+= count` → `++`, `countPrefix` not clearing the count, and the half-page keys multiplying by it — each
  failed the test that should catch it, so none of the cases above are vacuous.
- `bash scripts/gates/pty-walkthrough.sh` — `PTY: all checks passed`. Scenario 7b types `1`, `0`, `j`,
  `enter` at a real terminal and checks the marker the difftool leaves behind: the tree's last row is the row
  whose `enter` is the difftool, so a count read as nothing leaves the cursor two rows up, where `enter` is
  the editor and no marker is written. Three more checks read the new group out of the painted bars of the
  list, the drill and the overlay.
- `mise run check` for the formatting, vet and suite gate.

## Known limitations

- The mouse wheel ignores a pending count: a count belongs to a keystroke, and the wheel is not one.
- A count typed and then left is not visible, so the number has to be re-typed rather than corrected. That is
  vim's behaviour too, and it is the trade for the bar staying where it is.

Does any interaction that isn't a digit after a digit clear the prefix?
