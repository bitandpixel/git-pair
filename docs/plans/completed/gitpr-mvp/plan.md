# gitpr MVP — Technical Plan

## Goal

Ship a working `gitpr` CLI that implements the PRD's review protocol: changeset scaffolding,
lifecycle markers derived from Git commits, review submission with machine-readable
outcomes, review-relative diff spans, the surviving-review-additions safety check, review
archive refs, a machine-readable queue, and a deliberately small orchestration TUI that
delegates all code viewing/editing to `$EDITOR` and Git's difftool.

## Success Criteria

Observable end-to-end behaviour, per PRD §29:

1. `gitpr change init --base <ref>` creates `changesets/<slug>/CHANGESET.yaml` + `ABOUT.md`
   deterministically from the branch name, and is idempotent (never clobbers existing data).
2. `gitpr change ready` refuses to create a ready marker when non-blank additions from the
   most recent review submission still exist unchanged at `HEAD`, prints them with
   `path:line`, exits non-zero, and stays fully non-interactive. `--allow-surviving-review-additions`
   overrides it.
3. A ready marker produces a commit carrying `GitPR-State: ready` and `GitPR-Changeset: <slug>`,
   and makes the changeset appear in `gitpr review queue`.
4. Any later implementation commit returns effective state to `WORKING` and removes the
   changeset from the queue — with no mutable state file involved.
5. `gitpr review submit --block|--feedback|--approve` creates a commit with `GitPR-Outcome:`
   and `GitPR-Changeset:` trailers; `--approve` works on a clean tree via an empty commit;
   `refs/reviews/<slug>` is updated to the exact resulting `HEAD`.
6. `gitpr review history` lists only GitPR review submissions, chronologically indexed from
   `0`, addressable as `-1`.
7. `gitpr diff`, `gitpr diff --unreviewed`, `gitpr diff --since-review=-3` and
   `gitpr diff <path>` resolve to the exact commit ranges in PRD §17. `diff` and `review open`
   now also take `--base-*` and `--head-*` to name either end, which added ranges without
   changing these.
8. `gitpr status --json` emits the PRD §11.1 shape and reflects the derived state.
9. `gitpr review queue --json` gives automation a stable contract.
10. `gitpr review close` verifies a clean tree, a permitted latest outcome, runs the surviving
    check (overridable), writes `refs/reviews/archive/<slug>/<sha>`, marks the changeset
    closed, and never merges/pushes/squashes. After the source branch is deleted, the complete
    unsquashed history stays reachable from the archive ref.
11. `gitpr review open` runs the TUI: file list for the resolved span, per-file reviewed
    marks, and difftool/editor handoff. `gitpr review reopen` is the same session on the
    `<latest review>..HEAD` span, for returning to a changeset already reviewed. Bindings:
    `j`/`k` move, `Tab` switches section, `Enter`/`d` diff, `p` preview, `e` edit, `Space`
    reviewed, `a` for `ABOUT.md`, `t`/`T` for threads, `v`/`V` for spans, `s` submit, `q` quit,
    with the terminal correctly suspended/resumed around external processes.

    As written this criterion predates two changes: the key list above is the current one (it
    grew `Tab`, `d`, `p`, `V`, `r`, `s`), and `v` is no longer a two-state span toggle — it walks
    the spans the session has been in, which the Review Span Selection plan specified. See
    *Follow-on plans*.

Nothing above requires a forge, network access, or a third-party review API.

## Context

- Greenfield repo: only `PRD.md` exists, no commits yet on `main`.
- Environment as surveyed before planning: git 2.43.0, Python 3.14.5 + uv 0.11.17, Node 24,
  Go 1.26.3, no Rust toolchain, `VISUAL=vim`, `EDITOR=vim`. Two of those are historical: D1 chose
  Go, and the build runs on **Go 1.27.1 through mise** (`mise run build|test|check`) — never
  `go install`, never the Python toolchain the original sketch assumed. git 2.43 is still the
  version that matters, and it has quirks worth knowing (`%(objectname:strip=2)` is rejected,
  an annotated tag carries `taggerdate` and no `committerdate`).
- Plumbing feasibility is already established — see [research/git-plumbing-findings.md](research/git-plumbing-findings.md).
  The hard parts (trailer parsing, span resolution, `-U0` added-line extraction, binary/rename
  handling, ref-based archival, reachability after branch deletion) are verified.

## Constraints

From the PRD, treated as binding:

- No source editor, no custom diff renderer, no merge/squash/PR management (§26).
- Forge-independent: no forge API in any code path.
- Non-interactive commands (`change ready`, `status`, `diff`, `queue`, `submit`) must never
  prompt, so agents can drive them.
- `about`/`thread`/TUI must suspend the terminal cleanly around external processes.
- Local-first: remote propagation is out of scope.

## Assumptions

1. One author + one reviewer, one changeset per branch (§3).
2. Review submissions are always non-merge commits on the changeset's own branch.
3. The changeset directory is tracked in Git (it is review state inside the repo).
4. `changesets/<slug>/` is the only review-artifact location; nothing outside it is review state.
5. `base` in `CHANGESET.yaml` is a ref name resolvable in the local repo (a branch for stacked
   changesets, e.g. `base: booking-transaction`).
6. A changeset directory's `CHANGESET.yaml` is authoritative once the directory is identified;
   the directory is identified from the current branch name.

## Decisions (resolved)

| ID | Question | Decision |
| --- | --- | --- |
| D1 | Implementation language / toolchain | **Go 1.27.1** (cobra for the command tree, Bubble Tea for the TUI) |
| D2 | TUI implementation | **Bubble Tea**, with a headless `Session` model so review state is testable without a TTY |
| D3 | Scope of the surviving-review-additions blocking set | **Block only on additions outside `changesets/`**; surviving `ABOUT.md`/thread additions are reported as a non-blocking list (see findings §"Design problem") |
| D4 | What `Enter` does in the TUI | **`git difftool <span> -- <path>`**, honouring the user's configured `diff.tool`. Superseded twice: by the review-experience pass (live spans run `git difftool <from> -- <path>`, one revision against the working tree, so the tool's right-hand buffer is the real file and edits persist) and by the Review Span Selection plan (a span whose head is a commit has no working tree in the comparison, so it passes both pinned revisions — `difftoolHead`, `console.DiffToolCommand`) |
| D5 | Undoing a review submission | **No undo — submit again.** The newest submission decides the state and earlier ones stay in history. Resetting the commit is out (the no-destructive-verbs invariant, and a review commit may be shared); a withdrawal marker would need a trailer older builds cannot read, leaving two gitpr versions disagreeing about one branch; moving the ref back changes nothing because state is derived from trailers. Owner chose documentation over either |

## Status (updated during implementation)

