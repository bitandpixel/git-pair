# feat-change-tidy

## Summary

`git pair change tidy` moves the directory of a changeset that has landed out of `changesets/` and into
`changesets/.landed/`, as one commit of renames on the branch you are on.

This is milestone M4 of `docs/plans/simplify-architecture/plan.md`. Landing has stopped being something
git-pair has to be told about (M2) and stopped being something a stack measures itself against by name (M3).
What is left of the paper trail is the directory itself, still sitting among the work in progress months
after it stopped being work in progress. This moves it, the way an ordinary commit does.

## What changed

`internal/cli/tidy.go` is new. `git pair change tidy <id>…` and `git pair change tidy --all-landed` move
`changesets/<id>/` to `changesets/.landed/<id>/` with `git mv`, then commit exactly those paths with the same
commit machinery a marker uses (`marker.CommitPaths`), so the commit carries the move and nothing else that
happens to be on the index. `--dry-run` reports the plan and commits nothing; `--json` emits
`{"changesets":[{"id","from","to","action"}],"commit","dry_run","destination"}`.

Four refusals, each exit 1 with its own reason: the id is not landed on the integration branch; a changeset
still in flight is stacked on it; the working tree is dirty; the id has no `changesets/<id>/` on this branch.
`--all-landed` with no ids to move is a success with an empty list, not a refusal.

`change tidy` with neither ids nor `--all-landed` is a usage error (exit 2), as is naming ids together with
`--all-landed`.

README's command table carries the row, including the claim that matters for review: the move is a changeset
like any other, the reviewer sees the whole list as renames in one span, and it reaches trunk through the
normal flow.

`scripts/gates/e2e-29.sh` gained a step that lands a changeset, tidies it, and re-runs every question the tool
answers about it — `status --changeset`, `queue --json`, `check`, a second `tidy`, and `init` on the name it
freed.

## Design decisions

**The move is a rename, not a delete.** `git mv` and a pathspec-limited commit mean `git log --follow` still
reaches the review, and the reviewer's diff is `R100` lines rather than a removal and an addition. The e2e
asserts the commit carries nothing but renames.

**`changesets/.landed/` is a namespace, not a changeset.** `changeset.Reserved` already excludes it from every
listing, so `LandedIDs`, `ActiveIDs`, the queue, `check` and the destination walk behave identically on both
sides of the move. That is why this milestone is small: the tree model was built for it in M1.

**A refusal moves nothing.** A caller that named five ids and got a refusal on the third does not get two
moved. Each refusal is the state of the repository, so the fix is a fact to change rather than a flag to add.

**The whole tree must be clean, including inside `changesets/`.** The plan named the guard as "dirty outside
`changesets/`", the way `check` and `change integrate` put it. A tidy is stricter, because the thing it must
not sweep is precisely a sibling changeset's half-written `ABOUT.md`. The refusal names the paths it would have
taken.

**`--all-landed` is a filter, an id list is a demand.** Asking for everything landed means "move the ones that
are landed", so an open changeset is simply not in the set. Naming an id means "this one", so an id that has
not landed is a refusal rather than a silent skip.

## Validation

`go test ./internal/...` in this worktree, and `scripts/gates/e2e-29.sh` with the new step. New tests in
`internal/cli/tidy_test.go`: the move and its renames-only diff; the no-op second run; the refusal against
open work; the refusal against a parent with a live child; `--dry-run`; the two usage errors; the JSON shape
and the surfaces after the move; and the empty list as an empty array.

## Known limitations

`change tidy` writes on the checked-out branch and commits there; it does not create a branch or a changeset
for the move. The plan's "it rides its own changeset" is left to the author, which is also how `change
integrate` works.

The commit is a rename of a directory tree. `git log --follow` on a *file* inside it continues to work; a
tool that pinned a path in an older commit sees the old path, which is what the old path was.

## Discussion

**The parent merged while this changeset was in review, so `base:` moved to `main`.** That field names the
branch the review diff runs against and, for `change integrate`, the destination the declaration asks for
(`destination_source: "base"`). Left pointing at `feat/derived-bases` — a branch whose work is now in trunk —
the declaration would have asked CI to merge this into a branch nobody owns, and `check`'s next step read
"merge into feat/derived-bases with ordinary git". No content changed; this is the metadata that says where
the work is meant to land.

**The branch was restacked onto the merged main** (`dad07da`) at the same time: 12 commits replayed, no
conflicts. The approval `df449a1` is a child of the rebased tip `317fdce`, so it approves the branch as it now
stands rather than the pre-restack history.

**The handoff note this changeset wrote is deleted by M5,** at review's request: its content was the next
concrete action after M1-M4, which is the work M5 performs, and nothing reads the file. M5's `ABOUT.md` carries
the account.

## Open questions

Whether `--all-landed` should also offer to delete directories whose landing is old enough that nobody will
read them. It should not — a delete is a different decision, and the tree rule does not need the space.
