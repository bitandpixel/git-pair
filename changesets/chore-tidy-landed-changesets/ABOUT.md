# chore-tidy-landed-changesets

## Summary

Three changeset directories whose work is already in `main` are still sitting in `changesets/`, where a reader
sees them as open work. This moves them to `changesets/.landed/` in one reviewed commit. Nothing is deleted.

## What changed

One commit of renames (`git-pair: tidy 3 landed changesets`, 6 files, 0 insertions, 0 deletions):

-   `changesets/fix-installer-pinned-version/` → `changesets/.landed/fix-installer-pinned-version/`
-   `changesets/fix-pty-gate-verdict/` → `changesets/.landed/fix-pty-gate-verdict/`
-   `changesets/fix-ci-prove-destination/` → `changesets/.landed/fix-ci-prove-destination/`

Plus this directory, which is the changeset that carries the move.

## Design decisions

**The three ids were named rather than tidying everything landed.** `git pair change tidy --all-landed` exists
and would move roughly fifteen more directories that `main` has been carrying unfiled — `feat-publish-binary`,
`destination-credited-content`, the `feat-tui-*` set and others. Those belong to the destination's owner to
file deliberately, not to fold into a review about the three directories this branch's own work created. If the
whole shelf is wanted, `--all-landed --dry-run` says what it proposes without touching anything.

**The base is the main that already carries all three.** Filed two and left the third in place would have
needed a second tidy for one directory, which is the thing tidy exists to avoid.

**Housekeeping rides the normal loop.** The move is an ordinary commit of renames on a branch with its own
changeset, so a reviewer sees what leaves `changesets/` before it leaves. `git pair status`, `queue`, `check`
and the destination walk read both spellings, so the changesets themselves are unchanged apart from where they
sit, and `changesets/.landed/` is git-pair's own name that no changeset may claim.

## Validation

-   `git pair change tidy <the three ids>` reported each move and committed them; `git show --stat` confirms all
    six files are renames with no content change.
-   A changeset that another branch still recorded as its base would have been named by the run; it named none.
-   `mise run gates`: all gates green, run from this tree and the binary built from this branch.

Expected effect once merged, from `git pair change tidy --help`: `queue` and `status` stop listing these three
under `LANDED UNREVIEWED`, because the destination then carries the record filed away. `git pair status
--changeset <id>` still reads the same chain and says the same thing about it — the finding is untouched, only
the shelf it sat on changed.

## Known limitations

-   This changeset's own directory will be the next one needing a tidy, once it lands. The command is not
    idempotent in a single pass: it files what has landed, and this one has not landed yet.
-   The fifteen-odd older directories stay in place, deliberately, and are a decision rather than an oversight.

## Open questions

-   Should the integrate job tidy as part of a merge, or is filing records properly a human act? Nothing here
    argues for automating it — a merge job that also deletes history from view is a job whose mistakes are hard
    to see — but the question recurs every few changesets, which is data.
