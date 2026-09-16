# Audit — Review Span Selection — 2026-09-16

Immutable record. Subject: `docs/plans/active/review-span-selection/plan.md`, against branch
`gitpr/init-commits` at `eeb0919`. Two independent passes: one auditor with a shell (ran the suite,
ran a probe), one read-only reviewer (static reading, cross-counts). Findings are merged; where the
two passes agree, that is stated.

## Summary

All five milestones are implemented, committed and verified — no milestone is incomplete, and every
deliverable named in the plan exists. The plan's *wording* had drifted in five places, one of which
asserted a flag that never existed, and its evidence column conflated tests with hand-run terminal
sessions.

The audit also found three defects in shipped behaviour. Two contradict text in the plan itself:
reviewed marks do not survive a span round trip inside a session (a Success Criterion and an owner
decision), and the header prints `span: unreviewed` for any review-based span, so `--since-review=-3`
announces the wrong span. The third is narrow: a historical span can recreate `ABOUT.md` and open it
for editing, in a mode whose promise is that it changes nothing. None were fixed here — an audit
recommends rather than implements — and none were left as failing tests in the tree.

## Scorecard

### Overall status

🟡 **Minor Drift** — nothing is unfinished or broken in the build, but plan text described the wrong
details in five places, one evidence class is unreproducible from a checkout, and three shipped
behaviours do not meet what the plan claims.

### Project health

| Category           | Status | Note |
| ------------------ | ------ | ---- |
| Plan Accuracy      | 🟡     | Five stale or false claims, corrected in place with the reason recorded |
| Milestone Progress | 🟢     | M1, M2, M3, M3b, M4, M5 done; each now maps to a commit |
| Deliverables       | 🟢     | Every deliverable in every milestone exists and behaves as described |
| Verification       | 🟡     | 328 tests, incl. a refusal test per mutating key and falsified layout claims. Gaps: the failing criterion had no test; `pty` rows are hand runs with no script in the repo; `review open`'s span flags untested; multi-ref drift untested |
| Documentation      | 🟡     | README and PRD matched behaviour except two statements corrected during this audit (sample shortcut bar, one-revision difftool claim) and the requirements header. One copy item left open: `--tool`'s flag help, which is Go source |
| Technical Debt     | 🟡     | The three *Known gaps*, plus the finished MVP plan still sitting in `active/` with suite totals frozen at 237/13 |
| Risks              | 🟢     | `internal/hygiene` green: nothing pushes, merges, rebases or resets |

### Milestone status

| Milestone | Progress | Verified |
| --------- | -------- | -------- |
| M1 — checkpoints, nameable head | 100% (`cc7dc6c`) | Yes — `internal/span` checkpoint/drift tests, CLI flag tests |
| M2 — read-only historical mode | 100% (`8b9dd9d`) | Yes — six tests in `readonly_internal_test.go`, incl. one-ref-vs-two-rev difftool and the single-fetch preview claim; header/refusals also by hand, unscripted |
| M3 — the `V` picker | 100% (`efd9474`) | Yes — 16 tests in `picker_internal_test.go`, incl. `TestPickerNeverOffersHEAD`, `TestThePickerFillsTheWindow` |
| M3b — `v` walks session spans | 100% (`2bc714e`) | Yes for walking, dedup, escape-from-history and broken stops. **No** test for marks returning, and they do not |
| M4 — drift and `r` | 100% (`ebaeccd`) | Yes for a single drifted ref. The `(+N more)` branch and `RefreshDrift`'s loop over several refs are untested |
| M5 — base-side CLI flags | 100% (`eeb0919`) | Yes on `diff`: `TestDiffBaseCheckpoints`, contract rows. Nothing exercises `review open`, which cannot run headless |

## Findings

### Defects in shipped behaviour

1. **Marks do not survive a span round trip inside one session** — contradicts Success Criterion 5
   and the owner's decision that marks survive a span change and remain available if the span comes
   back. Probe (run during the audit, then deleted): mark `service.go` in the full changeset, `v` to
   the unreviewed span, `v` back → `reviewed=0/5`. Cause, in order of biting: `scan` rebuilds the
   file list and its only in-session carrier is the previous list's diff keys, which belong to the
   span just left; `Session.persisted` is read once in `NewSession` (`loadMarks`) and never re-read
   or updated; `toggleMark` → `SaveMarks` writes the *whole* current set, so the emptied set reaches
   disk on the next toggle and a later `review open` on the same commit resumes without marks it had.
   **Independently confirmed** by the second pass from `session.go:187-198`, `session.go:575-590`,
   `session.go:96` and `reviewmark.go:96`. Reachable through the ring M3b shipped. Written up in the
   plan under *Known gaps*.

