# Close the lifecycle-hooks plan

## Summary

The lifecycle-hooks plan is done: six milestones, each its own changeset, each reviewed before it landed, the last
of them (`feat-hooks-declined-state`) in `main` as `d4c8853`. This changeset carries nothing but the closing.

Two things happen here, both documentation:

- `docs/plans/lifecycle-hooks/` moves to `docs/plans/completed/lifecycle-hooks/`, with its two research files,
  following where the other finished plans live.
- The plan gains a **Closure** section: what shipped in six milestones, what the plan deliberately left undone,
  and the one number a later change has to beat.

No behaviour changes. Nothing outside the plan directory is touched.

## What changed

`docs/plans/completed/lifecycle-hooks/plan.md` — the move, plus:

- **Landed list** completed: M1 `9abcfc7`, M2 `c77633e`, M3 `29f49e8`, M4 `6768da6`, M5 `984f797`, and M6 recorded
  by approval and declaration rather than a landing hash it does not have yet — this changeset merges after it.
- **Closure** says what the feature is in one list, so a reader who finds the directory months from now does not
  have to reconstruct it from six ABOUT files: the runner and its execution contract, the twelve points, consent by
  content with four states and a per-clone record each, the repository's conditions inside the verdict, and the
  position asked where work starts and again at the gate.
- **What the plan left undone** is written as deliberately as what it did: no native-git-hook bridge, no shared
  reviewed statement of a declined tree, no `status`/`queue` mark for a clone that has recorded nothing, and a
  decline that records the list was shown rather than read. Each of those was a non-goal or an open question in a
  milestone, not an oversight, and the reason is given where the item is.
- **The number to beat**: the stack-cost gate measures 49 git invocations at two levels against the 50 allowed, and
  the trust read is a filesystem walk through the run's memoized `Resolution`. Stated in the plan because the
  obvious way to grow the feature — asking git for the hook state — is the one that would break the gate.
- The M6 heading and its spike row now describe what shipped after the review round (`init`, `change ready` and
  `review submit` ask too; `change unready`, `change abandon` and `git pair hooks` stay free), rather than only the
  verdict-level mandate first proposed.

## Design decisions

**The plan moves rather than gets deleted.** A completed plan is the record of why the code is shaped the way it
is — the rejected options, the measurements, the constraints a later contributor would otherwise rediscover. The
`completed/` directory is where the repository already puts them.

**Archived changeset records keep the old path.** `changesets/feat-hooks-runner/ABOUT.md` and four others point at
`docs/plans/lifecycle-hooks/plan.md`, which no longer exists. They are left alone: each ABOUT records what that
changeset did at the time it did it, and the same precedent holds for the plan moved earlier — the archived records
of `feat-landed-parent-advice` still name `docs/plans/landed-parent-restack-advice/plan.md`. Rewriting history into
the archive would make the archive say something that was never written.

**Written against the stack, merged on top of `main`.** The closure was drafted while M6 was still in review,
stacked on `feat/hooks-declined-state` so the plan could record the milestone as approved and declared rather than
leave the reader with a gap. M6 landed as `d4c8853`, so the branch was rebased onto `main` and the closure names
the landing hash instead — which is what the stack was for: the closing text could be written, reviewed and
corrected while the milestone it describes was still moving.

## Validation

- `mise run gates` at hand-off, green: 358 assertions across the suite, the e2e scripts and the PTY walkthrough
  passed, the CI integration script passed its 90 checks, and the invocation-cost gate measured **49 against the 50
  allowed** — the same figures `feat-hooks-declined-state` left, which is the expected result for a documentation
  move.
- The docs contract tests (`internal/cli/docs_contract_test.go`) keep the command tree, the command names in the
  docs, and the README transcripts honest; none of them reads `docs/plans/`, and they are reported as run rather
  than assumed.
- Every `docs/plans/` path in the moved plan was checked to exist after the move, and the two research files are
  named by the plan with their new relative location.

## Known limitations

- The archived changeset records point at the pre-move path, by the decision above.

## Open questions

- Should `docs/plans/completed/` gain an index? Six finished plans now sit in a directory whose names are the only
  thing a reader can search, and the plans themselves are the record of decisions the PRD does not repeat.
