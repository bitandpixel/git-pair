#!/usr/bin/env bash
# End-to-end replay of the PRD §29 success workflow against a scratch repo.
# Usage: bash scripts/gates/e2e-29.sh [/path/to/git-pair]
#        (default: the name `mise run build` installs from this repository — the shared
#        ~/.local/bin/git-pair on trunk, a branch-namespaced one anywhere else)
# `pipefail` is wrong for this script, and was failing it at random. Almost every assertion below is
# `printf '%s' "$out" | grep -q PATTERN`, and `grep -q` leaves the moment it matches. The writer is then left
# holding a closed pipe; a pipeline element runs in a subshell, and a subshell takes the default SIGPIPE
# disposition back, so the pipeline reports 141 and the check fails on output that contains the pattern - 4
# times in 2000 on a 511-byte payload taken from one such failure, which is about one spurious failure in four
# runs of this script. Nothing here asks whether the left half of a pipeline failed: the writers are `printf`s
# of variables in memory, and the only other pipes are display helpers. The answer that matters is grep's,
# which is what a plain `set -u` reports.
set -u
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
# Nothing under refs/git-pair. A review is a commit on the branch and nothing else: git-pair writes no ref at
# any point in a lifecycle, so there is no namespace for a submission to have written (PRD §13.4).
[ -z "$(git for-each-ref refs/git-pair)" ] && echo "  ok: a review submission writes no ref" \
  || { echo "  FAIL: reviewing wrote a ref: $(git for-each-ref refs/git-pair)"; FAILED=1; }

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
[ -z "$(git for-each-ref refs/git-pair)" ] && echo "  ok: integration-ready, and still no ref of git-pair's own" \
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

step "a landing on a branch that is not the destination leaves the work live"
# The merge is ordinary git and it can go anywhere. `landed` is a claim about one particular branch — the
# changeset's destination (PRD §13.1) — so a merge into a release line is a merge, not a landing: main does
# not carry the directory, the work is still in progress, and review can still rewrite the branch. There is
# no record to write and no flag to say where the work went: naming the destination is a read parameter, and
# the history that answers "landed, and reviewed?" is already in the branch it merged into (PRD §13.3).
# The branch last moved on a withdrawal, so offer it and approve it once more: this merge is the landing an
# approval permits, and the head it speaks about is the head the chain ends at.
git switch -q booking-transaction
$G change ready >/dev/null; check "re-offered for the landing" 0 $?
$G review submit --approve >/dev/null; check "and approved again" 0 $?
SOURCE=$(git rev-parse HEAD)
git switch -qc release/2.x main
git merge -q --no-ff -m "booking-transaction: land the reviewed work" booking-transaction
LANDING=$(git rev-parse HEAD)
if git merge-base --is-ancestor "$SOURCE" "$LANDING"; then
  echo "  ok: the merge carries the reviewed head, so the chain arrives with the directory"
else
  echo "  FAIL: the landing does not contain the reviewed head"; FAILED=1
fi
# The claim PRD §13.4 makes about the whole tool, checked where a ref used to be written: a landing writes
# nothing outside the destination's history.
[ -z "$(git for-each-ref refs/git-pair)" ] && echo "  ok: a landing writes no ref of git-pair's own" \
  || { echo "  FAIL: a landing wrote a ref: $(git for-each-ref refs/git-pair)"; FAILED=1; }
CHAIN=$(git rev-list --count "$LANDING")
[ "$CHAIN" -gt 5 ] && echo "  ok: the unsquashed chain survives in the destination ($CHAIN commits)" \
  || { echo "  FAIL: the landing lost history: $CHAIN commits behind it"; FAILED=1; }
landing_log=$(git log "$LANDING" --format=%B)
printf '%s' "$landing_log" | grep -q '^Review-Outcome: approve$' \
  && echo "  ok: the approval is readable in the destination's history, which is what a landing keeps" \
  || { echo "  FAIL: the approval did not arrive with the merge"; FAILED=1; }

# On the branch the work was done on, nothing has finished: the gate answers its ordinary question, and the
# landing refusal that belongs to a landed changeset is absent. This is the half of the design a stored
# record could not express — a ref held an object id and no question about the destination.
git switch -q booking-transaction
out=$($G check 2>&1); code=$?
if printf '%s' "$out" | grep -q "already landed"; then
  echo "  FAIL: check refused work the destination never took: $out"; FAILED=1
else
  echo "  ok: check has no landing refusal for a release-line merge"
fi
printf '%s' "$out" | grep -q "merge into main with ordinary git" \
  && echo "  ok: and it still names the destination the work is measured against (exit $code)" \
  || { echo "  FAIL: check did not name the destination: $out"; FAILED=1; }
