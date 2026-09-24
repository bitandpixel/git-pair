# The reviewer's edits, marked where they land in the text pane

## Goal

A file the span created is read as its own text, not as a patch. When the reviewer edits that file and
does not commit it, none of their typing is on screen: the pane's header says `· you edited it` and the
rows stay the committed file. Observed against `internal/tui` with a working patch of `+2 −1` for
`fresh.go`:

```text
fresh.go  ·  3 lines  ·  you edited it
  1 package main
  2
  3 func Fresh() {}
```

The same edits on a file the pane reads as a diff are visible — `previewRows` appends a
`── you · uncommitted  +2 −1` section with git's own lines under the span's patch — so the gap belongs to
the text pane alone.

The goal is that the text pane shows the reviewer's uncommitted edits where they land in the file, in
git's own bytes, marked as the reviewer's:

```text
fresh.go  ·  3 lines  ·  you edited it  +2 −1
  1 package main
  2
  3 -func Fresh() {}                      ← you
    +func Fresh() { return nil }          ← you
    +// reviewer: why?                    ← you
```

The removed line keeps the file's number, because it is a line of the file the reviewer is reviewing. An
added line carries no number, because it is in no file the reviewer is reviewing — the working copy's
numbering would be a second, different numbering in one gutter. The reviewer's lines arrive in the order git
wrote them: the pane moves lines to where they belong and reorders nothing.

## Success criteria

- Over a live span, on a row the pane reads as text (`+` for a file the span created, `~` for a move it
  carried whole), with uncommitted edits to that path, the pane draws the file's own rows and, at the
  position each edit lands, git's line for it in git's order: the `-` line at the file's own number, then
  the `+` lines unnumbered, each with a dim `← you` at the end of the row.
- With nothing of the reviewer's in the working tree, the pane is what it is today, down to the absence of
  the note: an invented warning is how a reviewer stops believing the real ones.
- Over a historical span, nothing changes: the working tree is not one of that span's endpoints, and no
  working patch is fetched for it.
- The header's note gains git's counts for the reviewer's own section: `· you edited it  +2 −1`.
- The pane's search, its paging, `n`, the match count, the overlay (`z`) and the row-counting the note
  uses all count the marked rows, because they all go through the one function that assembles them.
- `mise run check`, `scripts/gates/e2e-29.sh` and `scripts/gates/pty-walkthrough.sh` pass; `PRD.md` and
  `README.md` move in the same commit as the behaviour they describe.

## Context

- `previewContent` (`internal/tui/tui.go:57`) is the kind for a file the span created or moved whole. Its
  rows come from `m.contents[path]`, which `Session.FileContent` (`internal/tui/session.go:701`) fills
  with `git show <span head>:<path>` — deliberately the committed file, not the working copy.
