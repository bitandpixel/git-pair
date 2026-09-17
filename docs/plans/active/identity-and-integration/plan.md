# Changeset identity, archive refs, and integration

## Goal

Make a changeset's identity durable and independent of what branch happens to be checked out, keep its
complete unsquashed history behind exactly one ref per changeset, and record — rather than infer — where
that history landed, so CI can gate on `git pair check` and a forge adapter can report integration after
any merge strategy.

## Success criteria

Observable outcomes, not implementation details:

- `git pair change init --id <id>` creates `changesets/<id>/`; without `--id` the branch name still
  supplies the default. A colliding ID fails with the choice spelled out, never with a suffix.
- Every durable ref lives under `refs/git-pair/changesets/<id>/`. Nothing writes `refs/reviews/*`.
- `change ready`, `review submit` and `change abandon` keep `.../archive` current. `change archive`
  advances it over review-artifact-only commits and refuses over implementation commits.
- `git pair check` exits 0 when the changeset is approved, current, and not terminal; non-zero for a
  block, for feedback under the default policy, for drift, and for a terminal or already-integrated
  changeset. `--allow-feedback` relaxes feedback and nothing else.
- `git pair integration record --source A --commit B` finds the changeset by the archive ref pointing at
  A, writes `.../integration` → B, and refuses a second recording, an ambiguous source, and every later
  attempt to move the archive.
- Nothing derives integration from patch IDs, tree similarity, diff equivalence, or commit messages.
- `mise run check`, `e2e-29.sh` and `pty-walkthrough.sh` pass at every milestone.

## Context

`requirements.md` in this directory is the spec, supplied 2026-09-17. It was written against a design
that predates parts of the just-closed `anchored-lifecycle` plan — it still names `change close`,
`CLOSED`, and `READY_FOR_REVIEW` — and it asks for capability that plan deliberately left out: the
landing record. Both directions are reconciled in Decisions below rather than by editing the spec.

What exists now, on `main`:

- Five states (`WORKING`, `READY`, `BLOCKED`, `FEEDBACK`, `APPROVED`). State comes from markers, never
  from commits (`d682d76`); `change unready` withdraws an offer (`fa2424a`).
- `refs/reviews/<slug>` is a movable anchor, updated by `change ready` (`c9bee3a`), `review submit`
  (`internal/reviewops/reviewops.go:65`) and `change abandon` (`8a84a06`).
- `refs/reviews/archive/<slug>/<short-head>` is an immutable per-head ref written by `change complete`
  (`882100c`), which commits nothing.
- `change abandon` records `Review-State: abandoned` and reports terminality *beside* `state`
  (`8a84a06`, PRD §9.7).
- The queue enumerates branches and classifies a branchless directory as landed, never-offered, or
  unexplained (`e55f5ba`); reads accept `--changeset`, writes do not (`80c5d33`).

`research/2026-09-17-ref-namespace-reality.md` measures what the spec depends on: zero `refs/reviews/*`
refs exist to migrate, `for-each-ref --points-at` returns the ambiguity §19 expects, git enforces both
create-only refs and §8's leaf/namespace rule, and `merge-base --is-ancestor` is permitted by the
hygiene test.

## Constraints

- PRD §26 and `internal/hygiene/hygiene_test.go` forbid `push`, `merge`, `rebase`, `reset`, `branch -D`
  and `update-ref -d` in shipped code. `merge-base` is explicitly not caught. So refs are permanent and
  "frozen" is a rule git-pair enforces on its own writes, not a property git guarantees.
- `--json` `state` is an agent contract held at five values. `INTEGRATED` and integration-readiness are
  reported beside it, the way `abandoned_commit` already is.
- Hard renames, no dual-parsing, no deprecated aliases. Precedent: `gitpr`→`git-pair`,
  `GitPR-*`→`Review-*`, `review close`→`change complete`.
- The core protocol stays forge-independent (§33): git-pair is handed two SHAs and figures out the rest.
- `docs/plans/completed/gitpr-mvp/artifacts/e2e-29.sh` and `pty-walkthrough.sh` are live gates; they
  currently exercise `change complete` and `refs/reviews/*` and must move with the code that changes them.
- PRD.md and README.md are the specification and are updated in the same commit as the behaviour.

## Decisions

Settled with the reviewer on 2026-09-17. Rejected alternatives are named so they are not re-litigated.

- **No `CLOSED` state.** `change abandon` and `Review-State: abandoned` stay; terminality is reported
  beside `state`, and `integration record` and `check` refuse a terminal changeset, which is §12's
  behaviour without §25's name. Rejected: renaming to `change close` and adding a sixth `model.State`;
  keeping the verb but renaming the state value.
- **Full namespace move, hard break.** `refs/git-pair/changesets/<id>/{archive,integration}`, with no
  code that still reads `refs/reviews/*`. Rejected: keeping the old namespace and adding integration
  beside it; reading both for a release.
