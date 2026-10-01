# A declaration outranks an inference when choosing the active changeset

## Summary

`choose` reads a changeset's `ignores:` before it applies the stack link. Today the stack link is applied first, so
a recorded link that names the parent as a changeset id removes the parent from the candidate list before the
parent's own declaration is consulted, and the branch is answered with the changeset the author said was merely
sharing it.

## What this changes

One reorder in `choose` (`internal/changeset/resolve.go`), plus the comment that states the order as the rule. The
`ignores:` pass runs first; the stack-link pass runs on what the declaration left. Both passes keep their existing
guard against emptying the candidate list, so a pair of changesets that name each other still resolves to a
reported ambiguity rather than to nothing.

## Evidence

Two fixtures in `internal/changeset/ignores_precedence_test.go`, the same branch written in the two spellings of a
stack link, each with the last commit touching the *other* changeset's directory so nothing can pass by being
recent:

- `base: booking` with `base-changeset: booking`, an id the ancestor drop can match. This one fails before the
  reorder: the branch is answered `booking-tests` although `booking` records `ignores: booking-tests`, because the
  inference removed `booking` before anything read what it declared.
- `base: feature/booking` with the same `base-changeset:`. The ancestor drop cannot match a branch name against a
  slug, so the declaration is already the only thing that decides this branch. It passes before the reorder and has
  to keep passing, which is what makes the order a rule rather than an accident of one spelling.

The first fixture is the defect; the second is the contract that keeps the fix from being spelling-dependent.

## Not included

- No change to what `ignores:` means, to who writes it, or to the guard against a mutual declaration.
- No change to the ordering rule that ranks surviving candidates. Ranking only sees what the declarations left.
