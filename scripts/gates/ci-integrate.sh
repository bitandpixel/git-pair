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
# about the declared commit; a merge that pushed asks the destination to test its new tip, by name and with
# the merge's own sha, and an ask that cannot be made or is refused is said out loud; a dry run writes
# nothing; a merge that conflicts is aborted and reported red; a
# changeset whose branch sits outside feat/ merges like any other, and the trigger is checked for the branch
# filter that would have stopped it; a candidate is tested against the tree the destination would become, and
# one that fails is an error that pushes nothing and puts the local destination back; and the two workflow
# files still say what this shape needs — the merge triggered by a push with no poll and no other workflow to
# wait for, and the ask that follows a merge pointed at a workflow that cannot merge.
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

step "the destination has to be asked to test what the merge produced"

# The merge is pushed with the runner's own token, and GitHub creates no workflow runs for events a token
# caused, so the destination's own push trigger cannot fire for a merge this job performed. Without a request
# the destination keeps a tip no job ever built: the head the probe cleared is a different tree whenever the
# destination moved in between, which is what two landings in one evening look like. The request is a hook for
# the same reason the probe is one — what is under test is the job's decision to ask, and with which
# arguments, not the forge. The stub records every call, so both the ask and its absence are assertions.
cat > "$T/request" <<'EOS'
#!/usr/bin/env bash
printf '%s %s\n' "${1:-}" "${2:-}" >> "$REQUEST_CALLS"
printf '%s\n' "${REQUEST_SAYS:-stub: asked}"
exit "${REQUEST_RC:-0}"
EOS
chmod +x "$T/request"

ci_request_run() { # ci_request_run <rc> <says> <dir> [job args...]
  local rc=$1 says=$2 dir=$3; shift 3
  ( cd "$dir" && GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null GIT_PAIR_BIN="$G" \
      REQUEST_RC="$rc" REQUEST_SAYS="$says" REQUEST_CALLS="$T/request-calls" \
      bash "$CI" --ci-request "bash $T/request" "$@" 2>&1 )
}

for b in flow/proven-a flow/proven-b flow/proven-c flow/proven-d flow/proven-e; do
  declare_changeset "$b" "src/proven-${b##*/}.ts" "export const proven_${b##*/} = 1" || { echo "fixture: $b" >&2; exit 1; }
  git -C "$T/work" checkout -q "$b" && wing change integrate >/dev/null || { echo "fixture: declare $b" >&2; exit 1; }
done
git -C "$T/work" push -q origin flow/proven-a flow/proven-b flow/proven-c flow/proven-d flow/proven-e >/dev/null 2>&1 || {
  echo "fixture: push the proven branches" >&2; exit 1; }
: > "$T/request-calls"

PROV1=$(clone prov1) || { echo "cannot clone for prov1" >&2; exit 1; }
out=$(ci_request_run 0 'requested: ci.yml for main' "$PROV1" flow/proven-a); code=$?
show "$out"
check "a merge whose ask succeeded is still a green run (exit 0)" 0 $code
contains "$out" "pushed to main" "the landing happened"
contains "$out" "requested: ci.yml for main" "and the hook's own line reaches the log"
MERGE_A=$(git -C "$T/origin.git" rev-parse main)
check "the destination was asked by name" "main" "$(awk '{print $1}' "$T/request-calls" | tail -1)"
check "and asked about the merge commit rather than the head it merged" "$MERGE_A" "$(awk '{print $2}' "$T/request-calls" | tail -1)"

# The same changeset again: the destination already holds it, nothing was pushed, so there is no new tree to
# test. An ask here buys a run that proves a combination this job did not produce.
n0=$(wc -l < "$T/request-calls")
out=$(ci_request_run 0 'requested' "$PROV1" flow/proven-a); code=$?
show "$out"
check "an already-landed changeset costs nothing (exit 0)" 0 $code
contains "$out" "nothing to merge here" "and says there is nothing to do"
check "and asks for no run, because nothing was pushed" "$n0" "$(wc -l < "$T/request-calls")"

PROV2=$(clone prov2) || { echo "cannot clone for prov2" >&2; exit 1; }
out=$(ci_request_run 2 'cannot tell: no forge named' "$PROV2" flow/proven-b); code=$?
show "$out"
check "a destination nobody could be asked about is still a green merge (exit 0)" 0 $code
contains "$out" "cannot tell: no forge named" "the hook's reason is in the log"
contains "$out" "its tip is unproven" "and the job says so in plain words"