- **The spec's archive model is adopted.** One movable archive ref per changeset, advanced by the
  commands that already anchor, plus `change archive` for review-artifact-only commits. `change
  complete` retires, and the per-head `refs/reviews/archive/<slug>/<short>` refs retire with it:
  durability comes from the single archive ref, frozen once integration exists. Rejected: keeping
  `complete` and layering integration on top of it; keeping `complete` as a verify-only assertion.
- **Sequence: identity, then refs, then CI commands**, in one plan. Refs are named by ID, so deciding
  `--id` after the move would rename them twice.

Consequences recorded rather than asked separately:

- **`READY` stays `READY`.** `READY_FOR_REVIEW` describes the same thing; renaming a wire value for
  prose is a cost with no benefit behind it.
- **`--allow-unreviewed-changes` retires with `change complete`.** The drift question it answered moves
  to where it belongs: `change archive` advances or refuses, and `check` fails on drift with no hatch.
  A CI gate that could be talked past by a flag would not be a gate.
- **Ref-only writes may name any changeset.** The anchored-lifecycle rule that writes refuse
  `--changeset` exists because a marker is a commit and the commit primitive writes at `HEAD`.
  `integration record` writes a ref, so §19's escape hatch is safe and is the spec's own example.
- **The archive ref never moves backwards.** Under the old model each archived head had its own ref, so
  a move was not observable. With one movable ref, an author could rewind it and unsee a review. The
  update refuses a non-ancestor target, which is the invariant §23's freeze depends on.
- **§26's "review-addition validations" bullet stays with `change ready`.** That gate already exists;
  duplicating it in `check` would create two places to disagree about the same rule.
- **A stacked changeset's base is the parent's archive ref, written in the existing `base:` key** —
  `base: refs/git-pair/changesets/booking/archive` — rather than in a new `base_changeset:` key
  (reviewer addendum). Rejected: a second metadata key, which needs "exactly one of" validation and
  gives the metadata two ways to say the same thing. Verified with the shipped binary: the span
  machinery takes a ref-shaped base unchanged, and the value cannot collide with a branch name because
  a branch typed that way is stored at `refs/heads/refs/...`. Costs: fetched refs become required for
  stacked changesets rather than merely recommended, and a commit's base resolves against wherever the
  parent's ref sits today.
- **The default branch comes from a flag or from git, never from configuration.**
  `--default-branch <ref>` on the commands that resolve; otherwise `refs/remotes/origin/HEAD`, then a
  unique `origin/main`/`origin/master`, then a local `main`/`master`; otherwise refuse, naming the
  flag and `git remote set-head origin --auto`. Rejected: `pair.integrationBranch` config — the
  product reads no git config today, the requirements externalise the ref at the call site
  (`integration record --target origin/main`), and config is machine-local, which is the reason this
  plan rejected git config as a home for the `branch:` claim. Measured: `git clone` records
  `origin/HEAD` for a valid remote HEAD, and the CI shape (`init` + `remote add` + `fetch <branch>`)
  does not — one flag or `git remote set-head origin --auto` covers it.
- **Trunk pruning is the repository's business, and the rule's constraint is documented rather than
  coded around.** Landed means "the directory is in the default branch's tree", so deleting landed
  directories from the default branch makes every branch still carrying one look active again —
  measured, including in `queue`. The rule for anyone who tidies: **prune only what carries a terminal
  record** (an integration ref or an abandonment). Those are not hidden from resolution — a terminal
  candidate is reported with its fact, and `queue` and `check` act on it (the integration ref becomes a
  reported fact in M4, which is when the guarantee is complete) — because hiding it would leave
  `status` on such a branch with no answer about a directory that is still in its tree. Rejected
  alternatives, both measured: counting `CHANGESET.yaml` at any depth under
  `changesets/` would make relocating safe for ~2ms, but it defends tidying rather than deleting and
  adds a layout assumption nobody asked for; asking the history (`git log --diff-filter=A`, which does
  catch squash landings) costs ~33ms per candidate on a 20k-commit trunk and is paid by every *active*
  changeset, since those are absent from trunk's tree too. §35 already puts garbage collection out of
  scope, so git-pair neither prunes nor guards the prune.

## Under review after M1 → decided: the tree rule alone

M1 (identity, claim-based resolution) has landed as `8845a7a`/`94b88a8` (plus the batched-claim
performance fix `2c465a9`). The archive-ref discovery requirements reverse the mechanism it chose:
resolution by branch claim versus resolution by ancestor archive ref. `change archive`-shaped M2 is
**not to be built** until the four decisions at the end of
`research/2026-09-17-archive-ref-discovery.md` are made, because they decide whether the claim model
stays, becomes a fallback, or goes away. Measured findings the milestone design has to answer
(prototype in `artifacts/discovery-spike/`, six fixtures, all reproduced):

- a shared movable ref does not describe two diverged branches: whichever moved it last owns it, and
  the other branch reports `uninitialized` while its directory and markers are intact;
- a merge-commit landing with unrecorded integration makes every later branch **AMBIGUOUS**, so
  `integration record` becomes a prerequisite for a usable repository;
- squash integrations are invisible to discovery, so the answer depends on integration strategy —
  which §15 says it does not;
