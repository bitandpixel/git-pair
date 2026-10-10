# feat-external-edits-visible

## Summary

The tree lists what the span changed, which is what `git diff --name-status` answers. A change the reviewer
makes in the working tree to a file the changeset never touched is in no such diff, so it had no row: the file
a reviewer stopped reading and wrote a question into, and the file they created in order to write it, were
both invisible on the screen where they were made.

The pane could already show such a file's bytes — it prints the working tree's side of every file it is asked
about — but nothing could ask, because the row to select did not exist. This adds the row, and the two answers
a row of that kind needs: it is not part of what has been reviewed, and git will not diff a path it has never
tracked.

## What changed

- `internal/git/git.go`: `Repo.UntrackedPaths` — `git ls-files --others --exclude-standard -z`, the paths the
  working tree holds that are in no commit and no index entry, ignoring what git was told to ignore.
- `internal/git/dirtypaths_test.go`: `TestUntrackedPathsListsOnlyFilesGitHasNeverSeen` (a modified tracked
  file, a staged new file, an ignored file and a committed file are all excluded; a nested new file and a
  non-ASCII name are included) and
  `TestUntrackedPathsKeepsPathsRelativeToTheRepository`. The file's heading now covers both working-tree calls.
- `internal/tui/session.go`:
  - `File.OutsideSpan` and `File.Untracked` — the two facts a row of this kind carries.
  - `readWorkingTree` (which replaces `dirtyPaths`) reads both working-tree answers once per scan into
    `workingPaths{dirty, untracked}`.
  - `workingOnlyFiles` appends a row per dirty path the span did not list, sorted by path, marked `Dirty`.
  - `Count`, `Toggle`, `SetReviewedUnder` and `SaveMarks` exclude those rows; `Files` and `fileAt` say what
    the list holds.
  - `WorkingPatch` asks git the other question for an untracked path, and the `git diff` behind it is now
    `gitDiff`/`patch`/`gitDiffOut`, which is one invocation's arguments rather than two copies of the same
    call.
- `internal/tui/tree.go`: `branch.count` counts the span's files in `total`/`marked` and every file in
  `dirty`, so a directory of the reviewer's own files claims nothing about what has been read.
- `internal/tui/tui.go`:
  - `row.outside` and `row.untracked`, carried the way `row.dirty` is.
  - `buildRows` keeps `inSpan` the span's own map, so `d`/`Enter` on a thread the reviewer created is still
    the decision about a document, not about a file the span changed.
  - `dirGutter` prints an empty gutter for a directory with no file of the span under it.
  - `toggleMark` refuses such a file row, and a directory that holds only them.
  - `fileAction` and `openFile` take the untracked fact, so Enter opens a file git has never seen in the
    editor.
  - `previewNotice` says which empty case the pane is in (`nothing in the working tree to show`, `new files
    git has not been told about`), with `newFilesUnder` and `previewFileUntracked` as the two questions.
- Tests: seven new cases in `internal/tui/dirty_internal_test.go`, the Enter table in
  `internal/tui/change_internal_test.go`, and the working-tree scenario in
  `scripts/gates/pty-walkthrough.sh` extended with a tracked file the branch never touched and an untracked
  note. `tree_internal_test.go`'s fixture gained `notes/plan.md` — committed on the base, left alone by the
  changeset — which is what the Go tests had no way to write into.
- `README.md`, `PRD.md`: the tree's rule, and what the pane does about a file git has never seen.

## Design decisions

**The row is on the list; the review state is not.** The reviewed counter says how much of the span has been
read, and a file with no patch in the span has nothing to have read. Counting it would have made a changeset
whose review is finished read as `4 / 6`, with the two rows nobody can tick. So `Count`, the directory counts,
`Space`, and the mark store all treat these rows as not-there, while the tree treats them as entirely there:
same place in path order, same magenta name, same `✱`.

**One list with a flag, not two lists.** The tree orders by path, so a second list would be a second tree
under the first — two `internal/` rows and two `docs/` rows, and a reviewer sorting their own working tree
against the changeset's. `File.OutsideSpan` is one bool on the row the tree already flattens, and the four
places that mean "the span's files" say so.

**`git status` stays the answer, so ignored files stay off the list.** `DirtyPaths` is `status --porcelain
-uall`, which already excludes what `.gitignore` covers. Listing the reviewer's `node_modules` under a screen
about a review would be noise that looks like work.

**The untracked fact is asked separately, from `ls-files --others`.** `status` prints `??` for the same paths,
but reading it a second way meant either a second parse of the same output or a wider `DirtyPaths` than its
one caller needs. `ls-files --others --exclude-standard` is the canonical question — "what has git never been
told about" — keeps `DirtyPaths`'s tested contract alone, and is cheap.

**The pane asks git rather than reading the file.** `git diff <rev> -- <path>` compares only what git tracks,
so it prints nothing for an untracked path. The alternative was to read the bytes off disk and draw them,
which is the pane becoming a renderer PRD §3 refuses. `--no-index` against `/dev/null` is git's own way of
comparing an untracked path, and `/dev/null` is how git writes the empty side of any created file — the
header lines come out the same as a real add's, so the pane strips them the same way. Exit 1 is `--no-index`'s
"they differ", and is also the code for a path it cannot read, so the bytes decide between the two.

