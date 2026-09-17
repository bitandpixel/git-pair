#!/bin/sh
# The name `mise run build` installs under, printed so the build task and the gate
# scripts agree on one rule instead of each restating it.
#
# Several worktrees of this repository get built at the same time and ~/.local/bin is one
# shared directory, so the name carries where it was built from: the branch with its
# `feat/`-style prefix stripped, the worktree's directory on a detached HEAD, or the short
# commit when nothing survives sanitizing the name. Trunk keeps the plain `git-pair` —
# that is the checkout whose `git pair` is meant to be the real thing — and
# `mise run build:prod` writes that name from anywhere, on purpose.
#
# Usage: sh scripts/install-name.sh [repo]     (default: the current directory)
set -eu
repo=${1:-.}
branch=$(git -C "$repo" branch --show-current 2>/dev/null) || branch=""
if [ -z "$branch" ]; then
  # Detached HEAD: which worktree this is is the thing the name has to say.
  branch=$(basename "$(git -C "$repo" rev-parse --show-toplevel)")
fi
case "$branch" in
  main | master | git-pair) ns="" ;; # trunk, or the trunk clone sitting on a detached HEAD
  *)
    # Keep only what a binary name can hold. A name that sanitizes away to nothing falls
    # back to the commit, rather than to the shared name it exists to avoid writing.
    ns=$(printf '%s' "${branch#*/}" | tr -c 'A-Za-z0-9._-' '-' |
      sed -e 's/--*/-/g' -e 's/^-*//' -e 's/-$//')
    if [ -z "$ns" ]; then ns=$(git -C "$repo" rev-parse --short HEAD); fi
    ;;
esac
printf 'git-pair%s\n' "${ns:+-$ns}"
