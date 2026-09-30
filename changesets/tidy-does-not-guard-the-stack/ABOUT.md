# A tidy does not guard a stack

## Summary

`change tidy` moved a landed changeset aside only when no other branch had open work stacked on it. That guard
protected nothing, and it was the one tidy rule that could stop tidy entirely. It is gone; the fact it was blocking
on is now reported as a note.

The child measures its diff against a **commit** — `base:` names a branch, and once the parent lands the destination
answers the question — and every reader of `changesets/` takes either spelling of a landed directory. So moving the
parent's directory cannot move the child's review.

## What this changes

- `internal/cli/tidy.go`: `stackedOn` becomes `stackedBy`, returning every such branch rather than the first, and
  `classifyTidy` records it on the item instead of refusing. `tidyItem` gains `in_flight_on`, and the human report
  prints one note line for it.
- The note is correctly worded. The old message was `changeset <child-branch> is still in flight on <parent-id>`,
  which put a branch name in the slot the sentence calls a changeset, and reversed the parent and child shown in
  README's and the PRD's own sample of that line. It now says `the changeset on <branch> still records it as its
  base`.
- PRD §11.10 loses refusal 2 and gains the note, with the reason it existed recorded as no longer true. The README
  command table, the exit-code table, the troubleshooting entry, the skill's command row, and the skill's CLI
  reference follow. The `--json` shape is documented in all three places that document it.

## Why the guard was wrong, measured

Three probes, each building two identical stacks and tidying the parent's directory in only one of them, then
comparing every output with the ids and shas normalised away:

| Comparison | Result |
|---|---|
| child's `status`, `check`, `diff --stat`, `queue` before and after the parent's move | identical, including the base commit |
| the same with the parent's branch deleted as well | `check`, `diff`, `queue` identical; one `status` note reworded, correctly in both worlds |
| `change stack --base <parent>` and `init --base <parent>` from a new branch | refuse identically — the parent has **landed**, which is what removes it from the candidate set |
| a child whose own commit edits `changesets/<parent>/ABOUT.md`, then merges the tidied trunk | same span, same counts column, reported at `changesets/.landed/<parent>/ABOUT.md` |
| the same battery with the child's record spelled the old way (`parent:` + `parent-changeset:`) | identical |

The first two rows are now `TestChangeTidyMovesAParentWhoseChildIsStillOpen` and
`TestChangeTidyMovesAParentTheChildEditedWithoutChangingTheSpan`. The old behaviour's test,
`TestChangeTidyRefusesAChangesetAChildIsStackedOn`, is replaced rather than deleted: same fixture, opposite verdict,
with the assertions the probe actually made.

## What it cost before

One legacy record — `changesets/destination-from-tree/CHANGESET.yaml`, still `parent: feat/infer-parent-changeset`
for a changeset that landed in August — made `change tidy --all-landed` refuse on trunk. A refusal aborts the whole
run, by design, so 41 landed directories could not be tidied and the command was unusable there. The workaround was
to name 38 ids by hand and withhold the three with open children.

## Evidence

`go test ./internal/cli/` green, including the two new fixtures and the eleven unchanged tidy fixtures. Full gates
below: 22 packages, `E2E`, `PTY`, `CI-INTEGRATE: 69 checks`.

## Not included

- No change to the other three refusals. A not-landed id, a dirty tree, and an id this branch does not carry are all
  facts about the run itself.
- No migration of legacy `parent:` records. They keep being read, and this changeset removes one of the two places
  where they cost anything.
