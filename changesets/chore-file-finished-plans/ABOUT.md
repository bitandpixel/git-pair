# chore-file-finished-plans

## Summary

Ten plan directories sat in `docs/plans/`, which is where unfinished work lives, and nobody knew which of them
still described work. Each was read against the tree and against `origin/main`'s history, and nine of the ten are
done with. They move to `docs/plans/completed/`; one stays, because it has an open decision in front of its
second milestone.

Six were delivered and landed. Three were not finished — they were killed, and each already carried a banner
naming `simplify-architecture` as the plan that undid it; they are filed with that stated in the closing section
rather than presented as completions.

| Plan                              | Verdict          | Evidence                                                                                                                                                                                                    |
| --------------------------------- | ---------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `base-changeset-collapse`         | delivered        | four branches, all 2026-09-30: `feat/ignores-before-inference` `71c55bc`, `feat/rank-by-add-commit` `b4b5c6f`, `feat/base-changeset-always-recorded` `7d6518c`, `feat/set-operation-primary-rule` `3764473` |
| `change-integrate-cmd`            | delivered        | `feat/change-integrate-cmd` `7718a0e` (2026-09-27); `internal/cli/integrate.go`, `Declared()` in `check`                                                                                                    |
| `editor-term-gate`                | delivered        | `feat/editor-term-gate` `5f1dfb8` (2026-09-27); `TestEditorPrecedenceMatchesGit` pins `TERM`                                                                                                                |
| `lineage-in-the-surface`          | delivered        | `feat/lineage-in-the-surface` `6c081a5` (2026-09-22); chain print and `TestStatusStackChainCostsABoundedReadPerStep`                                                                                        |
| `parent-landed-detection`         | delivered        | `feat/parent-landed-impl` `043af3f` (2026-09-23); `parentStatus`, `landedParentStep`                                                                                                                        |
| `preview-you-marks`               | delivered        | `feat/preview-you-marks` `e504d5a` (2026-09-24); `previewEditsBody`, `youText`                                                                                                                              |
| `legacy-refs-and-remote-branches` | superseded       | namespace deleted by `simplify-architecture`; M2 never executed                                                                                                                                             |
| `publish-the-records`             | superseded       | no `integration publish`, no `--fetch`, no mirror refspec; `contract_test.go` fails a next action that names one                                                                                            |
| `review-architecture-v2`          | superseded       | M1–M5 landed 2026-09-21, design deleted by `simplify-architecture` `01dcd0a` (2026-09-29)                                                                                                                   |
| `derive-destination-from-tree`    | **stays active** | six of M2's tasks unticked, and "Open decision, needed before M2 lands" asks for a PRD ruling on landedness versus ref existence                                                                            |

## What changed

- The nine directories moved with `git mv`, files and relative links intact. `review-architecture-v2` carries
  `requirements.md`, `reconciliation.md` and a research note, which travel with it.
- Each moved plan gains a `## Closure` section: the verdict, the changesets or the deletion that produced it,
  and — for the three superseded ones — what survived and why the file is kept.
- `base-changeset-collapse` had three M3 boxes unticked while the code carried the work. They are ticked, each
  with the symbol that delivers it (`distanceFromAdd`, `rankByCreation`, the reworded comments). Its fourth box
  is a deferral written out with its reasons and stays exactly as it was.

## Design decisions

**A `## Closure` section, not an Audit History row.** The repository's own filed exemplar —
`docs/plans/completed/lifecycle-hooks/plan.md`, the one you named as the control — closes with a `## Closure`
section that states what shipped and what the plan left undone. An Audit History row fits a plan that was audited
while running; these plans needed a statement of what their files mean now, and nine plans had four different
audit-table shapes, two of them empty. One section name, in the words the control uses, is the cheaper
convention.

**Superseded plans are filed, with the supersession in the closing section.** Leaving three dead plans in
`docs/plans/` keeps the active listing false, which is the thing the survey was asked to fix. A
`docs/plans/superseded/` directory would make the distinction structural at the cost of a second convention, and
their banners already point into `completed/`. So each says plainly that it was not delivered, what deleted its
subject, and — for `legacy-refs-and-remote-branches` — that its second milestone was never executed. Nothing in
the tree can now read "finished" where the truth is "stopped".

**Ticked boxes carry the citation, not just the tick.** A box flipped with no evidence is a claim; flipped with
`distanceFromAdd` in `internal/changeset/resolve.go` and the merge that landed it, it is a check a reader can
repeat. The deferral keeps its text and its two reasons, because the reasoning about why it waits is the useful
part.

**`derive-destination-from-tree` was left where it is, and its own text is the reason.** Six of M2's tasks are
unticked, and the plan carries a section titled "Open decision, needed before M2 lands" that asks for a ruling in
the PRD beside `DestinationFor` — a `base:` whose branch still exists and carries work, while a same-slug
changeset has landed elsewhere. Filing it would file an unanswered question.

## Validation

- Each verdict rests on `git log` evidence rather than on the plan's checkboxes. For the delivered ones: the
  introducing commit found with `git log -S <symbol>`, and the merge that carried it with `git log --merges
--ancestry-path <commit>..origin/main`, both quoted in the plan's Closure section. For the superseded ones:
  the absence of the commands and flags the plan asked for, plus the test that fails if a next action names one
  of them.
- Symbols cited as still present were each located in the tree: `TestEditorPrecedenceMatchesGit`,
  `previewEditsBody`, `youText`, `parentStatus`, `landedParentStep`,
  `TestStatusStackChainCostsABoundedReadPerStep`, `Declared()`.
- `mise run check` and `mise run gates`. No Go file changes; plan files are not checked material, so these are
  regression checks, with one exception worth naming — `TestNoDocNamesADurableRef` and the hygiene scan read
  `PRD.md`, the README and the skill, and none of them reads `docs/plans/`.
- `git pair status --changeset chore-file-finished-plans` after landing, which is how the move itself is
  verified.

## Known limitations

- A verdict is an argument from the tree and the history, not a run of each feature. Where a plan promised
  behaviour that a later change quietly removed, the Closure records what is in the tree today
  (`lineage-in-the-surface` is the clearest case: two milestones read a namespace that no longer exists).
- `review-architecture-v2`'s M6–M8 are assessed as answered by later work — the per-branch queue, stacked
  changesets, the skill's agent contract — rather than traced to one commit each. That is the weakest row in
  the table, and it is a superseded plan, so nothing depends on the assessment being exactly right.
- `derive-destination-from-tree` keeps a "Follow-ups discovered during execution" note about the integrator's
  branch-name filter in `.github/workflows/git-pair-integrate.yml`. The CI profile work replaced those files, so
  the note describes something that no longer exists. Left alone: correcting a kept-active plan's research note
  is that plan's business, not a filing's.

## Open questions

- `derive-destination-from-tree` needs the decision its own section asks for: landedness or ref existence wins.
  Nothing else about that plan can be closed until somebody answers it.