2. **The header calls every review-based span `unreviewed`** — `Session.Unreviewed()`
   (`session.go:361-363`) tests only `base.Kind == KindReview && head.Kind == KindWorkingTree`, and
   `spanName` (`tui.go:964-969`) substitutes the word for the span label. So `review open
   --since-review=-3`, `--base-review=-3`, and the picker's `Review -3` base all display
   `span: unreviewed` for a span starting three submissions back. Contradicts Success Criterion 3 and
   requirement §17 (the current span should always be visible). `Unreviewed()` is asserted only for
   the genuine unreviewed span (`session_test.go:246`), and no `spanName` test uses a review base
   other than `-1`. Fix is a condition — base must be the *latest* review — plus a test; the plan now
   records it rather than the current behaviour.

3. **A historical span can recreate `ABOUT.md`** — `openArtifact` (`tui.go:684-692`) lets `ABOUT.md`
   through the read-only gate when the historical span touched it and it existed at the span's start;
   the file is then absent from the worktree, and the path calls `EnsureAbout` and opens the editor.
   A write and an edit in the mode that promises neither. Needs `ABOUT.md` missing from the worktree,
   so it is narrow — but Success Criterion 2 is stated absolutely and nothing covers this path.

### Plan and documentation drift

4. **False claim.** M1 justified deferring base-side flags with "since `--since-review` and `--base`
   cover the base end" — no `--base` flag exists on `diff` or `review open`, and never did, so the
   base end was not covered for an arbitrary commit or ref. Rewritten; the base trio is recorded as
   its own item (M5, `eeb0919`). Confirmed independently.

5. **Claim overtaken by later work.** M3b's verification said a *deleted ref* makes a span stop
   unresolvable. After M4's pinning a deleted ref still resolves to its pin, so
   `TestStepSpanStopsShortWhenAStopNoLongerResolves` breaks a stop by deleting a *tag* that a typed
   `Commit…` endpoint named. Plan now says so, and why the new form is the realistic case.

6. **Stale claim.** Context said "Keys `V` and `r` are unbound" — true when written, false since M3
   and M4. Annotated in place, since that section records the pre-plan state.

7. **Broken plan text.** M1's CLI-flags task had been flattened into one long line carrying a dangling
   fragment (`… one without the other.` / `` `--unreviewed`/`--since-review` where they conflict.``),
   which read as a rule the code does not implement. Rewrapped and completed.

8. **Evidence column overclaimed its class.** Rows credited "pty" rest on terminal sessions run by
   hand; no script for them is in the repository, unlike the sibling MVP plan's `artifacts/e2e-29.sh`.
   The Status section now says plainly that hand-run evidence is not reproducible from a checkout, and
   which rows are tests.

9. **Two success criteria outran their proof.** Criterion 1 (`review open` still opens the full
   changeset) is an inference from `diff`'s default-span test plus the shared flag set, because
   `review open` refuses to run without a terminal (`review_test.go:421-430`, `workflow_test.go:128`);
   criterion 2 (nothing editable in read-only mode) is defeated by finding 3. Both now carry the caveat
   where the claim is made. Criterion 3 carries finding 2.

10. **Two verified discoveries were filed under the wrong heading** — the pin-timing and
    banner-clipping notes sat as unlabelled bullets after M4's checkboxes, where a reader would take
    them for unchecked tasks. Moved to a new *Discoveries* section with a third entry.

11. **`requirements.md` departed from the handoff in more ways than the plan admitted.** It gained §8.1
    and a rewritten Scenario A (`2bc714e`), its header asserted a non-existent "audit notes at the end"
    section, and §24/§30 still describe `v` as a two-state toggle that §8.1 supersedes. The plan now
    lists the amendments and the deliberate survivals; the file's own header states both, since a
    reader cannot otherwise tell handoff from amendment.

12. **Two README statements were behind the code** — the sample shortcut bar omitted `V picker`, and
    the difftool paragraph asserted the one-revision form unconditionally, contradicted by
    `difftoolHead`, `console.DiffToolCommand`, `TestDiffToolCommandCarriesTheSecondRevisionWhenThereIsOne`,
    and by README's own span section. Both corrected in this commit. Left open as recommendation 5:
    `--tool`'s flag help (`internal/cli/diff.go:62`) still says the tool always compares against the
    working tree — that string is Go source, so the audit did not touch it.

13. **No completion metadata.** The plan had no status, no milestone→commit mapping, no suite totals. A
    Status section in the MVP plan's shape now carries them.

14. **Coverage gaps worth naming.** Every drift test moves exactly one ref
    (`drift_internal_test.go:54,88,103,148`, `session_test.go:392-459`), so M4's multi-endpoint banner
    and refresh loop are unverified; and no test registers `review open`'s span flags.

## Evidence

Run by the auditor (has a shell):

