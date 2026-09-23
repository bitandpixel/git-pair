# feat-ux-updates

Three changes to the review surface, all raised in the same pass over it: the screen can be asked for by
its short name, the diff can be asked for at the size of the whole terminal, and `Enter` on a file the
changeset created opens the file rather than a comparison that has one side missing.

## Summary

**`git pair review` did nothing but print its help.** The group answered `git pair review open`; the word
`open` was the only way in. `git pair review` with no subcommand now runs `review open` and takes the same
span flags, so `git pair review --unreviewed` is the command most reviewers mean.

**The diff had one shape, chosen by the window.** A wide terminal gets a column beside the list, a narrow
one gets the same diff over the whole screen, and the reviewer could not ask for the other one. `z` —
pressed where the diff already holds the keys — now takes the whole screen, and gives the list its column
back. The keys, the file and the place in the file stay put, so `z` twice is one reading gesture.

**`Enter` on a file the span added opened the difftool on it.** That comparison has nothing on its left
side: the tool shows the file with a `+` in front of every line, which is the file again and louder. `Enter`
now opens that file in the editor, which is the choice the screen already makes for a file it is reading as
text and for an `ABOUT.md` the changeset invented. `d` is unchanged, so the key for the patch is still there.

## What changed

**`review` is a group with a command of its own** (`internal/cli/review.go`). `newReviewCommand` registers
the span flags — the base family and the head family — and its `RunE` runs `runReviewOpen` when it is given
no arguments. A word that is not one of its commands reports `unknown review command "…"` as before; the
refusal moved out of `groupUsage` into `unknownGroupCommand` (`internal/cli/root.go`) so both spell it the
same way. `open` keeps its own command, its help, and its examples, because `review --help` reads better
with the verb in it.

**`runReviewOpen` and `openSession` take the whole invocation** (`internal/cli/review.go`). Both used to take
a subcommand and splice it into `` `git pair review %s` ``. The bare group would then have refused with
"`git pair review review` needs a terminal", so `openSession` now takes the command as the reviewer can type
it — `git pair review`, `git pair review open`, `git pair review reopen` — and quotes that back.

**`toggleFullScreen` is `z`** (`internal/tui/tui.go`). Over the pane it sets `modePreview`, the same state the
narrow terminal's `p` reaches, so the overlay is one region reached two ways rather than a second screen with
its own rules. Over the overlay it returns to the pane — `modeFiles` with the keys still on `focusPreview` —
where a pane fits, and refuses with the shortfall otherwise. Nothing else moves: the keys stay with the diff,
`previewPath` and `previewOffset` are untouched, and `prevFocus` was already the half of the list column that
handed them over, so `esc` still returns there.

**Two bars name `z`, each only where it works** (`helpTextFor`, `overlayHelp`). The pane's bar gained
`z full`. The overlay's gained `z pane` only when `previewShortfall()` is empty: on the terminal too narrow
for a column the key can only refuse, and the bar is not for naming keys that refuse.

**The row now carries the change, not its sign** (`row`, `buildRows`, `rowText`). `sign string` became
`change Change`, so one fact about the file drives both the character after its name and what `Enter` does
with it. `activateBy` asks `fileAction`, which returns `actionEdit` only for `ChangeAdded`.

**`openFile` is the one place the decision is made** (`internal/tui/tui.go`), reached from `activate` and from
`openPreview`, so `Enter` on the row and `Enter` in the pane showing that row cannot drift. It carries the
gate `openArtifact` already carried, because `Enter` is not a mutating key and so never passes through the one
in `handleKey`: over a historical span the editor is refused the way `e` refuses it, and an added file the
working tree no longer has goes to the difftool rather than to an empty buffer — the patch is where git still
has it. `artifactNote` became `editorNote` and takes a path and a name, since a file row now needs it too.

