# The verdict asks this clone for a position on the repository's own hooks

## Summary

A repository can commit scripts under `.git-pair/hooks/` and have them run at the lifecycle points. Until now a
clone that had recorded nothing about those scripts simply did not run them: the run said so on stderr and
`check` still answered ready. So a CI job reading `ready` could merge work the team's own suite never saw, and the
only signal was a line on a stream a machine is free to drop.

This changeset adds the missing position — `git pair hooks --decline`, "a person read these scripts and chose not
to run them here" — and makes the verdict ask for one. While the tracked hooks tree is `unrecorded` or `changed`,
`git pair check` refuses and `git pair change integrate` refuses with it, through the same gate, with a reason that
names both ways out. `accepted` passes because the scripts ran; `declined` passes because somebody answered a
question about them. What neither is, is silence.

The reviewer asked for this while reading the hook contract (`review 1af9b87`): leaning towards making the
check mandatory, with an explicit declined state as the escape hatch. The escape hatch is what makes the mandate
fair — without it, a gate that waits on approval asks every clone to either execute code it does not want or stay
red forever, and a team under a deadline executes the code.

Stacked on `feat/hooks-spec` (M5 of the lifecycle-hooks plan) because both move the same contract: this change
edits the PRD's hook section, the README, `references/cli.md` and the skill's hook reference that M5 wrote.

## What changed

`internal/hooks/trust.go` — the state machine.

- `declinedRecordPath` puts the record beside the acceptance: `<git-dir>/git-pair/declined-hooks`, same directory,
  same shape, same content hash. A decline is a fact about this clone, exactly like an acceptance, and it is
  keyed by the tree's hash for the same reason: the next version the repository commits is `unrecorded` again,
  not quietly exempt. That rule is the whole safety of the state.
- `TrustState` gains `TrustDeclined`, and `TrustAt` resolves the four states in one pass: acceptance matches the
  tree ⇒ `accepted`; the decline matches ⇒ `declined`; an acceptance exists but does not match ⇒ `changed`;
  otherwise `unrecorded`.
- `Decline` / `Declined` beside `Accept` / `Trusted`, with one shared `recordPosition` so that taking either
  position clears the other. A stale acceptance must not outrank a fresh decline, and a stale decline must not
  make accepted content read as `changed`.

`internal/hooks/hooks.go` — one trust read per run.

- `Resolution` memoizes the state, and `Discover` reads it from there. The state now decides two things at once —
  whether the tracked scripts run, and whether the gate calls the work ready — so a run that asked per hook point
  would walk the same directory for every point.

`internal/cli/hooks.go` — the position, and the report.

- `git pair hooks --decline` prints the list of scripts it is declining before recording them, for the same reason
  `--trust` prints what it approves: a position taken about content nobody has been shown is not a position.
  `--trust` with `--decline` is a usage error (exit 2); they are opposite answers about the same bytes.
- The report carries `declined` beside `accepted`, and `tree_hash` whenever the tree is not the accepted content,
  so a stale decline reads as "declined 3f1a…, the tree is now 82b0…" rather than as nobody having looked.
- The human report has three sentences for the three unrun states, and the skip note a run prints now distinguishes
  a decline from an absence. Telling a person who declined to accept what they just declined is the noise this
  avoids.

`internal/cli/check.go` — the verdict.

- `integrationGate` appends `hooksPositionReason`, so `check` and `change integrate` ask the same question and
  cannot drift apart. The reason names what is missing and both commands, because a refusal that names only
  `--trust` reads as demanding approval.

## Design decisions

**The mandate lives in the verdict, not in the report.** `change ready`, `review submit` and `status` keep
reporting the skip rather than refusing it: you can still work in a clone nobody has approved, still hand off,
still read the scripts you are being asked about. The gate is the one place where "the checks did not run" must
stop the action, because it is the one place an action is a merge.

**The record is per-clone, not tracked.** A tracked `declined` file would be inherited by the next clone, which
turns "this person read it and said no" into "this directory is exempt". The reviewer chose per-clone; the
consequence is spelled out under Known limitations.

**`accepted` and `declined` both clear the gate, for different reasons.** One ran the scripts, one is a stated
position about them. The gate is not asking for the checks to have run — it is asking for the clone to have
answered about them. That is why declining is not a way to get a script run, and the tests assert both halves:
declined content is skipped, and declined content does not block the declaration.

**The off switch is explicitly not a position.** `GIT_PAIR_HOOKS=0` and `git-pair.hooks=false` say a machine does
not want to run scripts right now; they say nothing about what the scripts do. The verdict refuses with them set,
because the quietest way past a gate should not be an environment variable nobody has to read.

**Agents take neither position.** `--trust` says "I read this script and I allow it to run on my machine", and
`--decline` says "I read this script and I am choosing not to run it here". Both sentences are about what a
person read, so the skill page now tells an agent to report the state, print what `check` refused with, and name
both commands.

## Validation

- `internal/hooks`: `TestDeclineStates` — the four states, the hash rule (a new version of declined content comes
  back `unrecorded`), the stale decline staying reportable, and the two records clearing each other in both
  directions.
- `internal/cli/hooks_gate_test.go`: `TestCheckAsksForAPositionOnTrackedHooks` — all four states at the gate in
  both the human and the `--json` shape, with the reason's wording asserted (what is missing, and both commands),
  and the off switch not lifting the refusal. `TestHooksDeclineCommand` — the `--decline` output, the JSON fields,
  `--trust` with `--decline` as exit 2, and a stale decline staying in the report after the tree moves.
- `TestTrackedRootAtTheIntegratePoint` rewritten to the new contract: this is the one place a tracked hook is
  ordinary reviewed content rather than a fixture. Unrecorded refuses the declaration and writes no marker;
  declined lets the declaration through with the hook still skipped; accepted runs the hook, which then blocks the
  declaration with no marker written.
- `mise run gates` at hand-off: the unit suites, the e2e scripts, the PTY walkthrough, the CI integration script,
  and the invocation-cost gate. The trust read is a filesystem read through the run's memoized `Resolution`, so no
  new git invocations are expected and the measured figure should stay where M4 left it (49 at two levels, 50
  allowed); the gate's number is quoted in the hand-off note either way.

## Known limitations

**A CI checkout is a clone that has recorded nothing.** From this change a pipeline that runs `git pair check`
must take a position in its own script — `git pair hooks --trust` is the usual line, since a runner that reports
the suite passed has to run it — and neither position is a line an agent should add on its own initiative. This is
safe to merge today because no repository in the wild has tracked hooks yet: the state is absent by vacuity
exactly where the tree is absent, which is every repository that never used the feature.

**Nothing proves the person read the scripts.** `--decline` records that the list was shown, not that it was
read. That is the same epistemic status `--trust` has had since M1, and the reason both print the list first: the
honest protection is that a person cannot take a position without seeing what the position is about.

**A decline is invisible to other clones, by design.** `status` and `git pair hooks` report it because they read
this clone; a verdict another clone reaches still cannot. If a team wants "this repository's hooks are declined on
runners" to be a shared, reviewed statement, that is a tracked file with its own review, and it is not in this
change.

## Open questions

- Should `status` and `queue` say more than they do now for a declined tree? They report the state through
  `git pair hooks`; neither nags, which is the point of a recorded position. A future `--decline` that carried an
  optional note (`--reason`) would make the position reviewable without making it tracked.
- Should `change ready` refuse too, so the author finds out at the hand-off rather than at the gate? It was left
  reporting because the hand-off is where a person reads the scripts, and refusing there pushes them to approve
  without reading.
