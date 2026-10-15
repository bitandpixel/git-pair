---
name: pair-preview-pr
description: Use when this repository deploys a running preview environment from a pull request — open a draft pull request purely to deploy it. This draft is not the review of the change, not the thing you wait on, and not the way it merges; how a repository reviews and merges its pull requests is another profile's answer.
---

# Preview environments from a pull request

Some repositories deploy a preview environment from a pull-request event: pushing a branch, opening the pull
request, and the repository's preview pipeline builds and serves something you can point a browser at. Where that
exists, a draft pull request is the cheap way to get one.

**Never guess a preview URL.** The host name, the build tag pattern and the deployment mechanism are facts about this
repository. Read them from the repository's own documentation or from its preview workflow, and if neither says, say so
and stop. A preview URL that is guessed looks exactly like one that is real.

What it is and is not:

| It is                   | It is not                                                                    |
| ----------------------- | ---------------------------------------------------------------------------- |
| a deploy trigger        | the review of the change                                                       |
| a stable URL per change | the thing you wait on — a finished deploy tells you the preview exists, nothing more |
| a draft, always         | the way the change merges — that belongs to whatever profile governs merging here |

## Opening one

```bash
git push -u origin "$(git branch --show-current)"
gh pr create --draft --title "<intent>" --body "Preview only. This pull request is not the review."
```

`gh` bases it on the repository's default branch. Record that the preview pull request exists, and its number, in
whatever this repository uses as the record a reviewer reads before opening a change — the reviewer should not have to
discover it, and someone reading that record later should not have to work out whether one existed.

## What does not happen here

- `gh pr merge`, `gh pr review`, or asking a reviewer to look at *this* pull request. Review and merge belong to
  whatever profile governs them in this repository, and that may well be a pull-request one. The point is that this
  draft is not it.
- Treating a green PR check run as proof the branch is green. A pull-request run is attached to the pull request's
  merge ref rather than to the commit you pushed, and it is not necessarily the suite this repository requires before a
  merge. It tells you the preview built.
- Leaving the PR open after landing with nothing said about it. Close it, and note that you did in the same record that
  named the PR.

## Long term

Branch-push deploys would remove the pull request entirely, but they need a stable preview identity — one
environment per change rather than one per push — which is not what allocates environments today in the repositories
that have this mechanism. That exploration is deferred.
