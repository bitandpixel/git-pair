#!/usr/bin/env bash
# Ask the forge to test a branch's newest commit. A request hook for
# `scripts/ci/git-pair-integrate.sh --ci-request`.
#
# Usage: scripts/ci/gh-request-ci.sh <ref> [sha]
#
# Exit codes, the same three the probe answers with:
#
#   0  requested   — GitHub accepted the request and will create a run
#   1  refused     — the repository said no: a permission this job does not hold, or a workflow that cannot
#                    be dispatched
#   2  cannot tell — no GitHub here, no gh, or no workflow named to dispatch
#
# Why a merge job needs to ask at all. This job pushes its merge with the runner's own GITHUB_TOKEN, and
# GitHub creates no workflow runs for events that token caused — the guard that stops a workflow from
# triggering itself forever. The destination's `push` trigger therefore cannot fire for a merge this job
# performed, so the destination is left holding a tree nobody built: the job proved the *declared head*
# green, and that is not the same tree whenever the destination moved between the head's run and the merge.
# A dispatch is an explicit request rather than a caused event, so the guard does not swallow it. This is the
# mechanism gh-head-green.sh declines to be — its own header calls it out: "CI on the destination after the
# push" is a different mechanism from proving a head green, and pretending one gives the other oversells it.
#
# `<ref>` names the branch to run, and [sha] is the commit the caller wanted covered. The sha is logged rather
# than sent: the dispatch API takes a branch or tag name and answers HTTP 422 for a bare commit, so naming
# the destination is the only shape that works here. The run then covers whatever that branch's tip is when
# GitHub gets to it — in the ordinary case this commit, and the log carries the sha so a reader can compare
# the two instead of assuming.
set -uo pipefail

ref=${1:-}
wanted=${2:-}
if [ -z "$ref" ]; then
  printf 'usage: %s <ref> [sha]\n' "$0" >&2
  exit 2
fi
if [ -z "${GITHUB_REPOSITORY:-}" ]; then
  printf 'cannot tell: GITHUB_REPOSITORY is unset, so there is no forge to ask\n'
  exit 2
fi
if ! command -v gh >/dev/null 2>&1; then
  printf 'cannot tell: no gh on PATH to ask %s for a run on %s\n' "$GITHUB_REPOSITORY" "$ref"
  exit 2
fi
workflow=${GIT_PAIR_CI_REQUEST_WORKFLOW:-ci.yml}

# One line of the forge's answer goes into whatever the job prints, because "refused" without the reason is
# the kind of log line that gets a reader guessing. `gh workflow run` exits non-zero on a 4xx, so anything
# that reaches the API and comes back unhappy is a refusal rather than an inability to ask.
out=$(gh workflow run "$workflow" --repo "$GITHUB_REPOSITORY" --ref "$ref" 2>&1)
rc=$?
if [ "$rc" -ne 0 ]; then
  printf 'refused: %s would not dispatch %s for %s (%s): %s\n' \
    "$GITHUB_REPOSITORY" "$workflow" "$ref" "$rc" "$(printf '%s' "$out" | head -1)"
  exit 1
fi
if [ -n "$wanted" ]; then
  printf 'requested: %s will run %s for %s, expected to cover %s\n' \
    "$GITHUB_REPOSITORY" "$workflow" "$ref" "${wanted:0:7}"
else
  printf 'requested: %s will run %s for %s\n' "$GITHUB_REPOSITORY" "$workflow" "$ref"
fi