- a forgotten `change init` owns trunk and every descendant branch until someone abandons it, and
  refs are never deleted;
- the rules as written cost six `git` invocations per archive ref — 1,802 calls for one resolution at
  300 refs, about eleven seconds per read in a 20,000-commit repository — so the implementation must
  be a one-pass computation, not per-ref ancestry.

A third rule beat both mechanisms in the same harness: **the changesets on a revision are the
`changesets/<id>/` directories present there and absent from the integration branch**. It passed all
six fixtures — including the two no amendment of the specified rule could fix — and cost 8 git
invocations where the specified rules cost 1,802. It keeps §7 (a directory name is a changeset ID, not
a branch), keeps §15 (a branch and trunk are what CI fetches), and gives `queue` a natural enumeration.
Its gap is a deleted branch, where only the archive ref survives; that is the case ref discovery is
good at, and the two rules may belong together as primary and fallback.

**Decided 2026-09-17**, with the reviewer: the combined rule is adopted (directories first, refs as
the fallback for a directory that no longer exists), `change init` still creates the archive ref at
init per §11, M1's `branch:` claim is deleted rather than kept as a tie-breaker, and M2 is rebuilt to
carry discovery and the namespace move together. The rule is specified, and its fallback cases
measured, in `research/2026-09-17-combined-rule.md`. (Amended the same day: the fallback was
dropped and ambiguity became a recorded decision — see the amendment at the end of that note.)

## Milestones

### M1 — Canonical changeset ID

#### Deliverables

- `change init --id <id>` chooses the identity; the branch name is the default source, not the identity.
- `CHANGESET.yaml` carries `id:` and `branch:` alongside `base:`, and the recorded branch is what makes
  a directory belong to a branch — without it an explicit ID would be invisible to every command.
- IDs are unique in the git-pair namespace: a collision refuses with §5's message and a suggested
  command, and nothing is ever suffixed to dodge one.
- A changeset whose `id:` disagrees with its directory is reported instead of silently resolved, and two
  directories claiming one branch refuse rather than being guessed between.

#### Tasks

- [x] **Resolution had to move first, and the plan did not say so.** `ForBranch` and `AtCommit`
  derived the directory from `SlugFromBranch(branch)`, so `--id booking-transaction-v2` would
  have created a directory no command could find. A directory now belongs to the branch its
  `CHANGESET.yaml` records, claims beat names, and `CHANGESET.yaml` gains `branch:`. That is
  also what fixes a bug nobody reported: a child branch created on its parent's head carries
  the parent's directory, and the name rule could make the queue show the child owning work it
  merely inherited.
- [x] `changeset.WriteOptions` and the metadata read path gain `ID`; `parseMetadata` already parses
  arbitrary keys, so this is the struct and the writer.
- [x] `change init --id` validated by the same character rule `SlugFromBranch` applies; explicit `--id`
  wins over the branch default, and an `--id` that normalises to something other than what was typed is
  refused rather than quietly rewritten. The path-separator check is redundant with the character
  loop and stays for its message: pasting a branch name as an ID is the likely mistake, and
  "must not contain a path separator" says what to fix where "contains `/`" does not.
- [x] Collision checks before anything is written: the directory at `HEAD` or in the worktree, and any
  ref under the changeset's ref namespace. The ref half is what stops a resurrected slug inheriting the
  archive of the changeset that owned the name first. Both go through one helper so M2's rename moves
  the check with it — `reviewref.Taken` and `reviewref.Namespace` are the only places the layout is
  spelled out.
- [x] Re-running `change init` on one's own changeset stays idempotent; colliding with *someone else's*
  changeset of the same ID is the failure.
- [x] Resolution enforces `id == filepath.Base(Dir)`; disagreement is an error naming both, which is
  §6's "reject ID mutation once refs exist" expressed as a read rule instead of a write rule.
- [x] Tests: `--id` honoured, default unchanged, collision by directory, collision by ref, hand-edited
  `id:` refused, `--id` with a path separator refused, a committed deletion releasing the name, and a
  live claim not being reported as stale.
- [x] PRD §9.1 and README's command surface and `CHANGESET.yaml` sample.

Not done, deliberately:

- **`change init --json`.** It has no JSON mode today, and inventing one is a new contract with
  keys to hold stable, which is not what M1 is for. `status`, `queue` and `check` are where
  machines read.
- **Two sibling branches whose names normalise alike are not refused.** Nothing is in use until a
  directory from one is in the other's tree, and refusing on a name that git has not been asked
  about would block a legitimate branch to prevent a hypothetical one.
- **`Slug` stays the field name for the ID.** It is the ID (PRD §4), and the concept is spelled
  `slug` across `reviewref`, `lifecycle`, `marker` and the CLI; renaming it is a mechanical commit
  of its own, and mixing 100 renamed call sites into a behavioural change helps nobody read either.
- **Renaming a branch is a hint, not a gate.** `change init` warns about a claim pointing at a branch
  that no longer exists and says how to fix it, then proceeds, because starting a second changeset
  after a rename is a legitimate answer.

#### Verification

