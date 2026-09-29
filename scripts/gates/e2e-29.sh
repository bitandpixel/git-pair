#!/usr/bin/env bash
# End-to-end replay of the PRD §29 success workflow against a scratch repo.
# Usage: bash scripts/gates/e2e-29.sh [/path/to/git-pair]
#        (default: the name `mise run build` installs from this repository — the shared
#        ~/.local/bin/git-pair on trunk, a branch-namespaced one anywhere else)
set -uo pipefail
# What this replay does NOT cover, and why: it lands one unstacked changeset, so the "parent landed" notes
# (PRD §21) never appear here — they need a child stacked on the branch being landed, and §29's workflow is
# one changeset from ready to published. Those surfaces are covered by
# internal/cli/status_parent_landed_test.go (the two notes, the exact commands, the worktree blocker) and
# internal/cli/parent_landed_surfaces_test.go (check's verdict, queue's note, the destination branch).
# The default comes from this script's own path, not the working directory, and from the
# same rule `mise run build` uses: this replay cd's into a scratch repo, and the binary
# under test has to be the one built from *this* repository. Falling back to the shared
# name instead would replay whatever another worktree last installed.
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
G=${1:-$HOME/.local/bin/$(sh "$ROOT/scripts/install-name.sh" "$ROOT")}
if [ ! -x "$G" ]; then
  printf 'no binary at %s - run `mise run build` in %s first\n' "$G" "$ROOT" >&2
  exit 1
fi
T=$(mktemp -d /tmp/git-pair-e2e.XXXXXX)
# Every command here runs with stdin at /dev/null. `git pair init` reads a pipe as piped `--about`
# content, so a harness that leaves stdin open — CI, an agent runner, tmux — would leave the first `init`
# blocked in a read that never ends. Nothing in this replay pipes anything in.
exec < /dev/null
trap 'rm -rf "$T"' EXIT
cd "$T" || exit 1
git init -q -b main .
git config user.email r@example.com
git config user.name Reviewer
git config commit.gpgsign false

step() { printf '\n\033[1m### %s\033[0m\n' "$1"; }
# A live git-pair process must never be piped straight into `grep -q` under `pipefail`. `grep -q` exits at
# the first match, the producer's next write gets SIGPIPE, and the pipeline's status becomes that failure --
# so an assertion whose text is present in the output reports FAIL. The fix is to capture the output into a
# variable and let `printf` (one small write, already finished before `grep` decides) do the matching. The
# `git ...` pipes below are safe as they are: their output is one small record written in a single call.
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
$G init --base main; check "init" 0 $?
$G init --base main >/dev/null; check "init idempotent" 0 $?
# A second init naming a different base is a conflict, not an edit: the changeset keeps the base it
# was created with until someone says --set-base. TestChangeInitBaseConflict covers this in Go;
# these two lines cover it against an installed binary, which is what the M1 row claims.
$G init --base trunk >/dev/null 2>&1; check "init refuses to move an existing base" 2 $?
grep -q '^base: main$' changesets/booking-transaction/CHANGESET.yaml && echo "  ok: the refused init left the base alone" || { echo "  FAIL: a refused init rewrote the base"; FAILED=1; }
[ -f changesets/booking-transaction/CHANGESET.yaml ] && echo "  ok: CHANGESET.yaml exists" || { echo "  FAIL: no CHANGESET.yaml"; FAILED=1; }
printf '# booking-transaction\n\n## Summary\n\nTransactional locking around offering creation and enrollment.\n\n## What changed\n\n- business-scoped locking\n\n## Design decisions\n\nAdmin scheduling serializes at business level.\n\n## Validation\n\n- unit tests\n\n## Known limitations\n\n## Open questions\n' > changesets/booking-transaction/ABOUT.md
git add -A && git commit -qm "implement transactional locking"

step "author: ready"
$G change ready; check "change ready" 0 $?
$G status --json | head -30
$G queue | sed 's/^/  /'
$G queue --json > /tmp/q.json; check "queue --json" 0 $?

step "author: withdraw the offer, then re-offer"
# Committing does not take a changeset out of the queue; a command does (PRD §12).
$G change unready; check "change unready" 0 $?
out=$($G queue)
if printf '%s' "$out" | grep -q booking-transaction; then
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
# Nothing under refs/git-pair yet. A review is a commit on a branch; the two durable refs are what
# `integration record` writes at the end, and nothing before that point has anything to record.
[ -z "$(git for-each-ref refs/git-pair)" ] && echo "  ok: a review submission writes no ref" \
  || { echo "  FAIL: reviewing wrote a durable ref: $(git for-each-ref refs/git-pair)"; FAILED=1; }

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
$G check >/dev/null 2>&1; check "check refuses: the reviewed content moved" 1 $?
$G change ready >/dev/null; check "ready again after feedback" 0 $?
$G review submit --approve; check "submit --approve (empty commit)" 0 $?
git log -1 --format='  %h %s%n%b' HEAD

