# The review screen names the destination, and says which parent landed

## Summary

Open the review screen on a stacked changeset whose parent has landed, and the base row reads:

```text
│ base  e2e44081c8f2a0f7390a42e1e80fbbe1212e8c2296d4e3f4       │
│ span  e2e44081c8f2a0f7390a07245bd97fe01606ded4...current     │
```

Two problems in one line. Forty characters do not fit the box's column — at 46 columns both rows arrive
truncated to `e2e44081c8f2a0f7390a07245bd97fe01606de…`, which is the same information as nothing. And the
string is unreadable even when it fits: it is not the landing commit a reader might recognise, because
`changeset.BaseFor` answers with the **merge base** of the child's head and the destination. After
`git rebase --onto <trunk> <parent> <child>` — the exact rebase the rule exists to serve — that merge base is
trunk's newest commit, which is usually somebody else's work:

```text
git pair diff: 822db2d5004a42e1e80fbbe1212e8c2296d4e3f4...current   # "other work lands on trunk"
```

`git pair status` at least prints the rule beside it (`— the parent alpha landed, so the run this branch
shares with origin/main`). The box printed the value alone.

Rule 2 now answers with the **destination**, and the box carries the one fact that name cannot:

```text
╭ reporting-tests ─────────────╮
│ base   main                  │
│ parent reporting landed      │
│ span   main...current ▸      │
│ ABOUT.md                     │
│ ▾ Threads                    │
╰──────────────────────────────╯
```

**The measurement does not move.** This is the part worth reading twice, because the obvious objection — "a
branch the child sits *behind* would put the destination's newer work in the child's diff" — is the old bug,
and it is not what happens. Two things make the label and the measurement separable:

1. No caller hands `Base.Ref` to `git diff`. `span.Resolve` takes the merge base of the base and the head
   before pinning either end (`internal/span/span.go`, `KindChangesetBase`), and
   `merge-base(merge-base(X, head), head) == merge-base(X, head)`.
2. Everything else takes a **log** range — `lifecycle.RangeForHead` builds `base..head`. For log ranges
   `A..B == merge-base(A,B)..B` always holds: any commit reachable from both `A` and `B` is an ancestor of
   their merge base, so it is excluded either way.

Measured, same repository, both builds, with trunk moved past the landing:

```text
before:  git pair diff: e2e44081c8f2a0f7390a07245bd97fe01606ded4...current
after:   git pair diff: origin/main...current
         c.txt | 1 +   changesets/child/ABOUT.md | 1 +   changesets/child/CHANGESET.yaml | 3 +++
```

Byte-identical stat, identical `check` output, identical `queue` output. Only the label changed. The audit of
every two-revision git call in the tree is in *Design decisions*.

## What changed

