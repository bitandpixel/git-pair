# Does archive-ref discovery work? (spike + measurements)

Date: 2026-09-17

## Question

The requirements make the changeset **archive ref** the source of truth for two things
that currently come from local state: which changeset a branch is working on
(requirements 3, §15–§20), and where a stacked changeset's base comes from (§21, §68).
Under the addendum, the base of a stacked changeset is written in the existing `base:`
key as the parent's archive ref — `base: refs/git-pair/changesets/booking/archive` —
rather than in a separate `base_changeset:` key.

The question is whether that works, not whether it is preferable in the abstract. Two
properties matter and they are separable:

- **Correctness**: do the five discovery rules resolve the cases the workflow actually
  produces, and what do they resolve in the cases it does not?
- **Cost**: the rules run on every `status`, `queue`, `check`, and `diff`, and their
  inputs — refs and history — grow without bound because refs are never deleted.

A prototype of the rules against real git is saved beside this note in
`../artifacts/discovery-spike/` (`discovery.go.txt`, `discovery_test.go.txt`). It is
deliberately separate from the product packages and is not meant to be merged; it exists
so the results below are measurements rather than opinions. The files carry a `.txt`
suffix so `go test ./...` does not try to build them — to re-run them, copy both into
`internal/discovery/` with the suffix removed (the test imports `internal/gittest`, so it
must live inside the module).

## Correctness

Each row is a fixture built with real commits and real refs, and the resolution the
rules produce. All six ran as specified.

| Fixture | Result | Notes |
| --- | --- | --- |
| Stacked child branch, parent readied and archived, child readied | resolves to the **child** | §18's nearest-tip rule works as intended |
| Parent branch and child branch both advance after branching | parent branch resolves to **uninitialized** | see 1 |
| Two changesets landed by a merge commit, no integration recorded | **AMBIGUOUS** (`[one@3 two@3]`) | see 2 |
| Same, with `git pair integration record` run for both | resolves to the later one | recording is load-bearing |
| Two changesets landed by squash, no integration recorded | resolves to the later one | see 3 |
| `change init` run on trunk and never abandoned | trunk **and every branch off it** resolve to it | see 4 |

**1. One movable ref cannot describe two lines of development.** The requirement makes
`change init` *continue* an ancestor changeset by default (§17), so a branch off an
active changeset shares its archive ref — and then both branches run `change ready`,
each moving the shared ref to its own marker commit. Whever moved it last owns it, and
the other branch reports `uninitialized` while its `changesets/<id>/` directory, its
CHANGESET.yaml and its marker commits are all still there. The failure is silent,
because a branch with no changeset is a normal thing to see.

The workaround the spec permits — init a new changeset on the child instead — fixes this
but contradicts §17's own default, and only fixes the branch that remembers to do it.

**2. A merge commit that lands two changesets is ambiguous until integration is
recorded.** The refs of both changesets are ancestors of the merge, at equal distance,
so rule 4 fires and `status` refuses on *every* branch created afterwards, including
branches that have nothing to do with either changeset. Recording integration for both
clears it. So `integration record` is not an optional reporting step in this model: a
merge-commit landing with unrecorded changesets breaks the repository for new work.

**3. Squash integration is invisible.** Squashed commits do not contain the archived
commits, so no archive ref is an ancestor and the branch resolves to whatever else
matches. That is *convenient* — it never triggers 2 — but it means discovery's answer
depends on which integration strategy the repo used, which §15 explicitly claims it does
not ("This works independently of the integration strategy").

**4. A forgotten `change init` owns its descendants forever.** §20 requires an explicit
terminal record to retire a changeset, and the archive ref is created at init (§11), so
a changeset initialised on trunk and abandoned in the author's head is a permanent
ancestor of every branch created from it. Nothing cleans it up: refs are never deleted,
and a ref whose commit is gone makes discovery refuse rather than be ignored.

The guard that follows is simple and not in the requirements: **refuse to initialize a
changeset on the default integration branch.** Trunk is not a changeset branch, and the
misuse 4 describes becomes unreachable instead merely discouraged.

## `base:` holding a ref (the addendum)

Verified with the shipped binary rather than by reading:

- The existing span machinery accepts a ref-shaped base with no changes. `status`
  reports `Span: refs/git-pair/changesets/booking/archive...current`, `change ready`
  accepts it, and the "base is the current branch" guard does not false-positive
  because `rev-parse --symbolic-full-name` returns the archive ref itself, not
  `refs/heads/...`.
- **The value cannot be ambiguous with a branch name.** A branch *typed* as
  `refs/git-pair/changesets/booking/archive` is stored at
  `refs/heads/refs/git-pair/changesets/booking/archive`, so exact-name resolution finds
  the archive ref and only the archive ref.