PROV3=$(clone prov3) || { echo "cannot clone for prov3" >&2; exit 1; }
out=$(ci_request_run 1 'refused: the token lacks actions: write' "$PROV3" flow/proven-c); code=$?
show "$out"
check "a refused ask does not undo a landing that already happened (exit 0)" 0 $code
contains "$out" "refused: the token lacks actions: write" "the reason reaches the log"
contains "$out" "its tip is unproven" "and the gap is stated rather than swallowed"

PROV4=$(clone prov4) || { echo "cannot clone for prov4" >&2; exit 1; }
out=$(ci_request_run 1 'refused' "$PROV4" --require flow/proven-d); code=$?
show "$out"
check "--require turns a refused ask into a red run" 1 $code

PROV5=$(clone prov5) || { echo "cannot clone for prov5" >&2; exit 1; }
n0=$(wc -l < "$T/request-calls")
out=$(ci_request_run 0 'requested' "$PROV5" --dry-run flow/proven-e); code=$?
show "$out"
check "a dry run writes nothing outside the clone (exit 0)" 0 $code
check "and asks for no run it did not make" "$n0" "$(wc -l < "$T/request-calls")"

# Three static claims, in the style of the workflow-name checks below. Each is a way the ask could stop
# existing while every run stayed green: the scope withdrawn while the ask was still made, the helper left
# unexecutable so the job's own `-x` test skips it, or the default deleted from the script.
#
# The first claim used to read `the merge job is granted the scope to ask for a run`, and asserted
# `actions: write` was present. This file no longer asks — it tests the candidate before it pushes, so there
# is no tip left untested for somebody else to check — so asserting the scope is there would force a wide
# token the job does not use. What still has to be true is the pairing: a file that asks for a run has to
# carry the scope that makes the ask, and one that does not ask has to leave the scope out. Either half
# failing is the same bug seen from the other side, so both are checked here.
ASKS=$(grep -v '^[[:space:]]*#' "$ROOT/.github/workflows/git-pair-integrate.yml" | grep -c -- '--ci-request')
SCOPED=$(grep -v '^[[:space:]]*#' "$ROOT/.github/workflows/git-pair-integrate.yml" | grep -c '^  actions: write$')
check "the merge job asks for a run only if it holds the scope to make the ask" 0 \
  "$([ "$ASKS" -gt 0 ] && [ "$SCOPED" -eq 0 ] && echo 1 || echo 0)"
check "and holds that scope only if it asks, so no token is wider than the job" 0 \
  "$([ "$ASKS" -eq 0 ] && [ "$SCOPED" -gt 0 ] && echo 1 || echo 0)"
if [ -x "$ROOT/scripts/ci/gh-request-ci.sh" ]; then
  ok "the request helper is executable, which is how the job finds it by default"
else
  fail "scripts/ci/gh-request-ci.sh is not executable — the job would find no hook and ask for nothing"
fi
check "and the job names it as the default" 1 "$(grep -c 'hook=("\$HERE/gh-request-ci.sh")' "$ROOT/scripts/ci/git-pair-integrate.sh")"

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

step "the candidate has to pass the tests it is merged with"

# `--verify-cmd` is what makes a landing conditional on this repository's own suite, so it is the one part of
# the job that has no second chance anywhere else: a repository that does not run CI on every push has no
# other run whose red the author would see. Three things are proven here, in one run each.
#
#   1. without the flag the job merges on the gate alone, as it always did,
#   2. with it, the command is asked about the tree the destination would become — the merge commit, not the
#      branch head the branch's own runs tested,
#   3. and a candidate that fails is an error rather than a skip: nothing is pushed, the local destination is
#      put back so the next changeset in the same run is not merged onto a refused tree, and the run says so
#      with exit 1.
#
# Trunk moves first, from the same starting point the branches were cut at. That move is what makes (2)
# checkable: a command run at the branch head cannot see `moved.txt`, and a command run at the merge commit
# can. The failing candidate is picked out by a file only its own branch has, so one run carries both verdicts.

