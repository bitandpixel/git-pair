#!/usr/bin/env bash
# Replays the CI job — scripts/ci/git-pair-integrate.sh — against scratch remotes, so the merge a
# declaration asks for is covered by something that runs without a GitHub runner.
# Usage: bash scripts/gates/ci-integrate.sh [/path/to/git-pair]
#        (default: the name `mise run build` installs from this repository)
#
# What it proves, in order: a declared and ready changeset is merged as a merge commit and pushed, leaving
# the remote nothing but branches; a re-run does nothing twice; an undeclared changeset and a drifted
# declaration are left alone; the queue-driven poll finds a declaration with no event behind it; a head that
# is not proven green is not merged, whether the probe says "no" or "I cannot tell", and the probe is asked
# about the declared commit; a dry run writes nothing; a merge that conflicts is aborted and reported red; a
# changeset whose branch sits outside feat/ merges like any other, and the trigger is checked for the branch
# filter that would have stopped it; and the two workflow files still name each other, so a rename fails a
# build instead of stalling a merge.
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

step "the job: gate, merge, push"

CI1=$(clone ci1) || { echo "cannot clone for ci1" >&2; exit 1; }
out=$(cigr "$CI1" flow/merge); code=$?
show "$out"
check "the run succeeds" 0 $code
contains "$out" "flow-merge: declared by ${DECL:0:7} for merge into main" "it names the declaration it read and the branch it chose"
contains "$out" "merged as" "it reports the merge commit"
contains "$out" "pushed to main" "and it says where the landing went"

TIP=$(git -C "$T/origin.git" rev-parse main)
if [ "$TIP" = "$MAIN0" ]; then fail "the destination moved"; else ok "the destination moved"; fi
PARENTS=$(git -C "$T/origin.git" rev-list --parents -n 1 "$TIP" | wc -w)
if [ "$PARENTS" = 3 ]; then ok "the landing is a merge commit, not a fast-forward"; else fail "the landing is not a merge commit ($((PARENTS-1)) parents)"; fi
SECOND=$(git -C "$T/origin.git" rev-list --parents -n 1 "$TIP" | cut -d' ' -f3)
if [ "$SECOND" = "$DECL" ]; then ok "the merge's second parent is the head that was declared"; else fail "the merge's second parent is ${SECOND:0:7}, want the declared head"; fi
contains "$(git -C "$T/origin.git" log -1 --format=%s "$TIP")" "Merge flow/merge into main" "the merge commit says what it merged"
# The merge was authored by the job: this clone ran with no global or system git configuration at all.
contains "$(git -C "$T/origin.git" log -1 --format='%ae' "$TIP")" "git-pair-ci@localhost" "the job set an author for the commit it made"
# The whole claim of PRD §13.4, checked on the shared remote rather than in a clone: a landing leaves
# nothing there but branches. There is no namespace to publish into, so there is nothing to keep in sync with
# the merge, and no command that has to run after the push for the landing to be true.
REMOTE_REFS=$(git -C "$T/origin.git" for-each-ref --format='%(refname)' | grep -v '^refs/heads/' | tr '\n' ' ')
if [ -z "$REMOTE_REFS" ]; then
  ok "the remote holds branches and nothing else"
else
  fail "the job put refs on the remote that no branch needs: $REMOTE_REFS"
fi
git -C "$T/origin.git" cat-file -e "$TIP:changesets/flow-merge/CHANGESET.yaml" 2>/dev/null \
  && ok "the destination's tree carries the directory, which is the record of the landing" \
  || fail "the merge reached the destination without the changeset directory"

step "the job again: a second event for a changeset already handled"

out=$(cigr "$CI1" flow/merge); code=$?
show "$out"
check "the re-run succeeds" 0 $code
contains "$out" "nothing to merge here" "it says there is nothing to do"
if [ "$(git -C "$T/origin.git" rev-parse main)" = "$TIP" ]; then ok "and the destination did not move again"; else fail "the destination moved twice"; fi
ADDS=$(git -C "$T/origin.git" log --format=%h --diff-filter=A main -- changesets/flow-merge/CHANGESET.yaml | wc -l | tr -d ' ')
check "and the directory entered the destination exactly once" 1 "$ADDS"

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
contains "$out" "pushed to main" "and finished the handoff"
if [ "$(git -C "$T/origin.git" rev-parse main)" != "$TIP" ]; then ok "the destination moved for the poll"; else fail "the poll merged nothing"; fi
for id in flow-merge flow-poll; do
  if git -C "$T/origin.git" cat-file -e "main:changesets/$id/CHANGESET.yaml" 2>/dev/null; then
    ok "the destination carries $id, read from its tree"
  else
    fail "the destination does not carry $id"
  fi
