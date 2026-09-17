# Audit: gitpr MVP plan — completion audit

**Audited:** `docs/plans/active/gitpr-mvp/plan.md` (724 lines as committed at `c4ef81b`), audited
2026-09-16 against branch `gitpr/init-commits` at `30e4738`.
**Auditor:** two independent passes — pass 1 (assistant, with shell: tests, e2e replay, spike
script, source reads) and pass 2 (`reviewer` subagent, read-only, no shell). Findings merged below.
**Plan status:** Complete → **archived** to `docs/plans/completed/gitpr-mvp/`.
**Overall:** 🟢 Complete, with 🟡 documented drift — **all drift was in the document, none in behaviour**

## Scorecard

### Decisions

| # | Decision | Status | Evidence / Current State |
| --- | --- | --- | --- |
| D1 | Go | 🟢 Honored | 15 packages under `internal/` + `cmd/gitpr`; the plan's Python architecture block is the drift this audit found, not the build |
| D2 | Bubble Tea, model/render split | 🟢 Honored | `internal/tui/session.go` is headless and driven by tests with no TTY; the view lives in `tui.go` |
| D3 | Surviving additions block outside `changesets/<slug>/`; override flag | 🟢 Honored | `internal/survival` + `--allow-surviving-review-additions`; e2e stops on exactly the untouched review line, then passes with the override |
| D4 | Diff-tool handoff | 🟢 Honored, twice superseded | One revision for live spans, two pinned revisions for historical ones; the plan's note now says both |
| D5 | No `review undo` | 🟢 Honored | No `undo` subcommand; `README.md` explains the omission; D5's reasoning is the only place the repo records it |

### Milestones

