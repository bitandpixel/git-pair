#!/usr/bin/env bash
# Prepare the tree before anything tests it — this repository's answer at the profile's `toolchain` seam
# (.dev-profiles.toml, `[commands] toolchain`).
#
# Both of the profile's jobs run it, and that is the point: `.github/workflows/git-pair-check.yml` runs it on a pull
# request and on a push to the destination, and `.github/workflows/git-pair-integrate.yml` runs it before it builds
# the merge candidate and tests that. The same step in both files is what stops a pull-request green and a candidate
# red from meaning two different builds of the tree.
#
# It runs *after* `scripts/ci/install-git-pair.sh`, which puts a pinned published release on PATH. That order is the
# reason this seam exists at all. A merge here cannot be gated by a release, because the release does not contain the
# change under review — so the tool that gates it has to come out of the tree, and the installer's copy is simply
# what gets replaced.
#
# Usage: scripts/ci/prepare-toolchain.sh
#
# Exit codes:
#   0  prepared  — mise available, the pinned tools present, and a binary built from this tree on ~/.local/bin
#   1  failed    — mise could not be obtained, or the build did not leave a binary where it said it would
#
# Three things happen here, and the third is why this is a file rather than a command line in the marker. A marker
# answer is `eval`'d inside a step, so a step that has to hand something to the steps after it has to be able to
# write to `$GITHUB_ENV` — and that is worth doing out loud, in a file a reader can open, because the value it passes
# is the name of the binary the whole job then depends on:
#
#   1. mise itself, when the runner has none. The profile ships no action steps for the consumer's toolchain, so the
#      fetch lives here rather than in a `- uses:` line. `PATH` is extended for the rest of this step and written to
#      `$GITHUB_PATH` for the steps after it; without the second half, the suite step would not find `mise`.
#   2. The tools `.mise.toml` pins — Go, and goreleaser beside it. Pinned in one file, fetched here, so a runner
#      cannot test on a Go the runner happened to ship.
#   3. The build, and the name it landed under. `mise run build` installs under the name `scripts/install-name.sh`
#      derives from the branch, so the plain `git-pair` the driver would otherwise fall back to does not exist on a
#      branch build. `GIT_PAIR_BIN` is what tells the driver and the queue report at the end of the merge job which
#      binary to ask — and it is the same one the candidate is tested against.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
cd "$repo"

if ! command -v mise >/dev/null 2>&1; then
  if ! command -v curl >/dev/null 2>&1; then
    printf 'cannot prepare: no mise on PATH and no curl to fetch one\n' >&2
    exit 1
  fi
  printf 'no mise on PATH — fetching one\n'
  if ! curl -fsSL https://mise.run | sh; then
    printf 'cannot prepare: the mise installer failed\n' >&2
    exit 1
  fi
  export PATH="$HOME/.local/bin:$PATH"
  if [ -n "${GITHUB_PATH:-}" ]; then
    printf '%s\n' "$HOME/.local/bin" >>"$GITHUB_PATH"
  fi
fi

if ! command -v mise >/dev/null 2>&1; then
  printf 'cannot prepare: mise is still not on PATH after installing it\n' >&2
  exit 1
fi

# Trust the config before asking anything of it. A runner is not a person at a terminal: an untrusted `.mise.toml`
# makes mise decline the pins rather than stop and ask, which would build the job on whatever Go the image carries
# and leave the version question unanswered in a log nobody reads.
mise trust .mise.toml
mise install

mise run build

name=$(sh scripts/install-name.sh)
bin="$HOME/.local/bin/$name"
if [ ! -x "$bin" ]; then
  printf 'cannot prepare: mise run build left no executable at %s\n' "$bin" >&2
  exit 1
fi

if [ -n "${GITHUB_ENV:-}" ]; then
  printf 'GIT_PAIR_BIN=%s\n' "$bin" >>"$GITHUB_ENV"
  printf 'prepared: %s, and the steps after this one will be asked about it\n' "$bin"
else
  printf 'prepared: %s\n' "$bin"
  printf 'GITHUB_ENV is unset, so this was a developer run: GIT_PAIR_BIN was not exported\n'
fi
