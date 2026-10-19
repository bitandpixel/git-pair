# Discussion

## What spends a count that is still being typed

From review `266c182`, written into ABOUT.md under Known limitations:

> Does any interaction that isn't a digit after a digit clear the prefix?

No: everything that is not a digit spends it, and the two exceptions are the clock and the terminal. The
rule the code is built to is that a count belongs to the next thing the reviewer does to the screen, and to
nothing after that.

### Spent by every key the region holding the keys reads

Each of the three key handlers asks `countPrefix` for the number before it looks at its own key table:
`handleKey` for the list column, `handleDiffKey` for the pane and the overlay, `handleSpanKey` and
`handleListKey` for the picker's columns and the drill. So a count dies at the first non-digit keystroke the
region sees, including:

- a key the region does nothing with at all (`t`, `x`, and the rest of the unbound letters), because the
  region read the key even though its switch had no case for it;
- a shifted digit (`%`, `&`), a paste arriving as one multi-rune message, `backspace`, `ctrl-c`;
- the keys that move the keys — `tab`, `shift-tab`, `f`, `p`, `z`, `a`, `V` — which is why `5 tab j` moves
  one row rather than five in the region the reviewer arrived in (`TestACountDoesNotFollowTheReviewerOntoAnotherScreen`
  pins `tab`, `esc`, `z`, `a` and `V`);
- `s`, and the `/` that opens either text field.

### Spent by the wheel, since this round

`handleWheel` clears the count before its own guards, so a notch spends it whether or not there was a diff
on screen to move, and takes its own three rows rather than `count × 3`. Two reasons: a notch rolled between
`10` and `j` is the reviewer changing their mind about the distance, and a count left standing across an
interaction the reviewer performed is the one case where the number can surprise them. The wheel reports
motion only under a held button, so a pointer dragged across the window is not an event and cannot be the
thing that clears it. `TestTheWheelSpendsACountWithoutMultiplyingIt` pins both halves, with the clear
removed as the mutation that fails it.

### Not spent by anything the reviewer did not type

Left standing — deliberately:

- the periodic drift check (`driftCheckMsg`, `driftMsg`), a note's expiry tick (`notifExpireMsg`), a window
  resize (`tea.WindowSizeMsg`), and a patch or document arriving from git (`previewMsg`, `contentMsg`,
  `docMsg`). The drift check runs on a timer, so spending here would eat a count typed a row at a time;
- the keys read by a screen that takes typed text: the diff's `/` field (`handleSearchKey`, dispatched
  before the count read) and the drill's filter (its `p.filtering` branch returns before it), and the submit
  prompt (`handleSubmitKey`, dispatched before `handleKey`'s read). None of the three can be open with a
  count pending, because the key that opens each — `/`, `s` — is read by a region and spends it first: `5 /`
  opens the field with nothing in it and `5 s` enters the prompt with nothing pending.

`TestDigitsTypedIntoTheSearchFieldStayText` and `TestDigitsTypedIntoTheDrillFilterStayText` hold the second
half: digits in a field are the term, and the `j` after `esc` is one row.
