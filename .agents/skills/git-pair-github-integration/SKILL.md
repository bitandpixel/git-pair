---
name: git-pair-github-integration
description: Use when a changeset is approved and ready to land in a repository whose merge is performed by CI — what the author declares, what the job verifies, and what nobody else may do. git-pair-github-integration, not git-pair-manual-integration.
---

# Landing a declared change

The author asks. A GitHub Actions run verifies the change and merges it. Read this with the loop skill.

## The author's half

    git pair change integrate    # one commit: Review-State: integrating, naming the head it covers
    git push

That commit is the whole ask. It is not a merge, not a queue and not a verdict — the approval underneath it is what
permits the merge, so `git pair check` keeps passing after you run this and keeps refusing after anything that would
have made it refuse before. A rebase rewrites the head the marker names, and the stale name is what makes the gate
refuse the rewrite rather than bless it: re-declare after a rewrite.

Never run `git pair review submit|open|reopen|discussion|about`, never `change tidy`, never merge into the destination,
and never ask for the merge in prose you already asked for with the command.

## What the job is allowed to merge

The job reads git-pair's gate whole, both answers together:

    git pair check --json | jq -e '.ready and .integrating'

`ready` answers "may this be merged". `integrating` answers "did the author ask". Merging on the first alone takes the
decision out of the author's hands.

## What it verifies, and of which tree

The merge commit is created locally, the consumer's `[commands] verify` runs against that merged tree, and the push happens
only if the suite is green. This is deliberate: green builds on a branch head say nothing about the merge commit the
destination is about to acquire.

A failing verification is always a refusal. It puts the local destination back, merges nothing, and exits 1 — it is
not a skip, not a retry, and not a signal to push anyway. If the suite is failing, the work is not ready and the
author fixes it on the branch, then re-declares.

**Nothing may change the tree between the merge and the push.** Before pushing, the driver compares the working tree
against the merge commit it made. A verification command that rewrites tracked files — a code generator, a formatter,
an updated lockfile — would leave the suite green about a tree the destination is never going to hold, which is the
claim this whole sequence exists to make. The refusal is the same shape as a failed suite: the local destination goes
back, nothing is pushed, the log prints the files that moved. Tracked files only; untracked build output is not what
gets pushed, so a compiled language is not refused for its `target/` directory.

That is also where installation belongs. Whatever the suite needs installed goes at the top of the `verify` answer,
not in a step of the workflow: in the merge job the tree under test does not exist until the merge is made, and putting
it in `verify` asks the question of the tree the installation is meant to serve.

The scripts under `scripts/ci/` are this profile's bytes, installed and byte-compared. Edit them in the library, not
in a checkout.

What runs as the verification is not this profile's choice, and no filename here is a requirement. The profile demands
one role — `verify` — and the consumer answers it in the marker's `[commands]` table with any command that runs its own
checks; `dev-profiles command verify` prints what was chosen. A checkout may keep its answer in
`scripts/ci/verify-candidate.sh`, or in a Makefile target, or in a one-liner calling the project's test command. The
demand is a role and the marker holds the answer, which is what stops the profile shipping a file it would then have to
overwrite.

## The seams you may leave unanswered

Two moments belong to the consumer rather than to this profile, and each has exactly one place to be answered.
Leaving one unanswered is a decision, not an error: `dev-profiles doctor` names what is open instead of hiding it,
and fails what is answered wrongly. A third heading below is a seam this profile has taken away, and the migration
out of it.

**`setup` — `.github/actions/git-pair-setup/action.yml`, a composite action.** This is what prepares a runner: a
compiler invoked, a toolchain installed, a dependency cache restored, a build of the repository's own tool. Both jobs
call it, after the pinned install of `git-pair` and before any work. It is a file rather than a marker key because
GitHub resolves a local `uses:` path while it reads the workflow, so no expression and no command table can supply
one; and it is the consumer's file rather than this profile's because a repository's build is nobody else's to write.
`install` never authors it and `doctor` never byte-compares it, so an upgrade costs one guard line and nothing else.
A starter sits at
`.dev-profiles/profiles/git-pair-github-integration/references/git-pair-setup.example.yml`, and `doctor` names that
path when the seam is unanswered. It is a file to copy and edit, not one `install` seeds: the step is called if and
only if the file is there, so a marker key saying "this repository uses the setup step" would state a fact the tree
already answers — and could be answered wrongly while the job stayed correct.

A repository that needs nothing on a runner writes no file. The step carries
`if: ${{ hashFiles('.github/actions/git-pair-setup/action.yml') != '' }}`, and GitHub evaluates a step's `if:` before it
resolves a local action path — so the step reports `skipped` and never reaches a runner. `doctor` reports that
absence as a choice. It fails two states instead, both of which leave a tree unprepared while the job looks green: a
file that is not a composite action (no `runs:` with `using: composite`), which makes GitHub fail the run when it
loads the file; and the same file named `action.yaml`, which GitHub would accept but the guard does not read.

What a composite action can do here was checked on a real runner rather than taken from the documentation, which is
narrower than the runner in the first two cases:

- Its steps write `$GITHUB_ENV` and `$GITHUB_PATH` into the parent job, so the actions and variables it sets reach
  every step after it — including the driver and the post-run report. That is how a repository builds git-pair from
  the tree under review: export `GIT_PAIR_BIN` from the composite, and both ask for the tool by that name.
- A nested step's `if:` may read `steps.<id>.outputs` and `env`, so a cache hit inside the composite can decide what
  its later steps do. The context list published for composite actions is narrower than what the runner accepts.
