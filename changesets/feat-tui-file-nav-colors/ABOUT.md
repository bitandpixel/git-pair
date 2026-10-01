# feat-tui-file-nav-colors

## Summary

The file tree already said what the span did to each file — `+` created, `-` deleted, `~` moved, nothing for
a modification — but it said it in faint grey after the name, at the end of a path, where nobody looks. The
signs now wear colour, so the exceptions are findable in one glance at a screenful of paths.

## What changed

- `internal/tui/tui.go`: three new styles (`styleSignAdded`, `styleSignDeleted`, `styleSignMoved`) and
  `signStyle(Change)`, which is the one place that maps a change to a colour. `rowText` renders the sign
  through it instead of wrapping it in `styleDim`, and draws the name of a deleted file faint.
- `internal/tui/change_internal_test.go`: `TestEachChangeWearsItsOwnColour` — the colour each change is
  given, that no two share one, that a sign is coloured rather than faint, and that a modification gets no
  styling at all.
- `scripts/gates/pty-walkthrough.sh`: a second fixture whose span adds, deletes, moves and changes one file
  each, and a check on the raw capture that each sign reaches a real terminal in some foreground colour,
  that the three colours are distinct, that the deleted file's name arrives faint, and that the modified
  file's row arrives in none of it.
- `PRD.md`, `README.md`: the paragraph that said the character is dim.

## Design decisions

**The palette is git's, not a new one.** Green created, red deleted, blue moved — the colours the diff column
beside the tree is already painted in, so a reviewer reads the same fact twice and it looks like one answer.

**Green and red were already taken on this screen.** Green is the reviewed mark in the gutter and red is a
refusal. Both collisions are safe because of where they sit: a mark is the first cells of a row and a sign the
last, and a refusal is a line of the band rather than a character after a name. The alternative — a palette
with no shared colours — would have made the tree disagree with the diff next to it.

**Colour replaces faint on the sign rather than adding to it.** Faint colour is the weakest thing a terminal
can be asked to draw, and a coloured character among plain ones already reads as the annotation the PRD says
it is: a fact about the file, not part of its name.

**Only a deleted file's name takes colour** (faint, not red). It is the one row whose subject is not there to
read, so the row can say so before the reviewer presses `Enter`. A faint name anywhere else would read as a
claim about the review rather than about the span.

**No sign for a modification.** The PRD's rule stands: a modification is what a span usually does, so a sign
marks the exception. Adding `M` would colour every row on a typical screen and make the exceptions harder to
see, not easier.

**The unit test checks the choice, the gate checks the codes.** `go test` is a terminal lipgloss will not
colour for, so the Go test reads the styles (`GetForeground`) and the pty walkthrough checks the bytes that
reach a real one. This is how the pane's own reviewer-row colours are already covered.

## Validation

- `gofmt -l` over `internal/` and `cmd/`: nothing to format. `go vet ./...`: clean.
- `go test ./...`: the whole suite passes, including `TestTheRowPutsTheSignAfterTheName`, which strips the
  escapes and still sees `fresh.go +`, `gone.go -`, `pure.go ~` in the same places, and the new
  `TestEachChangeWearsItsOwnColour`.
- `bash scripts/gates/pty-walkthrough.sh`: all checks pass, including the new scenario. Its capture shows
  the four rows drawn as `\x1b[92m+`, `\x1b[91m-` over a `\x1b[2mgone.go`, `\x1b[94m~`, and `keep.go` in the
  terminal's own colour — the codes this harness's xterm-256color terminal was told.
- `mise run gates` (what CI runs: `check`, the CLI e2e, the pty walkthrough, the integrate gate): passes.

## Known limitations

The colours are lipgloss's to spell: this run advertises xterm-256color and gets `92` for ANSI 10, with
`91` and `94` beside it, and another terminal would be spelled differently — so the gate checks the shape of
that rather than the exact codes, the way the reviewer-row check already does. A terminal with no colour to
give gets the character alone, which is the same answer it got before and legible on its own.

The new faint name sits inside the selected row's reverse video the way the directory's dim arrow and a
reviewed file's green `✓` already do: a styled span ends the highlight where its own reset lands, so one row
of the tree is highlighted shorter than its neighbours. That is how the row drew before this change and is
not what this change is for; making the highlight survive a styled span is a change to how `line` applies
reverse to a whole row.

## Open questions

Nothing open. If the shared green turns out to read as the reviewed mark once a screen is full of marks, the
one-line change is `styleSignAdded`'s colour; nothing else depends on which green it is.

Would a terminal theme change these colors at all, or are they purely determined by git diff? or are they just hardcoded to match git diff?

## Answered feedback

**The three colours are hardcoded, and they are indices rather than hues, so the theme still paints them.**
`tui.go` names palette slots — 10, 9, 12 — and nothing reads a colour out of git at runtime.

What reaches the terminal in this fixture's capture is `92`, `91` and `94`: the ANSI-16 codes for those three
slots. git's own diff, in the column beside them, sends `32`, `36` and `1`. Both are indices into the same
table, so a theme that paints green olive paints the diff body and the sign together and the two cannot drift
apart. `COLORTERM=truecolor` was run against the same fixture to check the other half of the question: lipgloss
still writes `92`, not `38;2;r;g;b`, so a wide-gamut terminal does not pin our colour to a hue of its own.

Where that alignment with git is exact and where it is not:

- **Hue family: the same.** Green for created, red for deleted, blue for moved, which is what git's defaults
  say for the same three facts.
- **Index: not the same.** git's added green is index 2 (`32`); ours is the bright slot, index 10 (`92`). The
  reason is inside the app rather than in git: index 10 is already this screen's green, the reviewed `✓`, and
  index 9 its red, the refusal. A row with one green in the gutter and a different green for its sign would
  have been the worse answer. On a theme that tells the two greens apart, the sign and the diff body are two
  greens.
- **git's palette is configurable and ours is not.** `color.diff.new = blue` moves the pane and leaves the tree
  in git's default hue family. Deriving from `git config` was not taken up: that syntax carries `bold`, `ul`,
  8-, 16-, 256- and RGB-colours and the `auto` decision with them, and PRD §3's refusal to read git's colour
  machinery into the pane points the other way.

**The remaining theme is the one with no colours.** `NO_COLOR`, `TERM=dumb`, or a terminal that advertises
nothing hands termenv an Ascii profile, the escapes never reach the terminal, and the character is the whole
answer — which is what every terminal got before this change.
