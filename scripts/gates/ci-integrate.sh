#!/usr/bin/env bash
# Replays the CI job — scripts/ci/git-pair-integrate.sh — against scratch remotes, so the merge a
# declaration asks for is covered by something that runs without a GitHub runner.
# Usage: bash scripts/gates/ci-integrate.sh [/path/to/git-pair]
#        (default: the name `mise run build` installs from this repository)
#
# What it proves, in order: a declared and ready changeset is merged as a merge commit, recorded, and
# published; a re-run does nothing twice; an undeclared changeset and a drifted declaration are left alone;
# the queue-driven poll finds a declaration with no event behind it; a dry run writes nothing; and a merge
# that conflicts is aborted, unrecorded, and reported red.
#
# What it does not: it is not a GitHub Actions test. The workflow file is thin on purpose — build, then this
# script — so the behaviour worth proving lives here, and the file's own claims (permissions, triggers,
# fetch-depth) are the kind only a runner can check.
set -uo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
G=${1:-$HOME/.local/bin/$(sh "$ROOT/scripts/install-name.sh" "$ROOT")}
if [ ! -x "$G" ]; then
  printf 'no binary at %s - run `mise run build` in %s first\n' "$G" "$ROOT" >&2
  exit 1
fi
CI=$ROOT/scripts/ci/git-pair-integrate.sh
if [ ! -f "$CI" ]; then
  printf 'no CI script at %s\n' "$CI" >&2
  exit 1
fi
exec < /dev/null
T=$(mktemp -d /tmp/git-pair-ci-gate.XXXXXX)
trap 'rm -rf "$T"' EXIT
FAILED=0
PASS=0

step()  { printf '\n\033[1m### %s\033[0m\n' "$1"; }
ok()    { PASS=$((PASS+1)); printf '  ok: %s\n' "$*"; }
fail()  { printf '  FAIL: %s\n' "$*"; FAILED=1; }
check() { # check <description> <expected-exit> <actual-exit>
  if [ "$2" = "$3" ]; then ok "$1"; else fail "$1 (exit $3, want $2)"; fi
}
contains() { # contains <haystack> <needle> <description>
  case $1 in *"$2"*) ok "$3" ;; *) fail "$3 — missing: $2" ;; esac
}
no_contains() { # no_contains <haystack> <needle> <description>
  case $1 in *"$2"*) fail "$3 — found: $2" ;; *) ok "$3" ;; esac
}

# A job's clone has no identity of its own and nothing inherited from a developer's machine, so every CI run
# here sets both away. The script under test is then forced to do what it claims about setting the merger's
# identity, and no result depends on whose laptop the gate runs on.
cigr() { # cigr <dir> [branch...] — the job, in a clone of the scratch origin
  local dir=$1; shift
  ( cd "$dir" && GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null GIT_PAIR_BIN="$G" \
      bash "$CI" "$@" 2>&1 )
}
# show prints what the job actually said. The assertions quote it; when one of them fails, the reader needs
# the run's own words rather than a needle. Off by default so the log stays a list of checks, on with
# CI_GATE_VERBOSE=1 for whoever is debugging the script.
show() { [ "${CI_GATE_VERBOSE:-}" = 1 ] && printf '%s\n' "$1" | sed 's/^/    | /'; return 0; }
clone() { # clone <name> — a throwaway clone, as a runner gets one; prints its path
  GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null \
    git clone -q "$T/origin.git" "$T/$1" >/dev/null 2>&1 || return 1
  printf '%s' "$T/$1"
}
wing() { # wing [git-pair args...] — git-pair inside the author's clone
  ( cd "$T/work" && "$G" "$@" 2>&1 )
}

