#!/usr/bin/env bash
# Is this commit green on GitHub? A probe for `scripts/ci/git-pair-integrate.sh --ci-probe`.
#
# Usage: scripts/ci/gh-head-green.sh <sha>
#
# Exit codes, which are the whole contract between the probe and the job:
#
#   0  green — every check and status GitHub reports for this commit finished without failing
#   1  not green — one failed, or one has not finished
#   2  cannot tell — no GitHub here, no gh, or nothing at all has run for this commit
#
# The job treats "cannot tell" as "do not merge", which is why 2 is not 0. A merge that happened because
# nothing objected to the commit is a merge of work nobody tested, and the case where nothing has run is the
# ordinary case for a head the branch moved to a moment ago.
#
# It asks about one commit rather than about a branch, so the answer cannot quietly become an answer about a
# newer head the branch reached while this job was starting. The sha it is handed is the head the declaration
# named, which is the head the merge is about to land.
#
# Two things it deliberately does not do.
#
# It does not read branch protection's list of *required* checks. "The checks GitHub can see are green" is
# one rule, written down, and a repository whose required set is narrower passes its own --ci-probe rather
# than editing this file. It also does not look at the *destination*: nothing here claims that the merge
# result is green, only that the head is. Testing what the merge produces is a different mechanism (a merge
# queue, or CI on the destination after the push), and pretending otherwise would oversell one line of this
# script.
set -uo pipefail

sha=${1:-}
if [ -z "$sha" ]; then
  printf 'usage: %s <sha>\n' "$0" >&2
  exit 2
fi
if [ -z "${GITHUB_REPOSITORY:-}" ]; then
  printf 'cannot tell: GITHUB_REPOSITORY is unset, so there is no forge to ask\n'
  exit 2
fi
if ! command -v gh >/dev/null 2>&1; then
  printf 'cannot tell: no gh on PATH to ask %s about %s\n' "$GITHUB_REPOSITORY" "${sha:0:7}"
  exit 2
fi

# The merge job's own check run lands on the same commit it is being asked about. Counted as a check in
# progress, it would make the job wait for itself forever, so the runs this workflow makes are skipped by
# name. Set GIT_PAIR_CI_IGNORE to the workflow's own name when it is not this one.
IGNORE=${GIT_PAIR_CI_IGNORE:-git-pair integrate}

runs=$(gh api "repos/$GITHUB_REPOSITORY/commits/$sha/check-runs" --paginate \
  --jq '.check_runs[] | [.name, .status, (.conclusion // "")] | @tsv' 2>/dev/null) || {
  printf 'cannot tell: the check-runs query for %s failed\n' "${sha:0:7}"
  exit 2
}
statuses=$(gh api "repos/$GITHUB_REPOSITORY/commits/$sha/status" \
  --jq '.statuses[] | [.context, .state] | @tsv' 2>/dev/null) || {
  printf 'cannot tell: the status query for %s failed\n' "${sha:0:7}"
  exit 2
}

total=0
not_green=''
while IFS=$'\t' read -r name status conclusion; do
  [ -n "$name" ] || continue
  case $name in *"$IGNORE"*) continue ;; esac
  total=$((total + 1))
  if [ "$status" != completed ]; then
    not_green="$not_green
    - $name: $status"
  elif [ "$conclusion" != success ] && [ "$conclusion" != skipped ] && [ "$conclusion" != neutral ]; then
    not_green="$not_green
    - $name: $conclusion"
  fi
done <<<"$runs"

# A commit status is the older half of the same question, and a repository can use both. `pending` counts as
# not finished, which is the answer that stops a merge rather than the one that allows one.
while IFS=$'\t' read -r context state; do
  [ -n "$context" ] || continue
  total=$((total + 1))
  if [ "$state" != success ]; then
    not_green="$not_green
    - $context: $state"
  fi
done <<<"$statuses"

if [ "$total" = 0 ]; then
  printf 'cannot tell: nothing has run for %s on %s — no checks to read is not a pass\n' "${sha:0:7}" "$GITHUB_REPOSITORY"
  exit 2
fi
if [ -n "$not_green" ]; then
  printf 'not green: %s has %s check(s) or status(es) and these are not green:%s\n' "${sha:0:7}" "$total" "$not_green"
  exit 1
fi
printf 'green: %s has %s check(s) or status(es), all of them finished and none failing\n' "${sha:0:7}" "$total"
exit 0
