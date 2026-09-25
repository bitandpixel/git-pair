#!/usr/bin/env bash
# Run the Go tests with several `go test` processes instead of one.
#
# The suite is subprocess-bound. `internal/cli` and `internal/tui` drive the product by calling
# `cli.Execute` inside the test binary, so they also drive it through the process working
# directory and the process standard streams — which means every test in those packages waits for
# the one before it. Most of a test's span is a git subprocess running somewhere else, with the
# test process waiting for it.
#
# Splitting a package's tests across *processes* gives that overlap back without touching the
# harness: each process still runs its own tests one at a time, so the shared working directory and
# the swapped standard streams are exactly as safe as they are today. A shard is one `go test
# -run` regex over names read from `go test -list`, so this script keeps no list of its own and
# cannot drift from the suite.
#
# Small packages are not worth splitting — a shard pays the binary start-up like any other
# `go test` — so a package is sharded only when it carries at least SHARD_MIN_TESTS tests.
#
# usage: scripts/test-sharded.sh [-n SHARDS] [-P WORKERS] [PACKAGES...]   (default: ./...)
#        GIT_PAIR_TEST_SHARDS   shards per sharded package; 1 reproduces `go test ./...`
#        GIT_PAIR_TEST_WORKERS  shards running at once; defaults to one past the core count
#
# Output matches `go test ./...`: one line per package as it finishes, and the captured detail only
# for the packages that failed.
set -uo pipefail

SHARD_MIN_TESTS=${SHARD_MIN_TESTS:-40}

# --- one shard ---------------------------------------------------------------
# Called as `--jobline <id>\t<package>` by the job queue at the bottom of this file. The parent
# exports OUT_DIR so every shard writes beside every other one in a single directory, and a shard
# cleans nothing up: that is the parent's job.
#
# The shard's `-run` regex arrives as a file named after the job rather than as an argument. It
# runs to a few thousand characters, and an argument that long is one more thing that can be
# mangled between the queue and the process. A file named by job id cannot be.
#
# A shard buffers its output so that shards finishing at the same moment cannot interleave their
# reports, and prints only the `ok`/`FAIL` line go test would have printed. The captured detail is
# replayed by the parent, and only for a shard that failed.
if [ "${1:-}" = "--jobline" ]; then
  if [ -z "${OUT_DIR:-}" ]; then
    printf 'test-sharded: --jobline needs OUT_DIR from the parent\n' >&2
    exit 2
  fi
  IFS=$'\t' read -r id pkg <<<"$2"
  log="$OUT_DIR/$id.log"
  if [ -f "$OUT_DIR/$id.re" ]; then
    go test -run "$(cat "$OUT_DIR/$id.re")" "$pkg" >"$log" 2>&1
  else
    go test "$pkg" >"$log" 2>&1
  fi
  code=$?
  grep -E '^(ok[[:space:]]|FAIL[[:space:]]|\?)' "$log" | tail -n 1
  if [ "$code" -ne 0 ]; then
    { printf '\n===== %s [%s] =====\n' "$pkg" "$id"; cat "$log"; } >"$OUT_DIR/$id.fail"
  fi
  printf '%s\n' "$code" >"$OUT_DIR/$id.rc"
  exit "$code"
fi

# --- the job list ------------------------------------------------------------
cores=$(nproc 2>/dev/null || echo 2)
case $cores in (''|*[!0-9]*) cores=2 ;; esac
[ "$cores" -lt 1 ] && cores=1

# Shards are finer than workers on purpose: several small jobs pack cores better than a few large
# ones, because the queue can start a short package while a long shard is still running. Two per
# core measured best, and neither number is worth much past eight — a shard pays the binary
# start-up, and past a point the machine is scheduling shards instead of running tests.
n=${GIT_PAIR_TEST_SHARDS:-$(( cores * 2 ))}
case $n in (''|*[!0-9]*) n=2 ;; esac
[ "$n" -lt 1 ] && n=1
[ "$n" -gt 8 ] && n=8

# How many shards run at once, one past the core count: a good deal of the work is a git subprocess
# whose spawn the test process only waits for, and the gap that leaves is worth filling.
workers=${GIT_PAIR_TEST_WORKERS:-$(( cores + 1 ))}
case $workers in (''|*[!0-9]*) workers=1 ;; esac
[ "$workers" -lt 1 ] && workers=1
[ "$workers" -gt 8 ] && workers=8

pkgs=()
while [ $# -gt 0 ]; do
  case $1 in
    -n) shift; n=${1:-1}; [ "$n" -lt 1 ] && n=1 ;;
    -P) shift; workers=${1:-1}; [ "$workers" -lt 1 ] && workers=1 ;;
    -h|--help) sed -n '2,25p' "$0"; exit 0 ;;
    *) pkgs+=("$1") ;;
  esac
  shift
done
[ ${#pkgs[@]} -eq 0 ] && pkgs=(./...)

mapfile -t pkgs < <(go list "${pkgs[@]}") || exit 1

# The job queue re-invokes this file as the worker, so it needs a path that does not depend on the
# worker's working directory.
self=$(cd "$(dirname "$0")" && pwd)/$(basename "$0")

jobs_file=$(mktemp) || exit 1
out_dir=$(mktemp -d) || exit 1
trap 'rm -f "$jobs_file"; rm -rf "$out_dir"' EXIT
export OUT_DIR="$out_dir"

# Job ids double as file names in OUT_DIR, so they are a counter rather than the package path: a
# package path contains slashes, and a log named after it would be written into a directory that
# does not exist.
job=0
for pkg in "${pkgs[@]}"; do
  mapfile -t names < <(go test -list '^(Test|Example|Fuzz)' "$pkg" 2>/dev/null | grep -E '^(Test|Example|Fuzz)')
  total=${#names[@]}
  if [ "$total" -lt "$SHARD_MIN_TESTS" ] || [ "$n" -eq 1 ]; then
    # One job for the whole package, and no `.re` file — that is how the worker knows to run it
    # unfiltered. A package with no test files still gets its `go test` line, exactly as
    # `go test ./...` prints it.
    printf 'job%s\t%s\n' "$job" "$pkg" >>"$jobs_file"
    job=$(( job + 1 ))
    continue
  fi
  # Round-robin by name. Test names and test costs are unrelated here, which is what makes the
  # shards land within a few seconds of each other; sorting by anything would not help.
  for i in $(seq 0 $(( n - 1 ))); do
    re=""
    for j in "${!names[@]}"; do
      [ $(( j % n )) -eq "$i" ] && re="$re|${names[$j]}"
    done
    [ -n "$re" ] || continue
    printf '^(%s)$' "${re#|}" >"$out_dir/job$job-$i.re"
    printf 'job%s-%s\t%s\n' "$job" "$i" "$pkg" >>"$jobs_file"
  done
  job=$(( job + 1 ))
done

# --- run ---------------------------------------------------------------------
# One job per line and `-d '\n'`, so a field is never re-split on whitespace; the script calls
# itself as the worker, so the tab split happens in exactly one place.
xargs -a "$jobs_file" -d '\n' -P "$workers" -I '{}' bash "$self" --jobline '{}'
ran=$?

fail=0
for rc in "$out_dir"/*.rc; do
  [ -f "$rc" ] && [ "$(cat "$rc")" != 0 ] && fail=1
done
for f in "$out_dir"/*.fail; do
  [ -e "$f" ] && cat "$f"
done

if [ "$ran" -ne 0 ]; then
  printf 'test-sharded: xargs exited %s\n' "$ran" >&2
  exit "$ran"
fi
exit "$fail"
