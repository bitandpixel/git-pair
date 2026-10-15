# git-pair-github-integration profile changelog


## 7 — 2026-10-15

`install-git-pair.sh` tells the release installer where to write. It chose a directory, fetched the pinned release's
installer into it, and ran that installer with no destination — so the installer wrote the binary to its own default,
`~/.local/bin`, and the next line exec'd `$INSTALL_DIR/git-pair --version`, a file that had never been written
there: exit 127, before the gate ever ran. Passing `--install-dir "$INSTALL_DIR"` makes the two halves name one
path. No text assertion could see it — one of them reads the version line and is satisfied by its wording — because
the command was correctly written and the file it execs was simply elsewhere.

`tests/ci-integrate/run.sh` now drives the script with `curl` stubbed and `$HOME` moved into the scratch directory,
against an installer that honours `--install-dir` and one that ignores it: the first must leave the binary where the
script says and print its version, the second must make the script fail rather than report a version it never
installed. Both defects in this changelog were found the same way — the first consumer to run the job for real, on a
repository that could afford to.


## 6 — 2026-10-15

Both jobs can be scheduled. `timeout-minutes` read `${{ vars.GIT_PAIR_TIMEOUT || '30' }}`; a repository variable
arrives as text, that key wants a number, and GitHub does not coerce the two — it fails the run before creating any
job. On a push carrying a declaration that meant the merge job, the one that has to start, died with no job, no
steps and no log, while the check job on the same push was refused by its `if:` — so its identical expression was
never evaluated — reported `skipped`, and looked healthy. `fromJSON(… )` makes the value a number. A
`GIT_PAIR_TIMEOUT` that is not an integer now fails the run rather than being coerced into a ceiling nobody chose.

The first consumer to run these files outside the library's fixtures is what found it: a repository declaring this
profile got a merge job that could never start, and no case in the suite could see it, because every existing
assertion read the file as text and none validated it the way GitHub does. `tests/ci-integrate/run.sh` now runs
`actionlint` over both files when the machine has it installed, and prints the reason it skipped when it does not.


## 5 — 2026-10-15

The shipped job is two files now, and the file the forge ask pokes can no longer land work.

- `.github/workflows/git-pair-check.yml` holds the check: the pull-request preview, the run a hand push to the
  destination gets, and the dispatched run the ask makes. It holds `contents: read` and nothing else, so it cannot
  certify a head and cannot be required-and-absent in the way the old check-run allowlist was written to catch.
  `.github/workflows/git-pair-integrate.yml` keeps the merge alone, with its own `concurrency` answer: a superseded
  check cancels freely, a superseded merge never does.
- Splitting them is what makes the ask safe by construction. The ask runs a workflow by name, and the file it names
  now merges nothing rather than merely agreeing not to. The alternative was a `mode` input on one file, which puts
  that safety on something somebody types.
- Neither job needs the other's trigger, and separating the trigger sets saves a started job per pull request: the
  merge job is no longer started and skipped beside every check. A branch push still costs two skipped jobs, as
  before, because a trigger cannot read `vars` and the destination branch is a variable.
- The post-merge report asks `"${GIT_PAIR_BIN:-git-pair}" queue` rather than `git-pair queue`. The driver already
  honoured that variable, so a consumer that builds the tool from the tree under review was merging with its own
  build and then reporting from the release the installer had fetched.
- The reasoning that spans both files — the triggers, the timeout and its variable, what each scope is for and why
  the ask's is always granted — moved into the profile's skill, where it is written once.

`install` writes a new file and rewrites the existing one; commit both. A consumer that runs its own check workflow
can now delete it, which is the reason this lands before that repository declares the profile.

## 4 — 2026-10-14

- The integrate workflow gained two seams a consumer answers from its own marker, and a ceiling on how long it may run.
  Both seams exist because this profile owns those bytes and byte-compares them, so a repository that differed could
  only fork the file — which is what the tool's own repository had ended up doing.
- `toolchain` is what prepares the tree before it is tested. It runs in both jobs, after the pinned install, so a
  repository that builds the tool from the tree under review is not overwritten by a published release that cannot
  contain it. The empty answer is legal and says so in the log; a misspelled answer fails `doctor` at the handoff,
  because the failure an optional step invites is the job skipping its own preparation and reporting green.
- `ci-request` asks the forge to run the destination branch after a landing, and the merge job now holds
  `actions: write` for it — the only new scope. Job permissions are static YAML, so the scope is granted whether the
  role is answered or not; the alternative was a second job, which would have duplicated the ask outside the driver,
  lost the refusal the driver reports inline, and cost a started job on every landing.
- `timeout-minutes` defaults to 30, overridable per repository by the `GIT_PAIR_TIMEOUT` variable. The default belongs
  to the profile so that no consumer can turn the ceiling off — an overnight hang is the one thing that turns an
  unused automation into a bill. It is a variable rather than a marker key because GitHub compiles the job before any
  step runs, and `vars` is the only context that key may read.

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
