# One discussion file replaces review threads

## Summary

The review conversation is one file now. A changeset directory holds `CHANGESET.yaml`, `ABOUT.md` and — if
anybody has started talking — `DISCUSSION.md`. The threads are gone: no thread files, no thread titles, no
slugs, no heading that counts them, and no command that knows what a topic is.

Two commands reach the file, one per side. The reviewer gets `git pair review discussion`, which opens it in
`$EDITOR` and writes the scaffold first if it is not there. The author gets `git pair change discussion`, the
non-interactive half, because an author may not run a `review` command. `git pair change combine --threads`
becomes `--discussion`, and it appends rather than copies.

This is the second of two changesets. The first made a reviewer's edit to `ABOUT.md` block `change ready`; this
one gives the rest of the conversation a single home. Nothing in the survival rule moves here.

## What changed

`internal/changeset` lost `ThreadSlug`, `ThreadPath`, `Threads`, `ThreadTemplate`, `EnsureThread` and the
test fixture's thread-listing helper, and gained `DiscussionFile`, `DiscussionTemplate`,
`ResolveDiscussionTemplate`, `Changeset.DiscussionPath` and `Changeset.EnsureDiscussion`. The file is reached by
one exact path everywhere it is touched, which is what makes `.combined/` inertness a property rather than a
loop that has to skip something.

`internal/cli` gained `review discussion` and `change discussion`, and `change combine` takes `--discussion`.
`internal/tui` lost the whole-conversation reading view, the `▾ Threads` heading, its rows, the `+ new thread…`
row, and the prompt that asked a reviewer for a thread title. What the box needs of the key layout is now the
span row, `ABOUT.md`, `DISCUSSION.md`, and nothing else; the submit confirmation and the pane's search field are
the two places left that take typed input, and neither is a row's key.

`PRD.md` §7 is a different section with the same number, §9.11 is new, and §10.3 is a different command. The
product skill, README and the two gate scripts moved with them, because the docs contract test fails a document
that names a command the code does not have.

