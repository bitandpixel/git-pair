#!/usr/bin/env bash
# The automatic merge, in the shape a CI job runs it.
#
# Usage:
#   scripts/ci/git-pair-integrate.sh [-n] [-r] [-b <ref>] [-R <remote>] [branch...]
#
#   no arguments   every changeset `git pair queue --json` reports as awaiting integration
#   branch...      just these branches (the changeset is whatever the branch carries)
#   -n, --dry-run  say what would happen and write nothing outside this clone
#   -r, --require  a gate refusal is an error instead of a skip (use for a hand-run of one branch)
#   -b, --base     the integration branch: passed to git-pair as --default-branch, and the branch to
#                  merge into when the changeset's own destination names none (default
#                  $GIT_PAIR_DEFAULT_BRANCH, else main)
#   -R, --remote   the remote to fetch from and to push the merge to (default origin)
#   -c, --require-ci
#                  do not merge until the head is proven green by --ci-probe. Without it the job asks
#                  git-pair's gate and nothing else, which is right for a repository whose CI is somebody
#                  else's business and wrong for one that has tests on the branch.
#   --ci-probe <cmd>
#                  the command that answers for a commit: it is handed the sha, prints why on one line, and
#                  exits 0 green, 1 not green, 2 cannot tell (default scripts/ci/gh-head-green.sh, used
#                  when GITHUB_REPOSITORY is set)
#   --ci-request <cmd>
#                  the command that asks for the destination to be tested after a merge: it is handed the
#                  destination and the merge's sha, prints what it did on one line, and exits 0 requested,
#                  1 refused, 2 cannot tell (default scripts/ci/gh-request-ci.sh, used when
#                  GITHUB_REPOSITORY is set)
#   --expect-head <sha>
#                  merge nothing but this head. A trigger that was told "CI finished with this commit" names
#                  it here, so a branch that moved since cannot be merged on the strength of a run that
#                  tested something else.
#
# The exit code is 0 when every changeset considered was merged, was already handled, or was legitimately
# not ready — a job triggered by a push runs before anybody has declared anything, and a red build for
# "nothing to do yet" trains people to ignore the build. A head that is merely not green yet is the same
# kind of nothing-yet, so it is a skip too. It is 1 when a merge or a push failed, and 2 for a bad
# invocation. `--require` turns the refusals into errors, which is what a hand-run of one branch wants.
# A merge that reaches the destination needs one thing after it: a request that the destination's new tip be
# tested, because the push that landed it came from this job's own token and GitHub creates no runs for
# events a token caused. The destination's tree remains the whole record of the landing.
#
# Why the sequence is in this order (PRD §29):
#
#   check --json            the gate. `ready` is "may this merge", `integrating` is "did the author ask
#                           for one", and the job needs both: the first alone merges approved work the
#                           moment it is approved, the second alone merges a request whose approval has
#                           since been rewritten, withdrawn, or answered.
#   ci probe              with --require-ci, the head has to be green before anything is merged. The gate
#                         above answers "has this been reviewed and handed over", which is a question about
#                         the review; whether the tests pass is a question about the forge, and it is asked
#                         of the commit the declaration names rather than of the branch, so the answer
#                         cannot be about a head that arrived while this job was starting.
#   merge --no-ff           ordinary git, run by whoever owns the destination — here the job, on the
#                           owner's behalf, because the author's declaration asked for exactly that.
#   push <destination>      the landing exists when the destination branch says it does, and the pushed
#                           branch's tree is then the whole record of it (PRD §13.4). Nothing follows this
#                           push except the request below: no ref to write, nothing to publish, and no second
#                           command for anybody to run. If the push fails, the landing did not happen.
#   request the tests       after a push, ask the forge to test the destination's new tip. The push came from
#                           the runner's own GITHUB_TOKEN, and GitHub makes no runs for events a token
#                           caused, so the destination's own push trigger cannot fire for a merge this job
#                           performed — and the merge is a different tree from the head the probe cleared
#                           whenever the destination moved in between. A request that cannot be made (no
#                           forge, no hook) is reported and stays green, which is the shape of a local run;
#                           a request the forge refuses is an error under --require and a loud note without
#                           it, because a landing nobody tested is worth a reader's attention either way.
#
# This is an example, not the product: git-pair still merges nothing, and nothing here is a git-pair
# subcommand (PRD §26). It is the merge a repository's owner chooses to run on their own branches.
set -uo pipefail