step "author: the gate over the approved head, and the three ways it stops meaning it"
SOURCE=$(git rev-parse HEAD)
$G check; check "check: the approved head is integration-ready" 0 $?
# An integration-ready changeset is still a branch and some commits. The record is written by the
# landing, not by the gate clearing.
[ -z "$(git for-each-ref refs/git-pair)" ] && echo "  ok: integration-ready, and still no durable ref" \
  || { echo "  FAIL: the gate wrote a ref: $(git for-each-ref refs/git-pair)"; FAILED=1; }
# The two ways a passed gate stops meaning what it passed: the reviewed content moved, and the author
# withdrew the offer. Both are exit 1 with a bullet naming which, which is what makes the gate usable
# without reading the source.
printf '\n// a note added after the approval\n' >> src/service.ts
git commit -qam "note an edge case after the approval"
$G check >/dev/null 2>&1; check "check: an implementation commit after the approval fails the gate" 1 $?
$G change ready >/dev/null; check "ready again after the implementation commit" 0 $?
$G change unready >/dev/null; check "withdraw the offer" 0 $?
WITHDRAWAL=$(git rev-parse HEAD)
[ "$WITHDRAWAL" != "$SOURCE" ] && [ -z "$(git for-each-ref refs/git-pair)" ] \
  && echo "  ok: the withdrawal is a marker commit, and writes no ref" \
  || { echo "  FAIL: the withdrawal wrote a ref, or recorded no marker"; FAILED=1; }
$G check >/dev/null 2>&1; check "check: a withdrawn changeset fails the gate" 1 $?

# The third way is the one the tree cannot see. A rebase that resolves no conflict changes no file, so
# the content comparison still passes and only the lineage test refuses: an approval is about a commit,
# and `Review-Head` is what makes that question askable (PRD §10.4, §11.3). This runs on a throwaway
# copy of the branch, because the landing below is about the history the record names.
git switch -qc rewritten booking-transaction
$G change ready >/dev/null; check "ready the copy" 0 $?
$G review submit --approve >/dev/null; check "approve the copy" 0 $?
REVIEWED=$(git rev-parse HEAD~1)
git switch -qc trunk-moved main
git commit -q --allow-empty -m "trunk moves without changing a file"
git switch -q rewritten
git rebase -q trunk-moved; check "rebase the copy onto the moved trunk" 0 $?
if [ -z "$(git diff HEAD@{1} HEAD --name-only)" ]; then
  echo "  ok: the rebase changed no file, so the tree comparison has nothing to report"
else
  echo "  FAIL: the rebase changed files, so this no longer isolates the lineage rule"; FAILED=1
fi
REBASED=$($G check 2>&1); REBASE_EXIT=$?
if printf '%s\n' "$REBASED" | grep -q "no longer in this history" && [ "$REBASE_EXIT" = 1 ]; then
  echo "  ok: a tree-identical rebase loses the approval"
else
  echo "  FAIL: a tree-identical rebase did not refuse the merge (exit $REBASE_EXIT)"; FAILED=1
  printf '%s\n' "$REBASED" | sed 's/^/    /'
fi
out=$($G check --json 2>&1)
if printf '%s\n' "$out" | grep -q "\"reviewed_head\": \"$REVIEWED\""; then
  echo "  ok: --json names the commit the approval spoke about ($REVIEWED)"
else
  echo "  FAIL: --json did not report reviewed_head $REVIEWED"; printf '%s\n' "$out" | sed 's/^/    /'; FAILED=1
fi
# Both forms are captured rather than piped, so a run that failed for an unrelated reason says so
# instead of looking like a missing field. Piping `status` into grep hides its exit code and its stderr,
# which turns a transient git error into an assertion about the reviewed head.
out=$($G status 2>&1)
if printf '%s\n' "$out" | grep -q "reviewed: ${REVIEWED:0:7}"; then
  echo "  ok: status names it too"
else
  echo "  FAIL: status did not report the reviewed head (${REVIEWED:0:7})"; printf '%s\n' "$out" | sed 's/^/    /'; FAILED=1
fi
git switch -q booking-transaction
git branch -D rewritten >/dev/null
git branch -D trunk-moved >/dev/null

step "integration: record where the work landed"
# The landing goes to a branch that is not the default one, which is the case only the record can
# answer for: the changeset directory is still absent from trunk, so the tree rule reads the branch
# as live work until someone says where the change went. The landing commit shares no ancestry with
# the reviewed head, the way a squash leaves them.
# `--source` is the commit the approval speaks about, identified by the changeset directory its tree
# carries — not by a ref, and not by whatever HEAD happens to be.
git switch -qc release/2.x main
git checkout "$SOURCE" -- changesets/booking-transaction
git commit -qm "booking-transaction: land the reviewed work"
LANDING=$(git rev-parse HEAD)
if git merge-base --is-ancestor "$SOURCE" "$LANDING"; then
  echo "  FAIL: the fixture landing should not descend from the reviewed head"; FAILED=1
