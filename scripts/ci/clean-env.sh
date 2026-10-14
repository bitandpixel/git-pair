#!/usr/bin/env bash
# Run the repository's own tests in an environment they were not given.
#
# Usage: scripts/ci/clean-env.sh <command> [args...]
#
# Why this exists. A CI job injects an environment, and the suite was written against nobody's. Two failures
# came from that distance, and neither was visible until the ladder ran inside a merge job:
#
#   a verdict that reported its own plumbing    The step exports GIT_PAIR_DEFAULT_BRANCH so the driver knows
#                                               where to merge. With it set, git-pair says which source its
#                                               destination came from, and the suite has tests asserting that
#                                               a verdict carries no such tool line — so they failed, on a tree
#                                               that was fine, and the candidate was (correctly) refused.
#   a replay that reached the forge             The driver asks the destination to test what it pushed, and the
#                                               default for that ask is the shipped hook, which fires whenever
#                                               GITHUB_REPOSITORY is set. One pass of the suite produced six
#                                               real `gh workflow run ci.yml --repo bitandpixel/git-pair --ref
#                                               main` calls against this repository, each caused by a merge
#                                               into a scratch repository that has nothing to do with it.
#
# Why a blank slate and not a list of names to unset. The list has to be written before the next variable
# arrives, and its failure is silent: the variable nobody thought of gets through, and the suite is wrong in a
# way that only reproduces on a runner. Starting from nothing inverts that. Anything the job injects is absent
# unless it is named below, so the failure mode is a tool saying it cannot find what it needs — loud, here,
# rather than quiet on the destination. `scripts/gates/pty-tui.py` already removes `CI`, `NO_COLOR` and
# `CLICOLOR_FORCE` for the same reason, and documents the rule as "removed rather than countered"; this is the
# same rule applied one level up, to the whole ladder.
#
# What is kept is what a toolchain needs to run at all, and nothing that describes where it is running:
#
#   PATH, HOME                    mise's shims and Go's cache; the two the ladder above was measured to need
#   TERM, TMPDIR, LANG, LC_ALL    the pty walkthrough's own terminals, and the scratch trees it builds
#   GOROOT, GOPATH, GOBIN, ...    the Go toolchain mise-action installs, kept so the build is the one it set up
#   MISE_* except its token       mise's own configuration, including the trust setting a worktree needs
#   SSL_CERT_*, *PROXY            whoever's network the runner reaches through
#
# Deliberately absent, in case the list above looks short: `GITHUB_TOKEN`, `GH_TOKEN`, `GITHUB_REPOSITORY` and
# every other Actions variable (the job's identity is not the tests' business), `CI` (termenv reads any
# non-empty value as not-a-terminal, which is the trap `pty-tui.py` documents), `GIT_CONFIG_GLOBAL` and
# `GIT_CONFIG_SYSTEM` (the gates that care about git's behaviour pin those themselves, per invocation), and
# `GIT_PAIR_*` (the product's own configuration belongs to whoever is driving it, not to the code under test).
#
# This wraps the tests, not the job. The merge job's own `--ci-request` runs outside it, against the
# destination that was just pushed to, where a forge to name is exactly what is wanted.
set -uo pipefail

if [ $# -eq 0 ]; then
  printf 'usage: %s <command> [args...]\n' "$0" >&2
  exit 2
fi

keep=()
for name in PATH HOME TERM TMPDIR TEMP TMP LANG LC_ALL USER LOGNAME SHELL \
  GOROOT GOPATH GOBIN GOFLAGS GOTOOLCHAIN GOCACHE GOMODCACHE \
  XDG_CACHE_HOME XDG_CONFIG_HOME XDG_DATA_HOME XDG_STATE_HOME \
  GIT_EXEC_PATH SSL_CERT_FILE SSL_CERT_DIR CURL_CA_BUNDLE \
  HTTP_PROXY HTTPS_PROXY NO_PROXY http_proxy https_proxy no_proxy; do
  [ -n "${!name-}" ] && keep+=("$name=${!name}")
done

# mise's configuration stays, its credentials do not: mise-action is handed a token to download tools with,
# and a test has no use for it.
while IFS= read -r line; do
  name=${line%%=*}
  [ "$name" = MISE_GITHUB_TOKEN ] && continue
  keep+=("$line")
done < <(env | grep '^MISE_' || true)

exec env -i ${keep[@]+"${keep[@]}"} "$@"