DRY=0
REQUIRE=0
REQUIRE_CI=0
EXPECT_HEAD=${GIT_PAIR_CI_EXPECT_HEAD:-}
BASE=${GIT_PAIR_DEFAULT_BRANCH:-main}
REMOTE=${GIT_PAIR_CI_REMOTE:-origin}
GP=${GIT_PAIR_BIN:-git-pair}
HERE=$(cd "$(dirname "$0")" && pwd)
PROBE=${GIT_PAIR_CI_PROBE:-}
REQUEST=${GIT_PAIR_CI_REQUEST:-}
BRANCHES=()

# The local name the destination is checked out under while the merge is made. It is deliberately not the
# destination's own name: the job must not be on the branch it is about to push, and for a stack the
# destination can be trunk while a branch checked out under that same name is the thing being merged.
BASE_LOCAL=git-pair-ci-destination

while [ $# -gt 0 ]; do
  case "$1" in
    -n|--dry-run) DRY=1 ;;
    -r|--require) REQUIRE=1 ;;
    -b|--base) [ $# -ge 2 ] || { printf '%s needs a ref\n' "$1" >&2; exit 2; }; BASE=$2; shift ;;
    -R|--remote) [ $# -ge 2 ] || { printf '%s needs a remote name\n' "$1" >&2; exit 2; }; REMOTE=$2; shift ;;
    -c|--require-ci) REQUIRE_CI=1 ;;
    --ci-probe) [ $# -ge 2 ] || { printf '%s needs a command\n' "$1" >&2; exit 2; }; PROBE=$2; shift ;;
    --ci-request) [ $# -ge 2 ] || { printf '%s needs a command\n' "$1" >&2; exit 2; }; REQUEST=$2; shift ;;
    --expect-head) [ $# -ge 2 ] || { printf '%s needs a sha\n' "$1" >&2; exit 2; }; EXPECT_HEAD=$2; shift ;;
    -h|--help)
      # The help is the header above, printed rather than duplicated. Reading the block to its end keeps a
      # new flag from silently falling off the help text, which a fixed line range does.
      awk 'NR > 1 && /^set -uo pipefail/ { exit } NR > 1 && /^#/ { sub(/^# ?/, ""); print }' "$0"
      exit 0 ;;
    -*) printf 'unknown option %s\n' "$1" >&2; exit 2 ;;
    *) BRANCHES+=("$1") ;;
  esac
  shift
done

if ! command -v "$GP" >/dev/null 2>&1; then
  printf 'no git-pair on PATH as %s (build it, or set GIT_PAIR_BIN)\n' "$GP" >&2
  exit 2
fi
if ! command -v jq >/dev/null 2>&1; then
  printf 'this job reads git-pair JSON, and needs jq\n' >&2
  exit 2
fi
# The merge is a commit, and a commit needs an author. A runner usually has `git config user.*` unset, and
# "who merged this" should read as the job rather than as whatever identity the image happens to carry.
git config user.name  >/dev/null 2>&1 || git config user.name  "git-pair-ci"
git config user.email >/dev/null 2>&1 || git config user.email "git-pair-ci@localhost"

say()  { printf '%s\n' "$*"; }
note() { printf '  %s\n' "$*"; }

# declared_branches is the poll shape: the queue is the list of things somebody has already asked for, so
# a scheduled run needs no event to act on. Each row names a branch and the head its declaration covers.
#
# It needs local branches — only a branch can be queued — which is why the driver below detaches and then
# fetches refs/heads/* into refs/heads/*.
declared_branches() {
  "$GP" queue --json --default-branch "$BASE" 2>/dev/null |
    jq -r '.awaiting_integration[]? | .branch'
}

