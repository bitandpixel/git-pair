# feat-preview-you-marks

The header could say `· you edited it` while the rows stayed the committed file, so the lines the reviewer had
just typed — the only lines on that screen they had written — were nowhere on it. They are on it now, at the
place each one lands, marked as the reviewer's.

```text
fresh.go  3 lines  ·  you edited it  +3 −1
  1 package main
  2
  3 ×func Fresh() {}                      ← you
    +func Fresh() { return nil }          ← you
    +// reviewer: why?                    ← you
```

The plan is `docs/plans/preview-you-marks/plan.md`; the ask came from reading the pane with an edit in it.

## Summary

**The text pane splices the reviewer's uncommitted lines into the file's own rows** — git's `-` line at the
number that line has in the file under review, then the `+` lines that replaced it, unnumbered, each ending in
`← you`. This covers a file the span created and a file it moved unchanged, which are the two rows the pane
reads as text.

**The header's note counts what it admits to**: `you edited it  +3 −1`, git's numbers for the reviewer's own
section, not the span's.

**The diff pane draws the reviewer's rows into the author's patch.** A line the reviewer deleted is drawn where
the author's patch shows that line, rather than once as the author's addition and once as the reviewer's
deletion, and a line the reviewer added follows the line it sits after. Each line the reviewer changed ends in
`← you`, and nothing else on the screen does. The counts went to the header, beside the file's own —
`main.go  +2 −0  ·  you edited it  +1 −1` — which is where the pane puts counts anyway, and which is why the
pane needs no caption and no section of its own.

**The header's counts sit in one slot.** A document and the thread heading used to read `path · 21 lines`;
they now read `path  21 lines`, the place a diff's `+N −M` sits. One kind of answer, one place.

**On a pane of one file, git's rows about which file the patch is about come out, and your rows take the pane's
colours.** `diff --git`, `index`, `---`/`+++` and the mode and rename lines are gone, because the header has
named the file twice over; `@@` stays, being the only row that says which lines of the file are not on show.
Your additions are blue, a line you deleted that the span added is purple and leads with `×` where git drew
`-`, and a line you deleted that predates the span is amber. A pane over a directory, or the changeset box,
keeps git's colours and the marker alone — its numbers belong to several files, and the arithmetic that tells
those two deletions apart needs them not to.

**A directory's pane keeps git's two sections.** There the numbers on the screen belong to no one file, so the
reviewer's rows stay a run above the author's, with git's chrome and their own `@@` headers untouched.

**A historical span still fetches no working patch at all.**

## What changed

**`editedDocRows` is the merge** (`internal/tui/preview.go`). It takes the file's text and the reviewer's
patch for the same path and returns the rows the pane draws: file rows in order, with each run of the
reviewer's lines spliced at the position its numbers give it. Placement uses `lineNumbers`, the arithmetic
that already numbers the diff pane's rows from git's `@@` headers, so nothing compares file content and no
new hunk parsing arrives. git's metadata — `diff --git`, `index`, `---`/`+++`, `@@` — is left out, because
this is the pane that reads a file as a file and patch chrome in it is what
`TestTheTextPaneShowsTheFileRatherThanItsPatch` exists against.

**`laidOut.marked` is a marked line's rows** (`internal/tui/preview.go`): the same column `line` lays out,
narrowed by the marker's width, with `← you` on the line's last row. Narrowing is what keeps the frame the
width of the terminal: a marker appended after the wrap would let the terminal wrap for us and shift every
row under the break. `previewRow.line` keeps git's text, so the search still looks inside what the reviewer
wrote and never at the marker.

**The pane calls it in one place** (`internal/tui/tui.go`, `previewContent`). That function is already the
only assembler of the pane's body, so the search, the paging, `n`, the match count and the `z` overlay all
count the marked rows because they all came through here. The working patch this needs was already fetched —
`ensurePreview` has asked for `patchWorking` on a content pane over a live span all along, and its only
reader was the header note.

**`patchRows` is the diff body, with the marking as an argument** (`internal/tui/preview.go`). `previewBody`
and `previewEditsBody` are the two calls of it: the second marks each `+`/`-` row and leaves the context rows,
which are the file's rather than anyone's. It marks only rows git gave a line number, because the metadata rows
above the first hunk — `--- a/path`, `+++ b/path` — carry a sign without being a line of anything, and a marker
beside the old path says the reviewer deleted it. `previewRows` puts the reviewer's section first, then the
author's span, and tells each half which row of the pane its own first row is so the search's `n` still lands.

**The header's count lost its separator** (`internal/tui/tui.go`, `previewTitle`). `path · 21 lines` became
`path  21 lines`, which is where `path  +2 −1` already sat.