Two branches whose names normalise to the same slug: the second `change init` fails with the suggestion
and exits 2; `--id` on the second one succeeds and produces a different directory. A hand-edited `id:`
makes `status` refuse with both names in the message.

Done. Landed as two commits: the resolution change first (`8845a7a`), because the flag is inert without
it, then `--id` and the collision rules. Mutations run against the working tree with `cp`
backup/restore — dropping the leaf-ref arm of `Taken`, matching refs by bare string prefix, ignoring
HEAD in `Claimed`, allowing a traversal or a separator in `ValidateID`, letting a branch acquire a
second ID, offering no suggestion, and treating every claim as stale. Two initially survived: the
separator arm was redundant with the character loop (kept for its message, now asserted), and one
assertion about stale claims was vacuous because no changeset directory existed in that fixture.

### M2 — One archive ref per changeset, in the git-pair namespace, and the rule that resolves it

#### Deliverables

- Discovery is the tree rule: the changeset directories on this revision that the integration
  branch does not have. No ref-based fallback — resolution reads trees, and refs are for
  archiving, integration and CI. Specified in `research/2026-09-17-combined-rule.md`, including the
  amendment that drops rank 1.
- Ambiguity is resolved by a recorded decision (`change use <id>`), not by a heuristic.
- `refs/git-pair/changesets/<id>/archive` is the only durable ref a changeset has before
  integration, created at `change init` (§11).
- The `branch:` claim from M1 is gone; `CHANGESET.yaml` is `id` + `base` again.
- `change complete` is gone; `change archive` advances the archive over review-artifact-only commits.
- The archive cannot move backwards.

#### Tasks

- [x] Default-branch resolution, one function serving discovery **and** `change init`'s existing
  `defaultBase` (local `main` then `master` today, `internal/cli/change.go:316`): `--default-branch
  <ref>` on the resolving commands, else `refs/remotes/origin/HEAD`, else a unique
  `origin/main`/`origin/master`, else a local `main`/`master`, else refuse naming the flag and
  `git remote set-head origin --auto`. No config key. Two resolvers would let a changeset's recorded
  base and the landed-test disagree, which is the inconsistency this rule exists to remove. Refuse
  rather than treat an unresolvable trunk as "no trunk", which makes every directory a candidate.
- [ ] `changeset.Resolve(ctx, repo, rev, defaultBranch)`: candidates from `ls-tree` of
  `changesets/` in the revision and in the default branch; nearest archive tip, then `base:`-names-parent
  to break a stack, then ambiguity. `ForID` stays for `--changeset` reads; `ForBranch`/`AtCommit` and the claim machinery
  are deleted, including `StaleClaims` and the renamed-branch warning.
  - *Amended while implementing:* a terminal candidate is **reported** (`Candidate.Terminal`) rather
    than excluded at step 0. Excluding it makes `status` on an abandoned branch answer "no changeset
    here" about a directory that is still in the tree — the regression M1's abandoned reporting exists
    to avoid — and the queue and `check` already filter terminal work at their own layer. The
    integration ref likewise becomes a reported fact beside `state` in M4, when something writes it;
    nothing writes one today, and the landing outside the default branch that only it can retire is
    measured in `research/2026-09-17-combined-rule.md`. Reading it as a fact keeps `Resolve` a read of
    what is there instead of a policy about what to hide.
- [ ] No ref fallback. Resolution is rank 0 alone; a branch whose changeset directory was deleted
  reports `uninitialized` with the normal hint. Fixture asserts exactly that, so the omission is a
  decision in the test suite rather than an oversight.
- [ ] `change use <id>`: records which candidate this branch is working on. Refuses an id that is not
  a candidate here, writes `ignores: <id> [...]` into the chosen changeset's `CHANGESET.yaml` and
  commits it. A candidate named in another candidate's `ignores:` stops being a candidate. In the
  chosen file and not the loser's, because an edit under `changesets/<other>/` is implementation
  drift to `change archive` and carries a foreign path into this changeset's landing.
- [ ] Ambiguity message names every candidate and points at `change use` and `status --changeset`,
  since both escape hatches already exist.
- [ ] `status --json` reports the default branch it resolved, its commit, and which source supplied
  it (`flag` / `origin-head` / `sole-candidate`), reported the same way whether the ref came from
  `--default-branch` or from detection. A CI run should be explainable from its own output rather
  than from what the machine happened to have fetched.
- [ ] Cost assertions: resolving on a changeset branch and on trunk stay in single-digit git
  invocations with 300 archive refs present, against 1,802 for the per-ref formulation.
- [ ] `queue` enumerates local branches and resolves each against trunk — two `ls-tree` calls per
  branch, which is the enumeration shape the previous plan's M4 wanted. Archive refs with no branch
  behind them are deliberately **not** listed in this milestone: with refs created at init, a
  branchless ref is as likely to be an abandoned attempt as a deleted branch, and guessing either
  way is a report nobody asked for. Recorded as a known gap.
- [ ] README troubleshooting gains the consequence the tree rule accepts: if someone merges your
  unlanded changeset and lands it, your changeset reads as landed on your own branch, because your
  directory is in trunk's tree. Measured, not inferred.
