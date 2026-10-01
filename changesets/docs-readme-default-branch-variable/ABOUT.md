# docs-readme-default-branch-variable

## Summary

`README.md` said the CI runner names the integration branch with `GIT_PAIR_DEFAULT_BRANCH` or
`--default-branch`. git-pair reads neither from the environment: the only interface is the flag. The variable
is real, but it belongs to the shipped merge job, which reads it and forwards the flag. One sentence became
three that say who reads what.

Found while adding `GIT_PAIR_NO_CACHE` in `feat-caching`, which followed the `GIT_PAIR_*` naming and noticed
on the way that this variable's reader was somewhere other than where the README put it.

## What changed

| File | Change |
| --- | --- |
| `README.md` | In the paragraph on what the runner has to provide, replaced "the integration branch named — `GIT_PAIR_DEFAULT_BRANCH` or `--default-branch` on each call" with the flag named as git-pair's interface, and a following sentence stating that the variable is the merge job's, where it comes from, and that a `git pair` command outside that script needs the flag |

No code changed. No behaviour changed.

## Design decisions

- **Corrected the sentence instead of implementing the variable.** The variable would have been easy — one
  `os.Getenv` next to the `--default-branch` flag, and the README becomes true. It should not exist, because
  what it sets is the destination: the ref that "has this landed?" is measured against, and the branch a
  declaration merges into. An inherited environment variable would move that without leaving any trace in the
  command line, so `status`, `queue` and `check` would answer a different question depending on how the shell
  happened to be populated. `--default-branch` is explicit for the same reason a changeset records `base:`
  rather than inheriting it.
- **The contrast with `GIT_PAIR_NO_CACHE` is the test for which variables are safe here.** That one landed last
  changeset and is read from the environment on purpose, because its only effect is to do more work and write
  nothing: a job that sets it globally cannot be misled by it. A destination variable can mislead, so the
  asymmetry between the two is deliberate rather than an inconsistency to be tidied.
- **Nothing was blocked by the gap, which is what makes a doc fix the whole answer.** The merge job works today
  because `scripts/ci/git-pair-integrate.sh:61` takes `BASE=${GIT_PAIR_DEFAULT_BRANCH:-main}` and passes
  `--default-branch "$BASE"` on each of the five `git pair` calls it makes. The runner only has to set the
  variable, which is what the workflow already does. So the sentence described a working setup while
  misattributing the reader — the misleading part was the attribution, not the outcome.
- **Kept the reason the flag is needed at all.** "A checkout that fetched one branch has nothing to compare
  against" was accurate and is the reason a runner must name the destination, so it stayed. Only the list of
  ways to name it was wrong.

## Validation

- Scope checked by grep: the claim existed in exactly one place, `README.md:1270`. Every other mention of the
  destination in the documentation names the flag — `README.md:342`, `:352`, `:564`, `:1111`, `:1783`, `:1811`,
  `:1834`, `:1844`, and `skills/git-pair/references/cli.md:45`. The only two readers of the variable are
  `.github/workflows/git-pair-integrate.yml:122`, which sets it from the `GIT_PAIR_BASE` repository variable,
  and `scripts/ci/git-pair-integrate.sh:61`, which reads it — both already correct, and left alone.
- The new wording was checked against the code it describes rather than against the old wording: `--default-branch`
  is registered as a persistent flag on the root command (`internal/cli/root.go:195`), so it is accepted by
  every command as the replacement sentence claims; the script's fallback is `main`; and the workflow's value
  comes from `vars.GIT_PAIR_BASE`.
- Line lengths left in the file's own range. This paragraph sits at 103–109 characters; `README.md` has 603
  lines over 100 and a maximum of 890, so nothing was reflowed to a width the file does not use.
- `mise run gates` on the final tree.

## Known limitations

- `GIT_PAIR_BASE` is now mentioned in the runner paragraph and nowhere else. There is no configuration section
  entry for the repository variable, so a reader who wants to move off `main` learns the knob exists only by
  reaching the CI section. That is where it is used, so it is arguably the right place, and it is out of scope
  for a change whose subject is a wrong attribution.

## Open questions

- A person whose trunk is not `main` currently types `--default-branch` per command or relies on the recorded
  `base:`. If that becomes a complaint, the fix is a per-repository setting that is visible in the repository
  — the same reason `base:` is recorded — and not an environment variable. No evidence anyone wants it today,
  so no request is invented here.
