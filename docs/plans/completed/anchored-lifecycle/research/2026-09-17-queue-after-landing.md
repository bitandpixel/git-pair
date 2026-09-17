# What a landed changeset does to `review queue`

Measured on this repository, on `main`, at the first changeset to land under the squash-merge
policy: `git-pair-complete-command`, merged as `882100c`. Reproducible in a scratch repo with
the script at the bottom.

## While the changeset branch still exists, the queue advertises merged work

`review queue` on `main`, immediately after the squash merge, before the branch was deleted:

```
READY FOR REVIEW

git-pair-complete-command
  base: main
  ready: 50m ago
  head: 934ce8b
```

The changeset had already merged. The queue lists it as awaiting review, and will keep doing so
for as long as the branch exists, because `lifecycle.Summarize(base..branch)` still finds
`git-pair: ready` as the newest marker in `main..934ce8b`. Nothing about the merge is visible to
that derivation: a squash merge puts the *content* on `main` and leaves the branch's commits
disconnected from it.

## Once the branch is deleted, the changeset directory becomes a permanent note

```
READY FOR REVIEW

  nothing is ready
note: skipped git-pair-complete-command (no branch matches this changeset directory)
```

`runReviewQueue` (`internal/cli/review.go:404`) iterates `changeset.List(repo)`, which is an
`os.ReadDir` of `changesets/` in the checked-out working tree. A directory with no matching
branch is reported as skipped, so every future queue run on `main` prints one line for this
changeset. That is the same code path that produces the filter this plan relies on ("no branch,
not ready") — the noise and the filter are one mechanism, and the noise is what its output looks
like when a changeset finishes.

Both of these come from the same root cause: the queue enumerates **directories in the working
tree**, while a changeset is a relationship between **a branch and a base**.

## Related hazards measured on the way

`change init` resolves a changeset from the checked-out branch name (`changeset.ForBranch`). With
`changesets/<slug>/` left on `main` and the branch gone, a branch created later whose slug matches
inherits the landed directory, its `CHANGESET.yaml` and any thread files, and `change ready`
succeeds on it. Observed by hand; the condition is exactly the state `main` was in above.

## What a squash merge leaves that *can* be used to detect landing

The archived head is not on `main` in any form: not an ancestor, not reachable, `git cherry`
reports `+`, and tree equality is unreliable because other work lands alongside. What does land is
the `changesets/<slug>/` directory itself, so comparing that subtree between the base branch and
the archive ref is a valid signal:

```
git diff --quiet <base> <archive-ref> -- changesets/<slug>   # no diff => this changeset's metadata
                                                            # is what landed on base
```

Non-circular, because the archive ref is a separate ref that nothing moves after the fact. It
needs a real archive ref, so it only works for changesets that were completed (or otherwise
anchored), which is why the plan pairs it with anchors written at `change ready`.

## The completion anchor is load-bearing

`change complete` archives the head it is called at, after requiring APPROVED or FEEDBACK. Today
an implementation commit after the approval flips the state to WORKING, so completion refuses
before it can archive a head nobody approved. Remove that flip without replacing it and the
archive ref can name a head that was never reviewed, while `latest_review` describes an earlier
one. The archive is the artifact an agent is told to trust when deciding what is safe to
squash-merge, so the plan keeps one anchor check at that call site (milestone 3) while deleting
the drift machinery everywhere else.

## Repro

```sh
git init -q -b main r && cd r && git config user.email a@b.c && git config user.name A
git config commit.gpgsign false
echo base > base.txt && git add -A && git commit -qm seed
git switch -qc feat
git pair change init --base main
printf '# feat\n\n## Summary\n\ncontent\n' > changesets/feat/ABOUT.md
echo 'func F() {}' > f.go && git add -A && git commit -qm impl
git pair change ready
git pair review submit --approve
git pair change complete
git switch -q main && git merge --squash -q feat && git commit -qm "feat landed"
git pair review queue          # advertises feat as READY FOR REVIEW
git branch -D feat
git pair review queue          # permanent skip note
```

## `status` reports an anchor that does not exist

On a changeset with no review activity yet, `git pair status` prints:

```
Review archive:
  refs/reviews/git-pair-anchored-lifecycle
```

and `status --json` reports:

```
review_ref    = refs/reviews/git-pair-anchored-lifecycle
review_commit =
archive_ref   =
```

Two things wrong, both pre-existing (`internal/cli/status.go:166` at `8fa7601`, unchanged by
`882100c`). `ReviewRef` is computed from the slug by `reviewref.Head(slug)` and never checked for
existence, so the field is never empty and cannot answer "has this changeset been reviewed" — the
empty `review_commit` is the real signal, which the field names do not suggest. And the human
heading calls the movable ref an "archive", which after M4 is actively wrong: the movable ref is
the anchor, and `refs/reviews/archive/<slug>/<sha>` is the archive. M4 makes the anchor prominent,
so it has to report it truthfully.
