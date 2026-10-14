# feat-ci-integrate-candidate-shape

## Summary

The integrate job now triggers on the push that carries the declaration, runs this repository's own ladder
against the merge candidate, and pushes only if it passes. The `*/15 * * * *` poll is gone, the `workflow_run`
wait is gone, `ci.yml` no longer runs on every branch push, and the merge job asks for `contents: write` alone.

Two things were true before and are not now: a merge was performed on the strength of a check run that had
tested the *branch head*, not the tree being pushed; and the queue was polled 96 times a day to catch
declarations whose event had been missed, whether or not anything was waiting. The declaration is a push, so it
can carry its own trigger.

The driver flag this uses, `--verify-cmd`, is the layer below (`feat-ci-verify-cmd`).

## What changed

-   `.github/workflows/git-pair-integrate.yml`: triggers are `push: '**'` and `workflow_dispatch`. The job's
    `if:` is an allowlist — dispatch, or a push to a non-destination branch whose commit message carries
    `Review-State: integrating`. `permissions:` is `contents: write` for the merge and `actions: write` for the request that follows it. The
driver is invoked with `--verify-cmd 'env -u GIT_PAIR_DEFAULT_BRANCH mise run gates'` and `--expect-head` from
`github.event.after`. The build step names the
    binary it gates with through `GIT_PAIR_BIN`, and the queue report stays under `if: always()`. Its header
    separates the rule worth copying — a landing conditional on the suite the destination would run — from this
    repository's value at that seam, `mise run gates` and `jdx/mise-action`, because this file ships as the
    sample and the two are easy to confuse. It also asks the destination to test what landed — `--ci-request`
    with the shipped helper named, `actions: write` for that request alone, and a token for `gh` — added on
    review (`cac6104`) so a landing leaves a run on the trunk that can go red and notify somebody.
-   `.github/workflows/ci.yml`: `pull_request`, `workflow_dispatch`, and `push` for the destination branch
    alone. The branch pushes went, because their only justification was the probe reading branch-head checks
    and nothing does that now. Its header now says what the branch trigger was for, why that reason is gone,
    and why trunk is kept.
-   `scripts/gates/ci-integrate.sh`: the static section that asserted the two files name each other (6 checks)
    now asserts what this shape needs (27), and the check that required `actions: write` became a pairing — an
    ask only with its scope, a scope only with its ask — beside positive checks that the ask, its scope, and
    its token are all present.
-   `README.md`, `PRD.md` §29's pointer, and `skills/git-pair/references/integration.md`: the places that
    stated the landing facts as "approved, declared, and green on the declared head", and the sentence that
    said the merge job triggers on `workflow_run` and the schedule.

## Design decisions

**The filter is in the job's `if:` because GitHub bills a job that starts.** The number of started jobs is what
the spending limit measures. A guard step that exits early still costs the minute a guard `if:` saves, and a
separate guard job costs a whole one — which was proposed and rejected during the design of this shape. That
is also why nothing here runs on a schedule: a timer-triggered run has to re-derive from the queue what the
declaration already states, and pays for the privilege 96 times a day.

**One accepted over-match.** Any push whose message *quotes* `Review-State: integrating` starts a job here.
The driver reads the marker out of the branch's own history, finds nothing to merge, and the job is inert. This
is not hypothetical: in the sibling repository, run `38477131713` started a merge job for a commit whose body
quoted the trailer. A step cannot replace the `if:` — the minute is spent once the job has started — so the
over-match is documented in the file rather than fixed.

**`ci.yml` stays, and is not folded into this file.** Deleting it was the obvious move (the ladder would then
live in one file) and it failed on `scripts/ci/gh-request-ci.sh`: the ask that follows a merge dispatches a
workflow *by name*, and its default is `ci.yml`. With that file gone, the only dispatchable workflow left would
be the one that merges, so "please test the destination's new tip" would become "please land a changeset".
Keeping two files with one job each also keeps the read-only token on the job that only reads.

