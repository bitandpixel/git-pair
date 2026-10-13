# feat-hooks-reviewer-submit

## Summary

A reviewer can now run their own script at the moment they submit a verdict, on the surface reviewers
actually use. A `<git-dir>/git-pair/hooks/pre-submit/` script that exits nonzero refuses the submission and
writes no review commit; the index is left exactly as the hook left it, and the refusal says the reviewer's
content is still staged. Because the reviewer's checks live under their own git directory, this works for a
person reviewing a repository they cannot edit — which is most of the reviewing git-pair does.

This is the fourth milestone of the approved lifecycle-hooks plan (`docs/plans/lifecycle-hooks/plan.md`),
stacked on `feat-hooks-check-gate`. It completes the point vocabulary: twelve points, six actions, two sides
each.

## What changed

-   `internal/cli/review.go` — `a.submitReview` becomes the one path that records a verdict: stage, then
    `pre-submit`, then the review commit, then `post-submit`. It is injected into the review screen through
    `tui.Options.Submit`, and `runReviewSubmit` calls it too.
-   `internal/tui/{session.go,tui.go}` — the screen submits through the injected path instead of calling
    `reviewops.Submit` itself.
-   `internal/cli/hooks.go` — `firstFailureAt`, `refusedBySubmitHook`, and the `overridden` field; the
    `active` flag is removed from `git pair hooks`.
-   `internal/hooks/hooks.go` — `PreSubmit`/`PostSubmit` documented as the reviewer's points; the `active`
    set and `Active` deleted; the vocabulary comment rewritten for a complete set.
-   Docs: PRD §11.4 gains the reviewer subsection and the three reviewer variables; the README's `review
    submit` and `hooks` rows and the skill's `references/cli.md` name the points and the flag.
-   Tests: `internal/cli/hooks_submit_test.go`, and the screen's delegation assertion in
    `internal/tui/submit_internal_test.go`.

## Design decisions

**The hooks are on the path, not in the primitive.** `reviewops.Submit` stays free of hook calls. The plan
said this to keep the review screen covered, and while implementing it the reason became concrete: the screen
called `reviewops.Submit` directly, so it was a second submission path, and hooks placed in the primitive would
have fired for `review submit` and silently not fired for the screen. The fix is to make the path single rather
than to attach the hooks in two places — `tui.Options.Submit` is the screen's only way to submit, and the CLI
supplies it. A screen opened without one says so on the status line rather than inventing a submission.

**`pre-submit` runs after staging, so the index is the hook's input.** A submission stages the reviewer's
source edits, thread files and `ABOUT.md` edit before it commits. A hook that ran before that would approve a
submission it could not see, which is the failure mode that matters most here: the reviewer's own style check
would pass on the tree as they left it and then be contradicted by the commit.

**The refusal names where the reviewer's work is.** A refused submission leaves staged content and no commit.
That state has no other symptom — the next plain `git commit` lands the reviewer's source edits as ordinary
work with no verdict attached — so the message says the review was not recorded and the content is still
staged. `--no-stage` is excluded from the claim rather than guessed at: git-pair staged nothing there, and
says nothing about what the reviewer staged themselves.

**`--allow-hook-failure` exists here and not on `check`,** and the two override different things. `check`
answers a question for a pipeline, so an override would let the person running it relax a policy they do not
own. A reviewer's hook is their own script on their own machine, and when it misbehaves it must not strand a
verdict the reviewer has already decided to record. The override is loud in both forms: the human stream says
it records the submission anyway, and `--json` carries `overridden: true` beside the failed entry in `hooks`.

**A failing `post-submit` leaves the verdict and says so.** The submission is a published fact by then, and
reversing it would be a write the contract does not account for. The message names the state (an APPROVE
review recorded), the hook, and that nothing was undone — the same three parts the author-side post-hooks use,
because a reader who sees only a nonzero exit assumes the review did not happen.

**No hook result enters the review marker.** The plan carried this as an open spike. The review commit carries
what the reviewer did — the outcome, the body, what was reviewed, where it was measured. A hook result is a
fact about one machine, produced by a script that machine's owner can edit or delete; recording it would make
a verdict look verified somewhere the reader has no reason to trust, and no one reading the review later could
check it. The `hooks` array in `review submit --json` carries the result, with the answer that produced it.

**`active` was removed rather than flipped to always-true.** M1 added it so a hook under a point nobody ran
stayed visible while the milestones wired the rest. With these two points landed every declared point is
invoked, so the field could never again be false — and a JSON field that can never be false is data that
misleads. `AllPoints()` still carries the vocabulary, `unknown[]` still catches a misspelled directory, and
each point is now exercised end to end in the CLI tests, which is the stronger claim the field was standing in
for.

**Trust is asserted at this point, not assumed.** A tracked hook from the repository under review stays
skipped until the reviewer accepts it — a committed script is content that arrived from somewhere, including
from the author whose work is being reviewed. The per-clone root needs no acceptance, which is what makes the
feature usable for a reviewer at all. Both are tested, including the same tracked hook running once
`git pair hooks --trust` has recorded it.

## Validation

-   `go build ./...` clean; `gofmt -l internal cmd` empty; `go vet ./internal/...` clean.
-   `go test ./internal/hooks/ ./internal/tui/` green, and the new CLI cases green: the hook sees the
    reviewer's edit in the index; a body policy blocks `--approve` and accepts the corrected body; the
    reviewer environment names the outcome, the reviewed head, and the body file; a refusal says the content is
    still staged; `--allow-hook-failure` records the verdict and reports `overridden`; a per-clone hook runs
    where nothing is tracked; a tracked hook stays skipped until accepted and then runs; both roots run in
    tracked-then-clone order; a failing `post-submit` exits nonzero with the approval standing.
-   `go test ./internal/tui/ -run TestSubmit` green, including the new assertion that the screen called the
    injected path exactly once with the outcome the reviewer pressed.
-   `go test ./internal/cli/ -run Docs` and `-run TestTheCommandTreeIsTheCommandSet` green.
-   `mise run check` exited **0**: 36 test shards ok (31 served from cache, the rest re-run against the final
    tree), no `FAIL`, and its `gofmt -l internal cmd` step reported nothing. Run again after this file was
    written, with the same result.

## Known limitations

-   A reviewer who submits from a screen opened by another caller of `tui.Run` with no `Submit` gets a status
    line and no verdict. The CLI is the only caller today; the nil case exists so a future caller fails
    visibly rather than submitting through a path nobody hooked.
-   `GIT_PAIR_REVIEW_BODY_FILE` is a path to a file git-pair removes when the command returns. A hook that
    needs the body after the fact has to copy it, which is what a hook that wants to keep it should do anyway.
-   There is still no results store, so `git pair hooks` reports configuration rather than the last result of
    each hook.

## Open questions

-   M1's question stays open: should a repository that has committed tracked hooks refuse outright in a clone
    that has not accepted them, instead of proceeding with a note? At this point it has a reviewer-side edge:
    a reviewer who never ran `--trust` reviews without the repository's own checks having run, and the stderr
    note is the only trace.
-   M5 has to settle the PRD §26 sentence about git verbs inside hooks.