- [ ] `reviewref`: `Archive(id)`, `Update`, `Resolve` against the new namespace; delete the per-head
  archive writer and its exact-SHA matcher; no reader of `refs/reviews/*` remains.
- [ ] `change archive`: succeeds when the archive is already at `HEAD`; advances when nothing outside
  `changesets/<id>/` changed between the archive and `HEAD`; otherwise refuses naming the paths and says
  a review cycle is required. Reuses `PathsChanged` and the drift machinery from the anchored-lifecycle
  plan rather than computing a second diff.
- [ ] `change archive` refuses a terminal changeset, and refuses when no archive exists yet — the first
  anchor is `change ready`'s job, and saying so is better than silently creating one.
- [ ] Refuse an archive move whose target is not a descendant of the current ref. This is the one place
  `merge-base --is-ancestor` re-enters the write path; document that it verifies a move, not a state.
- [ ] Delete `change complete`, `--allow-unreviewed-changes`, and the `complete_test.go` cases that only
  made sense for completion; re-home the surviving-review-additions and drift cases onto `change archive`.
- [ ] `status` reports the archive as current or stale with its SHA, beside `state`.
- [ ] Update `e2e-29.sh` and `pty-walkthrough.sh` in this commit — they are the live proof that the
  archive survives `git branch -D`, and that proof currently runs through `change complete`.
- [ ] PRD §9.5 rewritten for `change archive`, §13 for the namespace, §12's completion paragraph;
  README's concepts, command surface, JSON contract, troubleshooting.

Landed so far:

- Resolution and default-branch detection, in `internal/changeset/resolve.go`. Additive: no caller
  uses it yet, so this commit changes no output. Fixtures are the spike's ported — stacked,
  stacked-after-parent-landed, diverged pair, checkout with no refs, sibling merge, equal distance,
  deleted directory, on-trunk, pruning — plus the cost assertion (≤10 `git` invocations with 300
  review refs present), counted by `gittest.SpawnRepo` through a `PATH` shim so the code under test
  carries no instrumentation for the measurement.
- Two defects the first version shipped with, both found by mutation testing rather than by reading
  the code: the ordering rule was never exercised, because the `base:`-names-parent filter decided
  the stacked case before distance was consulted; and "no review ref" was treated as *tying* with
  any distance instead of *losing* to one, which made an unarchived candidate block a ready one.
  The tests for both are `TestResolveNearestRefDecidesWhenNothingElseDoes` and
  `TestResolveUnarchivedCandidateSortsLast`.

#### Verification

The seven tree-rule fixtures from the spike, ported to product tests: a stacked child resolves and
so does it after its parent lands; a diverged parent and child both resolve to the shared changeset;
a clone with no archive refs resolves from the tree alone; two unrelated directories are ambiguous
until `change use` records the choice, after which the branch resolves; a branch that merged an
unlanded sibling is ambiguous; a deleted directory is `uninitialized`. Two more fixtures pin the
pruning behaviour rather than fix it: pruning an unrecorded landed changeset from the default branch
resurrects it on a branch that has not merged the prune. (The pair to that fixture — pruning one that
carries an integration ref does not resurrect it — needs a writer for the integration ref, so it lands
with M4; `internal/changeset/resolve_test.go` says so where the first one is asserted.)

Approve, then: archive == the approval commit with no further command. Commit a reply in `ABOUT.md` →
`change archive` advances. Commit an implementation change → `change archive` refuses and names the file,
and `check` (M3) would fail on it. Force a backwards move → refused. Delete the branch → the archive ref
still resolves and `status --changeset <id>` still reads the history.

### M3 — `git pair check`

#### Deliverables

- A non-interactive assertion that the changeset is integration-ready, exit code first, `--json` second.
- `--allow-feedback` as the only policy switch.

#### Tasks

- [ ] One predicate, built from the derivation that already exists: terminal → fail; newest marker's
  outcome → `APPROVED` passes, `FEEDBACK` passes only with `--allow-feedback`, `BLOCKED` and a withdrawal
  fail; archive not at the source head → fail; implementation drift over the reviewed content → fail.
- [ ] §28's wording: `OK: <id> is integration-ready` plus `archive: <sha>`, or `NOT READY:` with one
  bullet per failed condition — every condition, not the first, so CI output is actionable without a
  second run.
- [ ] Exit codes follow the existing table: 0 ready, 1 not ready, 2 usage. No new codes.
- [ ] `--json`: `id`, `ready`, `state`, `archive`, `archive_current`, `reasons` as machine-readable
  strings, `policy`.
- [ ] Works on a checked-out changeset branch; deliberately does not take `--changeset`, since it is the
  thing a forge check runs *on* a branch.
- [ ] Table-driven tests over outcome × policy × drift × archive-current, plus a shell-level assertion
  of `$?` because the deliverable is an exit code.
- [ ] README's agent contract (this is the command agents should call) and a CI example; PRD gets a new
  section beside §9.

#### Verification

