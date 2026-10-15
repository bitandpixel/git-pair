# Alternative workflows

Listed here so an override is a read rather than a reinstall. Each path is a file in this repository, whether
or not that profile is installed.

| Profile     | Assumes                                                                     | Skills                                     |
| ----------- | --------------------------------------------------------------------------- | ------------------------------------------ |
| `git-pair`  | a human reads every change before it lands; review lives in git history     | `.dev-profiles/profiles/git-pair/skills/`  |
| `forge-pr`  | GitHub is the review surface, and checks and reviewers are configured there | `.dev-profiles/profiles/forge-pr/skills/`  |
| `plain-git` | nothing special: branch, commit, push, and ask before touching the forge    | `.dev-profiles/profiles/plain-git/skills/` |

## How to switch

Only when a person asks. Nothing in a session's own reasoning selects an alternative.

1. Read that profile's entry skill — the path above, `SKILL.md` inside it.
2. Follow it for the change you were asked to make.
3. Record the switch in the **change record** for the workflow you were following: what was overridden, who
   asked, and what the change therefore skipped. Under `git-pair` that is `changesets/<id>/ABOUT.md`.

An override that leaves no trace is how a workflow quietly stops being the workflow.

## What stays true when you switch

- Never approve your own work, whatever surface the review happens on.
- Never merge unless the repository's workflow says the merge is yours.
- Report state from the tool that derives it, not from a cached file or a remembered answer.
