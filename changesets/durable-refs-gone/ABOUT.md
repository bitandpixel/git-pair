# durable-refs-gone

## Summary

Milestone M5 of `docs/plans/simplify-architecture/plan.md`: delete the durable-ref subsystem. Not started.
This file is the map for whoever starts it, written from the state of the stack at the time M4 landed.

## State of the stack

- M1 `.landed/` in the tree model — merged, `81f01e2`.
- M2 landing is a tree fact — handed off, `feat/landing-is-a-tree-fact`.
- M3 derived bases — handed off, `feat/derived-bases`, gates green.
- M4 `git pair change tidy` — `feat/change-tidy`, this branch's parent.
- This branch is stacked on `feat/change-tidy`. Before `git pair change ready`, rebase onto the new
  `origin/main` (`git fetch origin && git merge-base --is-ancestor origin/main HEAD`).

## What M5 deletes

`internal/reviewref` (467 lines), `internal/cli/integration.go` (1366), `internal/cli/publish.go` (415),
`internal/cli/configure.go` (281), `internal/cli/published.go` (210), `internal/git/push.go` (204),
`internal/cli/fetch.go` (113), and their tests (~3.0k lines). The ref half of `internal/cli/landed.go`
(`refIndex`, `indexDurableRefs`) and the two reads that still use it: `internal/changeset/destination.go:60-125`
(parent `CHANGESET.yaml` from the landing commit's tree — make it read the tree of the destination instead,
which also fixes the landing-with-no-directory case) and `internal/cli/status.go`'s `Stack:` block, whose
per-ancestor `record:` line becomes a `landed:` line and whose `--json` key `stack[].integration` becomes
`stack[].landed_commit`.

Commands that go: `integration`, `integration record`, `integration publish`, `integration configure`, and
`--fetch` on `status`, `queue` and `check`. `git pair change integrate` stays, and `change wait --fetch`
stays.

## Order that keeps the tree compiling

1. `internal/cli/status.go`'s `Stack:` block and `internal/changeset/destination.go` — both stop reading the
   ref index. Commit with their contract tests.
2. `internal/cli/queue.go` and `internal/cli/landed.go` — drop `indexDurableRefs`, `publicationReport` and the
   `--fetch` paths. Commit.
3. The command files and the package, in one commit with the registrations in `internal/cli/root.go` and the
   ~3.0k lines of tests. `internal/hygiene/hygiene_test.go`'s single-`update-ref` rule (`:647-736`) and the
   audited-push fence (`:89-110`) become the ban: no shipped file may pass `update-ref`, `symbolic-ref` or
   `git tag` to git, and no string literal under `refs/git-pair/` may be built outside test fixtures.
4. `.github/workflows/git-pair-integrate.yml` — drop the record and publish steps and `fetch-depth: 0`.
5. `scripts/gates/e2e-29.sh` and `scripts/gates/ci-integrate.sh` — both walk the record and publish path
   today. They are contracts: rewrite them with the behaviour, do not loosen them. e2e-29 has ~121 `ok:`
   assertions and ci-integrate 60 checks; the durable-layer greps in them were rewritten once already, in M2.

## Known limitations carried into this milestone

A squash, cherry-pick or rebase-merge landing whose commit carries no directory leaves the changeset live and
its chain read has no markers to report. PRD §13 has to state that limit in the commit that removes the refs,
because the refs were the thing that made such a landing knowable.

## Open questions

Whether `parent.landed_in_default_branch` stays in `status --json` now that it is true whenever
`parent.landed` is. The index it was computed from is deleted here, so the decision belongs to this milestone.
