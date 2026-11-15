# feat-allow-feedback-config

## Summary

`--allow-feedback` — the switch that lets a non-blocking review suffice at the integration gate — was usable by
anyone who typed it, and agents typing it landed changes whose newest review was feedback, with no approval
anywhere. The switch is now the repository's decision: a new `allow-feedback` key under `[landing]` in
`.git-pair/config` enables it, and without that key both commands that carry the flag refuse it. Where a
feedback verdict meets a repository that has not decided, `git pair check` recommends the key, so the decision
is made in the file the repository commits rather than in the seat of whoever runs the gate.

## What changed

`.git-pair/config` gains a third key beside `route`:

```toml
[landing]
route = "automated"
allow-feedback = true
```

Absent means false. Only `true` enables the flag; `false` and every other value refuse it, and a defective
file enables nothing, as with the route.

With the key absent, `git pair check --allow-feedback` and `git pair change integrate --allow-feedback` exit 1
with the refusal named beside the other reasons:

```text
$ git pair check --allow-feedback
NOT READY:
state: FEEDBACK
- the newest review is feedback, which is non-blocking and is not sufficient to land (0c7c834)
- `--allow-feedback` is refused: the repository has not set `allow-feedback = true` under `[landing]` in .git-pair/config
recommend: if feedback alone should be enough to land in this repository, set `allow-feedback = true` under `[landing]` in .git-pair/config
```

Three behavior notes:

- The flag is refused whatever the verdict underneath: an approved head checked with the flag is refused for
  the flag alone. A script that passes the flag unconditionally learns in one run that the policy is not
  available here, instead of the flag sometimes working.
- The feedback reason's wording now says what is true in this repository. With the key present it names the
  switch as before ("not sufficient without `--allow-feedback`"); without it, it says the flag is refused
  because the repository has not enabled `allow-feedback`.
- `check --json`'s `policy` field reports the policy that actually applied: `approve-or-feedback` only where
  the flag was passed and the config enabled it.

## Design decisions

- **The refusal is exit 1 with a reason, not a usage error.** The command is correct; the repository said no —
  the same contract as every other gate refusal, and the reason lands in `reasons` where every consumer already
  reads.
- **The key lives in `[landing]` beside `route`.** Both are the repository's landing policy: what may land,
  and who lands it. A second section would have bought a parser change for one key.
- **The recommendation is a `recommend:` line, never a reason.** The gate refuses either way; enabling the key
  is a decision for the people who own the file, made once and committed. It prints only where the newest
  permitting review is feedback, the key is absent, and the changeset has neither landed nor ended — a landed
  changeset has no policy question left.
- **PRD §11.3's previous position is reversed deliberately.** "There is no config key for it, because the
  command that runs the gate is the only place the policy is known" was the argument that let whoever ran the
  gate relax the policy. The config is the one place every runner shares; the seat of whoever happens to run
  the gate is not.

## Boundaries

- Nothing else about the gate changes: `approve` and `block` behave exactly as before, and the key relaxes
  feedback and nothing else.
- This repository does not set the key — a human approval is required here, so its own gate now refuses the
  flag, which is the policy this repo wants.
- A merge job that already passes `--allow-feedback` unconditionally will start failing in repositories that
  have not set the key. The error names the key and the fix is one line in `.git-pair/config`; that break is
  the point of the change, and it is called out in the PRD and README passages that replaced "there is no
  config key for it".

## Validation

`mise run check` (markdown formatting, gofmt, go vet, full Go suite) and `mise run gates` (the four E2E
replays, including the CI merge-job driver) both pass. The new behavior is pinned by tests at each layer:

- `internal/landingconfig`: the key parses quoted and bare, beside a route; `yes`, `TRUE`, empty and
  out-of-table values are defects; a defect anywhere in the file drops the enablement with the route.
- `internal/cli`: the flag refused with a feedback verdict (two reasons, `policy: approve-only`), refused on
  an approved verdict (one reason, no feedback bullet), the `recommend:` line and its `recommendations[]`
  entry on an undecided config and its absence on an enabled one, `change integrate` refused with nothing
  written, and the declaration succeeding under an enabled key.
- Existing tests that exercise the permissive policy now commit the key first through a `withFeedbackAllowed`
  fixture helper — the same move a repository makes, placed before the review so the verdict covers the branch
  that carries it.

One failure is part of this change's record: the first `git pair integrate` run refused the merge candidate
because the §29 E2E gate (`scripts/gates/e2e-29.sh`) asserted the old contract — `check --allow-feedback`
accepting a feedback verdict in a repository that had not enabled it. Nothing had merged; the fix updates that
scenario to assert the refusal without the key, the acceptance with it, and the enablement as an ordinary
commit. The author had run `mise run check` but not `mise run gates` before the hand-off; the gates are what
the merge job's verify runs, and a hand-off that skips them is a break the job finds two minutes later.
Both are run before this re-offer.

## Reviewer attention

- The wording of the three reason variants (enabled, disabled without the flag, disabled with the flag) — the
  flag case deliberately does not repeat the config story that its own bullet carries.
- The decision that the flag refusal is unconditional, including on an approved verdict. Conditioning it on
  the verdict was the alternative; it would make the flag sometimes-work, which is the confusion this change
  exists to remove.
