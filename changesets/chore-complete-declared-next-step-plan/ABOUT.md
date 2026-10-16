# chore-complete-declared-next-step-plan

## Summary

The plan for `fix-declared-next-step-names-the-push` is finished — approved at `e877979`, declared at
`c68eaaa`, merged into `main` as `8c8f267` — and its files were still in `docs/plans/`, where unfinished work
lives. This moves the directory to `docs/plans/completed/`, where the other finished plans are. No code
changes, no behaviour changes.

```console
docs/plans/declared-next-step/            → docs/plans/completed/declared-next-step/
  plan.md
  research/2026-10-14-what-a-declared-head-is-told-to-do-next.md
```

## What changed

- The two files above, moved with `git mv`. Nothing inside them was rewritten.
- One closing row in the plan's Audit History table: the approval, declaration and landing commits, and the
  reason the move happens here rather than as the last commit of the changeset it belongs to.

## Design decisions

**The move cannot ride along with the work it documents.** It is the natural last commit of
`fix-declared-next-step-names-the-push`, and it was ready the moment the review approved. A changeset's
approval is a statement about a commit, and the gate compares the tree between that commit and `HEAD` while
ignoring one path: `changesets/<id>/`. `docs/plans/` is not inside it. Moving the plan after the approval would
have been content no review covered, the approval would have gone stale, and the gate would have refused the
merge that was already queued and declared. So it waits for its own changeset, which is what a finished plan
costs in a repository where every commit goes through review.

**Moved rather than copied.** A finished plan under two paths means a later reader has to work out which one is
true, and the copy left behind stops being read and starts being believed.

**The internal links needed no edit.** `plan.md` links its research note by relative path, so it moved with the
plan and still resolves. Nothing outside the directory referenced the old path, which a grep over the tree
confirmed before the move.

## Validation

- `mise run check` — gofmt, vet, and the sharded suite, which includes the docs contract test and the prose
  hygiene scan over `PRD.md`, the README and the skill. Nothing there reads `docs/plans/`, so this is a
  regression check rather than a test of the change.
- `mise run gates` — the scripted CI integration, the PTY walkthrough, and the issue-29 scenarios.
- `git pair status --changeset chore-complete-declared-next-step-plan` after landing, which is how the move
  itself is verified: the directory is in the destination, with the chain that carried it.

## Known limitations

- The plan's Audit History rows name commits by hash. If `main` is ever rewritten, the hashes stop resolving
  while the rows stay true in substance. Every other completed plan in that directory uses the same
  convention.
- Two other plans in `docs/plans/` may be finished in the same sense and have not been checked against their
  changesets: `derive-destination-from-tree` and `parent-landed-detection` are the ones I would look at first.
  That is a survey, not a claim — it comes from a directory listing, not from reading them.

## Open questions

None.
