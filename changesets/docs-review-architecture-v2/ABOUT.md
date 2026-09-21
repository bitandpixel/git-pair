# docs-review-architecture-v2

## Summary

Documents the review-architecture requirements supplied 2026-06-15, reconciles them against the code and
PRD, and turns the result into an eight-milestone plan. Docs only — nothing under `internal/` or `cmd/`
moves, and `mise run check` is unaffected.

The spec overlaps the current design in most places and contradicts it in three: a moving per-changeset ref
that exists today, a `refs/git-pair/reviews/*` namespace that exists nowhere, and a seven-field integration
record where the code deliberately stores an object id and nothing else (PRD §13.2). The reviewer narrowed
the spec twice in the same session — to the local workflow with two create-only refs, then by walking back
the `land` command — and the plan is written against the narrowed shape.

## What changed

Four files under `docs/plans/review-architecture-v2/`:

- `requirements.md` — the spec as supplied, unedited, as source of truth.
- `reconciliation.md` — the analysis: one architectural inversion, sixteen numbered discrepancies with code
  and PRD citations, decisions with rejected alternatives.
- `plan.md` — goals, success criteria, constraints, decisions, M1-M8, two spikes, six risks.
- `research/2026-06-15-landing-transition.md` — a scratch-repo measurement of the rule M3 and M4 rest on.

Start with `plan.md` §Decisions. `reconciliation.md` is the supporting detail; read it before reopening
anything the decisions close.

## Design decisions

Only the two the diff can't show you. Everything else is in `plan.md` §Decisions with its rejected
alternatives named.

- **No `land` command, and it is not a shortfall.** git-pair performs no merge; the contract is `check` →
  ordinary git merge → `integration record`. That keeps PRD §26 and the hygiene test untouched, and it turns
  out to cost nothing in determinism: the command's existing `--source`/`--commit` pair already carries
  exactly the two refs' values, and both tips are derivable without git-pair touching a merge.
- **The rebase rule needs no new ref namespace.** A `Review-Head` trailer plus an ancestry test refuses an
  approval whose reviewed commit is no longer in the line — which reverses current behaviour, where a
  tree-identical rebase survives because drift is compared by tree, not lineage. Dropping the per-review
  anchors to Deferred is the price: after a rebase the original review commit is reflog-only.

## Validation

- Every citation in `reconciliation.md` was read from the tree at `eb36eec`, not recalled.
- The first-parent transition rule is measured rather than asserted: it names the merge commit under
  `--no-ff` and the squash commit under `--squash`, with the first parent's tree on the other side of the
  boundary in both cases (research note has commands and output).
- Each milestone's verification section names the specific tests and gate scripts, including the two gate
  scripts that break deliberately in M1.

## Known limitations

- The reconciliation is one conversation's snapshot. Two of its sixteen discrepancies — D11 stacked
  changesets, D16 reviewer identity and resolution state — are analysed more shallowly than the ref model,
  because the ref model is what got decided.
- Two questions are open by design, as spikes: whether the transition rule survives one merge landing
  several changesets (S1), and whether folding integration refs into the existing ref listing really costs
  zero git invocations (S2).
- M6 hard-renames `review queue` → `queue` and `change init` → `init` with no aliases, on the repo's rename
  precedent. It's isolated in its own milestone so it can be dropped on its own.
- The plan describes the remote door and deliberately does not open it: no fetching, no pushing.

## Open questions

For the reviewer, as `plan.md` P1-P4:

1. How strict should `integration record`'s four verification checks be? Proposed: all four refuse, with a
   narrow merge-commit exception only if S1 forces one.
2. Is `parent: <branch>` + `parent-changeset: <id>` the right `CHANGESET.yaml` shape, refusing `parent:`
   alongside `base:` so there is one source of truth?
3. Adopt `git pair queue` and `git pair init` as the documented spellings, hard-renamed?
4. Defer reviewer identity and thread resolution state? `check` gating on open threads would change the
   verdict for every existing repository.

Two of my own, not the spec's:

5. Should this repository carry a `skills/git-pair/` surface? The sibling repository `bitandpixel/pi-git-pair` has one that already documents a namespace this code
   never used (`refs/reviews/archive/<slug>/<sha>`) and a command this code removed (`review close`) —
   evidence for the contract, and against letting it drift.
6. Migration for repositories holding `refs/git-pair/changesets/<id>/{archive,integration}`. The plan argues
   the M4 warning is itself the migration nudge and that no ref-renaming tool should ship. Worth a decision
   before M1, since it determines whether legacy names stay reserved under `Taken`.