**The probe's scopes went with the probe; the request's came back.** `checks: read` and `statuses: read`
belonged to `--require-ci`, and a job that tests its own candidate has no head-check to consult, so they stay
dropped. `actions: write` belongs to the request that follows a landing, and this repository wants that
request: the trunk's tip is the tree the job just tested, so the run adds no answer, but a trunk with no run
of its own has nothing to go red and nobody to notify when the merge is where the trouble was. That is a
paper trail bought with one scope, and the token is the part that fails quietly — `gh` with nothing to
authenticate with answers "cannot tell", the driver says so and stays green, and the trail simply is not
there. A token asks for what the job uses, which is why the pairing is asserted in both directions rather than
one.

**The candidate's environment is the merge environment, and that is a new coupling.** The first declaration
this shape saw on a runner (`38607720677`) refused its own merge, and the tree was fine. The step exports
`GIT_PAIR_DEFAULT_BRANCH` because the driver needs to know the destination, and the candidate inherits the
step's environment — so the suite ran with that variable set, and with it set the product says where its
destination came from. Several tests assert that a verdict carries no such tool line, so they failed, and the
job did the only honest thing with a candidate whose suite fails: it said so and pushed nothing. `env -u
GIT_PAIR_DEFAULT_BRANCH` on the verify command separates the two — the driver keeps the variable, the suite
sees the environment it was written against. Nothing here was wrong with `main` plus this branch; what the
combination exposed was the job definition, which is precisely the class of thing a head-check cannot see, and
it took 56 seconds on a runner rather than a trunk that looked green because nobody ran it.

**The build step names its binary.** `mise run build` installs under the name `scripts/install-name.sh`
derives from the branch, so a replay cannot quietly test a build another worktree left in the shared
`~/.local/bin`. On a runner nothing is shared, but the name is still the branch's — so the plain `git-pair` the
driver would fall back to does not exist. `GIT_PAIR_BIN` says which binary the driver and the report should
ask, and it is the one the candidate was tested with.

**Retry guidance is written into the file** because it is the kind of recovery a person reaches for by
instinct: `gh run rerun`, never `change unready` then `change ready`, which withdraws the approval the merge is
gated on and rebuilds it.

## Validation

`bash scripts/gates/ci-integrate.sh` → `CI-INTEGRATE: all checks passed (122 checks)`, against 90 on `main`
and 105 on the parent layer. `mise run gates` → `E2E: all checks passed`, `PTY: all checks passed`,
`CI-INTEGRATE: all checks passed`, exit 0 in 135s — run in a scratch worktree at the restacked tip (`018b4a9`)
so the checkout under review was not moved. A scratch worktree needs `MISE_TRUSTED_CONFIG_PATHS=<worktree>`:
without it mise refuses an unseen config file and the ladder fails at zero seconds with what reads like a
build error.

The environment finding is reproducible in three commands, and the third is the fix:

```bash
GIT_PAIR_DEFAULT_BRANCH=main go test ./internal/cli -run TestChangeIntegrateRefusesWorkThatWasNeverApproved  # FAILS
go test ./internal/cli -run TestChangeIntegrateRefusesWorkThatWasNeverApproved                                # passes
GIT_PAIR_DEFAULT_BRANCH=main sh -c 'env -u GIT_PAIR_DEFAULT_BRANCH go test ./internal/cli -run \
  TestChangeIntegrateRefusesWorkThatWasNeverApproved'                                                          # passes
```

The static checks are text checks, and each one is there for a failure that would otherwise be silent: a
branch allowlist on the trigger (evaluated before anything git-pair says, so it can exclude a whole changeset
family while every job stays green), a `schedule:` or `workflow_run:` coming back, an `if:` that admits a
`pull_request` (which has no write token to merge with) or matches the subject line instead of the trailer, a
scope the job does not use, `--require-ci` returning beside `--verify-cmd` with no rule for which one merges,
a shallow checkout that would answer "no review" about reviewed work, a cancellable merge, and the workflow
`gh-request-ci.sh` names not existing or being able to merge.

Both files were also parsed with `python3 -c 'import yaml; yaml.safe_load(...)'`, which found a real defect the
gate cannot: `run: "${GIT_PAIR_BIN:-git-pair}" queue` is a quoted scalar with trailing text, and the gate has no
YAML parser by design. Worth knowing that a syntactically broken workflow passes `mise run gates`; the first
runner to see one is the first to complain.

## Known limitations