git -C "$T/work" checkout -q main || { fail "candidate fixture: checkout main"; }
printf 'trunk moved after the branches were cut\n' > "$T/work/moved.txt"
git -C "$T/work" add -A && git -C "$T/work" commit -qm "main: moved after the cut" >/dev/null 2>&1
git -C "$T/work" push -q origin main >/dev/null 2>&1 || fail "candidate fixture: move main"
for b in flow/plain flow/failing flow/verified; do
  case $b in
    flow/plain)    f=src/p.ts;   v='export const p = 1' ;;
    flow/failing)  f=src/nope.ts; v='export const nope = 2' ;;
    flow/verified) f=src/ok.ts;  v='export const ok = 3' ;;
  esac
  declare_changeset "$b" "$f" "$v" "$MAIN0" || { fail "candidate fixture: $b"; }
done
for b in flow/plain flow/failing flow/verified; do
  git -C "$T/work" checkout -q "$b" && wing change integrate >/dev/null 2>&1 || fail "candidate fixture: declare $b"
done
git -C "$T/work" push -q origin flow/plain flow/failing flow/verified >/dev/null 2>&1 || fail "candidate fixture: push"

CIV=$(clone civ) || { fail "cannot clone for the candidate cases"; }
out=$(cigr "$CIV" flow/plain); code=$?
show "$out"
check "with no --verify-cmd the job merges on the gate alone" 0 $code
contains "$out" "pushed to main" "and it lands the work"
no_contains "$out" "verifying the candidate" "and no candidate was tested, because nobody asked for one"

# One command, two verdicts: it fails only for the tree that carries the failing changeset's own file, so the
# run below shows both halves of the behaviour at once — a refused candidate and a landed one.
VCMD='if [ -f src/nope.ts ]; then echo "candidate: the suite says no"; exit 1; fi
      printf "candidate: has %s\n" "$([ -f moved.txt ] && echo moved.txt || echo "no moved.txt")"
      git rev-parse -q --verify HEAD^2 >/dev/null && echo "candidate: two parents" || echo "candidate: one parent"'
out=$(cigr "$CIV" --verify-cmd "$VCMD" flow/failing flow/verified); code=$?
show "$out"
check "a candidate that fails its tests makes the run red, flag or no flag" 1 $code
contains "$out" "flow-failing: the merge candidate failed verification" "it says which candidate it refused"
contains "$out" "candidate: the suite says no" "and the suite's own words reach the log"
contains "$out" "flow-verified: the candidate passed verification" "the other candidate was tested too"
contains "$out" "candidate: has moved.txt" "and it was tested against the tree that has trunk's newest move"
contains "$out" "candidate: two parents" "on the merge commit itself, not on the branch head"
contains "$out" "flow-verified: pushed to main" "and only that one landed"

if git -C "$T/origin.git" cat-file -e main:changesets/flow-verified/CHANGESET.yaml 2>/dev/null; then
  ok "the destination carries the candidate that passed"
else
  fail "the candidate that passed its tests did not reach the destination"
fi
for path in changesets/flow-failing/CHANGESET.yaml src/nope.ts; do
  if git -C "$T/origin.git" cat-file -e "main:$path" 2>/dev/null; then
    fail "a candidate that failed verification put $path on the destination"
  else
    ok "and nothing of the refused candidate reached the destination — not even $path"
  fi
done
# The reset, checked where it matters: the job keeps one local destination for the whole run, so a tree this
# run refused has to be gone before the next merge is made. A merge on top of it would land the refused work
# under the passed work's own merge commit, which is the quiet failure this assertion is here for.
DEST=$(git -C "$CIV" rev-parse -q --verify git-pair-ci-destination)
TIP=$(git -C "$T/origin.git" rev-parse main)
if [ "$DEST" = "$TIP" ]; then
  ok "the local destination was put back to what the remote holds"
else
  fail "the local destination still holds a tree the run refused (${DEST:0:7} against remote ${TIP:0:7})"
fi
if git -C "$CIV" cat-file -e "git-pair-ci-destination:src/nope.ts" 2>/dev/null; then
  fail "the refused candidate is still in the local destination, where the next merge would build on it"
else
  ok "and the refused tree is out of it, so the next changeset is merged against the real destination"
fi

step "the replay cannot reach the forge"

# The job runs the suite, the suite runs the driver, and the driver — given a forge to name — asks it for a
# run. Run inside the merge job, where the token can make that ask succeed, one pass of `mise run gates`
# produced six real `gh workflow run ci.yml --repo bitandpixel/git-pair --ref main` calls against this
# repository, each one caused by a merge into a scratch repository that has nothing to do with it. The fix is
# `scripts/ci/clean-env.sh`, and both halves are asserted here: that the ask really does happen when the
# environment is left as the job has it, without which the assertion below would prove nothing; and that it
# does not happen through the scrubber.
GHSTUB=$T/ghstub
mkdir -p "$GHSTUB"
printf '#!/bin/sh\necho "gh $*" >> "%s/ghcalls"\nexit 0\n' "$T" > "$GHSTUB/gh"
chmod +x "$GHSTUB/gh"
: > "$T/ghcalls"

