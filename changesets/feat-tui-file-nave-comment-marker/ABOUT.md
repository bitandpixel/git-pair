# feat-tui-file-nave-comment-marker

## Summary

The tree already said what the span had done to each file — `+` created, `-` deleted, `~` moved, nothing for
a modification — and it said nothing about what the reviewer had done to it. A comment written into a file is
an uncommitted change, so it sits inside no span and earns no sign: the only way to find out that you had
already been writing in `main.ts` was to open it, or to land on it and read the pane's captioned section for
your own bytes. Every file row now says whether the working tree holds a change at that path, and a folded
directory says it for the rows it hides, which is the question a reviewer comes back to a screen to ask:
which of these have I already been into.

The nested-directory indent went with it. A directory spends two cells on its fold arrow and a file does not,
which put a nested directory's name two cells right of the files beside it. The arrow now comes out of the
indent rather than sitting after it, so at any depth below the top a directory and the files beside it name
themselves in one column.

## What changed

- `internal/git/git.go`: `DirtyPaths`, and `parseStatusZ` beside it. One `git status --porcelain -z
  --untracked-files=all` answers the question for every path at once; the parse handles the record that
  `--porcelain` writes for a rename or a copy, where the origin path follows the destination as a field of
  its own with no status in front of it.
- `internal/tui/session.go`: `File.Dirty`, set in `scan` from `dirtyPaths` — which asks git once per scan, and
  asks nothing at all over a historical span. `ErrNothingMoved` is the sentinel `r` answers with, so its
  caller can report that and still go and re-read the working tree.
- `internal/tui/tree.go`: `treeRow.dirty`, carried on a file row for the file and on a directory row for its
  subtree — set only while the directory is folded. `branch.count` returns the third number it needs for
  that, and keeps returning it for files a fold has hidden.
- `internal/tui/tui.go`: `row.dirty`; `nameStyle`, which is where the choice of a name's style now lives;
  `✱` in `fileGutter` and `dirGutter`; `styleDirtyName` and `styleDirtyMark`; `treeLead`, which is where the
  indent and the fold arrow are now arranged; and the `r` handler, which re-scans when no ref has moved.
- `internal/git/dirtypaths_test.go`: what `git status` really says for a staged edit, an unstaged edit, an
  untracked file, a staged rename (both of its paths) and a name git would quote, and that the paths stay
  relative to the repository under `status.relativePaths`.
- `internal/tui/dirty_internal_test.go`: the session's answer for dirty and clean files, that a mark and an
  edit are two answers on one row, that history has neither, the folded-directory readings with and without
  the count, the four readings of a file row, which style each row's name wears, that `r` picks up an edit
  made in another window, and one column per depth.
- `internal/tui/tree_internal_test.go`: the list as it renders, and the comment that described the arithmetic
  the arrow moved out of.
- `scripts/gates/pty-walkthrough.sh`: a fixture whose span changes one file and whose reviewer then writes
  in it, checking the mark and the folded directory's mark reach a real terminal, that the mark and the name
  arrive in colour, that the name also arrives bold, and that the file nobody wrote in arrives at neither.
- `PRD.md`, `README.md`: the indent sentence, the tree's marks, what `r` re-reads, and both screen examples.

## Design decisions

**The answer is git's, read once, and not a guess from file dates.** `Session.scan` already asks git for the
span's names, statuses and diff keys; this adds one more call to the same list-building pass and keeps the
tree's whole vocabulary git's. `File.Dirty` is set where `File.Change` is set, so the row cannot disagree with
the list above it, and a refresh, a span change and an editor handoff all re-read it for the same reason:
they all go through `scan`.

**`-z` is load-bearing, not tidiness.** The tree compares these paths against paths out of `diff
--name-status`, and the plain `git status` format prints paths relative to the process directory when
`status.relativePaths` is on — a repository with that setting would have quietly matched nothing. `-z` also
means git never quotes a name, so a file with a space or a non-ASCII letter in it is the same string on both
sides of the comparison.

**Staged, unstaged and untracked are one question.** The question the row answers is "has anything happened
here that the commit under review does not have", and a `git add` does not answer it. The three states are
also three different ways for a reviewer's own work to be sitting in the tree, and a mark that depended on
which one you happened to leave it in would need explaining every time.

**Magenta, and bold with it.** The pane beside the tree already paints the reviewer's own lines — magenta is
what it gives a line you deleted, in a family with the blue and amber it uses for the lines you added. The
tree spends no magenta of its own, so the colour arrives with one meaning already learned. Bold goes with it
because a colour is the one thing this terminal is not obliged to render, and unlike a sign this mark has no
character of its own in that case: for a file that is reviewed as well as written, the glyph is the tick and
the name is the only difference.

**The gutter keeps the review answer, and the name says whose change it is.** The two states are independent
and a row has to be able to say both, so the gutter stays the reviewed column — `✓`, `○`, `◐` — and `✱`
replaces only the marks that say "not read". A file that is reviewed *and* written into keeps its tick,
because the tick is the answer the counter is made of and losing it would move information out of the one
place that counts it. That combination is not a corner case: a mark is keyed on the file's diff within the
span, both of the span's ends are commits, and so an edit never invalidates a mark — comment-then-mark is the
ordinary sequence, and it is the row this change exists to make visible.

