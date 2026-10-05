# fix-pty-gate-verdict

## Summary

`scripts/gates/pty-walkthrough.sh` could print a failure and exit 0. It found one today —
`FAIL: tiny never quit; the harness killed it` — printed it, then said `PTY: all checks passed` and
returned success. Every `mise run gates` and every CI run since the file acquired the pattern was reporting
on twelve scenarios it could not fail on.

Found while chasing something else (the pinned-version installer bug); it is the more serious of the two,
because it is the file that decides whether everything else counts.

## What changed

One file, `scripts/gates/pty-walkthrough.sh`, three defects:

- **A subshell cannot reach the parent's `FAILED`.** Twelve scenarios run inside
  `( COLS=… ROWS=…; session … )`, because the terminal they must be painted in is not the one this script
  runs in. `session` reports through `fail()`, which sets `FAILED=1` in the subshell's copy. `fail()` now
  also writes a marker file — the one channel a subshell has back to the parent — and the verdict reads
  it before deciding.
- **The keep-transcripts-on-failure trap was overwritten.** Line 32 installed the trap the file's own
  comment promises; line 37 replaced it with `trap 'rm -rf "$T"' EXIT`. bash replaces an EXIT trap rather
  than appending, so a failed run deleted the transcripts that comment was written to preserve — which is
  why today's failure could not be reproduced. One trap now, and it is the only place `$T` is removed.
- **A self-test**, as the first step: raise a failure the way those twelve scenarios raise one, assert it
  reaches the verdict, and assert the probe did not contaminate the run it was proving.

`fail()` ends in an `if` rather than `[ -n … ] && : >…` because the last command's status is the
function's status, and a `fail` that returns 1 turns every caller's continuation into a question about
`set -e`.

## Design decisions

**A file, not a redesign.** The alternatives were running the scenarios through a function that returns a
status (every caller in a subshell would still need to propagate it), or exporting results through
`$( … )` (the scenarios write transcripts and need their side effects). The marker is three lines, matches
the harness's existing style, and is the only channel that exists across a `fork()`.

**The self-test is isolated on purpose.** It points `GATE_FAILMARK` at its own path inside the subshell,
so the deliberate failure lands where the probe can check it and nowhere else, and asserts
`FAILED = 0` afterwards. Without that second assertion the probe would be a check that guarantees the
verdict by writing to it.

**The other three gate scripts were left alone.** `e2e-29.sh`, `ci-integrate.sh` and `install-sh.sh` have
zero subshell-wrapped check groups, which is why they report correctly today. They each carry their own
copy of `ok`/`fail`; when a scenario there first needs a `( … )`, the marker pattern comes with it. Lifting
the helpers into a shared file is the better long-term answer and a larger change than this.

## Validation

- Injecting `fail "injected: …"` into the `tiny` scenario's own subshell: **before**, exit 0 with
  `PTY: all checks passed`; **after**, `PTY: FAILURES PRESENT`, exit 1, and
  `PTY: transcripts kept for inspection in /tmp/git-pair-pty.EFcgxh` with the marker file present in it.
- Clean run of the fixed script: exit 0, self-test both lines ok.
- `mise run gates` on this branch: green.
- The failure that prompted this (`tiny` killed by the harness) did not recur on the re-run, consistent
  with what `ci.yml` says about the walkthrough on a loaded machine. It is now a scenario that can prove
  itself when it does — with transcripts kept to read.

## Known limitations

- The self-test proves the marker mechanism, not that every scenario's assertions are worth anything. A
  scenario that checks the wrong string still passes.
- The three helper duplication copies (`ok`/`fail` in four scripts) remain. This change is in one of them.
- The `tiny` scenario's underlying behaviour — the preview overlay at 30 columns not quitting on `q` —
  is unexplained. It has been seen once and passed on re-run. If it recurs, the transcripts now kept are
  the artifact to start from, and it becomes its own changeset.

## Open questions

- Should `check`/CI assert that the gate scripts agree on one helper implementation, the way
  `docs_contract_test.go` asserts the prose and the command tree agree? The duplication is how one script
  drifted into silence.
- Should the transcripts be uploaded as a CI artifact on failure rather than left in `/tmp` on a runner
  that is about to be discarded? Today's failure would have been reviewable instead of re-runnable.