| Milestone | State | Evidence |
| --- | --- | --- |
| M0 scaffold + git core | done | `go build ./...`, `go vet` clean; `gitpr --help`. The project check is `mise run check` now (Context → Toolchain) |
| M1 `change init` | done | Idempotence exercised by `artifacts/e2e-29.sh` (it calls `change init --base main` twice and expects the second to be a no-op). **Corrected at audit:** the base-conflict path is *not* in that script — no conflicting-base step exists in it — it is `TestChangeInitBaseConflict`. Adding a `$G change init --base other; check … 2` line to the script would make the claim true in the artifact too |
| M2 lifecycle, `status`, `history` | done | e2e replay shows `READY → BLOCKED → WORKING → FEEDBACK → APPROVED → CLOSED`; `status --json` emits PRD §11.1's keys (`TestStatusJSONEmitsPRDKeySet`) plus nine more, pinned as a set by `TestJSONKeySets` |
| M3 spans, `diff`, survival, `change ready` | done | e2e replay blocks `change ready` on exactly the untouched review line, then passes after resolution and with the override |
| M4 submit, refs, queue, close | done | e2e replay re-run at audit (2026-09-16), all checks passed — reproduce with `mise run build && bash docs/plans/completed/gitpr-mvp/artifacts/e2e-29.sh ~/.local/bin/gitpr`, expect `E2E: all checks passed` (the script defaults `$G` to `/tmp/gitpr`, so pass the binary): empty approve commit, ref moves, and after `git branch -D` the archive ref still reaches **14** commits — one more than the 13 recorded when M4 closed, because `change init` now commits its scaffold. The promise being tested is that the whole unsquashed chain survives deletion, not the number |
| M5 TUI | done | Verified under a pty at the time: first paint, `j/k`, `space` (0/4 → 1/4 → 2/4), `v`, `a` editor handoff, `s`+`b` submit (created `review: block demo` and moved the ref), clean `q` exit. The gap recorded here — `Enter` launching a real difftool *inside* the TUI — has since been verified against a configured `diff.tool` under a pty, during the preview-pane and span work. **Evidence caveat closed after the audit:** the hand runs are scripted in `artifacts/pty-walkthrough.sh`, which drives the installed binary through a real pty against a repository it builds itself — first paint, a read-only historical span, the `V` picker, the `v` ring, a mark surviving `v` away and back, drift and `r` with a ref moved by another process, and the difftool handoff keeping the alternate screen |
| Post-MVP: `review reopen` opens the session on the since-review span | done, model replaced | `TestReviewReopenWithoutReviews`, `TestReviewReopenAfterTheAuthorResponds`. The covered-span fallback and its three tests (`TestReviewReopenFallsBackToTheSpanTheReviewCovered`, `TestResolveCoveredSpan`, `TestSessionToggleSpanFromCoveredGoesToTheFullChangeset`) no longer exist: once a span's head decides whether it is reviewable, a covered span — which ends at a commit — would have opened read-only. `reopen` is `Review(-1) → WorkingTree` now; discoveries 11 and 12 say what was lost and what was kept |
| Post-MVP: reviewed marks persist locally, keyed on the commit under review | done, keying corrected | `internal/reviewmark` tests, `TestReviewedMarksResumeInALaterSession`, `TestMarksDoNotResumeOnceTheCodeHasChanged`, `TestClearingEveryMarkIsRemembered`. A mark turned out to be a fact about *(path, diff key)* rather than path: two spans ending at one commit had been erasing each other's marks on save, and a mark did not come back when `v` returned to the span it was made in. Fixed in `9b5a877` with `TestMarksComeBackWhenTheSpanComesBack` and `TestSavingOneSpanLeavesTheOtherSpansMarks` (discovery 13) |
| Post-MVP: `k` at the top of the file list no longer panics | done | `TestNavigationStopsAtBothEndsOfTheList`, `TestNavigationOnAnEmptyListDoesNotPanic` |
| Post-MVP: staleness compares trees, not commit counts | done | `TestSummarizeChangesetOnlyCommitDoesNotInvalidateReady`, `TestSummarizeMixedCommitInvalidatesReady`, `TestSummarizeBaseMovingUnderAReadyChangesetKeepsReady` |
| Post-MVP: difftool shows the working tree; submit exits the TUI | done | `TestSubmitKeyEndsTheSession`, `TestEscInSubmitModeKeepsTheSessionOpen`; the one-revision difftool argv verified by hand against a configured tool |
| M6 docs + dogfood | done | `README.md`; `artifacts/e2e-29.sh` is the scripted replay and passes end to end |
| Post-MVP: refuse a self-referential base | done | `TestBaseIsOwnBranch` (11 cases) + init/ready/status CLI tests; reproduces and closes the owner's dogfood report |
| Post-MVP: `change init` commits, takes `--about` | done | `TestChangeInit*` (20 test functions) plus the corrected golden workflow; e2e replay still passes |
| Post-MVP: one TUI list, two sections — files, then ABOUT.md and nested threads with `+ new thread…` | done | `TestJRunsFromTheFilesIntoTheChangesetSection`, `TestTabSwitchesBetweenTheTwoSections`, `TestThreadsHeadingCollapsesAndExpands`, `TestEnterOpensWhatTheRowIsFor`, `TestSpaceMarksFilesAndRefusesTheRest`, `TestNewThreadRowSitsUnderItsThreads`, `TestThreadPromptKeepsSpacesInATitle` |
| Post-MVP: author-side `change feedback` and `change wait`, replacing `diff --unreviewed` in author hints | done | `TestChangeFeedbackShowsTheReviewItself`, `TestChangeFeedbackWithoutAReview`, `TestChangeWaitReturnsAtOnceWhenAReviewIsAlreadyIn`, `TestChangeWaitTimeoutReportsWhereThingsStand`, `TestChangeWaitFetchesAndSeesAReviewFromAnotherClone` (two clones, bare remote, no network), `TestPollUntil*` (6 cases) |

Test suite at audit (2026-09-16): 39 test files, 342 test functions across 14 packages (the audit's own 38/335 grew by the tests its recommendations asked for), all
passing; `mise run check` (build, `go vet`, `gofmt`, `go test`) green. It read 29 files / 237
functions / 13 packages when the MVP closed; the growth is the rows above, the Review Span
Selection plan, and the fixes from its audit. The suite found four real defects, all fixed in `fix: exit codes, added-line
positions, changeset detection, editor expansion`: an off-by-one in
`survival.AddedLines` line numbers, state-based refusals exiting 2 instead of 1, unknown
subcommands exiting 0, and `changeset.List` treating any directory under `changesets/` as a
changeset. Hand-verification of everything the README documents additionally corrected
`status`'s `archive_ref` and `$VISUAL`/`$EDITOR` word splitting.

### Follow-on plans

This plan covers the MVP and the post-MVP corrections above. Two later bodies of work changed
behaviour it describes and are recorded elsewhere:

- **Review Span Selection** — `docs/plans/completed/review-span-selection/plan.md`, with its audit
  at `docs/plans/completed/review-span-selection/audits/2026-09-16-completion.md`. It replaced
  `internal/span`'s range model with checkpoints (which is what removed the covered span,
  discoveries 11–12), made spans whose head is a commit read-only, added `V`/`v`/`r`, and added the
  `--base-*`/`--head-*` flags. Where this plan and that one disagree about spans, that one is
  current.
- **The three defects its audit found** — fixed in `9b5a877` (marks across a span round trip),
  `d0ceee7` (the header called every review-based span `unreviewed`) and `1575437` (a read-only span
  could recreate `ABOUT.md`). Discovery 13 and the marks row above carry the parts that matter here.

### Deviations and discoveries worth keeping

1. **`git interpret-trailers` cannot be handed a subject without a trailing newline.**
   With no trailing newline it appends the trailers to the subject paragraph with no blank
   separator, so git sees a one-line subject, finds no trailer block, and *every* derived
   state collapses to `WORKING`. `marker.Message.Render` now builds the message directly.
   Any future change to message construction must keep the blank line before trailers.
2. **`git rev-parse --verify --quiet` signals an unresolvable rev with exit 1 and empty
   stderr.** Matching on the message alone turned "ref does not exist" into a generic git
   failure, which broke `CreateRefIfAbsent` (and therefore `review close`). `git.ExitCode`
   exists so callers can distinguish the two.
3. **Interactive subcommands must refuse without a terminal.** `review about`, `review
   thread`, and `review open` previously inherited the caller's `$VISUAL` and hung forever
   when stdin was a pipe — which is the normal agent context. They now exit 2 with a
   message pointing at the file, which is an ordinary file in the working tree.
4. **Deferred from MVP as planned:** `review queue --global`, the `gitpr repo` registry, and
   remote propagation of `refs/reviews/*`. Persistent review progress was deferred too and has
   since been delivered — see discovery 13.
5. **git writes "nothing to commit" to stdout, not stderr.** `git.Error` carried only stderr,
   so `isNothingToCommit` never matched and a no-op commit surfaced as an opaque
   `git exited with status 1` with exit 3. `git.Error` now carries stdout too, and
   `change init` asks `git diff --cached --quiet HEAD -- <paths>` first, which is also
   locale-proof — the message text is translated.
6. **`aboutIsTemplate` never matched, so the "you left the scaffold" nudge was dead code.**
   `AboutTemplate` ends with a newline, so splitting it produced one more line than the
   trimmed file and the length check always failed. Nothing covered it: an assertion-free
   warning is invisible to the suite until someone reads the output. Now asserted by
   `TestChangeReadyWarnsWhenAboutIsStillTheScaffold`.
7. **A changeset based on its own branch is unrecoverable, and gitpr let you create one.**
   Everything is derived from `base...HEAD`; when the base names the branch it is stacked on,
   that range is empty for all time. `change ready` reported success for a marker no reader
   could ever observe, and `status` answered `WORKING` forever — found by the owner while
   dogfooding a single-branch repo. `changeset.BaseIsOwnBranch` now gates `change init` (exit
   2) and `change ready` (exit 1), and `status` names it as the reason. The predicate compares
   *refs*, not commits: a branch created moments ago legitimately shares its base's tip, and a
   commit-based test would have refused the normal first `change init`.
