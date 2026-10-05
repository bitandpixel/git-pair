# fix-ci-prove-destination

## Summary

Merges performed by this repository's own integrate job reached `main` with no CI run attached to them. The
job proved the declared head green and then pushed a merge that nothing ever built. It now asks GitHub to run
CI on the destination after a push, and the gate replays that ask against a stub so the ask cannot disappear
while every run stays green.

## What changed

-   `scripts/ci/gh-request-ci.sh` (new): the forge hook. Dispatches the test workflow for a branch and answers
    `0` requested, `1` refused, `2` cannot tell, the same three answers `gh-head-green.sh` gives.
-   `scripts/ci/git-pair-integrate.sh`: `request_ci` runs after a successful push and only after one, with
    `--ci-request <cmd>` / `GIT_PAIR_CI_REQUEST` as the seam and the helper as the default when
    `GITHUB_REPOSITORY` is set. The header's claim that "nothing follows this push" now names the exception
    instead of hiding it.
-   `.github/workflows/git-pair-integrate.yml`: `actions: write`, with the reason next to the other scopes,
    and the step renamed to say what it does.
-   `scripts/gates/ci-integrate.sh`: a scenario that drives the job with a recording stub hook, plus three
    static checks on the things that could remove the ask silently — the scope, the helper's executable bit,
    and the default named in the script.

## Design decisions

**The job asks for the run; it does not wait to be triggered by it.** GitHub creates no workflow runs for
events that the runner's own `GITHUB_TOKEN` caused, which is the guard that stops a workflow triggering
itself forever. That is why both merges this evening landed untested: `ci.yml`'s `push: branches: '**'` never
saw them. A dispatch is a request rather than a caused event, so it gets through with the token the job
already has, and `actions: write` is the only new scope it needs. The alternatives were a PAT so the push
looks human (a stronger credential than the merge needs, and it makes automated commits appear authored by a
person), running the gate inside the merge job (proves the tree, leaves `main` with no check suite for its
own tip), or a schedule (finds drift late, per merge not at all).

**The ask is a hook, like the probe.** `--ci-request` mirrors `--ci-probe`: what belongs under test is the
job's decision to ask and the arguments it asks with, not GitHub's API. The default is chosen the same way the
probe's is, by `[ -x "$HERE/gh-request-ci.sh" ]` plus a named forge — which is exactly why the gate asserts
the bit: an unexecutable helper would disable the whole mechanism without failing anything.

**The request names the destination, and the sha is logged rather than sent.** The dispatch API takes a branch
or tag and answers HTTP 422 for a bare commit, so `--ref <merge-sha>` is not a thing that works. The run
covers whatever `main`'s tip is when GitHub gets to it, which is this commit unless another merge beat it
there; the line the hook prints carries the sha the caller wanted, so a reader compares instead of assuming.

**Asking is not the merge's verdict.** The landing already happened, so a missing forge (`2`) is reported and
the run stays green — that is what a local run looks like — and a refusal (`1`) says so in plain words, going
red only under `--require`, which is the flag a hand-run of one branch uses. Nothing here turns a merge into a
CI result.

## Validation

`bash scripts/gates/ci-integrate.sh`: 90 checks, all passing, 20 of them new. The five cases are asked-and-
landed (asserting the destination's name and the merge's own sha, read out of the stub's recording), landed-once
then-asked-once (a re-run pushes nothing and asks for nothing), cannot-tell (green, and says the tip is
unproven), refused (green, same words) and refused under `--require` (red), plus a dry run that asks for
nothing it did not make.

The checks were then proved able to fail: deleting the `request_ci` call, the `actions: write` scope, and the
helper's executable bit produced 11 failures and a red run (exit 1). That proof is the point of the scenario —
a fixture that only ever passes proves nothing, which is the lesson this repository has now taken twice.

`mise run gates`: see the run referenced in the review thread.

## Known limitations

-   The run is not bound to the merge's sha. It is bound to the destination's tip at the moment GitHub creates
    it, and the log carries the sha the job intended.
-   Nothing waits for the result. A red run on `main` is found by whoever opens it; this change makes the run
    exist, not the trunk blocking.
-   A push refused mid-flight has no fixture. `scripts/gates/ci-integrate.sh` covers a refused merge (the
    conflict scenario) and a refused ask, but not a `git push` that fails after the merge commit exists.
-   The ask is skipped silently-by-design outside a forge. It is reported in the log and asserted in the gate,
    but a repository with no `GITHUB_REPOSITORY` and no `--ci-request` gets no run and no red build.

## Open questions

-   Should `ci.yml` grow a `workflow_dispatch` input naming a commit to check out, so the run can be pinned to
    the merge's sha rather than to the branch tip?
-   Should `main` require the `gates` check? That is the step that turns "the run exists" into "an untested
    tip cannot become the base of the next changeset", and it is a repository setting rather than a file in
    this changeset.
