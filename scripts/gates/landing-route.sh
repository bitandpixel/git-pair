#!/usr/bin/env bash
# End-to-end replay of the landing route declaration against scratch repos.
# Usage: bash scripts/gates/landing-route.sh [/path/to/git-pair]
#        (default: the name `mise run build` installs from this repository — the shared
#        ~/.local/bin/git-pair on trunk, a branch-namespaced one anywhere else)
set -uo pipefail
# What this replay covers, and why it exists: the unit tests pin each route's sentence against the
# builders, and this replay drives the installed binary through a declared repository the way a user
# or an agent meets it — a file in the tree, four commands reading it, the defect warning on stderr.
# The route is repository policy, so the replay asserts the whole contract in one place: the declared
# route names one exit everywhere, the merge sentence a merge-job repository cannot afford is gone
# from the human screens as well as from --json, and a declaration this build cannot honor shows the
# default view with the defect named rather than a guess at what was meant. Each scenario gets its own
# scratch repository, because a branch cut from the previous scenario's branch carries its changeset
# directory, and a second changeset on one branch is a refusal of its own rather than this gate's subject.
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
G=${1:-$HOME/.local/bin/$(sh "$ROOT/scripts/install-name.sh" "$ROOT")}
if [ ! -x "$G" ]; then
  printf 'no binary at %s - run `mise run build` in %s first\n' "$G" "$ROOT" >&2
  exit 1
fi
T=$(mktemp -d /tmp/git-pair-route.XXXXXX)
for __env in $(compgen -e | grep '^GIT_PAIR_' || true); do unset "$__env"; done
exec < /dev/null
trap 'rm -rf "$T"' EXIT

step() { printf '\n\033[1m### %s\033[0m\n' "$1"; }
# The capture-then-match rule e2e-29.sh states: a live git-pair process is never piped straight into
# `grep -q` under `pipefail`, because grep's early exit SIGPIPEs the producer into a false failure.
contains() { # contains <haystack> <needle> <description>
  case "$1" in
  *"$2"*) printf '  ok: %s\n' "$3" ;;
  *) printf '  FAIL: %s\n      looked for: %s\n      in: %s\n' "$3" "$2" "$1" >&2; FAILED=1 ;;
  esac
}
lacks() { # lacks <haystack> <needle> <description>
  case "$1" in
  *"$2"*) printf '  FAIL: %s\n      found: %s\n      in: %s\n' "$3" "$2" "$1" >&2; FAILED=1 ;;
  *) printf '  ok: %s\n' "$3" ;;
  esac
}
FAILED=0

# scenario <branch> <config> builds a fresh repository with one changeset taken from init to approval
# on <branch>, with <config> already in the tree so every session in the flow resolves it.
scenario() {
  R="$T/$1"
  mkdir -p "$R"
  cd "$R" || exit 1
  git init -q -b main .
  git config user.email r@example.com
  git config user.name Reviewer
  git config commit.gpgsign false
  printf 'func seed() {}\n' > seed.go
  git add -A && git commit -qm seed
  git switch -qc "$1"
  mkdir -p .git-pair
  printf '%s' "$2" > .git-pair/config
  $G init --base main >/dev/null
  printf 'package main\n\nfunc main() {}\n' > main.go
  printf '# %s\n\n## Summary\n\nOne change.\n\n## What changed\n\n- the work\n\n## Design decisions\n\n## Validation\n\n- replay\n\n## Known limitations\n\n## Open questions\n' \
    "$1" > "changesets/$1/ABOUT.md"
  git add -A && git commit -qm work
  $G change ready >/dev/null
  $G review submit --approve >/dev/null
}

step "automated: the hand-off is the whole answer"
scenario feat-auto '[landing]
route = "automated"
'
out=$($G check)
contains "$out" "next:  \`git pair check\`, then hand it to main's owner: \`git pair change integrate\`, then \`git push origin feat-auto\`" \
  "check names the hand-off as the next step"
lacks "$out" "merge into main with ordinary git" "check offers no merge exit"
lacks "$out" "or land it yourself" "check offers no merge as an alternative"
out=$($G status)
contains "$out" "hand it to main's owner" "status names the hand-off"
lacks "$out" "or hand it to" "status does not offer the hand-off twice"
out=$($G status --json)
contains "$out" '"landing_route": "automated"' "status --json names the route"
contains "$out" 'hand it to main' "status --json next_action is the hand-off"
lacks "$out" "merge into main with ordinary git" "status --json keeps no merge sentence"
out=$($G check --json)
contains "$out" '"landing_route": "automated"' "check --json names the route"

step "automated: the declared wait stands alone"
$G change integrate >/dev/null
declared=$(git rev-parse HEAD)
git update-ref refs/remotes/origin/feat-auto "$declared"
out=$($G status)
contains "$out" "waiting on main's owner to merge it" "status names the wait"
lacks "$out" "or land it yourself" "the route removed the merge exit, so the wait has no alternative"

step "custom: the repository's own sentence, substituted"
scenario feat-pr '[landing]
route = "custom"
next = "push {branch}, then open the pull request into {destination} for {changeset}"
'
out=$($G check)
contains "$out" "next:  \`git pair check\`, then push feat-pr, then open the pull request into main for feat-pr" \
  "check renders the template with the facts filled"
lacks "$out" "merge into main with ordinary git" "check keeps no built-in exit beside the template"
lacks "$out" "or hand it to" "check keeps no built-in hand-off beside the template"
out=$($G status --json)
contains "$out" '"landing_route": "custom"' "status --json names the route"

step "custom with an unknown placeholder: default view, defect named"
scenario feat-broken '[landing]
route = "custom"
next = "ask {forge} about {destination}"
'
out=$($G check 2>err.txt)
contains "$(cat err.txt)" '"forge"' "the warning names the placeholder it cannot fill"
contains "$out" "next:  \`git pair check\`, then merge into main with ordinary git" \
  "the unfillable template falls back to the default sentence"

step "unknown route: default view, defect named"
scenario feat-typo '[landing]
route = "ci"
'
out=$($G check 2>err.txt)
contains "$(cat err.txt)" "unknown landing route" "the warning names the defect"
contains "$(cat err.txt)" "the default landing advice is shown" "the warning names the fallback"
contains "$out" "next:  \`git pair check\`, then merge into main with ordinary git" \
  "the broken declaration shows the default sentence"
contains "$out" "or hand it to" "the default fork stays open under the fallback"
out=$($G status --json)
contains "$out" '"landing_route": "default"' "status --json reports the resolved route"

if [ "$FAILED" = 0 ]; then
  printf '\nlanding-route gate: all assertions passed\n'
else
  printf '\nlanding-route gate: FAILURES above\n' >&2
  exit 1
fi