**Tests** (`internal/tui/*_test.go`). `TestZTakesThePaneToTheWholeScreenAndBack` scrolls a pane, presses `z`,
and checks the mode, the focus, the scroll position, the row at the top of the diff, that the frame is not
split, and that the bar says `z pane`; then `z` again and the same checks in the pane's shape.
`TestZHasNowhereToGoOnANarrowTerminal` pins the refusal and the absence of `z pane` from its bar.
`TestZIsNotAKeyOfTheListColumn` keeps `z` out of the list's keys. `TestEnterOnAnAddedFileGoesToTheEditor`
is the table over all five changes plus the handoff on the fixture's added file and the `d` that still opens
the difftool on it. `TestHistoricalSpanRefusesEnterOnAnAddedFile` pins the gate.
`TestThePaneSaysEnterOpensAnAddedFile` pins the bar. `TestArtifactNoteNamesTheReason` became
`TestEditorNoteNamesTheReason` with its function.

**The pty walkthrough** (`scripts/gates/pty-walkthrough.sh`) drives `p,z` at 140 columns and expects the
overlay's bar, then `p,z,z` and expects the list repainted with `z full`, then `p,z` at 60 columns and
expects the refusal naming the width. The first ends with `ctrl-c` because over the overlay `q` gives the
list back rather than quitting — that is the other scenario's subject.

**The documents** (`PRD.md`, `README.md`): §10.1 states the shorthand, and both binding tables gain a `z` row;
the README's command table, its quickstart, its list of commands that refuse without a terminal, the pane
paragraph, the overlay paragraph, and the paragraph on the two file rows that read as files.

## Design decisions

**`z` is read by the region that already holds the keys.** It is not a list-column key. `p` is how the list
asks for the diff; `z` is how the diff asks for more screen. That keeps every layout change off the list,
where a key that changes what the review is would have to be gated.

**Refusal over substitution.** `z` over the overlay on a narrow terminal does not close the diff to "give
back" a column that does not exist, and `Enter` on an added file over history does not quietly open the
difftool instead. Both say what the terminal or the span will not allow. The pane's bar says `z full`
always, because from the pane the whole screen always fits — the overlay's floor is lower than the pane's.

**Only `ChangeAdded` opens the editor.** `ReadsAsFile` also covers a rename whose bytes are unchanged, and
the pane keeps doing that: a two-line argument about a path is not a patch worth a pane. `Enter` is
different, because there the rename *is* the comparison and it is why the file is under review. An added
file has no other side at all, which is the same fact `artifactAction` acts on for `ABOUT.md` and threads.

**`d` keeps its meaning on every row.** The two keys were already different — `Enter` reads a document the
changeset invented, `d` refuses to diff it — and this widens that gap rather than closing it. The pane's bar
says `enter open` over an added file and `enter diff` over every other file, so the difference is on screen
before the key is pressed.

**`runReviewOpen` takes the invocation, not the subcommand.** The refusal that quotes a command nobody typed
reads like a bug in the tool. Passing the whole phrase also stops the message from lying if a third name for
the screen ever appears.

## Validation

- `mise run check` — gofmt clean, `go vet ./...` clean, `go test ./...` green.
- `mise run build`, then the pty walkthrough against the binary this branch installs —
  `PTY: all checks passed`, including the three new `z` scenarios.
- `bash scripts/gates/e2e-29.sh` — green.
- `git-pair review --help` read by eye: the group's flags, its examples, and `open` still listed.

## Known limitations

- The pane's bar is longer by the group `z full`, so `helpRows()` can budget a row more at some
  widths. That is the bar doing its job; `TestNeitherTheKeysNorANoteMoveTheRowArea` still pins that the
  three bars of one screen do not move the row area between them.
- `z` toggles between the two shapes only. It cannot turn the pane off — nothing can, and `previewOn` has
  been true since the session opens.
- The bare `git pair review` refuses without a terminal exactly as `review open` does, so an agent that
  typed the short form expecting help gets exit 2 and the pointer to `git pair diff` rather than the
  command list. `git pair review --help` is the help.

## Open questions

- None open. If a second full-screen gesture is ever wanted — the changeset box over the whole screen, say —
  the shape of `toggleFullScreen` is the one to copy, including the rule that the bar names the key only
  where it does something.