forge_run() { # forge_run <dir> <branch> [scrub] — the job, with a forge named and a recording `gh` on PATH
  local dir=$1 branch=$2 mode=${3:-plain}
  (
    cd "$dir" || exit 1
    export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null GIT_PAIR_BIN="$G"
    export GITHUB_REPOSITORY=bitandpixel/git-pair GITHUB_TOKEN=forge-token PATH="$GHSTUB:$PATH"
    if [ "$mode" = scrub ]; then
      exec bash "$ROOT/scripts/ci/clean-env.sh" bash "$CI" "$branch" 2>&1
    fi
    exec bash "$CI" "$branch" 2>&1
  )
}
count_gh() { wc -l < "$T/ghcalls" | tr -d ' '; }

declare_changeset flow/forge-direct src/i.ts 'export const i = 9' || { echo "fixture: flow/forge-direct" >&2; exit 1; }
git -C "$T/work" checkout -q flow/forge-direct && wing change integrate >/dev/null || { echo "fixture: declare flow/forge-direct" >&2; exit 1; }
git -C "$T/work" push -q origin flow/forge-direct >/dev/null 2>&1
OUT=$(forge_run "$(clone fd)" flow/forge-direct); show "$OUT"
contains "$OUT" "requested: bitandpixel/git-pair will run ci.yml" \
  "left as the job has it, the environment names a forge, and the default hook asks it for a run"
if [ "$(count_gh)" -ge 1 ]; then
  ok "and the ask was an outbound call, not a line of text"
else
  fail "the ask never reached gh, which makes the assertion below meaningless"
fi

declare_changeset flow/forge-scrubbed src/j.ts 'export const j = 10' || { echo "fixture: flow/forge-scrubbed" >&2; exit 1; }
git -C "$T/work" checkout -q flow/forge-scrubbed && wing change integrate >/dev/null || { echo "fixture: declare flow/forge-scrubbed" >&2; exit 1; }
git -C "$T/work" push -q origin flow/forge-scrubbed >/dev/null 2>&1
BEFORE=$(count_gh)
BEFORE_TIP=$(git -C "$T/origin.git" rev-parse main)
OUT=$(forge_run "$(clone fs)" flow/forge-scrubbed scrub); show "$OUT"
contains "$OUT" "no --ci-request given, no forge named" \
  "through the scrubber the driver cannot name a forge, so it says so instead of asking"
if [ "$(count_gh)" = "$BEFORE" ]; then
  ok "and nothing was dispatched: a test cannot ask the repository it is testing for a run"
else
  fail "the scrubber let $(( $(count_gh) - BEFORE )) call(s) through"
fi
check "and the merge still happened, so the scrubber removed the ask rather than the job" 1 \
  "$( [ "$(git -C "$T/origin.git" rev-parse main)" != "$BEFORE_TIP" ] && echo 1 || echo 0 )"

step "the workflow files say what this shape needs"

# Text checks of conventions, not a YAML parse — there is no YAML parser to depend on in a gate. Each one
# here fails a build for something that would otherwise show up as a merge that quietly never happened, a
# minute spent on a run that could not have merged, or a token wider than the job uses. Comments are stripped
# from the blocks being matched, so the file's prose can name a rule without tripping the assertion of it.
INTEGRATE=$ROOT/.github/workflows/git-pair-integrate.yml
CIFIKE=$ROOT/.github/workflows/ci.yml

check "the merge workflow is still there to be triggered" 1 "$(grep -c '^name: git-pair integrate$' "$INTEGRATE")"
check "and the test workflow is still there to be asked for" 1 "$(grep -c '^name: CI$' "$CIFIKE")"

# The triggers this shape runs on, and the two it stopped running on. A schedule is 96 jobs a day whether or
# not anything is waiting; a `workflow_run` merges a head some other workflow certified rather than the tree
# the destination would become. Both are gone, and neither comes back without a decision.
check "the merge job is triggered by a push, which is how a declaration arrives" 1 "$(grep -c '^  push:$' "$INTEGRATE")"
check "it has no scheduled poll" 0 "$(grep -Ec '^[[:space:]]*schedule:' "$INTEGRATE")"
check "and no run of another workflow to wait for" 0 "$(grep -Ec '^[[:space:]]*workflow_run:' "$INTEGRATE")"