| file | what it does now |
| --- | --- |
| `internal/changeset/base.go` | rule 2 answers `out.Ref = db.Ref` instead of the merge-base commit, and keeps the `merge-base` call as the proof that a run is shared (`mb != ""` selects the rule, no longer names the answer). `Base` gains `ParentLanded`, which is the containment read rule 2 already made. Rule 2's comment records both the older failure and the reason the name changed. New `Measure` is `BaseFor` for a caller that prints the rule and has no error path; `MeasureBase` is now `Measure(...).Ref` |
| `internal/tui/session.go` | `NewSession` reads `Measure` rather than `MeasureBase`, so the rule is in hand, and stores the landed parent's id. `Header` gains `ParentLanded` |
| `internal/tui/tui.go` | `parentLine()` draws the box's second non-navigable line, and `boxLines` draws it under the base when there is a parent to name. `boxLabel` pads to seven cells, which is what `parent` costs and what keeps the three values in one column. `listContentWidth` counts the line so the column it is in stays sized to it |
| `internal/changeset/shape.go` | the comment that said the derived base "is a commit, and a commit names no branch" — still the right guard (`!BaseDerived`), wrong about the value |
| `internal/changeset/base_test.go` | `changedFromSpan`, the tests' version of how callers measure, replacing the direct `git diff b.Ref head` in the three rule-2 assertions. New `TestMeasureCarriesTheRuleAndCannotFail`. `ParentLanded` asserted at all three rules |
| `internal/cli/parent_relink_test.go`, `internal/cli/status_parent_landed_test.go` | the four assertions that pinned the SHA name the destination instead, and `TestADerivedBaseReplacesTheParentBranchWhileTheBranchIsHere` gains the diff check the SHA used to carry implicitly |
| `internal/tui/fetched_base_test.go` | `landedChild`/`stackedChild` fixtures and three session tests: the base names the destination and the parent, a deleted parent branch changes neither, and a parent still standing says nothing about landing |
| `internal/tui/regions_internal_test.go` | `TestTheParentLineSaysWhatTheBaseRowCannot` — the line's wording, that it is not a control, that it costs a row only in this shape, and that its value starts in the base value's column |
| `PRD.md` | §21's landing bullet and "Where a child is measured is not where it lands"; §14's box, with the second screenshot |
| `README.md` | the box screenshot, and the paragraph on the three labels |
| `scripts/gates/pty-walkthrough.sh` | the first-paint assertions follow the wider label, and a landed-parent fixture checks what reaches a real terminal — including that the landing's object id does not |

## Design decisions

**The name and the measurement were always two answers; the type only ever carried one.** `Base` already has
`Derived` and `Why` precisely so a surface can print a base and say which rule produced it, and rule 3 already
answers with `db.Ref` — so "the destination, with the rule beside it" was already an answer this type gives.
Rule 2 was the one rule that answered with a commit, and the surfaces that render it have nowhere to put the
rule. Naming the destination makes rule 2 read like its two neighbours.

**Why the label belongs on `Base` rather than in the TUI.** The review screen needs "the parent landed" beside
the base. `internal/cli` cannot be imported from `internal/tui` — `cli` imports `tui` — and the richer landed
view (`parent.landed_commit`, `parent.stale_branch`) lives there. `BaseFor` had already made the containment
read that decides the case, so the fact is carried rather than recomputed one layer up. `ParentLanded` is
`false` for a stack whose parent has not landed, which is what keeps "gone" and "landed" tellable when both
name the same ref.

**The two-revision audit, because "the measurement does not change" is the whole claim.** Every call in the
tree that takes two revisions: `DiffNames`/`Numstat`/`PathsChanged*` reach `sp.From`/`sp.To`
(`cli/diff.go`, `cli/review.go`, `tui/session.go`) — resolved spans, safe; or a review marker's SHA
(`reviewops`, `cli/change.go`); or `queue.go`'s `classifyOrphan`, which reads `changeset.BaseAt` — the recorded
base out of the tree, never the derived one. `LogFields` takes a range, which is the identical-either-way case.
`lifecycle`'s drift check starts at `marker.SHA`. Nothing hands a derived base to a two-dot tree diff, and the
A/B measurement above is the empirical half of the same claim.

**The tests followed the callers rather than the shorter path.** `changedBetween(t, f, b.Ref, head)` was a
direct two-dot diff, which asserted a property production never relied on — that the base sits *in front of*
the head. Left alone it would have failed for the right reason and tempted the next reader into the same
conflation. `changedFromSpan` resolves the merge base the way `span.Resolve` does, so the content assertions
still say "the child's diff excludes trunk's later work" and now say it about the measurement the product
makes.

**Seven cells for the label column, not six with a misaligned seventh word.** `base` and `span` were padded to
six and `parent` is the word a reader of a landed child is looking for, so shortening it to fit (`stack`) put a
section name where a relationship belongs. One cell on two rows is cheap once the base is a name.
`tree_internal_test.go`'s expected box and both screenshots moved with it.

**The parent line is a line of the frame, like the base.** It goes nowhere, so making it a row would put an
arrow on a caption — the exact distinction `TestTheSpanRowIsARowAndTheBaseLineIsNot` exists to hold. It is
empty for every other shape, so a changeset that is not a landed child pays no row.

