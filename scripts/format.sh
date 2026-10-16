#!/usr/bin/env bash
# Format this repository's markdown, or report the files that are not formatted.
#
# usage: scripts/format.sh check [PATH...]   # the gate: exit 1 and name what needs formatting
#        scripts/format.sh write [PATH...]   # rewrite in place, and print the files it changed
#
# With no PATH the input is every tracked `.md` file. With PATHs it is those, which is how the
# pre-commit hook asks about one commit instead of the whole tree. Paths outside the tracked corpus
# still obey `.prettierignore`, so a caller cannot format an excluded file by naming it.
#
# Exit codes: 0 formatted, 1 not formatted (`check`), 2 wrong usage, 3 no formatter available.
# The third is distinct because its two callers want different answers. `mise run check` fails on it —
# a gate that cannot run is not green. The pre-commit hook commits anyway and says so, because that
# hook is a convenience and the gate is the rule; a person without the toolchain still has to be able
# to commit.
#
# Two decisions live in prettier.config.mjs and are worth restating, because they are the two the
# formatter will not let you win: it pads every table column to align the pipes, and it writes
# emphasis with `_` rather than `*`. Prettier offers no option for either — the maintainers have
# declined both — so this repository accepts them as the price of one command that makes any two
# people's markdown identical. What the config does steer is `proseWrap: preserve`, which keeps the
# hand-wrapped line breaks: a formatter that re-wraps prose turns a one-sentence edit into a
# whole-paragraph diff in the file a reviewer is reading.
#
# The input is `git ls-files`, not a `**/*.md` glob, for two reasons. An untracked scratch file under
# the repository — a replay's working directory, a pasted log — is not this repository's markdown and
# must not be rewritten by a gate run. And the ignore rules in `.prettierignore` are about committed
# bytes owned by another tool, which is the set worth stating precisely.

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
MODE=${1:-check}
case "$MODE" in
  check | write) shift ;;
  *)
    printf 'usage: scripts/format.sh {check|write} [PATH...]\n' >&2
    exit 2
    ;;
esac

cd "$ROOT"

# The formatter is pinned in `.mise.toml` beside Go and goreleaser, so a mise task already has it on
# PATH. The second branch is for callers that do not run inside the mise toolchain — the git hook,
# mostly, which git starts with whatever environment the committing process happened to have.
if command -v prettier >/dev/null 2>&1; then
  PRETTIER=(prettier)
elif command -v mise >/dev/null 2>&1 && mise exec -- prettier --version >/dev/null 2>&1; then
  PRETTIER=(mise exec -- prettier)
else
  printf 'format: no prettier on PATH and none in the mise toolchain.\n' >&2
  printf '        `.mise.toml` pins one; `mise install` puts it where the next run will find it.\n' >&2
  exit 3
fi

files=("$@")
if [ "${#files[@]}" -eq 0 ]; then
  mapfile -d '' -t files < <(git ls-files -z -- '*.md')
fi
if [ "${#files[@]}" -eq 0 ]; then
  printf 'format: no markdown to check\n'
  exit 0
fi

ERR=$(mktemp)
trap 'rm -f "$ERR"' EXIT

# `--list-different` rather than `--check`, so the names come out of Prettier once and this script owns
# the wording around them. A nonzero exit above 1 is Prettier failing — a file that is not there, a
# config it cannot read — and its own text is the only useful report of that.
out=$("${PRETTIER[@]}" --list-different "${files[@]}" 2>"$ERR") || rc=$?
rc=${rc:-0}
if [ "$rc" -gt 1 ]; then
  printf 'format: prettier could not run:\n' >&2
  cat "$ERR" >&2
  exit 1
fi

# Everything Prettier left out of the list is already formatted, and `.prettierignore` keeps excluded
# files out of the list too — which is what stops `write` from rewriting, and so re-staging, a file this
# repository has said another tool owns.
if [ -z "$out" ]; then
  exit 0
fi

mapfile -t changed <<<"$out"

if [ "$MODE" = check ]; then
  printf 'not formatted: %d file(s) differ from what prettier produces\n' "${#changed[@]}"
  printf '  %s\n' "${changed[@]}"
  printf '\nformat them: mise run format\n'
  # Worth saying, because the failure looks like the formatter misbehaving: a run that changes the file and
  # a run that changes it again are not one formatter settling down. A code span opened on one line and
  # closed on the next is re-printed differently each pass, so the file never converges and `mise run format`
  # cannot fix it. README's Development section states the rule.
  printf '\nIf a file is still listed after formatting, a code span probably wraps across a line break:\n'
  printf "close each span on the line it opens, and quote a sentence containing inline code with two\n"
  printf "backticks rather than one. README's Development section says why.\n"
  exit 1
fi

"${PRETTIER[@]}" --write "${changed[@]}" >/dev/null
printf '%s\n' "${changed[@]}"
