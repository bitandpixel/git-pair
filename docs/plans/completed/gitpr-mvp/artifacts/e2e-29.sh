#!/usr/bin/env bash
# End-to-end replay of the PRD §29 success workflow against a scratch repo.
# Usage: bash docs/plans/completed/gitpr-mvp/artifacts/e2e-29.sh [/path/to/git-pair]
#        (default: ~/.local/bin/git-pair — run `mise run build` first)
set -uo pipefail
G=${1:-$HOME/.local/bin/git-pair}
T=$(mktemp -d /tmp/git-pair-e2e.XXXXXX)
trap 'rm -rf "$T"' EXIT
cd "$T" || exit 1
git init -q -b main .
git config user.email r@example.com
git config user.name Reviewer
git config commit.gpgsign false

step() { printf '\n\033[1m### %s\033[0m\n' "$1"; }
check() { # check <description> <expected-exit> <actual-exit>
  if [ "$2" = "$3" ]; then printf '  ok: %s\n' "$1"; else printf '  FAIL: %s (exit %s, want %s)\n' "$1" "$3" "$2"; FAILED=1; fi
}
FAILED=0

mkdir -p src
printf 'func createOffering() {\n\to := load()\n\treturn o\n}\n\nfunc enroll() {\n\treturn write(debit())\n}\n' > src/service.ts
echo '# demo' > README.md
git add -A && git commit -qm "initial implementation"

step "author: branch, init, implement"
git switch -qc booking-transaction
$G change init --base main; check "change init" 0 $?
$G change init --base main >/dev/null; check "change init idempotent" 0 $?
# A second init naming a different base is a conflict, not an edit: the changeset keeps the base it
# was created with until someone says --set-base. TestChangeInitBaseConflict covers this in Go;
# these two lines cover it against an installed binary, which is what the M1 row claims.
$G change init --base trunk >/dev/null 2>&1; check "change init refuses to move an existing base" 2 $?
grep -q '^base: main$' changesets/booking-transaction/CHANGESET.yaml && echo "  ok: the refused init left the base alone" || { echo "  FAIL: a refused init rewrote the base"; FAILED=1; }
[ -f changesets/booking-transaction/CHANGESET.yaml ] && echo "  ok: CHANGESET.yaml exists" || { echo "  FAIL: no CHANGESET.yaml"; FAILED=1; }
printf '# booking-transaction\n\n## Summary\n\nTransactional locking around offering creation and enrollment.\n\n## What changed\n\n- business-scoped locking\n\n## Design decisions\n\nAdmin scheduling serializes at business level.\n\n## Validation\n\n- unit tests\n\n## Known limitations\n\n## Open questions\n' > changesets/booking-transaction/ABOUT.md
git add -A && git commit -qm "implement transactional locking"

step "author: ready"
$G change ready; check "change ready" 0 $?
$G status --json | head -30
$G review queue | sed 's/^/  /'
$G review queue --json > /tmp/q.json; check "queue --json" 0 $?

step "author: withdraw the offer, then re-offer"
# Committing does not take a changeset out of the queue; a command does (PRD §12).
$G change unready; check "change unready" 0 $?
if $G review queue | grep -q booking-transaction; then
  echo "  FAIL: an unreadied changeset is still in the queue"; FAILED=1
else
  echo "  ok: the queue dropped it"
fi
$G change unready; check "unready again is a no-op, not a refusal" 0 $?
$G change ready; check "ready again after unready" 0 $?

step "reviewer: edits code, adds thread, submits --block"
printf '  // What happens if these execute concurrently?\n' >> src/service.ts
printf '  // Please use a transaction here\n' >> src/service.ts
$G review thread "concurrency tests" </dev/null; check "review thread (no tty refuses)" 2 $?
[ -f changesets/booking-transaction/concurrency-tests.md ] && echo "  ok: thread file created" || { echo "  FAIL: thread not created"; FAILED=1; }
$G review submit --block; check "review submit --block" 0 $?
$G review history; check "review history" 0 $?
git for-each-ref --format='  %(refname) -> %(objectname:short)' refs/git-pair/changesets

step "author: ready must fail on surviving review additions"
$G change ready; check "change ready blocked" 1 $?
printf '\n// unrelated fix\n' >> src/service.ts
git commit -qam "respond partially"
$G change ready; check "change ready still blocked" 1 $?

step "author: resolve one comment, leave the other"
grep -v 'What happens if these execute concurrently' src/service.ts > /tmp/s && mv /tmp/s src/service.ts
git commit -qam "synchronize transactions before the capacity read"
$G change ready; check "change ready blocked on the remaining line" 1 $?
$G change ready --allow-surviving-review-additions; check "ready with override" 0 $?