The matrix in tests, and one end-to-end run in the live gate: unapproved fails, approved passes, a later
implementation commit fails again, `change unready` fails.

### M4 — `integration record`

#### Deliverables

- `git pair integration record --source A --commit B [--target <ref>] [--changeset <id>]`.
- `refs/git-pair/changesets/<id>/integration` → B, created once, never rewritten.
- Integration-readiness and integrated-ness reported by `status`, and `check` failing once integrated.
- The archive frozen after integration.
- **First landing wins.** A changeset has one integration ref; a backport is refused with the existing
  record named, because a backport is a fact about the release branch and git already records it.

#### Tasks

- [ ] `reviewref`: `Integration(id)`, `ResolveIntegration`, and archive-ref discovery by
  `for-each-ref --points-at`, filtered to `/archive` children. Resolve `--source` through `rev-parse`
  first, so an abbreviated SHA from a CI log resolves before discovery rather than matching nothing.
- [ ] §18 and §19 as written: no match fails saying so; more than one fails listing the candidates and
  naming `--changeset`; `--changeset` disambiguates but never substitutes for a missing archive. The
  two-refs-at-one-commit fixture is built by moving a ref by hand, since git-pair cannot create that
  state itself.
- [ ] Order the §21 checks so the useful failures come first: unknown source, no archive, ambiguity,
  terminal changeset, unknown integrated commit, `--target` reachability, existing integration ref.
- [ ] Reachability via `merge-base --is-ancestor`, with a comment on the re-added helper explaining why
  verifying a caller-named target is not the derivation the anchored-lifecycle plan removed. Note the
  asymmetry the fixtures showed: `--commit` need not descend from `--source` (a squash landing has no
  ancestry between them), but it must be reachable from `--target`.
- [ ] Verify the record rather than believing it: `changesets/<id>/` must exist in `tree(--commit)`.
  Measured true for merge, squash and cherry-pick landings, since the directory is committed content
  that travels with the change; it is what makes a release-branch record pointing at an unrelated
  commit fail.
- [ ] Create-only write (`update-ref <ref> <new> ""`) as the backstop, behind an existence check that
  prints §22's message instead of git's `fatal:`.
- [ ] The freeze: the single archive-update path refuses once an integration ref exists, for every
  command including `change archive`.
- [ ] `status` gains `integrated`, `integrated_commit` and `integrated_target` beside `state`; the
  target matters because a changeset that retired into `release/2.x` and never reached the default
  branch must not look like a default-branch landing. `check` fails an integrated changeset with
  "already integrated at <sha>" naming the target.
- [ ] Second-record refusal prints the existing record's commit **and target**, so a backport attempt
  explains itself: the release branch's own history is the record, and git-pair does not keep a second
  ref per landing.
- [ ] `integration record` runs from any branch — it addresses changesets by SHA and ref, not by
  checkout — and reports what it wrote in `--json`.
- [ ] Tests for each failure ordering, the squash shape (approved head A, unrelated target commit B,
  no ancestry between them, record succeeds), the second-record refusal, and the release-branch shape:
  merged into `release/2.x` with a `main` baseline, the changeset stays active until recorded, and the
  recorded commit's tree carries the directory.
- [ ] PRD section for the command and §13's namespace; README surface, JSON contract, exit codes.

#### Verification

A fixture with no ancestry between A and B proves §15's independence: the record succeeds where any
patch-ID or ancestry heuristic would have failed. Re-recording refuses loudly, `change archive` refuses
afterwards, and the archive ref is unchanged by any later command on the branch.

Measured, and the reason recording is not merely reporting: with the baseline at `main`, a changeset
merged into `release/2.x` resolved as active until its integration ref existed, then retired. Any
landing outside the default branch depends on this command for a correct `status`, which is a CI
documentation requirement, not just a command surface.

### M5 — CI ergonomics: absent refs are reported, not mistaken for state

#### Deliverables

- Every command that needs the namespace says so plainly when it is not there, with the fetch to run.
- A documented CI recipe for fetching and for publishing the namespace.

#### Tasks

- [ ] One helper answering "is the namespace empty?" separately from "does this changeset have a ref?",
  because §24's error is about a CI job that fetched the wrong things and §18's is about a changeset that
  was never archived. Conflating them sends someone to the wrong fix.
- [ ] Used by `integration record` and `check` where refs are load-bearing; `status` and `review queue`
  keep working without the changeset namespace and must not start failing because a CI job fetched
  nothing. They do require the **default branch** — the tree rule compares against it — so the resolver's
  refusal is the correct failure there, and the guidance it prints has to name the fetch rather than
  blame the changeset.
- [ ] README: the fetch refspec, the push config a human needs to publish refs in the first place
  (documented, not configured — git-pair runs no `push`), and the error they will see if they skip it.
  The CI recipe also fetches the default branch, because resolution is a comparison against it.
- [ ] README states the consequence of landing outside the default branch: such a landing is invisible to
  resolution, so `git pair integration record` is what retires the changeset. A release-line repository
  that treats recording as optional reporting will show landed changesets as active forever.