fi
ARCHIVE=refs/git-pair/archive/booking-transaction
INTEGRATION=refs/git-pair/integrations/booking-transaction
# The recorder verifies before it writes, and the destination is one of the four things it verifies. No
# --target here means git-pair names the destination itself — the changeset's `base:`, then the default
# branch — and this landing is in neither, so the refusal is the answer, with the flag that settles it.
# Landing on a release branch is allowed; it is only not allowed to be silent.
out=$($G integration record --source "$SOURCE" --commit "$LANDING" 2>&1); code=$?
if [ "$code" = 1 ] && printf '%s\n' "$out" | grep -q "is not reachable from main" \
   && printf '%s\n' "$out" | grep -q "changeset's own \`base:\`" \
   && printf '%s\n' "$out" | grep -q -- "--target <ref>"; then
  echo "  ok: an unnamed destination outside trunk is refused, and says how to name it"
else
  echo "  FAIL: the unnamed destination was not refused as expected (exit $code)"; printf '%s\n' "$out" | sed 's/^/    /'; FAILED=1
fi
[ -z "$(git for-each-ref refs/git-pair)" ] && echo "  ok: a refused record writes nothing" \
  || { echo "  FAIL: a refused record wrote a ref"; FAILED=1; }
$G integration record --source "$SOURCE" --commit "$LANDING" --target release/2.x; check "integration record" 0 $?
[ "$(git rev-parse "$ARCHIVE")" = "$SOURCE" ] && echo "  ok: the archive names the reviewed head" \
  || { echo "  FAIL: the archive does not name the reviewed head"; FAILED=1; }
[ "$(git rev-parse "$INTEGRATION")" = "$LANDING" ] && echo "  ok: the integration ref names the landing" \
  || { echo "  FAIL: the integration ref is wrong"; FAILED=1; }
[ "$(git for-each-ref refs/git-pair | wc -l)" = "2" ] \
  && echo "  ok: the pair is all git-pair writes" || { echo "  FAIL: git-pair wrote other refs"; FAILED=1; }

# A clone is the shape CI gets: `refs/heads/*` mapped into `refs/remotes/*`, and nothing else. The
# durable refs were published perfectly and are still absent, so the command that reads them has to
# describe the checkout rather than sentence the work — and `check` is no longer that command, because
# its verdict is the derivation and the trunk and needs no custom ref to be right.
REMOTE=$(mktemp -d)/remote.git; CLONE=$(mktemp -d)/ci
git init -q --bare -b main "$REMOTE"
git push -q "$REMOTE" --all
git push -q "$REMOTE" 'refs/git-pair/*:refs/git-pair/*'
git clone -q "$REMOTE" "$CLONE"
git -C "$CLONE" switch -q booking-transaction
# A clone carries no identity of its own. Whoever runs this gate may or may not have a global git config —
# a CI runner has none — and the fixtures below commit in these clones, which needs an author. Set where the
# work happens rather than depending on whose laptop the gate runs on.
git -C "$CLONE" config user.email ci@example.com
git -C "$CLONE" config user.name CI
# The clone's own status has to say what it compared against: "nothing has landed" from a CI job is
# either a stale fetch or the wrong trunk, and a log that names neither cannot be triaged.
status=$(cd "$CLONE" && $G status --json)
printf '%s' "$status" | grep -q '"default_branch": "origin/main"' \
  && printf '%s' "$status" | grep -q '"default_branch_source": "origin-head"' \
  && echo "  ok: status names the trunk it compared against and how it knew" \
  || { echo "  FAIL: status did not explain its comparison: $status"; FAILED=1; }
out=$(cd "$CLONE" && $G check 2>&1); code=$?
check "check answers about the changeset with no durable refs fetched" 1 $code
printf '%s' "$out" | grep -q "took the changeset out of review" \
  && echo "  ok: the verdict is the changeset's own, refs or no refs" \
  || { echo "  FAIL: the verdict was not the changeset's: $out"; FAILED=1; }
printf '%s' "$out" | grep -q "this clone has no" \
  && { echo "  FAIL: check blamed the clone for something it never read"; FAILED=1; }
# The recorder is the one that reads the namespace, and a record written without seeing the one
# already pushed is a duplicate whose push cannot explain itself. It says so, and writes anyway: the
# record it can write locally is the record this clone is able to write.
out=$(cd "$CLONE" && $G integration record --source "$SOURCE" --commit "$LANDING" --target origin/release/2.x 2>&1); code=$?
check "integration record still records from a clone that cannot see the refs" 0 $code
printf '%s' "$out" | grep -q "holds no refs/git-pair/\* refs at all" \
  && echo "  ok: and it says the clone is short of refs" \
  || { echo "  FAIL: the recorder did not name the missing fetch: $out"; FAILED=1; }
printf '%s' "$out" | grep -q "git fetch origin" \
  && echo "  ok: and prints the fetch to run" \
  || { echo "  FAIL: the recorder did not print the fix: $out"; FAILED=1; }
