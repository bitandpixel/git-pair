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

## Milestones

### M1 — Canonical changeset ID

#### Deliverables

- `change init --id <id>` chooses the identity; the branch name is the default source, not the identity.
- `CHANGESET.yaml` carries `id:` alongside `base:`.
- IDs are unique in the git-pair namespace: a collision refuses with §5's message and a suggested
  command, and nothing is ever suffixed to dodge one.
- A changeset whose `id:` disagrees with its directory is reported instead of silently resolved.

#### Tasks

- [ ] `changeset.WriteOptions` and the metadata read path gain `ID`; `parseMetadata` already parses
  arbitrary keys, so this is the struct and the writer.
- [ ] `change init --id` validated by the same character rule `SlugFromBranch` applies; explicit `--id`
  wins over the branch default, and an `--id` that normalises to something other than what was typed is
  refused rather than quietly rewritten.
- [ ] Collision checks before anything is written: the directory at `HEAD` or in the worktree, and any
  ref under the changeset's ref namespace. Both go through one helper so M2's rename moves the check
  with it — `reviewref` should expose the namespace root, not have callers rebuild the path.
- [ ] Re-running `change init` on one's own changeset stays idempotent; colliding with *someone else's*
  changeset of the same ID is the failure.
- [ ] Resolution enforces `id == filepath.Base(Dir)`; disagreement is an error naming both, which is
  §6's "reject ID mutation once refs exist" expressed as a read rule instead of a write rule.
- [ ] Tests: `--id` honoured, default unchanged, collision by directory, collision by ref, hand-edited
  `id:` refused, `--id` with a path separator refused.
- [ ] PRD §9.1 and README's command surface and `CHANGESET.yaml` sample.

#### Verification

Two branches whose names normalise to the same slug: the second `change init` fails with the suggestion
and exits 2; `--id` on the second one succeeds and produces a different directory. A hand-edited `id:`
makes `status` refuse with both names in the message.

### M2 — One archive ref per changeset, in the git-pair namespace

#### Deliverables

- `refs/git-pair/changesets/<id>/archive` is the only durable ref a changeset has before integration.
- `change complete` is gone; `change archive` advances the archive over review-artifact-only commits.
- The archive cannot move backwards.

#### Tasks

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

#### Verification

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

#### Tasks

- [ ] `reviewref`: `Integration(id)`, `ResolveIntegration`, and archive-ref discovery by
  `for-each-ref --points-at`, filtered to `/archive` children. Resolve `--source` through `rev-parse`
  first, so an abbreviated SHA from a CI log resolves before discovery rather than matching nothing.
- [ ] §18 and §19 as written: no match fails saying so; more than one fails listing the candidates and
  naming `--changeset`; `--changeset` disambiguates but never substitutes for a missing archive.
- [ ] Order the §21 checks so the useful failures come first: unknown source, no archive, ambiguity,
  terminal changeset, unknown integrated commit, `--target` reachability, existing integration ref.
- [ ] Reachability via `merge-base --is-ancestor`, with a comment on the re-added helper explaining why
  verifying a caller-named target is not the derivation the anchored-lifecycle plan removed.
- [ ] Create-only write (`update-ref <ref> <new> ""`) as the backstop, behind an existence check that
  prints §22's message instead of git's `fatal:`.
- [ ] The freeze: the single archive-update path refuses once an integration ref exists, for every
  command including `change archive`.
- [ ] `status` gains `integrated` and `integrated_commit` beside `state`; `check` fails an integrated
  changeset with "already integrated at <sha>".
- [ ] `integration record` runs from any branch — it addresses changesets by SHA and ref, not by
  checkout — and reports what it wrote in `--json`.
- [ ] Tests for each failure ordering, the squash shape (approved head A, unrelated target commit B,
  no ancestry between them, record succeeds), and the second-record refusal.
- [ ] PRD section for the command and §13's namespace; README surface, JSON contract, exit codes.

#### Verification

A fixture with no ancestry between A and B proves §15's independence: the record succeeds where any
patch-ID or ancestry heuristic would have failed. Re-recording refuses loudly, `change archive` refuses
afterwards, and the archive ref is unchanged by any later command on the branch.

### M5 — CI ergonomics: absent refs are reported, not mistaken for state

#### Deliverables

- Every command that needs the namespace says so plainly when it is not there, with the fetch to run.
- A documented CI recipe for fetching and for publishing the namespace.

#### Tasks

- [ ] One helper answering "is the namespace empty?" separately from "does this changeset have a ref?",
  because §24's error is about a CI job that fetched the wrong things and §18's is about a changeset that
  was never archived. Conflating them sends someone to the wrong fix.
- [ ] Used by `integration record` and `check` where refs are load-bearing; `status` and `review queue`
  keep working from branches and must not start failing because a CI job fetched nothing.
- [ ] README: the fetch refspec, the push config a human needs to publish refs in the first place
  (documented, not configured — git-pair runs no `push`), and the error they will see if they skip it.
- [ ] Tests: a clone without the namespace produces the fetch guidance, not a false "not ready" or a
  misleading "no archive"; after the fetch, the same command succeeds.

#### Verification

Push a fixture's refs to a scratch remote by hand, clone without the refspec, run each command, compare
against running the fetch first.

## Spikes / research

- `research/2026-09-17-ref-namespace-reality.md` — done before planning. Migration set is empty; the
  four git primitives the spec leans on behave as required, including the two failures git enforces for
  us (create-only refs, leaf-vs-namespace).
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
- **Ambiguous source SHAs** (§19) are reachable in stacked work, where a child branch with no commits of
  its own shares a head with its parent. The escape hatch is specified behaviour, and the test fixture
  should include that shape rather than only the two-independent-changesets case.
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
