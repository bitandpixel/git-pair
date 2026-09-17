# git-pair-complete-command

## Summary

`git pair review close` becomes `git pair change complete`. Completion is the changeset owner's
half of the lifecycle: a reviewer's approve is a judgement about the code, and deciding that the
reviewed state is what gets taken forward belongs to the owner. It also stops recording a commit —
completing a changeset anchors the chain and writes the immutable archive ref at `HEAD`, and
nothing else.

## What changed

- **`git pair change complete`** (new, author-side): clean tree → newest effective review at `HEAD`
  permits integration (`approve` or `feedback`) → surviving-review-additions gate, same
  `--allow-surviving-review-additions` override `change ready` has → move `refs/reviews/<cs>` to
  `HEAD` → write `refs/reviews/archive/<cs>/<short-head>` at `HEAD`. `git pair review close` is
  removed, with no alias.
- **No close marker.** `marker.CloseMessage`, `lifecycle.KindClosed`, `model.StateClosed` and
  `model.StateValueClosed` are deleted. Derived states are now `WORKING`, `READY`, `BLOCKED`,
  `FEEDBACK`, `APPROVED`. `change wait`, `review submit`'s `next_action` and `status`'s
  `next_action` point at `change complete`.
- **`status` reports completion through `archive_ref`.** It is set when an archive ref points
  *exactly* at `HEAD`, and empty otherwise; `next_action` becomes "safe to squash/merge; this head
  is archived at …". `archiveRefFor` (newest archive reachable from `HEAD`) became
  `archiveRefAtHead` (exact match).
- **Idempotent instead of refusing.** Re-running at the same `HEAD` succeeds, prints that the
  archive already points there, and reports `archive_created: false`. There is no longer an
  "already closed" state to refuse with.
- `change complete --json`: `changeset`, `state` (unchanged by the command), `head`, `short`,
  `base`, `review_ref`, `archive_ref`, `archive_created`, `squash_safe`, `acknowledged_survivors`,
  `surviving_review_artifacts`.
- Consequences of removing the closed state: `reviewops.Submit` loses its `summary` parameter and
  its closed-changeset refusal, and `git.Repo.IsAncestor` is deleted — `archiveRefFor` was its only
  caller.
- PRD (§8 tree, new §9.5, §12, §13, §19.3, §22, §29), README (quickstart, marker table, refs,
  command table, exit codes, JSON contracts, agent contract) and the `e2e-29.sh` artifact follow.

## Design decisions

**No marker commit.** "Markers are commits" stays true for `ready` and review outcomes. Completion
is not one: the changeset is finished when the archived history is merged into the deployment
branch, which git-pair neither performs nor derives. Deriving a terminal state from the archive ref
instead would make `status` answer a ref question while everything else answers a commit question,
and would silently un-complete the changeset on the next commit. Merge-derived completion against
`base` is the follow-up the owner asked for; this change keeps it out.

**Legacy `Review-State: closed` commits are retired, not dual-parsed.** They land in the existing
unrecognised-marker path — an implementation commit that invalidates the approval, listed by
`status` — which is the conservative reading and keeps one vocabulary. Consistent with the hard
break taken for `gitpr`→`git-pair` and the trailer rename.

**The movable review ref still moves.** A changeset-only commit after the approval leaves
`refs/reviews/<cs>` short of `HEAD` (only submissions move it), so completion anchors the head it
archives before archiving it. `TestChangeCompleteAnchorsTheMovableRefAtTheCompletedHead` pins that.

**`archive_ref` matches `HEAD` exactly.** Reporting the newest archive reachable from `HEAD` would
tell an agent "safe to squash/merge" about a branch that has new, unreviewed work on top of the
archived commit.

## Validation

- `mise run check` (gofmt, vet, `go test ./...`) green.
- New/updated tests: `internal/cli/complete_test.go` replaces `close_test.go` — no-commit
  completion, archive names `HEAD` exactly, full chain reachable after `git branch -D`, survival
  gate and its override, dirty-tree and stale-approval refusals, no merge/push/squash, idempotency,
  the `--json` contract, and the `status` reporting above. `workflow_test.go`'s PRD §29 walk now
  ends at `complete` and asserts the commit sequence has no close marker. Lifecycle tests drop the
  close rows and gain a retired-marker test; `reviewops` gains a submission-against-a-completed-head
  test.
- Mutation-checked, each turning its covering test red: archive lookup following ancestry instead of
  exact match; completion creating a commit (four tests); dropping the movable-ref update.
- `e2e-29.sh` green against the rebuilt binary, with two new checks: completion moves no commit, and
  the archive ref names `HEAD`. `pty-walkthrough.sh` green.

## Known limitations

- Nothing derives that a changeset has landed. `status` stays `APPROVED` after completion until the
  merge; only `archive_ref` distinguishes "approved" from "approved and archived".
- `review submit` no longer refuses to review a completed head: with no closed state there is
  nothing to refuse on, and the archive stays where it was (`TestSubmitStillRecordsAgainstACompletedHead`).

## Open questions

- Should merge-derived completion (`HEAD` contained in `base`) be next, and should it be a state
  name or a `status` field?
- The `pi-git-pair` skill and its `references/cli.md` still document `review close` and `CLOSED`;
  that is a separate repository and changeset.
