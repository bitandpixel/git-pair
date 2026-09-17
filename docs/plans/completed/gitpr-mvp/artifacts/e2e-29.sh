#!/usr/bin/env bash
# End-to-end replay of the PRD §29 success workflow against a scratch repo.
# Usage: bash docs/plans/completed/gitpr-mvp/artifacts/e2e-29.sh /path/to/gitpr
set -uo pipefail
G=${1:-/tmp/gitpr}
T=$(mktemp -d /tmp/gitpr-e2e.XXXXXX)
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
[ -f changesets/booking-transaction/CHANGESET.yaml ] && echo "  ok: CHANGESET.yaml exists" || { echo "  FAIL: no CHANGESET.yaml"; FAILED=1; }
printf '# booking-transaction\n\n## Summary\n\nTransactional locking around offering creation and enrollment.\n\n## What changed\n\n- business-scoped locking\n\n## Design decisions\n\nAdmin scheduling serializes at business level.\n\n## Validation\n\n- unit tests\n\n## Known limitations\n\n## Open questions\n' > changesets/booking-transaction/ABOUT.md
git add -A && git commit -qm "implement transactional locking"

step "author: ready"
$G change ready; check "change ready" 0 $?
$G status --json | head -30
$G review queue | sed 's/^/  /'
$G review queue --json > /tmp/q.json; check "queue --json" 0 $?

step "reviewer: edits code, adds thread, submits --block"
printf '  // What happens if these execute concurrently?\n' >> src/service.ts
printf '  // Please use a transaction here\n' >> src/service.ts
$G review thread "concurrency tests" </dev/null; check "review thread (no tty refuses)" 2 $?
[ -f changesets/booking-transaction/concurrency-tests.md ] && echo "  ok: thread file created" || { echo "  FAIL: thread not created"; FAILED=1; }
$G review submit --block; check "review submit --block" 0 $?
$G review history; check "review history" 0 $?
git for-each-ref --format='  %(refname) -> %(objectname:short)' refs/reviews

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
printf '\n// tighten naming\n' >> src/service.ts
git commit -qam "rename for clarity"
$G review close; check "close refused while WORKING" 1 $?
$G change ready >/dev/null; check "ready again after feedback" 0 $?
$G review submit --approve; check "submit --approve (empty commit)" 0 $?
git log -1 --format='  %h %s%n%b' HEAD

step "close: archive and squash-safety"
$G review close; check "review close" 0 $?
ARCHIVE=$(git for-each-ref --format='%(refname)' refs/reviews/archive | head -1)
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
