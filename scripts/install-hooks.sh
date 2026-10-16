#!/usr/bin/env bash
# Point this clone's git hooks at the repository's own hook scripts.
#
# usage: scripts/install-hooks.sh [--force]
#
# Run it once per clone, and again after a change to what `scripts/git-hooks/` holds. `mise run
# hooks:install` is the same command with the toolchain active.
#
# Why an installer exists at all: git reads hooks from its own directory, outside the tree and outside
# review, so nothing a repository commits can install itself. git-pair's tracked hook root has the same
# problem and answers it the same way — a script that runs must be accepted by a person, once, on that
# machine.
#
# Why a shim and not a copy: a copy goes stale the moment the tracked script is edited, and the stale one
# is what git runs. The file written here resolves the path at commit time, so the hook that runs is the
# one in the worktree you are committing from — which matters in a repository worked in several worktrees
# at once, where the alternative is a hook from whichever branch happened to be installed last.
#
# Why a shim and not `core.hooksPath`: setting that redirects every hook point to this directory, so a
# clone already running something from its hook directory — git-lfs, a person's own notification script,
# an editor's integration — stops running it without being asked. Installing one named file leaves the
# rest of the directory alone.
#
# The shim stays out of the way when the tree has nothing to run: a clone at a branch from before the
# hook, or a worktree checked out to a commit that never had it, commits as it did before.
#
# Exit codes: 0 installed or already in place, 1 a foreign hook is in the way (re-run with --force, which
# moves the file aside rather than deleting it), 2 wrong usage.

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
SRC="$ROOT/scripts/git-hooks"

FORCE=0
case "${1:-}" in
  "") ;;
  --force) FORCE=1 ;;
  *)
    printf 'usage: scripts/install-hooks.sh [--force]\n' >&2
    exit 2
    ;;
esac

if [ ! -d "$SRC" ]; then
  printf 'install-hooks: nothing to install — %s does not exist\n' "scripts/git-hooks" >&2
  exit 0
fi

# The common directory, not `--git-dir`: every worktree of one clone shares one hook directory, so this
# installs once for all of them and says so.
common=$(git -C "$ROOT" rev-parse --git-common-dir)
case "$common" in
  /*) ;;
  *) common="$ROOT/$common" ;;
esac
HOOKS="$common/hooks"
mkdir -p "$HOOKS"

installed=0
inplace=0
for src in "$SRC"/*; do
  [ -f "$src" ] || continue
  name=$(basename "$src")
  target="$HOOKS/$name"
  marker="# git-pair repository hook: scripts/git-hooks/$name — installed by scripts/install-hooks.sh"

  # Anything in the way that is not this installer's own output is somebody else's hook. Overwriting it
  # quietly is how a formatting change loses a person's signing or notification script.
  if [ -e "$target" ] && ! grep -qF "git-pair repository hook: scripts/git-hooks/$name" "$target" 2>/dev/null; then
    if [ "$FORCE" != 1 ]; then
      printf 'install-hooks: %s is not this repository'"'"'s hook — refusing to overwrite it.\n' "$target" >&2
      printf '               Run it beside this one by renaming it, or re-run with --force to move it\n' >&2
      printf '               aside as %s.bak.\n' "$name" >&2
      exit 1
    fi
    mv "$target" "$target.bak"
    printf '  %-14s existing hook moved aside to %s.bak\n' "$name" "$name"
  fi

  # Built in a temporary file first, so an existing hook is left untouched when it already says exactly
  # this — and so a failure part-way through writing cannot leave a half-written hook that git would run.
  tmp="$target.tmp.$$"
  {
    printf '#!/bin/sh\n'
    printf '%s\n' "$marker"
    printf '# Resolved at commit time, so the hook that runs is the one in the worktree being committed\n'
    printf '# from. Silence when that file is absent: a branch without the hook commits as it did before.\n'
    printf 'root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0\n'
    printf '[ -f "$root/scripts/git-hooks/%s" ] || exit 0\n' "$name"
    printf 'exec bash "$root/scripts/git-hooks/%s" "$@"\n' "$name"
  } >"$tmp"

  if [ -f "$target" ] && cmp -s "$tmp" "$target"; then
    rm -f "$tmp"
    printf '  %-14s already in place\n' "$name"
    inplace=$((inplace + 1))
    continue
  fi

  chmod 755 "$tmp"
  mv -f "$tmp" "$target"
  printf '  %-14s installed\n' "$name"
  installed=$((installed + 1))
done

printf 'hooks installed in %s\n' "$HOOKS"
printf 'That directory is shared by every worktree of this clone, so one install covers them all.\n'
if [ "$installed" = 0 ]; then
  printf 'Nothing changed: %d hook(s) were already in place.\n' "$inplace"
fi
