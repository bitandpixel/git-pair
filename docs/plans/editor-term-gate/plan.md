# The editor precedence test depended on the developer's `TERM`

## Goal

`TestEditorPrecedenceMatchesGit` passes or fails according to the `TERM` of whoever ran it, so the
case asserting `VISUAL` over `EDITOR` is green on a laptop and red on a CI step. Fix the fixture so
the assertion says what it depends on, and say the same thing in the prose that currently describes
git's editor resolution as an unconditional four-rung ladder.

Observed in `feat/change-integrate-cmd` on 2026-09-27 — the repository's first CI run
(`36294210281`, job `gates`):

```
--- FAIL: TestEditorPrecedenceMatchesGit/VISUAL_beats_EDITOR
    console_test.go:76: resolved "ed", want "vs"
```

Nothing in `internal/console` changed in that changeset. The failure reproduced locally on the same
git, without a runner, once the inherited variable was removed:

```
$ env -u TERM go test -count=1 ./internal/console/ -run TestEditorPrecedence
--- FAIL: TestEditorPrecedenceMatchesGit/VISUAL_beats_EDITOR
    console_test.go:76: resolved "ed", want "vs"
```

`git var GIT_EDITOR` consults `VISUAL` only when it believes the terminal can show one, and it calls
a terminal unusable when `TERM` is unset or `dumb`. From git's `editor.c` (v2.43.0, unchanged in
2.55.0 as far as this behaviour goes):

```c
int is_terminal_dumb(void)
{
	const char *terminal = getenv("TERM");
	return !terminal || !strcmp(terminal, "dumb");
}

const char *git_editor(void)
{
	const char *editor = getenv("GIT_EDITOR");
	int terminal_is_dumb = is_terminal_dumb();

	if (!editor && editor_program)          /* core.editor */
		editor = editor_program;
	if (!editor && !terminal_is_dumb)     /* VISUAL: gated on TERM */
		editor = getenv("VISUAL");
	if (!editor)
		editor = getenv("EDITOR");
	if (!editor && terminal_is_dumb)
		return NULL;                        /* `git var GIT_EDITOR` then exits 1 */
	...
}
```

`gittest.New` isolates `HOME`, `GIT_CONFIG_GLOBAL`, `GIT_CONFIG_SYSTEM`, `GIT_TERMINAL_PROMPT`, the
pagers and the locale, and not `TERM`, so the fixture's git inherited the shell's — `tmux-256color`
here, which is why the case had always looked fine.

## Success criteria

- `go test ./internal/console/` passes with `TERM` unset, with `TERM=dumb`, and with `TERM` naming a
  usable terminal, and the three results cannot disagree by construction.
- The precedence table states the `TERM` condition in its own rows: `VISUAL` wins on a usable
  terminal, `VISUAL` is skipped on a dumb one, `core.editor` wins on both.
- The prose in `internal/console`, `README.md`, and `PRD.md` names the condition instead of
  describing the order as unconditional, and says what git does when nothing is configured on a dumb
  terminal (answers nothing, exits 1) and where git-pair's fallback diverges.
- No behaviour change in `internal/console`: `EditorCommand` still asks git and still falls back the
  way it did.

## Non-goals

- Making the fallback honour git's `TERM` gate — skipping `VISUAL` on a dumb terminal. The fallback
  runs only where git refused to answer, and a person who configured only `VISUAL` meant it. This
  change documents the difference and asserts it; it does not decide it.
- Isolating `TERM` for the whole suite in `gittest.New`. Every fixture would then run git as if on a
  usable terminal, which is the opposite of what the CI job needs, and other tests that shell out to
  git have no opinion about the terminal.
- Anything about `GIT_SEQUENCE_EDITOR`, `core.editor` quoting, or the difftool.

## Context

The test's own comment claimed "the order git documents". git's man page does list the four rungs,
and omits the gate on the third. The assertion was therefore written against the documentation
rather than against git, and held for as long as nobody ran it where git behaves as the code says.

Only one of the four cases was `TERM`-sensitive, which matches the CI output exactly: `GIT_EDITOR` is
read first, `core.editor` before the check, `EDITOR` unconditionally. `TestFallsBackToTheEnvironment-
WhenGitCannotAnswer` forces a git failure with a non-repository, so it never reaches the gate either.

## Constraints

