# chore-complete-landing-handoff-plan

## Summary

The plan for `feat-landing-handoff-guidance` is finished — approved, declared, and merged into `main` as
`4ae3125` — and its files were still in `docs/plans/`, which is where unfinished work lives. This moves the
directory to `docs/plans/completed/`, where the other finished plans are. No code changes, no behaviour
changes.

```console
docs/plans/landing-handoff-guidance/            → docs/plans/completed/landing-handoff-guidance/
  plan.md
  research/2026-10-14-advice-that-names-a-missing-flag.md
  research/2026-10-14-handoff-line-when-declaration-refused.md
```

## What changed

- The three files above, moved with `git mv`. Nothing inside them was rewritten.
- One closing row in the plan's Audit History table: the landing commit, and the reason the move happened
  here rather than as the last commit of the changeset it belongs to.

## Design decisions

**The move could not have ridden along with the work it documents.** It is the natural last commit of
`feat-landing-handoff-guidance`, and it was ready to be made the moment the review approved. A changeset's
approval is a statement about a commit, and the gate compares the tree between that commit and `HEAD` while
ignoring one path: `changesets/<id>/`. `docs/plans/` is not inside it. Moving the plan after the approval
would have been content that no review covered, the approval would have gone stale, and the gate would have
refused the very merge that was already queued and declared. So it waits for its own changeset, which is what
a finished plan costs in a repository where every commit goes through review.

**Moved rather than copied.** A finished plan under two paths means a later reader has to know which one is
true, and the copy left behind stops being read and starts being believed.

**The internal links needed no edit.** `plan.md` links its research notes by relative path, so they moved
with it and still resolve; nothing outside the directory referenced it, which `grep` over the tree confirmed
before the move.

## Validation

- `mise run check` — gofmt, vet, and the sharded suite, which includes the docs contract test and the prose
  hygiene scan over `PRD.md`, the README and the skill. Nothing there reads `docs/plans/`, so this is a
  regression check rather than a test of the change.
- `mise run gates` — the scripted CI integration, the PTY walkthrough, and the issue-29 scenarios.
- `git pair status --changeset chore-complete-landing-handoff-plan` after landing, which is how the move
  itself is verified: the directory is in the destination, with the chain that carried it.

## Known limitations

- The plan's Audit History row names commits by hash. If `main` is ever rewritten, the hashes stop
  resolving while the row stays true in substance. The same convention is used by every other completed
  plan in the directory.

## Open questions

None.