- `${{ secrets.X }}` is not available in a composite action at all — it is a load failure for the file, not an empty
  string. If an action in there needs a token, pass it in from the calling step's `env:`. This profile deliberately
  does not hand `GITHUB_TOKEN` to the composite: the merge job's token is `contents: write`, and a third-party action
  fetched inside your setup should not receive it.

Two consequences for what you put in it. A nested action's `post:` runs in the job's post phase, after the parent's
last step, so a cache that is *saved* on success is fine while anything that depends on ordering is not. And cache
what you download, not what you build: `setup` runs against the branch head in the check job and against whatever
your `verify` runs in the merge job, and a cache key built from one tree's artifacts does not describe the other.

Nothing runs before the pinned install. A repository that needs a prerequisite for a shared action it cannot edit puts
`run:` steps at the top of its own composite; that window is not opened by the profile, because the first thing the
job does that could affect the tree is fetch the pinned release, and a step ahead of it would be a step whose output
that fetch can overwrite.

**`toolchain` is retired — delete its line.** It was the same seam answered as a shell string in `[commands]`, which
both jobs passed to `eval`. A marker that still answers it fails `doctor`, which names this replacement: a command
nothing runs is how a preparation goes missing quietly, and that is the failure this seam is most exposed to. The name
is retired rather than renamed because `setup`, `bootstrap` and `prepare` describe one moment, and offering all three
teaches nobody the difference.

**`ci-request` names a script that asks the forge to run the destination branch after a landing.** Unanswered means
trunk gets no run of its own, which is allowed: the tree the destination now holds is the tree this job tested, and
nothing waits on the run the ask produces.

## The two files the profile ships

`install` writes both and `doctor` byte-compares them. Each holds one job, and the split is the design rather than a
tidy-up:

- `.github/workflows/git-pair-check.yml` — the `check` job. Started by a pull request, a push to the destination, or
  a dispatch. Permissions: `contents: read`, nothing else.
- `.github/workflows/git-pair-integrate.yml` — the `integrate` job. Started by a push whose head carries the
  declaration, or a dispatch. Permissions: `contents: write` and `actions: write`.

Four rules span both files, written once here rather than twice:

- **The only green that lands work is the merge job's own.** It builds the merge commit and runs your `verify`
  answer against that tree before it pushes. `check` reports an opinion about a head, and the merge job never reads
  it — so a check that is skipped, cancelled or never configured can neither certify a merge nor block one. That is
  why `check` holds no write scope, and why the forge ask is allowed to name that file: the file it dispatches
  merges nothing, by construction rather than by agreement.
- **Nothing runs on a timer, and nothing waits on another workflow.** No `schedule:` — that poll was 96 started jobs
  a day whether or not anyone was working — and no `workflow_run:`, because the merge tests its own candidate and so
  has nothing to be told. A push to any branch starts both files' jobs and both `if:` expressions refuse it, because
  a trigger cannot read `vars` and the destination branch is a variable. A refused job never reaches a runner and
  bills nothing — on this repository's runs, `gh api …/runs/<id>/timing` reports `billable.total_ms` of 0 with each
  job at 0 ms, while `run_duration_ms` shows a second or two of wall clock that is not billed. What does cost is a job
  that starts: each job's executed time rounds up to a whole minute, so a five-second job is a minute. That is the
  arithmetic behind putting a condition in `if:` rather than in a guard step, and behind never adding a schedule to
  come back later. The retry for a merge that died mid-run is re-running the run.
- **`timeout-minutes` is the profile's, its override is yours:** the same expression on both jobs,
  `fromJSON(vars.GIT_PAIR_TIMEOUT || '30')` — 30 by default, raised or lowered by the `GIT_PAIR_TIMEOUT` repository
  variable. `vars` is the only context GitHub lets that key read, so the ceiling cannot live in the marker, and
  keeping the default in the profile means no consumer can turn it off. The `fromJSON` is the part that is easy to
  lose and expensive to get wrong: a repository variable arrives as text, this key wants a number, and GitHub does
  not coerce them — it fails the run *before creating a job*, so the symptom is a red run with no job, no steps and
  no log to read. It once shipped without it, and a push that declared a changeset produced a merge job that could
  never be scheduled while the check job on the same push — refused by its `if:`, so its identical expression was
  never evaluated — reported `skipped` and looked healthy. `actionlint` names the defect
  (`type of expression … must be number but found type string`), and `tests/ci-integrate/run.sh` runs it over both
  files when the machine has it.
- **Each scope is what its job uses, and the ask's scope is always granted.** `actions: write` has one user: the
  `ci-request` hook, which asks the forge to run the tree that just landed, and an ask is a `workflow_dispatch`. Job
  permissions are static YAML — no expression reaches them, and no step may declare its own — so the scope sits
  there whether or not you answer the role. It is not the wide grant on that job: `contents: write` on a destination
  the job exists to push to already lets it land a commit that rewrites this file and declares anything. A protected
  destination does not change the comparison either, because a job merging into a protected branch has to be named
  in that protection — and a repository that will not name it should declare `git-pair-manual-integration` and merge
  by hand.

`GIT_PAIR_BIN` is the seam between your `setup` action and the merge job. The driver and the post-run queue
report both ask for the tool by that name, so a repository that builds git-pair from the tree under review lands and
reports on its own build rather than on the release `scripts/ci/install-git-pair.sh` fetched.
