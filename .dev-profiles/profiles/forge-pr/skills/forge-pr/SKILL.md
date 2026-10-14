---
name: forge-pr
description: Use for any change in this repository — feature branch, commits at milestones, a draft pull request, checks and reviewers on GitHub, and a human merges. Replaces general Git workflow advice.
---

# Pull-request workflow

Work reaches `main` as a pull request that a human approves and merges. The forge is the review surface, and
nothing in this workflow is local-only state.

## Rules

- Never implement on the default branch unless explicitly told to. Check the branch and status first.
- Create a feature branch before changing anything if one does not exist. Find the real default branch from
  the repository rather than assuming `main` or `master`.
- Cut the branch from the remote's current tip, not from a local copy that may be behind:
  `git fetch origin && git switch -c <branch> origin/<default>`. A branch cut from a stale local trunk drags
  other people's commits into your diff, and they land with your change.
- Commit at meaningful milestones, with messages that describe the intent rather than the files touched.
  Titles and bodies become PR titles and descriptions.
- Push the branch when work is complete, when handing off, when blocked, and when the session is ending.
- Open PRs with `gh`; keep the base explicit.
- The PR body is the review record, not a commit log. Write it against the change-description skill
  (`.agents/skills/change-description/SKILL.md`): the reviewer's seven questions, observable behaviour before
  implementation, one concrete example, and the sections that add nothing left out rather than filled.
- Open as a draft and flip to ready when the layer is done — a draft can never merge.

## Sizing

One branch, one PR for a single concern. More than one reviewable concern becomes a stack — see the
**stack-gh** skill. Roughly 50–200 changed lines per layer; more than 250 lines or 25 files is a signal to
split. Split where concerns change. Ship tests with the layer that exercises them.

## Verification before you call anything green

Never call a layer green from a command exit code. Confirm base and head, changed files, draft-vs-ready state,
and that layer's own checks after pushing. `gh pr checks` output beats a remembered assumption, and a
`Sync aborted` message can still come with exit 0.

## What is not yours

- Do not approve your own PR, and do not merge unless the repository says the merge is yours. Merging is a
  publishing action.
- Do not force-push over commits a reviewer has read without saying so in the PR.
- Do not close a mid-stack PR — it blocks everything above it.