done

step "the head has to be proven green"

# The probe is a stub, so what is under test is the job's three-way decision rather than any forge. The stub
# records the sha it was asked about, which is the claim worth asserting: the job asks about the commit the
# declaration named, not about whatever the branch tip happens to be when the answer comes back.
ci_probe_run() { # ci_probe_run <rc> <says> <dir> [job args...]
  local rc=$1 says=$2 dir=$3; shift 3
  ( cd "$dir" && GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null GIT_PAIR_BIN="$G" \
      PROBE_RC="$rc" PROBE_SAYS="$says" PROBE_CALLS="$T/probe-calls" \
      bash "$CI" --require-ci --ci-probe "bash $T/probe" "$@" 2>&1 )
}
cat > "$T/probe" <<'EOS'
#!/usr/bin/env bash
printf '%s\n' "$1" >> "$PROBE_CALLS"
printf '%s\n' "${PROBE_SAYS:-stub: no opinion}"
exit "${PROBE_RC:-2}"
EOS
chmod +x "$T/probe"

declare_changeset flow/ci src/h.ts 'export const h = 8' || { echo "fixture: flow/ci" >&2; exit 1; }
git -C "$T/work" checkout -q flow/ci && wing change integrate >/dev/null || { echo "fixture: declare flow/ci" >&2; exit 1; }
git -C "$T/work" push -q origin flow/ci >/dev/null 2>&1
DECL_CI=$(git -C "$T/origin.git" rev-parse refs/heads/flow/ci)
MAIN_BEFORE_CI=$(git -C "$T/origin.git" rev-parse main)
CI5=$(clone ci5) || { echo "cannot clone for ci5" >&2; exit 1; }

out=$(ci_probe_run 1 'not green: the test job failed' "$CI5" flow/ci); code=$?
show "$out"
check "a head whose tests failed is not merged (exit 0)" 0 $code
contains "$out" "not green: the test job failed" "the probe's own reason reaches the log"
contains "$out" "not merging — the head is not green" "and the job says what it declined"
if [ "$(git -C "$T/origin.git" rev-parse main)" = "$MAIN_BEFORE_CI" ]; then ok "the destination did not move"; else fail "a red head was merged"; fi

out=$(ci_probe_run 2 'cannot tell: nothing has run for this commit' "$CI5" flow/ci); code=$?
show "$out"
check "a head nobody has tested is not merged either (exit 0)" 0 $code
contains "$out" "nothing proves the head green" "and the answer is not treated as a pass"

out=$(ci_probe_run 1 'not green' "$CI5" --require flow/ci); code=$?
check "--require turns the decline into a red run" 1 $code

out=$(ci_probe_run 0 'green: everything passed' "$CI5" --expect-head "$MAIN_BEFORE_CI" flow/ci); code=$?
show "$out"
check "the green answer does not license a different commit" 0 $code
contains "$out" "nothing tested to merge" "the job says which two commits disagree"
if [ "$(git -C "$T/origin.git" rev-parse main)" = "$MAIN_BEFORE_CI" ]; then ok "and nothing moved"; else fail "a head CI never tested was merged"; fi

: > "$T/probe-calls"
out=$(ci_probe_run 0 'green: 12 checks, all finished' "$CI5" --expect-head "$DECL_CI" flow/ci); code=$?
show "$out"
check "green, and this is the commit CI finished with: merged" 0 $code
contains "$out" "pushed to main" "the handoff finishes"
check "the probe was asked about the declared head" "$DECL_CI" "$(tail -1 "$T/probe-calls")"

step "a changeset under a branch name the trigger used to exclude"

# The job has never filtered on a branch name, and the fixture's own branches prove it by accident rather than
# by assertion. This says the fact out loud, on the name that was left out of the trigger's allowlist and could
# not land: approved, declared, green, merged.
declare_changeset docs/notes notes.md 'the plan notes' || { echo "fixture: docs/notes" >&2; exit 1; }
git -C "$T/work" checkout -q docs/notes && wing change integrate >/dev/null || { echo "fixture: declare docs/notes" >&2; exit 1; }
git -C "$T/work" push -q origin docs/notes >/dev/null 2>&1
BEFORE=$(git -C "$T/origin.git" rev-parse main)
CI6=$(clone ci6) || { echo "cannot clone for ci6" >&2; exit 1; }
out=$(cigr "$CI6" docs/notes); code=$?
show "$out"
check "the run succeeds on a docs/ branch" 0 $code
contains "$out" "notes: declared by" "it read the declaration on that branch"
contains "$out" "merged as" "and it merged it"
if [ "$(git -C "$T/origin.git" rev-parse main)" = "$BEFORE" ]; then
  fail "a branch outside feat/ was merged without the destination moving"