-   **What a runner has now proven, and what it has not.** Minutes came back partway through this stack, and
     three pushes to this branch started runs of *this* file — `38596941248` (head `df92077`), `38597587466`
     (head `6c4959f`), `38603795241` (head `018b4a9`) — each ending with job `integrate` **skipped**, because
     none of those heads carried the declaration trailer. That is the filter measured on a runner rather than
     asserted: a push starts a run, the `if:` costs it nothing, and `ci.yml` started nothing for those pushes
     either. The old shape charged three `mise run gates` runs for the same three pushes.
   - **The merge path is now proven, on a candidate that should fail.** The first declaration (`ab87732`)
     started run `38607720677` from this branch's copy of the file: the job ran rather than being skipped, the
     candidate was built from the destination's tip plus the declared head, `--verify-cmd` executed against it,
     the run said `feat-ci-integrate-candidate-shape: the merge candidate failed verification — nothing merged
     into main`, and `origin/main` stayed at `8c8f267`. The queue report printed afterwards, which is the
     `if: always()` step doing its job over a red run. The same head under the old shape would have merged:
     its checks were green, because `ci.yml` runs the suite in an environment without the variable that broke
     it.
   - Still unproven: that `contents: write` alone is enough to land a merge, and that the post-merge request
     reaches `ci.yml` — both live behind a candidate that passes, which is what the `env -u` above is meant to
     deliver. `gh run view <id> --log` is what settles each.
   - Layer 1 was landed by the shape this one replaces, which is the expected order: `main` still carried the
     old file when the declaration was pushed, so run `38598069537` (`workflow_run` on `ef5fa30`, 16:26:20)
     walked the queue and produced `b5f54a4` at 16:26:42 with the probe's `--require-ci` in force.
-   The `if:` over-match above, accepted.
-   The destination's new tip *is* the tested candidate, so trunk is not untested after a merge; what it lacks
    is a run record on trunk, because a token-caused push creates none. `--ci-request` is how that record is
    asked for, and this file asks for it: `--ci-request` with the helper named, `actions: write` for the
    request, and `GITHUB_TOKEN` for `gh`. A refused ask is reported and does not undo the landing; the failure
    mode the three together are asserted against is the quiet one, where no token means nothing was ever asked.
-   Trunk keeps a `push` trigger in `ci.yml` for commits pushed straight to `main`, which have no candidate run
    behind them. It costs a run only for human pushes, since a token-caused push creates none — but it is one
    more line that names the destination branch, because a trigger cannot read `vars`. A repository moved off
    `main` edits it in two places, and the comment there says so.
-   Two declarations for the same destination are serialised per ref, not per destination (`concurrency`
    groups on `github.ref`). Pre-existing and unchanged.
-   **The gate's stack-cost guard has almost no slack, and it is not this change.** The paragraph in
    `scripts/gates/ci-integrate.sh` records 25/43/52/61 `git` invocations for a stack one to four levels deep,
    measured 2026-08. `main` and both layers here measure **29/49/58/67** — checked by running `main`'s own
    copy of the gate against the same binary, so the drift is pre-existing and neither layer moves it (no Go
    changes). `STACK_CEILING=50` now sits one invocation above the measured 49: the next change that adds a
    read to `check` fails a build for a number 6 higher than the guard's own paragraph predicts.
    `bash scripts/gates/ci-integrate.sh` reproduces it. Re-recording someone else's measurement belongs in its
    own changeset, not here.

## Open questions

-   Should this repository pass `--ci-request` after all, so a merge leaves a trunk run behind? Answered on
    review (`cac6104`) — *“I like the idea of a github-native paper trail and notifications in case something
    goes red. let's add it”* — and this layer implements it.
-   The same question now belongs to every repository adopting the profile, not to each file. `bitandpixel/dev-profiles`
    carries its own integrate workflow, which does not ask today. Whether the answer lives in that file or in
    the profile as house policy for the GitHub family is the open half of the vendoring discussion, and it is
    easier to settle once the profile can carry forge-specific policy at all.
-   Once this ships, `bitandpixel/dev-profiles` carries a second copy of the driver and its own replay of it.
    Deleting that copy depends on a released binary new enough to have `--verify-cmd`, so the removal belongs
    after the next tag.
