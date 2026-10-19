# A renamed path is two changed paths

## Summary

Every list git-pair builds from a diff now names each path whose content or presence differs between the
span's ends. `git diff` finds renames by default, so one commit that drops a file and writes a similar one is
reported as one path moving, and the path that left appears in no list. That list was the review's file
report, the changed-path list a refused `git pair diff` prints, the tree on the review screen, and the file
list behind the parent-movement note. `internal/git` has one way left to turn a diff into a list, and it runs
`--no-renames`.

The rule each caller was asked was "at which paths does this differ". None of them was asking what a diff
would show. The previous changeset applied this to the drift and credit rule; this one applies it to the
surfaces a reader reads.

## What becomes different

- `git pair review submit` reports, and its `--json` carries, both halves of a pair git folded. A reviewer
  who rewrote a note under a new name submitted a deletion, and the report said one file.
- `git pair change feedback --name-only` names the file the reviewer deleted.
- `git pair diff <path>` takes a path a move emptied. It used to refuse it with `"…" does not appear in
main...HEAD`, and the changed-path list in that refusal named only the file that arrived.
- The review screen's tree holds a row for the path a move left behind, signed `-`, beside the row for the
  path it arrived at with `~`. Before it had one row, for the file that arrived.
- The parent-movement note names the file the parent deleted that this branch also changes. Before, it could
  say `none of them touch files this branch changes` about a parent that removed this branch's file — a false
  all-clear about the two branches meeting.

## One example, measured

The reviewer's submission in `internal/cli/rename_halves_test.go` deletes `review-notes/round-1.md` and
writes `review-notes/round-2.md` with the same bytes. git calls that `R100 review-notes/round-1.md
review-notes/round-2.md`.

| Command                                                    | Before                    | After                          |
| ---------------------------------------------------------- | ------------------------- | ------------------------------ |
| `git pair review submit --approve --json` → `files`        | `review-notes/round-2.md` | both paths                     |
| `git pair change feedback --name-only`                     | `review-notes/round-2.md` | both paths                     |
| `git pair diff account.go` (span dropped it, wrote a twin) | exit 2, not in the span   | the deletion's patch           |
| review tree, `git mv src/pure.go src/moved/pure.go`        | `src/moved/pure.go ~`     | that row, plus `src/pure.go -` |

## What is deliberately not changing

- What sign a row wears still follows git's rename settings, the way the patch pane does. The list decides
  which rows exist; the sign is git's verdict on the row. So `~` stays, `MovedFrom` stays, an unchanged move
  still opens as a file rather than as a patch, and a clone with `diff.renames=false` still gets `-` and `+`
  about the pair — `TestWithoutRenameDetectionTheTreeSaysDeleteAndAdd` is unchanged and still passes.
- `diffKeys` is untouched: a file is still keyed by its post-image patch, so review marks recorded before this
  change keep their keys. A deleted path that only appears as a rename's origin gets no key, which means an
  unmarked row rather than a wrong one.
- Three callers only asked `len(names) == 0` — `git pair review reopen`'s `nothing has landed since …`, the
  empty-review note in `change feedback`, and `reviewops.Result.Empty()`. Folding cannot empty a non-empty
  diff, so none of their answers moved. They were migrated for one question and one helper, not because their
  answer was wrong. The same is true of the queue's landedness comparison: the case where folding could hide
  the difference needs a changeset directory whose only difference from its base is a file moving out of it,
  which no test here constructs.
- The drift and credit comparisons are as the previous changeset left them.

## How it was implemented

`internal/git/git.go` loses `DiffNames`, `PathsChanged` and `PathsChangedOutside` along with the
`noRenames`-parameterised core they shared. `ContentPathsChanged` and `ContentPathsChangedOutside` are the
only two readings left, and they always pass `--no-renames`. Deleting them rather than leaving them for the
next caller is the point of the change: a helper whose answer omits a path is the one a future caller reaches
for without knowing. The eight call sites moved to the remaining pair.

`changeStatuses` keeps asking git for rename verdicts, and now signs the origin too. When git reports
`R100 old new` it spends its one status line on the pair, so the row the list carries for `old` would have
had no sign; it is signed as the deletion it is. A copy (`C100`) is not: its source still holds its content,
and the list does not carry a row for it either.

## Where to look

- `internal/tui/session.go`, `scan` and `changeStatuses`: the two git reads now disagree about rename
  detection on purpose, and the comment says why. If that reasoning is wrong, the row/sign pair is where it
  shows.
- The one case where folding produced a wrong answer rather than a short one is
  `TestParentMovementNamesTheFileTheParentDeleted`. The other new tests pin content that was hidden; this one
  pins an all-clear that was false.
- The `signs` scenario in `scripts/gates/pty-walkthrough.sh` previously worked around the fold: its comment
  said the new file must not look like the one that went away, because the pair would otherwise be one `~`
  row. That is no longer needed for the list, and the comment now says what the fixture still claims.

## How we know it works

Five new tests, each confirmed to fail when `--no-renames` is removed from the helper and to pass with it:

- `internal/cli/rename_halves_test.go` — the review submission's file report and `change feedback`,
  `git pair diff` of a deleted path (and the shape of the refusal for a path that really is absent), and the
  parent-movement overlap.
- `internal/tui/change_internal_test.go` — `TestTheTreeListsThePathAMoveLeftBehind` and
  `TestTheTreeListsBothHalvesOfAChangeGitFoldsIntoOne`.

`internal/git/contentpaths_test.go` now asks git itself for the folded side of its contrast, since the helper
that answered that way is gone; it still fails if a future git stops folding. The pty walkthrough adds the row
assertion for the path a move left behind, and passes its 156 checks. `mise run check` and `mise run gates`
are green (212s), and the git-invocation ladder is unchanged: 27 invocations at one level, 46 at two, 55 at
three, 64 at four, against a ceiling of 51.
