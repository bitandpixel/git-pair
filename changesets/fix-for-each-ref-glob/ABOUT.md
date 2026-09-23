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
^ What happens when the remote ref for a branch and a branch both carry the directory?

Nothing on this changeset, and that is worth saying rather than leaving to be inferred: this change still
lists `refs/heads/` only, so `refs/remotes/origin/feat/ux` never reaches the candidate list. The branch in
the working tree has nothing to compete with, and a fetched copy of it — even one behind, even one carrying
the directory — cannot pull the derivation into an ambiguity or move the record off the local tip. Checked
rather than reasoned: with the changeset on `feat/booked`, the merge on `main` and
`refs/remotes/origin/feat/booked` pointing at `feat/booked~1`, the no-flag record writes the archive at the
local tip and says nothing about a second carrier.

That blindness is what the next commit in the stack removes — and the answer there is the same one, for a
better reason. `fix-legacy-refs-and-remote-branches` keys candidates on the branch a ref *names*, so a local
branch and the copy the last fetch brought are one candidate and the local tip is what gets recorded; two
candidates appear only when two *different* branches carry the directory, which is the refusal above. Its
`TestIntegrationRecordCountsOneBranchUnderBothSpellings` is the case above, kept as a test rather than run as
a check, with the fetched copy deliberately behind the local one — keyed on the commit those would be two
carriers and a refusal, which is the failure this question is really asking about.

great thank you. can you move this to a resolved questions section? I'll approve.

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