# A branch filter on the trigger is evaluated before anything git-pair says, so it can stop a whole family of
# changeset branches from ever landing while every job stays green and every fixture passes. The push block is
# read between its trigger and the next one, minus comments, for the same reason the old `workflow_run:`
# block was: the prose may explain the rule without tripping the assertion.
PUSH=$(awk '/^  push:/{f=1;next} f&&/^  [a-z_]+:/{f=0} f' "$INTEGRATE" | grep -v '^[[:space:]]*#')
check "the push trigger carries no denylist to exclude a changeset from landing" 0 "$(printf '%s\n' "$PUSH" | grep -Ec '^[[:space:]]*(branches-ignore|paths-ignore|tags-ignore):')"
check "no allowlist to do the same job" 0 "$(printf '%s\n' "$PUSH" | grep -Ec '^[[:space:]]*(branches|paths|tags):[^-]')"
check "and it takes every branch, so a changeset named outside the usual prefixes still lands" 1 "$(printf '%s\n' "$PUSH" | grep -Ec "^[[:space:]]*-[[:space:]]*'\*\*'[[:space:]]*$")"

# The condition that decides whether the merge job runs at all. It lives in the job's `if:` and not in a step
# because a job that starts is billed: a guard step that exits early costs the minute a guard `if:` saves. Two
# properties are worth an assertion each — that it cannot admit a trigger with no write token, and that it
# matches the trailer the declaration writes rather than its subject line.
IF=$(awk '/^    if: >-/{f=1;next} f&&/^    runs-on:/{f=0} f' "$INTEGRATE" | grep -v '^[[:space:]]*#')
check "the merge job's condition is an allowlist that admits no pull request" 0 "$(printf '%s\n' "$IF" | grep -c pull_request)"
check "it reads the declaration as a trailer, so it matches where git-pair actually writes it" 1 \
  "$(printf '%s\n' "$IF" | grep -c "contains(github.event.head_commit.message, 'Review-State: integrating')")"
check "it excludes the destination branch, which cannot carry a merge of itself" 1 \
  "$(printf '%s\n' "$IF" | grep -c "github.ref_name != (vars.GIT_PAIR_BASE || 'main')")"
check "and it leaves the manual trigger unfiltered, because that is the recovery path" 1 \
  "$(printf '%s\n' "$IF" | grep -c "github.event_name == 'workflow_dispatch'")"

# `contents: write` is the merge; the three scopes that used to sit beside it belonged to the probe and to the
# post-merge request, and a token asks for what a job uses. The permissions block is read whole, minus
# comments, so this file can explain what was dropped without the assertion reading the explanation as a ask.
PERM=$(awk '/^permissions:/{f=1;next} f&&/^[a-z]/{f=0} f' "$INTEGRATE" | grep -v '^[[:space:]]*#')
check "the job asks to write the destination" 1 "$(printf '%s\n' "$PERM" | grep -c 'contents: write')"
check "and to make the one request that follows" 1 "$(printf '%s\n' "$PERM" | grep -c 'actions: write')"
check "and holds nothing the probe would have needed" 0 "$(printf '%s\n' "$PERM" | grep -Ec '(checks|statuses):')"

# What the job runs. `--verify-cmd` is the whole point of the shape — the merge is conditional on the suite
# running against the candidate — and `--require-ci` must not come back into this file's own commands, because
# a job that waits for a head-check while testing the candidate itself has two answers and no rule for which
# one merges. These are read from the file with its comments away: the prose above is allowed to name a flag
# it explains, the steps are not.
INTEGRATE_CODE=$(grep -v '^[[:space:]]*#' "$INTEGRATE")
check "the job makes the merge conditional on the suite" 1 "$( [ "$(printf '%s\n' "$INTEGRATE_CODE" | grep -c -- '--verify-cmd')" -ge 1 ] && echo 1 || echo 0 )"
check "and does not also gate on somebody else's checks" 0 "$(printf '%s\n' "$INTEGRATE_CODE" | grep -Ec -- '--require-ci|--ci-probe')"

