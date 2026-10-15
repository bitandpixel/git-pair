---
name: gh-cli
description: Use when driving GitHub from the command line with gh — auth and repository state (gh auth status, gh repo view), pull requests (gh pr view, checks, create), CI runs and job logs (gh run list, view), issues (gh issue list, view), code and file lookups (gh search code, gh api), and check-run or status queries (gh api --paginate).
---

# Driving GitHub with `gh`

Driving `gh`. It says nothing about how a change should be reviewed or where it lands; it covers the
mechanics of asking GitHub questions and the answers that mislead.

## Before anything that needs the network

```bash
gh auth status            # account, protocol, and token scopes. Not `gh api user` — it hides scope problems
gh repo view --json defaultBranchRef -q .defaultBranchRef.name
```

Never hardcode the default branch. Read it: repositories disagree about `main` versus `master`, and a script
that guesses merges into the wrong place.

## Parse, do not eyeball

| Need                   | Do                                                                     |
| ---------------------- | ---------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Any list or field      | `--json <fields>` plus `--jq`, or `gh api … --jq`                      |
| A paginated collection | `gh api --paginate`; `check-runs` and `statuses` are both paginated    |
| Job wall time          | `gh run view <id> --json jobs -q '.jobs[]                              | {name, conclusion, startedAt, completedAt}'`— the fields are`startedAt`/`completedAt`, not `started`/`completed`, and `run_duration_ms` is absent from the runs list response |
| Presence of a check    | `gh api "repos/$GITHUB_REPOSITORY/commits/$sha/check-runs" --paginate` |

A commit status and a check run are two different things and a repository can use both. Asking one and
concluding "green" about the commit answers a narrower question than was asked.

## Two gotchas that cost real money

**Checks on a pull-request run are attached to the PR's merge ref, not to the branch head.** A head that
"passed CI on the PR" is not evidence about the commit on the branch. When a decision depends on _this commit_
being tested, read check runs for that sha, and note that check names arrive composite — `CI / Test`, not
`Test` — so match what the API actually returns.

**Nothing having run is not a pass.** A commit with zero check runs has not been verified. Treat "no checks"
as "cannot tell" and stop, because the ordinary case for that answer is a head nobody tested yet.

## Ask first

Anything the forge records durably: `gh pr merge`, `gh pr review --approve`, `gh release create`, `gh pr close`,
deleting a branch, force-pushing, changing protection rules or workflow permissions. Opening a draft PR is
usually fine when the repository's workflow says previews come from PRs; merging is not your call by default.

Never approve your own work, and never treat an exit code from `gh` as a review verdict.

## Issues, code, and one-off reads

| Question                                      | Command                                                                                      |
| --------------------------------------------- | -------------------------------------------------------------------------------------------- |
| What is open, and what is on it?              | `gh issue list --state open --json number,title,labels`, then `gh issue view <n> --comments` |
| What does this file contain, without a clone? | `gh api "repos/$GITHUB_REPOSITORY/contents/<path>" --jq .content \| base64 -d`               |
| Where is this symbol used across the org?     | `gh search code "<term>" --repo <repo>` — it indexes the default branch only                 |
| Anything the CLI has no verb for              | `gh api <path>`, and `--paginate` whenever the endpoint is a list                            |

`gh api` prints the raw endpoint shape. When a field is missing, `--jq` failing quietly is the usual answer:
check `gh api <path> --jq keys` before assuming the response is what the docs showed.

## Failure shapes worth recognizing

- `403` from a protection endpoint on a plan without branch protection means the feature is unavailable, not
  that permissions are missing.
- A workflow's `contents: write` in YAML is not enough if the repository's Actions settings hand out a
  read-only token; the failure shows up at the push, not at the gate.
- `gh run list` shows old runs from other branches unless you filter; do not infer "CI is green" from the most
  recent row.
