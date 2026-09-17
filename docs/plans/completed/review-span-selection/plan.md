# Review Span Selection — Execution Plan

Requirements: [requirements.md](requirements.md) — the owner handoff, reproduced apart from heading
levels, with two amendments made during execution at the owner's request: §8.1 (the session's span
ring) and the rewrite of Scenario A. Two places are knowingly superseded rather than edited: §24 and
§30 still describe `v` as a two-state toggle, which §8.1 replaces, and they stay as received so the
handoff remains legible against the decision log.


> **Renamed after archiving (2026-09-17).** `gitpr` in this document is today's **`git-pair`** — binary
> `git-pair`, invoked `git pair …`, module `gitpair` — and the marker trailers it specifies,
> `GitPR-State` / `GitPR-Outcome` / `GitPR-Changeset`, are today's `Review-State` / `Review-Outcome` /
> `Review-Changeset`. This plan keeps the names the work was actually done under, and so do the audit and
> research files it links: they cite commit SHAs as evidence, and rewriting them would rewrite the record.
> The directory name `docs/plans/completed/gitpr-mvp/` stays for the same reason — `internal/survival` and
> the audit artifacts cite that path. The living specification is `README.md` and `PRD.md`.

## Goal

A review span becomes a pair of explicit checkpoints, `BASE → HEAD`, resolvable through review
submissions, commits, refs, the changeset base and the working tree. The head decides whether the
session is a live review or a read-only look at history. The common cases stay one key long.

## Status (audited 2026-09-16)

