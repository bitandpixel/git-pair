# fix-deleted-content-is-drift

## Summary

An approved changeset could delete reviewed content after the approval and stay integration-ready. Two
readings of the same comparison hid it, and both are fixed here: the drift comparisons now run with rename
detection off, so a file that leaves the tree is a changed path, and a path is credited only where the cited
ref accounts for it, so content nobody holds is credited to nobody. The three shapes are pinned by tests.

## What changed

`git pair check` on an approved changeset whose head deleted a reviewed file now refuses, where it said
`OK: feature is integration-ready`. The three cases a reviewer will ask about, measured with a binary built
from this branch against a repository whose `main` carries `a.go`:

```console
# the approval covers a.go; the author deletes it and adds a file with the same bytes
$ git rm a.go && cp a.go a_copy.go && git commit -am "delete a.go, add a similar file"
$ git pair check --json
ready False reasons ['content outside changesets/feature/ changed since 359acd5: a.go, a_copy.go']
```

Before the change the same repository reported `ready True`, no reasons, and
`drift_credited_by: {'a.go': 'main'}` — a deletion cleared by a branch that holds nothing at that path.

What must keep passing, and does:

```console
# trunk deletes a.go; the branch merges trunk in
$ git pair check --json
ready True reasons [] credited ['a.go'] by {'a.go': 'main'}

# …and the branch then commits its own work on top of that merge
$ git pair check --json
ready False reasons ['content outside changesets/feature/ changed since 13542b7: extra.go'] by {'a.go': 'main'}
```

The merged-in deletion is credited to the branch that removed it, and the branch's own later file is the only
thing refused. A change that punishes that third case would tax every honest merge to catch one dishonest
deletion, so it is the case the design is arranged around.

## Design decisions

**Two diff helpers rather than one flag flipped on the existing one.** `internal/git/git.go` keeps
`PathsChanged` and `PathsChangedOutside` as they were, and adds `ContentPathsChanged` and
`ContentPathsChangedOutside`, which pass `--no-renames`. The pair answers two different questions: what a
reader of this diff would see, and at which paths content or its presence differs. Only the drift rule reads
the second. `git diff` finds renames by default (`diff.renames` is on unless configured off), so asking for no
renames is a flag and not the absence of one — a point the code comment makes because the absence of `-M`
reads like an answer.

**Presence, with a memory of the branch point.** The per-path reading asked whether the cited ref and head
agree at a path, and a comparison of two trees calls two absent paths identical. The rule now requires the ref
to account for the path: it holds the path in its tree, or it held it at `merge-base(ref, marker)`. The second
half is what keeps the merged-in deletion credited — trunk no longer has the file, but it had it where the
branch was cut — while a file the branch's own work held and no ground ever carried has nobody to vouch for
it. A ref that cannot answer the history question keeps the credit its own tree supports, the way an unreadable
ref already costs only its own credit.

**The whole-branch reading is untouched and still runs first.** A clean take-in is settled by one
`git merge-tree` per ref, before any per-path read, so the common case pays nothing for the new reads and a
hand-written conflict resolution remains drift.

## Validation

`mise run check` and `mise run gates` (sharded suites plus the e2e, pty and CI-integration gates). The cost
measurement in the CI-integration gate came back at the recorded 27, 46, 55 and 64 invocations with the
two-level bound at 51, because the three reads this change adds run only where paths moved above the marker,
and that fixture's branches have nothing above their markers. The number the change could move is the cost of
checking a branch that moved content, which no gate measures; it is bounded by three reads per cited ref, and
the whole-branch reading still answers the clean-merge case before any of them are asked.

The behaviour is pinned at two levels. `internal/git/contentpaths_test.go` pins the collapse itself — that
`PathsChanged` does not list the deleted path where `ContentPathsChanged` does — so the reason the two helpers
exist stays visible. `internal/cli/deletion_credit_test.go` pins the gate: deletion nobody's ground holds is
refused, deletion of content the destination holds is refused, a deletion the ground made is credited to that
ground, and it is still credited when the branch goes on working after the merge.

Each half was checked against the tests by putting the defect back. Dropping the presence condition from
`creditContent` fails `TestCheckRefusesAContentDeletionNobodyGroundHolds` with `service.go was credited to
"main"`; routing the drift comparison back through `PathsChanged` fails both deletion refusals.

## Known limitations

Rename detection is still on for the surfaces that were not measured as defective. `DiffNames` feeds the
reviewer-facing file lists (`git pair diff`, the review screen, the TUI's changed-file panes), where
`git pair diff` can show a deletion-plus-addition as one renamed path; the comment above
`internal/tui/session.go` even claims the list passes no renames, which is not what it does. The same
default applies to the landedness comparison in `internal/cli/queue.go`, the contribution reading in
`internal/changeset/contribution.go`, and the parent-fork comparison in `internal/cli/stacked.go`. Each is a
candidate for the same treatment and none is the hole this changeset measured, so none is changed here: a
change to a reviewer-facing list or to contribution identity deserves its own measurement.

`TreeHasPaths` reads a tree, so a changed path that is a directory rather than a file (a gitlink, or a path
whose only change was inside an excluded directory) is answered from the file listing. The drift rule's
pathspec excludes `changesets/<slug>/`, which is where that shape would come from.
