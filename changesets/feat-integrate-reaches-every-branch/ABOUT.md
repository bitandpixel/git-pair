# The integrate trigger reaches every branch, and a gate check keeps it there

## Summary

`git-pair integrate` woke for CI runs on `feat/**` and `feature/**` only. The allowlist is gone, and the gate now
fails a build if a branch filter comes back, because the failure it causes is invisible from inside the job.

## What this changes

`.github/workflows/git-pair-integrate.yml`: the `branches:` list under `workflow_run:` is removed, and the comment
says why there is no filter. What decides whether to merge is the gate inside the job - a review, a declaration, and
a green run on the exact commit the declaration names. A branch filter in the trigger is a second, weaker copy of
that rule, and the weaker copy fails closed: nothing is declined loudly, the job simply is never told to look.

`scripts/gates/ci-integrate.sh`: two new checks on the trigger block, and one new merge step. 60 checks become 66.

The branch is named `feat/...` on purpose. `workflow_run` is evaluated against the copy of the workflow file on the
default branch, so this change takes effect for the changesets that follow it and not for itself - landing it through
the mechanism it widens is the only route that needs no manual dispatch.

## Evidence

The defect, from the day it was noticed: `docs-base-changeset-collapse` was approved, declared, and its CI run on
the declared head completed green at 18:39:14. No integrate run was created, then or later. The `*/15` poll that is
supposed to catch a missed event had not run since 15:07 - scheduled workflows are best-effort on GitHub's side, so
the fallback is not a safety net.

| Check | Result |
|---|---|
| `a changeset under a branch name the trigger used to exclude` - approved, declared, green on `docs/notes` | merged, destination moved |
| `the trigger carries no branch allowlist to exclude a changeset from landing` | passes with the filter removed |
| the same check with the allowlist put back into the YAML | **FAIL**, and the run goes red |
| `and no denylist taking its place` | passes; fails on a `branches-ignore:` the same way |
| the existing 60 checks | unchanged and green |

The two halves are worth telling apart. The merge step passes with or without the allowlist, because the job script
has never filtered on a branch name - it is there to state the fact the fixture used to prove by accident, through
branches named `flow/*`. Only the trigger checks can catch the regression that actually happened, and they are the
reason this changeset exists. They read the block between `workflow_run:` and the next trigger, with comment lines
dropped, so the prose above can explain the rule without tripping the assertion.

## Not included

- No PRD change. The PRD describes what `change integrate` promises; it never described this repository's runner, and
  the trigger's branch list was never named there.
- No change to `scripts/ci/git-pair-integrate.sh`. Its gate is the thing the trigger should have deferred to, and it
  already refuses anything unreviewed, undeclared, or unproven.
- No fix for the schedule. A `*/15` cron that GitHub may or may not run is not something a repository can repair, and
  the comment now says so where someone would look for it.