git -C "$CLONE" fetch -q origin 'refs/git-pair/*:refs/git-pair/*'
out=$(cd "$CLONE" && $G integration record --source "$SOURCE" --commit "$LANDING" --target origin/release/2.x 2>&1)
check "with the refs fetched, the same call is a plain no-op" 0 $?
printf '%s' "$out" | grep -q "already recorded" \
  && echo "  ok: it found the record it could not see before" \
  || { echo "  FAIL: after the fetch the record said something else: $out"; FAILED=1; }
printf '%s' "$out" | grep -q "holds no refs/git-pair" \
  && { echo "  FAIL: the warning outlived the fetch"; FAILED=1; }

git switch -q booking-transaction
out=$($G integration record --source "$SOURCE" --commit "$LANDING" 2>&1)
check "recording the same pair twice is a success that changed nothing" 0 $?
printf '%s' "$out" | grep -q "already recorded" && echo "  ok: and it says so" \
  || { echo "  FAIL: the second recording explained nothing: $out"; FAILED=1; }
# A different pair for the same changeset is a different claim, and there is no answer that is both
# safe and automatic: git-pair has no operation that moves a durable ref.
git switch -q release/2.x
printf 'backported\n' > backport.md && git add -A && git commit -qm "booking-transaction: backport it"
BACKPORT=$(git rev-parse HEAD)
out=$($G integration record --source "$SOURCE" --commit "$BACKPORT" --target release/2.x 2>&1)
check "a second landing for a recorded changeset is refused" 1 $?
printf '%s' "$out" | grep -q "$INTEGRATION records" \
  && echo "  ok: the refusal names the record and its commit" \
  || { echo "  FAIL: the refusal did not name what is on the record: $out"; FAILED=1; }
printf '%s' "$out" | grep -q "never moves" \
  && echo "  ok: and says plainly that there is no flag for this" \
  || { echo "  FAIL: the refusal offered a way round it: $out"; FAILED=1; }
[ "$(git rev-parse "$INTEGRATION")" = "$LANDING" ] && echo "  ok: the record did not move" \
  || { echo "  FAIL: a refused recording moved the record"; FAILED=1; }

# A landing on a branch that is not the integration branch leaves the work live. The record used to refuse
# every in-flight command the moment it existed, whatever branch it named; the tree says what it says, and
# the release line can still revert the merge or leave the changeset to reach main on its own. This is the
# half of the design that a ref could not express, because a ref stored an object id and no question.
git switch -q booking-transaction
$G change unready >/dev/null 2>&1; check "a changeset merged only into release/2.x can still be withdrawn" 0 $?
$G change ready >/dev/null 2>&1; check "and offered again" 0 $?
out=$($G check 2>&1); code=$?
if printf '%s' "$out" | grep -q "already landed"; then
  echo "  FAIL: check refused work the integration branch never took: $out"; FAILED=1
else
  echo "  ok: check has no landing refusal for a release-line merge"
fi
printf '%s' "$out" | grep -qE "READY|NOT READY" \
  && echo "  ok: and it still answers the question it was asked (exit $code)" \
  || { echo "  FAIL: check printed no verdict: $out"; FAILED=1; }
git switch -q main
out=$($G queue 2>&1)
printf '%s' "$out" | grep -q "booking-transaction" \
  && echo "  ok: the queue still accounts for the changeset, branch and all" \
  || { echo "  FAIL: the queue lost a changeset whose branch is live"; FAILED=1; }
[ "$(git rev-parse "$ARCHIVE")" = "$SOURCE" ] && [ "$(git rev-parse "$INTEGRATION")" = "$LANDING" ] \
  && echo "  ok: the record the release landing wrote is untouched by any of that" \
  || { echo "  FAIL: a durable ref moved after the record"; FAILED=1; }
BEFORE=$(git rev-list --count "$ARCHIVE")
git branch -D booking-transaction >/dev/null
AFTER=$(git rev-list --count "$ARCHIVE")
echo "  archive ref: $ARCHIVE ($AFTER commits reachable after branch deletion)"
[ "$BEFORE" = "$AFTER" ] && [ "$AFTER" -gt 5 ] && echo "  ok: full unsquashed chain survived branch deletion" \
  || { echo "  FAIL: the record lost history"; FAILED=1; }
# What the record does not hold: anything after the head it named. The withdrawal marker came after
# the approval, so the record does not reach it and the branch was the only thing that did. That is the
# window the design accepts, rather than closing with a ref four commands had to keep pointing at HEAD.
if git merge-base --is-ancestor "$WITHDRAWAL" "$ARCHIVE" 2>/dev/null; then
  echo "  FAIL: the archive reaches a commit recorded after the head it names"; FAILED=1
else
  echo "  ok: the archive stops at the head the record names"
fi

