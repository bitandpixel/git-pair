---
name: plain-git
description: Fallback for Git work in a repository that declares no development-workflow profile. Branch, commit at milestones, push before stopping; ask before any forge action. Do not use when the repository carries a .dev-profiles.toml.
---

# Plain Git work

For a repository that has not declared a workflow. It states the floor, not a review process.

## Do

- Check the branch and status before changing anything, and work on a named branch rather than the default one.
- Commit at meaningful milestones with messages that describe intent.
- Leave the repository recoverable before stopping: useful work committed, and the branch pushed.
- Say plainly what remains, so the next session does not have to reconstruct it.

## Ask first

Anything a forge would record — opening or merging a pull request, pushing a tag, publishing a release,
closing an issue, or any irreversible or destructive operation. Asking is cheap; undoing is not.

## What this skill deliberately does not claim

It has no review protocol, no change record, and no verdict to check, because the repository declared none. If
a change here needs a human read before it lands, that is the moment to declare a workflow, not to improvise
one: run `dev-profiles status` to see what this machine offers, and ask which profile the repository wants.

A repository may also already be following a workflow it never declared — a `changesets/` directory is one sign.
Mention it rather than guessing, and let the owner decide whether to declare it.
