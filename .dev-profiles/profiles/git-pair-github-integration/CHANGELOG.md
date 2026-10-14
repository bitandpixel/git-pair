# git-pair-github-integration profile changelog


## 3 — 2026-10-14

- `git-pair-github-integration` is now declared as a `[[router]]` entry with a `reason`, and the entry line in `AGENTS.md` carries
  that sentence: The pointer line now says why to open the skill, not just that it exists
- The driver no longer appends its own three sentences to the stanza. They described a hand-off by command and a
  gate separated from the merge — facts about this profile's workflow, printed into every repository's `AGENTS.md`
  including the ones that do not run it. The rules are not gone: they are in the skill this line points at.

## 2 — 2026-10-14

**Ask the suite of the tree the destination would become, not of the branch head.**

- The shipped job merges locally, runs `[commands] verify` against that merge commit, and pushes only if the suite
  is green. A failing verification puts the local destination back, merges nothing, and exits 1.
- This is the one refusal in the job that is never a skip. A probe can answer "not yet", and a job that treats
  "not yet" as permission trains people to ignore the build; here there is nowhere else to show an author a failing
  candidate, so a non-zero answer is always a refusal.
- One CI declaration, `ci_files`. It names the bytes this profile owns in the consumer tree and, in the same pass,
  what the destination is obliged to keep — a job that is not on trunk cannot verify a merge candidate. A file the
  profile needs but does not own is demanded as a `[[require]]` role instead, never listed as a path it does not own.
- The driver is upstream git-pair's own `scripts/ci/git-pair-integrate.sh`, carried byte-for-byte from tag `v0.5.0`
  with nothing patched on top, and `scripts/ci/install-git-pair.sh` defaults to that tag. The candidate shape is this
  profile's choice, so it lives in the workflow file the profile owns rather than in a fork of the tool's script.
- The gate the job obeys is git-pair's, read whole: `git pair check --json | jq -e '.ready and .integrating'`.
  `ready` answers "may this be merged"; `integrating` answers "did the author ask". The profile asks for the merge and
  never performs one.

## 1 — 2026-10-14 (documented, never shipped to a consumer)

**The head-green model, recorded so it can be brought back.**

- v1 was the per-commit model: Actions runs were created per commit, and the integrate job merged an
  integration-ready tip only when that commit's builds were all green — the driver's `--require-ci` with
  `scripts/ci/gh-head-green.sh` as the probe, plus `--ci-request` to ask the destination to test the new tip.
- It was replaced because it proved the wrong tree. Green builds on a branch head say nothing about the merge
  commit the destination is about to acquire, and it paid for that in a run per commit.
- Nothing adopted v1: this profile is new, so this entry documents a design rather than a migration, and there is no
  version gate to satisfy. The tree that carries it is tagged, and upstream `v0.5.0`'s driver still accepts every
  flag v1 used:

  ```bash
  git checkout ci-integration-v1 -- \
    .github/workflows/git-pair-integrate.yml \
    scripts/ci/gh-head-green.sh
  ```

  then pass `--require-ci` to the driver in the job's step.