# The candidate is run by the job that merges it, so it runs in the job's environment — and that environment
# describes where the code is being run, which is information the suite was never written against. Two failures
# came from it, both invisible off a runner: with `GIT_PAIR_DEFAULT_BRANCH` exported the product names where its
# destination came from and the verdict tests fail on a fine tree; and with `GITHUB_REPOSITORY` exported the
# driver's replay asked the real repository for runs of `ci.yml` caused by merges into scratch repositories.
# `scripts/ci/clean-env.sh` is the seam, and the step asserting it here is what stops a later edit from
# quietly dropping it. Note which side keeps the variables: the driver still has them, because the driver is
# the part that merges and asks.
CI_CODE=$(grep -v '^[[:space:]]*#' "$ROOT/.github/workflows/ci.yml")
check "and the candidate is run with the job's environment removed" 1 \
  "$( [ "$(printf '%s\n' "$INTEGRATE_CODE" | grep -c 'clean-env.sh mise run gates')" -ge 1 ] && echo 1 || echo 0 )"
check "and so is the ladder on the pull-request path, so there is one definition of green" 1 \
  "$( [ "$(printf '%s\n' "$CI_CODE" | grep -c 'clean-env.sh mise run gates')" -ge 1 ] && echo 1 || echo 0 )"
check "the scrubber is there and executable, since a wrapper nobody can run is a silent no-op" 1 \
  "$( [ -x "$ROOT/scripts/ci/clean-env.sh" ] && echo 1 || echo 0 )"

# The request that follows a landing is house policy, not decoration. The destination's new tip is the tree
# this job just tested, so the run adds no answer — but a trunk with no run of its own has nothing to go red
# and nobody to notify when the merge is where the trouble was. Three things have to hold together, and each
# has failed alone before: the ask named in the file, the scope that makes a request, and a token for the tool
# that makes it. An ask without a token answers "cannot tell", which the driver reports and then survives —
# the quiet version of a paper trail that was never printed.
check "the merge reads the whole history, so an approval and a landing can be found" 1 "$( [ "$(printf '%s\n' "$INTEGRATE_CODE" | grep -c 'fetch-depth: 0')" -ge 1 ] && echo 1 || echo 0 )"
check "two jobs for one declaration would race, so a merge is never cancelled midway" 1 "$( [ "$(printf '%s\n' "$INTEGRATE_CODE" | grep -c 'cancel-in-progress: false')" -ge 1 ] && echo 1 || echo 0 )"
check "and the queue report runs after a failure, when it is most wanted" 1 "$( [ "$(printf '%s\n' "$INTEGRATE_CODE" | grep -c 'if: always()')" -ge 1 ] && echo 1 || echo 0 )"
check "the merge job asks the destination to test what it landed" 1 "$( [ "$(printf '%s\n' "$INTEGRATE_CODE" | grep -c -- '--ci-request')" -ge 1 ] && echo 1 || echo 0 )"
check "and names the helper rather than leaning on the driver's default" 1 \
  "$(printf '%s\n' "$INTEGRATE_CODE" | grep -c -- '--ci-request scripts/ci/gh-request-ci.sh')"
check "and the tool that asks is given a token to ask with" 1 \
  "$( [ "$(printf '%s\n' "$INTEGRATE_CODE" | grep -c 'GITHUB_TOKEN')" -ge 1 ] && echo 1 || echo 0 )"

# The pair, in the direction that still matters. After a merge, trunk's own push trigger cannot fire — the
# push came from a token, and GitHub makes no runs for what a token caused — so `scripts/ci/gh-request-ci.sh`
# asks for a workflow by name. That name has to be a file that exists, and a file that cannot merge: asking
# for a test must not be able to cause a landing. Again the file minus its prose, which names the merge file
# in order to say the two are a pair.
REQUESTED=$(sed -n 's/^workflow=\${GIT_PAIR_CI_REQUEST_WORKFLOW:-\(.*\)}/\1/p' "$ROOT/scripts/ci/gh-request-ci.sh")
REQUESTED_CODE=$(grep -v '^[[:space:]]*#' "$ROOT/.github/workflows/$REQUESTED")
check "the request hook still names a workflow that exists" 1 "$( [ -f "$ROOT/.github/workflows/$REQUESTED" ] && echo 1 || echo 0 )"
check "and it is the test workflow, not the one that merges" 0 "$(printf '%s\n' "$REQUESTED_CODE" | grep -c '^name: git-pair integrate$')"
check "so asking for a test cannot cause a merge" 0 "$(printf '%s\n' "$REQUESTED_CODE" | grep -Ec -- '--verify-cmd|git-pair-integrate.sh')"

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
