# feat-gate-scripts-pipefail

## Summary

The three gate scripts ran with `set -o pipefail`, and that option made their own assertions fail at random.
`grep -q` leaves the moment it matches, the writer is left holding a closed pipe, and the pipeline element is a
subshell - which takes the default SIGPIPE disposition back - so the pipeline reports 141 and the check fails on
output that contains the pattern it was looking for. They now run with `set -u`.

## What changed

-   `scripts/gates/e2e-29.sh`, `scripts/gates/ci-integrate.sh`, `scripts/gates/pty-walkthrough.sh`: the option
    line, with the reason beside it. No assertion was edited, and no check was added, weakened, or removed.

## Evidence

A 511-byte `$out` taken from one of these failures, evaluated with the script's own idiom - the two-grep chain
that failed:

```
$ bash -c 'set -uo pipefail; ...'   # 2000 iterations, payload holds both patterns
  chain failed 4 times of 2000      # pipeline status 141
$ bash -c 'set -u; ...'             # 4000 iterations, same payload
  chain failed 0 times of 4000
```

About 140 checks in `e2e-29.sh` are that shape, so ~0.2% per check is roughly one spurious failure in four runs.
Two were observed in one session, and both printed the text their own grep had just refused to find:

-   `FAIL: deleting the branch lost the changeset:` followed by JSON carrying `"landed": true` and
    `"chain_base": "12a7d6d"`, the two strings that check greps for.
-   `FAIL: status said only that the branch has no changeset:` followed by output whose next line is
    `LANDED UNREVIEWED`.

## Design decisions

**Change the option, not the 50 call sites.** The here-string form (`grep -q PATTERN <<<"$out"`) is the usual
fix and is correct, but it means rewriting 55 lines of the file that gates this repository's releases to
correct an option, and the pipe is not what is wrong here. One line per script is reviewable in one screen and
cannot change what any check asserts.

**`{ printf '%s' "$x" || true; } | grep -q P` does not fix it.** Measured: still 141. The signal kills the
subshell before `|| true` is reached.

**The other scripts that set `pipefail` were left alone.** `scripts/ci/gh-head-green.sh` and
`scripts/ci/git-pair-integrate.sh` have no shell pipelines of this shape - their `|` characters are jq filters
and `case` patterns - and `scripts/test-sharded.sh` greps a file, so its writer cannot be left on a closed
pipe.

## Validation

-   `bash -n` on all three scripts.
-   The loop above: 0 failures in 4000 iterations after the change, 4 in 2000 before, same payload.
-   Each gate script run to completion, and `mise run gates` reported below.

## Known limitations

-   The idiom still costs a subshell per check, and a writer that fails for a real reason is now invisible. That
    is the trade `set -u` makes, and it is cheap here because every writer is a `printf` of a variable in
    memory.