step "declaration: the author asks for the merge, and a pipeline reads that"
# `git pair change integrate` (PRD §9.9) is the author's half of an automatic merge: a marker commit, and
# no ref, no push, no merge. This step walks the request from the branch to a second clone, because a
# request is only useful to somebody who can see it, and it asserts that the gate's two answers move apart
# — which is the whole reason there are two of them.
# What this replay does not cover: the three landing shapes and what each leaves the record to name. That
# matrix is internal/cli/integration_test.go and internal/cli/integration_destination_test.go, where a
# landing can be staged without pretending one CI job performed three merges of one changeset.
git switch -qc awaiting-merge main
mkdir -p changesets/awaiting-merge
printf 'base: main\n' > changesets/awaiting-merge/CHANGESET.yaml
printf 'Summary: work whose author hands it over for merging.\n' > changesets/awaiting-merge/ABOUT.md
printf 'offered\n' > awaiting.md && git add -A && git commit -qm "awaiting-merge: the work"
$G change ready >/dev/null 2>&1; check "the changeset to be declared is offered" 0 $?
$G review submit --approve >/dev/null 2>&1; check "and approved" 0 $?
out=$($G check --json 2>&1)
printf '%s' "$out" | grep -q '"integrating": false' \
  && echo "  ok: an approved head nobody declared is not integrating" \
  || { echo "  FAIL: an undeclared head reported integrating: $out"; FAILED=1; }
$G change integrate >/dev/null; check "change integrate" 0 $?
[ "$(git log -1 --format=%s)" = "git-pair: integrate awaiting-merge" ] \
  && echo "  ok: the declaration is a commit on the branch" \
  || { echo "  FAIL: the newest commit is not the declaration"; FAILED=1; }
[ -z "$(git diff HEAD~1 HEAD --name-only)" ] && echo "  ok: and it changes no file" \
  || { echo "  FAIL: the declaration touched the tree"; FAILED=1; }
git log -1 --format=%B | grep -q '^Review-State: integrating$' \
  && echo "  ok: it carries the state" \
  || { echo "  FAIL: the declaration carries no state trailer"; FAILED=1; }
git log -1 --format=%B | grep -q "^Review-Head: $(git rev-parse HEAD~1)$" \
  && echo "  ok: and the head it declares, which is what makes a rewrite refuse instead of bless" \
  || { echo "  FAIL: the declaration names no head, or the wrong one"; FAILED=1; }
# Scoped to this changeset: the replay wrote booking-transaction's pair earlier, and "git-pair writes no
# ref" is a claim about the command that ran, not about the repository's whole namespace.
[ -z "$(git for-each-ref refs/git-pair/archive/awaiting-merge refs/git-pair/integrations/awaiting-merge)" ] \
  && echo "  ok: a declaration writes no durable ref (PRD §26)" \
  || { echo "  FAIL: a declaration wrote a ref"; FAILED=1; }
$G check >/dev/null 2>&1; check "the gate still passes under the declaration" 0 $?
out=$($G check --json 2>&1)
printf '%s' "$out" | grep -q '"ready": true' && printf '%s' "$out" | grep -q '"integrating": true' \
  && echo "  ok: CI's gate is both answers of this one command" \
  || { echo "  FAIL: ready and integrating were not both true: $out"; FAILED=1; }
out=$($G queue 2>&1)
printf '%s' "$out" | grep -q "AWAITING INTEGRATION" && printf '%s' "$out" | grep -q "merge into: main" \
  && echo "  ok: the queue lists it as awaiting integration, with its destination" \
  || { echo "  FAIL: the queue did not report the request: $out"; FAILED=1; }
printf '%s' "$out" | sed -n '/READY FOR REVIEW/,/AWAITING INTEGRATION/p' | grep -q awaiting-merge \
  && { echo "  FAIL: declared work was listed for review too"; FAILED=1; }
out=$($G change integrate 2>&1)
printf '%s' "$out" | grep -q "already declared" \
  && echo "  ok: re-running at the same head records nothing and succeeds" \
  || { echo "  FAIL: a second declaration explained nothing: $out"; FAILED=1; }
# The declaration is not a verdict. Content that moved since the approval takes `ready` away and leaves the
# request standing, and the command that would have made a new one refuses.
printf 'answered\n' >> awaiting.md && git add -A && git commit -qm "awaiting-merge: an answer the reviewer never saw"
out=$($G check --json 2>&1)
printf '%s' "$out" | grep -q '"ready": false' && printf '%s' "$out" | grep -q '"integrating": true' \
  && echo "  ok: the pair reads asked, and not yet permitted" \
  || { echo "  FAIL: the two answers did not move apart: $out"; FAILED=1; }
$G change integrate >/dev/null 2>&1; check "and the drift refuses a new declaration" 1 $?
$G change unready >/dev/null 2>&1; check "change unready withdraws the declaration" 0 $?
out=$($G check --json 2>&1)
printf '%s' "$out" | grep -q '"integrating": false' \
  && echo "  ok: a superseded request stops the pipeline, with no rebase and no force-push" \
  || { echo "  FAIL: a withdrawn declaration still read as a request: $out"; FAILED=1; }