8. **PRD §421 says "implementation commit", and counting non-marker commits over-reads it.**
   An `ABOUT.md` typo fix after `change ready` was demoting the changeset to `WORKING` and
   calling itself an "implementation commit". Staleness is now a tree comparison — does
   anything outside `changesets/<slug>/` differ between the marker's parent and `HEAD` — in
   `lifecycle.ReconcileStaleness`, leaving `derive` a pure commit counter so it stays
   unit-testable. Consequences: a commit touching both the changeset directory and code still
   invalidates (the code moved); an unrecognised `GitPR-*` marker invalidates regardless of the
   tree, because the tree cannot speak for a trailer set gitpr cannot read; and reverting code
   back to the approved state returns `READY`, which is the intended reading of "the exact
   implementation state the marker represents".
9. **Unreproduced test flake, instrumented rather than fixed.**
   `TestSessionResetsReviewedMarkWhenFileDiffChanges` failed twice in whole-suite runs and
   passed in roughly 25 targeted ones, including `-count=3` and shuffled orders. Candidates
   were tested and ruled out: `diff.noprefix` (absorbed by the parser's `+++ ` fallback) and
   `diff.mnemonicPrefix` (does not apply to commit-to-commit diffs); cross-package interference
   is impossible through memory, since Go test binaries are separate processes. The assertion
   conflated "file left the span" with "still marked", which is what made the reports
   undiagnosable; those are now separate failure messages.
10. **A resume-like default for `review open` was built, then reverted in favour of `review
    reopen`.** The complaint was real: with no flag every session opened on `base...HEAD`, so
    reopening after submitting a review re-showed what had just been read. The first cut had
    `span.StartingSpan` choose `<latest review>..HEAD` when code had changed since the review,
    with `--full` as the escape hatch; it passed the gate and an eight-case pty check, and was
    reverted on the owner's direction. Inferring the span from repository state makes one
    command mean two things on two days with nothing on the command line saying which you got.
    `review reopen` names the intent instead and leaves every no-flag command on the full
    changeset, which is what PRD §17.2 specifies. Two guards are what make it a command rather
    than an alias: no review yet is a usage error pointing at `review open`, and a span with no
    files in it — the author has not committed since the submission — refuses with exit 1 and
    names the review it is relative to, since an empty file list reads as a broken tool.
    (The second guard lasted one discovery: 11 removed it as the wrong answer.)
11. **The empty-span refusal was the wrong answer, and is gone.** The owner hit it in the
    dogfood repo: `review reopen` refused because their feedback submission *was* the newest
    commit, which is precisely the state in which a reviewer types `reopen`. Refusing there
    served the command's definition over the user's intent. `span.Covered` now resolves the
    changes a submission saw (discovery 12 settles where that range ends, and why), and
    `reopen` falls back to it with one stderr line saying nothing has landed since. The only remaining refusal is `ErrNoReviews`
    (exit 2), which has no sensible fallback. `v` from a covered span goes to the full changeset
    rather than the empty since-review span, since covered is only reachable when that span has
    no content. The exit-1 path was deleted rather than tested around: it had no reachable
    input left, and the TUI already labels an empty span `(no changed files in this span)`.

    Lesson for the plan: a guard added to protect an interface detail (an empty file list) should
    be checked against the states where people actually invoke the command.

    **Superseded, and deliberately kept.** The Review Span Selection plan deleted `span.Covered`,
    the fallback, and the covered-span behaviour of `v` (commit `cc7dc6c`). Under that plan a
    span's head decides whether it is reviewable, and the covered span ends at a commit, so
    reopening would have opened a screen that refuses every keystroke; `review reopen` resolves
    `Review(-1) → WorkingTree` instead, and the `you` section of the preview covers the case the
    fallback existed for. The two lessons above outlive the mechanism.
12. **The covered span ended in the wrong commit, so reopen showed a reviewer their own notes.**
    The first cut ran `merge-base(base, review)..<review>`, and `..<review>` from the *previous*
    review for a second submission. A review commit carries what the reviewer wrote — a thread,
    an `ABOUT.md` edit, occasionally a file — so reopening listed those as the work under review,
    and back-to-back submissions produced a span holding nothing but the previous reviewer's
    notes. Covered now runs `merge-base(base, review)..<review>^`: the changeset as it stood when
    the submission was made, minus the reviewer's own output. Verified on the dogfood repo, where
    `changesets/bonjour/greetings.md` — a thread the submission itself wrote — dropped out of the
    file list.

    Worth keeping: the bug was invisible in the tests I first wrote, because the fixture's review
    markers touched the same `service.go` the implementation did. A fixture where the submission
    writes a file nothing else touches is what pins this. (The covered span this discovery fixes was
    later removed, as discovery 11 records; the fixture lesson is general and still applies.)
13. **Reviewed marks persist between sessions, as a local cache.** PRD §16 keeps marks out of the
    durable review artifact and permits an in-memory implementation; caching them locally honours
    the first while making a review resumable, which the owner asked for. They are written to
    `<gitdir>/gitpr/marks/<slug>/<commit>.json` — inside the git directory, so `git status` cannot
    see them and `git add -A` cannot stage them — keyed by the commit the span ends at, each file
    stored with its diff key. A mark is restored only when that path still has the same diff key,
    so a new commit, a rebase, or a different span cannot revive a mark that no longer describes
    anything; clearing every mark **in that span** writes an empty set for the keys that span uses,
    so its own marks do not reappear — since `9b5a877` it leaves other spans' keys alone, which is
    the point of the correction below; the newest
    12 commits' sets are kept per changeset. `status`, `diff` and the JSON contracts are untouched,
    because reading progress is not derived state.

    **Corrected by `9b5a877`, and worth the detail.** "Each file stored with its diff key" hid an
    assumption that a path has one key at a time. It does not: the full changeset and the unreviewed
    span both end at HEAD, so the same file has a different diff key in each, and saving one span's
    set erased the other's — from disk, while both were on screen. A mark is a fact about
    *(path, diff key)*, so the store keeps several keys per path and `Save` replaces only the pairs
    it is handed. The session also reads the store for the span it just entered rather than once at
    open, which is what makes a mark survive `v` away and back. Discovery 24's "the reviewed counter
    stays the span's" is the same rule from the other side.

14. **The author was being sent to the reviewer's command.** `nextActionFor(BLOCK)` and
    `status`'s `next_action` both told an author to read a new review with
    `gitpr diff --unreviewed`, and PRD §29's success-criteria walkthrough said the same. That
    span is `<latest review>..HEAD`, so immediately after a submission it is empty — the
    submission is the newest commit — and the author's own code changes sit in it too. Added
    `change feedback` (diff the submission: `review^..review`, so threads, `ABOUT.md` edits and
    reviewer code edits arrive together) and `change wait` (block until the state leaves
    `READY`; `--fetch` re-derives after `git fetch` and also inspects `refs/remotes/*/<branch>`,
    so a review pushed from another clone is noticed without a forge integration). Every
    author-facing hint now points at `change feedback`; `diff --unreviewed` stays the
    reviewer's command. `wait`'s JSON spells states the way `status --json` does — an agent
    comparing the two should be comparing literals — and a timeout reports the state it
    actually saw plus `timed_out: true`, exiting 1. The polling loop lives in `pollUntil`,
    separate from the repository reading, so its timing is tested without a reviewer in the
    room; `wait` itself writes nothing, so "no daemon, no cache, no database" still holds for
    it. Fixtures push to local bare remotes, which hygiene allows: it skips `_test.go`, whose
    job is to stage states the CLI itself refuses to create.

15. **The shortcut bar overflowed narrow windows, and the list did not know.** The TUI wrote
    the key help as one 101-character string, so the terminal broke it wherever 80 columns
    ended — mid-shortcut — and the scroll window, which reserves a fixed 12 rows for
    chrome, kept counting one footer row. The helper now wraps between shortcut groups at the
    terminal width (`wrapGroups`, same wrapping for the submit prompt) and `windowRows()` is
    the single place the header/footer allowance is computed, used by both `clamp` and
    `visibleRows`, so the list gives up the rows a wrapped helper uses. Known adjacent gap, since closed:
    `truncate` shortened only the *selected* row, so an unselected path longer than
    the window still wraps in the terminal.

16. **The threads browse mode was a second place to be, so the TUI is now one list.** ABOUT.md
    and the threads sat in a fixed block under the counter, reachable only by `T`, and creating
    a thread was a separate prompt — three gestures for one job. The navigable list is now the
    files in the span followed by the changeset section: ABOUT.md, a `Threads (N)` heading that
    collapses, each thread nested under it, and `+ new thread…` as the group's action. `j` runs
    off the bottom of the files into it; `Tab` toggles between the halves (a key that only goes
    one way strands the reviewer there); `Enter` activates per row kind through `activateBy`,
    kept apart from the process handoff so the table is testable without launching an editor.
    `Space` stays file-only and says so. Files that *are* artifacts keep their file rows when
    they change in the span — the list is the record of what the diff did, the section is the
    shortcut to read it — which the owner chose over deduplicating. Rows live in the model and
    are rebuilt by `refresh`, which preserves the cursor by row kind and path, so a thread
    created in the editor does not move the reviewer; `chromeRows`/`windowRows` now count the
    real header and footer because the section left the fixed chrome. The two blocks are drawn
    on either side of the reviewed counter — one window and one cursor over one list, split only
    at render time — so the block above the counter is "files the diff touched" and the one below
    is "what the review is made of", which is what makes Tab read as skipping a section. The owner
    asked for that placement after seeing the section directly under the file rows. Driving the
    real TUI in a pty caught a bug the unit tests could not: bubbletea reports a lone space as
    `KeySpace`, not `KeyRunes`, so every space in a new thread's title was silently dropped
    before the file name was derived from it. Two polish corrections came from using it: the
    thread heading shows its count only while collapsed (expanded, the rows below *are* the
    count), and the shortcut bar needed a rule over it, because sitting directly under the
    section it read as more rows of the list. `d` was added as an explicit diff key: Enter's
    meaning depends on which block the cursor happens to be in, and a reviewer who wants the
    difftool should not have to remember where they are. It takes any row naming a file the
    span touched; a row naming a file the span never touched has no diff to show, so `d` opens
    the file in the editor instead and says so. The saying-so is the part with any thought in
    it: a status set before the terminal is handed over is under the editor's own screen by the
    time the reviewer can read it, so the note is carried to the far side of the handoff
    (`pendingNote`, printed when the child exits) and assigned by every handoff, including a
    failed one — a difftool that fails is a misconfiguration to report, not a reason to pretend
    the fallback happened.
17. **Enter on a changeset document follows the span, because a diff against nothing is not a
    diff.** The proposal was to make Enter on ABOUT.md and threads behave like `d`. The
    objection was that these files are *added* by the changeset, so `git difftool <base> --
    changesets/x/ABOUT.md` opens a two-pane view with `/dev/null` on the left: the document,
    with extra steps. That objection only holds for the full span. In a since-review span the
    documents already exist at its left end, so the span holds a genuine comparison — the lines
    the author rewrote after the last submission, which is exactly what a returning reviewer
    came for, in prose as much as in code. So the rule is *in the span and existed where the
    span starts* → difftool; anything else → editor, with a note naming which case it was.
    `e` and `a` stay unconditional ways into the editor, so nothing is harder to reach than
    before. Verified against a two-round fixture (review submitted, then the author's fix
    commit) with a probing `diff.tool` and `EDITOR`: in the reopen span ABOUT.md and an edited
    thread launch the difftool with no note; a thread written after the review launches the
    editor with "added by this changeset"; in the full span ABOUT.md does the same.

    Flagged there and fixed below: `console.EditorCommand` resolved `VISUAL`/`EDITOR` (default
    `vi`) and ignored `GIT_EDITOR` and `core.editor`, both of which git itself honours, so a
    reviewer who configured their editor the git way got `vi` in the TUI. Fixed in 19.
19. **The editor now comes from git, which is where everyone configures it.** `EditorCommand`
    read `$VISUAL`, then `$EDITOR`, then `vi`, so `git config core.editor vim` — what git's own
    docs recommend, and what my `~/.gitconfig` said — was invisible: the TUI opened `vi`, and an
    `$EDITOR` set alongside a `core.editor` won in the wrong order. Asking git removes both
    defects and the rules we should not be maintaining: `git var GIT_EDITOR` answers the whole
    documented chain, `GIT_EDITOR`, `core.editor` (repository-local included), `VISUAL`,
    `EDITOR`, and the fallback the git build was configured with — which on this platform is
    `editor`, not `vi`, a detail the first version of these tests got wrong and the code did
    not. The environment chain stays as the last resort for a git that cannot answer, where
    `vi` remains the literal default. The launch shape is unchanged (`eval exec
    ${GITPR_EDITOR} "$@"`), so `core.editor="code --wait"` still splits into program and flags,
    and `GIT_EDITOR=true` still means "no editor" exactly as it does for git. Verified in the
    TUI against a repository whose `core.editor` is a marker script: `e` ran the marker with the
    absolute path, where the previous binary had opened `vi`.
*(18 and 19 are out of order — 19 was committed first. The numbers are labels, not a sequence.)*

18. **The difftool's exit messages outlived the TUI, so a session that handed the terminal away
    clears the screen.** Bubbletea's handoff suspends the renderer and leaves the alt screen,
    so the child draws on the main screen; when vimdiff quits it prints `2 files to edit` there
    — where the session's last frame had been — and closing the alt screen reveals it above the
    prompt, once per difftool opened. Nothing in gitpr's own rendering can stop that, since the
    child is on screen by then, so the cleanup happens after `p.Run()` returns: every handoff
    goes through `runExternal`, which sets `handedOff`, and Run emits erase-screen and home to
    the terminal it drew on. Scoped to sessions that handed off, so one that only read the list
    does not wipe what the user had on screen; scrollback is left alone, because that output is
    history rather than debris. Verified at the byte level on the two-round fixture: after the
    last `\x1b[?1049l` there is `\x1b[2J\x1b[H` when a difftool ran, and nothing when none did.
    Superseded by 20 — the flash that prompted the report had the same cause, and holding the
    screen instead of mopping up afterwards removes both.
20. **The session keeps its alt screen through a handoff, which removes the flash and the
    debris together.** Opening an editor or difftool exposed the command-line history for a
    beat first: `tea.ExecProcess` calls `ReleaseTerminal()` before starting the child, and for
    bubbletea releasing means `exitAltScreen()` and a 10 ms pause — so the shell screen is
    visible for as long as the tool takes to paint, which is tens of milliseconds for vim and
    several hundred for the rest. Cleaning up afterwards could not reach that.

    The session now takes the screen itself — the sequences bubbletea would write, `?1049h` +
    erase + home + cursor reset, and `?1049l` on the way out — and starts the program without
    `tea.WithAltScreen()`. bubbletea falls back to its inline renderer, which is sound here:
    it tracks its own frame relative to the cursor, so the frame lands from the top after the
    screen is cleared and homed, and dropping lines taller than the window is moot at our
    sizes. The one obligation is to clear and home when something else has had the screen, done
    in the `externalDoneMsg` handler: a handoff resets the renderer's line count, so without it
    the next frame is written *onto* the tool's output instead of over it. Leaving the screen is
    explicit before anything is printed — the submitted summary has to land where the user is
    still looking — and deferred for the panic path.

    A/B on the same fixture and keys: the previous build emitted 5 alt-screen exits, two of them
    immediately before a child started (bytes 853→884, 7444→7475, which is the flash); this one
    emits 3, ours a single pair at the ends of the session and the middle two vim's own, with the
    frame after the last tool closing intact.
21. **Prototype: a diff preview in a right pane when the terminal is wide enough.** The choices
    were made with the user: a right pane (the list keeps its rows and gives up width), the
    span's committed diff — matching `gitpr diff`, `--stat` and the reviewed marks, deliberately
    not the working tree the difftool shows — and paging on `ctrl-f`/`ctrl-b`, because `ctrl-d`
    already quits and a key must not change meaning depending on whether a pane is on screen.

    The line that mattered: PRD §3 rules out building a diff renderer, so the pane prints git's
    bytes — `git diff --color=always --no-ext-diff --no-textconv <from> <to> -- <path>` — and adds
    nothing to them. No parsing, no hunk model, no folding, no colouring of ours; what it adds is
    a header carrying git's own numstat counts and a note about the lines it is not showing. That
    boundary is what keeps it a preview and leaves the difftool as the real view.

    Things that needed deciding: 100 columns minimum with the list never below 34 (superseded by
    discovery 23: the list is sized from its own content, clamped 24..48), because a pane that
    squeezes the list is worse than no pane; patches fetched off the event loop and cached
    per span, so walking a list with `j`/`k` asks git once per file, and a span toggle or a tool
    handoff drops the cache rather than showing a stale diff; every row clipped by terminal width
    rather than bytes, since one wrapping row would shift the divider out from under its column —
    the old selected-row-only truncation was already wrong at any width, it just had no neighbour
    to expose it — which is discovery 15's "known gap, not fixed" closed.

    Prototype status: the threshold, the split ratio and the header are taste. `p` exists so they
    can be argued about without rebuilding.
22. **The preview takes the frame with it.** Four asks on top of discovery 21: fill the window's
    height, pad to the edges, wrap the diff rather than cut it, and put a line number beside each
    line. Two of them turned out to be load-bearing for the other two.

    Padding the frame to the terminal's width and the row area to its height exposed a scroll that
    predates the preview: Bubble Tea's renderer writes one line per frame line and moves down after
    it, so a frame of exactly `height` lines *ending in a newline* has no row left for the cursor
    and the terminal scrolls the top line away — which is what any changeset taller than the window
    did. The frame is joined rather than newline-terminated now, and the invariant is a test rather
    than an observation. Reading the renderer's source was what made this visible; guessing from a
    screenshot would not have shown it, because the scroll only bites when the content is exactly
    full-height.

    Wrapping went through the library first. `ansi.Wrap` is word wrapping: it collapses whitespace
    at the break, which in a diff is the indentation — the content. `wrapLine` breaks at the column
    instead, keeps every space and tab where git put it, and moves a wide glyph whole. Wrapped rows
    then had to become self-contained, because the renderer skips redrawing a row that has not
    changed and a colour opened at a break would otherwise tint everything drawn beneath it.

    The gutter is the one place the pane reads git's output rather than passing it through, and it
    reads as little as possible: the two starts of the `@@` header, then counting `+`, `-` and
    context lines, which is git's own arithmetic. Nothing before a hunk header is numbered, a patch
    with no hunk header is not numbered at all, and the classification strips colour first because
    git colours the very characters being looked for. It does not fold, group, filter or restyle —
    PRD §3 stays intact, and reading a diff still means opening it.
23. **The split is adaptive: the list pays for its own paths.** The 60/40 split made the diff share
    the screen with columns of whitespace, because most changesets are named after a package and a
    feature, not a tarball. The list now takes the width of its widest row — plus the two header
    lines over it, which are the other thing the column has to be able to say — clamped to 24..48
    columns, and the pane takes what is left. At 140 columns with fixture-sized paths that is a list
    of 35 and a diff of 102, enough for git's own `diff --git` header to stop wrapping.

    Two decisions worth their weight. **The column is sized from every row, not the rows in the
    window:** sizing from the window would slide the divider sideways as a longer path scrolled into
    view, and a screen that jumps under the cursor is worse than one that wastes a few columns. And
    **the cap is what makes the floor unnecessary in practice**: one vendored path should not cost
    the reviewer the columns the diff is read in, so the path is clipped instead of widening the
    column, and at the 100-column threshold the pane still gets 49 columns with the list at its cap.
    That last fact is a test rather than a guard — the earlier lesson was that minimums nobody can
    reach are decoration, so `previewMinPane` is asserted against rather than branched on.

    The 60/40 constant is gone. What remains are two numbers to argue with: `previewListMax` (48)
    and `previewListMin` (24).
24. **The reviewer's own edits get their own section.** Playing with the preview surfaced an
    asymmetry the design had left implicit: `d`/`Enter` diff the span's start against the **working
    tree** so edits persist, while the preview diffs `<from>..<to>`, which is committed state — so
    the keys showed the reviewer's typing and the pane never did. The reviewer's uncommitted edits
    are a real part of what they are looking at, and `review submit` already treats them as *not*
    part of the review ("the working tree is still dirty; those changes are not part of this
    review").

    Chosen: stack them, the author's span first, then the reviewer's under a caption naming them.
    Three decisions inside that.

    **Measure the reviewer's section from the span's end, not its start.** `git diff <to> -- path`.
    From `<from>` it would carry the author's changes as well, and then the two sections would be
    indistinguishable — the exact confusion the caption exists to prevent. A test pins this: the
    author's line is in the span patch and absent from `WorkingPatch`, and vice versa.

    **The caption is the disambiguator, not colour.** Git's bytes do not say who typed a line: an
    added line the reviewer wrote is the same green as one the author wrote. So the section is
    labelled `── you · uncommitted` and carries its own `+N −M`, while the file header keeps the
    span's counts — otherwise a number beside the file would read as a statement about the author's
    work that includes the reviewer's typing.

    **The reviewed counter stays the span's.** Making the pane show a second diff must not silently
    change what "reviewed" means, or what the marks are keyed on. It is a view, not state.

    Cost of the two-source pane: one more git call per file per first visit, cached and dropped with
    the rest on a span toggle or a tool handoff. `previewMsg` now carries which source it answers,
    because the bytes look alike; the cache is two maps for the same reason. The pane also declines
    to say "no changes in this span" while the answer about the reviewer's edits is still in flight,
    which would be wrong for the length of one git call.

    Rejected: a three-way view. Git has three-way *merge* machinery (`merge-file`, `mergetool` with
    `$BASE`/`$LOCAL`/`$REMOTE`, `diff3`/`zdiff3` conflict styles) but no three-way **diff**; a
    `merge-file` rendering would be a merged file that silently resolves everything except
    collisions, and pointing a real 3-way tool at base/author/you means owning a per-tool argument
    convention, which PRD §3 puts outside the project.


## Architecture

Fifteen packages under `internal/` behind one `cmd/gitpr`, a thin `cli` layer over a pure-git
core. No database, no daemon, and no cache of *derived* state: state comes from commit order and
nothing reads a stored answer.

"One package" and "no cache" are both corrected at audit. There are fifteen packages (D1's Go
layout, and the `cli` package is among the larger ones), and discovery 13 deliberately added a
cache of reviewed marks. That one is inside the invariant because it holds only non-derived,
non-authoritative progress — the marks are never read by `status`, `diff`, or any JSON contract, and
deleting `<gitdir>/gitpr/marks` loses nothing but clicks.

### As built (Go)

D1 chose Go, so this is the tree that exists. The Python sketch that follows is the plan as
written; it is kept because the *responsibility split* in it is what got built, and every module
in it has a Go counterpart.

```
gitpr/
  cmd/gitpr/main.go     the binary; calls cli.Execute
  internal/git/         plumbing wrappers: run, rev-parse, log, diff/show, update-ref, ref tips;
                        git.Error carries stdout too, git.ExitCode separates "ref missing" from
                        "git failed" (discoveries 2, 5)
  internal/changeset/   branch -> changesets/<slug>/, CHANGESET.yaml, ABOUT.md path, threads, and
                        the base-is-own-branch guard (discovery 7)
  internal/lifecycle/   one pass over <base>..HEAD deriving state; staleness by tree comparison
                        (discovery 8); the summary every command reads
  internal/marker/      writes the ready/review/close commits; message built directly, not through
                        interpret-trailers (discovery 1)
  internal/model/       outcome and state vocabulary shared by every package
  internal/span/        checkpoints -> a resolved Span with pinned OIDs, answering
                        Live/Historical/CanEdit/CanMark/CanSubmit; drift and refresh (span plan)
  internal/survival/    the -U0 added-line check and the surviving set; D3 scoping in one function
  internal/reviewref/   refs/reviews/<slug> and refs/reviews/archive/<slug>/<sha>
  internal/reviewops/   the submit and close operations, shared by the CLI paths
  internal/reviewmark/  the local reviewed-mark cache under <gitdir>/gitpr/marks (discovery 13)
  internal/console/     editor and difftool resolution (`git var GIT_EDITOR`) and handoff hygiene
                        (discoveries 19, 20)
  internal/tui/         Session — headless, testable without a TTY (D2) — and the Bubble Tea view:
                        session.go, tui.go, picker.go, preview.go
  internal/cli/         the command surface: cobra tree, flags, exit codes, --json shapes, and the
                        refusals that keep non-interactive commands non-interactive
  internal/gittest/     throwaway-repository fixtures used by every package's tests
  internal/hygiene/     PRD §26 enforced structurally, in three layers: a git-argv field equal to a
                        forbidden verb, a bare verb string literal anywhere, and shell text inside
                        exec.Command
```

Build and verify with mise: `mise run build` (installs to `~/.local/bin/gitpr`), `mise run test`,
`mise run check` (build, `go vet`, `gofmt`, `go test`). Never `go install`, and never a bare
`go build` as the project's check.

### As planned (superseded by D1; kept for the responsibility split)

```
gitpr/
  __init__.py
  __main__.py          python -m gitpr
  git.py               run_git(), plumbing wrappers: rev-parse, log, diff, show,
                       update-ref, commit, status; raises GitError with stderr
  repo.py              Repo: root discovery, current branch, is_clean(), head(), slug()
  changeset.py         branch -> changeset dir, CHANGESET.yaml read/write, ABOUT.md path,
                       thread path + slugify + collision resolution
  lifecycle.py         parse GitPR-* trailers; LifecycleEvent list; derive_state();
                       find_reviews(); find_latest_review(); is_stale()
  span.py              resolve_span(base, since_review, unreviewed) -> (revA, revB, label)
  survival.py          review_added_lines(R) -> [AddedLine]; surviving(R, HEAD) -> [Surviving]
  refs.py              review_ref(), archive_ref(), update/archive/resolve/list
  outcome.py           outcome <-> state vocabulary, permitted-for-integration predicate
  ui.py                editor/difftool resolution + suspend/resume handoff
  cli/
    main.py            argparse tree, --json, exit codes, error rendering
    change.py          init, ready
    review.py          open, about, thread, submit, history, queue, close
    top.py             status, diff
  tui.py               curses file-list reviewer (M5)
tests/                 pytest, real throwaway git repos in tmp_path
```

Core invariants the code must hold:

- **Derive, don't store.** Effective state comes only from commit order in
  `<base>..HEAD` plus `HEAD`. No `.gitpr-state` file.
- **Marker = commit.** `ready`, review outcomes, and `close` are commits with trailers;
  staleness is ordering, not bookkeeping.
- **The check is diff-shaped.** Surviving additions come from
  `git show -U0 --no-ext-diff --no-textconv --no-renames --format= <R>`, plus exact-line membership
  in the `HEAD` blob, with `git show --numstat …` for the binary skip and blank-line exclusion.
  Corrected at audit: this said `git diff -U0 --no-renames <R>^ <R>`, which is not what
  `survival.AddedLines` runs. `git show` is the deliberate choice — it *is* a first-parent diff and
  handles the root commit with no special case, which is why M3's "root-commit handling" task needs
  no code. `--no-ext-diff` and `--no-textconv` are load-bearing rather than tidy: without them a
  configured external diff or textconv turns the output into whatever the user's tool prints, and
  the parser stops being a parser.
- **Refs before irreversibility.** `refs/reviews/<slug>` is updated in the same operation
  that produces the review commit, never as a separate later step.
- **No destructive verbs.** No `push`, `merge`, `rebase`, `reset`, `branch -D`, or
  `update-ref -d` anywhere in the codebase. Enforced by `internal/hygiene`, which is not a grep: it
  parses every Go file and checks a git-invocation argument equal to a forbidden verb, a bare verb
  string literal anywhere in the file, and verb-bearing shell text inside `exec.Command`. The
  forbidden set is `push`, `merge`, `rebase`, `reset`, `switch`, `checkout`, `branch` — wider than
  the five this section used to list, because `switch`/`checkout`/`branch` would move the worktree
  or invent the branch a changeset is supposed to already have.

Exit codes: `0` success, `1` business-rule refusal (surviving additions, non-permitted
outcome, dirty tree), `2` usage/argument error, `3` repository/git error. Stable for agents.

## Milestones

The **Tasks** lists below are the plan as written, Python file names and `uv`/`pytest` commands
included, because D1 was answered after this document was drafted and rewriting every bullet would
have destroyed the record of what was asked for. Read them as intent: `lifecycle.py` became
`internal/lifecycle`, `tui.py` became `internal/tui`, `uv run pytest -q` is `mise run check`. The
**Deliverables** and **Verification** paragraphs are what was actually verified, and the Status
table above records where reality diverged from them.

### M0 — Repository scaffolding and git core (no product decisions needed)

Deliverables
- Project builds and installs; `gitpr --help` works; test harness runs.
- `git.py`/`repo.py` wrap the plumbing verified in the spike, with error propagation.

Tasks
- `pyproject.toml` (uv, console script `gitpr = gitpr.cli.main:main`), `src/` layout,
  `.gitignore`.
- `git.py`: `run_git(args, cwd, stdin, check)` capturing stdout/stderr; typed `GitError`.
- `repo.py`: `find_root`, `current_branch`, `head`, `is_clean` (`status --porcelain -uall`),
  `rev_parse(rev, verify=False)`, `slug_from_branch`.
- Test fixture `repo` factory: temp dir, `git init -b main`, deterministic
  `user.name/email/commit.gpgsign`, helper `commit(msg, files=..., allow_empty=...)`.
- Commit the PRD + plan as the first commits on a feature branch.

Verification — `uv run pytest -q` green; `gitpr --version` prints; unit tests assert slug
rule and clean/dirty detection against a real repo.

### M1 — Changeset identity and `change init` (unblocked)

Deliverables
- `gitpr change init --base <ref> [--about <text>]` creates `changesets/<slug>/CHANGESET.yaml`
  and `ABOUT.md`, then commits them; safe to re-run; refuses to run on a detached `HEAD`.

Tasks
- `changeset.py`: `for_branch()`, `dir`, `metadata` (minimal YAML written/parsed by hand —
  one `key: value` line; no PyYAML dependency), `ABOUT.md` template with PRD §6 headings.
- Idempotence: never overwrite an existing `ABOUT.md`/`CHANGESET.yaml`; `--base` conflict
  reported, `--set-base` to change it; `--about` content over an existing `ABOUT.md`
  conflicts the same way, with `--set-about` as the override.
- Commit the scaffold, scoped to the changeset directory with `git commit --only`, so an
  unrelated staged file survives on the author's index. `--no-commit` opts out for authors
  who want the scaffolding inside their first implementation commit (PRD §22 step 4).
- `--about <text>`, `--about -`, or piped stdin supplies `ABOUT.md` content, making
  initialise-and-describe one non-interactive call.
- `cli/change.go init`.

Verification — init twice ⇒ byte-identical files and a non-destructive notice; a populated
`ABOUT.md` survives a second `init` and survives `--about` unless `--set-about` is passed;
`CHANGESET.yaml` round-trips `base: booking-transaction`; a pre-staged unrelated file is still
staged after init and absent from its commit; an empty pipe yields the scaffold, not an empty
`ABOUT.md`.

### M2 — Lifecycle, state derivation, `status`, `review history` (unblocked)

Deliverables
- `gitpr status` / `status --json` report derived state, base, head, latest review, review ref.
- `gitpr review history` renders PRD §10.5's table with `0..n` and `-1` addressing.

Tasks
- `lifecycle.py`: single pass over `git log --reverse --format=...%x09...` of `<base>..HEAD`;
  classify `ready` / `review(outcome)` / `close` / implementation.
- `derive_state()` precedence: `CLOSED` > latest marker > `WORKING` if an implementation
  commit follows the marker; expose `state_reason` for humans and JSON.
- `outcome.py` vocabulary; `review history` formatting + `--json`.
- `cli/top.py status` with the PRD JSON key set — `changeset`, `branch`, `base`, `state`, `head`,
  `latest_review{index,outcome,commit}`, `review_ref` — plus nine the dogfood needed (`head_full`,
  `review_commit`, `archive_ref`, `uncommitted`, `reviews`, `reason`, `span`, `next_action`,
  `unrecognised_markers`). "Exactly" was the plan's word; a superset is what shipped, and
  `TestJSONKeySets` pins the whole set so the next addition is a decision while §11.1's readers
  stay served (`TestStatusJSONEmitsPRDKeySet`).

Verification — golden-history test driving the PRD §12 sequence
`implementation → ready → review(block) → ready → review(feedback)` asserting state after each
step; explicit test that an implementation commit after an `approve` marker yields `WORKING`
(the PRD's named safety property).

### M3 — Spans, `diff`, surviving additions, `change ready` (unblocked except D3 default)

Deliverables
- `gitpr diff` with `<path>`, `--unreviewed`, `--since-review[=N]`, and `--stat`; resolves to
  exactly the PRD §17 commit ranges and delegates rendering to Git.
- `gitpr change ready` with the surviving-review-additions gate, PRD-verbatim output, and
  `--allow-surviving-review-additions`.
- Ready marker commit `gitpr: ready <slug>` with `GitPR-State: ready`.

Tasks
- `span.py`: `full` = `merge-base(base,HEAD)..HEAD`; `since_review(N)`; `--unreviewed` as an
  alias for `--since-review=-1`; clear errors for "no reviews yet" and out-of-range indexes.
- `survival.py` per findings: `-U0 --no-renames`, `--numstat` binary skip, blank exclusion,
  `\`-line skip, root-commit handling, exact-line membership, `path:line@HEAD` reporting,
  deterministic ordering, dedup of repeated identical lines with an occurrence count.
- `cli/change.py ready`: changeset exists → clean tree → `ABOUT.md` exists → survival gate →
  marker commit. All checks before any mutation; no prompts.
- Implement the D3 default (block outside `changesets/<slug>/`, non-blocking list for
  surviving artifacts) behind a single `scope` function so the rule is one-line changeable.

Verification — the PRD §29 scenario as one integration test: review adds two source comments,
author fixes one and leaves one ⇒ `change ready` fails listing exactly the untouched one at
its `HEAD` line; with the comment resolved ⇒ passes; with the override ⇒ passes and prints
what was acknowledged. Plus unit tests for: deleted-at-`HEAD` additions, binary additions,
`ABOUT.md`-only additions, blank-line additions, renamed file, review commit as root commit,
review with empty diff (`--approve`), and `-3`/`0` index spans.

### M4 — Review submission, refs, queue, close (unblocked except D3 default)

Deliverables
- `gitpr review submit --block|--feedback|--approve` (mutually exclusive, one required),
  staging the reviewer's work and updating `refs/reviews/<slug>` to the exact `HEAD`.
- `gitpr review about`, `gitpr review thread` (slugified, dedup-aware, `$VISUAL`/`$EDITOR`).
- `gitpr review queue` (+ `--json`) listing READY changesets with base, age, head.
- `gitpr review close` with archive ref, closed marker, and the surviving check.

Tasks
- `outcome.py`/`cli/review.py submit`: commit message `review: <outcome> <slug>` per PRD
  §10.4, trailers via `git interpret-trailers`; `git add -A` then `--allow-empty` fallback;
  `update-ref` immediately after the commit succeeds.
- `refs.py`: `refs/reviews/<slug>` (movable), `refs/reviews/archive/<slug>/<shortsha>`
  (create only if absent, so archival is idempotent and immutable).
- `about`/`thread`: shared `ui.edit(path)`; thread slug collision reuses an existing file;
  no-title-in-TTY ⇒ usage error (keeps non-interactive commands honest).
- `queue`: for each `changesets/*/CHANGESET.yaml`, resolve its branch, derive state, emit
  READY ones; deterministic order (ready age, then name); JSON contract documented in the
  README.
- `close`: clean tree → latest outcome ∈ {approve, feedback} → survival gate → archive ref →
  `gitpr: close <slug>` marker commit with `GitPR-State: closed` → print archive ref +
  "Safe to squash/merge." No merge/push/squash.
- Source-hygiene test asserting the implementation never invokes a destructive verb.

Verification — integration test: `submit --approve` on a clean tree creates an empty commit,
ref points at `HEAD`; `review history` shows `block, feedback, approve` with indexes `0,1,2`;
`queue --json` gains then loses the changeset across `ready`/`submit`; `close` on a branch
then `git branch -D` leaves the full chain reachable via `git rev-list refs/reviews/archive/...`
(count asserted), reproducing the PRD's archival promise.

### M5 — Review TUI (needs D2, D4)

Deliverables
- `gitpr review open [--unreviewed] [--since-review=N]` curses UI matching PRD §14: header
  (changeset, base, span), file list with `○`/`✓`, `n / m reviewed`, `ABOUT.md` and
  `Threads (k)` rows.
- `gitpr review reopen` — the same session on `<latest review>..HEAD`, with no span flags: the
  named way back to a reviewed changeset. *As delivered it is `Review(-1) → Working Tree`*; the
  covered-span fallback described here was removed by the Review Span Selection plan, because a
  span that ends at a commit is read-only under that plan's rule and a resume you cannot resume
  from is not one (discoveries 11 and 12 carry the history).
- Bindings `j/k Enter e Space a t T v q`; file state resets to unreviewed when its diff
  changes during the session (PRD §16). *As delivered the set is larger:* `Tab` section, `d` diff,
  `p` preview, `v`/`V` span ring and picker, `r` drift refresh, `s` submit, `ctrl-f`/`ctrl-b` to
  page the preview. `Enter`'s meaning depends on the row, which is why `d` exists.

Tasks
- `tui.py` with a `ReviewSession` model (files, per-file content hash, reviewed set) kept
  separate from rendering, so the model is unit-testable without a terminal.
- `ui.py`: `curses.wrapper` + `endwin()`/`doupdate()` around subprocess handoff; restore on
  `SIGTSTP`/editor failure; re-scan the repo after the child exits.
- Session-level reset: hash `git diff <span> -- <path>` per file; invalidate the checkmark
  when the hash changes.

Verification — headless: model tests for toggling, span switch, hash-based invalidation, span
error surfacing. **Corrected at audit:** the `TERM=dumb` smoke test listed here was never written.
What exists instead is `internal/tui/teardown_internal_test.go`, which asserts the alt-screen
sequences are a pair and that returning from a tool starts on a clean screen — stronger claims than
a smoke test, but not the same one, and no test drives the program under an unusual `TERM`. Real
`vimdiff`/`diff.tool` handoff is checked by hand in a pty.

### M6 — Docs and dogfood pass (unblocked)

Deliverables
- `README.md` with the PRD §29 walkthrough, JSON contracts, exit codes, and agent guidance.
- A `CHANGELOG.md` entry; `gitpr --help` text reviewed for agent-parseability. **Not delivered:**
  no `CHANGELOG.md` exists. The README carries the user-visible contract and `git log` the
  per-change rationale, so nothing is undocumented — but the deliverable was listed and is not
  there, so it is recorded as skipped rather than quietly dropped.
- End-to-end run of the §29 script in a scratch repo, transcript captured under
  `docs/plans/completed/gitpr-mvp/artifacts/`. **Partly delivered:** the artifact is the *script*
  (`e2e-29.sh`), which is reproducible and re-run at audit; no captured transcript was kept. The
  script is the better artifact of the two — a transcript goes stale, a script re-runs — so this is
  recorded rather than back-filled.

## Spikes / Research

Completed before planning: `research/spike-git-plumbing.sh` +
`research/git-plumbing-findings.md`. Open question resolved by the spike: whether Git can
reliably expose "lines added by one commit, still present at HEAD" — it can, with the binary,
rename, blank-line and root-commit caveats recorded there. Remaining spike: none blocking;
M5's terminal-restore behaviour is validated by its own tests rather than a spike — the
alt-screen pairing and clean-screen-on-return tests in `internal/tui/teardown_internal_test.go`
(not the `TERM=dumb` smoke test this line used to cite, which was never written; see M5).

## Risks

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Literal §19 semantics make `change ready` fail on `ABOUT.md`/thread noise (measured: 4/6 additions) | Check becomes wallpaper; agents auto-override | D3 default scopes blocking to non-`changesets/` paths; artifact survivals still reported, not silenced |
| Base ref absent/unresolvable (fresh clone, renamed base, merged stack) | Wrong or empty spans, silent `main` fallback | Fail with an explicit "cannot resolve base X" error; never guess; test covers missing base |
| `main` advancing under a changeset shifts `merge-base` and hides history | Reviewer sees a smaller diff than expected | Print the resolved span in `diff`/`status`/TUI header so the range is never implicit: `gitpr diff: <label>` on stderr, `Span:` in `status`, and a labelled counter line in the TUI. Label shapes are `<base>...HEAD`, `<short>..HEAD (after review N)`, and `<ref>@<sha>..<sha>` for a pinned ref span — not the `base...HEAD @ <sha>` this cell used to quote |
| Blank/whitespace-only additions dominate the survival report | False positives | Blank lines excluded from the check; documented, tested |
| Review commit not on the current branch (detached HEAD, wrong branch) | Review submitted to the wrong place | Require a named branch and a matching changeset dir; `submit` prints the changeset, outcome, commit, files, the ref it moved and the submission it supersedes — after the commit, so what was written is legible without another command. It does **not** print branch or span, which is what this cell claimed until the audit read the output |
| A `TERM` the terminal library mishandles | TUI unusable, leaked terminal | The risk stands; the mechanism changed twice. D2 chose Bubble Tea, so `curses` is gone, and the session now owns its alt screen (discovery 20) instead of asking the library for one — which makes leaked terminal state *our* bug rather than the library's. Mitigation as delivered: headless model tests plus the alt-screen pairing tests; no `TERM=dumb` run exists (see M5) |
| Line-membership check mis-locates duplicated review lines | Misleading `path:line` | Report occurrence count with the first match; never claim uniqueness |
| State derivation silently classifies a hand-written `GitPR-*` trailer | Ghost lifecycle event | Accept a marker only with both expected trailers (`GitPR-Changeset` matching); malformed ⇒ warning line, not a state change |

## Verification Strategy

- **Unit** — pure functions with faked `git` output where practical: slug rule, span maths,
  index resolution, outcome/state tables, thread slugify/dedup.
- **Integration (primary layer)** — real throwaway git repos built by a fixture factory,
  asserting on `git log`/`git rev-list`/`for-each-ref` outcomes rather than stdout formatting.
  Every PRD-mandated behaviour gets one; the caveats from the spike (binary, rename, blank,
  root commit, deleted path, empty review) each get their own test.
- **Golden workflow** — one long test replaying PRD §29 end to end, plus
  `artifacts/e2e-29.sh`, the same workflow driven against an installed binary. It is a script
  rather than a captured transcript: a transcript goes stale, a script re-runs, and this one did
  (all checks passed at audit, against the binary from `mise run build`).
- **CLI contract** — `--json` outputs validated against a schema-ish key assertion; exit codes
  asserted per command (agent-facing surface).
- **TUI** — headless model tests, which is where nearly every TUI defect was caught; real
  `vimdiff`/`diff.tool` handoff is checked by hand under a pty. That was written as "cannot be
  automated here" and it was true of the original plan; a pty harness does drive the real program
  and found bugs the model tests could not (the dropped space in a thread title, the shortcut bar
  clipping `[r] refresh`). What is still missing is those harness scripts in the repository, so a
  reviewer can re-run them — see the audit's recommendations.
- **Source hygiene** — `internal/hygiene`: a syntax-level scan of every git invocation for
  `push`, `merge`, `rebase`, `reset`, `switch`, `checkout`, `branch`, plus bare verb literals and
  shell text passed to `exec.Command` — to keep PRD §26 non-goals structurally enforced. A substring
  grep would reject `merge-base` and the `branch` key in JSON output while missing
  `run("--hard", "reset")`, so the test parses instead; the `internal/gittest` exclusion is itself
  asserted, so fixtures cannot quietly widen the rule.
- Not attempted: cross-platform terminal behaviour, Windows (one branch exists — the executable
  lookup in `internal/console/console.go` — and it has never been run on Windows), forge
  integration, CI wiring.

## Execution Ordering (parallel-friendly)

Dependency structure, so unblocked work never waits on an owner decision:

```
M0 ──► M1 ──► M2 ──┬──► M3 ──► M4 ──► M6
                   │
        D1 ────────┘ (toolchain: needed to write code at all)
        M5  ◄── D2, D4          D3 ──► M3 gate semantics + M4 close
```

- **Starts immediately, no owner input:** M0, M1, M2, M3 (spans + survival engine + ready
  gate using the D3 default), M4 (submit/refs/queue/close using the D3 default), M6 README.
- **Starts on answer:** M5 (needs D2/D4). M5 is deliberately last and is the only milestone
  that can be skipped without invalidating the CLI contract.
- **Reversible answers:** D3 is one function; D2/D4 are confined to `tui.py`/`ui.py`; D1
  affects M0 scaffolding and file extensions only, with M1–M4 tests restating intent.

## Audit History

| Date | Audit | Summary |
| --- | --- | --- |
| 2026-09-16 | — | Initial plan. Plumbing spike complete; D1–D4 raised. |
| 2026-09-16 | implementation pass | D1–D4 resolved. M0–M4 implemented and verified by the PRD §29 replay; M5 implemented with the TUI verified under a pty. Three implementation-level discoveries recorded above; no PRD requirement dropped. |
| 2026-09-16 | `change init` change of contract | Init now commits the scaffold (scoped with `git commit --only`) and accepts `ABOUT.md` content by flag or pipe, so agents can initialise and describe atomically. M1 updated above; two further discoveries recorded. Found while this change was being tested, not by it: the template warning in `change ready` was unreachable. |
| 2026-09-16 | dogfood bug report | `status` said `WORKING` after a successful `change ready` in a single-branch repo. Cause: base == branch, so `base...HEAD` is permanently empty and the ready marker sits below the range that reads it. Guarded at init and ready, explained by status; the review workflow itself was never broken. |
| 2026-09-16 | review-experience pass | Three owner-reported items. The difftool was read-only and blind to `e` edits because it diffed two revisions — now one revision against the working tree, superseding D4's two-rev form. Submitting with `s` now ends the session. Readiness no longer demotes on changeset-only commits (discovery 8). |
| 2026-09-16 | span default reversed | The resume-like default for `review open` (discovery 10) was reverted the same day and replaced by `review reopen`, so `review open` stays on the full changeset as §17.2 specifies. |
| 2026-09-16 | reopen no longer refuses an empty span | Owner report from the dogfood repo (discovery 11). `span.Covered` added; `review reopen` falls back to the span the submission covered; the exit-1 refusal and its test removed as unreachable. |
| 2026-09-16 | space no longer advances the cursor | Owner report. `toggleAt` moved the cursor down after marking, so the file just marked could not be re-read without pressing `k`, and repeated presses marked successive files instead of toggling one. `TestSpaceMarksTheFileAndLeavesTheCursorOnIt` pins it; confirmed to fail with the advance restored. |
| 2026-09-16 | marks persist locally; cursor clamp fix | Two owner requests. Reviewed marks are cached under the git directory keyed on the commit under review (discovery 13), restored only on a matching diff key. Separately, `clamp()` bounded the cursor from above only, so `k` at the top made it negative and `View` indexed `files[-1]`, panicking the program. |
| 2026-09-16 | no undo for submissions | Owner asked whether `review reopen` should undo a submission (reset the commit, or a cancelling commit on top). Neither: D5 records that a submission is corrected by submitting again, with `previous_review` in the submit output making supersession visible. Evidence in the discussion: deleting the review ref left `State: FEEDBACK` unchanged. |
| 2026-09-16 | covered span ends at the submission's parent | Second owner report on the same command: reopen listed the thread and files the submission itself wrote (discovery 12). `coveredSpan` now diffs to `<review>^` from the merge base, dropping the previous-review start point. |
| 2026-09-16 | author-side commands | `change feedback` and `change wait`, and every author-facing hint retargeted off `diff --unreviewed`, which is the reviewer's span and empty the moment a submission lands (discovery 14). |
| 2026-09-16 | the TUI became one list | Files and the changeset section under one cursor, `Tab` between them, threads nested under a collapsing heading, `+ new thread…` as the group's action, `d` as an explicit diff key (discovery 16). The shortcut bar learned to wrap (15); the editor comes from git (19); the session keeps its alt screen through a handoff (18, 20). |
| 2026-09-16 | diff preview pane | A right pane printing git's own bytes, with line numbers, wrapping, tabs at their stops, the reviewer's uncommitted edits under their own caption, and an adaptive split (21–24). PRD §3 stays intact: nothing in the pane interprets a diff. |
| 2026-09-16 | Review Span Selection (separate plan) | Spans became checkpoints with a capability answer derived from the head; `V` picker, `v` ring, `r` drift refresh, `--base-*`/`--head-*`. Recorded in `docs/plans/completed/review-span-selection/`; it removed the covered span (11, 12) and replaced `reopen`'s fallback. |
| 2026-09-16 | **completion audit (this row)** | Audited and archived out of `active/`. Corrected: the Python architecture tree and the `uv`/`pytest` task lists against the Go tree that exists (D1); M4's "13 commits" (now 14, because init commits its scaffold); M5's "not yet verified" difftool gap (verified since) and its never-written `TERM=dumb` smoke test; the covered-span claims; the suite totals (237/13 → 335/14); a truncated sentence in Success Criterion 11; a `--no-tui` mitigation that never existed; `CHANGELOG.md` and the M6 transcript, both listed and never delivered. A second read-only pass then found what the first missed: the survival invariant naming `git diff` where the code runs `git show -U0 --no-ext-diff --no-textconv`, hygiene described as a grep where it is a three-layer syntax scan over seven verbs, `status --json` claimed exact where it is a sixteen-field superset, a span label format that has never been printed, a base-conflict credit to a script with no such step, three wrong evidence counts, and four references that break on the move. `e2e-29.sh` re-run green; the research spike re-run green. Full record:
`audits/2026-09-16-completion.md` — immutable; further corrections go in this plan, not there. |
| 2026-09-17 | **the audits' follow-ups, worked** | Six things the two audits recorded and left open, done. `--tool`'s help stopped promising a working tree for every span (`TestToolFlagHelpNamesBothKindsOfSpan`). `e2e-29.sh` grew the base-conflict step its M1 row always claimed (`change init --base trunk` → exit 2, and the base is still `main` afterwards) — falsified by removing the conflict guard, which turns both new lines red. The pty hand runs became `artifacts/pty-walkthrough.sh` plus `pty-tui.py`/`pty-plain.py`, checked against four mutants so it is a test and not a demo. The multi-ref drift branch, `review open`'s span-flag registration, the head-side mutual exclusion, and the default span at the session level gained tests; `TestBaseIsOwnBranch` lost a duplicate case row whose label made a failure ambiguous. One thing the plan promised is still not there and now has a number rather than a promise: with `TERM=dumb` the TUI paints nothing and quits cleanly — harmless, useless, and a product decision about refusing rather than a regression. |