step "author: resolve the last one, ready cleanly"
grep -v 'Please use a transaction here' src/service.ts > /tmp/s && mv /tmp/s src/service.ts
git commit -qam "use the enclosing transaction"
$G change ready; check "change ready clean" 0 $?

step "reviewer: unreviewed span shows resolution"
$G diff --unreviewed --stat; check "diff --unreviewed --stat" 0 $?
$G diff --since-review=0 --stat >/dev/null; check "diff --since-review=0" 0 $?
$G diff --since-review=-1 --stat >/dev/null; check "diff --since-review=-1" 0 $?
$G diff src/service.ts --stat >/dev/null; check "diff <path>" 0 $?
$G diff --unreviewed --since-review=1 >/dev/null 2>&1; check "conflicting span flags" 2 $?
$G diff nope/nope.ts >/dev/null 2>&1; check "path outside span" 2 $?

step "reviewer: feedback then approve"
$G review submit --feedback -m "Naming only, non-blocking."; check "submit --feedback" 0 $?
$G status | sed 's/^/  /'
# The gate's deliverable is `$?`, which is the one thing the Go harness cannot assert: it calls
# the same cli.Execute that main passes to os.Exit, so only a shell sees an exit status.
$G check; check "check: feedback alone is not integration-ready" 1 $?
$G check --allow-feedback >/dev/null; check "check --allow-feedback accepts it" 0 $?
printf '\n// tighten naming\n' >> src/service.ts
git commit -qam "rename for clarity"
$G change archive; check "archive refused: the reviewed content moved" 1 $?
$G change ready >/dev/null; check "ready again after feedback" 0 $?
$G review submit --approve; check "submit --approve (empty commit)" 0 $?
git log -1 --format='  %h %s%n%b' HEAD

step "author: archive: advance the archive and check squash-safety"
HEAD_BEFORE=$(git rev-parse HEAD)
$G change archive; check "change archive" 0 $?
[ "$(git rev-parse HEAD)" = "$HEAD_BEFORE" ] && echo "  ok: archiving recorded no commit" || { echo "  FAIL: archiving created a commit"; FAILED=1; }
ARCHIVE=refs/git-pair/changesets/booking-transaction/archive
$G check >/dev/null; check "check: the archived approved head is integration-ready" 0 $?
[ "$(git rev-parse "$ARCHIVE")" = "$HEAD_BEFORE" ] && echo "  ok: the archive names the archived head" || { echo "  FAIL: the archive does not point at HEAD"; FAILED=1; }
# The gate over the two ways a passed gate stops meaning what it passed: the reviewed
# content moved, and the author withdrew the offer. Both are exit 1 with a bullet naming
# which, which is what makes the gate usable without reading the source.
printf '\n// a note added after the approval\n' >> src/service.ts
git commit -qam "note an edge case after the approval"
$G check >/dev/null 2>&1; check "check: an implementation commit after the approval fails the gate" 1 $?
$G change ready >/dev/null; check "ready again after the implementation commit" 0 $?
$G change unready >/dev/null; check "withdraw the offer" 0 $?
$G check >/dev/null 2>&1; check "check: a withdrawn changeset fails the gate" 1 $?

step "integration: record where the work landed, and what the record freezes"
# The landing goes to a branch that is not the default one, which is the case only the record can
# answer for: the changeset directory is still absent from trunk, so the tree rule reads the branch
# as live work until someone says where the change went. The landing commit shares no ancestry with
# the archived head, the way a squash leaves them.
# `--source` is the commit the archive names — §17's invariant — not whatever HEAD happens to be.
SOURCE=$(git rev-parse "$ARCHIVE")
git switch -qc release/2.x main
git checkout "$SOURCE" -- changesets/booking-transaction
git commit -qm "booking-transaction: land the reviewed work"
LANDING=$(git rev-parse HEAD)
if git merge-base --is-ancestor "$SOURCE" "$LANDING"; then
  echo "  FAIL: the fixture landing should not descend from the archived head"; FAILED=1
fi

