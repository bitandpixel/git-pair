# Review Span Selection — Execution Plan

Requirements: [requirements.md](requirements.md) (owner handoff, reproduced in full).

## Goal

A review span becomes a pair of explicit checkpoints, `BASE → HEAD`, resolvable through review
submissions, commits, refs, the changeset base and the working tree. The head decides whether the
session is a live review or a read-only look at history. The common cases stay one key long.

## Success Criteria

Observable outcomes, not implementation details:

- `gitpr review open` still opens the full changeset, unchanged (§21's "existing behavior" holds).
- A span whose head is not the working tree opens **read-only**: no marking, no submitting, no
  editing of product files or `ABOUT.md`, no new threads, and the screen says so.
- Both ends of a span can be named by review index, commit id, or ref, from the CLI now and from
  the `V` picker later; all of them resolve through one implementation shared by CLI and TUI.
- A ref chosen as a checkpoint keeps its name on screen, diffs against the commit it pointed at when
  chosen, reports drift without acting on it, and changes only when the reviewer says so.
- Reviewed marks are never reused across a span change, and remain available if the span goes back.

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
- Keys `V` and `r` are unbound.

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
  `--head-ref=NAME`. Base-side equivalents wait for M3, since `--since-review` and `--base` cover the
  base end for now.
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
- [x] CLI head flags, mutually exclusive with each other. `diff` has them now; **`review open` does      not until M2**, because a screen that ignores the mode would be worse than no flag. `register`      and `registerHead` are separate so a command can take one without the other.
      `--unreviewed`/`--since-review` where they conflict.
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
  the default span unchanged. `review open` gains the flags in M2.

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
- [ ] Base-side CLI flags (`--base-review`, `--base-commit`, `--base-ref`), so a scripted call can
      name a base the way the picker does. Deferred: `--since-review` already covers the review case,
      and nothing else in the plan needs them.

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
- [x] A stop whose ref was deleted leaves the span, the ring and the position alone, and works
      again once the ref is back.
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

- **Where a pin is taken matters more than where it is stored.** The picker's ref list is a snapshot,
  but the pin is taken when the reviewer applies the span. In the pty walkthrough that ordering was
  visible: a branch moved *before* `Enter` is not drift, and the span is honestly labelled with the
  commit it chose (`probe@43915ed..HEAD`). A drift demo has to move the branch after the apply, which
  is what a reviewer with a second window open actually experiences.
- **A banner in the list column loses its key.** `⚠ probe moved …  [r] refresh` in a 24-to-48-column
  list column clips to `[r]…` at 110 terminal columns with the preview open — a warning that has lost
  the thing to press. The footer spans the terminal, so the banner lives there.

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

## Risks

- **Mode checks scattered through the key handler.** One gate function, and a test per mutating key
  that state is unchanged in historical mode.
- **The picker becoming a second TUI.** It is a model mode with the same update path and the same
  frame invariants; no nested program.
- **Pinned refs and the reviewed marks disagreeing after `r`.** Marks are keyed on the end commit, so
  a refreshed base simply matches nothing; the report tells the reviewer why the counter dropped.
- **`gitpr diff`'s meaning drifting.** `gitpr diff` with no flags is base → pinned HEAD, exactly as
  today; the `you` section belongs to the preview, not to `diff`.

## Verification Strategy

Unit tests in `internal/span` for resolution and drift against the `gittest` harness with real git
objects; CLI tests for every new flag including mutual exclusion; TUI tests per refused key and for
the header; hygiene test still proving nothing pushes, merges, rebases or resets; a pty walkthrough
per acceptance scenario in the requirements, with the raw bytes checked rather than a screen model.

## Audit History

| Date       | Audit | Summary                            |
| ---------- | ----- | ---------------------------------- |
| —          | —     | No audits performed yet.           |