**Enter goes to the editor for such a file, and `d` keeps its meaning.** A file in no commit and no index
entry has no left side, so the difftool opens a window on nothing — the same reason a file the span added, and
a document the changeset invented, already go to the editor. A file outside the span that git *does* track
keeps the difftool: its comparison is the reviewer's own typing against the revision under review, which is
worth opening. `d` still means "ask git for the diff" on every row, the way it does on a span-added file
whose patch is all `+`.

**A directory's pane says why it is empty.** A pathspec reaches only tracked files, so the new files under a
directory have no patch to print and git prints none. Filling the section in by running one `--no-index` per
file would be git-pair assembling a multi-file diff out of parts; the rows beside the pane already name the
files, so the pane names the reason instead.

**Over a historical span none of this exists.** Both of the span's ends are commits, so the working tree is
not part of what is on screen — the rule that already drops the pane's working section and the tree's `✱`.

## Validation

- `gofmt -l internal/ cmd/`: nothing to format. `go vet ./internal/tui ./internal/git`: clean.
- `go test ./...`: the whole suite passes, including the new cases:
  - `TestAFileOnlyTheReviewerChangedGetsARow` — the row, its mark, its colour, no sign, and the directory
    above it wearing no review mark.
  - `TestAFileTheReviewerCreatedGetsARow` — the untracked row's facts and its Enter.
  - `TestTheCounterCountsTheSpanNotTheWorkingTree` — the counter unmoved, a directory of only the reviewer's
    files at `0`, and a directory the span changed still counting its own two with the reviewer's file inside
    it.
  - `TestMarkingADirectorySkipsTheReviewersOwnFiles` — one `Space` marks the span's two, leaves the reviewer's
    row `✱`, and keeps the directory's `✓`.
  - `TestSpaceRefusesAFileTheSpanNeverTouched` — both refusals, sticky, nothing marked.
  - `TestHistoryListsNoFilesOfTheReviewersOwn`.
  - `TestThePaneShowsAFileGitNeverTracked` — the new file's bytes and `+2 −0`, and the edited tracked file
    still `+1 −0` rather than the whole file.
  - `TestThePaneOfTheReviewersOwnNewFiles` — the directory's notice and the file's body.
  - `TestEnterOpensInEditorAFileWithNoOtherSide` — the Enter table with the two working-tree rows added.
- Two existing tests were moved rather than relaxed, both for the same reason: they asserted on the whole
  screen while the fixture's working tree held files. `TestThreadsHeadingCollapsesAndExpands` now looks at
  the changeset box (`boxOf`), because a thread the reviewer wrote is a tree row as well as a box row;
  `TestPreviewSaysWhenThereIsNothingToPreview` now names the file row it means, because the row the cursor
  starts on is a directory, and a directory holding new files has its own reason to have no diff.
- `bash scripts/gates/pty-walkthrough.sh`: all checks passed, including three the extended working-tree
  scenario now makes against a real terminal — `✱ elsewhere.go` (a tracked file the branch never touched,
  written into), `✱ reviewer.md` (a file git has never seen), and the counter still reading `0 / 4 reviewed`
  with both of them on screen. Its own fixture repository, because the state is one commit on the base that
  the changeset then leaves alone.

## Known limitations

- A directory whose only new content is untracked shows no diff and says `new files git has not been told
  about`. The files are readable one row down; the pane will not assemble their patches itself.
- Nothing here attributes an uncommitted line to the reviewer, and this change does not claim to. The rule
  stays what the pane already says: the tree under review is the reviewer's, and a tree holding the author's
  uncommitted work is outside this screen's model.
- The rows appear on a live span only. A file whose change is *staged* is a row like any other — `status`
  counts staged, unstaged and untracked alike — so a reviewer who stages their own edit before coming back
  still sees it.
- `Space` refusing is the whole of the interaction design for these rows: there is no key that says "I have
  read my own edit". See Open questions.

## Open questions

- Should `Space` mark such a file, in a counter of its own ("3 of your 4 edits looked at")? It is the obvious
  next keystroke and it is a different number from the reviewed counter, which is why it is not in this
  changeset: the first thing the screen has to get right is that these rows are not part of the span.
- Should the rows be grouped under a heading at the bottom of the tree instead of mixed into it by path? The
  argument for mixing is that a reviewer looks for `internal/tui/scratch.md` where `internal/tui/` is; the
  argument against is that a changeset of 40 files and 3 reviewer notes is then 3 rows of a different kind
  inside the review. Mixed is the simpler claim, so that is what shipped.
- Should a directory of new files get its patches after all, one `--no-index` per file, appended below git's
  own section? It is ~15 lines in `WorkingPatch` and makes the directory pane complete. Left out because it
  is git-pair assembling a diff rather than printing one, and the rows already say what is there.
