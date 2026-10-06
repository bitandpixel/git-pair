# feat-tui-mobile-scroll

## Summary

The wheel scrolls the preview, and the session asks for the wheel back after a handoff.
`git-pair.mouse` turns the reporting off.

Asking for the wheel costs nothing that the session had before. tmux hands the wheel to whatever is
on the alternate screen — where the session lives — so a program that never asked for the mouse was
not keeping the scrollback, it was dropping the events. Measured below.

## What changed

- `internal/tui/mouse.go` (new): `ResolveMouse` reads `git-pair.mouse` with git's own boolean
  vocabulary and says so when the value is not a boolean; `handleWheel` turns one notch into three
  rows of the diff; `enableWheel`/`wheelBack` ask bubbletea to report the wheel again after a child
  has held the terminal.
- `internal/tui/tui.go`: `tea.WithMouseCellMotion()` when the setting is on, a `tea.MouseMsg` case in
  `handle`, and `wheelBack` on both exits from the `externalDoneMsg` branch — including the one that
  returns early when the child failed.
- `internal/tui/session.go`, `internal/cli/review.go`: `Options.Mouse`, resolved from the repository
  when the session opens.
- `README.md`: the wheel in the Configuration section, with the two facts about tmux and the editor
  that make the setting look like it did nothing; and one notch in the preview's paragraph.

## Design decisions

**The wheel belongs to the diff, wherever the pointer is standing** — over the list column, over the
divider, over the whole-screen overlay. The pane is what a reviewer scrolls while reading, and a
pointer that has to be held over a column to move it is a second thing to aim at. The alternative —
routing by the pointer's column, or by `m.focus` — is the open question below.

**One notch is one event.** A wheel notch reaches the program as a press inside tmux and as a press
plus a release on terminals that report both, so only the press counts. Reading both would move six
rows where the reviewer rolled three.

**Cell motion (1002) and SGR coordinates (1006), which is what `WithMouseCellMotion` sends, and
deliberately not 1003.** Nothing in this screen is a hover target, and any-motion reporting gives up
the pointer for nothing. Nothing else is read from the mouse either: a click marks nothing, so a
reviewer can rest a finger on the button without wondering what the screen is about to do.

**The step is three rows**, the distance a notch moves a pager. The reviewer who wants half a page
has `d`/`u` and `ctrl-d`/`ctrl-u`, which keep a line of context across the break and clamp at both
ends through the same `scrollPreview` the wheel now uses.

**On by default, with a real opt-out.** `git config git-pair.mouse false` leaves the wheel alone —
including the request for reporting, so the terminal keeps its own click-drag selection. A setting
that only skipped the handling while still grabbing the pointer would take the cost and give back
nothing.

**The handoff is the interesting half.** `tea.ExecProcess` releases the terminal for the child, and
bubbletea's `ReleaseTerminal` → `restoreTerminalState` disables mouse reporting (`tty.go:45`), which
is what lets vim own it. Its `RestoreTerminal` (`tea.go:885`) puts bracketed paste and focus
reporting back and leaves mouse reporting off — so without `wheelBack`, the first trip into vim or
the difftool would take the wheel away for the rest of the session.

## Validation

Unit tests (`internal/tui/mouse_internal_test.go`): one notch moves three rows and back; both ends
clamp; the setting decides it; press plus release is one notch; an empty pane and the span picker do
not move; a handoff asks for the wheel back and a session with the setting off does not; and
`ResolveMouse` over git's boolean vocabulary plus an unusable value. `go test ./...` passes, and
`gofmt`/`go vet` are clean.

End to end inside tmux 3.7c, with the wheel injected to a real client as SGR mouse input, and the
pane read back with `capture-pane`:

| case | result |
| --- | --- |
| six notches over the diff | last visible row went from `+added 0040` to `+added 0058` — 18 rows, six × three |
| `z` overlay, six notches | scrolls the same way |
| `git-pair.mouse false` | `mouse_any=0` in the pane, six notches changed nothing |
| while `$GIT_EDITOR` held the terminal | `mouse_any=0`: the modes were released, so the child could take them |
| after the editor exited | `mouse_any=1` and six notches scrolled 18 rows again — the `wheelBack` path |

The same probe is what produced the tmux facts in the README: the shipped root binding is
`WheelUpPane → if-shell -F "#{||:#{alternate_on},#{pane_in_mode},#{mouse_any_flag}}" { send-keys -M }
{ copy-mode -e }`, an alt-screen pane that asked for 1002/1006 received
`\E[<64;10;10M`, and an alt-screen pane that asked for nothing received nothing at all.

## Known limitations

- **Inside vim, the wheel is vim's decision.** git-pair releases the mouse before the child starts,
  and vim scrolls on the wheel only with `set mouse=a`. vim 9.1 on this machine runs with `mouse=` —
  nothing in `~/.vimrc` sets it and `/usr/share/vim/vim91/defaults.vim` does not either — so the
  handoff's scrolling is a vim setting, not something this change can finish.
- **`set -g mouse on` is required under tmux**, and the probe did not exercise that gate: it wrote
  into the client's input stream, which bypasses the option. That half of the README's claim is
  tmux's documented behavior rather than something measured here.
- A value outside git's boolean vocabulary keeps the default and puts a note on the status line, so
  a reviewer who typed `diabled` hears that it did nothing. The note is a note, not a refusal.
- Trackpads were not verified: two-finger scroll usually arrives as wheel events, and momentum
  scrolling would move three rows per event with no throttle.
- Only the wheel-up and wheel-down buttons are read. Horizontal wheels do nothing.

## Open questions

- Should the pointer's column decide which region scrolls, the way a mouse usually works, rather than
  the diff always winning? The list keeps `j`/`k` and the page keys either way.
- Should a click move the keyboard focus to the column it landed in, once the wheel has proven
  itself? It is left out here so that no mouse event can change review state.