# ci_green asks the probe about one commit and turns its three answers into something this job can act on.
#
# The probe's own line goes into the log, because "why" is the half a reader needs and the probe is the only
# thing that knows it. "Cannot tell" is its own answer and not a pass: the default probe exists only where a
# forge is named, and `--require-ci` with no probe merges nothing, which is the direction that sends somebody
# to the configuration rather than to a merge.
ci_green() {
  local sha=$1 out rc probe=()
  if [ -n "$PROBE" ]; then
    read -r -a probe <<<"$PROBE"
  elif [ -n "${GITHUB_REPOSITORY:-}" ] && [ -x "$HERE/gh-head-green.sh" ]; then
    probe=("$HERE/gh-head-green.sh")
  fi
  if [ ${#probe[@]} = 0 ]; then
    note "no --ci-probe given, no forge named: nothing can say whether ${sha:0:7} is green"
    return 2
  fi
  out=$("${probe[@]}" "$sha" 2>&1) || rc=$?
  printf '%s\n' "$out" | sed 's/^/    /'
  return "${rc:-0}"
}

# request_ci asks the forge to test the destination's new tip. It runs after a push and only after a push:
# a merge that stayed in this clone tests nothing, and a destination that already held the work was tested
# when it landed. The hook answers the way the probe does — 0 asked, 1 refused, 2 cannot tell — so one reader
# of the log learns both halves from the same three words.
#
# It is not the merge's verdict and it does not become one. The landing already happened, and the case where
# nothing can be asked is the ordinary case for a run outside a forge, so rc 2 is a reported fact rather than
# a red build. A refusal is a fact with a reason attached: `--require` makes it an error, which is what a
# hand-run of one branch wants, and without it the note says in plain words that the tip is unproven.
request_ci() {
  local dest=$1 sha=$2 out rc hook=()
  if [ -n "$REQUEST" ]; then
    read -r -a hook <<<"$REQUEST"
  elif [ -n "${GITHUB_REPOSITORY:-}" ] && [ -x "$HERE/gh-request-ci.sh" ]; then
    hook=("$HERE/gh-request-ci.sh")
  fi
  if [ ${#hook[@]} = 0 ]; then
    note "no --ci-request given, no forge named: nothing will be asked to test $dest's new tip"
    return 2
  fi
  out=$("${hook[@]}" "$dest" "$sha" 2>&1) || rc=$?
  printf '%s\n' "$out" | sed 's/^/    /'
  return "${rc:-0}"
}

# integrate_one <branch>: the whole handoff for one declared changeset.
#
# Every value it acts on comes from git-pair rather than from a guess: the head from the gate that cleared
# it, the destination from the queue rather than from `base:` (for the child of a landed parent the base is
# the parent's landing commit, and a commit is not a branch anything can merge into), and the changeset id
# from the gate too, because a stacked child's source carries its parents' directories as well.
integrate_one() {
  local branch=$1 cs src decl dest head_now merge subject rc

  if ! git show-ref --verify --quiet "refs/heads/$branch"; then
    note "skip: no local branch $branch"
    return 0
  fi
  # One branch, one command: -B points the local branch at the remote's tip and checks it out, so there is
  # no stale local copy to merge and no `reset --hard` to get wrong.
  git checkout -q -B "$branch" "$REMOTE/$branch" || { note "cannot check out $REMOTE/$branch"; return 1; }

  local check ready integrating
  if ! check=$("$GP" check --json --default-branch "$BASE" 2>&1); then
    # A gate that will not answer is not a gate that said no, and the difference decides whether the build
    # is red. The case a job meets here is a branch whose changeset has already landed: `check` calls that
    # "no changeset for this branch", which is true of the branch and not a problem with the work. The queue
    # tells the two apart — a branch it does not list as awaiting integration is one this job was never
    # asked to merge — so the words go into the log and the run stays green. A branch the queue does list,
    # where the gate still refuses to answer, is a repository problem, and stays red.
    if ! declared_row "$branch"; then
      note "$branch: nothing to merge here — the queue lists no declaration for it"
      printf '%s\n' "$check" | sed 's/^/    /'
      return 0
    fi
    note "gate failed to answer for $branch:"
    printf '%s\n' "$check" | sed 's/^/    /'
    return 1
  fi
  cs=$(printf '%s' "$check" | jq -r '.changeset // empty')
  src=$(printf '%s' "$check" | jq -r '.head // empty')
  ready=$(printf '%s' "$check" | jq -r '.ready')
  integrating=$(printf '%s' "$check" | jq -r '.integrating')
  if [ -z "$cs" ] || [ -z "$src" ]; then
    note "skip: check did not name a changeset for $branch"
    printf '%s\n' "$check" | sed 's/^/    /'
    return 1
  fi

  if [ "$ready" != true ] || [ "$integrating" != true ]; then
    # The ordinary case for a push-triggered job: work is here, the request is not, or the gate says no.
    note "$cs: not merging (ready=$ready integrating=$integrating)"
    printf '%s' "$check" | jq -r '.reasons[]? | "    - " + .'
    if [ "$REQUIRE" = 1 ]; then
      return 1
    fi
    return 0
  fi

  dest=$(declared_destination "$cs")
  [ -n "$dest" ] || dest=$BASE

  # The branch is a moving thing and this job reads it twice. If it moved between the gate and the queue,
  # the merge below would land a head the gate never cleared: stop, and let the next event try again.
  head_now=$(git rev-parse HEAD)
  if [ "$head_now" != "$src" ]; then
    note "$cs: the branch moved while this job looked (gate said ${src:0:7}, now ${head_now:0:7}) — try again"
    return 1
  fi

  # A trigger that says "CI finished with this commit" names the commit. If the branch's declaration covers a
  # different one, the run being trusted tested other work — the ordinary case of an author pushing while a
  # job starts, so it is a skip and the next event decides rather than a red build for a race.
  if [ -n "$EXPECT_HEAD" ] && [ "$EXPECT_HEAD" != "$src" ]; then
    note "$cs: CI finished with ${EXPECT_HEAD:0:7} and the branch declares ${src:0:7} — nothing tested to merge"
    return 0
  fi

  # Reviewed and handed over is the gate's answer. Whether the work passes its tests is a different question,
  # asked of the forge about the commit the declaration names — not of the branch, so the answer cannot be
  # about a head that arrived while this job was starting.
  if [ "$REQUIRE_CI" = 1 ]; then
    local green=0
    ci_green "$src" || green=$?
    if [ "$green" != 0 ]; then
      if [ "$green" = 1 ]; then
        note "$cs: not merging — the head is not green"
      else
        note "$cs: not merging — nothing proves the head green"
      fi
      if [ "$REQUIRE" = 1 ]; then
        return 1
      fi
      return 0
    fi
  fi

  decl=$(printf '%s' "$check" | jq -r '.integrate_commit // empty')
  say "$cs: declared by ${decl:0:7} for merge into $dest"

  if [ "$DRY" = 1 ]; then
    note "dry run: would merge --no-ff ${src:0:7} into $dest and push $REMOTE/$dest"
    return 0
  fi

  # The destination, at its remote's tip. `-B` again rather than a pull: the job must merge what the
  # destination actually holds, not what this clone last happened to have.
  git checkout -q -B "$BASE_LOCAL" "$REMOTE/$dest" || { note "cannot check out $REMOTE/$dest"; return 1; }
  if git merge-base --is-ancestor "$src" HEAD; then
    # Nothing to merge: the destination already carries the work. `git pair status --changeset $cs` reads
    # that landing out of the branch, and `git pair change tidy $cs` moves the directory aside when the
    # destination's owner wants the room.
    note "$cs: $dest already holds ${src:0:7} — already landed, nothing to merge"
    return 0
  fi

  subject="Merge $branch into $dest"
  [ -n "$decl" ] && subject="$subject (declared by ${decl:0:7})"
  if ! git merge --no-ff --no-edit -m "$subject" "$src"; then
    # A conflict is the author's or the reviewer's to resolve, never a job's to guess at: put the branch
    # back the way it was and let the red build say so.
    git merge --abort >/dev/null 2>&1
    note "$cs: the merge conflicted against $dest — nothing merged"
    return 1
  fi
  merge=$(git rev-parse HEAD)
  note "merged as ${merge:0:7} against ${dest}"

  if ! git push -q "$REMOTE" "HEAD:refs/heads/$dest"; then
    # The landing is the pushed branch. A merge that stayed in this job's clone is not one, and there is
    # nothing here to leave half-written: no ref, no record, no second remote to reconcile.
    note "$cs: the push to $dest was refused — the landing did not happen"
    return 1
  fi
  note "$cs: pushed to $dest — the destination's tree is the record of the landing"

  # The push came from this job's own token, so the destination's push trigger will not fire for it; the
  # request is where the tip gets its tests. It is asked here, where the destination and the merge's sha are
  # both known, rather than by a step after this script that would have to re-derive both.
  request_ci "$dest" "$merge"; rc=$?
  case $rc in
    0) ;;
    2) note "$cs: landed, and nothing was asked to test $dest — its tip is unproven" ;;
    *)
      note "$cs: landed, and the request to test $dest was refused — its tip is unproven"
      if [ "$REQUIRE" = 1 ]; then
        return 1
      fi
      ;;
  esac
}

# declared_row is the queue's read-only statement that somebody asked for this merge: it has a row for the
# branch in `awaiting_integration`. It answers the question the gate cannot when the gate will not answer,
# and it is the same read `declared_destination` uses.
declared_row() {
  "$GP" queue --json --default-branch "$BASE" 2>/dev/null |
    jq -e --arg b "$1" '.awaiting_integration[]? | select(.branch == $b)' >/dev/null
}

# declared_destination asks the queue where the author asked this to land, which is the branch a landed
# parent landed on rather than the `base:` a stacked child's own yaml names. Empty answers mean "the queue
# had no row for it" and the caller falls back to --base.
declared_destination() {
  "$GP" queue --json --default-branch "$BASE" 2>/dev/null |
    jq -r --arg cs "$1" '.awaiting_integration[]? | select(.changeset == $cs) | .destination' |
    head -1
}

# A queue reads local branches — only a branch can be reviewed, so only a branch can be queued — and a CI
# clone holds remote-tracking refs instead. Detach first so the fetch below can never be refused for
# updating the checked-out branch, then bring every branch down as a branch.
git checkout -q --detach HEAD 2>/dev/null
# Twice, in this order. The first fetch is the one whose refs the job trusts for the destination
# (`$REMOTE/$dest` must mean what the remote says, not what this clone was created with); the second turns
# those same branches into local ones, because a queue only lists local branches. They cannot be one fetch:
# a refspec into refs/heads/* leaves refs/remotes/$REMOTE/* where it was.
if ! git fetch -q "$REMOTE" "+refs/heads/*:refs/remotes/$REMOTE/*"; then
  printf 'fetching %s failed\n' "$REMOTE" >&2
  exit 1
fi
if ! git fetch -q "$REMOTE" "+refs/heads/*:refs/heads/*"; then
  printf 'fetching %s failed\n' "$REMOTE" >&2
  exit 1
fi
# Nothing else is fetched. git-pair reads a landing out of the branches themselves, so a job that has the
# branches has everything (PRD §13.4).

if [ ${#BRANCHES[@]} -eq 0 ]; then
  while IFS= read -r b; do
    [ -n "$b" ] && BRANCHES+=("$b")
  done < <(declared_branches)
fi

FAILED=0
for branch in ${BRANCHES[@]+"${BRANCHES[@]}"}; do
  say "### $branch"
  integrate_one "$branch" || FAILED=1
done
exit "$FAILED"
