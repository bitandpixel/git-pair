# The two exits: `change stack` and `change combine`

## Summary

The branch-shape refusal says a branch carries two unconnected changeset directories and stops. This is what the
author can do about it: `git pair change stack --base <branch>` records the link that makes the second directory an
ancestor, and `git pair change combine --into <id>` folds two directories that are one piece of work into one
changeset. The refusal now names both commands instead of describing them.

## `change stack --base <branch>`

Compares two sets, each read as `ActiveIDs` of a branch minus `LandedIDs` of the destination. The one in both is the
parent, the one only here is the child, and the parent candidates exclude what the **base branch** records as its own
ancestors: on a third level this branch carries the grandparent too, and telling it to exclude its own ancestors
would ask it to exclude the fact the command is about to write. Writes `base:` and `base-changeset:` into the child's
file and nothing else, prints both keys old and new - a base that moves is a reviewer's diff moving - and says to
offer the branch again. It loads no session, because the shape it repairs is one the resolver refuses to resolve.

Four refusals, each asserting its own text: nothing in common names the integration branch as the answer rather than
a stack; a base branch recording no chain names both candidates and the level to settle first; two of this branch's
own names `change combine`; `--base` naming this branch says to make a branch for the child.

## `change combine --into <id>`

The disappearing changeset moves whole into `changesets/<into>/.combined/<gone>/`. Nothing is deleted, so the fold is
visible in the diff and reversible. The survivor keeps its own `base:` and `base-changeset:` - folding something in is
not a licence to move a comparison - and the report says which links were kept and whose were dropped. The survivor's
`ABOUT.md` gains one line pointing at the archive rather than a copy of the disappeared prose.

`--threads` copies the disappeared threads into the survivor, each prefixed with its id so two threads called
`scope.md` do not collide, and says that replies continue in those copies while the archived originals stay put. A
thread file is not an implementation change, so the copy cannot move what a review is comparing.

It refuses while either changeset carries a lifecycle marker or a review, read from the same `lifecycle` summary
`check` reads so the two cannot disagree, and refuses when the two directories are dirty because it commits them.

## What an archive cannot do

Three assertions, one per reader, because they have to stay in agreement: a `CHANGESET.yaml` at
`changesets/x/.combined/y/` answers no id to `Resolve`, appears in neither `ActiveIDs` nor `LandedIDs`, and is not
listed as a thread of `x`. A second level of archive (`.../.combined/y/.combined/z/`) is inert too, which is the case
a reader that walked one level deep would get wrong. The tree already behaved this way - `changesetDir` accepts
metadata only at exactly `changesets/<id>/CHANGESET.yaml`, `dirsUnder` lists immediate children, `Threads` skips
directories - and this makes it a rule someone has to keep rather than a coincidence.

## Evidence

| Fixture | Result |
|---|---|
| `TestChangeStackRecordsTheLinkTheBranchWasMissing` | both keys printed old and new, child's file rewritten, commit touched exactly one path inside `changesets/`, then `change ready` succeeds |
| `TestChangeStackWritesTheDeepestLinkOnAThirdLevel` | the child records `feature/auth-tests`, not the grandparent it had to exclude |
| `TestChangeStackRefusesWhenTheBaseBranchRecordsNoChain` | exit 2, both candidates named, "level below first" |
| `TestChangeStackRefusesABaseThatSharesNothing` | exit 2, both sets named, `main` named as the answer |
| `TestChangeStackRefusesABranchCarryingTwoOfItsOwn` | exit 2, both own ids named, `change combine` named |
| `TestChangeStackRefusesItsOwnBranch` | exit 2, says to branch, and wrote nothing |
| `TestChangeCombineArchivesTheDisappearedChangeset` | archive whole at `.combined/lexer/`, survivor's `base: main` intact, ABOUT line present, then offerable |
| `TestChangeCombineCopiesThreadsUnderTheDisappearedID` | `lexer-scope.md` copied, archived original stays, the survivor's own thread untouched |
| `TestChangeCombineRefusesWhileEitherChangesetIsOffered` | exit 2 naming the changeset and `change unready`, and moved nothing |
| `TestAnArchiveUnderCombinedAnswersNoID`, `TestAnArchiveIsNotAThreadOfItsHost`, `TestArchivesNestedInAnArchiveStayInert` | inert to `Resolve`, `ActiveIDs`, `LandedIDs`, and `Threads` |

The M6 fixtures that asserted the described ways out (`--set-base`, `git restore`) now assert the command names, so
the message cannot rot back into naming nothing.

## Deviations from the plan text

- `combine` takes no `--from`: the pair comes from the branch, and a branch carrying the invariant's shape has exactly
  two candidates. Three or more is refused with the list and the instruction to fold a pair first.
- The review-state guard is "any marker or review on either changeset", which is stricter than "offered or under
  review" and errs toward leaving a fold undone.
- `combine` also requires the two changeset directories to be clean, since it commits them.

## Not included

- No `change unstack`, and no command that undoes a fold: the archive is the undo, and moving a directory back is an
  ordinary `git mv` with a reviewer looking at it.
- `combine` does not merge the two `ABOUT.md` bodies into one narrative. One line points at the archive; writing the
  combined story is the author's, because it is prose about intent.
