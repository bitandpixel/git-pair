# Adopt dev-profiles v3 and v9: the profile fix, and the CI shape it carries

## Why this exists

The four `review thread` mentions in the profile skills were fixed upstream in `dev-profiles` (`git-pair-loop` 2 → 3,
`git-pair-github-integration` 8 → 9, `git-pair-manual-integration` 2 → 3). This repository cannot take that wording
without taking `git-pair-github-integration` v9 with it: the library is vendored as one directory, and `doctor` fails
a checkout whose installed copy no longer matches the library it came from. v8 is what v9 sits on, and v8 retired the
profile's `toolchain` seam in favour of a composite action. So this changeset is the adoption the marker has been
holding back since upstream retired that seam — the profile fix is the reason it is now, not the reason it is possible.

## What becomes different

**The CI shape the runner sees.** Both jobs prepare the tree through a composite action this repository writes:

```yaml
      - name: setup
        if: ${{ hashFiles('.github/actions/git-pair-setup/action.yml') != '' }}
        uses: ./.github/actions/git-pair-setup
```

and that file's single step runs the script it ran before:

```yaml
runs:
  using: composite
  steps:
    - name: prepare the toolchain
      run: ./scripts/ci/prepare-toolchain.sh
      shell: bash
```

Same work, same position in both jobs — after the pinned release install, so the binary that gates the merge is this
tree's build, and `GIT_PAIR_BIN` still reaches the steps after it. What changed is that the preparation is now a step
GitHub resolves rather than a marker line it `eval`s.

**The agent-facing text.** `.agents/skills/git-pair-workflow/SKILL.md` and `git-pair-github-integration` now forbid
`review discussion` where they named the removed `review thread`, and `AGENTS.md` declares `git-pair-loop` v3 and
`git-pair-github-integration` v9.

**The marker.** `toolchain = "scripts/ci/prepare-toolchain.sh"` is gone from `[commands]`, and `doctor` reports the
seam answered where it matters: `setup:.github/actions/git-pair-setup action.yml is a composite action, and both jobs
call it before the work`.

**The library itself** is upstream `e4dacd0` byte for byte — the driver, the profile changelogs, the profile's new
`references/git-pair-setup.example.yml`, and the `planning` profile, which arrives as files and stays undeclared, so
nothing of it installs or is checked here.

## Design decisions

Adopted by extraction rather than by patch. `git -C …/dev-profiles archive origin/main .dev-profiles | tar -x`, after
removing the old directory, so the vendored tree is once again the exact bytes of one named ref. A hand-applied
four-line fix would have kept `doctor` green and made the next adoption overwrite something silently, which is the
failure mode the vendoring rule exists to avoid.

`scripts/ci/prepare-toolchain.sh` stays, and the action calls it, rather than its logic being transcribed into YAML.
The script is runnable where a composite action is not: a developer in a checkout uses it, and the gate replays use
it. Its header no longer claims to be a marker answer, and the action's header says why the split is where it is.

No cache step. The profile's starter offers one, and caching what the preparation downloads is a tuning decision
with its own failure modes — a key built from the check job's tree describes neither job. Neither runner had any
cache before this file existed, so leaving it out preserves the status quo rather than regressing it.

`README.md`'s CI paragraph named the retired seam, so it describes the action now; the `AGENTS.md` stanza was left for
`dev-profiles install` to rewrite rather than being hand-edited, which is what keeps it a fact about the marker.

## What is not changing

The jobs' order, the pinned release install, `verify = "mise run gates"`, the answered `ci-request`, and every other
byte of the profile-owned CI files beyond what upstream carries. `prepare-toolchain.sh`'s work is untouched — only its
header. The `planning` profile is not declared, so its three skills are not installed and not compared. `changesets/`
history that mentions `prepare-toolchain.sh` is left as written: those are records, not references.

## Costs and limits

**The one claim local verification cannot make is the action itself.** GitHub resolves `uses:` when it reads the
workflow, and a composite action is only ever exercised on a runner. `doctor` checks the two things it can see from a
checkout — the file exists, and it is a composite action — and the guard makes the interesting failure silent in
exactly the way the profile warns about: a wrong path or an `action.yaml` spelling means the step reports `skipped`
and the job runs an unprepared tree. So the first CI run of this changeset has to be read at its `setup` step, and a
`setup` that says `skipped` means the file is at the wrong path or under the wrong name, not that it is broken.

The adoption also imports three changelog entries and a driver update nobody here asked for, because they travel with
the version. That is the mechanism working, but it means this diff is wider than the wording fix that motivated it.

## Where to pay attention

`.dev-profiles.toml`: the two version lines, and the deleted `toolchain` answer. An answer under a key no profile
offers is a preparation that stopped happening while the job stayed green, which is the class of failure this profile
treats as fatal — so the deletion matters more than the addition.

`.github/actions/git-pair-setup/action.yml` and `scripts/ci/prepare-toolchain.sh` together: the split is the design
decision, and if you would rather the whole preparation live in YAML the script should go in the same commit.

Then the grep for the retired seam: `README.md` is rewritten, and the remaining mentions of `prepare-toolchain.sh` are
in `changesets/*/ABOUT.md`, which are history.

## Validation

`./.dev-profiles/bin/dev-profiles doctor` — no FAIL. It reports both skill pairs byte-matching the library, all four
CI files matching with their exec bits, the pointer stanza current, `capability:git-pair current at machine`,
`forbidden:git-workflow not discoverable`, and the setup seam answered: `action.yml is a composite action, and both
jobs call it before the work`.

`./.dev-profiles/bin/dev-profiles install --scope repo` was run rather than hand-copying, so the installed skills and
the `AGENTS.md` stanza are the tool's output. It reported overwriting the two workflow files and
`scripts/ci/git-pair-integrate.sh`, and installing the two skills.

YAML parsed with `python3 -c "yaml.safe_load(...)"`: the action is `using: composite` with one `bash` step, and both
workflows have a step named `setup`. The guard string `hashFiles('.github/actions/git-pair-setup/action.yml')` is
present in both files, and the only file on that path is `action.yml` — the spelling `doctor` fails when it is wrong.

`bash -n scripts/ci/prepare-toolchain.sh`, and `bash scripts/format.sh check`.

`mise run gates` — format, gofmt, vet, the sharded suite, then `e2e-29.sh`, `pty-walkthrough.sh` and
`ci-integrate.sh`. The last of those replays the merge job against the new `scripts/ci/git-pair-integrate.sh`, which
matters here because upstream added a dirty-tree guard to it; the interface is unchanged, no new flags, so the
repository's own replay drives it as before.

Not run: the action step itself, which needs a runner — see Costs and limits.

## Open questions

Should `prepare-toolchain.sh`'s downloads be cached now that the preparation is a real step? The profile's starter
suggests it; I left it out to keep this changeset's CI behaviour equal to yesterday's.

Should this repository declare the `planning` profile? Its three skills currently live at machine level
(`~/.pi/agent/skills/plan-audit`, `plan-execution`, `project-planning`), where nothing compares them. Declaring the
profile moves them to repo scope with a byte-comparison and a version to track; not declaring it leaves them as
developer-local copies of a profile this repository's library now ships.

## Addressed feedback

No review yet.
