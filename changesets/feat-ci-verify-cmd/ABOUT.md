# feat-ci-verify-cmd

## Summary

The integrate driver can now be handed a command to run against the **merge candidate** — after the merge
commit exists and before the push — so a landing can be conditional on the repository's own suite instead of
on somebody else's report about the branch head. The layer above this one, `feat/ci-integrate-candidate-shape`,
is the caller that uses it. This layer is the seam and the replay of it, and it is green without that caller.

A green head is a report about a commit somebody already ran, and it is the wrong commit: the merge is the
combination no run on the branch has ever seen, and it is exactly what the destination receives.

## What changed

- `scripts/ci/git-pair-integrate.sh`: `--verify-cmd <cmd>`, with `GIT_PAIR_CI_VERIFY_CMD` for a whole
  process. The usage block is the `--help` text, so the flag is documented where the job is documented;
  the §29 sequence list gains a `verify candidate` entry between `merge --no-ff` and `push`, and the
  exit-code paragraph says what a failing candidate costs.
- `scripts/gates/ci-integrate.sh`: one scenario, 15 checks.

`--require-ci`, `--ci-probe`, `--ci-request` and both helper scripts are untouched, and the scenarios that
cover them are unchanged.

## Design decisions

**Where it sits in the sequence is the whole feature.** It runs after `merge=$(git rev-parse HEAD)` and before
the push, and after both the moved-branch check and `--expect-head`. Before the merge it would test the head,
which is the question the probe already answers; after the push it would test a landing nobody can undo.
`--expect-head` therefore matters more here, not less: it is what makes the candidate be built from the commit
the declaration named.

**A failure is an error, not a skip.** Every other refusal in this script returns 0, because a push-triggered
job normally runs before anybody has declared anything and a red build for "not yet" trains people to ignore
the build. A candidate that failed its own tests is not that, and on a repository without per-commit CI this
is the only place the author would see it. So it returns 1 with or without `--require`.

**Two answers, not three.** The probe and the request hook each answer `0`, `1`, or `2 cannot tell`, because
both ask a forge about something outside the repository. A candidate either ran and failed or it ran and
passed; a suite that could not run is a failure worth seeing, so there is no `cannot tell` here.

**The local destination is put back before anything is said.** The job checks the destination back out from
the remote (`git checkout -q -B "$BASE_LOCAL" "$REMOTE/$dest"`) before it reports the failure, because the job
merges several changesets in one run and without the reset the next merge would be made onto, and verified
against, a tree this run had already refused. If that reset itself fails, the run says the later changesets are
unverified rather than letting them look tested.

**`sh -c`, like the other two hooks.** One seam style for all three external commands, and a suite that wants
a script passes the script's path.

**Still nothing in the product.** git-pair itself knows nothing about this: `check` answers the review
question and nothing else, and none of this is a subcommand (PRD §26).

## Validation

`bash scripts/gates/ci-integrate.sh` → `CI-INTEGRATE: all checks passed (105 checks)`, against **90 on
`main`**, and with `main`'s own workflow files in the tree — the layer passes without the caller that will use
it. `mise run gates` runs the same ladder as CI: `E2E: all checks passed`, `PTY: all checks passed`,
`CI-INTEGRATE: all checks passed`.

The scenario drives the real driver against a scratch bare remote in one run over two declared branches, with
a verify command that fails only for the tree carrying one of them, so both verdicts come from a single job:

- with no `--verify-cmd` the job merges on the gate alone and tests no candidate;
- the command was asked about the tree that has trunk's newest move — trunk gains `moved.txt` _after_ the
  branches are cut, so a command run at the branch head cannot see it, which is what makes "it ran at the
  merge commit" a checked thing rather than an asserted one — and it had two parents;
- the refused candidate made the run red, named itself, and its own words reached the log;
- nothing of it reached the destination, not even `src/nope.ts`, while the passing one landed in the same
  run;
- the local destination was back at the remote's tip and no longer held the refused tree. Those two
  assertions are what fails if the reset is removed.

## Known limitations

- Nothing in this repository calls the flag yet. The shipped job still gates on `--require-ci`; the caller
  lands one layer up. Until then the flag is exercised only by the replay.
- The command runs in the job's clone with the job's token, so a suite that needs secrets gets them the way
  a test workflow gives them today, and artefacts it writes stay in that clone — per-job and discarded, but
  a suite that _commits_ would leave the clone dirty for later changesets in the same run.
- No timeout of its own: a suite that hangs hangs the job. The driver has never owned timeouts and the probe
  and request hooks have the same property; the workflow's `timeout-minutes` is where the bound lives.

## Open questions

- Should the reset also `git clean -fd` the destination worktree? A suite that leaves untracked files behind
  could have them visible to the next candidate in the same run. The reset covers tracked state, which is
  what the merge and the gate read; untracked contamination is possible and has not been seen.
