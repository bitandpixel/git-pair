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
#   -R, --remote   the remote to push the merge to and publish the refs through (default origin)
#   -c, --require-ci
#                  do not merge until the head is proven green by --ci-probe. Without it the job asks
#                  git-pair's gate and nothing else, which is right for a repository whose CI is somebody
#                  else's business and wrong for one that has tests on the branch.
#   --ci-probe <cmd>
#                  the command that answers for a commit: it is handed the sha, prints why on one line, and
#                  exits 0 green, 1 not green, 2 cannot tell (default scripts/ci/gh-head-green.sh, used
#                  when GITHUB_REPOSITORY is set)
#   --expect-head <sha>
#                  merge nothing but this head. A trigger that was told "CI finished with this commit" names
#                  it here, so a branch that moved since cannot be merged on the strength of a run that
#                  tested something else.
#
# The exit code is 0 when every changeset considered was merged, was already handled, or was legitimately
# not ready — a job triggered by a push runs before anybody has declared anything, and a red build for
# "nothing to do yet" trains people to ignore the build. A head that is merely not green yet is the same
# kind of nothing-yet, so it is a skip too. It is 1 when a merge, a push or a record failed, and 2 for a bad
# invocation. `--require` turns the refusals into errors, which is what a hand-run of one branch wants.
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
#   push <destination>      the landing exists when the destination branch says it does.
#   integration record      the only git-pair ref write, and deliberately after the push: a record of a
#                           landing that failed to travel is a claim about a merge nobody can reach. The
#                           recorder verifies `--commit` is in `--target` and is the commit that added
#                           changesets/<id>/ there, which makes it the check that the merge did what this
#                           job believes it did.
#   integration publish     the pair to the shared remote, unforced, before anything gets tidied.
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

# integrate_one <branch>: the whole handoff for one declared changeset.
#
# Every value it acts on comes from git-pair rather than from a guess: the head from the gate that cleared
# it, the destination from the queue rather than from `base:` (for the child of a landed parent the base is
# the parent's integration ref, and a ref is not a branch anything can merge into), and the changeset id
# from the gate too, because a stacked child's source carries its parents' directories as well.
integrate_one() {
  local branch=$1 cs src decl dest head_now merge subject

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
    note "dry run: would merge --no-ff ${src:0:7} into $dest, push $REMOTE/$dest, record, publish"
    return 0
  fi

  # The destination, at its remote's tip. `-B` again rather than a pull: the job must merge what the
  # destination actually holds, not what this clone last happened to have.
  git checkout -q -B "$BASE_LOCAL" "$REMOTE/$dest" || { note "cannot check out $REMOTE/$dest"; return 1; }
  if git merge-base --is-ancestor "$src" HEAD; then
    # Nothing to merge. The changeset is in the destination and unrecorded — the queue calls that
    # LANDED, UNRECORDED, and `integration record` is the command to run, by hand, with the SHAs.
    note "$cs: $dest already holds ${src:0:7}; record it with git pair integration record --changeset $cs"
    return 0
  fi

  subject="Merge $branch into $dest"
  [ -n "$decl" ] && subject="$subject (declared by ${decl:0:7})"
  if ! git merge --no-ff --no-edit -m "$subject" "$src"; then
    # A conflict is the author's or the reviewer's to resolve, never a job's to guess at: put the branch
    # back the way it was and let the red build say so.
    git merge --abort >/dev/null 2>&1
    note "$cs: the merge conflicted against $dest — nothing merged, nothing recorded"
    return 1
  fi
  merge=$(git rev-parse HEAD)
  note "merged as ${merge:0:7}"

  if ! git push -q "$REMOTE" "HEAD:refs/heads/$dest"; then
    note "$cs: the push to $dest was refused — the merge stays local and NO record was written"
    return 1
  fi
  # The push updated $REMOTE/$dest locally, so the recorder can verify the landing against the destination
  # it is claiming, rather than against a branch this clone invented.
  if ! "$GP" integration record --changeset "$cs" --source "$src" --commit "$merge" \
       --target "$REMOTE/$dest" --default-branch "$BASE"; then
    note "$cs: the merge is in $dest and the record refused — the queue will call this LANDED, UNRECORDED"
    return 1
  fi
  if ! "$GP" integration publish "$cs" --remote "$REMOTE" --default-branch "$BASE"; then
    note "$cs: recorded locally, but the pair did not reach $REMOTE"
    return 1
  fi
  note "$cs: merged, recorded, published"
  return 0
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
# The records too, because a destination is read through them: where the child of a landed parent lands is
# the branch its parent landed on, and this job learns that from the parent's ref. A repository with no
# records yet has nothing to match, which git reports as a failure of the fetch rather than an empty one.
git fetch -q "$REMOTE" "+refs/git-pair/*:refs/git-pair/*" 2>/dev/null || true

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
