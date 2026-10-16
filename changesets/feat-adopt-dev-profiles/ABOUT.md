# Adopt the declared workflow in git-pair's own repository

## Why this change exists

`dev-profiles` claims that nothing under `.dev-profiles/` may name a particular repository, its forge, or its CI, and
that claim has only ever been tested by the repository that develops the library. This is the second repository: a
git-pair checkout with `changesets/`, Review-State trailers and approval commits, and no marker, no `AGENTS.md`, and no
declaration of what workflow it runs. Adopting here is the cheapest available proof that the library is a library, and
the cheapest place to find out what a real consumer finds awkward.

## What was done

- Vendored `.dev-profiles/` from dev-profiles `60567f4` (`git archive … | tar -x`), which is the adoption command the
  README states, used here as written. That is 31 commits ahead of the `52ba773` this branch first carried, and the
  difference is what makes the rest of this stack possible: `git-pair-github-integration` is at v5, it ships two
  workflow files rather than one, and it offers two seams — `toolchain` and `ci-request` — that a consumer answers
  from its own tree. The changed paths are the driver, that profile's manifest, changelog, skill and integrate
  workflow, plus the new `git-pair-check.yml`; the installed copies this repository commits are unchanged, because
  only the loop profile is declared here.
- `.dev-profiles.toml` declares `git-pair-loop` v2, requires the `git-pair` capability at machine scope and
  `change-description` at repo scope, and forbids the `git-workflow` standard.
- `AGENTS.md` is written by this repository with a four-line project header; the `dev-profiles:profile` stanza inside it
  is the driver's, written by `install`.
- `install` was run and the installed copies committed, as the library requires: `.agents/skills/git-pair-workflow/`,
  `.agents/skills/pair-stack/`, `.agents/skills/change-description/`.

## What was considered and rejected

**Declaring `git-pair-github-integration` too.** Not in this layer, and when this branch was first offered that was a
rejection rather than a sequence. The argument was sound then and is worth keeping in plain sight, because it is the
reason the library grew what it grew: that profile does not only promise a verified merge, it _ships_ the job — the
workflow, the integrate driver, the installer — and byte-compares them. The paths it ships are product code here. And
the integrate job in this repository builds the tool from the tree under review (`mise run build`, then
`git-pair-integrate.sh --expect-head "$PUSHED"`), which a job that installs a published release cannot do: the release
does not contain the change under review.

What has changed is upstream, not this repository's needs. The profile now runs the consumer's own build step after the
pinned install and before the suite, in both jobs, from an answer the consumer gives at the `toolchain` seam — so the
binary that gates the merge is the one the tree produced, and the release the installer put there is simply what gets
replaced. That is the whole objection, answered at the seam the objection defined. Declaring the profile is therefore
the next layer in this stack, where it can be reviewed as the decision it is; this layer only moves the bytes that make
it possible. The driver this repository vendors and the driver the profile ships remain byte-identical
(`scripts/ci/git-pair-integrate.sh`, 415 lines against 415).

**Adopting `preview-pr`.** No preview environment deploys from a pull request here.

## What this layer leaves, and what the next one takes

`doctor` says it in one line, and the line is accurate for this layer: _the declared profiles declare no CI files —
nothing lands from CI_. So the promise that a job verifies the merge candidate before it pushes is still carried by
this repository's own `.github/workflows/` files, watched by `scripts/gates/ci-integrate.sh` and by review, and not by
`doctor`. The layer after this one moves that promise to the profile, which is what `ci_files` and the byte comparison
are for, and the marker paragraph that explains the gap goes away with it.

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
