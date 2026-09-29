# simplify-architecture: handoff state

Written when the session that executed M1-M4 stopped. It records verified state and the next concrete
action, in order. The plan itself is `plan.md` beside this file; the M5 decisions are in
`changesets/durable-refs-gone/ABOUT.md` on `feat/durable-refs-gone`.

## Verified state

| Milestone | Branch | State |
| --- | --- | --- |
| M1 `.landed/` in the tree model | - | merged, `81f01e2` |
| M2 landing is a tree fact | `feat/landing-is-a-tree-fact` | pushed, `mise run gates` green, `change ready`, in review |
| M3 derived bases | `feat/derived-bases` | pushed, gates green, `change ready` head `677e59c`, in review |
| M4 `change tidy` | `feat/change-tidy` @ `e19673d` | code, 9 tests, README row, PRD §9.10, cli.md row, e2e step. Go suite green. Gates red on one assertion, see below. Not readied, not pushed |
| M5 delete the durable-ref subsystem | `feat/durable-refs-gone` @ `e3a3b45` | draft. `BaseDerived` added, `DestinationFor` walks the integration branch, `internal/changeset` no longer imports `reviewref`, two namespace-only tests deleted. Two destination tests red pending the decisions in the changeset |
| M6 documents | - | not started |

Worktrees: `/home/david/dev/worktrees/git-pair/{simplify-architecture,change-tidy,durable-refs-gone}`.
All clean. Nothing in M5 is pushed.

## Next actions, in order

1. **M4 gate failure.** `scripts/gates/e2e-29.sh:438` greps the first `queue` after
   `git fetch "$DECLREMOTE" 'refs/git-pair/*:refs/git-pair/*'` for `awaiting-merge (integrated at …)`. It
   failed in two runs on `feat/change-tidy` and passed on `feat/derived-bases` with product sources that
   differ only by `tidy.go` and the docs. The note comes from `internal/cli/queue.go:164`, inside the branch
   that requires `br.Resolution.Selected != nil`, so the note is absent when the branch resolves to no
   candidate - under the tree rule, when the integration branch is read as carrying its directory. The
   durable-index path is not the cause: it returns its error and `queue` propagates it. Check the tree
   first: `git ls-tree --name-only main changesets/` in the scratch clone, inserted before line 438.

   Measured answer (run on `feat/change-tidy`, debug inserted then reverted): at that point the clone holds
   `booking-transaction booking-tests feat/change-tidy integrated-interface main review-comments
   unrecorded-landing` — no `awaiting-merge` branch — and `main`'s `changesets/` holds
   `booking-transaction booking-tests awaiting-merge`. The note is emitted only from the per-branch candidate
   path (`queue.go:150-169`), so with no branch there is nothing to print it from and the assertion cannot
   pass; the directory in `main` is also why no other branch offers it. The note does appear later in the
   same script, so the branch is created after this step. Decide one of: the step creates or fetches the
   branch before asserting the note, or it asserts the durable fact (the record is present, nothing offers the
   changeset for review) instead of a per-branch note. Either is a decision about the step's claim — do not
   relax the grep until that is settled.
2. Rerun `mise run gates` in `change-tidy` with nothing else running, then `git pair change ready` and
   `git push -u origin feat/change-tidy`.
3. **M5.** Resolve the two questions in `changesets/durable-refs-gone/ABOUT.md`, then follow the order in
   that file: the two remaining ref-index reads, then `queue.go`/`landed.go`, then the commands, the
   package, the push helper and `--fetch`, the hygiene rule, the CI workflow, and the two gate scripts.
   `e2e-29.sh` and `ci-integrate.sh` walk record and publish today; they are contracts, rewrite them with
   the behaviour.
4. **M6.** PRD (the record sections §11.4, §13, §21 and the exit-code table), README, `skills/git-pair/`
   (`SKILL.md`, `references/integration.md`, `references/cli.md` - including `LANDED, UNRECORDED`),
   `internal/cli/docs_contract_test.go`'s whitelist, supersede the three older plans, move this plan to
   `docs/plans/completed/`.

## Two rules this plan does not relax

Gate scripts are contracts: rewrite them with the behaviour, never loosen an assertion to make a run pass.
Every `--json` array is `[]` and never `null`; shapes are contracts with contract tests, changed in the same
commit as the behaviour.