else
  ok "and the destination moved for a branch outside feat/"
fi

step "the dry run"

declare_changeset flow/dry src/d.ts 'export const d = 5' || { echo "fixture: flow/dry" >&2; exit 1; }
git -C "$T/work" checkout -q flow/dry && wing change integrate >/dev/null || { echo "fixture: declare flow/dry" >&2; exit 1; }
git -C "$T/work" push -q origin flow/dry >/dev/null 2>&1
BEFORE=$(git -C "$T/origin.git" rev-parse main)
CI3=$(clone ci3) || { echo "cannot clone for ci3" >&2; exit 1; }
out=$(cigr "$CI3" --dry-run flow/dry); code=$?
show "$out"
check "the dry run succeeds" 0 $code
contains "$out" "dry run: would merge" "it says what it would have done"
contains "$out" "into main" "and names the destination"
if [ "$(git -C "$T/origin.git" rev-parse main)" = "$BEFORE" ]; then ok "and it merged nothing"; else fail "the dry run merged"; fi
if [ "$(git -C "$T/origin.git" for-each-ref --format='%(refname)' | grep -cv '^refs/heads/')" = 0 ]; then
  ok "and it left nothing behind on the remote but branches"
else
  fail "the dry run wrote a ref"
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
contains "$out" "nothing merged" "and says what it did not do"
if [ "$(git -C "$T/origin.git" rev-parse main)" = "$BEFORE" ]; then ok "the destination is untouched"; else fail "a conflicted merge reached the destination"; fi
if git -C "$T/origin.git" merge-base --is-ancestor refs/heads/flow/clash main; then
  fail "the branch the job could not merge is in the destination anyway"
else
  ok "and the branch it could not merge is still outside the destination"
fi
if [ -z "$(git -C "$CI4" status --porcelain)" ]; then ok "the clone was left clean"; else fail "the clone was left dirty"; fi
if [ -f "$CI4/.git/MERGE_HEAD" ]; then fail "a merge was left half-done"; else ok "and no merge was left half-done"; fi

step "the two workflow files still name each other"

# Renaming either workflow is invisible until a merge fails to happen: `workflow_run` matches the other
# file's `name:` string, and nothing but a push runs the pair. These are text checks of two conventions
# rather than a YAML parse — there is no YAML parser to depend on in a gate — but they fail the build here
# instead of leaving a merge job that quietly never fires.
check "the test workflow declares the name the merge job triggers on" 1 "$(grep -c '^name: CI$' "$ROOT/.github/workflows/ci.yml")"
check "and the merge job names it back" 1 "$(grep -c "^[[:space:]]*- 'CI'\$" "$ROOT/.github/workflows/git-pair-integrate.yml")"
check "CI runs on branch pushes, whose checks are the ones on the head the probe reads" 1 "$(grep -c '^    branches:$' "$ROOT/.github/workflows/ci.yml")"
check "and the merge job has no push trigger that would make it certify its own check run" 0 "$(grep -c '^  push:$' "$ROOT/.github/workflows/git-pair-integrate.yml")"

# The check above and the step before it cover two halves of one rule, and only this one can see the trigger.
# A branch filter in `workflow_run:` leaves every job green and every fixture passing while it stops a whole
# family of branches from ever landing, because the trigger is evaluated before anything git-pair says. The
# block is taken between the trigger and the next one, minus its comments, so the prose can explain the rule
# without tripping the assertion.
WRT=$(awk '/^  workflow_run:/{f=1;next} f&&/^  [a-z_]+:/{f=0} f' "$ROOT/.github/workflows/git-pair-integrate.yml" |
  grep -v '^[[:space:]]*#')
check "the trigger carries no branch allowlist to exclude a changeset from landing" 0 "$(printf '%s\n' "$WRT" | grep -Ec '^[[:space:]]*branches:')"
check "and no denylist taking its place" 0 "$(printf '%s\n' "$WRT" | grep -Ec '^[[:space:]]*branches-ignore:')"

step "cost: what a stacked branch costs the gate"

# `check --json` is the gate the CI job runs, and a stacked branch is where its reads multiply: each level below
# this one has to be read to know what the work sits on and whether any of it has landed. The two assertions are
# the shape of that cost, not a performance claim. The first says one hop costs what was measured; the second
# says an extra hop costs the same as the one before it, which is the property worth defending - a change that
# re-reads the whole chain per level is quadratic and shows up here rather than in a slow CI job nobody
# investigates.
#
# The first hop costs more than the ones after it, and that is the honest shape rather than an accident to fix:
# below one level there is nothing to look for, while at two the code has a parent to find, so the reads that
# locate a changeset on another branch (the destination probe, its .landed tombstone, the branch probe) run for
# the first time. Recorded 2026-08 with `check --json`: 25 invocations at one level, 43 at two, 52 at three, 61
# at four. Each hop past the second costs nine - one tree read of that level, two landedness probes, a branch
# probe, and the log that reads the level's markers.
#
# The count comes from a shim placed ahead of git on PATH, because the number that matters is what the binary
# asked for, not what the code appears to do. A change that moves either number has to say why in this paragraph.