**Complete, and the defects the audit found are fixed.** Every milestone is implemented, verified and
committed on `gitpr/init-commits`, and the closing audit
([audits/2026-09-16-completion.md](audits/2026-09-16-completion.md)) found no milestone incomplete. It
found three defects in shipped behaviour, which the audit recorded rather than fixed; all three are now
fixed in `9b5a877` (marks across a span round trip), `d0ceee7` (the header's span name) and `1575437`
(the read-only `ABOUT.md` write), each with a test that fails without the fix. See *Known gaps*.

The plan was archived to `docs/plans/completed/review-span-selection/` when the audit closed. Evidence
marked *pty* below means it was run in a real terminal. Those hand runs are scripted now:
`docs/plans/completed/gitpr-mvp/artifacts/pty-walkthrough.sh` drives the installed binary through a
pty and checks the read-only historical screen, the `V` picker's endpoints, the `v` ring's position
note, the drift banner and `r` with a ref moved by another process, a mark surviving `v` away and
back, and the difftool handoff keeping the alternate screen. It builds its own repository, prints
`PTY: all checks passed`, and was checked against four mutants — a screen that stops declaring
itself read-only, a banner that never appears, marks that are not restored, and a session that does
not take the alternate screen — each of which turns the scenario that covers it red. Everything the
Evidence column otherwise names is a test.

The audit's own verification claims are checkable against the tree: milestone commits, test names and
suite counts are listed below and were re-counted by a second, independent pass.

| Milestone | State | Commit | Evidence |
| --------- | ----- | ------ | -------- |
| M1 checkpoints and a nameable head | done | `cc7dc6c` | `internal/span` checkpoint/drift tests; CLI flag tests in `internal/cli` |
| M2 read-only historical mode | done | `8b9dd9d` | `internal/tui/readonly_internal_test.go` (a refusal per mutating key), pty walkthrough |
| M3 the `V` picker | done | `efd9474` | `internal/tui/picker_internal_test.go`, `internal/git` commit/ref-tip tests, pty |
| M3b `v` walks the session's spans | done | `2bc714e` | `TestSessionStepSpan*`, `TestVStepsThrough*`, `TestStepSpanStopsShort*`, pty |
| M4 drift and `r` | done | `ebaeccd` | `internal/tui/drift_internal_test.go`, `TestRefreshDrift*`, `TestSessionStaysOnTheRefItPinned`, pty with a branch moved mid-session |
| M5 base-side CLI flags | done | `eeb0919` | `TestDiffBaseCheckpoints`, `internal/cli/contract_test.go` |

Suite at audit: 14 packages, 328 test functions, `mise run check` (build, vet, gofmt, test) green,
and `internal/hygiene` still proving nothing pushes, merges, rebases or resets.

## Success Criteria

Observable outcomes, not implementation details:

- `gitpr review open` still opens the full changeset, unchanged (§21's "existing behavior" holds).
  Verified indirectly: `review open` refuses to run without a terminal, so this rests on `diff`'s
  default-span test plus the flag set the two commands share, not on a `review open` test.
- A span whose head is not the working tree opens **read-only**: no marking, no submitting, no
  editing of product files or `ABOUT.md`, no new threads, and the screen says so. **Met** since
  `1575437`; before that a historical span could recreate a missing `ABOUT.md` and open it for
  editing. See *Known gaps*.
- Both ends of a span can be named by review index, commit id, or ref, from the CLI and from the `V`
  picker; all of them resolve through one implementation shared by CLI and TUI. **Met** since
  `d0ceee7`; before that the header printed `span: unreviewed` for any span based at a review and
  headed at the working tree, so `--since-review=-3` announced itself as the unreviewed span. See
  *Known gaps*.
- A ref chosen as a checkpoint keeps its name on screen, diffs against the commit it pointed at when
  chosen, reports drift without acting on it, and changes only when the reviewer says so.
- Reviewed marks are not carried across a span change — a mark made in one span does not silently
  mark a file in another — and they are still there when the span comes back. **Met** since `9b5a877`,
  which is also what makes the first half worth keeping a test for: before it, both halves failed in
  the same direction. See *Known gaps*.

## Context

Already in the code, so this plan does not rebuild it:

- `internal/span` resolves a span to two full SHAs; `internal/lifecycle` numbers review submissions
  chronologically, so §5's stable negative aliases exist.
- `--unreviewed` and `--since-review=N` (bare `-1`) exist on `diff` and `review open` and share that
  resolver — §21's "one canonical implementation" holds for the base end today.
- `v` toggles the two common spans (§7), which M3b generalises into a walk over the session's
  spans.
- `internal/reviewmark` keys marks by end commit plus a per-file diff key (§14's invalidation).
- The preview pane shows the author's span diff and, beneath a `── you · uncommitted` caption, the
  reviewer's own uncommitted edits.
- Keys `V` and `r` are unbound. (True when this was written; M3 binds `V` and M4 binds `r`.)

Owner decisions taken before this plan:

- **Default unchanged.** `review open` opens the full changeset even when reviews exist. §6.2 is
  overridden; PRD §17.2 stands. The unreviewed span stays behind `v`, `--unreviewed` and the
  picker's `u` preset.
- **`Working Tree` means the HEAD pinned when the session starts**, not a mutable worktree compare.
  Uncommitted edits keep appearing as the captioned `you` section, which is how the pane reads as
  `base → working tree` without breaking the commit-keyed marks or changing what `gitpr diff` prints.
- **Marks survive.** A span change reports how many marks do not apply rather than deleting them.
- **This round is the model plus read-only historical mode.** The `V` picker and the drift banner
  come after.

One consequence of "live only when the head is the working tree", adopted deliberately:

- **`review reopen` moves from the covered span to `Review -1 → Working Tree`.** The covered span
  ends at a commit, so under the new rule it would be read-only — and a resume entry you cannot
  resume from is not a resume entry. The case the covered fallback was built for (nothing new has
  landed, but the reviewer has notes in the working tree) is now served by the `you` section, which
  shows those edits inside a live span. If a covered, read-only look at a submission is wanted, that
  is what the picker's `Commit…`/`Review…` heads are for.

## Constraints

- `gitpr` never pushes, merges, rebases, resets, deletes branches, or squashes. Ref drift detection is
  `git rev-parse`, and refresh re-resolves a name — both read-only. `internal/hygiene` stays green.
- PRD §3: no diff renderer, no DAG. The commit and ref pickers are filtered lists; the pane keeps
  printing git's bytes.
- Review state stays Git-native and forge-independent; marks stay local, keyed on commits.
- A key must not change meaning with mode: `s`, `Space`, `e`, `a`, `t` are *refused* in historical
  mode, not silently repurposed.
- The pane's row arithmetic must keep holding: rows padded to width, frame exactly terminal height.

## Assumptions

- `git difftool <from> <to> -- <path>` behaves for two commits the way it does for one commit and the
  working tree (verified in a spike before M2 relies on it).
- Review history for numbering means every submission commit on `refs/reviews/<slug>`, as today —
  superseded submissions keep their index (§5 "complete known history").
- Pinning lives for the life of the TUI process. Nothing about a resolved span is written to disk.

## Milestones

### M1 — Checkpoints, and a head you can name

Deliverables:

- `Checkpoint` is a real type: changeset base, working tree, review index, commit id, ref with its
  pinned OID. Nothing resolves a rev in the TUI or the CLI.
- `Span` carries both resolved checkpoints and answers `Live`, `Historical`, `CanEdit`, `CanSubmit`,
  `CanMark`. Mode is derived from the head, never passed around separately.
- `gitpr diff` and `gitpr review open` accept a head: `--head-review=N`, `--head-commit=SHA`,
  `--head-ref=NAME`. Base-side equivalents wait: `--unreviewed` and `--since-review` cover the
  review case, and nothing on those commands named an arbitrary base — which is why the base trio
  (`--base-review`, `--base-commit`, `--base-ref`) is recorded as its own last item rather than
  folded into the picker work.
- Drift detection and refresh exist as span operations, tested against real git, with nothing in the
  UI depending on them yet.

Tasks:

- [x] `internal/span`: `Checkpoint` type, constructors, `Selector{Base, Head}`, and a resolver that
      turns a selector into `From`/`To` SHAs; rewrite `Resolve` on top of it so flags and picker
      share one path.
- [x] Dropped the now-derived `Kind`/`FromRef`/`ReviewIndex` fields in favour of the checkpoints plus a
      label rendered from them.
- [x] `Drift(ctx, repo)`: re-resolve each ref checkpoint and report name, pinned OID, current OID.
      `RefreshRef(ctx, repo, name)`: re-pin one checkpoint and recompute the span.
- [x] CLI head flags, mutually exclusive with each other. `diff` has them now; **`review open` does
      not until M2**, because a screen that ignores the mode would be worse than no flag.
      `register` and `registerHead` are separate so a command can take one without the other, and
      the head flags refuse each other; `--unreviewed`/`--since-review` were left to conflict as
      they already did until M5 made base naming one rule.
- [x] `review reopen` resolves to `Review(-1) → WorkingTree`.

Verification:

- `mise run check` green, including new span tests: each checkpoint kind resolves to the SHA git
  gives; `Review -2` means the same submission whatever the head; a ref checkpoint resolves to its
  pinned OID and keeps its name; drift is reported and does not move the span; refresh re-pins and
  recomputes.
- `git log --oneline --format`-independent integration tests use the existing `gittest` harness, so
  no test depends on a developer's repo.
- CLI: verified against a scratch repo — `diff --head-commit`, `diff --head-ref` (label rendered
  `probe@1a2b3c4`), a bad name exiting 2 naming what failed, two head flags refusing each other, and
  the default span unchanged. `review open` gained the flags the same way in M2, through the shared
  `spanOptions.register`/`registerHead`. Nothing tests them on `review open`, which refuses to run
  without a terminal: every `--head-*`/`--base-*` assertion in `statusdiff_test.go` and
  `contract_test.go` invokes `diff`.

### M2 — Historical mode, genuinely read-only

Deliverables:

- A session opened with a non-working-tree head shows `HISTORICAL · READ ONLY` beside the span, hides
  the reviewed gutter and counter, and refuses `Space`, `s`, `e`, `a`, `t`, `+ new thread…` with a
  message naming the head and the key that changes it.
- `Enter`/`d` on a historical span run `git difftool <from> <to> -- <path>` — two pinned commits,
  never today's working tree — while a live span keeps comparing base against the working tree.
- The preview pane and the `you` section behave sensibly: a historical span has no reviewer edits to
  show, so the section is absent rather than empty.

Tasks:

- [x] Spike done (see Spikes): two revs give the tool two temp files; labels arrive empty.
- [x] The model asks the session for its `Span` rather than endpoints. `mutatingKey` names the
      keys that change something and `cannot` says why the span forbids one, checked once at the top
      of the key handler; `activate` covers the row action the table cannot reach.
- [x] The counter's slot carries `HISTORICAL · READ ONLY`; the reviewed gutter, the counter and
      the `+ new thread…` row are absent, and the shortcut bar drops the keys it cannot take.
- [x] `difftoolHead` passes the pinned head over history and nothing over a live span; the pane
      fetches only the span diff, and says "no changes in this span" rather than waiting for an
      answer about edits it never asked about.
- [x] README's command table and TUI prose, PRD §14 (the read-only screen) and a new §17.4 for
      checkpoints and the mode matrix.

Verification:

- Unit tests per refusal: a table over `Space`, `e`, `a`, `t`, `s` asserts mode unchanged, marks
  unchanged, row count unchanged, working tree still clean, and a status that names the head and `v`.
  Falsified by short-circuiting the gate — without it the marks flip, submit mode is entered, and no
  explanation appears.
- The escape hatch is tested too: `v` lands on a live span, after which `s` is legitimate again.
- A test asserting a historical session never issues a worktree diff (`WorkingPatch` not called) and
  always passes two revs to the difftool.
- End to end in a pty: `review open --head-review=-1`, confirm the header, the refusals, and that
  `gitpr review submit` is unreachable.

### M3 — The `V` picker: choose both ends

Deliverables:

- `V` opens a two-column picker with pending base and head, `Tab` between columns, `j`/`k`, `Space`
  to set one end, `Enter` to apply, `u`/`f` presets, `Esc`/`q` to cancel.
- `Commit…` opens a searchable commit list (short SHA, subject, age) with direct id entry; `Ref…`
  opens local branches, remote refs, tags and other refs, with direct entry, and returns a ref
  checkpoint pinned at its current OID.
- `HEAD` is not offered as a first-class option in either column.

Tasks:

- [x] `Repo.RecentCommits` and `Repo.RefTips` behind `git.Repo` — the latter peels tags and carries
      dates — and `span.ShortRef` so the screen reads `main` while the checkpoint keeps
      `refs/heads/main`.
- [x] The picker as one more mode of the same model (`modeSpan`), with the same frame invariants as
      the review screen: every row padded to the width, the frame exactly the terminal's height, the
      divider in one cell.
- [x] `V` in both shortcut bars. The picker is deliberately outside the read-only gate: it is how a
      historical span gets out, and it changes nothing until `Enter`.
- [x] README, PRD §14 and §28.
- [x] Base-side CLI flags (`--base-review`, `--base-commit`, `--base-ref`) on `diff` and
      `review open`, so a script can name a base the way the picker does. Listed here because it is
      the picker's CLI twin; it shipped last, after M4, in `eeb0919`. Registered on both commands,
      tested on `diff` only — see M1's note on why `review open` cannot be exercised headlessly. `--unreviewed` and
      `--since-review` name the same end, so the two families are mutually exclusive rather than
      one silently winning; `--base-review` bare means `-1`, like its siblings.

Verification:

- [x] Pending versus applied: `Space` leaves the session's span alone while the picker shows what the
      pair would become (`HISTORICAL · read-only` before it is true); `Enter` applies it; `Esc`
      discards it and clears the status.
- [x] Filtering, direct entry, and a typed id git refuses — reported inside the drill, which stays
      open with the text intact rather than swallowing the keystroke.
- [x] Ref names friendly on screen and full in the checkpoint; the pin is the commit the ref pointed
      at; an annotated tag resolves to the commit behind it.
- [x] `HEAD` is offered nowhere; the last three submissions are aliased `-1`…`-3` and older ones
      carry their index; the alias displayed is the index stored, so §5's stability holds through the
      picker.
- [x] The frame invariant in both picker layouts (columns and drill).
- [x] Pty walkthrough of scenarios B and C, and of a live span whose base is a commit — §23's first
      matrix row.
- [x] The CLI says the same thing the picker does: `--base-review=0 --head-review=1` and
      `--since-review=0 --head-review=1` print byte-identical diffs, a `--base-ref` span names
      itself `probe@<sha>`, and naming the base twice — within a family or across the two — is a
      usage error.

### M3b — `v` walks the spans this session has been in

Deliverables:

- The session keeps the spans it has stood on: the span it opened on (including one named
  entirely by CLI flags), the two presets, and every span chosen with `V`. `v` steps forward
  and wraps, and the status names the position when there are enough stops for it to mean
  something.
- It is a set of spans, not a keystroke log: choosing the same span twice is one stop, and a
  rescan after an external tool closes is not a visit.
- From a read-only span, `v` returns to the last span that was reviewable — the promise M2's
  refusal message makes.

Tasks:

- [x] `Session` ring (`noteSpan`, `seedRing`, `StepSpan`, `SpanPosition`, `SpanRing`) and
      `span.Selector.Key()` for identity; the presets are seeded at open so PRD §17.2's toggle
      still works before anything custom has been chosen, and `Reload` re-seeds so the first
      review submission mid-session does not leave `v` with nowhere to go.
- [x] `v` in the TUI steps the ring and reports `span <label> (n of m)`, sharing the status
      sentence with the picker's apply path so both say the same thing about marks that stopped
      applying and read-only spans.
- [x] README and PRD §14, §28.

Verification:

- [x] Walking with a custom span chosen: a whole turn returns to it.
- [x] Rescans add no stops; choosing the same span twice is one stop.
- [x] A stop whose checkpoint no longer resolves leaves the span, the ring and the position alone,
      and works again once it resolves. This line originally said *"whose ref was deleted"*; after
      M4 a deleted ref still resolves to its pin, so `TestStepSpanStopsShortWhenAStopNoLongerResolves`
      now breaks the stop by deleting a tag that a typed `Commit…` endpoint named — the realistic
      case anyway, since the drill takes typed revisions.
- [x] From a read-only span `v` lands on a live one — and on the *last live* one, not merely a
      live one, which is the difference between getting back to work and being sent to a preset.
- [x] Two-stop sessions say `span <label>` with no position.

### M4 — Drift the reviewer can see and act on

Deliverables:

- Ref checkpoints are re-resolved at the cheap points (after `$EDITOR`/difftool returns, on reload).
- A drifted span shows `⚠ main moved abc123 → def456  [r] refresh`, non-modally, and the review
  continues against the pinned OID until `r`.
- `r` re-pins, recomputes the span, and reports how many marks no longer apply.

Tasks:

- [x] Resolution honours a pin: a ref checkpoint with an OID resolves to that OID, and the session
      writes the pins from a resolved span back into its selector. Without both halves the span
      follows the branch on the next rescan, which is §13's forbidden move — and drift would never
      be visible, because the span would have arrived at the new commit already.
- [x] `Session.CheckDrift` (asks, remembers nothing), `RefreshDrift` (re-pins, rescans, reports),
      `Span.TracksRefs` so a span with no ref endpoint asks git nothing.
- [x] The check runs on a 3s tick and after an external tool closes (`Reload`). The answer comes back
      as a message carrying the span it was about, so a slow check cannot paint a banner for a span
      the reviewer has stepped off.
- [x] The banner — `⚠ probe moved bb0f343 → 43915ed  [r] refresh` — is a row of the footer, not of
      the list column, and takes a chrome row.
- [x] `r` is bound always, advertised only by the banner: with nothing drifted it says so rather than
      sitting on an unbound key.
- [x] README, PRD §14 and §28.

Verification:

- [x] Scenarios D, E and F in a pty, with a branch moved by a background `git branch -f` while the
      screen is open: the pinned span keeps browsing, the banner appears with its key intact at 110
      columns with the preview pane up, and `r` re-pins — the header's `probe@bb0f343` becomes
      `probe@43915ed` and the report reads `refreshed probe bb0f343 → 43915ed`.
- [x] Tests that the banner is not the status line and does not grow the frame, in both the plain and
      split layouts — the split-screen case is the one the first implementation failed.
- [x] That a pinned ref survives a reload, a move, and its own deletion; that a deleted ref is not
      reported as drift; and that refreshing rewrites the stop on the span ring rather than adding a
      new one.
- [x] Falsified: with the banner parked in the list column, the split-screen test fails on both the
      clipped key and the frame height.

## Spikes / Research

- `git difftool <from> <to> -- <path>` — **done, M2 can proceed.** In a scratch repo with a stub
  tool, two revs invoke the tool with two paths: `/dev/null` and `/tmp/git-blob-XXXX/enroll.ts` for
  an added file. So §19.2's snapshots are git's, not ours. The wart: `$LOCAL_LABEL` and
  `$REMOTE_LABEL` arrive empty in the two-rev form, populated in the one-rev-against-the-worktree
  form. A historical session therefore shows the tool unnamed files; acceptable for a read-only look
  and worth a sentence in the help rather than a workaround.
- `git for-each-ref` for friendly names — **done, and it bit twice.** `%(objectname:strip=2)` is
  rejected by git 2.43 (`unrecognized %(objectname) argument`), so peeling needs `%(*objectname)`,
  which is empty for the refs that need no peeling. And an annotated tag carries a `taggerdate` and
  **no** `committerdate`, so a parser requiring the latter silently dropped every tag from the list —
  caught only by a test with an annotated tag, which is why it has one now. Both date fields are
  asked for and either may fill it; a ref with no date is still a ref worth listing, shown without an
  age. `refs/heads/main` displays as `main`, `refs/remotes/origin/main` keeps its remote, and
  `refs/reviews/booking` keeps its full name rather than becoming a bare `booking` beside the branch
  of that name.

## Correction after archiving: `v` was cycling in place

M3b's rule — "from a read-only span, `v` goes back to the last span you could review" — was applied
to *every* step out of a historical span. Two historical spans on the ring therefore could not be
reached from each other: the walk closed a loop between the last reviewable stop and the first
historical one, and the second was unreachable by any number of presses. The report was "v gets stuck
cycling through… first to last and back to first".

The rule was right about the need and wrong about the trigger. A first fix earned the escape with a
refusal: the read-only gate credited the next `v`, and that press alone meant "get me out"
(`Session.StepOut`) while every other press was the plain walk. That shipped, and was then removed.
A reviewer found it predictable in the wrong direction — after a refusal on the first stop, `v` jumped
to the fourth, past two spans they could have reviewed — and the reason was structural: a key whose
destination depends on what happened a few keystrokes ago cannot be learned by pressing it. `v` is now
the walk and nothing else, from every stop including a read-only one. What replaced the escape is `V`,
which lists spans and whose head column always offers `Current`, so getting to a reviewable span is
still one keystroke of intention — it is simply a different key from the one that walks. The refusal
message was changed to match (`V chooses a span you can review`, not `v opens`).

A second defect in the same corner, found while fixing the first and still there: a stop whose endpoint
had stopped resolving (a deleted tag, a force-pushed branch) stopped the walk *entirely* — every press
failed the same way and `V` was the only way past. `v` now steps over such stops and names them, reason
included (`StepResult.Skipped`, worded by `skippedNote`), and the stop stays on the ring so it returns
with the ref. Where nothing else resolves either, the press says so and moves nothing.

The tests are `TestSessionStepSpanReachesEveryStop` (the loop, as reported),
`TestSessionStepFromHistoricalWalksOutToAReviewableSpan`, and
`TestARefusalDoesNotChangeWhereVGoes`, which is what keeps the escape from creeping back; plus
`TestStepSpanSkipsAStopThatNoLongerResolves`, `TestStepSpanNamesEveryStopItSkipped`,
`TestStepSpanSaysSoWhenEveryOtherStopIsDead` and `TestVNamesTheStopItSkipped` for dead stops. Wherever
this document says the step escapes from history, read: **it does not escape at all** — the walk is
the only rule, and `V` is the way to a span you can review.

## Naming that changed after this plan was archived

A second spelling of the same span was removed. The picker's `Selected:` preview showed the pending
pair twice — `changeset base → working tree` from the checkpoints, then `main...current` from the
resolved span — and two vocabularies side by side read as two spans. The preview now says the one span
string the header, the status line, `gitpr diff`'s stderr and `status --json` all say. The endpoint
words survive for the case where they are the most specific thing available: when git cannot resolve
the pair, there is no span to name, and the line shows what was chosen (`changeset base → nonsense`).
The choices are not otherwise hidden — the columns mark them with `*`, and a drill names the end it is
choosing for. Test: `TestSelectedLineNamesTheSpanTheRestOfTheAppNames`.

On the owner's request (2026-09-17, branch `gitpr/span-labels`) the endpoint vocabulary moved, so a
reader of everything above should translate as they go:

| Endpoint | Was | Now |
| --- | --- | --- |
| working-tree head, live span | `HEAD` | `current` |
| newest review | `review -1`, picker row `Review -1` | `last review`, picker row `Last Review` |
| any other review | `(after review N)` from the resolved index | the index as entered: `review -2`, `review 0` |

`main...HEAD` is now `main...current`; the unreviewed span's `8ab932f..HEAD (after review 0)` is
`last review..current`. Three consequences for reading this plan:

- Wherever it says `Working Tree` for the picker's live row, that row reads `Current` (detail
  `latest + edits`). Requirements §4's rule — `HEAD` is never a pickable checkpoint — still holds;
  only the name of the one live target moved, and the reason for the old name (two similar-looking
  live targets) is why the new name is not `HEAD`.
- Wherever it quotes a label containing `HEAD` or `(after review N)`, the equivalent today is
  `current` and the entered alias. `Unreviewed()` still means "based on the newest review" — that
  rule and its test are untouched; only the string the header falls back to changed.
- Labels print the index as typed, so `--since-review=-3` and `--since-review=0` on a three-review
  changeset label differently and resolve identically. `TestSpanLabelsReadBackAsEntered` pins both
  halves; the picker's rows still store the alias they show.

## Follow-ups the audit recommended, and where they landed

- **Multi-ref drift** — `TestDriftBannerCountsEveryMovedRef` (the `(+N more)` banner, a refresh that
  re-pins both endpoints, and the span staying read-only across it) and
  `TestRReportsTheMarksAMovedRefInvalidated` (the note counts the marks the refresh invalidated).
- **`review open`'s span flags without a terminal** —
  `TestReviewOpenRegistersEverySpanFlagDiffDoes` (both commands register all eight, and `diff`'s
  `--stat`/`--tool` have not leaked onto `review open`) and
  `TestSpanFlagsRefuseTwoEndpointsOnOneEnd` (the mutual exclusion is a count over every spelling of
  one end, base and head alike).
- **The default span at the level the session sees** — `TestNoSpanFlagMeansTheFullChangeset` (no
  flags resolves to the full changeset, in the one function that applies the default) and
  `TestSessionRefusesASelectorThatNamesNoHead` (a selector with no head is refused: the full
  changeset is chosen, never defaulted into by a zero value).
- **`--tool`'s help** — it now says a live span compares its start against the working tree and a
  historical one compares its two pins, pinned by `TestToolFlagHelpNamesBothKindsOfSpan`.
- **The hand-run evidence** — scripted, as above.

## Known gaps (found by the audit, then fixed)

**Marks do not survive a span round trip inside one session.**

The owner's decision and the Success Criteria both say marks survive a span change and are still
there when the span comes back. The reporting half is true: `spanNote` says how many stopped
applying. The returning half is not. During the audit a probe marked `service.go` in the full
changeset, pressed `v` to the unreviewed span and `v` back, and found `reviewed=0/5` — the mark was
gone. Cause, in the order it bites:

- `SetSpan` → `scan` rebuilds the file list for the span being entered, and the only in-session
  carrier of marks is the previous file list, whose diff keys belong to the span just left. They no
  longer match, so the mark is dropped.
- `Session.persisted` is read once, in `NewSession`, for the commit the session opened on. A span
  change never re-reads it and a toggle never updates it.
- `toggleMark` writes the *whole* current set through `SaveMarks`. So the next toggle after a round
  trip persists the emptied set, and a later `review open` on the same commit resumes without marks
  that an earlier session had recorded.

Fixed in `9b5a877`, which took the third bullet more literally than either suggestion did. A mark is
a fact about a path *and* a diff key, so the store now keeps several keys per path and `Save` replaces
only the `(path, key)` pairs it is handed — the two spans a reviewer toggles between most often, full
changeset and unreviewed, both end at HEAD, and one span's save had been erasing the other's marks from
disk while both were still on screen. On top of that the session reads the store for the commit the
span it just entered ends at, rather than once at open. `TestMarksComeBackWhenTheSpanComesBack` marks
two files, presses `v` twice and checks both are still marked;
`TestSavingOneSpanLeavesTheOtherSpansMarks` covers the disk case. Both were run against the old code
and fail: the first reports `0 files are marked`, which is the probe's result.

**The header calls every review-based span `unreviewed`.** `Session.Unreviewed()` asks only whether
the base is a review and the head is the working tree, and `spanName` swaps the word `unreviewed` in
for the span label on that answer. So `review open --since-review=-3`, `review open
--base-review=-3`, and the picker's `Review -3` base all print `span: unreviewed` for a span that
begins three submissions back. It contradicts Success Criterion 3 and requirement §17 (the current
span should always be visible), and no test pinned it: `Unreviewed()` was asserted once, for the real
unreviewed span. Fixed in `d0ceee7`: the predicate is now the *newest* review — head is the
working tree, base is a review, and that review is the last one in the changeset — and a span based
further back keeps the label it resolved to, which already names the review it starts after
(`bb0f343..HEAD (after review 0)`). `TestUnreviewedMeansTheNewestReview` and
`TestHeaderNamesUnreviewedOnlyForTheUnreviewedSpan` cover the predicate and the line on screen.

**A historical span can recreate `ABOUT.md`.** `openArtifact` treats `ABOUT.md` as a diff target when
the historical span touched it and it existed at the span's start, so the read-only gate lets the
action through; the file is then missing from the working tree, and the code path calls `EnsureAbout`
before opening the editor. That is a write and an editing handoff in a mode whose entire promise is
that it changes nothing. Narrow — it needs `ABOUT.md` absent from the worktree — but Success Criterion
2 is absolute and nothing tested this path. Fixed in `1575437` by guarding that branch with
`Span().CanEdit()`, so a historical span gets the difftool over its two pins instead.
`TestHistoricalSpanDoesNotCreateTheAboutItDiffersFrom` asserts the working tree is untouched, and
`TestLiveSpanStillCreatesAWorkingTreeAbout` keeps the live behaviour, so the guard is a mode boundary
rather than a branch that never runs.

## Discoveries

Things execution taught that the plan did not know when it was written.

- **Where a pin is taken matters more than where it is stored.** The picker's ref list is a snapshot,
  but the pin is taken when the reviewer applies the span. In the pty walkthrough that ordering was
  visible: a branch moved *before* `Enter` is not drift, and the span is honestly labelled with the
  commit it chose (`probe@43915ed..HEAD`). A drift demo has to move the branch after the apply, which
  is what a reviewer with a second window open actually experiences.
- **A banner in the list column loses its key.** `⚠ probe moved …  [r] refresh` in a 24-to-48-column
  list column clips to `[r]…` at 110 terminal columns with the preview open — a warning that has lost
  the thing to press. The footer spans the terminal, so the banner lives there.
- **Pinning is load-bearing in more than one place.** Making `resolve` honour a pinned OID, and
  writing pins back into the session's selector, is what makes drift observable at all — and it
  quietly changed another promise: a deleted ref now keeps resolving to its pin, which is right for
  drift and meant a plan claim about broken span stops had to be rewritten.

## Risks

- **Mode checks scattered through the key handler.** One gate function, and a test per mutating key
  that state is unchanged in historical mode.
- **The picker becoming a second TUI.** It is a model mode with the same update path and the same
  frame invariants; no nested program.
- **Pinned refs and the reviewed marks disagreeing after `r`.** Marks are keyed on the end commit, so
  a refreshed base simply matches nothing; the report tells the reviewer why the counter dropped.
  Realised, and behaved as designed. The same keying bit a different way on span *navigation*, where
  the marks were expected to come back — fixed in `9b5a877`, see *Known gaps*.
- **`gitpr diff`'s meaning drifting.** `gitpr diff` with no flags is base → pinned HEAD, exactly as
  today; the `you` section belongs to the preview, not to `diff`.

## Verification Strategy

Unit tests in `internal/span` for resolution and drift against the `gittest` harness with real git
objects; CLI tests for every new flag including mutual exclusion; TUI tests per refused key and for
the header; hygiene test still proving nothing pushes, merges, rebases or resets; a pty walkthrough
per acceptance scenario in the requirements, with the raw bytes checked rather than a screen model.

## Audit History

| Date       | Audit                                                      | Summary |
| ---------- | ---------------------------------------------------------- | ------- |
| 2026-09-16 | `9b5a877`, `d0ceee7`, `1575437` | The three defects the audit recorded, fixed in the order it listed them: marks across a span round trip, then the header's span name, then the read-only `ABOUT.md` write. Each fix was falsified by running its test against the code before it. Coverage the audit recommended and these commits did not take on: a multi-ref drift case, a `review open` flag-registration test, and criterion 1 at the session level. |
| 2026-09-16 | [audits/2026-09-16-completion.md](audits/2026-09-16-completion.md) | Completion audit, run twice — one pass per reviewer, findings merged. M1–M5 verified against the branch; no milestone incomplete. Plan corrected for five stale or false claims (the `V`/`r` note; a `--base` flag that never existed; a broken-span-stop claim overtaken by pinning; requirements-file departures that were under-described; a flattened M1 task line), two orphaned bullets moved to *Discoveries*, evidence labelled honestly where it was a hand run rather than a test, and criteria 1–3 and 5 qualified where the claim outran the proof. Three shipped defects recorded under *Known gaps* and not fixed: marks lost on a span round trip, `span: unreviewed` shown for any review-based span, and `ABOUT.md` recreation reachable in read-only mode. Plan archived to `docs/plans/completed/`. |
