# feat-check-after-stacked-parent-landed

## Summary

`git pair check` refused an approved child whose parent had landed and whose branch had been deleted, with
the reason a content difference gets — even though the diff under test was the diff the approval measured.
The gate's question was right and its record was not: the approval had written down no starting point, so
the comparison could not be asked, and "cannot ask" was reported in the words reserved for "it moved".

Three changes, in that order: a review submission now records the commit it measured its diff from, the
landed-parent comparison uses that record, and the branch-is-gone path reports where this head sits relative
to the landing instead of advising a rebase onto a deleted branch.

Reproduced against a real repository before the change:

```text
NOT READY:
- the parent feat-dev-workflow-profile landed as 92d6dd3 — parent landed as 92d6dd3;
  rebase onto origin/main and have the result reviewed again
```

and after, on the same head:

```text
OK: feat-dev-workflow-install is integration-ready
parent: feat-dev-workflow-profile landed as 92d6dd3 — this head is already on it, so there is nothing to rebase
```

## What changed

- `internal/model/model.go`: `TrailerBaseHead` (`Review-Base-Head`), beside `TrailerHead` and
  `TrailerParentHead`.
- `internal/marker/marker.go`, `internal/reviewops/reviewops.go`: `ReviewMessage` and `Submit` take the
  measured base and write the trailer when the caller can name one.
- `internal/lifecycle/lifecycle.go`: `Event.ReviewedBase`, parsed with the same `reviewedHead` validation as
  the other two heads.
- `internal/changeset/base.go`: `MeasuredBase` — `MeasureBase`'s answer taken to its merge base with the
  head, so a caller gets the commit the diff starts at rather than the ref it is printed under.
- `internal/cli/review.go`, `internal/tui/tui.go`: both submission surfaces call the same helper, so the CLI
  and the review screen cannot record different values for one submission.
- `internal/cli/stacked.go`:
    - `parentStatus.Measured`, `approvalBase()`, and `LandingUnderHead`.
    - `parentLive` and `parentGone` compare the landing against `approvalBase()` rather than against the
      parent's branch tip, and both stop refusing when the approval recorded neither record — they say what
      is missing, which is what §21 already said to do.
    - `parentGone` reads whether this head already carries the landing, which it never did, and its reason is
      spelled like `parentLive`'s instead of printing the landing twice.
    - `landedParentStep` names no step when the branch is gone and the head is already on the landing; before,
      it printed `git rebase --onto <landing> <parent-branch> <child-branch>` with a branch that does not
      exist.
    - `owesLandingStep()` keeps `check`'s `next` from repeating the sentence its `parent:` line printed.
- `internal/cli/status.go`, `internal/cli/check.go`: `parent.head_carries_landing` and
  `parent_head_carries_landing`, beside the existing `stale_branch`.
- `internal/cli/parent_landed_base_test.go`: five scenarios — the case above passing, the boundary where the
  recorded base predates the landing still refusing, an approval with no record being a note, the two
  trailers naming two different commits after a landing, and an unstacked submission recording its base.
- `internal/cli/stacked_internal_test.go`: `approvalBase`, `owesLandingStep`, and the three steps
  `landedParentStep` spells.
- `internal/cli/status_parent_landed_test.go`: one assertion, that `head_carries_landing` is true in the case
  `stale_branch` is already asserted true.
- `PRD.md` (§10.4, §21), `README.md` (the marker table, the fields beside `Review-Head`, the landed-parent
  section, `review submit`).

## Design decisions

**The content rule already meant "the diffs are identical"; what was wrong was which two bases it compared.**
`base...head` with a fixed head gives equal patches exactly when the two merge bases give equal trees: at any
path where the two bases disagree, at most one of them matches the head, so at least one of the two diffs
carries an entry there. So the cheap tree comparison stays, and no patch-id enters the gate. What changed is
that the older reading is now the base the approval measured from instead of the parent's branch tip — the
same commit only while that branch is the base.

**Two trailers, two questions, not one trailer with two meanings.** `Review-Parent-Head` names a branch tip,
which is what the movement rule compares a live branch against. Writing a derived commit into it would have
made `status` print a commit as "the tip the approval recorded" and made the movement rule compare a commit
against a branch. `Review-Base-Head` names the commit, which is what the content rule compares today's base
against. They hold the same SHA while the parent's branch is the base and stop agreeing the moment it is not.

**Every submission records it, including unstacked ones.** The question — is the diff under test the diff that
was approved? — is not one only a stack can ask, and a record written only where a rule happens to read it
today leaves the next such question back where this one started. It costs one `merge-base` per submission.

**A commit, not a ref.** `MeasureBase` answers with a ref on purpose: `origin/main` is what a reader wants and
what git resolves freshly. A trailer has to keep meaning the same thing in a clone that arrives after the
branch it named was deleted, so it records the merge base instead — which is also what a derived base already
names, so the two agree where they overlap.

**An approval that named no starting point is a note, not a refusal.** PRD §21 already makes that call for a
missing parent tip: the absence says the trailer was not written. `parentGone` was the one place that refused
on it, and it is the place where the record is least likely to exist — a submission made after the parent's
branch is gone can record no tip at all. What the path can still establish is printed instead: the commit the
parent became, and whether this head is already on it.

**The boundary stays.** An approval whose recorded base predates the landing — measured before the child was
rebased onto it, so the reviewed diff still carried the trunk work the landing merge brought in — is still
refused, and `internal/cli/parent_landed_base_test.go` pins that. The approval measured a wider diff than the
one that would land, which is exactly the case the rule was written for.

**`head_carries_landing` is a second field, not a widened `stale_branch`.** `stale_branch` is a statement about
a branch that is still here and can be deleted, and an existing test says it must stay false when the branch is
gone. This head being on the landing is a statement about the head, and it is the half that survives the
deletion — so it is reported where it is true, and the delete advice stays attached only to a branch that is
present.

## Validation

- `gofmt -l internal cmd`: nothing to format. `go vet ./...`: clean.
- `go test ./...`: the whole suite passes, including the five new CLI scenarios and the new internal ones.
- The reported case, replayed in a scratch clone of the repository it came from (a real stack, a real merge
  landing, the parent's branch deleted): `git pair check` printed `NOT READY` with the parent-landed reason
  before the change and `OK` after, on the same head. `git pair check --json` gains
  `parent_head_carries_landing: true` and keeps `parent_stale_branch: false`.
- `scripts/gates/pty-walkthrough.sh` and `mise run gates`: recorded under Validation after the run.

## Known limitations

- `Review-Base-Head` on an unstacked changeset is written and not read. Nothing gates on it today; the tests
  pin that it is recorded, not that it is used.
- The queue's per-row landed-parent note keeps its own cheap read of the parent and still calls a deleted
  parent branch stale. It has no `Tip` to reason from, and its note is printed once per row on a surface a
  reviewer scans; the sharper reading is on `status` and `check`, where an author acts on it.
- An approval with neither record is waved through rather than compared. That is the documented policy for a
  missing trailer, and it is looser than the tree comparison it replaces, but only for markers git-pair did
  not write.

## Open questions

- `internal/changeset/base.go`: `MeasureBase`'s body moves into a new `Measure` in
  `feat/tui-stacked-span-rendering`, which also makes the *printed* base the destination rather than the
  merge-base commit. `MeasuredBase` keeps working under that change — it calls `MeasureBase` and takes the
  result to its merge base — and the text conflict is inside `MeasureBase`'s body. Whoever lands second
  resolves a few lines.