ST=$T/stack
git init -q -b main --bare "$ST.git"
git clone -q "$ST.git" "$ST" >/dev/null 2>&1 || { fail "cost fixture: cannot clone"; }
git -C "$ST" config user.email cost@example.com
git -C "$ST" config user.name Cost
echo '# cost fixture' > "$ST/README.md"
git -C "$ST" add -A && git -C "$ST" commit -qm initial >/dev/null 2>&1
git -C "$ST" push -q -u origin main >/dev/null 2>&1
for level in one two three four; do
  case $level in
    one) from=main ;;
    two) from=stack-one ;;
    three) from=stack-two ;;
    four) from=stack-three ;;
  esac
  git -C "$ST" checkout -q -b "stack-$level" "$from" || fail "cost fixture: branch stack-$level"
  ( cd "$ST" && "$G" init --base "$from" --set-base >/dev/null 2>&1 )
  printf '%s work\n' "$level" > "$ST/$level.txt"
  git -C "$ST" add -A && git -C "$ST" commit -qm "$level work" >/dev/null 2>&1
  ( cd "$ST" && "$G" change ready >/dev/null 2>&1 )
done
git -C "$ST" push -q origin stack-one stack-two stack-three stack-four >/dev/null 2>&1

SHIM=$T/git-shim
mkdir -p "$SHIM"
{ printf '#!/usr/bin/env bash\n'
  printf 'printf "%%s\\n" "$*" >> "${GIT_CALL_LOG:-/dev/null}"\n'
  printf 'exec "%s" "$@"\n' "$(command -v git)"; } > "$SHIM/git"
chmod +x "$SHIM/git"

count_calls() { # count_calls <branch> - the git invocations `check --json` makes on it
  # GIT_PAIR_NO_CACHE is what makes the numbers below mean something. The four measurements share one clone,
  # so a run on stack-two can answer from the derived facts stack-one's run left on disk, and the count stops
  # being a property of the formulation. It showed up as this assertion flipping between runs on the same
  # commit — 16/24/28/32 once, 16/24/29/32 and 16/25/28/31 others — while the same measurement without the
  # caches reproduced 25/43/52/61 every time. A bound on how a chain is walked cannot depend on whether
  # somebody walked it a minute ago. The rest of this suite runs the binary with caching on, as CI does; this
  # one measurement is the exception, for the same reason `gittest.SpawnShim` sets the same variable.
  local log=$T/calls-$1
  git -C "$ST" checkout -q "$1" || return 1
  : > "$log"
  ( cd "$ST" && GIT_PAIR_NO_CACHE=1 GIT_CALL_LOG=$log PATH="$SHIM:$PATH" "$G" check --json >/dev/null 2>&1 )
  wc -l < "$log" | tr -d ' '
}
ONE=$(count_calls stack-one)
TWO=$(count_calls stack-two)
THREE=$(count_calls stack-three)
FOUR=$(count_calls stack-four)
ok "measured: $ONE invocations at one level, $TWO at two, $THREE at three, $FOUR at four"
STACK_CEILING=50 # the recorded cost of a two-level stack, plus five invocations of slack
check "a child on a parent stays inside the recorded cost of the stack ($TWO measured, $STACK_CEILING allowed)" 0 \
  "$([ "$TWO" -le "$STACK_CEILING" ] && echo 0 || echo 1)"
check "each hop past the second costs what the hop before it cost, so the chain is read level by level" 0 \
  "$([ "$((FOUR - THREE))" -eq "$((THREE - TWO))" ] && echo 0 || echo 1)"

# And the guard that keeps the paragraph above honest, rather than a comment someone deletes under pressure.
# Measuring the same branch a second time has to cost the same. If the caches were allowed to answer, the
# repeat run would come back cheaper — cheaper by a dependent bound, and cheaper in a way that depends on what
# somebody ran a minute ago. That is the whole failure this assertion is here to prevent, arrived at by
# accident: it showed up as the bound above flipping between CI runs on one commit.
TWO_AGAIN=$(count_calls stack-two)
check "the measurement is a property of the formulation, not of what ran before it ($TWO then $TWO_AGAIN)" 0 \
  "$([ "$TWO_AGAIN" -eq "$TWO" ] && echo 0 || echo 1)"

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