**Tests** (`internal/tui/preview_internal_test.go`, `change_internal_test.go`). The merge is tested with no
model and no terminal: an edit lands where it lands, in git's order, with the removed line before the lines
that replaced it; a removed
line stands in place of the file's row rather than beside it, and is the only row carrying that number; added
lines carry none; two hunks in one file both land and each of the reviewer's four lines is marked; an empty
patch is byte-identical to `docRows`; a hunk past a capped file adds nothing; no marked row exceeds the
column; and `/you` finds nothing while `/nil` finds the reviewer's own line. The diff pane's section is tested
the same way: it paints above the author's span, it carries two markers for two changed lines and none on the
context line between them, and your lines are on the first screen while paging still reaches the span below
them. Then the pane over the real
fixture: the reviewer edits `fresh.go` without committing and the screen carries the three lines, four
markers and the counted note, and over a historical span it carries none of that while the working copy sits
changed under it. The count's slot is pinned by name and number together (`fresh.go  3 lines`), because
"contains 3 lines" passes whether or not the count sits where the counts go.

**`TestAFileRowAndTheDocumentOfTheSamePathAreDifferentPanes` moved with the behaviour**
(`internal/tui/change_internal_test.go`). It asserted that a file row shows none of the reviewer's text. The
property it guards — the file row is the span's file and the box's About row is the document on disk, from
two caches — is unchanged, and is now pinned the other way round: the file row shows the reviewer's line
*as an edit* (`-# Changeset` and the marker), and the About row shows the same text as the document, with no
marker on it because all of it is the reviewer's.

**`mergeRows` is the merge** (`internal/tui/preview.go`). It walks the author's rows and the reviewer's, both
numbered against the same file — the author's head and the reviewer's base — and drops the author's row where the
reviewer deleted that line, drops the reviewer's context row where the author already shows the line, and puts
the reviewer's additions after the lines they follow. The sort that reassembles the body is stable and keyed on
that number, and within one patch the numbers only ever increase, which is why merging two patches cannot
reorder either of them.

**`rowPositions` replaced `lineNumbers`** (`internal/tui/preview.go`). The pane already read one number per row
off git's `@@` headers; a merge needs two — the number git printed for the row, which is what the gutter shows,
and the line of the head file the row sits at, which differ for a row that is not a line of that file at all.
The pane that reads a file as text reads its placements off the same walk now, which also corrects a context
row's number there: it was the working copy's, which drifts ahead of the file's own as soon as the reviewer adds
a line.

**The documents** (`PRD.md`, `README.md`) — the paragraphs on what the text pane shows said the reviewer's
edits were not in it. They say what is in it now, and why the kept line comes before the line it replaced.

**The pty walkthrough** (`scripts/gates/pty-walkthrough.sh`) rewrites the changeset's `ABOUT.md` from the
working copy, opens the session, walks one row down onto the file's row, and expects the typed line, the
line it replaced, the marker on them, the span's own text still on show, and no `diff --git`.

## Design decisions

**git's order, reordered by nothing.** The reviewer's lines arrive `-` before `+`, the way git wrote them.
The pane's one liberty is placement: it moves a line to where it belongs in the file. Reordering them as well
would make the pane a second account of the change rather than the same patch `d` and `git pair diff` show.

**An added line is numbered in no file.** It is not in the file at the span's head, which is the file these
numbers belong to, and the working copy's numbering beside it would be two numberings in one gutter with one
of them a lie. The removed line, by contrast, is a line of that file and keeps its number.

**The marker is the pane's word, in the pane's dim, and not a colour.** Colour is git's here. The colour
argument would also be ambiguous in the other direction: in a pane that reads a file as a file, *every*
marked line is the reviewer's, which is precisely the fact to state rather than leave to a palette.

**The reviewer's own section leads the diff pane.** It is the section they came to check, and on a diff longer
than the pane a section at the bottom is a section below the fold. The order the author's rows and the
reviewer's rows paint in says nothing about the order they were written in or reviewed in — the two sections
are measured against two different revisions, which is what the marker and the header's note are for.

**The marker is on the row, and there is no caption at all.** A caption names rows that are on screen with it,
and a pane scrolls: halfway down a diff, the reviewer would have to remember who the unmarked green belonged
to. The same reason gives the text pane no caption either — the fact belongs on the row it is about — so the
section's counts went to the header instead, where the pane puts counts anyway. And the mark is put on
`head..working` rows only: the pane's one `you`-making claim, so it is made where the evidence is and nowhere
else.

**The colours and the `×` come from arithmetic on two diffs' line numbers.** The span's `+` rows and the
reviewer's `-` rows are both numbered against the head file, so "the line the reviewer deleted is one the span
added" is an intersection — no comparison of content, and no claim about what two pieces of text have in
common. Git draws the same `-` for that and for deleting a line that predates the span, and they are not the
same thing: one undoes reviewed work. `×` is one cell, so the sign column and the narrow-terminal rule are
untouched, and git's bytes stay on the row as what it is a line of — a search matches `-gone` on a row the pane
painted with `×`.

**The pane asks the model what it is looking at, not the patch.** Whether a pane is one file's comes from the
session's list of files, which is what put the row there. The shape of the patch cannot answer it: a directory
holding one file has one `diff --git` in its patch, and a rule that counted them would strip the chrome that
names the file from exactly the pane that needs it.

**Placement is arithmetic on git's own numbers.** The merge moves lines; it never compares the file to
anything to work out what changed. That keeps PRD §3's restriction intact — the pane remains a renderer, and
`Enter`/`d` remain where interpreting belongs.