# A changeset, ready for review and approved: the state a declaration is allowed to be made from. The
# starting point defaults to trunk; the conflict scenario passes an older commit, because a branch cut from
# trunk after trunk moved contains the move and cannot conflict with it.
declare_changeset() { # declare_changeset <branch> <path> <content> [start-point]
  local branch=$1 file=$2 body=$3 start=${4:-main}
  git -C "$T/work" checkout -q -b "$branch" "$start" || return 1
  wing init --base main >/dev/null || return 1
  mkdir -p "$(dirname "$T/work/$file")" || return 1
  printf '%s\n' "$body" > "$T/work/$file" || return 1
  git -C "$T/work" add -A && git -C "$T/work" commit -qm "$branch: $body" || return 1
  wing change ready >/dev/null || return 1
  wing review submit --approve >/dev/null || return 1
}

step "fixture: an origin, a trunk, and changesets in three different states"

git init -q -b main --bare "$T/origin.git"
git clone -q "$T/origin.git" "$T/work" >/dev/null 2>&1 || { echo "cannot clone the scratch origin" >&2; exit 1; }
git -C "$T/work" config user.email author@example.com
git -C "$T/work" config user.name Author
echo '# the project' > "$T/work/README.md"
printf 'alpha\nbeta\ngamma\n' > "$T/work/f.txt"
git -C "$T/work" add -A && git -C "$T/work" commit -qm initial
git -C "$T/work" push -q -u origin main >/dev/null 2>&1 || { echo "cannot seed main" >&2; exit 1; }
MAIN0=$(git -C "$T/origin.git" rev-parse main)

for b in flow/merge flow/quiet flow/drift; do
  case $b in
    flow/merge) f=src/a.ts; v='export const a = 1' ;;
    flow/quiet) f=src/b.ts; v='export const b = 2' ;;
    flow/drift) f=src/c.ts; v='export const c = 3' ;;
  esac
  declare_changeset "$b" "$f" "$v" || { echo "fixture: $b" >&2; exit 1; }
done
# Only flow/merge and flow/drift get a declaration. flow/quiet stays approved-but-undeclared, which is the
# state a push-triggered job must leave untouched.
git -C "$T/work" checkout -q flow/merge && wing change integrate >/dev/null || { echo "fixture: declare flow/merge" >&2; exit 1; }
git -C "$T/work" checkout -q flow/drift && wing change integrate >/dev/null || { echo "fixture: declare flow/drift" >&2; exit 1; }
# The drift: a commit after the declaration. The newest marker stops being a declaration, `ready` goes with
# it, and a job that merged this would merge work no reviewer ever saw.
git -C "$T/work" checkout -q flow/drift
printf 'export const c = 4\n' > "$T/work/src/c.ts"
git -C "$T/work" commit -qam "flow/drift: a commit after the declaration"
git -C "$T/work" push -q origin flow/merge flow/quiet flow/drift >/dev/null 2>&1 || { echo "fixture: push" >&2; exit 1; }
DECL=$(git -C "$T/origin.git" rev-parse refs/heads/flow/merge)
ok "origin holds three branches and one declaration at ${DECL:0:7}"

step "the job: gate, merge, push, record, publish"

CI1=$(clone ci1) || { echo "cannot clone for ci1" >&2; exit 1; }
out=$(cigr "$CI1" flow/merge); code=$?
show "$out"
check "the run succeeds" 0 $code
contains "$out" "flow-merge: declared by ${DECL:0:7} for merge into main" "it names the declaration it read and the branch it chose"
contains "$out" "merged as" "it reports the merge commit"
contains "$out" "merged, recorded, published" "it says the whole handoff finished"