- Keeping the single `base:` key is a real simplification: no `base_changeset:` field,
  and no "exactly one of" validation.

Two costs come with it:

- **A ref-shaped base makes fetched refs load-bearing for ordinary reads.** In a clone
  that has not fetched the namespace, every read of a stacked changeset fails:
  `cannot resolve changeset base "refs/git-pair/changesets/booking/archive": unknown
  revision`. Today `base: main` resolves in any clone. §24's refspec moves from
  "recommended, needed for integration" to "required, needed to open a stacked
  changeset at all", and CI setup that skips it breaks *before* it gets to
  `git pair check`.
- **The base of a historical commit becomes time-dependent.** `base:` in a commit's
  metadata names a moving ref, so the span for an old commit resolves against wherever
  the parent's archive ref sits today. The queue's landed-check already compares
  `PathsChanged(baseFromMetadata, anchor, directory)`, so a parent whose archive has
  advanced since is compared against a base it never had. Integration freezes the ref,
  which closes the window for landed work — but only for work that was integrated.

## Cost

Rules 1–3 as written need, per archive ref, an ancestry test, a terminal-record test and
a distance: six `git` invocations each. Measured, with 300 archive refs:

| Setup | Measurement |
| --- | --- |
| 300 refs, 301-commit history | **1,802 git invocations** for one resolution (the prototype, counted) |
| 300 refs, 20,000-commit history | 300 × `merge-base --is-ancestor` = **9,065 ms**; with a commit-graph 2,036 ms |
| same | 300 × `rev-list --count <ref>..HEAD` = **9,089 ms**; with a commit-graph 2,339 ms |
| same | one `git rev-list HEAD` = **83 ms**; with a commit-graph **28 ms** |

Read literally, the rules cost about eleven seconds per `status` in a large repository
with a year of landed changesets — and they run on every read, including `queue`, which
would multiply it by the number of branches.

The same answers are available in three invocations: one `rev-list HEAD` (or
`rev-list --parents`) gives ancestry *and* depth for every commit at once, one
`cat-file --batch` reads the candidate commits' messages for terminal trailers, one
`for-each-ref` lists the candidates. That is the shape the implementation must have; the
per-ref formulation in the requirements is not implementable as written. Note that
`rev-list HEAD` is bounded by history depth, so a repository that grows to a million
commits pays for it — an index or a ref-backed cache would be the next step if that ever
matters, and is worth knowing about before committing to the model.

## What this model is genuinely better at

Recorded so the recommendation is not read as opposition:

- **Work survives a deleted branch.** Today a deleted branch takes the changeset out of
  `queue` even though its archive ref and commits are intact. Archive-ref discovery
  finds it. This is a real gain over the claim model, which resolves in the worktree or
  not at all.
- **No branch-name coupling anywhere**, which removes the whole class of stale-claim
  bugs M1 accepted as a cost, including the renamed-branch warning.
- **Stacked bases resolve against a ref that freezes at integration**, which is better
  than a branch name that keeps drifting after landing.
- **Discovery is shared between humans and CI** by construction, which is what §15 wants.

## Not yet answered

- Whether rule 3's "nearest" should be a *signed* distance (HEAD an ancestor of the ref
  counts as comparable, matching the branch that still holds the work) or the spec's
  ancestor-only reading. Signed comparability would fix finding 1 at the cost of
  permitting a shared ref across diverging branches, which is arguably what the
  workflow already assumes.
- Whether discovery should fall back to a reachable changeset directory when no archive
  ref matches. The directory name is not a branch name, so this does not violate §7, and
  it would make finding 1 recoverable.
- What `queue` enumerates when refs, not branches, are the index of changesets — the
  previous plan's finding (M4: an integration ref made `queue` report a landed changeset
  as landed while an active branch was working on it) needs re-deriving against this
  model.
- Which commands need a `--changeset` escape hatch once discovery can answer
  `uninitialized` for a branch that visibly has a changeset.

## Decisions this note is for

1. Adopt archive-ref discovery as the requirement states, with the four findings
   accepted, or adopt it with the amendments above (signed distance, directory fallback,
   refuse-init-on-trunk).
2. Is `integration record` allowed to be a prerequisite for a usable repository
   (finding 2)? If not, rule 2 or rule 4 has to change.
3. Keep the M1 claim model as a fallback under ref discovery, or remove branch discovery
   entirely as §7 asks.
4. Implement discovery as a one-pass history walk (required by the cost numbers), and
   accept that the cost is then bounded by history depth.