$G change ready >/dev/null 2>&1; $G review submit --approve >/dev/null 2>&1
$G change integrate >/dev/null 2>&1; check "and the request can be made again" 0 $?
# The clone is the shape a merge job gets: it sees the branch because it was pushed, and the request
# because a request is a commit. Nothing but the branch carried it here.
DECL=$(mktemp -d); DECLREMOTE="$DECL/remote.git"; DECLCLONE="$DECL/ci"
git init -q --bare -b main "$DECLREMOTE"
git push -q "$DECLREMOTE" --all
git clone -q "$DECLREMOTE" "$DECLCLONE"
git -C "$DECLCLONE" switch -q awaiting-merge
git -C "$DECLCLONE" config user.email ci@example.com
git -C "$DECLCLONE" config user.name CI
out=$(cd "$DECLCLONE" && $G check --json 2>&1)
printf '%s' "$out" | grep -q '"ready": true' && printf '%s' "$out" | grep -q '"integrating": true' \
  && echo "  ok: a clone that never met the author gates on the same two fields" \
  || { echo "  FAIL: the clone could not see the request: $out"; FAILED=1; }
# The merge is ordinary git, performed by whoever owns the destination — and the record follows it with no
# flags, which is the CI shape: two SHAs a pipeline already holds are not needed when the branch is here.
git -C "$DECLCLONE" switch -q main
git -C "$DECLCLONE" merge -q --no-ff -m "awaiting-merge: land the reviewed work" awaiting-merge
out=$(cd "$DECLCLONE" && $G integration record 2>&1); code=$?
check "the recorder needs no flags in the clone that merged" 0 $code
printf '%s' "$out" | grep -q "verified reachable from main" \
  && echo "  ok: and it verified the landing against the destination it derived" \
  || { echo "  FAIL: the record did not verify a destination: $out"; FAILED=1; }
out=$(cd "$DECLCLONE" && $G integration publish --remote origin 2>&1); code=$?
check "and publishes the pair from there" 0 $code
for family in integrations archive; do
  if git --git-dir="$DECLREMOTE" for-each-ref --format='%(refname)' \
       "refs/git-pair/$family/awaiting-merge" | grep -q .; then
    echo "  ok: refs/git-pair/$family/awaiting-merge reached the remote"
  else
    echo "  FAIL: the declared landing's record never left the clone"; FAILED=1
  fi
done
# The clone published the pair, and this clone reading it back is the last claim the declaration has to
# survive: the request was made here, the merge happened elsewhere, and the paper trail that says so has to
# reach the author's machine or the author is looking at a changeset that appears to still be waiting.
git fetch -q "$DECLREMOTE" 'refs/git-pair/*:refs/git-pair/*'
out=$($G queue 2>&1)
if printf '%s' "$out" | grep -q "awaiting-merge (integrated at"; then
  echo "  ok: with the record fetched, this clone stops listing the request"
else
  echo "  FAIL: the author's clone still lists a changeset the other clone landed and recorded"
  $G queue 2>&1 | sed 's/^/    /'
  FAILED=1
fi
rm -rf "$DECL"

step "work that reached trunk with no approving verdict is reported rather than hidden"
# The merge is git's, and so is the review that may or may not have happened before it. When a changeset's
# directory is in trunk, the rules that find work in progress stop seeing it, and the only account of it is
# the history behind the directory. The detector reads that history, which is why the integration contract
# (PRD §22) is enforceable with no refs to consult.
#
# Two shapes reach the heading and the report separates them: a chain that carries no verdict, and a landing
# that carried the directory in one commit so no markers came with it.
git switch -q main
git checkout -qb unreviewed-merge
mkdir -p changesets/unreviewed-merge
printf 'base: main\n' > changesets/unreviewed-merge/CHANGESET.yaml
printf 'Summary: work that reached trunk with nobody approving it.\n' > changesets/unreviewed-merge/ABOUT.md
printf 'merged\n' > unreviewed.md && git add -A && git commit -qm "unreviewed-merge: the work"
git switch -q main
git merge -q --no-ff -m "unreviewed-merge: merge it without a review" unreviewed-merge
git branch -q -D unreviewed-merge
# The squash shape, from a changeset that *was* reviewed: the review happened and the landing shape kept
# none of it. Same heading, different sentence.
git checkout -qb unrecorded-landing
mkdir -p changesets/unrecorded-landing
printf 'base: main\n' > changesets/unrecorded-landing/CHANGESET.yaml
printf 'Summary: work that lands without its record.\n' > changesets/unrecorded-landing/ABOUT.md
printf 'landed\n' > unrecorded.md && git add -A && git commit -qm "unrecorded-landing: the work"
$G change ready >/dev/null 2>&1; check "the reviewed changeset is offered" 0 $?
$G review submit --approve >/dev/null 2>&1; check "and approved" 0 $?
git switch -q main
git checkout unrecorded-landing -- changesets/unrecorded-landing
git commit -qm "unrecorded-landing: land it, write no record"
out=$($G queue 2>&1)
printf '%s' "$out" | grep -q "LANDED UNREVIEWED" \
  && echo "  ok: the queue reports the landings as its own heading" \
  || { echo "  FAIL: work in trunk with no verdict left no trace in the queue: $out"; FAILED=1; }