$G change unready >/dev/null 2>&1; check "a changeset merged only into release/2.x can still be withdrawn" 0 $?
$G change ready >/dev/null 2>&1; check "and offered again" 0 $?
git switch -q main
# A record used to make this changeset a finished thing in every report. The tree says what the destination
# says: main never took it, so the work is still here — offered, and not landed — which is the half of the
# design a stored record could not express, because a ref named an object and no destination.
out=$($G status --changeset booking-transaction --json 2>&1)
printf '%s' "$out" | grep -q '"state": "READY"' \
  && printf '%s' "$out" | grep -q '"landed": false' \
  && echo "  ok: the branch the work was done on is still work in progress" \
  || { echo "  FAIL: the release landing made the changeset a finished thing: $out"; FAILED=1; }

# Where the answers differ is in the destination, and naming it is the only thing that changes them. Measured
# against main the changeset is unlanded work; measured against the branch that carries the directory it is a
# landing whose chain carries the approval. One read, two branches, no write.
out=$($G status --changeset booking-transaction --json 2>&1)
printf '%s' "$out" | grep -q '"landed": false' \
  && printf '%s' "$out" | grep -q '"landed_branch": ""' \
  && echo "  ok: measured against main, it is not landed" \
  || { echo "  FAIL: status measured against the wrong branch: $out"; FAILED=1; }
out=$($G status --changeset booking-transaction --default-branch release/2.x --json 2>&1)
printf '%s' "$out" | grep -q '"landed": true' \
  && printf '%s' "$out" | grep -q '"landed_branch": "release/2.x"' \
  && printf '%s' "$out" | grep -q '"reviewed": true' \
  && echo "  ok: named as the destination, the same history reports it landed and reviewed" \
  || { echo "  FAIL: naming the destination did not report the landing: $out"; FAILED=1; }
printf '%s' "$out" | grep -q "\"chain_head\": \"${SOURCE:0:7}\"" \
  && echo "  ok: and names the head the approval spoke about (${SOURCE:0:7})" \
  || { echo "  FAIL: the derived chain did not name the reviewed head: $out"; FAILED=1; }
# Once the branch is gone, the landing is the only account of the work, and it is enough: the queue has
# nothing to ask, and the read by name still walks the chain out of the destination.
git switch -q main
git branch -q -D booking-transaction
out=$($G status --changeset booking-transaction --default-branch release/2.x --json 2>&1)
printf '%s' "$out" | grep -q '"landed": true' \
  && printf '%s' "$out" | grep -q '"chain_base": "' \
  && echo "  ok: after the branch is deleted, the destination still answers for it" \
  || { echo "  FAIL: deleting the branch lost the changeset: $out"; FAILED=1; }
queued=$($G queue 2>&1)
printf '%s' "$queued" | grep -q "LANDED UNREVIEWED" \
  && { echo "  FAIL: a landing whose chain carries an approval was reported unreviewed"; FAILED=1; } \
  || echo "  ok: and it is not a landing to complain about, because the chain carries the approval"

# A clone is the shape CI gets: `refs/heads/*` mapped into `refs/remotes/*`, and nothing else. Nothing has
# to be fetched beyond the branches, so the command that reads a landing has to describe the checkout rather
# than sentence the work — and it must not blame the clone for a namespace it was never given.
REMOTE=$(mktemp -d)/remote.git; CLONE=$(mktemp -d)/ci
git init -q --bare -b main "$REMOTE"
git push -q "$REMOTE" --all
git clone -q "$REMOTE" "$CLONE"
git -C "$CLONE" switch -q release/2.x
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
out=$(cd "$CLONE" && $G status --changeset booking-transaction --default-branch release/2.x --json 2>&1); code=$?
check "a clone that never saw the merge answers about the landing" 0 $code
printf '%s' "$out" | grep -q '"landed": true' \
  && printf '%s' "$out" | grep -q '"landed_branch": "release/2.x"' \
  && echo "  ok: from the branches alone, with no namespace to fetch" \
  || { echo "  FAIL: the clone could not read the landing: $out"; FAILED=1; }
