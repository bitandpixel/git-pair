# One durable ref layout, and every branch the clone can name

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
  `legacyIntegrationChild` and the classification branch. `List` keeps returning everything under the root
  in a second, unclassified count — or exposes `AnyUnderRoot` — whatever keeps `NamespaceEmpty` honest.
- `internal/cli/landed.go:80`, `internal/cli/published.go:103`: two families only.
- `internal/cli/landed.go:52,66`: the comments about the layout "not being uniform" describe history;
  rewrite them to describe the code.
- Invert `TestQueueAcceptsALandingTheRetiredLayoutRecorded` to what now happens, and keep the retired ref
  in the fixture so the *other* claim is still pinned: a retired ref is not evidence of a missing fetch.
- `internal/reviewref/reviewref_test.go`, `internal/cli/id_test.go`, `internal/cli/cost_test.go`: update to
  the two-family reading; keep the "reserves no name" case.
- PRD §13.4, README:510, README:727, `internal/cli/docs_contract_test.go`.

Verification

- `mise run check`.
- A fixture with a retired `integration` ref and no current one: `git pair queue` lists the landing as
  `LANDED, UNRECORDED` (the retirement), and does **not** print the "never fetched" hint (the §13.4 rule).
- A fixture with no ref under `refs/git-pair/` at all: still prints the fetch hint.

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

- `internal/cli/integration.go`: a small `branchKey(ref)` for the name normalisation both rules need, then
  the widened `RefTips` ask (`refs/heads/` and `refs/remotes/`), the skip for `…/HEAD`, and the
  exclude-by-key for destinations.
- Keep the exclusion of the destination's commit: a destination's tip is already excluded by SHA and stays.
- New `internal/cli` test: a clone with the changeset branch only as `refs/remotes/origin/<branch>` derives
  the pair with no flags. Add one where both spellings exist (one candidate, records) and one where a stale
  `refs/remotes/origin/main` carries the directory (excluded, records).

Verification

- `mise run check`.
- The existing derivation tests stay green, including the two-carrier refusal and
  `TestIntegrationRecordDerivesFromABranchNamedWithASlash` from the parent changeset.

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