**`✱` is the empty mark with something on top of it, not a third state.** Marks stay marks: keyed per
`(path, diff key)`, persisted where they always were, unaffected by what the working tree is doing. Nothing
about a file's dirty state is stored, and toggling it changes nothing you can submit.

**Only a folded directory wears it.** Unfolded, the children are on screen saying it themselves, and a mark
repeated down a subtree is one more thing to look past to find the row that means it. Folded, the directory is
the row standing in for the rows it hides, so it carries the mark — and it keeps its `n/m` count, so the fold
loses nothing about what has been read. A subtree reviewed all the way down keeps its tick and names itself in
the reviewer's colour, the same way its files do.

**Nothing over a historical span.** A historical span's ends are both commits, so an uncommitted change is not
work inside it; the pane leaves its own working section out for the same reason, and the tree draws no gutter
there either. `dirtyPaths` returns nothing rather than filtering at render, so the session also does not ask
git a question it will throw the answer away for.

**`r` re-reads the working tree whether or not a ref moved.** `r` means "look again", and the refs were only
half of what it looked at. A reviewer who edits in another window sends this screen no message, so before this
there was no key that would show it. The answer about the refs is unchanged, wording included: the banner text
is still what `r` says when nothing has moved, and the drift tests still read it.

**The arrow moved into the indent rather than the files moving out of it.** A nested directory can give up two
cells and gain the column the files beside it are in; making the files give up two instead would have cost
every row in the tree two cells to fix an alignment only directories had. A top-level directory keeps its
column, because there is no indent there to spend the arrow on, and that row has nothing above it to line up
with. Each level is still four cells.

**A failed `git status` costs the marks and nothing else**, which is `changeStatuses`'s rule for the signs:
the list itself came from git already, and a preview that cannot be drawn is a line in the pane rather than a
problem the reviewer has to handle. The cost is that a failed read reads as "no edits anywhere", which is why
the read is one call over the whole tree rather than one per file — the failure is one failure, in the same
place the signs already depend on it.

## Validation

- `gofmt -l internal cmd`: nothing to format. `go vet ./...`: clean.
- `go test ./...`: the whole suite passes, including the new session, tree, render and refresh tests, the
  `git status` parsing tests against real git output, and `TestTheListAsItRenders`, which is where the indent
  change shows up.
- `bash scripts/gates/pty-walkthrough.sh`: all scenarios pass, including the new one: its capture shows the
  `✱` and the name of the written-in file reaching the terminal in foreground codes, the name arriving bold
  as well, the clean row's name arriving in neither, and `▸ ✱ src/` on the folded tree. It also passes
  inside `mise run gates`, which is the run that matters when the whole suite is competing for the CPU.
- `mise run gates` (what CI runs: `check`, the CLI e2e replay, the pty walkthrough, the integrate gate):
  passes — `E2E: all checks passed`, `PTY: all checks passed`, no failures in the log.

## Known limitations

**The mark says "uncommitted change at this path", not "you commented here".** `git status` is what answers,
and it cannot tell a reviewer's comment from anybody else's uncommitted work in the same checkout — nor from a
`git mv`, a stash pop, or a build that wrote into the tree. The reading is normally right because the review
flow makes it so: what is under review is committed, so what is uncommitted on top of it is yours.

**A file you created is not a row at all.** The tree lists the files the span changed, so a new untracked file
has no row to wear a mark — it joins the list when a commit brings it into the span. The pane still shows it
where it belongs: a thread you start appears in the box.

**Both asterisks are asterisks.** The span picker marks the endpoint you chose with `*`, and this mark is `✱`
in the tree. Different screens, and the picker covers the list when it is open, so they are never both on
screen; they are the same glyph family, and a reviewer who reads one as the other will find out from the
first glance at the other screen.

**The coloured span inside a selected row is the pre-existing one.** A styled name ends the reverse-video
highlight where its own reset lands, the same way a green `✓`, a dim arrow and a faint deleted name already
do. `line` applies reverse around styled spans; that is a change to how a row is highlighted, not to what this
row says.

**The codes are lipgloss's and the terminal's to spell.** Which codes carry slot 13 on a given terminal is
lipgloss's business — the same slot the pane already paints with — so the gate checks the shape of the claim,
that the mark carries some foreground code, that the name carries a colour and weight, and that the clean row
carries neither, rather than naming the bytes. That is how the reviewer-row and change-sign checks already
work, and for the same reason: a unit test is a terminal lipgloss will not colour for.

## Open questions

Nothing open. Two things a reviewer may want next, neither of which this change presumes:

- A count of written-in files beside the reviewed counter, for the case where a changeset is big enough that
  the rows are scrolled and the glance has nothing to glance at. The number is already in `branch.count`; the
  counter's line is the surface it would have to share.
- Whether the mark should ever be a refusal to submit. `s` sweeps the working tree into the submission, so
  these files are what a reviewer is about to commit — a warning band on `s` with an empty file list is the
  case where "you wrote in three files and are about to commit them" is worth saying out loud.