- `internal/hygiene` still finds no merge, push, or ref write in Go sources.
- The fixture must not leave a duplicate `TERM` in one environment array: the filter drops the
  inherited value and the chosen one is appended once, so the test does not depend on which
  duplicate a C `getenv` returns.
- Tests keep asserting git's real behaviour rather than git-pair's opinion of it; where the two
  differ, the difference is named in both the comment and the table.

## Milestones

- [x] **M1 — pin `TERM` in the fixture, and assert both sides of the gate.** `editorRepo` takes a
      `term` parameter, filters `TERM=` from the inherited environment the way it already filters
      `GIT_EDITOR`/`VISUAL`/`EDITOR`, and appends the requested value once. The table gains
      `VISUAL is skipped on a dumb terminal` and `core.editor still wins on a dumb terminal`. A new
      test pins the fallback where git exits 1: `TERM=dumb`, no editor variable in the repository
      environment, `VISUAL` in the process environment, and `VISUAL` is what resolves. Verified with
      `go test -count=1 ./internal/console/` under unset, `dumb`, and usable `TERM`.
- [x] **M2 — correct the precedence prose.** `internal/console/console.go` above `EditorCommand`,
      `README.md` (Configuration), `PRD.md` (the dependencies bullet and the resolution ladder). Each
      now names the `TERM` gate, git's `1`-exit when nothing is configured on a dumb terminal, the
      consequence for headless callers (`EDITOR`, or `core.editor`), and the one place git-pair
      deliberately answers where git refused.
- [x] **M3 — plan and `ABOUT.md`.** This file, and the changeset description.

## Spikes / research

Measured on git 2.43.0 with `VISUAL=vs EDITOR=ed`, `HOME` and both config files isolated, git
invoked three ways (`stdin` from `/dev/null`, from a pipe, from a pty). The answer was the same in
all three columns; only `TERM` moved it:

| `TERM` | `/dev/null` | pipe | pty |
| --- | --- | --- | --- |
| `xterm-256color` | `vs` | `vs` | `vs` |
| `xterm` | `vs` | `vs` | `vs` |
| empty (`TERM=`) | `vs` | `vs` | `vs` |
| `dumb`, or unset | `ed` | `ed` | `ed` |

Two details worth keeping: an empty `TERM` counts as usable (the pointer is non-null and the value is
not `dumb`), which is why `editorRepo` unsets rather than empties; and with `TERM` dumb and nothing
configured, `git var GIT_EDITOR` prints nothing and exits `1` — so a caller that treats "git said
nothing" as "no editor configured" reaches the fallback, which is git-pair's behaviour.

## Risks

- **A future git removes the gate.** Then the dumb-terminal cases fail, and they fail with the
  message naming `VISUAL` — which points straight at the rule being asserted. Accepting that failure
  as the useful signal is the choice made here, over deleting the cases.
- **Pinning `TERM` could hide a genuine dependency on the ambient terminal.** It cannot: the value is
  a parameter of the case, and the case names it.
- **The suite elsewhere inherits `TERM` too.** Any other test that asks git a terminal-dependent
  question has this bug. None found today; `grep -rn "GIT_EDITOR\|VISUAL" internal/` names only
  `internal/console` and the harness.

## Verification strategy

- `go test -count=1 ./internal/console/` with `TERM` unset, `TERM=dumb`, `TERM=tmux-256color`.
- `go test -count=1 ./internal/console/ -run 'TestEditorPrecedence|TestFallsBack' -v` — six
  precedence rows plus the two fallback tests, all passing under each of those three.
- `go test ./internal/cli -run 'Docs|Contract'` — the corrected `README.md` and `PRD.md` still name
  only commands that exist.
- `mise run check` — gofmt, vet, the whole sharded suite.
- The three gate scripts are unaffected (no CLI behaviour changed) and run anyway as part of the
  handoff where `mise run gates` is used.

## Audit history

| Date | Finding | Action |
| --- | --- | --- |
| 2026-09-27 | Root cause established in `feat/change-integrate-cmd` by reproducing the CI failure with `env -u TERM`, and against git's `editor.c`. The integrate changeset is untouched by it. | Opened this changeset off `main` rather than stacking the fix onto unrelated work. |
| 2026-09-27 | M1 green under all three `TERM` conditions. M2 prose corrected in three files. | Plan and `ABOUT.md` written; handed off with `change ready`. |

## Execution status

Complete, awaiting review.