| # | Milestone | Status | Evidence / Current State |
| --- | --- | --- | --- |
| M0 | Scaffolding and git core | 🟢 Complete | `mise run check` green at audit. The row cited `go build ./...` + `go vet`, which is not the project's check — noted in the row |
| M1 | `change init` | 🟢 Complete | 20 `TestChangeInit*` functions; `TestBaseIsOwnBranch` 11 cases |
| M2 | Lifecycle, `status`, `history` | 🟢 Complete | `TestStatusJSONEmitsPRDKeySet`, `TestJSONKeySets`, lifecycle staleness tests |
| M3 | Spans, `diff`, survival, `change ready` | 🟢 Complete | e2e replay drives the survival block and the override; the spike-caveat coverage the plan promised is real: `TestCheckSkipsBinaryFiles`, `TestCheckRenamedFileHasNoAdditionsAtTheOldPath`, `TestCheckSkipsBlankLineAdditions`, `TestCheckReviewCommitIsRepositoryRootCommit`, `TestCheckFileDeletedAtHead`, `TestCheckEmptyReviewDiff` |
| M4 | Submission, refs, queue, close | 🟢 Complete | e2e replayed at audit: `E2E: all checks passed`; archive ref reaches 14 commits after `git branch -D` (plan said 13; the extra commit is `change init`'s scaffold). Pass 2 traced why independently: the archive ref is written at the pre-close `HEAD`, i.e. the approve commit |
| M5 | Review TUI | 🟡 Complete, with unverifiable evidence | Behaviour verified by ~130 headless TUI tests plus hand runs under a pty. Two claims were false: the `TERM=dumb` smoke test never existed, and "difftool inside the TUI not yet verified" was stale — verified since. No pty script is in the repo, so that row is not reproducible from a checkout |
| M6 | Docs and dogfood | 🟡 Complete, two deliverables skipped | `README.md` exists and matches the CLI; `artifacts/e2e-29.sh` is the scripted replay. `CHANGELOG.md` and a "captured transcript" were listed and never delivered — now recorded as skipped, not deleted |

### Success Criteria

| # | Criterion | Status | Evidence / Current State |
| --- | --- | --- | --- |
| 1 | Non-interactive, no TUI without a terminal | 🟡 Qualified | `TestChangeReadyWithoutATerminal`, `TestReviewThreadWithoutATerminalExits`, `TestReviewSubmitWithoutATerminal`, `TestOpenArtifactRefusesWithoutATerminal`; the TUI itself is refused by `console.Interactive()`, not exercised headlessly in CI |
| 2 | Surviving additions with `path:line` + override | 🟢 Verified | e2e: `change ready` blocked on the untouched review line, passes after resolution and with the override |
| 3 | `review submit` records what was reviewed | 🟢 Verified | e2e: ref updated, review listed; empty submissions recorded (`TestReviewApproveEmptyRecordsAnEmptyReviewCommit`) |
| 4 | State from commit order alone | 🟢 Verified | e2e: `READY → BLOCKED → WORKING → FEEDBACK → APPROVED → CLOSED`, no stored phase read |
| 5 | Archive survives branch deletion | 🟢 Verified | e2e: 14 commits reachable after `git branch -D` |
| 6 | TUI usable for the §29 loop | 🟡 Qualified | Hand-verified under a pty, twice; ~130 headless tests cover the model. Not reproducible from a checkout — the harness scripts are not in the repo |
| 7 | Spans addressable; base always stated | 🟢 Verified | Span plan + `TestDiffBaseCheckpoints`, `TestReviewSpanFlags`, `TestDiffSpanFlags`, `TestSpanFlagsAreMutuallyExclusive`, `TestReviewReopenWithoutReviews`/`AfterTheAuthorResponds` |
| 8 | Nothing in derived state; clean tree after | 🟢 Verified | `mise run check`; e2e asserts no `changesets/` path inside a review commit |
| 9 | Agent-appropriate output; hints for the next actor | 🟢 Verified | `TestJSONKeySets`, exit-code tests, `TestReviewSubmitPointsAtTheAuthorsCommand`, `TestSubmitHintNamesTheAuthorsCommand`, `TestDiffUnreviewedSuggestsTheAuthorsCommand` |
| 10 | Archive ref under `refs/reviews/archive/` | 🟢 Verified | e2e + `TestCloseWritesTheArchiveRefBeforeDeletingTheBranch` |
| 11 | Terminal restored | 🟡 Qualified | `teardown_internal_test.go` asserts the alt-screen enter/leave are a pair and that a return from a tool starts on a clean screen. No `TERM=dumb` run exists (the plan claimed one); no automation covers a real handoff |
| 12 | No destructive verbs | 🟢 Verified | `internal/hygiene` — see Verification coverage below |
| 13 | `--json` per PRD §11.1 | 🟡 Qualified | A **superset**: 16 fields against §11.1's 7. `TestStatusJSONEmitsPRDKeySet` asserts §11.1's presence, `TestJSONKeySets` pins the whole set. The plan said "exactly" |

### Risks & Mitigations

| Risk | Planned mitigation | Reality |
| --- | --- | --- |
| Literal §19 semantics make `change ready` fail on artifact noise | D3 scoping + override | In place, and exercised end to end |
| Staleness by commit count invalidates a correct `READY` | Tree comparison | `TestSummarizeChangesetOnlyCommitDoesNotInvalidateReady` and siblings |
| Branch deleted before close loses history | Archive ref at the newest review | e2e, 14 commits after deletion |
| Review submitted to the wrong branch | "`submit` prints branch + changeset + span" | **False as written.** `runReviewSubmit` prints changeset, outcome, commit, files, ref, supersedes, next — after the commit, never the branch or the span. The real guards are elsewhere: the changeset is the checked-out branch, `change init` refuses a detached HEAD, `TestChangeInitRefusesDetachedHead` |
| A `TERM` the library mishandles | "`TERM=dumb` smoke test", `--no-tui` hint | **Both fiction.** No such test, no such flag. What holds the risk: the session owns its alt screen (`teardown_internal_test.go`) and the TUI refuses to start with `--no-tui`-style guidance when there is no terminal |

### Verification coverage

| Layer | Exists | Evidence |
| --- | --- | --- |
| Unit | ✅ | 335 test functions in 38 files, 14 packages (`mise run check`, 2026-09-16) |
| Golden workflow | ✅ | `artifacts/e2e-29.sh` re-run at audit against the installed binary: all checks passed. It is a script, not a transcript; the plan claimed a captured transcript that was never kept |
| CLI contract | ✅ | `TestJSONKeySets`, `TestExitCodesAcrossCommands`, `TestDiffJSON*` |
| Git spike caveats | ✅ | Six named tests cover binary/rename/blank/root/deleted/empty — the plan's claim here was accurate and is the rarest kind of claim to keep honest |
| TUI handoff | 🟡 | Real `diff.tool`/`GIT_EDITOR` handoffs run by hand under a pty; harness scripts not in the repo |
| Source hygiene | ✅, and stronger than claimed | Not a grep. `internal/hygiene` parses every Go file: a git-argv field equal to a forbidden verb, a bare verb string literal, and verb-bearing shell text inside `exec.Command`. Forbidden set is `push`, `merge`, `rebase`, `reset`, `switch`, `checkout`, `branch`. `internal/gittest` is excluded and that exclusion is itself asserted, so fixtures cannot quietly widen the rule |

## Confirmed Drift

| Severity | Claim (in the plan) | Reality (in the tree) | Resolution applied |
| --- | --- | --- | --- |
| P1 | `## Architecture` is a Python package: `src/gitpr/*.py`, `pyproject.toml`, `uv`, `pytest`, `tui.py` with curses | 15 Go packages + `cmd/gitpr`; D1 chose Go before any code existed, and the section was never updated | Added an **As built (Go)** tree naming what each package owns; retitled the original **As planned (superseded by D1; kept for the responsibility split)**. Also corrected its opening line: "Single package… no cache" — there are fifteen packages, and `internal/reviewmark` is a cache by design (discovery 13), so the invariant was restated as "no cache of *derived* state" |
| P1 | The as-built tree itself omitted `internal/cli` | The command surface is the largest package in the suite | Added, with hygiene's real mechanism |
| P1 | M5: `review reopen` "falls back to the covered span (`span.Covered`)", Status row crediting three covered-span tests | `span.Covered`, the fallback and all three tests were deleted in `cc7dc6c`; `reopen` resolves `Review(-1) → WorkingTree` | Status row and M5 bullet rewritten to what ships; discoveries 11 and 12 annotated as superseded with their lessons kept. Cross-linked to the span plan's audit |
| P1 | M5: "`TERM=dumb` smoke test asserting clean startup/teardown" and Risks' same claim + a `--no-tui` hint | Neither exists (`grep -rn TERM internal` → only `GIT_TERMINAL_PROMPT`). What exists is `teardown_internal_test.go`: alt-screen sequences are a pair, returning from a tool starts on a clean screen | Both claims replaced with what the tests actually assert, and the missing coverage named as missing |
| P1 | M5: "Not yet verified: `Enter` launching a real difftool inside the TUI" | Verified since, against a configured `diff.tool`, under a pty, during the preview and span work | Gap closed, with the reproducibility caveat stated in the same row |
| P1 | M4: "the archive ref still reaches 13 commits" | 14, on a re-run of the plan's own script; the extra commit is `change init`'s scaffold commit | Row updated to 14 with the cause, plus the command to reproduce. Pass 2 independently traced the count to the approve commit |
| P1 | Success Criterion 11 contained a truncated sentence (a line beginning `toggles, `), an outdated key list, and `v` as a span toggle | The paragraph was cut mid-edit; keys now include `Tab`, `d`, `p`, `v`/`V`, `r`, `s`; `v` walks a ring of visited spans, `V` opens the picker | Criterion rewritten to the delivered behaviour |
| P1 | M6 deliverable: `CHANGELOG.md`; M6 deliverable: "transcript captured under `artifacts/`"; Verification Strategy: "plus a scripted transcript in `artifacts/` from a manual run" | No `CHANGELOG.md`; `artifacts/` holds only `e2e-29.sh`; no transcript was ever kept | All three recorded as not delivered, with the reason the script is the better artifact — kept visible so no one reads a clean plan and assumes otherwise |
| P1 | M1 evidence: "idempotence **and base-conflict** paths exercised by `artifacts/e2e-29.sh`" | The script has no conflicting-base step; that path is `TestChangeInitBaseConflict` | Row corrected to credit the test, with the one-line script addition that would make the claim true in the artifact |
| P1 | Discovery 10: "a span with no files in it refuses with exit 1 and names the review it is relative to" | Discovery 11 deleted that guard; an empty span now opens with a stderr note, pinned by `TestReviewReopenReachesTheSessionWhenNothingHasLanded` | Parenthetical in discovery 10 pointing at 11 |
| P2 | Core invariant: survival comes from `git diff -U0 --no-renames <R>^ <R>` | `survival.AddedLines` runs `git show -U0 --no-ext-diff --no-textconv --no-renames --format= <R>` plus `git show --numstat` | Invariant rewritten, including why: `git show` *is* the first-parent diff and needs no root-commit special case (which is why M3's "root-commit handling" task needs no code), and `--no-ext-diff`/`--no-textconv` keep the output parseable against a user's configured tools |
| P2 | Hygiene described as "a test that greps the source" (three places) | A syntax-level scan with three layers and seven forbidden verbs | Rewritten in all three places; a reader told "grep" may go write one |
| P2 | Risks: "`submit` prints branch + changeset + span before committing" | Prints changeset/outcome/commit/files/ref/supersedes/next, after the commit; never branch or span | Cell rewritten to the output that exists, and to where the real protection lives |
| P2 | M2 deliverable: "`status` with the PRD JSON key set exactly" | 16 fields — §11.1's seven plus nine the dogfood needed | "Exactly" → superset, with both pinning tests named |
| P2 | Risks: span printed as `base...HEAD @ <sha>` | `span.label` produces `<base>...HEAD`, `<short>..HEAD (after review N)`, `<ref>@<sha>..<sha>` | Quoted form replaced by the real ones; the row's underlying promise (the range is never implicit) holds and is asserted by `TestReviewSpanReopenNamesTheSpanItResumes` |
| P2 | "Not attempted: Windows" | One Windows branch exists — the executable lookup in `internal/console` | Qualified: never run on Windows |
| P2 | Suite totals "29 files, 237 test functions, 13 packages" | 38 files, 335 functions, 14 packages | Updated. Pass 2 counted independently and agreed |
| P3 | Status evidence counts: `TestBaseIsOwnBranch` (9), `TestChangeInit*` (11), `TestPollUntil*` (5) | 11 cases, 20 functions, 6 cases | Corrected |
| P3 | Discovery 13: "clearing every mark writes an empty set so the old ones do not reappear" | Since `9b5a877`, clearing writes the empty set for *that span's keys* and deliberately leaves other spans' keys on disk | Sentence scoped to "every mark in that span", pointing at the correction paragraph below it |
| P3 | Discovery 15's "known gap, not fixed" (unselected rows wrap); discovery 21's "list never below 34" | `clip()` trims every row; the list is sized from content, clamped 24..48 (discovery 23) | 15 marked closed by 21; 21's floor annotated as superseded by 23 |
| P3 | Spikes/Research: "M5's terminal-restore behaviour is validated by its own smoke test" | Contradicted by M5's own corrected text two sections apart | Now cites the alt-screen pairing tests and the pty hand runs |
| P3 | Status rows and Audit History ended at 2026-09-16 "covered span ends at the submission's parent" | Five work streams happened after that row: author commands, unified list, preview pane, the entire span plan, and three fixes | Five rows added, including this audit |
| P3 | Milestone Tasks written against `.py` files and `uv`/`pytest` | Built in Go with mise | Prefaced rather than rewritten: the preamble maps `lifecycle.py → internal/lifecycle`, `uv run pytest -q → mise run check`. Preserves the record of what was asked for, which a silent rewrite would have destroyed |

Two pass-2 findings were **not** adopted: the suggestion to delete the Python sketch outright (kept, labelled — an archived plan should show what was decided, not only what shipped), and the description of `internal/hygiene` as "three layers over `go/parser` + `go/ast`" while calling it a scan of "every git invocation" (the layers are three *strategies*, not three AST passes; the wording in the plan says what it checks).

## Two-pass merge

Pass 1 (with shell) found and fixed the architecture/toolchain drift, the covered-span claims, the
two false M5 verification claims, the M4 count, the totals, Criterion 11's truncation, the missing
`CHANGELOG.md`/transcript, the D4 second supersession, and the missing Audit History rows. Pass 2
(read-only) found what pass 1 missed, all of it deeper in the document: the wrong git command in a
**core invariant**, hygiene described as a grep in three places, a Risks mitigation describing
output the command does not produce, `status --json` "exactly" where it is a superset, a quoted
label format that has never been printed, the base-conflict credit to a script with no such step,
three wrong evidence counts, discovery 13's unscoped clearing sentence, the Windows clause, and the
four cross-references that break on the move.

Pass 2 also flagged the concurrency honestly: pass 1 was editing the file while pass 2 read it, so
pass 2's line numbers drifted and it reported against the latest text. **This artifact cites
identifiers — function names, test names, commands — rather than line numbers, because line numbers
rot within a commit and both passes produced some that were already stale.**

## Recommendations (by priority)

1. **Repoint the four cross-references in the same commit as the move** — `README.md`,
   `internal/survival/survival.go` (a doc comment naming the research file), and each script's own
   usage line. After the move, `git grep -n 'plans/active/gitpr-mvp'` should return only the two
   audit artifacts — this file and `docs/plans/completed/review-span-selection/audits/
   2026-09-16-completion.md` — both of which name the pre-move path because that is where the thing
   they audited lived when they audited it. Both are immutable records; neither is rewritten to look
   tidy. *(Done in the move commit.)*
2. **Add the missing base-conflict step to `e2e-29.sh`** (`$G change init --base other` → expect 2),
   so the M1 row's claim is true of the artifact and not only of the unit test. *(Not done.)*
3. **Promote the pty harness into the repo**, as `artifacts/pty-*.sh` beside `e2e-29.sh`. Three
   separate audit rows now say "verified by hand under a pty" with nothing in the tree to re-run;
   that is the plan's largest reproducibility gap, and it is cheap to close. *(Not done.)*
4. **Fix the duplicate case row** in `TestBaseIsOwnBranch`: `"main on a branch off main"` appears
   twice with identical inputs, so a failure prints an ambiguous label and one case is dead weight.
   *(Not done — test-only change, left for the owner's call.)*
5. **Write the `TERM=dumb` run the plan promised, or say in the README that the TUI is tested
   headlessly and by hand.** The claim is now corrected in the plan; the gap is still open.
   *(Not done.)*
6. Carry forward from the span plan's audit, still open there and not duplicated here: the `--tool`
   help line in `internal/cli/diff.go` promising a working tree for any span; a terminal-free span
   registration test for `review open`; a multi-ref drift test; the criterion-1 test at the session
   level.

## Open Items / Follow-Up

- Items 2–5 above.
- `CHANGELOG.md`: still absent. Either write one or record the decision in the README — an
  MVP with 55 commits and no changelog is fine, an MVP whose plan promises one is a trap.
- Windows: one branch exists, unexercised. Leave as "not attempted" until someone runs it.
- No remote is configured in this repository, so nothing on any branch has been pushed; every
  result above is local.

## What should not be changed

- **The discovery bodies.** Twenty-four numbered discoveries, and the ones spot-checked against
  source all held: `marker.Message.Render` builds the trailer block directly; `git.ExitCode` and
  `git.Error` carrying stdout are exactly as described; staleness compares trees with the commit
  count left as a counter; `git var GIT_EDITOR` resolves the editor; no `tea.WithAltScreen`, the
  session writes its own `?1049h`/`?1049l`; the preview constants (`previewMinWidth` 100, list
  clamped 24..48, `previewMinPane` 40) match discovery 23's arithmetic. A plan whose discoveries age
  this well is the reason to write them.
- **The "As planned (superseded)" framing** for the Python sketch, and the Milestones preamble that
  tells a reader how to read the Python task lists. Deleting the sketch would have made D1 look
  obvious; it was not obvious, it was one of five decisions with real trade-offs.
- **D5 and the "no undo" evidence** — the only place the repo records why there is no `review undo`,
  and the `supersedes` output it cites is real.
- **The spike-caveat row** in Verification Strategy: the rarest kind of planning claim, a promise of
  specific tests that turned out to have specific tests.
