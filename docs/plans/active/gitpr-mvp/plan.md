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
   `gitpr diff <path>` resolve to the exact commit ranges in PRD §17.
8. `gitpr status --json` emits the PRD §11.1 shape and reflects the derived state.
9. `gitpr review queue --json` gives automation a stable contract.
10. `gitpr review close` verifies a clean tree, a permitted latest outcome, runs the surviving
    check (overridable), writes `refs/reviews/archive/<slug>/<sha>`, marks the changeset
    closed, and never merges/pushes/squashes. After the source branch is deleted, the complete
    unsquashed history stays reachable from the archive ref.
11. `gitpr review open` runs the TUI: file list for the resolved span, per-file reviewed
    toggles, `a` for `ABOUT.md`, `t`/`T` for threads, `Enter` for difftool, `e` for editor,
    `v` for span toggle, with terminal correctly suspended/resumed around external processes.

Nothing above requires a forge, network access, or a third-party review API.

## Context

- Greenfield repo: only `PRD.md` exists, no commits yet on `main`.
- Environment: git 2.43.0, Python 3.14.5 + uv 0.11.17, Node 24, Go 1.26.3. No Rust toolchain.
  `VISUAL=vim`, `EDITOR=vim`.
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
| D4 | What `Enter` does in the TUI | **`git difftool <span> -- <path>`**, honouring the user's configured `diff.tool` |

## Status (updated during implementation)

| Milestone | State | Evidence |
| --- | --- | --- |
| M0 scaffold + git core | done | `go build ./...`, `go vet` clean; `gitpr --help` |
| M1 `change init` | done | idempotence and base-conflict paths exercised by `artifacts/e2e-29.sh` |
| M2 lifecycle, `status`, `history` | done | e2e replay shows `READY → BLOCKED → WORKING → FEEDBACK → APPROVED → CLOSED`; `status --json` matches PRD §11.1 keys |
| M3 spans, `diff`, survival, `change ready` | done | e2e replay blocks `change ready` on exactly the untouched review line, then passes after resolution and with the override |
| M4 submit, refs, queue, close | done | e2e replay: empty approve commit, ref moves, and after `git branch -D` the archive ref still reaches 13 commits |
| M5 TUI | done, partially verified | Verified under a pty: first paint, `j/k`, `space` (0/4 → 1/4 → 2/4), `v`, `a` editor handoff, `s`+`b` submit (created `review: block demo` and moved the ref), clean `q` exit. **Not yet verified:** `Enter` launching a real difftool *inside* the TUI — the same command path is verified outside it via `gitpr diff --tool`, which reached the configured tool with the right blob paths |
| M6 docs + dogfood | in progress | `artifacts/e2e-29.sh` is the scripted replay; README is being written |

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
4. **Deferred from MVP as planned:** `review queue --global`, the `gitpr repo` registry,
   persistent review progress, and remote propagation of `refs/reviews/*`.

## Architecture

Single package, thin `cli` layer over a pure-git core. No database, no cache, no daemon.

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
- **The check is diff-shaped.** Surviving additions come from `git diff -U0 --no-renames
  <R>^ <R>` plus exact-line membership in the `HEAD` blob, with `--numstat` binary skipping
  and blank-line exclusion.
- **Refs before irreversibility.** `refs/reviews/<slug>` is updated in the same operation
  that produces the review commit, never as a separate later step.
- **No destructive verbs.** No `push`, `merge`, `rebase`, `reset`, `branch -D`, or
  `update-ref -d` anywhere in the codebase (enforced by a test that greps the source).

Exit codes: `0` success, `1` business-rule refusal (surviving additions, non-permitted
outcome, dirty tree), `2` usage/argument error, `3` repository/git error. Stable for agents.

## Milestones

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
- `gitpr change init --base <ref>` creates `changesets/<slug>/CHANGESET.yaml` and
  `ABOUT.md`; safe to re-run; refuses to run on a detached `HEAD`.

Tasks
- `changeset.py`: `for_branch()`, `dir`, `metadata` (minimal YAML written/parsed by hand —
  one `key: value` line; no PyYAML dependency), `ABOUT.md` template with PRD §6 headings.
- Idempotence: never overwrite an existing `ABOUT.md`/`CHANGESET.yaml`; `--base` conflict
  reported, `--set-base` to change it. Do not auto-commit (the author commits with their
  implementation, per PRD §22 step 4).
- `cli/change.py init`.

