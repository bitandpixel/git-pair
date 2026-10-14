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
    `tui.Options.Submit`, and `runReviewSubmit` calls it too. The screen's half is `a.screenSubmission`, which
    adds the hook output to the refusal the screen shows (see *The screen owns the terminal*), and
    `withHookText`, which caps what it appends.
-   `internal/cli/hooks.go` — `hookOutputLines` is now the one definition of a hook run's text, with
    `replayHookOutput` writing it to stderr and `app.hookPresenter` diverting it to a caller that can render it.
-   `internal/tui/{session.go,tui.go}` — the screen submits through the injected path instead of calling
    `reviewops.Submit` itself.
-   `internal/cli/hooks.go` — `firstFailureAt`, `refusedBySubmitHook`, and the `overridden` field; the
    `active` flag is removed from `git pair hooks`.
-   `internal/hooks/hooks.go` — `PreSubmit`/`PostSubmit` documented as the reviewer's points; the `active`
    set and `Active` deleted; the vocabulary comment rewritten for a complete set.
-   Docs: PRD §11.4 gains the reviewer subsection and the three reviewer variables; the README's `review
    submit` and `hooks` rows and the skill's `references/cli.md` name the points and the flag.
-   Tests: `internal/cli/hooks_submit_test.go`, the screen's delegation assertion in
    `internal/tui/submit_internal_test.go`, the refused-submission case beside it, and
    `internal/cli/hooks_submit_internal_test.go` for the text the two surfaces share.

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

**The screen owns the terminal, so a refusal travels as data rather than as a write.** While the review screen
is up, the program is painting the terminal and a write to stderr lands on a surface that is repainted over. So
the submission path does not print the hooks' text when a screen is listening: `app.hookPresenter` diverts it,
`screenSubmission` collects it, and `withHookText` appends the tail of it to the refusal, which the status area
wraps and holds until a keypress. `hookOutputLines` is the one definition of what a hook run's text is — a
failing hook always shows it, a passing one only under `$GIT_PAIR_HOOK_OUTPUT` — and both surfaces read it, so
the command and the screen cannot drift into disagreeing about when a hook is silent. `withHookText` caps the
appended text at 15 lines and 1200 runes and keeps the tail, naming `git pair review submit` as where the whole
of it is readable.

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
-   `TestRefusedSubmissionStaysOpenWithTheReason` (screen layer): a submission whose injected path returns an
    error leaves the session open with the file list back, submits nothing, and renders the whole refusal — the
    hook name, the point, and the staged-content clause — rather than swallowing a sentence too long for a
    status line. `TestHookOutputLinesIsTheTextBothSurfacesShare`,
    `TestScreenRefusalCarriesTheReasonTheHookGave` and `TestLongHookOutputKeepsItsTail` (cli layer): what a
    hook's text is, that the screen's refusal carries both the sentence and the text, and that the cap keeps the
    tail and says where the rest is.
-   Manual, over a pty sized 32×110, in a fixture whose `pre-submit` hook prints to both streams and exits 1:
    `git pair review submit --block` printed `running the suite` and `suite red: 2 failures` and exited 1 with
    nothing committed; `git pair review` then `s` then `b` showed the refusal sentence with `the hook printed:`
    and both lines beneath it, kept the file list and diff on screen, and `q` left the command at exit 0 with no
    review commit. The same fixture against the binary from before this change showed only the sentence in the
    status area, with the hook's text arriving mid-frame and gone on the next repaint — which is the defect the
    change is for, and why the transcript above is quoted rather than described.
-   `go test ./internal/cli/ -run Docs` and `-run TestTheCommandTreeIsTheCommandSet` green.
-   `mise run check` exited **0**: 36 test shards ok (31 served from cache, the rest re-run against the final
    tree), no `FAIL`, and its `gofmt -l internal cmd` step reported nothing. Run again after this file was
    written, with the same result.
-   Restacked onto M1's answers to the reviewer's feedback, twice — once for the two questions, once for the
    `trust_state` work that came out of the vendoring discussion — and re-run each time: exit 0, 36 shards ok,
    no `FAIL`, `gofmt` silent. Neither answer bears on this milestone's behaviour; both were answered from what
    the code already does, so no test changed. The reviewer-side trust rule did get sharper as a side effect of
    the second answer: a reviewer reviewing a repository they cannot edit is exactly the person who may be
    sitting on a `changed` tracked tree they never read, which is why the open question about unaccepted tracked
    hooks now names the reviewer. The two documentation rows collided with M1's new trust-state wording during
    the restack and carry both texts.

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

## Addressed feedback

### "what happens if the presubmit hook fails when you submit using the tui? does it exit with nonzero or show the error in the tui?"

The screen shows it and stays open. The nonzero exit belongs to `review submit`, which refuses with exit 1.

This is the review screen after `s` then `b`, driven over a pty in a fixture whose `pre-submit` hook prints and
fails. The file list and the diff are still on screen above the status area:

```text
hook "01-suite" at pre-submit refused the operation with exit 1

the hook printed:
running the suite
suite red: 2 failures
```

The reviewer's comments, their ABOUT edit and their staged files are untouched, and no review commit was
written — `git log` still ends at the implementation commit. Leaving with `q` returns 0.

Two decisions are in that shape, and the reviewer's question is the reason both are written down.

-   **The refusal ends the submission, not the session.** At the moment the hook refuses, the reviewer has work
    in progress: comments typed into threads, an ABOUT edit, source annotations staged for the review commit.
    An outcome key that closed the screen would discard it, and the reviewer would start the review again from
    nothing. What was refused is the verdict, so what stops is the verdict.
-   **The exit code belongs to the command, not the screen.** `review submit --block` ran, was refused, and says
    so to the shell with 1 — that is what automation reads. `q` is the ordinary way out of a screen that looked
    at a changeset and recorded nothing, and a nonzero code for it would report a business refusal that nobody
    made. `review submit --json` is the surface for a script that needs the hooks' outcomes too.

The question also exposed a defect, and that is the part that changed code. Before this change the hook's own
text went to stderr while the screen held the terminal. The same fixture produced this, in the middle of the
frame, and the next repaint covered it:

```text
Submit review: [b]lock  [f]eedback  [a]pprove  [esc] cancel
running the suite
suite red: 2 failures
──────────────────────────────────────────────────────────────────────────────────────────────────────────
hook "01-suite" at pre-submit refused the operation with exit 1
```

The reviewer was left with the sentence — which hook refused, and its exit code — and not the reason. Text
written to stderr during the alternate screen is not a surface anyone can read back, so the output now travels
as data: `app.hookPresenter` diverts the run's hook text from stderr, `screenSubmission` collects it, and
`withHookText` appends it to the refusal the status area renders. `hookOutputLines` is the single definition of
what a hook's text is, so the command and the screen cannot drift on when a passing hook stays silent.

The appended text is capped — 15 lines and 1200 runes, keeping the tail — with a line naming
`git pair review submit` as where the whole of it is readable. The tail is the part that survives because a
suite's verdict is at the end, and a lint run that dumped a megabyte would otherwise replace the review instead
of informing it.
