# feat-hooks-author-points

## Summary

Both sides of the author's four write points now run hooks: `pre-`/`post-ready`, `-unready`, `-abandon`, and
`-integrate`. A pre-hook stands in front of a transition and refuses it; a post-hook stands after it and
reports it. The second half is the one that needed designing, because git-pair does not unwind a lifecycle
marker once it is written — so a failure after the fact has to be visible without being able to take anything
back.

This is the second milestone of the approved lifecycle-hooks plan (`docs/plans/lifecycle-hooks/plan.md`),
stacked on the hook runner in `feat-hooks-runner`.

## What changed

-   `internal/hooks/hooks.go` — `RunAll` beside `Run`, `Result.Ran()`, and the active set now naming the eight
    author points.
-   `internal/cli/hooks.go` — `runHooks` split into `runPre` and `runPost` over a shared `runPoint` with an
    explicit `runMode`; `firstFailure`, `executedHooks`, and `refuseIfHooksDirtiedTree` (which `change ready`
    now uses too, instead of its own inline copy).
-   `internal/cli/change.go` — `runChangeUnready` and `runChangeAbandon` wired; `post-ready` wired into
    `runChangeReady`; `postHookError`, `stateAfterPoint`, `warnIfHooksMovedHead`.
-   `internal/cli/integrate.go` — `pre-integrate` after the gate, `post-integrate` after the marker, and the
    `hooks` array beside `reasons` in that command's JSON.
-   Docs: PRD §11.4, the README command table, and the skill's `cli.md`.
-   Tests: `internal/cli/hooks_points_test.go` asks the same four questions of each of the four transitions,
    plus the idempotent-run rule and the tracked-root trust rule at a non-ready point.

## Design decisions

**Post-hooks run to completion; pre-hooks stop at the first failure.** On the way into a transition the point
is to prevent something, so the suite that fails at minute one should not be followed by the next suite. On
the way out there is nothing to prevent, and a notification hook should not be silenced because the coverage
hook before it failed. The two behaviours are `hooks.Run` and `hooks.RunAll` rather than a flag, because they
are not two settings — they are two different questions.

**`runMode` is a named type, not a bool.** The mode decides three things at once: whether execution stops at a
failure, whether a failure becomes a refusal, and what the caller owes the reader. A `true` at a call site
communicates none of them, and getting it backwards silently turns a notification hook into a gate.

**A failing post-hook exits nonzero, and its message carries the other half.** The exit code is what makes
automation see it; the message is what stops a person drawing the wrong conclusion from it. The plan asked for
both, and the wording states the state the changeset is in and says nothing was undone:

```text
git-pair: booking-transaction is READY, but the post-ready hook "01-notify" failed with exit 1 — the
marker stands and nothing was undone
```

An author who is told only "failed" assumes the marker was not written and looks for the command to re-run —
and re-running `change ready` on a changeset that is already `READY` is the wrong next step. **This corrects
M1's PRD paragraph**, which said a post-hook failure "does not change the exit code". That sentence contradicted
the plan's reporting section, and the plan is right: a failure nobody's pipeline can see is a failure nobody
fixes.

**Nothing reverses, because nothing may.** PRD §26 lists the git verbs git-pair does not invoke, and `reset`
is one of them. A post-hook that could roll a published verdict back would put a write in an agent's hands
that the exit-code contract does not account for, and would make `change ready` atomic in a way git-pair is
not otherwise atomic anywhere.

**A command that writes nothing fires no hooks.** `change unready` on a changeset that was never offered,
`change abandon` on one already abandoned, and `change integrate` on a head already declared all succeed and
record nothing. A point fires when a lifecycle event happens, and a re-run of a finished command is not an
event. The alternative is a notification hook that fires on every retry, which is the first thing a person
switches off — with the switch that turns all hooks off.