- [ ] README states the pruning rule in the same breath: a repository that removes landed changeset
  directories from the default branch may only prune those with a terminal record, because the landed
  test reads the default branch's tree. Measured, and it is a documentation requirement rather than a
  bug to fix — the resurrection of an unrecorded landing is the rule working as designed.
- [ ] Tests: a clone without the namespace produces the fetch guidance, not a false "not ready" or a
  misleading "no archive"; after the fetch, the same command succeeds.

#### Verification

Push a fixture's refs to a scratch remote by hand, clone without the refspec, run each command, compare
against running the fetch first.

## Spikes / research

- `research/2026-09-17-ref-namespace-reality.md` — done before planning. Migration set is empty; the
  four git primitives the spec leans on behave as required, including the two failures git enforces for
  us (create-only refs, leaf-vs-namespace).
- `research/2026-09-17-archive-ref-discovery.md` + `artifacts/discovery-spike/` — does archive-ref
  discovery work? Prototype of the five rules against real git: six fixtures reproduced (stacked
  nearest-wins works; diverged branches lose the changeset; unrecorded merge integrations are
  ambiguous; squash integrations are invisible; a forgotten init owns its descendants) and the cost of
  the rules measured at 1,802 git invocations per resolution with 300 refs.
- `research/2026-09-17-combined-rule.md` — the rule that was chosen, specified: rank 0 from the
  changeset directories this revision has and trunk does not, rank 1 from archive refs on this line
  only when rank 0 is empty. Seven measured fixtures, the cost table, and the integration-branch
  resolution order the rule newly depends on.
- Open, to settle during M3: whether `check` should re-verify review-addition survival (§26's last
  bullet) or leave it to `change ready`'s existing gate. Current position: leave it, because two
  implementations of one rule drift apart. Record the choice in the plan when M3 lands.

## Risks

- **Retiring `change complete` two days after it landed.** Real cost: PRD, README, the e2e gate and
  anyone's muscle memory. Mitigation: the guard it provided is not lost but relocated — `change archive`
  answers the drift question and `check` refuses what `complete` refused — and all four documents move in
  the same commit as the deletion.
- **A single movable archive ref can be moved by hand.** git-pair will refuse to move it backwards or
  after integration, but `git update-ref` is always available to a person. The honest claim is "git-pair
  never rewrites these", which is what §22 and §34 actually ask for; the plan says that in the README
  rather than implying a guarantee git does not give.
- **Ambiguous source SHAs** (§19) are reachable only through refs moved outside git-pair — a hand-run
  `update-ref`, a restored or copied namespace — since every command that moves an archive ref commits
  first and so names a commit that did not exist before (`research/2026-09-17-ref-namespace-reality.md`).
  The escape hatch is specified behaviour and the recovery path for exactly those cases; the test builds
  the collision with `update-ref` because no sequence of commands produces it.
- **Custom refs do not survive a plain clone.** M5 exists for this; until it lands, CI integration is
  half-broken in a way that looks like a state bug. This is why M5 is a milestone and not a README note.
- **`--points-at` needs the exact object.** Resolving `--source` first is mandatory; abbreviated SHAs
  from CI logs are the normal input, not an edge case.
- **The write path's `--changeset` rule is being narrowed, not contradicted** — but a future reader may
  see `integration record --changeset` and think the rule was dropped. The rule gets restated where it
  lives (`internal/cli/root.go`) with the commit-vs-ref distinction in the comment.

## Verification strategy

- Unit tests where the rule lives: `changeset` for ID and metadata, `reviewref` for namespace, discovery,
  ordering and freeze, `lifecycle` for what a terminal record means to readiness.
- Harness tests in `internal/cli` for every command's exit code and JSON keys, following the
  `contract_test.go` pattern. `--json` keys are asserted as a set, so an added key is a deliberate act.
- Live gates move with the behaviour in the same commit: `e2e-29.sh` (the archive-survives-deletion proof)
  and `pty-walkthrough.sh` (the human path).
- Mutation pass per milestone, against a **committed** tree, using `cp` backup and restore — never
  `git checkout --`, which destroys uncommitted work. The previous plan found three defects this way and
  none from reading the code; the mutations worth writing are the archive freeze, the backwards-move
  refusal, the discovery ambiguity, and each `check` condition independently.
- `mise run check` (gofmt, vet, tests) at every milestone, not only at the end.

## Audit history

