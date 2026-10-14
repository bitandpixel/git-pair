# Adopt the declared workflow in git-pair's own repository

## Why this change exists

`dev-profiles` claims that nothing under `.dev-profiles/` may name a particular repository, its forge, or its CI, and
that claim has only ever been tested by the repository that develops the library. This is the second repository: a
git-pair checkout with `changesets/`, Review-State trailers and approval commits, and no marker, no `AGENTS.md`, and no
declaration of what workflow it runs. Adopting here is the cheapest available proof that the library is a library, and
the cheapest place to find out what a real consumer finds awkward.

## What was done

- Vendored `.dev-profiles/` from dev-profiles `52ba773` (`git archive … | tar -x`), which is the adoption command the
  README states, used here as written.
- `.dev-profiles.toml` declares `git-pair-loop` v2, requires the `git-pair` capability at machine scope and
  `change-description` at repo scope, and forbids the `git-workflow` standard.
- `AGENTS.md` is written by this repository with a four-line project header; the `dev-profiles:profile` stanza inside it
  is the driver's, written by `install`.
- `install` was run and the installed copies committed, as the library requires: `.agents/skills/git-pair-workflow/`,
  `.agents/skills/pair-stack/`, `.agents/skills/change-description/`.

## What was considered and rejected

**Declaring `git-pair-github-integration` too.** Rejected, and the reason is the substance of this change record. That
profile does not only promise a verified merge — it *ships* the job: the workflow, the integrate driver and the
installer, installed and byte-compared. In this repository those three paths are product code. The integrate job here
builds the tool from the tree under review (`mise run build`, then `git-pair-integrate.sh --expect-head "$PUSHED"`), so
a job that installs a published release could not merge anything reviewed here: the release does not contain the change.
`install` would have overwritten `.github/workflows/git-pair-integrate.yml` — 211 lines of product code against the
profile's 199 — and `doctor` would then have certified the consumer's copy as what this repository promises. The
integrate driver is identical (`scripts/ci/git-pair-integrate.sh`, 415 lines against 415, no difference), which is what
the verbatim-carry was for; the workflow and the installer are not, and neither belongs in a repository that owns them.

**Adopting `preview-pr`.** No preview environment deploys from a pull request here.

## What this leaves undeclared, which the library should fix

Nothing here states that a job verifies the merge candidate before it pushes. The mechanism for "the consumer provides
this and the profile does not own the bytes" is `[[require]]`, answered in `[commands]` — and that answers a *command*,
not a workflow file. A repository that owns its integration job therefore has no way to make `doctor` watch the promise
it depends on: `.github/workflows/git-pair-integrate.yml` is guarded today only by this repository's own
`scripts/gates/` suite and by review. The marker records the gap where a reader will look for it; a follow-up change in
dev-profiles should close it, most likely by letting a profile demand a path it does not own, distinct from shipping
one.

## What you can run

```bash
./.dev-profiles/bin/dev-profiles doctor --scope repo    # exit 0, no FAIL
./.dev-profiles/bin/dev-profiles install                # second run writes nothing
```

Nothing under `.github/` or `scripts/` changes, so this cannot affect the job that merges it.

## What it breaks, and how to know

Three installed copies become committed files, and `doctor` byte-compares them against the vendored library from now on.
The machine-scope `git-pair` skill is stale against the binary on this machine (`install` reports the three files that
differ and refuses without `--force`); that is developer state, `doctor --scope repo` does not evaluate it, and the fix
is `git pair skill install --harness agents --scope user --force`, which is the operator's call, not this commit's.

## What is not done

A second overlap is visible but untouched, because deciding it is not mine to make alone: `git pair init` inserts its
own `## git-pair` block into `AGENTS.md` (from `skills/agents-md.md`) carrying three rules — the handoff is a command,
feedback is how a review is read, and so on. The router line in the dev-profiles stanza now carries the same advice from
the profile side, which is exactly the duplication the driver's own three sentences were removed for. A repository that
runs both tools gets the same instruction from two owners, and only one of them is versioned with a changelog. Which one
owns that block is a product decision for git-pair; this change writes no block of that kind, so nothing conflicts yet.