TIP=$(git -C "$T/origin.git" rev-parse main)
if [ "$TIP" = "$MAIN0" ]; then fail "the destination moved"; else ok "the destination moved"; fi
PARENTS=$(git -C "$T/origin.git" rev-list --parents -n 1 "$TIP" | wc -w)
if [ "$PARENTS" = 3 ]; then ok "the landing is a merge commit, not a fast-forward"; else fail "the landing is not a merge commit ($((PARENTS-1)) parents)"; fi
SECOND=$(git -C "$T/origin.git" rev-list --parents -n 1 "$TIP" | cut -d' ' -f3)
if [ "$SECOND" = "$DECL" ]; then ok "the merge's second parent is the head that was declared"; else fail "the merge's second parent is ${SECOND:0:7}, want the declared head"; fi
contains "$(git -C "$T/origin.git" log -1 --format=%s "$TIP")" "Merge flow/merge into main" "the merge commit says what it merged"
# The merge was authored by the job: this clone ran with no global or system git configuration at all.
contains "$(git -C "$T/origin.git" log -1 --format='%ae' "$TIP")" "git-pair-ci@localhost" "the job set an author for the commit it made"
# `for-each-ref` patterns do not match across a `/`, so this asks for the namespace by prefix and greps the
# names, rather than writing a glob that quietly matches nothing at two levels deep.
REMOTE_REFS=$(git -C "$T/origin.git" for-each-ref --format='%(refname)' 'refs/git-pair/' | tr '\n' ' ')
contains "$REMOTE_REFS" "refs/git-pair/integrations/flow-merge" "the record reached the remote"
contains "$REMOTE_REFS" "refs/git-pair/archive/flow-merge" "and so did its archive copy"
no_contains "$REMOTE_REFS" "awaiting-merge" "and no durable ref was written for the declaration itself"

step "the job again: a second event for a changeset already handled"

out=$(cigr "$CI1" flow/merge); code=$?
show "$out"
check "the re-run succeeds" 0 $code
contains "$out" "nothing to merge here" "it says there is nothing to do"
if [ "$(git -C "$T/origin.git" rev-parse main)" = "$TIP" ]; then ok "and the destination did not move again"; else fail "the destination moved twice"; fi
check "one record, not two" 1 "$(git -C "$T/origin.git" for-each-ref --format='%(refname)' 'refs/git-pair/integrations/*' | wc -l | tr -d ' ')"

step "what must be left alone"

out=$(cigr "$CI1" flow/quiet); code=$?
show "$out"
check "approved work nobody declared costs nothing (exit 0)" 0 $code
contains "$out" "not merging" "and it is not merged"
contains "$out" "integrating=false" "because the author never asked"
out=$(cigr "$CI1" flow/drift); code=$?
show "$out"
check "a declaration that work moved past costs nothing (exit 0)" 0 $code
contains "$out" "ready=false" "because the gate no longer says ready"
if [ "$(git -C "$T/origin.git" rev-parse main)" = "$TIP" ]; then ok "and neither moved the destination"; else fail "an undeclared or drifted branch moved the destination"; fi

step "the poll: no event, only the queue"

declare_changeset flow/poll src/e.ts 'export const e = 6' || { echo "fixture: flow/poll" >&2; exit 1; }
git -C "$T/work" checkout -q flow/poll && wing change integrate >/dev/null || { echo "fixture: declare flow/poll" >&2; exit 1; }
git -C "$T/work" push -q origin flow/poll >/dev/null 2>&1
CI2=$(clone ci2) || { echo "cannot clone for ci2" >&2; exit 1; }
out=$(cigr "$CI2"); code=$?
show "$out"
check "the poll succeeds with no branch named" 0 $code
contains "$out" "flow-poll: declared by" "it found the declaration with no event behind it"
contains "$out" "merged, recorded, published" "and finished the handoff"
if [ "$(git -C "$T/origin.git" rev-parse main)" != "$TIP" ]; then ok "the destination moved for the poll"; else fail "the poll merged nothing"; fi
check "two changesets are now recorded" 2 "$(git -C "$T/origin.git" for-each-ref --format='%(refname)' 'refs/git-pair/integrations/*' | wc -l | tr -d ' ')"

step "the dry run"

