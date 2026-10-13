# feat-hooks-check-gate

## Summary

`git pair check` now runs the repository's own scripts as part of the verdict. A `.git-pair/hooks/pre-check/`
suite that fails makes the gate refuse — with `ready: false` and a reason naming the hook — so
`git pair check --json | jq -e '.ready and .integrating'` remains the one command and the one answer a
pipeline reads, and the repository's conditions do not have to be a second job whose result someone joins to
it afterwards.

This is the third milestone of the approved lifecycle-hooks plan (`docs/plans/lifecycle-hooks/plan.md`),
stacked on `feat-hooks-author-points`.

## What changed

-   `internal/cli/check.go` — `pre-check` runs before the gate and `post-check` after the verdict is built;
    both contribute reasons; `hooks` added to `check`'s JSON beside `reasons`.
-   `internal/cli/hooks.go` — `hookReasons`, the wording that says who refused.
-   `internal/hooks/hooks.go` — the two gate points activated; `cmd.WaitDelay` on the hook spawn.
-   Docs: PRD §11.4 gains the gate subsection; the README gains a CI example and updates the `check` and
    `hooks` rows; the skill's `references/cli.md` names the check points.
-   Tests: `internal/cli/hooks_check_test.go`.

## Design decisions

**At the gate, a hook's failure is a condition rather than a refusal.** Everywhere else a failing hook refuses
an operation. `check` performs no operation — it assembles an answer — so the honest thing a hook can do there
is contribute a reason. That keeps the CI contract unchanged in shape, which is the property that makes this
usable: a job that already reads `ready` and `integrating` learns about the suite without being rewritten.

**Both points join the verdict, including `post-check`.** A post-hook beside a marker cannot reverse the
transition it follows, and does not try. A post-hook beside `check` has nothing to reverse, so its failure is
a condition like any other. The invariant kept in both places is that the exit code and `ready` never answer
different questions — `ready: true` beside a failing hook is the one shape a pipeline cannot use, because the
two halves disagree.

**The hooks run before the gate, and all of them run.** `check` is a report, and its value is that one run
names every reason the merge would be wrong; skipping the suite because the gate already had a reason would
put a round trip in per problem. This is the opposite of the order `change integrate` uses — its own cheap
conditions first, then `pre-integrate` — and the difference is that integrate is an action with an expensive
step to protect. That is why the points are attached to the command rather than to the gate function.

**`change integrate` runs neither gate point.** It runs the gate as the same code. Firing the gate's hooks
there would start the repository's suite twice in one author action, and a suite that takes six minutes would
take twelve with nothing in the output explaining why. The test observes this rather than counting
subprocesses: a `pre-check` hook records that it ran, `change integrate` must not produce the record, and
`git pair check` must — so the assertion is about the path, not about a hook that never fires anywhere.

**The reason names who refused.** `the pre-check hook "01-suite" failed with exit 1` is deliberately not
shaped like any condition the gate derives from history. A reader who takes a script's failure for a claim
about their markers goes looking for a commit that is not missing. `hooks[]` beside `reasons` carries the same
event with its status, exit, and duration, so a log line does not have to be parsed to find out which script
ran for how long.

**No override, and no invented one.** `change ready` and `review submit` carry an `--allow-*` flag for the
judgement of the person standing at them; `check` carries none, for the reason §19.3 gives for the absence of
`--allow-feedback` in its own list. A test asserts that `check --allow-hook-failure` exits **2** — an unknown
flag is a usage error, not a refusal — so the flag cannot be half-added and quietly ignored.

**`next_action` is cleared when a post-hook drops the verdict.** It was written for a gate that passed, and a
reader who stops at `next_action` would be told to merge a change the same response refuses.

**`cmd.WaitDelay` is a correctness fix, not a tuning knob.** `exec.Cmd.Wait` waits for the captured pipes to
close as well as for the process, and a hook that starts a background process and then exits leaves the write
ends held by the process it abandoned. A `sleep 30` behind a one-second deadline therefore cost thirty
seconds, which turns a deadline into a suggestion. Five seconds of drain, then the pipes close and whatever
arrived is what the report carries.

## Validation

-   `go build ./...` clean; `gofmt -l internal cmd` empty.
-   `go test ./internal/hooks/ ./internal/cli/ ./internal/hygiene/` green, run in full (121s). The new cases:
    a failing `pre-check` refuses the gate with itself as the only reason, keeps its output off stdout and on
    stderr, and the human form exits 1 under `NOT READY:`; a passing hook leaves the verdict alone and is
    reported; the gate's own conditions and the hook's are named in the same run; a timeout says "deadline"
    rather than "failed"; an unaccepted tracked hook does not refuse the gate; an invented override flag is a
    usage error; a failing `post-check` sets `ready: false` and clears `next_action`; both points appear in
    the array in the order they ran; and `change integrate` runs neither point while `check` runs its own.
-   `go test ./internal/cli/ -run Docs` and `-run TestTheCommandTreeIsTheCommandSet` green.
-   The README's CI example was exercised against a fixture repository rather than written from the JSON's
    shape. The committed hook, `git pair hooks --trust`, the refusal, the reason wording and the `hooks` entry
    are the output that run produced, including `check --json` exiting 0 with `ready: false` and the hook's log
    on stderr only.
-   `mise run check` exited 0: 36 test shards ok, 22 served from cache, no `FAIL`, and its `gofmt -l internal
    cmd` step reported nothing. Re-run after this file was written, with the same result.
-   Restacked onto M1's answers to the reviewer's feedback, twice — once for the two questions, once for the
    `trust_state` work that came out of the vendoring discussion — and re-run each time: exit 0, 36 shards ok,
    no `FAIL`, `gofmt` silent. Neither answer touches this milestone's behaviour: a hook that joins the verdict
    does so whatever the reason it did not run was, and the trust state is what `git pair hooks` reports rather
    than when a point fires. What did reach this branch is documentation — M1's two rows gained the three
    acceptance states, and the restack carried both texts forward.

## Known limitations

-   `pre-submit` and `post-submit` remain configured-but-inert until M4; `git pair hooks` reports them as such.
-   A tracked `pre-check` hook that nobody has accepted does not refuse the gate. Consent is not a merge
    condition — a fresh clone must still get a verdict — but it means the suite a team committed does not run
    in a clone that has not accepted it, and `check` says so on stderr rather than treating the absence as a
    pass.
-   `WaitDelay` closes the pipes five seconds after the deadline. Output still in flight at that moment is
    truncated in the report, not lost silently: the status is already `timeout`, and the reason names the
    deadline.
-   There is still no results store, so `git pair hooks` reports configuration rather than the last result of
    each hook.

## Open questions

-   Whether `LANDED UNREVIEWED`-style reporting should ever mention a hook result. Nothing here persists one.
-   M1's question stays open: should a repository that has committed tracked hooks refuse outright in a clone
    that has not accepted them, instead of proceeding with a note?
