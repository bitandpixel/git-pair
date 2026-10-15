---
name: stack-gh
description: Use with GitHub pull requests when one change should be split into several reviewable layers — creating the stack, ordering it by dependency, and restacking the layers above after a lower one changes. The `gh stack` extension over GitHub native stacked PRs. Not for a single pull request, and not for git-pair changeset stacks.
---

# Stacked pull requests

`gh stack` (the `github/gh-stack` extension over GitHub's native stacked PRs). Exact flags and exit codes:
`gh stack --help` and the extension's docs — this skill carries the rules that cost people branches.

```bash
gh stack init <topic>/<concern> --base <trunk>   # bottom layer; also adopts existing branches
gh stack add <topic>/<concern>                   # next layer — commit or stash first
gh stack submit --auto                           # push branches, create/update PRs as drafts
gh stack up | down | top | bottom | trunk        # navigation
gh stack rebase --upstack && gh stack push       # after editing a lower layer
gh stack sync --prune                            # after lower layers land
```

## Plan the layers before writing code

- Design the layers first and create the stack at the start. Splitting one finished branch later means
  `unstack` + `init`; there is no non-interactive reorder.
- Order by dependency, foundations at the bottom. A layer depends only on its own commits and layers below.
- Set the trunk explicitly: branch protection, required checks and CODEOWNERS are evaluated for every layer
  **against the stack base**, not the branch below it.
- Name layers `<topic>/<concern>` unless repository conventions say otherwise.

## Mechanics worth knowing

- Always pass explicit arguments. Bare `init`, `add`, `checkout`, `submit` and `merge` open a TUI and hang in
  a headless session.
- **Never invoke `gh stack modify`** — it is TUI-only. To restructure: `unstack` → `init --base <trunk> b1 b2 b3`
  → `submit --auto`.
- Turn on `git config rerere.enabled true` per repository. The same conflict repeats on every layer above a
  low change. GitHub's docs claim `init` does this for you; in gh-stack v0.1.1 it does not.
- Prefer local `rebase` + `push` over the web "Rebase stack" button: server-side rebases reset the committer
  and produce unsigned commits, which breaks signed-commit requirements.
- On multi-remote repos pass `--remote <name>` to `push`, `submit`, `sync`, `rebase` and `link` unless
  `remote.pushDefault` is set. Pushes are per-branch `--force-with-lease` and are explicitly **not atomic** —
  re-run after a partial rejection.
- Fix review feedback in the layer that owns it, then cascade upward. Editing a lower layer and restacking are
  one unit of work; pushing out of turn breaks every layer above.
- Do not hand-rebase after a squash merge; `gh stack sync` detects it and replays with `--onto`.
- Landing a stack needs `gh stack merge <stack-number | pr-number> --yes`. Bare `gh pr merge` cannot land a
  stack, and merging needs authorization.

## Availability

Stacked PRs are public preview and the extension is 0.x: verify syntax against the docs before trusting it, and
re-check whether preview limitations (auto-merge, ruleset bypass) have changed. Requires gh 2.90+ and Git 2.20+.
No cross-fork stacks; history between layers must stay linear.

Where `gh stack` is unavailable, keep the same decomposition discipline with sequential branches —
`gh pr create --base <previous-branch>`, `git config rebase.updateRefs true`, `rerere` — or with `git-spice`,
`spr`, or Graphite if already installed. Each has its own restack vocabulary, so bind the rules to the tool in
use rather than mixing vocabularies.
