# feat-ux

## Summary

Over the whole-screen preview, `q` ended the session. That screen is the only form the diff has on a
narrow terminal, and it is the only form on *any* terminal below the pane's floor, so the key a reviewer
reaches for to get the file list back instead threw away the session they were in the middle of — on a
narrow terminal, the one screen where the preview is all there is.

This changeset makes `q` over the overlay give the list back, the way `esc` and `enter` already do. From
the preview *column* nothing changes: the list is drawn beside the diff there, so `q` means leave as it
means it on every other screen of the program.

## What changed

**`q` over the overlay closes the preview** (`internal/tui/tui.go`, `handleDiffKey`). It now routes to
`closePreview`, the same path `esc` and `enter` take over that screen: mode back to `modeFiles`, focus back
to the region that handed the keys over, and the place in the file kept, so `q` then `p` returns to the
lines the reviewer had read. In the pane the case falls through to what it did before — `quitting`,
`tea.Quit`. `ctrl-c` is untouched and quits from both layouts, and with the search field open `q` still
types a `q`.

**The overlay's shortcut bar says so** (`overlayHelp`). Its exit group was `esc enter back` and its tail
was `q quit`; it is now one group, `esc enter q back`, and there is no `q quit` on that bar. The pane's bar
still ends `q quit`, which is the point: the bar of the screen that is up is what names the key.

**The documents that state the key** (`PRD.md`, `README.md`). The PRD's preview prose and both of its `q`
rows in the binding list, and the README's pane paragraph (which now names the overlay as the exception
rather than claiming the whole screen) and overlay paragraph.

**The pty walkthrough's `q` step** (`docs/plans/completed/gitpr-mvp/artifacts/pty-walkthrough.sh`). It
asserted `q` gives the terminal back from the overlay, which is now false; the step drives `p,q,ctrl-c` at
60x14 and checks the list is repainted and the terminal is only given back on `ctrl-c`, and keeps the pane
scenario as it was. It is a runnable check, so it was corrected rather than left describing the old key.

## Design decisions

**Read by layout, not by focus.** Both layouts hold the keys the same way, so the branch is on
`m.mode == modePreview` — what is drawn — rather than on which region has the keyboard. The difference the
key is sensitive to is whether the list is on the screen to be given back.

**The pane keeps `q` as leave.** Its rationale survives unchanged: the pane is one `p` away, the marks are
on disk, and the list the reviewer would be leaving is visible while they press it. Making the two layouts
agree would have meant either a `q` that quits on the overlay or a `q` that closes in the pane; the first
is the bug, and the second takes the settled meaning off the screen where the reviewer can see what they
are leaving.

**One meaning per screen, named by the bar.** `q` now means two things in the program, which the previous
wording of both documents argued against. What carries it is the rule this screen already follows for
`enter` and `ctrl-d`: the shortcut bar names what each key means while its screen is up, so no meaning
travels with a keystroke alone.

## Validation

- `mise run check` (gofmt, `go vet ./...`, `go test ./...`) — clean.
- `TestQClosesTheOverlayAndPInertsOnTheOverlay` (`internal/tui/overlay_internal_test.go`) replaced the test
  that asserted the old behaviour: `q` leaves mode `modeFiles`, does not quit, repaints the list's bar,
  keeps the scroll, and returns to the same offset on `p`; `ctrl-c` still quits from the overlay. The bar
  assertions in that file and in `TestTheOverlayBarNamesAndFitsTheKeysThatCloseIt` now look for
  `esc enter q back` and no longer for `q quit`.
- `TestQQuitsFromThePaneWhereTheListIsStill` (`previewfocus_internal_test.go`, renamed from
  `…AsItDoesEverywhere`) still asserts `q` quits from the pane, which is the half of the contract the
  change deliberately leaves alone.
- The pty walkthrough against a binary built from this branch (`mise run build`).

## Known limitations

- The bar's exit group is now three keys wide. At the narrowest width the overlay allows it is still one
  group and the band fit was re-checked by the existing per-width test rather than assumed.

## Open questions

- None open. If the pane's `q` later turns into a close as well, `esc` becomes the only way out of the
  preview column on a wide terminal, and the bar for that screen has to say so.
