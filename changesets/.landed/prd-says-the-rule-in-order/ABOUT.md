# The documents say the rule the code now runs

## Summary

Four passages still described `parent:` as the link an author writes, the PRD gave two of the four ways out the
refusal prints, and nothing stopped an example from going stale again. This closes all three, and adds the cost
measurement the plan asked for: `check --json` on a stacked branch, counted from outside the binary.

## What this changes

- **PRD §5** (the file's own description of a stacked pair, and the example above it) and **`# 21`** (the worked
  stack example and the paragraph under it) now give `base:` beside `base-changeset:` as what is written. Both say
  the older names are read and never written, and why: `parent:` and `base:` naming the same branch twice would give
  one question two answers, so that file is an error to correct, and a silently rewritten file would say the
  relationship changed when it did not.
- **README**'s stacked-branch non-goal and its `CHANGESET.yaml` paragraph, for the same reason and with the same
  sentence about the older keys.
- **PRD §9.8** prints all four ways out, in the order the error prints them: the two commands, then
  `git restore --source=<ref> -- changesets/<id>`, then `git fetch`. The order is the error's, and the reason for it
  is recorded with it - the record that makes a branch true comes before the edits that make it true.
- **`TestStackedExamplesShowTheWrittenPair`** (`internal/cli/docs_contract_test.go`) fails the build when a fenced
  block assigns `parent:` or `parent-changeset:` without also assigning `base-changeset:`. Prose is out of scope on
  purpose: a reader of a file written before M4 has to be told what they are looking at, and an example is the thing
  a person copies. An unclosed fence is also a failure, because every block after it would go unchecked.
- **`scripts/gates/ci-integrate.sh`** has a cost step: 69 checks, up from 66.

## The measurement

Taken with a shim placed ahead of git on `PATH`, so the count is what the binary asked for rather than what the code
appears to do. `check --json`, four branches cut from each other:

| levels below | invocations |
|---|---|
| 0 | 25 |
| 1 | 43 |
| 2 | 52 |
| 3 | 61 |

The first hop costs nine more than the hops after it, and that is the honest shape rather than an accident to fix:
below one level there is nothing to look for, while at two the code has a parent to find, so the reads that locate a
changeset on another branch run for the first time. Each further hop is nine calls - a tree read of that level, two
landedness probes (the directory and its `.landed` tombstone), a branch probe, and the log that reads the level's
markers. The gate pins the two-level count against a ceiling and pins the equality of successive hops, because a
change that re-walked the chain per level would grow quadratically and would otherwise arrive as a slow CI job nobody
investigates.

## Evidence

| Break | Result |
|---|---|
| stale `parent:` example appended to the PRD | `TestStackedExamplesShowTheWrittenPair` names the file, quotes the block, and says the example cannot be produced by any command; removed after |
| cost ceiling tightened to 20 | the two-level check fails printing `43 measured` |
| nine invocations added to one level only | the successive-hops check fails, and the ceiling does not - the two assertions look at different things |

`mise run gates`: 22 packages, `E2E`, `PTY`, `CI-INTEGRATE: all checks passed (69 checks)`.

## Deviations from the plan

- Two deliverable references were stale: §13.1 is *The destination* (the selection order is §4's, and §4 was
  rewritten in M5, so that deliverable needed no edit), and the refusal prints four ways out where the deliverable
  said three. Both are recorded in the plan's M9 status rather than quietly reworded out of it.
- The plan expected the invocation check to be linear from the first hop. It is linear from the second, and the step
  says so with the measured numbers instead of asserting a shape that the first hop does not fit.

## Not included

- No change to how the stack is read. The count is recorded so a future change has to argue with a number; changing
  the reads is a different piece of work with its own evidence.
- The PRD's other `parent:` mentions, which are prose about landing and approval, are left alone. The new test
  deliberately does not read prose.