printf '%s' "$out" | grep -q "unreviewed-merge" \
  && printf '%s' "$out" | grep -q "no review verdict" \
  && echo "  ok: naming the changeset whose chain carries no verdict" \
  || { echo "  FAIL: the chain-with-no-verdict case is missing: $out"; FAILED=1; }
printf '%s' "$out" | grep -q "unrecorded-landing" \
  && printf '%s' "$out" | grep -q "one commit" \
  && echo "  ok: and the landing that carried no history says so rather than blaming the reviewers" \
  || { echo "  FAIL: the squash case is missing or blamed the wrong thing: $out"; FAILED=1; }
printf '%s' "$out" | grep -q "status --changeset" \
  && echo "  ok: and prints the read that goes and looks, not a command that writes" \
  || { echo "  FAIL: the heading offered a command instead of a read: $out"; FAILED=1; }
out=$($G queue --json)
printf '%s' "$out" | grep -q '"landed_unreviewed"' \
  && echo "  ok: the machine surface carries the same finding" \
  || { echo "  FAIL: queue --json has no landed_unreviewed"; FAILED=1; }
out=$($G status 2>&1); code=$?
[ "$code" = 2 ] && echo "  ok: status on the destination branch still exits 2" \
  || { echo "  FAIL: status on the destination branch exited $code"; FAILED=1; }
printf '%s' "$out" | grep -q "no changeset for this branch" \
  && printf '%s' "$out" | grep -q "LANDED UNREVIEWED" \
  && echo "  ok: and its answer carries the finding" \
  || { echo "  FAIL: status said only that the branch has no changeset: $out"; FAILED=1; }
out=$($G status --changeset unreviewed-merge --json)
printf '%s' "$out" | grep -q '"landed": true' \
  && echo "  ok: and a landed changeset read by name says landed, from the tree" \
  || { echo "  FAIL: status --changeset did not report the landing"; FAILED=1; }
# Nothing closes the heading with a command. Writing the record -- the invocation this step used to print as
# the fix -- changes the finding not at all, which is the difference between this heading and the one it
# replaced. The record is still written, because the publish step below needs a pair to send.
$G integration record --changeset unrecorded-landing >/dev/null 2>&1
check "recording the landing is a success" 0 $?
out=$($G queue 2>&1)
if ! printf '%s' "$out" | grep -q "LANDED UNREVIEWED"; then
  echo "  FAIL: the report went quiet when a record was written, so it was never about the work"; FAILED=1
else
  echo "  ok: and the report is still there, because the finding is about the chain, not the paper trail"
fi

step "publish: the refs leave the clone that wrote them"
# The landing contract (PRD §29) is record, publish, then delete. This step walks the second step and
# then checks it from somewhere else, because "published" means a different clone can read it — the
# claim cannot be checked from the machine that made it.
PUBDIR=$(mktemp -d /tmp/git-pair-pub.XXXXXX)
PUBREMOTE="$PUBDIR/remote.git"
git init -q --bare -b main "$PUBREMOTE"
git remote add pub "$PUBREMOTE"
git push -q pub --all
out=$($G integration publish --remote pub 2>&1); code=$?
check "integration publish" 0 $code
printf '%s' "$out" | grep -q "unrecorded-landing: published to pub" \
  && echo "  ok: it says what it published" \
  || { echo "  FAIL: publish did not report the pair: $out"; FAILED=1; }
for family in integrations archive; do
  if git --git-dir="$PUBREMOTE" for-each-ref --format='%(refname)' \
       "refs/git-pair/$family/unrecorded-landing" | grep -q .; then
    echo "  ok: refs/git-pair/$family/unrecorded-landing reached the remote"
  else
    echo "  FAIL: refs/git-pair/$family/unrecorded-landing never arrived"; FAILED=1
  fi
done
out=$($G integration publish --remote pub 2>&1)
printf '%s' "$out" | grep -q "already published" \
  && echo "  ok: re-running publishes nothing and says so" \
  || { echo "  FAIL: a second publish did not report idempotence: $out"; FAILED=1; }

git clone -q "$PUBREMOTE" "$PUBDIR/reader"
(cd "$PUBDIR/reader" && git switch -q unrecorded-landing)
# `--fetch` is the read side, and the check is that the record arrives in the reader's own namespace —
# checked with plumbing here because every changeset in this scratch repository has landed, so `status`
# has no live changeset to speak about. The claims about what a fetched record then *answers* live in
# internal/cli/fetch_test.go, where a live changeset can be staged.
out=$(cd "$PUBDIR/reader" && $G queue --fetch --json 2>&1); code=$?
check "--fetch works from a clone that never saw the landing" 0 $code
for family in integrations archive; do
  if git -C "$PUBDIR/reader" for-each-ref --format='%(refname)' \
       "refs/git-pair/$family/unrecorded-landing" | grep -q .; then
    echo "  ok: --fetch brought refs/git-pair/$family/unrecorded-landing to a fresh clone"
  else
    echo "  FAIL: the published record is not readable elsewhere"; FAILED=1
  fi