| Date | Audit | Summary |
| --- | --- | --- |
| 2026-09-17 | requirements.md | Spec supplied. Reconciled against the closed anchored-lifecycle plan: conflicts in §12/§25 (CLOSED) and §8–§11 (namespace, archive shape, `change complete`); the rest is either already true or additive. |
| 2026-09-17 | research/2026-09-17-ref-namespace-reality.md | Primitives verified; nothing to migrate; §19's ambiguity is reachable in stacked work; `merge-base` permitted by hygiene, so ancestry may return for verification. |
| 2026-09-17 | — | Four decisions settled with the reviewer, all as recommended: no CLOSED state, full namespace move, adopt the movable-archive model and retire `change complete`, identity first. `READY`, `--allow-unreviewed-changes` retirement, and the narrowed write rule recorded as consequences. |
| 2026-09-17 | — | Reviewer corrected the §19 ambiguity claim: an empty child branch shares its parent's head but not its archive ref, because every ref-moving command commits first. Ambiguity is now an out-of-band recovery case, and M4's fixture builds the collision directly. |
| 2026-09-17 | measurements | M1's claim resolution cost 859 extra `git show` calls per queue run (0.29s → 2.3s) and turned `status --changeset` from name matching into a full walk (0.03s → 2.3s) on a 41-branch, 40-directory fixture. Fixed before M2 by `2c465a9`: one `ls-tree` per branch plus one `cat-file --batch` over distinct object ids, back to 0.30s and 0.15s. Worth keeping because the shape is a standing cost: the directory count grows with every changeset that lands and is never garbage-collected (§35). |
| 2026-09-17 | measurements | Archive-ref discovery prototyped (`research/2026-09-17-archive-ref-discovery.md`). The five rules as written resolve the stacked case but leave a diverged parent without a changeset, make an unrecorded merge landing ambiguous for every later branch, answer differently for squash versus merge, and let a forgotten `change init` own its descendants; measured cost 1,802 invocations per resolution at 300 refs, ~11s per read on a 20k-commit history. Two amendments measured (exclude landed refs; comparable-not-ancestor) each fixed part of it. The tree rule — directories here and not on trunk — passed all six fixtures in 8 invocations. M2 is held until the rule is chosen. |
| 2026-09-17 | decision | Combined rule adopted after measurement: changeset directories on the revision and not on trunk decide resolution, archive refs are the fallback for a directory that no longer exists (`research/2026-09-17-combined-rule.md`). Seven fixtures in the spike pass, including the two no amendment of the specified rule could answer; cost 8 invocations on a changeset branch and 12 on trunk against 1,802 for the per-ref formulation. The `branch:` claim goes, `change init` still creates the archive ref, and M2 now carries discovery alongside the namespace move. The one new dependency is resolving the integration branch, which nothing in the product does today. |
| 2026-09-17 | decision | Rank 1 (the archive-ref fallback) removed after the reviewer read its purpose correctly: two of the three justifications were false (a PR checkout has the tree; a rebase that drops the init commit also detaches the archive ref), leaving only a committed deletion of `changesets/<id>/`, where the loss is one confusing message. Resolution is the tree rule alone. Two findings from that probe: merging an unlanded sibling branch makes the branch ambiguous (`[mine@4 theirs@4]`, a case the claim model could not reach), answered by `change use <id>` recording `ignores:` in the chosen changeset rather than the losing one; and landing someone else's merge of your unlanded changeset makes yours read as landed on your own branch, measured with and without any recorded decision — a control run showed the flag was never the cause. |
| 2026-09-17 | decision | The integration branch is a flag or git's own answer, not configuration: `--integration <ref>`, else `refs/remotes/origin/HEAD`, else a unique `origin/main`/`origin/master`, else refuse naming the flag and `git remote set-head origin --auto`. The `pair.integrationBranch` key I had proposed was an invention — the product reads no git config today, the requirements already pass `--target origin/main` at the call site, and machine-local config is what this plan rejected for the `branch:` claim. Measured: `git clone` records `origin/HEAD` when the remote HEAD names an existing branch (path and `file://`); the CI `init`+`remote add`+`fetch <branch>` shape does not; `git remote set-head --auto` fixes it; git refuses to guess when the remote HEAD dangles. |
| 2026-09-17 | decision | Read-side flag named `--default-branch`, not `--integration` and not `--target`. `--target` stays on `integration record` because the two are different concepts: a backport records `--target release/2.x` while the branch defining "landed" for discovery is still `main`, and neither flag can say both. Also found: `defaultBase` (`internal/cli/change.go:316`) already guesses trunk as local `main` then `master` for `change init --base`, so it must fold into the same resolver — two resolvers would let a changeset's recorded base and the landed-test disagree. |
| 2026-09-17 | measurements | How recording interacts with release branches, measured: merged into `release/2.x` with a `main` baseline, the changeset stayed active until its integration ref existed, then retired — so a landing outside the default branch makes `integration record` load-bearing rather than informational. The record becomes verifiable: `--commit` must be reachable from `--target`, and `changesets/<id>/` must be in `tree(--commit)`, which held for merge, squash and cherry-pick landings. Backports: first landing wins, one integration ref, the refusal names the existing commit and target. Also confirmed `git cherry-pick` fast-forwards by default, which is why reachability is a poor definition of "landed" and content is a good one. |
| 2026-09-17 | measurements | Trunk pruning probed: deleting landed `changesets/<id>/` from the default branch resurrects the changeset on any branch that has not merged the prune (measured, `status` and `queue` alike), while an integration ref or terminal record survives it. Decided: document "prune only what carries a terminal record" and leave the rule as a top-level match. Rejected with numbers: any-depth `CHANGESET.yaml` matching (~2ms, defends relocation only) and a history walk for deleted directories (~33ms per candidate on a 20k-commit trunk, charged to every active changeset). |