# A clone is the shape CI gets: `refs/heads/*` mapped into `refs/remotes/*`, and nothing else. The
# durable refs were published perfectly and are still absent, so the two commands that lean on them
# have to describe the checkout rather than sentence the work. The assertion that carries the weight
# is that the answer *changes* after the fetch: that is what shows the first one was about the clone.
REMOTE=$(mktemp -d)/remote.git; CLONE=$(mktemp -d)/ci
git init -q --bare -b main "$REMOTE"
git push -q "$REMOTE" --all
git push -q "$REMOTE" '+refs/git-pair/changesets/*:refs/git-pair/changesets/*'
git clone -q "$REMOTE" "$CLONE"
git -C "$CLONE" switch -q booking-transaction
# The clone's own status has to say what it compared against: "nothing has landed" from a CI job is
# either a stale fetch or the wrong trunk, and a log that names neither cannot be triaged.
status=$(cd "$CLONE" && $G status --json)
printf '%s' "$status" | grep -q '"default_branch": "origin/main"' \
  && printf '%s' "$status" | grep -q '"default_branch_source": "origin-head"' \
  && echo "  ok: status names the trunk it compared against and how it knew" \
  || { echo "  FAIL: status did not explain its comparison: $status"; FAILED=1; }
out=$(cd "$CLONE" && $G check 2>&1); code=$?
check "check in a clone without the durable refs fails" 1 $code
printf '%s' "$out" | grep -q "this clone has no refs/git-pair/changesets/\* refs at all" \
  && echo "  ok: it says the clone is short of refs" \
  || { echo "  FAIL: it did not name the missing fetch: $out"; FAILED=1; }
printf '%s' "$out" | grep -q "the review history is not anchored" \
  && { echo "  FAIL: a fetch problem got the wording that blames the changeset"; FAILED=1; }
out=$(cd "$CLONE" && $G integration record --source "$SOURCE" --commit "$LANDING" --target origin/release/2.x 2>&1); code=$?
check "integration record without the durable refs fails" 1 $code
printf '%s' "$out" | grep -q "git fetch origin" \
  && echo "  ok: and prints the fetch to run" \
  || { echo "  FAIL: the recorder did not print the fix: $out"; FAILED=1; }
git -C "$CLONE" fetch -q origin '+refs/git-pair/changesets/*:refs/git-pair/changesets/*'
out=$(cd "$CLONE" && $G check 2>&1); code=$?
check "after the fetch, check answers about the changeset" 1 $code
printf '%s' "$out" | grep -q "took the changeset out of review" \
  && echo "  ok: the same command now reports the withdrawal, not the clone" \
  || { echo "  FAIL: the answer did not change with the fetch: $out"; FAILED=1; }
(cd "$CLONE" && $G integration record --source "$SOURCE" --commit "$LANDING" --target origin/release/2.x >/dev/null 2>&1)
check "after the fetch, the record succeeds from the clone" 0 $?
git switch -q booking-transaction
$G integration record --source "$SOURCE" --commit "$LANDING" --target release/2.x >/dev/null 2>&1
check "integration record links the archived head to a landing with no ancestry to it" 0 $?
[ "$(git rev-parse refs/git-pair/changesets/booking-transaction/integration)" = "$LANDING" ] \
  && echo "  ok: the integration ref names the landing" || { echo "  FAIL: the integration ref is wrong"; FAILED=1; }
out=$($G integration record --source "$SOURCE" --commit "$LANDING" 2>&1)
check "recording twice is refused" 1 $?
printf '%s' "$out" | grep -q "already recorded" && echo "  ok: the refusal names the record that exists" \
  || { echo "  FAIL: the second refusal explained nothing: $out"; FAILED=1; }
$G check >/dev/null 2>&1; check "check: an integrated changeset is not integration-ready again" 1 $?
$G change ready >/dev/null 2>&1; check "the archive is frozen after the record (change ready)" 1 $?
$G change archive >/dev/null 2>&1; check "the archive is frozen after the record (change archive)" 1 $?
[ "$(git rev-parse "$ARCHIVE")" = "$SOURCE" ] && echo "  ok: nothing moved the frozen archive" \
  || { echo "  FAIL: the archive moved after the record"; FAILED=1; }
git switch -q main
$G review queue 2>&1 | grep -q "booking-transaction (integrated at" \
  && echo "  ok: the queue says the changeset landed instead of listing it" \
  || { echo "  FAIL: the queue did not account for the landed changeset"; FAILED=1; }
git switch -q booking-transaction
BEFORE=$(git rev-list --count "$ARCHIVE")
git switch -q main
git branch -D booking-transaction >/dev/null
AFTER=$(git rev-list --count "$ARCHIVE")
echo "  archive ref: $ARCHIVE ($AFTER commits reachable after branch deletion)"
[ "$BEFORE" = "$AFTER" ] && [ "$AFTER" -gt 5 ] && echo "  ok: full unsquashed chain survived branch deletion" || { echo "  FAIL: archive lost history"; FAILED=1; }

step "queue is empty again"
$G review queue | sed 's/^/  /'

printf '\n'
if [ "$FAILED" = 0 ]; then echo "E2E: all checks passed"; else echo "E2E: FAILURES PRESENT"; exit 1; fi
