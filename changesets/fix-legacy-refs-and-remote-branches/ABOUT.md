# The durable refs mean one thing, and a branch is a branch under either root

Two claims git-pair made about refs were weaker than they looked, in the same corner of the code. One is a
reading nobody needs any more; the other asks a question of only half the refs that can answer it.

**The retired layout still counted as a record.** `refs/git-pair/changesets/<id>/{archive,integration}` was
the layout before the two frozen families, retired by `7c0a90a`. `reviewref.KindLegacyIntegration` kept it
alive: a landing written down only there still counted as recorded for the unrecorded-landing detector
(`internal/cli/landed.go`) and the published view (`internal/cli/published.go`). So the namespace had three
path spellings that meant something, none of them written any more, and `origin` holds none of them.

**The derivation saw only branches the clone had checked out.** `deriveArchiveTip` listed `refs/heads/`, so
a CI runner — which is handed the branch by git, and git puts it at `refs/remotes/origin/<branch>` with no
local branch at all — was told the branch it was standing on did not exist, and asked for a `--source` it had
no way to know. Same class as the `refs/heads/*` glob fixed in the parent changeset, one root further out.

## The change

`KindLegacyIntegration` and `KindLegacyArchive` are gone, and `refs/git-pair/changesets/…` is read nowhere.
`refs/git-pair/archive/<id>` and `refs/git-pair/integrations/<id>` are what a record is. A landing recorded
only at the retired path is now reported `LANDED, UNRECORDED`, and the finding names the command that writes
the pair where it is read.

The one thing the retired path still answers is whether this clone fetched the namespace, which is a
different question from whether it holds a record of ours. `List` now returns every ref under
`refs/git-pair/` and classifies only what it owns — `Entry.Kind` is empty for a ref that is not one of the
two families — so `NamespaceEmpty` stays "nothing at all under the root". Answering it from the classified
entries instead would tell a repository holding only retired refs that it never fetched, pointing the reader
at a fetch that cannot change the answer. That is the §13.4 rule, and this is the place it could quietly
break.

Widening the derivation is the part with a rule in it rather than a root added. Candidates are keyed by
**the branch a ref names** (`branchKey`), not by the path spelling it:

- `refs/heads/feat/ux` and `refs/remotes/origin/feat/ux` are one candidate, and the local tip is what gets
  recorded. Keyed on the commit they would be two carriers, so every pushed branch would refuse as
  ambiguous for a reason nobody reading the refusal would believe.
- A destination is excluded under both spellings. A fetched `refs/remotes/origin/main` carries the changeset
  directory once the merge is pushed and fetched, and offering it as the reviewed head would record a
  landing as its own source. Commit exclusion does not always reach it — `main` moves, the fetched copy does
  not — which is why the exclusion is on the branch, not only the SHA.
- A remote's symbolic `HEAD` names no branch, so it is not a candidate.

The full retirement was decided with its cost known: the old reading was a kindness to upgraded
repositories, and a namespace with three spellings is one nobody can hold in their head. PRD §13.4 and
README:510, :727 said the old rule plainly; they now say this one, and the consequence for a repository
still holding those refs is written out rather than left to be discovered.

## Tests

- `TestQueueReportsALandingTheRetiredLayoutMissed` replaces `TestQueueAcceptsALandingTheRetiredLayoutRecorded`
  and holds both halves: the retired ref now produces `LANDED, UNRECORDED`, and the hedge offered is "none
  *in this clone*", not the "holds no refs/git-pair/* refs at all" hint.
- `TestListKeepsARetiredOnlyNamespaceNonEmpty` pins the same fact one layer down;
  `TestListClassifiesTheTwoFamiliesAndReportsTheRest` asserts the retired paths come back unclassified
  rather than dropped.
- `TestIntegrationRecordDerivesFromARemoteTrackingBranch`,
  `TestIntegrationRecordCountsOneBranchUnderBothSpellings` (the fetched copy deliberately *behind* the local
  one, so a commit-keyed implementation would refuse), and
  `TestIntegrationRecordExcludesTheDestinationsRemoteSpelling` (`main` moved past the fetched copy, so no
  SHA exclusion reaches it). Each fails alone: dropping the remote ask fails the first, dropping the dedupe
  fails the second, dropping the branch-key exclusion fails the third.
- `cost_test`'s bulk fixture moved off the retired paths. It was adding 300 refs the code no longer reads,
  which would let a per-changeset regression through.

## Verification

`mise run check` clean, and the pty walkthrough passes. Neither milestone is on the walkthrough's path —
that script records with `--source` and `--commit`, and the derivation only runs when one is empty — so it
is the regression gate rather than the proof. The proof is the three mutation results above.

## Stacked on

`fix-for-each-ref-glob`. Both halves edit `deriveArchiveTip`, which that changeset already rewrites, and its
nested-branch test is one of the assertions this changeset keeps green.
