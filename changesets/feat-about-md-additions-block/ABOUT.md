# ABOUT.md additions block change ready

## Summary

A reviewer's edit to `ABOUT.md` now blocks `git pair change ready` the way a comment in an implementation
file does. The rest of the changeset directory is conversation surface and stays reported-but-forgiven, so a
reply appended to a thread still cannot block a hand-off.

This is the first of two changesets. The second removes review threads and puts the conversation in one
`changesets/<id>/DISCUSSION.md`. Splitting them keeps the review of one rule separate from the review of one
feature, and the two share no code.

## What changed

`survival.isArtifactPath` had one rule: anything under `changesets/` is review state, so nothing there ever
blocked. It now reads the file name as well, and `ABOUT.md` is not review state.

The refusal gains the sentence the rule needs. "Apply it, then remove the comment it came from" is the right
advice for a comment and the wrong advice for the reviewer's sentence standing inside your own description, so
the report now says to keep the description you want in your own words. The override line below it is what says
so when the author meant to keep their wording.

## Example

The reviewer adds one source comment and a heading with a sentence:

```text
Cannot mark changeset booking-transaction ready.

3 additions from review 332887c still survive unchanged:

src/service.ts:10
  // Please use a transaction here

changesets/booking-transaction/ABOUT.md:12
  ## Review note

changesets/booking-transaction/ABOUT.md:14
  The concurrency story needs to be explicit.
```

Two exits, both tested. Removing the reviewer's lines clears it, and so does rewriting the point as the
author's own text, because the check compares lines exactly.

## Design decisions

**Keyed on the file name, not on the active changeset's directory.** `changesets/<id>/ABOUT.md` for a changeset
the command is not running, and a description under `.landed/` or `.combined/`, block too. The alternative —
plumbing the active slug into `Check` — makes the guarantee depend on the shape of the tree, so a description
becomes review state the moment `change tidy` moves it. A reviewer editing a landed changeset's description is
rare, and blocking is the honest answer when it happens.

**The gate was left alone, deliberately.** `git pair check` refuses when anything outside `changesets/<slug>/`
moved since the marker, and it still does. Making `ABOUT.md` drift too would refuse a merge over prose, and a
survivor the author acknowledged with `--allow-surviving-review-additions` already lands unverified today, in
`src` as much as here. So `ABOUT.md` now has exactly the guarantee `src` has, which is the point of treating it
like any other file. The comment at `integrationReasons` and PRD §19.3 said the drift test "guarantees that
nothing has moved since the marker"; with this change that sentence is only true of the implementation, so both
now say what is left out rather than implying it is covered.

## What is deliberately not changing

`git pair check`, the submission identity digest (`changeset.DigestExclusions`) and approval staleness
(`lifecycle.ReconcileStaleness`) all still exclude the changeset directory, so replying in the changeset
directory can neither invalidate an approval nor make one stale. `--allow-surviving-review-additions` keeps its
all-or-nothing shape: one `ABOUT.md` line you want to keep lets every unaddressed comment through with it.

## Where to look hardest

The consequence above is the thing to disagree with if you are going to. A review that edits the description and
approves in the same submission is legal, so its reviewer text is at `HEAD` with no later `change ready` to
settle it, and it lands as written. The alternative considered was to refuse `--approve` on a submission that
adds `ABOUT.md` lines, which closes that path at the cost of a round trip every time a reviewer fixes a sentence
on the way to approving. That felt too restrictive for a first cut and is deferred, not rejected.

## Validation

`mise run check` — markdown format, `gofmt`, `go vet`, then the sharded suite.

The rule is pinned at three levels. `TestIsArtifactPath` is the table, including a landed and an archived
description. `TestCheckAboutOnlySurvivalBlocks` runs against real commits and asserts a review whose only edits
are descriptions blocks, and that a touched conversation file would have been forgiven.
`TestChangeReadyBlockedByReviewerTextInAbout` drives the CLI through refusal, checks the new sentence, and then
passes on the rewrite. `TestCheckChangesetArtifactsAreNonBlockingAndCodeBlocks` is the joint case: one commit
settles the code and the description, and the answered thread's lines still survive without blocking.
`TestPRDTwentyNineGoldenWorkflow` now settles the description on the golden path, and its refusal assertion
counts three survivors rather than one.