declare_changeset flow/dry src/d.ts 'export const d = 5' || { echo "fixture: flow/dry" >&2; exit 1; }
git -C "$T/work" checkout -q flow/dry && wing change integrate >/dev/null || { echo "fixture: declare flow/dry" >&2; exit 1; }
git -C "$T/work" push -q origin flow/dry >/dev/null 2>&1
BEFORE=$(git -C "$T/origin.git" rev-parse main)
REFS0=$(git -C "$T/origin.git" for-each-ref --format='%(refname)' 'refs/git-pair/integrations/*' | wc -l | tr -d ' ')
CI3=$(clone ci3) || { echo "cannot clone for ci3" >&2; exit 1; }
out=$(cigr "$CI3" --dry-run flow/dry); code=$?
show "$out"
check "the dry run succeeds" 0 $code
contains "$out" "dry run: would merge" "it says what it would have done"
contains "$out" "into main" "and names the destination"
if [ "$(git -C "$T/origin.git" rev-parse main)" = "$BEFORE" ]; then ok "and it merged nothing"; else fail "the dry run merged"; fi
if [ "$(git -C "$T/origin.git" for-each-ref --format='%(refname)' 'refs/git-pair/integrations/*' | wc -l | tr -d ' ')" = "$REFS0" ]; then
  ok "and it wrote no record"
else
  fail "the dry run wrote a record"
fi

step "the merge that cannot be made"

# Trunk moves under the branch, in the same line, so the merge conflicts. Readiness is a question about
# review and not about mergeability, so the gate still says yes and the job has to fail at the merge.
#
# The author's clone has to look at the same trunk the job will merge into, and it has not seen the merges
# the job made from its own clone: fetch first, then fast-forward local trunk, then note where it stood.
git -C "$T/work" fetch -q origin >/dev/null 2>&1
git -C "$T/work" checkout -q main
git -C "$T/work" merge --ff-only -q origin/main >/dev/null 2>&1 || { echo "fixture: local trunk cannot catch up" >&2; exit 1; }
MAIN_BEFORE=$(git -C "$T/work" rev-parse main)
printf 'alpha\nmain moved\ngamma\n' > "$T/work/f.txt"
git -C "$T/work" commit -qam "main moves under the branch"
git -C "$T/work" push -q origin main >/dev/null 2>&1
declare_changeset flow/clash src/g.ts 'export const g = 7' "$MAIN_BEFORE" || { echo "fixture: flow/clash" >&2; exit 1; }
printf 'alpha\nbranch moved\ngamma\n' > "$T/work/f.txt"
git -C "$T/work" commit -qam "flow/clash: the same line"
git -C "$T/work" checkout -q flow/clash &&
  wing change ready >/dev/null && wing review submit --approve >/dev/null && wing change integrate >/dev/null ||
  { echo "fixture: declare flow/clash" >&2; exit 1; }
git -C "$T/work" push -q origin flow/clash >/dev/null 2>&1
BEFORE=$(git -C "$T/origin.git" rev-parse main)
CI4=$(clone ci4) || { echo "cannot clone for ci4" >&2; exit 1; }
out=$(cigr "$CI4" flow/clash); code=$?
show "$out"
check "a conflicting merge is a red run" 1 $code
contains "$out" "the merge conflicted" "it says so"
contains "$out" "nothing merged, nothing recorded" "and says what it did not do"
if [ "$(git -C "$T/origin.git" rev-parse main)" = "$BEFORE" ]; then ok "the destination is untouched"; else fail "a conflicted merge reached the destination"; fi
check "no record was written for a merge that did not happen" 2 "$(git -C "$T/origin.git" for-each-ref --format='%(refname)' 'refs/git-pair/integrations/*' | wc -l | tr -d ' ')"
if [ -z "$(git -C "$CI4" status --porcelain)" ]; then ok "the clone was left clean"; else fail "the clone was left dirty"; fi
if [ -f "$CI4/.git/MERGE_HEAD" ]; then fail "a merge was left half-done"; else ok "and no merge was left half-done"; fi

step "usage"

out=$(cd "$T" && "$CI" --help 2>&1); check "--help works" 0 $?
contains "$out" "dry-run" "and shows the flag list"
out=$(cd "$T" && "$CI" --nonsense 2>&1); code=$?
check "an unknown option is a usage error" 2 $code

if [ "$FAILED" = 0 ]; then
  printf '\n\033[1mCI-INTEGRATE: all checks passed\033[0m (%s checks)\n' "$PASS"
  exit 0
fi
printf '\n\033[1mCI-INTEGRATE: FAILURES PRESENT\033[0m\n'
exit 1
