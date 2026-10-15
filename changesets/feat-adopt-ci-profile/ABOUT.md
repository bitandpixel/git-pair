# Let the profile own the two files that run this repository's CI

## Why this change exists

The parent layer moved the vendored library to the commit that carries the `toolchain` seam. This is the decision that
seam was built for, and it is the reason the library grew it.

Two files decide how work lands here: the ladder that runs on a pull request, and the job that merges a declaration
after testing the tree the destination would become. This repository wrote both, guarded both with assertions in
`scripts/gates/ci-integrate.sh`, and left `doctor` silent about the promise they carry — while the library shipped
copies of the same shape for consumers to install. Two owners, one shape, and no check that they still agree.
Declaring `git-pair-github-integration` makes one copy authoritative, and this repository is the only consumer whose
copy is also where the library's came from, which is the position from which drift is actually visible.

## What becomes different

`.github/workflows/ci.yml` (86 lines) and `.github/workflows/git-pair-integrate.yml` (211) are gone. In their place
are the profile's `.github/workflows/git-pair-check.yml` (112) and `.github/workflows/git-pair-integrate.yml` (239),
plus `scripts/ci/install-git-pair.sh` (56) as a new file and `scripts/ci/git-pair-integrate.sh` (415) unchanged — it
was already byte-identical to the profile's, which is what the verbatim carry bought. All four are now
`install`-written and `doctor`-byte-compared, and the exec bit of the two scripts is checked too.

The merge is now conditional on a command the marker names (`mise run gates`) rather than a command line embedded in
YAML, the build that produces the gating binary is the consumer's own step rather than an action step, and the ask
that follows a landing targets a file by path. None of that changes what a green merge means here; it changes who owns
the sentence that says what it means.

## What that looks like

A declaration, pushed:

```bash
git pair change integrate && git push origin feat/some-work
```

GitHub reads the workflow files from the pushed branch, so both of the profile's files act:

- **`git-pair check`** starts a run and its job is refused by `if:` — a push that is not the destination. It never
  reaches a runner, and a refused job bills nothing: measured on the library's runs, `timing.billable.total_ms` is 0
  with each job at `duration_ms: 0`, while `run_duration_ms` shows a second or two of wall clock that is not billed.
- **`git-pair integrate`** runs: full-history checkout → `install-git-pair.sh` (pinned release) → `toolchain` →
  resolve `verify` → build the merge commit, run `mise run gates` against *that* tree, push if green → report
  `"$GIT_PAIR_BIN" queue`.

On a pull request only the check file triggers, and it runs the same `toolchain` step and the same resolved `verify`,
which is what keeps a pull-request green and a candidate red from meaning two different builds.

## How it was implemented, and why this way

**The marker** declares `git-pair-github-integration` v5 beside `git-pair-loop` v2 and answers the three roles the
profile asks about: `verify`, and the two offers `toolchain` and `ci-request`. The paragraph that recorded the
non-adoption is replaced by one that records the adoption, including the three repository variables that steer these
files (`GIT_PAIR_BASE`, `GIT_PAIR_VERSION`, `GIT_PAIR_TIMEOUT`) and why a tuning dial belongs in a variable rather
than in installed bytes.

**`scripts/ci/prepare-toolchain.sh`** is new, and it is a file rather than a command line in the marker for one
reason: it has to hand something to the steps after it. It fetches mise when the runner has none (and writes
`$GITHUB_PATH`, without which the next step cannot find `mise`), trusts and installs `.mise.toml`'s pins, runs
`mise run build`, and exports `GIT_PAIR_BIN` as the branch-named binary `scripts/install-name.sh` derives. That export
is what makes the driver and the closing queue report ask the tool this tree built instead of the release the
installer left behind — and it is why the step sits *after* the install rather than before it.

**`scripts/ci/gh-request-ci.sh`** keeps its three exit codes and changes only its default, from `ci.yml` to
`git-pair-check.yml`. It names the file rather than the workflow's display name: `gh workflow run` accepts either, the
two differ here (the file is `git-pair-check.yml`, the `name:` is `git-pair check`), and a path is the address that
cannot drift when somebody rewords a header.

**`scripts/gates/ci-integrate.sh`** had seven assertions retargeted. Each one encoded this repository's *layout* rather
than the rule, and the installed files answer all seven differently without any of them being wrong:

| the assertion assumed | the installed file does |
|---|---|
| `name: CI` for the ladder | `name: git-pair check` |
| `permissions:` at column zero | `    permissions:` on the job |
| `actions: write` indented two | indented six, under the job |
| `branches:` as a block list `- '**'` | flow style `branches: ['**']` |
| `ref_name != (vars.GIT_PAIR_BASE \|\| 'main')` on one line | two comparisons, variable and fallback |
| `--ci-request scripts/ci/gh-request-ci.sh` written in the job | resolves the role: `dev-profiles command ci-request`, answered in the marker |
| the requested workflow repeated as a literal in the fixture | (unchanged shape; now derived) |

The retargets accept the rule in either spelling — a scope at file level or job level, a branch filter in flow style
or block form — and still fail on the meaning: a `branches:` filter that is not the every-branch value, or a
destination comparison missing either half, is still red.

## What was considered and rejected

**Keeping `ci.yml` as the ladder and installing only the merge file.** `ci_files` is a profile's list, not a
checkbox; a repository that declares the profile takes the four paths or declares a different profile. Keeping the
old ladder alongside the new one means two ladders on every pull request — about 2m30s each — and two answers to one
question.

**Answering `toolchain` inline** (`mise run build`) — rejected because the answer also has to extend `PATH` for the
steps after it and export `GIT_PAIR_BIN`. A marker line can do both and a reader would never see either.

**Widening the library's files to match the old assertions.** Rejected on the library's own rule: installed bytes are
byte-compared, so re-formatting them to suit one consumer's grep is the wrong direction, and the assertions were
measuring layout anyway.

## What is deliberately not changing

`scripts/ci/git-pair-integrate.sh` is the same 415 lines it was — the merge driver's behaviour is untouched, and
`scripts/gates/ci-integrate.sh`'s fixtures exercise it as before. `release.yml` is untouched. Nothing about the
product changes: no Go file, no task, no version. The README and PRD still describe the two workflow files as this
repository's own; that prose is the child layer, so this record can be read as the ownership change without also
arguing about documentation wording.

One wording item is visible and left alone because the text is the driver's, not this repository's: the stanza
`install` writes into `AGENTS.md` joins the declared profiles with a comma — "declares `git-pair-loop` v2,
`git-pair-github-integration` v5 in `.dev-profiles.toml`" — where "and" would read better. Editing the file by hand
would put a consumer's bytes out of step with the thing that byte-compares them, so it belongs in the library.

## Where to look hardest

**`scripts/ci/prepare-toolchain.sh` has never run.** It is CI-only code with no local caller, so nothing here proves
it works — the three failure modes (no curl, mise installed but not on `PATH`, a build that names a different binary)
are each handled with a message, and none of them has been exercised. The first real proof is the next declaration's
integrate run.

**The retargeted assertions.** Seven rules moved from one spelling to another, which is exactly when a rule gets lost
rather than changed. The interesting one is the last row of the table: the fixture that *repeated* the requested
workflow name passed while the one that *derived* it failed, when the default named a file that does not exist. A
reviewer who wants to check my work should delete one half of a rule locally and confirm the gate goes red.

**The timeout.** The profile's ceiling is 30 minutes; the deleted files said 20. `GIT_PAIR_TIMEOUT` is unset on the
remote, so until somebody sets it, a hung replay runs to 30 rather than 20. It is a repository variable, not an edit
to installed bytes — and the marker's comment says so, which is the argument for keeping it out of the file.

## How we know it works

```bash
./.dev-profiles/bin/dev-profiles doctor --scope repo        # exit 0
MISE_TRUSTED_CONFIG_PATHS=$PWD bash scripts/gates/ci-integrate.sh   # 132 checks, all passing
MISE_TRUSTED_CONFIG_PATHS=$PWD mise run gates                        # exit 0
```

`ci-integrate.sh` is the gate that owns the workflow-shape assertions, and it runs 132 checks green against the
installed files — the seven retargeted ones above are among them, along with the fixtures that drive the driver
against scratch bare remotes. `doctor` reports all four `ci_files` installed and byte-matching, the three roles resolved
(`command:verify` → `mise run gates`, `command:toolchain` → `scripts/ci/prepare-toolchain.sh`, `command:ci-request` →
`scripts/ci/gh-request-ci.sh`), `needs:git-pair-github-integration` satisfied by the declared loop profile, and both
versions matching the library at v2 and v5. The full ladder — gofmt, vet, the sharded Go suite, the PRD §29 replay,
the pty walkthrough and the CI merge replay — passes end to end.

One defect was found before this was offered, and found by the gate rather than by me: the ask's default named
`git-pair check.yml` with a space, copied from the workflow's display name, and no such file exists. The derived
assertion (`the request hook still names a workflow that exists`) went red; the neighbouring fixture that repeated the
same string stayed green, because a fixture that repeats a value moves with it. Both are fixed, and the fixture now
derives the name too.
