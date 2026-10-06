# feat-edit-bug

## Summary

Opening a file whose name contains `$` in the review TUI gave an empty buffer. Reported against a
Next.js repository, on `src/routes/api/businesses/$businessId/offering-schedules/$offeringScheduleId/activate.ts`
and its `generate.ts` sibling: `e` handed the terminal to the editor, and the editor drew a new,
empty file.

The editor was given a path that is not that file. The launch line was:

```go
exec.Command("/bin/sh", "-c", `eval exec ${GIT_PAIR_EDITOR} "$@"`, "git-pair", path)
```

Two parses happen there. `"$@"` carries the path as one word, quoting intact — that half was
correct. Then `eval` assembles a command string from the expanded words and parses *that*, so the
path is shell input at the second parse and its `$businessId` is a variable reference. The two
dynamic directories expanded away, the path that remained does not exist, and an editor asked to
open a nonexistent file shows an empty buffer.

What the second parse did to a path, each verified against the old line:

| path held | the editor was given |
| --- | --- |
| `$businessId` | nothing, or the value of that environment variable — the wrong file, silently |
| `` $(...) `...` `` | the substitution ran, as the reviewer's own user |
| `*`, `?`, `[a]` | the matching files, when any matched |
| `a b` | two arguments |
| `a\b` | `ab` |
| `a'b`, `a"b`, `a;b`, `a&&b`, `a\|b` | a syntax error, or a truncated argument |

Only files with those characters hit it, which is why it surfaced on dynamic-route paths and not
before. The TUI, the session, and the path handed to `EditorCommand` were all correct; the damage
was one line inside the launch.

## What changed

- `internal/console/console.go`: the launch script now parses only the editor value as shell.

  ```sh
  path=$1
  eval "set -- ${GIT_PAIR_EDITOR}"
  if [ "$#" -eq 0 ]; then
      printf '%s\n' 'git-pair: editor setting is empty' >&2
      exit 127
  fi
  exec "$@" "$path"
  ```

  The value still goes through `eval`, so `code --wait` still splits into a program and its flags
  and quoting inside the value is still honoured. The path is assigned from `$1` before `set --`
  overwrites the positionals, and is quoted at the `exec`, so no parser reads its contents again.
  The comment records why the two halves are parsed differently.
- `internal/console/console_test.go`:
  - `editorProbe(t)`, extracted from `TestEditorCommandLineIsSplitAndGivenTheFile`, so the stand-in
    editor that logs its `argv` is one script. Its arguments are now bracketed, because a log that
    joins them cannot tell one argument containing a space from two arguments — which is one of the
    bugs.
  - `TestEditorPathIsNotReadAsShell`: 14 cases, each a real file created in the fixture repository,
    asserting on the `argv` the editor actually received rather than on what git-pair said it sent.
  - `TestEditorValueThatExpandsToNothingNamesNoProgram`: the `$#` guard.

## Design decisions

**Keep `eval` for the value, keep the path out of it.** The value needs shell semantics for the
same reason git runs it through a shell: `EDITOR="code --wait"` is a program plus flags, and
`core.editor="/my editor.sh" --wait` quotes its way past a space in the program name. Dropping to
`strings.Fields` would break the quoted case, and quoting the expansion would treat the whole value
as one program name. Both were tried before and are refused by the comment above the launch. Only
the path needed rescuing, so only the path moved.

**`path=$1` before `eval "set -- …"`, not after.** `set --` replaces the positional parameters, so
`$1` is the path only until that line runs. Reading it into a variable first is what leaves
something to quote at the `exec`; an assignment's right-hand side is not re-scanned, so the value
arrives byte for byte.

**Guard the zero-word case rather than assume it away.** A value that expands to nothing —
`core.editor=$UNSET` — is not the empty setting the fallback above it already replaces: it reaches
the shell as words that vanish. `exec "$@" "$path"` then has one word and execs the reviewed file,
which is the file the reviewer is standing in front of. `git var GIT_EDITOR` echoes that config
value verbatim, so the case is reachable. 127 and a line on stderr is the shape of "the program
named by the setting could not be run", and `openFile` in the CLI already wraps a non-zero exit as
"editor exited with an error".

**The editor value is still shell-parsed, deliberately.** A value containing `$`, a backtick or a
glob behaves the way git's own value handling makes it behave. That input is the reviewer's git
config, which is trusted in exactly the same way git trusts it, and this changeset does not change
it.

**Nothing else needed the same fix.** The difftool and the diff go to `git` as `exec` arguments with
no shell between, so their paths were never parsed; the same is true of every other `exec.Command`
in the tree. The Windows branch of `EditorCommand` never had a shell in it, which is why the bug was
POSIX-only.

## Validation

- The new tests fail against the old launch line and pass against the new one. Before the fix, 13 of
  the 14 path cases were mangled (`$businessId` → `EVIL`, `$offeringScheduleId` → empty, the space
  case split into two arguments, both substitutions ran and created their marker files) and the
  zero-word case exited 0 after exec-ing the reviewed file. The `*` case passes either way in a
  directory with nothing to match; it guards the quoting at the `exec`.
- `businessId=EVIL` is set in the environment for every case, so the path surviving unexpanded is
  not an artefact of the variable being unset.
- Checked against the reporting repository itself, with `GIT_EDITOR` resolved through
  `git var GIT_EDITOR` and `businessId` set, for both reported files: the editor received
  `ARGV:[/home/david/dev/worktrees/cadence/m4-generate/src/routes/api/businesses/$businessId/offering-schedules/$offeringScheduleId/activate.ts]`
  and the same for `generate.ts`. That check was a temporary test file and is not in the changeset.
- `gofmt -l` clean, `go vet ./...` clean, `go test ./...` green, including the editor tests that
  already pinned the rest of the launch: precedence, the dumb-terminal rungs, `GIT_EDITOR=true`, and
  flags-before-file.

## Known limitations

- A path with a newline reaches the editor as one argument (correct), where the old line would have
  split it; no case asserts it, since a newline in a filename is rarer than the ones listed and the
  mechanism is the same word splitting the space case covers.
- The guard reports `git-pair: editor setting is empty` on the child's stderr rather than as a
  returned error, because an empty value is only knowable after the shell has expanded it. The
  caller's message is the one a reviewer reads.
- A `core.editor` that relies on shell features beyond splitting and quoting (`VAR=x editor`,
  `&&`) keeps working exactly as far as git's own handling does, which is to say incidentally and
  without a test.

## Open questions

- Should git-pair split the value itself, the way git's `split_cmdline` does, and drop the shell
  from this launch entirely? It would remove the last `eval`, make the value glob-free, and match
  git's tokenizer rather than approximate it with `eval`. It also changes what a quoted value means
  for everyone who has one configured, which is a larger claim than the bug, so it is left out of
  this changeset.
- Should the guard's message be pinned by a test? It is asserted only through the exit status today.