Verification — init twice ⇒ byte-identical files and a non-destructive notice; a populated
`ABOUT.md` survives a second `init`; `CHANGESET.yaml` round-trips `base: booking-transaction`.

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
- `cli/top.py status` with the PRD JSON key set exactly (`changeset`, `branch`, `base`,
  `state`, `head`, `latest_review{index,outcome,commit}`, `review_ref`).

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
- Bindings `j/k Enter e Space a t T v q`; file state resets to unreviewed when its diff
  changes during the session (PRD §16).

Tasks
- `tui.py` with a `ReviewSession` model (files, per-file content hash, reviewed set) kept
  separate from rendering, so the model is unit-testable without a terminal.
- `ui.py`: `curses.wrapper` + `endwin()`/`doupdate()` around subprocess handoff; restore on
  `SIGTSTP`/editor failure; re-scan the repo after the child exits.
- Session-level reset: hash `git diff <span> -- <path>` per file; invalidate the checkmark
  when the hash changes.

Verification — headless: model tests for toggling, span switch, hash-based invalidation, span
error surfacing; a `TERM=dumb` smoke test asserting clean startup/teardown and no leaked
terminal state; manual check with `vimdiff` as `diff.guitool`/`diff.tool`.

### M6 — Docs and dogfood pass (unblocked)

Deliverables
- `README.md` with the PRD §29 walkthrough, JSON contracts, exit codes, and agent guidance.
- A `CHANGELOG.md` entry; `gitpr --help` text reviewed for agent-parseability.
- End-to-end run of the §29 script in a scratch repo, transcript captured under
  `docs/plans/active/gitpr-mvp/artifacts/`.

## Spikes / Research

Completed before planning: `research/spike-git-plumbing.sh` +
`research/git-plumbing-findings.md`. Open question resolved by the spike: whether Git can
reliably expose "lines added by one commit, still present at HEAD" — it can, with the binary,
rename, blank-line and root-commit caveats recorded there. Remaining spike: none blocking;
M5's terminal-restore behaviour is validated by its own smoke test rather than a spike.

## Risks

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Literal §19 semantics make `change ready` fail on `ABOUT.md`/thread noise (measured: 4/6 additions) | Check becomes wallpaper; agents auto-override | D3 default scopes blocking to non-`changesets/` paths; artifact survivals still reported, not silenced |
| Base ref absent/unresolvable (fresh clone, renamed base, merged stack) | Wrong or empty spans, silent `main` fallback | Fail with an explicit "cannot resolve base X" error; never guess; test covers missing base |
| `main` advancing under a changeset shifts `merge-base` and hides history | Reviewer sees a smaller diff than expected | Print the resolved span (`base...HEAD @ <sha>`) in `diff`/`status`/TUI header so the range is never implicit |
| Blank/whitespace-only additions dominate the survival report | False positives | Blank lines excluded from the check; documented, tested |
| Review commit not on the current branch (detached HEAD, wrong branch) | Review submitted to the wrong place | Require a named branch and a matching changeset dir; `submit` prints branch + changeset + span before committing |
| `curses` on odd `TERM`s | TUI unusable, leaked terminal | Model/render split, `TERM=dumb` smoke test, `--no-tui` hint to CLI equivalents (`review about`, `review thread`, `diff`) |
| Line-membership check mis-locates duplicated review lines | Misleading `path:line` | Report occurrence count with the first match; never claim uniqueness |
| State derivation silently classifies a hand-written `GitPR-*` trailer | Ghost lifecycle event | Accept a marker only with both expected trailers (`GitPR-Changeset` matching); malformed ⇒ warning line, not a state change |

## Verification Strategy

- **Unit** — pure functions with faked `git` output where practical: slug rule, span maths,
  index resolution, outcome/state tables, thread slugify/dedup.
- **Integration (primary layer)** — real throwaway git repos built by a fixture factory,
  asserting on `git log`/`git rev-list`/`for-each-ref` outcomes rather than stdout formatting.
  Every PRD-mandated behaviour gets one; the caveats from the spike (binary, rename, blank,
  root commit, deleted path, empty review) each get their own test.
- **Golden workflow** — one long test replaying PRD §29 end to end, plus a scripted
  transcript in `artifacts/` from a manual run.
- **CLI contract** — `--json` outputs validated against a schema-ish key assertion; exit codes
  asserted per command (agent-facing surface).
- **TUI** — headless model tests + startup/teardown smoke test; real `vimdiff` handoff checked
  manually since it cannot be automated here.
- **Source hygiene** — test grepping the package for `push`, `merge`, `rebase`, `reset`,
  `branch -D`, `update-ref -d` to keep PRD §26 non-goals structurally enforced.
- Not attempted: cross-platform terminal behaviour, Windows, forge integration, CI wiring.

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