The review round rewrote three paragraphs of `skills/git-pair/SKILL.md` that were wrong rather than wordy, and
the file now carries this changeset's first real `DISCUSSION.md`. What the skill says, and what it may not, was
checked against the code rather than against memory: the reviewer-command rule names the four commands and gives
both reasons (the reviewer's role, and the terminal), because the blanket "avoid any `review <subcommand>`" also
forbade `review history`, which the same table lists as an author's command. The approval-validity conditions
cite what `check` actually compares — the commit the approval's `Review-Head` names, tested with
`git merge-base --is-ancestor`, and a name-only tree diff from the newest marker with this changeset's directory
excluded — and drop the claim that merging an unlanded parent in preserves a review, which
`internal/cli/contribution_gate_test.go:46` refuses. The one sentence that changed a policy rather than a fact
is where the merge belongs; it is raised in `DISCUSSION.md` for the reviewer's ruling, and the wording there keys
it to the repository's declared workflow, which is what PRD §22 and §26 admit.

## Example

The reviewer blocks the change, and the point is one they want talked through rather than folded into the
description:

```text
$ git pair review discussion          # writes "# Discussion" the first time, then opens the editor
$ git pair review submit --block
```

The author reads the submission as a diff — `change feedback` has no idea the file is prose — and answers under
the reviewer's heading:

```md
## Concurrency tests

I'm not convinced the current test actually causes the two transactions to overlap.

### Response

The fixture serialised access earlier than I expected. Both transactions now block
immediately before the capacity check, and the test fails without the lock.
```

`change ready` then reports the reviewer's lines as surviving text under `changesets/` and lets the hand-off
through, because the answer was appended and every original line is still there.

## Design decisions

**Nothing parses the file.** The alternative on the table was a `review note` command that wrote each entry
under a heading the tool chose, and a review screen that listed those headings so a reviewer could jump between
topics. It was dropped: the list was the only reason the old shape needed titles, slugs and a deduplicating
writer, and it charged every reviewer a title before they could write a sentence. A heading index would have
been the first step back towards a schema, so there is none — `e` on the row, `Enter` on the row, and one arrow
key below `ABOUT.md` are the whole interface.

**Two commands, not one.** A single `discussion` command under a neutral parent would have been less surface,
but this repository's own workflow skill forbids an author from running any `review` command. An author needs a
way to open the file when the review reached them as a comment in a source file, so the author gets a
`change`-parented half that reports instead of opening.

**`init` does not write it.** The file arrives on first use, from either command or from `e`. A scaffold in
every changeset directory is a file most changesets never need and every reviewer scrolls past. The row is in
the box either way — the row is the offer, and a pane whose file is absent says `does not exist yet — e creates
it`.

**The scaffold is a title, and the repository may replace it.** `.git-pair/discussion-template.md` is read by
the same three rules as `.git-pair/about-template.md`: blank counts as absent, unreadable is an error rather
than a quiet fallback, and `{{slug}}` is the changeset id. A team that wants prompts writes prompts.

**`--discussion` appends.** The survivor keeps its own text and gains the disappeared changeset's under one line
naming the archive it came from, guarded against double-append. Merging two conversations needs an opinion about
whose heading wins; appending needs none, and the archived original stays where the fold put it.

## What is deliberately not changing

The survival rule is the parent changeset's and is untouched: text under `changesets/` is reported and
forgiven, text in `ABOUT.md` blocks. `git pair check`, the submission identity digest and approval staleness
still exclude the changeset directories, so what somebody writes into the discussion file cannot invalidate the
approval it is written into — `TestAReplyInTheDiscussionKeepsTheRecordedIdentity` and its `reviewops` twin are
that claim at two levels. `change feedback` still prints `review^..review` with git's own plumbing and no
markdown knowledge: no conversation view, no heading parsing, no per-topic rendering.

Existing thread files are not migrated. Six of them are in this repository, and they were always plain markdown
under `changesets/`, which is why they were already non-blocking. They stay exactly where they are, readable,
diffable and inert. The `## Addressed feedback` sections in the ten changesets that have them are history too.

## Where to look hardest

The deleted prompt subsystem is the risk. The review screen had one text-input prompt and it is gone, which
touched the mode enum, the key table, the footer band's row count, the shortcut bar and the layout's chrome
arithmetic — and the pane's search field borrowed that prompt's block caret, so the glyph had to survive the
deletion of the thing that owned it. `TestBandHeightIsWhatTheChromeCounts` no longer iterates a prompt mode,
which is exactly the kind of coverage that disappears quietly.

The second is the box's shape. It went from six rows — span, `ABOUT.md`, the heading, two threads, the
create row — to three, and the box sits at the end of one shared row list, so tests that counted rows or stood
on "the last box row" now stand on the edge. `TestJkCrossTheEdgeBetweenTheBoxAndTheTree` and
`TestTabWalksTheFocusRing` are the two that encode the geometry directly, and both had to name their row rather
than count from the end: `j` off the box's last row is the crossing, which is what made the old
"stand on the discussion row and press `j`" test stop moving.

The third is the skill's policy text, which outlives the code: `Do not cross the line` and `What is not yours` in
`skills/git-pair/SKILL.md`. Both were rewritten during review, both restate rules that PRD §22 and §26 own, and
the sentence about where the merge belongs is the one to read against them and against `DISCUSSION.md`.

## Validation

`mise run check` — markdown format, `gofmt`, `go vet`, then the sharded suite. `mise run gates` runs the whole
suite plus the three gate scripts, one of which (`e2e-29.sh`) drives `change discussion` and asserts
`review discussion` refuses without a terminal, and the other (`pty-walkthrough.sh`) asserts the box lists the
`DISCUSSION.md` row on a real terminal.

The new file is pinned at three levels. `internal/changeset/discussion_test.go` covers the path, the scaffold
rules (a repository template wins, a blank one counts as absent, an unreadable one is an error rather than a
quiet fallback, `{{slug}}` is replaced), `EnsureDiscussion` writing once and never overwriting, and `init` not
creating the file.
`TestReviewDiscussionScaffoldsAndNeverOverwrites` and `TestChangeDiscussionReportsCreatedOrFound` drive the two
commands, including the exit-2 refusal that still leaves the scaffold behind. `TestChangeCombineAppendsTheDisappearedDiscussion`
and `TestChangeCombineWithNothingToBringAcross` are the append, in both directions.

The changeset also eats its own cooking: this `DISCUSSION.md` was written by `git pair change discussion`, which
reported `Created` and then `already exists` on the second run, and the exit-2 refusal was read from the command
line before the gate script asserted it.

## Known limitations

A thread file that already exists in an active changeset has no command that lists it any more. It is still
plain markdown in the changeset directory: `change feedback` shows it, a reviewer can open it, and nothing
blocks on it. Migration was rejected because a migration for six files would be a heavier change than the
feature, and because the files are what the new design says they always were.

The workflow skills this repository runs are vendored from dev-profiles, and three of them name `review thread`
as a command an author may never run: `git-pair-workflow` in the `git-pair-loop` profile, and the
`submit|open|reopen|thread|about` lists in `git-pair-github-integration` and `git-pair-manual-integration`. That
word is now wrong in three places, and it is not corrected here. The profiles are somebody else's files and the
materialised copies under `.agents/skills/` are byte-compared against them by `dev-profiles doctor`, so editing
one without the other makes the tree fail its own check, and editing both makes this checkout disagree with the
library it says it matches. The change belongs in the dev-profiles profiles and will arrive here on the next sync.
Nothing in this repository's verification depends on it: `docs_contract_test` reads `PRD.md`, `README.md` and the
shipped `skills/git-pair` skill, not the vendored workflow profiles.

One contradiction turned up during the accuracy pass and is left alone: `README.md:2426` advises anyone with no
terminal — an agent, a pipe, a cron job — to "use `git pair diff`, `git pair status` and `git pair review
submit`". That is correct about terminals and wrong about roles, and it predates this change. It is not about
the discussion file either, so it is a follow-up rather than a line in this changeset.