**The author's `@@` header stays above rows that are no longer the author's.** Its counts then describe the
author's rows and not the body under them. That is the cost of a merge, and the alternative is worse: git
printed no header for the shape the pane draws, and writing one would be the pane claiming a hunk it never saw.
The reviewer's own headers are dropped on a merged pane for the same reason — two headers over one run of the
file would read as two hunks.

**An added line is numbered in no file, in either pane.** It is not a line of the file at the span's head, and a
working copy's number beside it would be a second numbering in one gutter with one of them a lie.

**Where it cannot place, it says nothing rather than guessing.** A capped file holds less than the patch
describes: a run that replaces a line past the cut is not drawn, and the header note is what still tells the
reviewer edits exist. A column too narrow for text and marker together falls back to the unmarked file, which
is the readable half.

## Validation

- `go test ./internal/tui/` green, and `mise run check` green — gofmt clean, `go vet ./...` clean, the whole
  suite. The merge is tested on its own, with no model and no terminal: a deleted line takes the author's row's
  place and the pane says that line once; an edit outside every hunk of the author's stands where its number
  puts it, with the gutter's jump as the only signal; a directory's pane keeps the chrome, the two sections and
  git's `-` on every deletion.
- `scripts/gates/pty-walkthrough.sh` — `PTY: all checks passed`, against a binary built from this branch, with
  the two scenarios: the text pane drawing the reviewer's edit into the file with the mark on it and the line it
  replaced led by `×`, and the diff pane on the file the span modifies marking the row the reviewer typed while
  a context line stays unmarked, keeping `@@`, and counting the reviewer's typing in the header. What the chrome
  strip takes away is the Go test's claim rather than the walkthrough's: the window it captures includes the
  directory pane the cursor walked through, and a directory's patch is allowed to say which file it is about.
- Colour is checked where a terminal is: the walkthrough compares the colour codes the reviewer's row and the
  author's row arrive in, and requires them to differ. It does not name a particular escape sequence, because
  lipgloss drops colour when it decides there is no terminal to write to — which is what a Go test is — and
  picks 256-colour or 24-bit by what the terminal advertises.
- `mise run gates` runs gofmt, `go vet`, the suite, `e2e-29.sh` and the walkthrough in one pass over the tree.
- The pane read by eye at 140 columns on the fixture with the reviewer's edit in the working copy:
  `you edited it  +3 −1` above `-func Fresh() {}` and the two `+` lines, each ending in `← you`, and the
  file's own rows around them.
- The cases that are the tests' rather than by eye: the unedited file (`TestTheTextPaneSaysWhenTheReviewer
  EditedTheFile`, `TestEditedRowsWithNoEditsAreTheFileAlone`) and the historical span
  (`TestTheTextPaneMarksNothingOverAHistoricalSpan`).
- One thing the walkthrough cannot check and the Go test can: which section is on top. The capture holds
  repaints, and the frame that arrives first is the span without the working patch, so `grep` over it says
  nothing about screen order. `TestPreviewMarksYourRowsAndDropsWhatNamesTheFile` asserts on a reconstructed
  `View()`, where the order is the pixels the reviewer gets.

## Known limitations

- A reviewer's *submitted* edits — lines they changed in an earlier review submission, now inside the file at
  the span's head — are still not distinguished. Marking those needs a review-relative diff for the path and
  a rule about which submission owns which line; the ask was the typing the pane currently refuses to show.
- A long run of edits carries `← you` on every row of it. Marking only the first row of a run is wrong for a
  scrolling pane, where the row above the window is the one that would have carried it.
- The reviewer's section is `head..working`, so what it holds is *whatever is not in the revision under
  review*. The marker reads that as "yours", which is git-pair's model — the tree under review is the
  reviewer's, and the author's work is committed before the handoff. In a tree where the author is still
  working, their unreviewed lines arrive in that section marked as the reviewer's, and the whole file can look
  like the reviewer wrote it. Nothing on the screen attributes an uncommitted line.
- The pane's body and the marker together need `4` columns beyond the gutter; below that the file draws
  unmarked and the header note is all the reviewer gets. At that width the file is barely readable anyway.
- The merge and the diff pane's `previewRows` are now two renderings of the same working patch, and the diff
  pane still prints the reviewer's rows twice: once in their own section, and once as the span's row that the
  reviewer has since deleted or left behind. They share `laidOut`, the marker and `patchRows`; what can drift
  is the placement rule, and the merge's own tests are what notice.
- The colours need a pane of one file, so they are off wherever the numbers are not all of one file: a
  directory, the changeset box, and any pane whose patch git assembled from more than one path. A reviewer
  looking at a whole changeset sees the marker and git's colours, which is the old answer.

## Open questions

- The tree still signs a file the span created with `+` whether or not the reviewer has edited it, while a
  file the span changed carries `~` and gains nothing from the reviewer's typing either. If the tree is ever
  to carry this fact, the place it belongs is beside that sign — and it is a question about the list, not
  this pane.