printf '%s' "$out" | grep -q "this clone has no" \
  && { echo "  FAIL: the clone was blamed for a namespace it was never given"; FAILED=1; } \
  || echo "  ok: and it says nothing about refs it has no reason to hold"
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
  && echo "  ok: a declaration writes no ref (PRD §26)" \
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
# The merge is ordinary git, performed by whoever owns the destination — here, the clone. Nothing follows it:
# no ref to write, nothing to publish, no second command for the author to run (PRD §13.4). The author pulls,
# and the branch itself is the news.
git -C "$DECLCLONE" switch -q main
git -C "$DECLCLONE" merge -q --no-ff -m "awaiting-merge: land the reviewed work" awaiting-merge
# Whoever owns the destination pushes it. That is the person's git, not git-pair's: the tool's half of the
# landing contract is that it performs neither the merge nor the push (PRD §26).
git -C "$DECLCLONE" push -q origin main
[ -z "$(git -C "$DECLCLONE" for-each-ref refs/git-pair)" ] \
  && [ -z "$(git --git-dir="$DECLREMOTE" for-each-ref refs/git-pair)" ] \
  && echo "  ok: the clone that merged writes no ref and publishes nothing (PRD §26)" \
  || { echo "  FAIL: the landing wrote a ref: $(git -C "$DECLCLONE" for-each-ref refs/git-pair)"; FAILED=1; }
git fetch -q "$DECLREMOTE" 'refs/heads/main:refs/heads/main'
out=$($G status --changeset awaiting-merge --json 2>&1)
printf '%s' "$out" | grep -q '"landed": true' \
  && printf '%s' "$out" | grep -q '"landed_branch": "main"' \
  && echo "  ok: the author's clone reads the landing out of the branch it just pulled" \
  || { echo "  FAIL: the author cannot see the landing the other clone made: $out"; FAILED=1; }
queued=$($G queue 2>&1)
if printf '%s' "$queued" | grep -q "AWAITING INTEGRATION"; then
  echo "  FAIL: the queue still lists a request the destination already carries"
  printf '%s\n' "$queued" | sed 's/^/    /'
  FAILED=1
else
  echo "  ok: and the queue stops asking, because the destination carries the work"
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
# Nothing closes this heading by writing something. No command makes a squash landing carry the review it
# lost, and the finding stays where the destination is until somebody reviews the work or tidies the
# directory out of the way — so the read is re-run, and says the same thing the second time.
queued=$($G queue 2>&1)
if ! printf '%s' "$queued" | grep -q "LANDED UNREVIEWED"; then
  echo "  FAIL: the report went quiet on its own, so it was never about the work"; FAILED=1
else
  echo "  ok: the finding does not expire, and the fix it prints is a read"
fi
[ -z "$(git for-each-ref refs/git-pair)" ] \
  && echo "  ok: with three landings done, the repository holds no git-pair ref anywhere (PRD §13.4)" \
  || { echo "  FAIL: a landing wrote a ref: $(git for-each-ref refs/git-pair)"; FAILED=1; }

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
# The branch the work was done on is where a reader arrives by habit. It answers the same way before the
# tidy below and after it: there is no changeset there to check, because the destination already carries the
# directory; the exit code is the one for "this command has no changeset to speak about", and the sentence
# names the landing and the read that goes and looks.
git switch -q tidied-landing
out=$($G check 2>&1); code=$?
if [ "$code" = 2 ] && printf '%s' "$out" | grep -qi "landed" \
   && printf '%s' "$out" | grep -q "status --changeset tidied-landing"; then
  echo "  ok: the branch the work was done on says it landed, before the tidy"
else
  echo "  FAIL: check on the source branch before the tidy exited $code: $out"; FAILED=1
fi
git switch -q main
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
out=$($G status --changeset tidied-landing --json 2>&1); code=$?
if [ "$code" = 0 ] && printf '%s' "$out" | grep -q '"landed": *true'; then
  echo "  ok: the machine surface still says landed, from the directory's new home"
else
  echo "  FAIL: status --json after the tidy exited $code: $out"; FAILED=1
fi
git switch -q tidied-landing
out=$($G check 2>&1); code=$?
# The message comes from the no-changeset-on-this-branch path, which is where a branch whose directory has
# been tidied away arrives: the directory is no longer here, so the answer is the one that names the
# destination as the holder of the work. Asserting the string `check` prints when a branch *does* carry the
# changeset would pass for the wrong reason and fail the moment the two paths are untangled again.
if [ "$code" = 2 ] && printf '%s' "$out" | grep -q "the integration branch already holds it"; then
  echo "  ok: the branch the work was done on refuses it as landed after the move too"
else
  echo "  FAIL: check on the source branch after the tidy exited $code: $out"; FAILED=1
fi
git switch -q tidy-up
git switch -q main
git branch -q -D tidy-up tidied-landing

step "queue is empty again"
$G queue | sed 's/^/  /'

printf '\n'
if [ "$FAILED" = 0 ]; then echo "E2E: all checks passed"; else echo "E2E: FAILURES PRESENT"; exit 1; fi