- `mise run check` at `eeb0919` — build, `go vet`, `gofmt`, tests: green. 14 packages, 328 test functions.
- Named tests read for each claim: `internal/span/checkpoint_test.go` (pinning, drift, refresh),
  `internal/tui/readonly_internal_test.go` (6), `internal/tui/picker_internal_test.go` (16),
  `internal/tui/session_test.go` (`TestSessionStepSpan*`, `TestRefreshDriftRepinsAndSaysWhatItCost`,
  `TestSessionStaysOnTheRefItPinned`), `internal/tui/drift_internal_test.go` (6),
  `internal/cli/statusdiff_test.go` (`TestDiffHeadCheckpoints`, `TestDiffBaseCheckpoints`),
  `internal/cli/contract_test.go`, `internal/git/git_test.go`
  (`TestRecentCommitsAndRefTipsCarryWhatAPickerNeeds`).
- Experiment for finding 1: a temporary `internal/tui` test doing mark → `v` → `v`, run with
  `go test -run TestAuditProbe -v`, failing at `reviewed=0/5`; deleted afterwards, so no red test was
  committed.
- Flag inventory from `internal/cli/diff.go` `register`/`registerHead` and `internal/cli/review.go:84-85`
  for findings 4 and 14; `session.go:361-363` and `tui.go:964-969` for finding 2; `tui.go:684-692` for
  finding 3; `git log --format=%cd -- docs/.../requirements.md` for finding 11.

Reported by the read-only reviewer (no shell; static reading only), and treated as corroboration
rather than execution: milestone→commit mapping cross-checked against `.git/logs/HEAD`; suite counts
re-derived per package (tui 105, cli 94, lifecycle 22, survival 26, span 16, changeset 13, gittest 11,
reviewops 8, reviewmark 7, console 6, model 6, reviewref 6, git 5, hygiene 3 = 328, 14 dirs) — which
matches the auditor's `mise run check` totals exactly; and finding 1's mechanism reached independently.

## Recommendations

1. **Fix the mark round trip before more span work** (`internal/tui/session.go`). Key in-session marks
   by `(span key, path)` rather than path alone, or re-read the mark store for `Span.To` on every span
   change. Land the probe as a regression test beside `TestSessionStepSpanWalksFullChangesetAndUnreviewed`
   — eight lines, and it is the only thing preventing a silent repeat.
2. **Fix `Unreviewed()`, and decide the label first.** The condition is "base is the latest review";
   the open question is what the header should say for a span based at an older review — the span label
   is already correct (`bb0f343..HEAD (after review 0)`), so the simplest change is to stop overriding
   it except for the genuine unreviewed span. One condition, one test.
3. **Close the `ABOUT.md` escape** by refusing the artifact-open path when the span is historical, and
   add the case to `readonly_internal_test.go`, where the other refusals are falsified per key.
4. **Add the three missing test classes**: multi-ref drift (banner `(+N more)` and a two-ref
   `RefreshDrift`), a `review open` flag-registration assertion that does not need a terminal, and
   criterion 1 at the session level.
5. **Sync the last copy item**: `--tool`'s flag help should not promise the working tree for a
   historical span.
6. **Capture the hand-run evidence.** The pty walkthroughs for M2–M4 are the plan's only non-scripted
   class; promoting the harness and fixture into `artifacts/`, as the MVP plan does with `e2e-29.sh`,
   would make the M2/M3/M4 rows reproducible.
7. **Audit and archive the MVP plan next.** `docs/plans/active/gitpr-mvp/` is finished work whose Status
   table still says "237 test functions, 13 packages" (now 328 / 14); `docs/plans/active/` should mean
   work in progress.
8. **The branch is still unpushed** — no remote is configured, so all of this exists on one machine.
   Worth resolving before the mark fix, which touches persisted state.

## Confidence

**High** on plan-versus-code alignment: the suite was run, every milestone claim traced to a named test
or an explicitly-labelled hand run, and the two claims that could not be traced tested directly
(finding 1 fails; finding 4's flag does not exist). The read-only pass independently reproduced the
milestone mapping, the suite counts and finding 1's mechanism, and disagreed with this audit's first
draft on the Documentation row and on evidence class — both corrections are reflected above.

**Medium** on the blast radius of finding 1: the mechanism is legible in the code and reproduced once in
a fixture, but the disk-erasure path (an emptied set overwriting a previous session's marks) was reasoned
about rather than demonstrated end to end.

**Medium** on finding 3's reachability: the path is clear in `openArtifact` and the gate, but the audit
did not drive it in a running session.

### What the audit changed in the tree

Plan text, README prose (two spots), the `requirements.md` header, and this file; plus `git mv` of the
plan directory from `docs/plans/active/` to `docs/plans/completed/`. No Go file was modified: the three
defects are recommendations 1–3, per the plan-audit contract.
