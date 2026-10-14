# Profile: forge-pr

Work reaches the destination branch as a GitHub pull request: checks run on the forge, a human approves there,
and a human merges there.

## When this profile fits

- The review surface is GitHub, and the team reads it there rather than in the repository.
- Branch protection, required checks, CODEOWNERS, or preview deploys are configured against pull requests.
- Stacked layers are useful and the linear-history constraint is acceptable.

## Roles

| Role     | Who               | Does                                                                        |
| -------- | ----------------- | --------------------------------------------------------------------------- |
| Author   | usually the agent | branches, commits, pushes, opens the PR as a draft, answers review comments |
| Reviewer | a human           | approves on the forge                                                       |
| Merger   | a human           | merges. Needs authorization; merging is a publishing action                 |

## Skills here

- `forge-pr` — branches, commit discipline, sizing, opening PRs, verification.
- `stack-gh` — the `gh stack` mechanics for layered work.
- The `gh-cli` capability covers the `gh` command mechanics themselves.

## Relationship to the git-pair profile

These are alternative routes, not layers of one route. A repository picks one. If someone asks for the other
route for one change, read the other profile's skill, follow it, and record the switch in this workflow's
change record — the pull request description.
