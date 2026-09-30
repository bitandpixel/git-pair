# feat-editor-term-gate

## Summary

`TestEditorPrecedenceMatchesGit/VISUAL_beats_EDITOR` passed on a developer's shell and failed on the
repository's first CI run, because `git var GIT_EDITOR` reads `VISUAL` only when it believes the
terminal can show an editor — it calls a terminal unusable when `TERM` is unset or `dumb` — and the
test fixture inherited `TERM` from whoever ran it. The fixture now pins `TERM`, the precedence table
asserts both sides of git's rule, and the prose that described the order as an unconditional ladder
says what it actually is.

No behaviour change: `EditorCommand` still asks git and still falls back the same way.

## What changed

- `internal/console/console_test.go` — `editorRepo` takes a `term` parameter, drops an inherited
  `TERM=` from the fixture environment the way it already drops `GIT_EDITOR`/`VISUAL`/`EDITOR`, and
  appends the requested value once. `TestEditorPrecedenceMatchesGit` gains a `term` column and two
  rows: `VISUAL is skipped on a dumb terminal` (`VISUAL=vs EDITOR=ed` resolves `ed`) and `core.editor
  still wins on a dumb terminal`. `TestFallsBackWhereADumbTerminalLeavesGitRefusing` pins the case
  where git prints nothing and exits `1`: the fallback still resolves `VISUAL`.
- `internal/console/console.go` — the comment above `EditorCommand` names the `TERM` gate, cites
  `is_terminal_dumb()`, states git's refusal on a dumb terminal with nothing configured, and says
  plainly that the fallback does not copy that refusal.
- `README.md` (Configuration) and `PRD.md` (dependencies, and the editor resolution ladder) — same
  correction, plus what it means for a headless caller: `EDITOR` is the dependable environment
  variable, `core.editor` the dependable one everywhere, and `VISUAL` alone can be invisible to git.
- `docs/plans/editor-term-gate/plan.md` — the root cause with the `editor.c` excerpt, the measured
  `TERM` matrix, and the reasoning behind the choices.

## Design decisions

**The fixture states the condition, rather than the suite adopting one.** Setting `TERM` in
`gittest.New` would make every fixture claim a usable terminal, which is the opposite of the CI
condition that exposed the bug, and would silently change what other git-shelling tests observe.
`TERM` belongs to the case that cares about it.

**Both sides of the gate are asserted.** A single dumb-terminal row would document git's rule; the
`core.editor`-on-a-dumb-terminal row documents the part that is easy to get wrong — the gate sits
below the config rung, so configuration is unaffected.

**An unset `TERM`, not an empty one.** git's check is `!terminal || !strcmp(terminal, "dumb")`, so
`TERM=` is usable and `TERM` missing is not. The fixture removes the variable instead of blanking it,
so a case asking for a dumb terminal means what it says.

**No duplicate entries in the environment array.** The inherited `TERM` is filtered and the chosen
one appended once, so the outcome does not rest on which duplicate a C `getenv` returns.

**The fallback keeps preferring `VISUAL`.** It runs only where git refused to answer, and an error
opens no file for someone who configured exactly one editor variable. The divergence is documented
in three places and asserted, so it reads as a decision rather than an oversight. Changing it is a
behaviour decision this changeset deliberately does not make.

## Validation

- `go test -count=1 ./internal/console/` green with `TERM` unset, `TERM=dumb`, and `TERM` naming a
  usable terminal. The pre-fix repro is `env -u TERM go test -count=1 ./internal/console/ -run
  TestEditorPrecedence`, which failed with `resolved "ed", want "vs"` — the CI failure, on git
  2.43.0, without a runner.
- `-run 'TestEditorPrecedence|TestFallsBack' -v`: six precedence rows and two fallback tests, all
  passing under each `TERM` condition.
- `go test ./internal/cli -run 'Docs|Contract'` — the edited `README.md`/`PRD.md` name only commands
  that exist.
- `mise run check` — gofmt, vet, the sharded suite.

## Known limitations

- `internal/console` still resolves differently on a dumb terminal than on a usable one when only
  `VISUAL` is configured: git names `EDITOR` or nothing, and git-pair then names `VISUAL`. That is
  git's rule plus a documented divergence, not a fixed inconsistency.
- Other tests that ask git a terminal-dependent question would have the same latent problem. Only
  `internal/console` asks one today; `TERM` is not isolated globally, so a future test must pin it the
  same way.
- The `TERM` behaviour was measured on git 2.43.0 and read in its source. The CI runner reports
  2.55.0 and fails identically, which is consistent with the gate being unchanged; the excerpt quoted
  in the plan is from 2.43.0.

## Open questions

- Should the fallback mirror git's gate, so that a dumb terminal never gets a full-screen editor?
  Left open deliberately. If it should, the change belongs in `firstSet` plus a decision on what to
  answer when `VISUAL` is all that exists (`EDITOR`? `vi`? an error?).
- Should `gittest` assert that a fixture's environment carries no `TERM` at all, so a future fixture
  cannot inherit one quietly? Cheap, and it would have caught this; left out to keep the change to
  the code that had the bug.