**`Measure` rather than a second read in the session.** The review screen needs the rule, `MeasureBase`
returns a ref and swallows errors, and `BaseFor` can fail. `Measure` is the point where "fill the head I was
not given" and "fall back to the recorded base" live once, with `MeasureBase` a one-line projection of it, so
`cli`'s cheap path and the screen's rich one cannot drift.

## Validation

- `mise run check` — gofmt, `go vet`, the whole suite sharded. Green.
- New tests, each confirmed to fail before the change and pass after:
  - `TestAChildWhoseParentLandedNamesTheDestinationAndTheParent` — fails on the object id, and on the file
    list if the measurement moves.
  - `TestTheParentLineSaysWhatTheBaseRowCannot` — fails on the absent line, and on the column if the labels
    stop aligning.
  - `TestMeasureCarriesTheRuleAndCannotFail`.
- Updated, with the reason each pinned recorded at the assertion: `TestBaseOfAStackWhoseParentLanded`,
  `TestBaseOfAStackWhoseParentBranchIsGone`, `TestBaseOfARebasedStackIsItsForkFromTheDestination`,
  `TestBaseFallsBackToTheIntegrationBranch`, `TestADerivedBaseReplacesTheParentBranchWhileTheBranchIsHere`,
  `TestRelinkAfterARebaseOntoTrunkShowsOnlyTheChildsOwnWork`,
  `TestADerivedBaseNeedsNoRecordInThisClone`, `TestStatusCallsALandedParentStaleWhileItsBranchIsPresent`,
  `TestTheListAsItRenders`.
- `bash scripts/gates/pty-walkthrough.sh` — all checks pass, including the new fixture, which asserts in a
  real pty that `base   main`, `parent alpha landed` and `span   main...current` paint and that the landing's
  object id, the parent's file and the unrelated trunk commit do not.
- `bash scripts/gates/e2e-29.sh` and `bash scripts/gates/ci-integrate.sh`.
- Measured by hand in scratch repositories before writing any of it: the two-build diff above, and `check`,
  `queue`, `status`, `status --json` and `diff --stat` compared side by side.

## Known limitations

**`git pair status --json`'s `base` and `base_ref` change value for a landed child**: a pinned object id
becomes `"refs/remotes/origin/main"`. A consumer doing `git diff $base...HEAD` is unaffected, because three
dots take the merge base; one doing `$base..HEAD` was relying on the base being pre-resolved and now gets the
destination's newer work reversed into the diff. Nothing in this repository does that — the audit above covers
`scripts/ci/` too, which shells out to `git pair` and parses fields rather than diffs — but it is a machine
contract and it is the one item here that is not purely cosmetic. `base_why` and `base_derived` still say which
rule produced the value, and `landed_commit` on the parent still names the commit.

**The span picker's commit list gets a little noisier.** `picker.go` calls
`RecentCommits(limit, "HEAD", base)`, which is a union rather than an exclusion. With an ancestor OID as the
base the branch's own history was the whole list; with the destination, the destination's recent commits join
it. This is what the picker already does for every changeset measured against trunk, so it is consistency
rather than a new defect — and reaching a trunk commit from the picker is arguably the point of the row. It is
unfixed and unmeasured beyond reading the call.

**The header no longer states where the diff begins.** `origin/main...current` relies on git's three-dot
meaning. For a child that has not been rebased the merge base is the parent's old tip, and nothing on the
screen says so; `status`'s `Stack:` section does, and the new line says which parent. The alternative was a
line no one can read.

## Open questions

Should the box's parent line also carry the rebase advice? `status` prints
`rebase onto it: git rebase --onto <landing> <parent> <child>` for exactly the un-rebased case, and the box
currently says only `landed`. The column fits `parent alpha landed` and not much more, and the queue and
`status` both carry the step, so this change leaves the advice where it is. Worth revisiting if a reviewer is
found to need it at the moment they open the screen.