done
# A clone that holds no pairs of its own still answers the question, with an empty list rather than a
# missing key: `[]` means "nothing here is waiting to be published" and nothing else. The finding itself —
# a record that exists only locally, and half a pair — is covered by internal/cli/published_test.go, which
# can stage those states without pretending a fresh clone holds records it never wrote.
out=$(cd "$PUBDIR/reader" && $G queue --fetch --json 2>&1)
printf '%s' "$out" | grep -q '"unpublished": \[\]' \
  && echo "  ok: a clone with nothing to publish answers with an empty list, not an absent key" \
  || { echo "  FAIL: the queue JSON omitted the answer: $out"; FAILED=1; }
rm -rf "$PUBDIR"

step "tidy: a landing's directory moves aside, and every answer about it holds"
# Landing keeps the directory, which is right the moment it lands and scenery a release later.
# `change tidy` moves it out of the way as one commit of renames, and the point of the step is the
# second half: every question the tool answers about that changeset has to be answered the same way
# on both sides of the move, because nothing about the work changed.
git switch -q main
git checkout -qb tidied-landing
mkdir -p changesets/tidied-landing
printf 'base: main\n' > changesets/tidied-landing/CHANGESET.yaml
printf 'Summary: work that lands, then gets out of the way.\n' > changesets/tidied-landing/ABOUT.md
printf 'tidied\n' > tidied.md && git add -A && git commit -qm "tidied-landing: the work"
git switch -q main
git merge -q --no-ff -m "tidied-landing: merge it" tidied-landing
# The move rides a changeset, so it happens on a branch and reaches trunk the way any other change does.
git switch -qc tidy-up main
out=$($G change tidy tidied-landing --dry-run 2>&1); code=$?
if [ "$code" = 0 ] && printf '%s' "$out" | grep -q "changesets/tidied-landing -> changesets/.landed/tidied-landing"; then
  echo "  ok: the dry run names the move it would make"
else
  echo "  FAIL: the dry run did not report the move: $out"; FAILED=1
fi
git rev-parse --quiet --verify HEAD:changesets/tidied-landing/CHANGESET.yaml >/dev/null \
  && echo "  ok: and committed nothing" \
  || { echo "  FAIL: the dry run moved the directory"; FAILED=1; }
out=$($G change tidy tidied-landing 2>&1); code=$?
check "the tidy succeeds" 0 "$code"
if git diff --name-status HEAD~1 HEAD | grep -qv '^R'; then
  echo "  FAIL: the tidy commit carries more than renames: $(git diff --name-status HEAD~1 HEAD)"; FAILED=1
else
  echo "  ok: the commit is renames only, so a reviewer reads it as a move"
fi
git rev-parse --quiet --verify HEAD:changesets/.landed/tidied-landing/CHANGESET.yaml >/dev/null \
  && echo "  ok: the directory is where the tidy said it would be" \
  || { echo "  FAIL: the landed spelling is not in HEAD"; FAILED=1; }
out=$($G status --changeset tidied-landing 2>&1); code=$?
if [ "$code" = 0 ] && printf '%s' "$out" | grep -qi "landed"; then
  echo "  ok: status still reports it landed, read from where it now sits"
else
  echo "  FAIL: status after the tidy exited $code: $out"; FAILED=1
fi
out=$($G change tidy tidied-landing 2>&1); code=$?
if [ "$code" = 0 ] && printf '%s' "$out" | grep -q "already tidied"; then
  echo "  ok: the second run is a no-op that names the reason"
else
  echo "  FAIL: the second run exited $code: $out"; FAILED=1
fi
out=$($G init --id tidied-landing --base main 2>&1); code=$?
if [ "$code" != 0 ] && printf '%s' "$out" | grep -q "changeset tidied-landing is landed on main at changesets/"; then
  echo "  ok: the name is still held by the landing, and the refusal says by what"
else
  echo "  FAIL: reusing a tidied landing's id exited $code: $out"; FAILED=1
fi
out=$($G queue --json 2>&1)
if printf '%s' "$out" | grep -q '"slug": *"tidied-landing"'; then
  echo "  FAIL: the queue lists a tidied landing as work in review"; FAILED=1
else
  echo "  ok: the queue does not list it as work"
fi
out=$($G check --changeset tidied-landing 2>&1); code=$?
if [ "$code" = 1 ]; then
  echo "  ok: check refuses it the way it refuses any landing"
else
  echo "  FAIL: check exited $code: $out"; FAILED=1
fi
git switch -q main
git branch -q -D tidy-up tidied-landing

step "queue is empty again"
$G queue | sed 's/^/  /'

printf '\n'
if [ "$FAILED" = 0 ]; then echo "E2E: all checks passed"; else echo "E2E: FAILURES PRESENT"; exit 1; fi
