# The derivation could not see a branch named with a slash

`git pair integration record` with no SHAs reads both ends out of the graph: the landing is the
first-parent commit on the destination that added `changesets/<id>/`, and the reviewed head is the tip of
the branch still carrying that directory. The second half asked git for the branches with
`repo.RefTips(ctx, "refs/heads/*")`, and `for-each-ref` is not a shell: it matches its pattern path-name
aware, so `*` never crosses a `/`. In a repository that names its branches `feat/…`, `fix/…` and `docs/…`
that ask answers a single branch — the integration branch — which the derivation then correctly excluded.
So it reported `no branch here carries changesets/<id>/` about a branch sitting in the repository, and
asked for `--source` it had no business asking for.

This happened for real on `feat-ux`: the no-flag record refused, and a same-commit alias branch named
`probe` was accepted, because a flat name matches `refs/heads/*` and a nested one does not.

## The change

`internal/cli/integration.go:379` asks for the prefix `"refs/heads/"` instead of the glob. Nothing else in
non-test code passes a glob to `for-each-ref`, and the other three branch listings already used the prefix
form.

Seeing every branch also makes the derivation's existing ambiguity rule reachable for the first time: when
two branches carry the directory it refuses instead of picking. Before, the nested one was invisible, so it
recorded a guess quietly.

## Tests

- `TestIntegrationRecordDerivesFromABranchNamedWithASlash` puts the changeset on `feat/booked`, merges it
  into `main`, and derives both tips with no flags. Fails before the fix with the exact refusal from the
  real run; passes after.
- `TestIntegrationRecordRefusesToGuessBetweenTwoCarryingBranches` names both candidates and
  `--source <ref>`, writes nothing, and records the pair once the branch is named. Exits 0 before the fix —
  the quiet guess.
- `TestRefTipsPatternIsAGitPatternNotAShellGlob` (`internal/git`) pins the two pattern forms at the layer
  that hands the pattern to git, so a later edit that shortens a prefix back to a glob fails where it will
  be read.
- `TestIntegrationRecordDerivesBothTips` and `TestIntegrationRecordDerivationStopsWhenTheBranchIsGone` had
  to say what they mean. `recordFixture` lands the work on `release/2.x`, which therefore carries the
  directory too; both tests were green only because that second carrier was invisible to the glob. They now
  delete it and keep their single-carrier premise, and the two-carrier case has its own test above.

No README or PRD change: README:557 already promises "anything ambiguous is a usage error naming the
candidates". The contract was written; this makes the code keep it.

## Verification

`mise run check` — format, vet, whole suite — clean. Both new tests were run against the pre-fix pattern to
confirm they fail. The pty walkthrough is not the gate for this: it records with `--source` and `--commit`,
and `resolveIntegrationRecord` only calls the derivation when one of those is empty, so that run never
reaches the changed line.