**The clean-tree re-check costs nothing where there are no hooks.** `refuseIfHooksDirtiedTree` asks
`executedHooks` first, so a repository with no hooks configured does the work it always did. Without that
guard every author command would pay an extra `git status` for a feature it may not have. A hook that was
skipped for trust, or refused before starting for want of the executable bit, changed no file, and is not
counted.

**The HEAD-move warning names the hooks that ran, not the one that committed.** A hook is a child process that
can run any git command, and git-pair does not watch the index between them, so "which of these did it" is not
a question this command can answer. Naming the set and the marker they invalidated is the honest version, and
it is the set the author has to read anyway.

**`pre-integrate` runs after git-pair's own gate.** The gate is cheap and reads commits; a repository's suite
is expensive and reads the working tree. Running the suite first would make the slow check the loud one and
would print a hook failure over a refusal `check` already knew the answer to. The ordering is the same as
`change ready`'s: built-in conditions, then the repository's.

**Trust is a property of the root, not of the point.** One test puts a tracked hook at `post-unready` to keep
that true: the same consent rule, and the same silence until somebody reads the script, at every point.

## Validation

-   `go build ./...` clean; `gofmt -l internal cmd` empty.
-   `go test ./internal/cli/` green, which includes `internal/hooks` and the four-point table: per transition,
    a pre-hook refusal writes no marker and names the hook and the point; a post-hook failure exits nonzero
    with the marker still standing; a post-hook that commits is noticed by name; and every hook after a
    failure still runs. Plus the JSON carrying both sides in order, the post-hook context naming the marker as
    head, the three idempotent runs firing nothing, and the tracked-root rule at a non-ready point.
-   `go test ./internal/hooks/` green, including the active-point table, which is the assertion that `git pair
    hooks` cannot lie about what this build runs.
-   `go test ./internal/cli/ -run Docs` and `-run TestTheCommandTreeIsTheCommandSet` green.
-   `mise run check` exited 0. On the implementation commit it reported **36 test shards ok, 1 served from
    cache**, no `FAIL`, and the gate's own `gofmt -l internal cmd` step reported nothing — almost nothing was
    cached, which is what changing `internal/hooks` and `internal/cli` should do. Re-run on every tree since —
    this file, and the branch restacked twice onto M1's answers to the reviewer (the two questions, then the
    `trust_state` work that came out of the vendoring discussion) — each time 36 shards ok, no `FAIL`, `gofmt`
    silent.
-   The reviewer's questions reach this milestone as a fact rather than a change. Hooks run sequentially and the
    run stops at the first blocking result, which is what `runPre` relies on to skip the rest of a suite; and
    the trust state changes what `git pair hooks` reports, not when a hook fires. Both of M1's documentation
    rows collided with its new trust-state wording during the restack, in `README.md` and
    `references/cli.md`; the resolution carries both texts, so the row states the point set and the three
    acceptance states.

## Known limitations

-   The gate and the reviewer's points are not wired: `pre-check`, `post-check`, `pre-submit`, `post-submit`
    are configured-but-inert, and `git pair hooks` reports them as such. M3 and M4 run them.
-   There is still no results store, so `git pair hooks` reports configuration rather than the last result of
    each hook, as in M1.
-   `executedHooks` is the guard that keeps the clean-tree re-check cheap, and it is a heuristic in one narrow
    place: a hook that exits 0 is assumed to have been able to write files, and a hook that never started is
    not. A hook that `chmod`s a file without changing tracked content is not detected as the cause of a dirty
    tree; it is refused as one, which is the safe direction.
-   The HEAD-move warning compares HEAD before and after the whole post-hook group. It cannot say which hook
    committed, and it does not claim to.

## Open questions

-   Whether a post-hook failure should also be visible in `status` and the queue, or whether the exit code and
    stderr are the whole contract. Nothing here persists it.
-   M1 asks the same question about an untrusted tracked tree refusing outright once a repository has committed
    hooks. It stays open.
