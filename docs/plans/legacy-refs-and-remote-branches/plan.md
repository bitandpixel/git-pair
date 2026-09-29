# One durable ref layout, and every branch the clone can name

> **Superseded by `docs/plans/completed/simplify-architecture/plan.md`.** M1 retired a retired layout; the plan
> that replaced this one retired the layout. There is no `refs/git-pair/` namespace left to normalize,
> so the legacy reading, `KindLegacyIntegration`, and the guard built on "nothing under
> `refs/git-pair/``" all went with it. M2's remote-branch claims are unaffected in principle, and were
> not executed.

## Goal

Two claims git-pair makes about refs are weaker than they look, in the same corner of the code.

1. **The retired layout still counts.** `refs/git-pair/changesets/<id>/integration` — written by the
   pre-two-ref code, retired by `7c0a90a` — is still read as a landing record by the unrecorded-landing
   detector (`internal/cli/landed.go:80`) and by the published view (`internal/cli/published.go:103`), and
   `reviewref.KindLegacyIntegration` exists to carry that reading. So today the durable namespace has three
   families that mean something, not two. `origin` holds no retired ref and this repository's five local
   ones are gone, which means the code is maintaining a reading of a layout that, where it still exists, is
   an upgrade residue.

2. **The derivation sees only branches you have checked out somewhere.** `deriveArchiveTip` lists
   `refs/heads/`, so in a CI clone — where the feature branch exists only as `refs/remotes/origin/feat/ux`
   — the reviewed head cannot be derived, and the command asks for `--source` about a head the clone can
   name. This is the same class of failure as the `refs/heads/*` glob fixed in `fix-for-each-ref-glob`: the
   branch is there and git-pair cannot see it.

The goal is that the durable namespace means one thing — `refs/git-pair/archive/<id>` and
`refs/git-pair/integrations/<id>`, nothing else — and that "the branch still carrying the changeset" is
answered about every branch the clone can name, local or remote-tracking.

## Success criteria

- `refs/git-pair/changesets/…` is not a record anywhere in the code, and no document says it is.
- A clone whose only refs under `refs/git-pair/` are retired ones is **not** reported as "this clone has
  never fetched the durable refs". That report would be false, and it is the one report the §13.4 rule
  exists to keep honest.
- `git pair integration record` with no SHAs derives the reviewed head in a clone where the changeset
  branch exists only as a remote-tracking ref.
- A branch present under both `refs/heads/feat/ux` and `refs/remotes/origin/feat/ux` is one candidate, not
  two, and does not turn the derivation into an ambiguity.
- A destination is excluded from the candidates under every spelling it has, including a stale
  `refs/remotes/origin/main` that still carries the directory.
- `mise run check` clean, and the pty walkthrough unaffected.

## Context

- The retirement rationale, written when the layout changed, is in `internal/reviewref/reviewref.go:246`
  and PRD §13.4 (the "a landing the retired layout recorded is recorded" bullet, PRD:273) and README:727:
  reporting every pre-upgrade changeset as a lost paper trail trains people to ignore the finding. That is
  a real cost and this plan accepts it deliberately — see Constraints.
- `reviewref.List` returns classified `Entry` values and `landed.buildRefIndex` derives `NamespaceEmpty`
  from `len(entries) == 0`. The two facts are different: "no durable ref of ours" and "nothing at all under
  the namespace". Today the retired kinds make them coincide. Retiring the kinds separates them, and the
  §13.4 guard must be rebuilt on the second fact, not the first.
- `docs_contract_test.TestRefPathsInTheDocsAreOnesWeWrite` whitelists the retired prefix specifically so
  the migration prose can name it. If the documents stop naming it, that whitelist entry goes with them.
- `deriveArchiveTip` already refuses to choose: two carriers is a usage error naming the candidates
  (pinned in `fix-for-each-ref-glob`). Widening the candidate set has to keep that property, which is why
  the remote-tracking rule below is "the same branch, one candidate" rather than "add more candidates".

## Constraints

- Full retirement, as decided on 2026-09-22, including PRD §13.4 and README. The consequence is stated in
  the PRD rather than softened: in a repository holding retired refs and nothing else, those landings read
  as unrecorded, and `git pair integration record --source --commit` re-records them in the current layout.
- Nothing moves or deletes a durable ref. `CreateOnly` stays create-only; a retired ref is left exactly
  where it is.
- `reviewref.Taken` keeps refusing to reserve a name only a retired ref holds (`internal/cli/id_test.go:83`
  pins it). Two families being the only *records* does not make a retired path free to reuse.

## Assumptions

- `origin` is the remote a CI clone maps feature branches from, and the fetch refspec for
  `refs/remotes/origin/*` is ordinary git rather than git-pair's doing. If a clone maps a second remote,
  the branch-name keying rule below still gives one candidate per branch name; which remote wins is then
  the local-before-remote rule.
- Nobody has a *live* workflow that depends on a retired `integration` ref counting. Verified here: no
  retired ref on `origin`, none locally.

## Milestones

### M1 — the retired layout stops being a record

Deliverables

- `reviewref.KindLegacyIntegration` and `KindLegacyArchive` are gone; `List` classifies only the two
  families.
- "This clone has never fetched the durable refs" is answered from the raw count of refs under
  `refs/git-pair/`, so a namespace holding only retired refs is not called empty.
- The unrecorded-landing detector and the published view treat a retired ref as inert.
- PRD §13.4's bullet, README:510 and README:727 state the new rule and what an upgraded repository should
  do. The docs-contract whitelist entry for the retired prefix is removed with the prose that named it.

Tasks

- `internal/reviewref/reviewref.go`: drop the two kinds, `legacyRoot`/`legacyArchiveChild`/
  `legacyIntegrationChild` and the classification branch. `List` now returns every ref under the root and
  classifies only what it owns — `Entry.Kind` is empty for a ref that is not a durable ref of ours — which
  keeps `len(entries) == 0` meaning "nothing at all under `refs/git-pair/`" without a second `for-each-ref`.
  That is the shape the §13.4 guard needed; the alternative (a separate `NamespacePresent`) costs a git call
  per read to ask a question the same listing already answered.
- `internal/cli/landed.go:80`, `internal/cli/published.go:103`: two families only.
- `internal/cli/landed.go:52,66`: the comments about the layout "not being uniform" describe history;
  rewrite them to describe the code.
- Invert `TestQueueAcceptsALandingTheRetiredLayoutRecorded` to what now happens, and keep the retired ref
  in the fixture so the *other* claim is still pinned: a retired ref is not evidence of a missing fetch.
- `internal/reviewref/reviewref_test.go`, `internal/cli/id_test.go`, `internal/cli/cost_test.go`: update to
  the two-family reading; keep the "reserves no name" case.
- PRD §13.4, README:510, README:727, `internal/cli/docs_contract_test.go`. The whitelist entry for the
  retired prefix stays — the documents still name the path, in order to say nothing reads it — and its
  comment now says that instead of claiming the path is a record.

Verification (done)

- `mise run check` clean.
- `TestQueueReportsALandingTheRetiredLayoutMissed` holds both halves: a retired `integration` ref now puts
  the landing in `LANDED, UNRECORDED`, and the report says "none *in this clone*" rather than the
  "holds no refs/git-pair/* refs at all" hint.
- `TestListKeepsARetiredOnlyNamespaceNonEmpty` pins the same fact one layer down, and
  `TestQueueSaysOnceThatTheNamespaceIsAbsent` still passes, so the empty-namespace report is intact.

### M2 — the derivation names a branch it cannot check out

Deliverables

- `deriveArchiveTip` considers `refs/remotes/**` as well as `refs/heads/`, skipping symbolic `HEAD` refs.
- Candidates are keyed by branch name with the local ref preferred, so `feat/ux` and `origin/feat/ux` are
  one candidate.
- Destinations are excluded by branch name, so `main` excludes `origin/main` even when the remote-tracking
  copy is stale.
- The ambiguity refusal still fires, and still names every candidate, when two *different* branches carry
  the directory.

Tasks

- `internal/cli/integration.go`: a `branchKey(ref)` for the name normalisation both rules need, then the
  widened `RefTips` ask (`refs/heads/` and `refs/remotes/`, local first), the skip for a remote's symbolic
  `HEAD`, and the exclude-by-key for destinations.
- Keep the exclusion of the destination's commit: a destination's tip is already excluded by SHA and stays.
- New `internal/cli` test: a clone with the changeset branch only as `refs/remotes/origin/<branch>` derives
  the pair with no flags. Add one where both spellings exist and the fetched copy is *behind* the local one
  (one candidate, and the local tip recorded — keyed on the commit these would be two carriers and a
  refusal) and one where a stale `refs/remotes/origin/main` carries the directory after `main` moved on
  past it (excluded, because no SHA exclusion reaches that commit).

Verification (done)

- `mise run check`.
- Each of the three rules fails alone: removing the `refs/remotes/` ask fails the derivation test, removing
  the local-before-remote dedupe fails the both-spellings test, and removing the branch-key exclusion fails
  the stale-destination test. The widened ask is not carried by one assertion.
- The parent changeset's derivation tests stay green, including the two-carrier refusal and
  `TestIntegrationRecordDerivesFromABranchNamedWithASlash`.

## Risks

- **An upgraded repository loses a reading it had.** Its old landings read as unrecorded. Mitigation: the
  finding names the commits and the fix is one `integration record` per changeset, and this is a deliberate
  decision rather than an oversight — PRD §13.4 will say so.
- **Retiring the kinds quietly breaks the §13.4 guard.** If `NamespaceEmpty` stays `len(entries) == 0`, a
  retired-only namespace prints "this clone has never fetched the durable refs", which is false and points
  at a fetch that cannot help. This is the one place two milestones collide: M1 must rebuild the guard on
  "nothing under `refs/git-pair/`" and test both fixtures before anything else in M1 lands.
- **Widening the candidate set manufactures ambiguity.** A local branch plus its remote-tracking copy would
  look like two carriers. Mitigation: key by branch name, local first; pinned by test before the widened
  ask is used by anything.
- **A remote-tracking ref can be stale in the other direction** — fetched before the review commit, so its
  tip was never reviewed. This is why the derivation keeps recording the *branch tip it can see* and the
  record stays verifiable: the source's history still has to carry the changeset's markers, which is
  `checkSourceCarriesTheChangeset` and the marker rule. A stale remote-tracking copy fails there rather than
  writing a wrong record quietly.

## Verification strategy

Unit and CLI tests in `internal/reviewref` and `internal/cli` carry both milestones, on `gittest` fixtures
that write the refs by name — the same route the current legacy tests already use. The §13.4 rule is tested
as a pair (retired-only namespace, empty namespace) rather than one, because it is the pair that shows the
two facts are different. `mise run check` per milestone. The pty walkthrough records with `--source` and
`--commit`, so it exercises neither milestone and stays the overall end-to-end gate.

## Audit History

| Date       | Audit | Summary |
| ---------- | ----- | ------- |
