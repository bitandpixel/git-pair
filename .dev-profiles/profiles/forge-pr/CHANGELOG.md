# forge-pr profile changelog

## 1 — 2026-10-01

The workflow that the machine-level `git-workflow` skill used to prescribe for every repository, narrowed to
repositories that choose it, and split in two:

- `forge-pr` — branches, commit discipline, sizing, opening PRs, verifying a layer is really green.
- `stack-gh` — the `gh stack` mechanics, kept intact, including the gotchas that cost branches: `modify` is
  TUI-only, `rerere` is not enabled for you in gh-stack v0.1.1, pushes are non-atomic, and a layer is never
  green just because a command exited 0.

Nothing here changed behaviour for any repository: it moved from an always-on global skill to one a repository
installs deliberately.
