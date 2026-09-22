# feat-mvp-readiness

## Summary

Four gaps sit between "the plans are done" and "this is releasable". None is a missing feature.

1. The two end-to-end gate scripts sat under a completed plan's `artifacts/` directory. A pipeline could
   not name them and a contributor would not find them.
2. Three `--json` arrays printed `null` when empty, against the rule the durable-records plan adopted.
3. PRD §8's command tree missed `change abandon`, `review reopen` and `integration publish`. `review
   reopen` has no PRD section at all.
4. `status` on a branch whose changeset landed told the reader to run `git pair init`.
5. The review TUI showed a file the span created as a patch: its own text with a `+` on every line. A move
   with no edits showed two lines about a path. Nothing in the tree said which files were new.

## What changed

- `e2e-29.sh`, `pty-walkthrough.sh` and their two python helpers are `scripts/gates/` now. `mise run
  gates` builds the binary and runs both. README gained a Development section that says what each gate
  covers and what it needs. The old directory keeps a README that maps each historical path to the new
  one. The commands quoted inside finished plans and audits still resolve.
- Both scripts run every command with stdin at `/dev/null`. `git pair init` reads an open pipe as
  `--about` content, so a harness that leaves stdin open blocks the first `init` forever. That is why this
  replay hung under a process runner and nothing printed.
- `queue`'s `ready_for_review` and `skipped`, `review submit`'s `files` and `status`'s `stack` are `[]`
  when empty. PRD §22 and README's JSON contracts state the rule in one place. Both documents name
  `latest_review` and `uncommitted` as the two fields that answer null on purpose.
- `change feedback` and `diff` accept the global `--json` and have none. Each says so on stderr, so an
  empty stdout stops reading as an empty answer.
- PRD §8's tree gained the three missing commands, §10.7 documents `review reopen`, and
  `TestEveryCommandIsNamedInTheDocs` checks the other direction of the docs contract.
- `status` on a branch whose directory the integration branch already holds says the changeset landed and
  names `git pair status --changeset <id>`. The `init` hint stays for a branch where nothing happened yet.
- The file tree puts git's status after a name: `+` for a file the span created, `-` for one it deleted,
  `~` for one it moved. A file the span only changed carries nothing, which is most of them.
- The pane reads two kinds of file as the file rather than as a patch: one the span created, and one it
  moved with its bytes unchanged. Every other file row keeps the patch.
- The pane's header counts the lines, names the path a move came from, and says `you edited it` when the
  reviewer edited that file after the commit. `Enter` still opens the difftool there.

## Design decisions

- The empty-array rule lives at the emit boundary. `orEmpty` in `internal/cli/root.go` turns a nil slice
  into `[]` where a command builds its JSON, which is where the contract lives. The alternative was a
  reflection pass inside `emitJSON`. That would cover every future command. It would also rewrite any
  slice field a later command meant to be absent. `omitempty` and pointers stay the way to say "not
  applicable".
- The harness stopped accepting null as empty. `jsonList` fails on a null now. Six tests read `skipped` as
  `nil` to mean "nothing skipped". They assert emptiness instead. That is the claim they meant to make. A
  helper that forgives the violation is how two of `queue`'s lists stayed null while the suite passed.
- The viewers get a sentence rather than a JSON mode. `change feedback` and `diff` print a report. A
  machine-readable form for both is a design task, not a fix. PRD §22 names `change feedback` as a primary
  agent command, so silence was the wrong third state.
- The gate scripts moved, and the old path keeps a pointer. Finished plans and their audits name
  `artifacts/e2e-29.sh` in text nobody should rewrite after the fact. A two-line map keeps those commands
  runnable without editing a record.
- The pane reads a file at the span's head, never from the working copy. A reviewer's uncommitted edits belong
  to a patch's `you` section. An editor view that folded them in would show a reviewer their own typing back as
  reviewed work.
- Only two changes read as a file: an addition, and git's `R100`. A rename with edits keeps the patch, because
  the edits are the review. A deletion keeps it, because it is the only place the removed text is.
- The text has its own cache, apart from the documents'. ABOUT.md is a file the span created and a document the
  reviewer edits, so one path carries two different texts.
- The status call passes no `-M`, because the call that lists the files passes none either. The two agree in a
  repository with rename detection turned off.
- No key was added. `Enter` on such a row already meant the difftool, and the row is still a file.

## Validation

- `mise run check` — gofmt, vet, the whole Go suite.
- `scripts/gates/e2e-29.sh` and `scripts/gates/pty-walkthrough.sh`, both from the new path, both green.
- New tests: `TestEmptyListsAreEmptyArrays`, `TestNoJSONArrayIsEverNull`,
  `TestJSONOnAViewerSaysTheFlagChangedNothing`, `TestEveryCommandIsNamedInTheDocs`,
  `TestStatusOnALandedChangesetsOwnBranchSaysItLanded`. The second runs every `--json` surface in six
  repository states, with the permitted nulls named.
- The carrying rule, the stack chain and the record read from the last three plans stay untouched, and
  their tests are the check that it stayed that way.
- The TUI tests are in `internal/tui/change_internal_test.go`. `TestTheTreeSignIsWhatGitSaysTheSpanDid`
  and `TestTheRowPutsTheSignAfterTheName` read the signs. One test names the pane each change gets. The
  rest read the text pane: its lines, a move's origin, the note about the reviewer's edits. One more says a
  file that is not text is not drawn. `TestWithoutRenameDetectionTheTreeSaysDeleteAndAdd` turns git's
  detection off.
- The preview fixture gained a file the span only changed. Every file it had before was new, so every pane
  test had quietly stopped looking at a diff.

## Known limitations

- `change feedback` and `diff` still have no machine-readable output. The note is an apology, not a
  feature.
- `TestNoJSONArrayIsEverNull` runs the surfaces the suite can reach without a terminal. `review open`,
  `review reopen`, `review about` and `review thread` are not in it, and none of them has JSON to check.
- The null allowlist is a list, not a type system. A new null key fails the test until someone writes it
  down with a reason. That is the whole of the protection.
- A reviewer cannot see the patch of a new file inside the TUI. `Enter` and `d` both take it to the difftool,
  which is the answer this screen has.
- The sign follows git's rename detection. A repository with `diff.renames=false` gets `-` and `+` for a
  move, and the pane diffs both halves of it.

## Open questions

- Should `change feedback --json` exist? PRD §22 tells an agent to read the submission with it, and today
  that means parsing prose off stderr.
- What does `change feedback` print? The diff between a submission and its parent, with a header naming the
  review, its outcome and the slug. `--name-only` lists the files, and `--stat` sizes them. The submission's
  own text is in none of it.