- The same pane already fetches the reviewer's own patch: `tui.go:2877-2890` requests `patchWorking`
  (`Session.WorkingPatch`, `session.go:605`, which is `git diff` from the span's head to the working tree)
  for a content pane over a live span. Today its only reader is `previewTitle` (`tui.go:3533`), which
  appends `· you edited it` when it has lines. The data is there; the drawing is not.
- `previewRows` (`internal/tui/preview.go:61`) is the diff pane's answer, and the reason the text pane's
  silence is an inconsistency rather than a rule: "Both sections are git's bytes, and git's bytes do not
  say who typed them … The caption is what keeps the reviewer's edits from reading as the author's."
- `lineNumbers` and `parseHunk` (`internal/tui/preview.go`) already read git's `@@` headers and count the
  `+`, `-` and context lines beneath them to number every patch line, stripping colours before
  classifying. The merge uses those numbers and adds no new hunk parsing.
- Tests that pin the shape being changed, all in `internal/tui`: `TestTheTextPaneShowsTheFileRatherThanItsPatch`
  (a clean added file shows no patch chrome), `TestTheTextPaneSaysWhenTheReviewerEditedTheFile` (the note,
  and its absence when nothing was edited), `TestAFileRowAndTheDocumentOfTheSamePathAreDifferentPanes`
  (the two caches).

## Constraints

- PRD §3 and the header comment of `preview.go`: the pane is git's output plus two things — the line
  number git put in the hunk header, and a break where a line is too wide. The merge moves git's lines to
  the position they belong at. It must not compare file content to work out what changed, and it must not
  fold, group, or decide what is worth reading.
- Colour is git's. The `← you` marker is the pane's own and is dim, the way the caption above the diff
  pane's section is dim; it does not repaint git's green and red.
- Geometry: a marker may not push a row past the column. Where a marked line is too wide for the column,
  the body narrows by the marker's width and the marker goes on the last row of the line.
- Nothing about a file row that the pane already gets right changes: `enter` still opens the editor for a
  file the span created, `d` still hands the patch to the difftool, `e` still refuses over history.

## Assumptions

- "The reviewer's edits" means edits in the working tree that are not in the span's head — the case the
  note already admits to. Lines a reviewer edited in an earlier *submitted* review and that are now inside
  the file at the span's head stay unmarked: distinguishing them needs a review-relative diff for the path
  and a rule about which submission owns which line, which is a second change.
- Every changed region of the working patch carries an `@@` header, so the numbers `lineNumbers` returns
  are enough to place each line. Where they are not (a patch git produced in a shape this does not read),
  the line is dropped rather than guessed at, and the header note still says the edits exist.
- The pane's two caches stay apart: the file row is the span's file, `ABOUT.md` and threads are the disk.

## Milestones

### M1 — the merge, as a function

Deliverables

- One function in `internal/tui` that takes the file's text and the reviewer's patch for the same file and
  returns the rows the pane draws, with git's lines spliced at their positions and the reviewer's rows
  marked.
- Unit tests over it, with no model and no terminal.

Tasks

- Add the merge beside `previewRows` in `preview.go`, reusing `lineNumbers` for placement and `laidOut`
  for geometry. Skip git's metadata lines (`diff --git`, `index`, `--- `, `+++ `, `@@`) — a text pane with
  patch chrome in it is the thing `TestTheTextPaneShowsTheFileRatherThanItsPatch` exists against.
- Give `laidOut.line` the marker (or wrap it), so a marked row reserves its width instead of overflowing.
- Test: an added line lands after the old line its hunk follows; a removed line takes the file's number
  and replaces the file's own row rather than appearing twice; two hunks in one file both land; a file
  with no patch is byte-identical to `docRows`; a hunk whose old side starts past the last shown line
  adds nothing; the `← you` marker is on the reviewer's rows and nowhere else.

Verification

- `go test ./internal/tui/` — the new tests and every existing pane test.

### M2 — the pane uses it

Deliverables

- `previewContent` for `previewKind == previewContent` draws the merged rows when the reviewer has
  uncommitted edits, and `docRows` alone when they have none.
- The header note carries the reviewer's counts.

Tasks

- Wire it at `tui.go:3394` (`reviewModel.previewContent`), which is the one place the pane's body rows are
  assembled and is already shared by the search, the paging, the note and the overlay.
- Keep the "(reading your edits…)"-style timing honest: the working patch arrives asynchronously, so the
  unmarked file is what is on screen while it is in flight, and the note appears with the patch.
- Tests in `internal/tui`: the screen of an added file with the reviewer's edits shows the marked rows and
  the counted note; without edits it does not; over a historical span it does not; the search finds a term
  that only exists in the reviewer's added line, and `n`/the match count agree with the rows drawn;
  `z` over the whole screen shows the same rows.

Verification

- `go test ./internal/tui/` and `mise run check`.
- The screen read by eye: `mise run build`, a scratch changeset with a new file, the reviewer edits it and
  does not commit, `git-pair-… review` on the file row, and again after `git pair change unready` so the
  edit is the only thing in the span.

### M3 — the documents and the replay

Deliverables

- `README.md` and `PRD.md` describe the marked rows in the text pane, and the review walkthrough drives it
  through a real pty.

Tasks

- `scripts/gates/pty-walkthrough.sh`: rewrite one line of the changeset's `ABOUT.md` in the working copy,
  open the session, walk one row down onto its row, and expect the typed line, the line it replaced, and the
  marker on them, with the file's own first line still drawn and no `diff --git`. Then the same pane on an
  untouched file it reads as text, expecting no marker. One line rather than a rewrite, because the whole
  edit has to fall inside the rows the pane has; the counted note is the Go test's at 140 columns, since at
  the walkthrough's 100 the path in front of it is long enough to clip the header.
- `README.md`: the pane's paragraph on a file the span created, and the key/column table. `PRD.md`: the
  section that says what the pane shows for a file the span created.

Verification

- `mise run gates` — check, `e2e-29.sh`, and `pty-walkthrough.sh` including the new scenario.

### M4 — the reviewer's own section, moved and marked (review feedback)

The first hand pass looked at the diff pane as well, and asked for the same treatment there: the reviewer's
uncommitted lines were named by a caption at the bottom of a patch they can lose by scrolling.

Deliverables

- `── you · uncommitted` leads the author's span in the diff pane, and each line the reviewer changed there
  carries `← you`.
- The header's count of a document or the thread heading sits in the slot a diff's `+N −M` sits in.

Tasks

- `previewRows`: the reviewer's section first, the author's below, with each half told which pane row its own
  first row is. `patchRows` takes the marking as an argument, so `previewEditsBody` marks `+`/`-` rows and
  leaves context rows — which belong to the file, not to the reviewer — alone.
- `previewTitle`: `path  21 lines` rather than `path  ·  21 lines`.
- Tests: the section paints above the span, two changed lines give two markers and the context line between
  them none, the first screen already holds your lines, paging still reaches the span below, and the count is
  pinned beside the name rather than anywhere in the line.
- `pty-walkthrough.sh`: the same walkthrough onto the file the span *modifies*, expecting the caption, the
  typed line, one marker, the author's span below, and the reviewer's line before it.

Verification

- `go test ./internal/tui/`, then `mise run gates`.

## Risks

- A long run of edits puts `← you` on every row of it. Marking only the first row of a run was the
  alternative and is wrong for a pane that scrolls: the first row of a run can be off screen while its
  rest is not. If the noise proves real in use, the fix is the marker's own rendering, not a rule about
  which rows deserve it.
- A capped file (`too long to read here`) shows fewer lines than the patch describes. Hunks that land
  beyond the cut are not drawn, and the note remains, which is the honest half-answer: the reviewer is
  told edits exist and sees the ones that fall inside what the pane can carry.
- `previewRows` for the diff pane and this merge are now two renderings of the reviewer's patch. They share
  `patchRows`/`laidOut` and the marker, so what can drift is the placement rule; the M1 tests are the
  ones that notice.

## Verification strategy

Static first (`gofmt`, `go vet`), then unit tests in `internal/tui` — the model can be driven with an
injected `workingFor`, which is how the note is tested today. Then the pty walkthrough, which is the only
layer that proves a keystroke repaints, then a hand pass over the real screen. The change is presentational
and reaches no state: the pane marks rows, and `space`, `s`, the refs and the markers on the list are
untouched, which is what `e2e-29.sh` keeps pinned.

## Audit History

| Date       | Audit | Summary |
| ---------- | ----- | ------- |
| —          | —     | Plan written before implementation. |
| 2026-09-24 | M1–M3 executed; M4 added from review feedback | The merge, the pane and the documents are in. The review pass looked at the diff pane too and asked for the reviewer's section there to lead and to be marked, which is M4; the count's slot moved with it. |
| 2026-09-24 | M4 executed | `previewEditsBody` marks `head..working` rows only — the author's span, the context lines and git's `---`/`+++` rows carry no mark — and the section paints above the span. `mise run gates` green, including the walkthrough's new diff-pane scenario. |
