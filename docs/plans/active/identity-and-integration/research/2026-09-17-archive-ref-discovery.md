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

## Amendments that were tested, not assumed

Two amendments were implemented in the prototype and run against the same fixtures.

**Exclude refs whose target has already landed** (a ref whose target is on the integration
branch is history, not work in progress). This fixes two of the four failures: the
unrecorded merge landing resolves to `uninitialized` instead of `AMBIGUOUS`, and the
forgotten `change init` on trunk stops owning its descendants. It does not fix the other
two: a squash-landed changeset's target is not on trunk, so it is still invisible, and a
diverged parent still resolves to nothing.

**Treat a ref target that contains HEAD as comparable** (the branch that still holds the
work). This was an attempt to fix the diverged parent and it does not: as soon as both
sides commit, neither contains the other, and no rule over one shared ref can attribute
both. Finding 1 is not a bug in the rule — it is what one movable ref per changeset means.

## A rule that survived all six fixtures

Asking a different question resolves every fixture, including the ones the amendments
could not: **the changesets on this revision are the `changesets/<id>/` directories
present here and absent from the integration branch.** A directory that has reached trunk
is landed work, so it drops out with no integration ref and no dependence on the landing
strategy; a directory that exists only on this branch is work in progress, whichever
branch line it is on. Archive refs are still read — to order stacked candidates by
distance and to spot terminal records — but they are not the oracle.

All six fixtures pass under it, measured rather than reasoned:

| Fixture | Rules as written | Tree rule |
| --- | --- | --- |
| Stacked child branch | child ✓ | child ✓ |
| Diverged parent and child | parent `uninitialized` ✗ | both branches resolve to the shared changeset ✓ |
| Merge landing, integration unrecorded | **AMBIGUOUS** ✗ | `uninitialized` ✓ |
| Squash landing, integration unrecorded | resolves, by accident | `uninitialized` ✓ — the same answer as the merge |
| Forgotten `change init` on trunk | owns every descendant ✗ | `uninitialized` ✓ |
| A clone with no archive refs at all | `uninitialized` ✗ | resolves ✓ |

Its cost is also independent of how many changesets have ever existed: **8 git
invocations against 1,802** for the same fixture with 300 archive refs. It satisfies §7
(no branch name in the durable data) because a directory name is a changeset ID, not a
branch; it satisfies §15 (shared with CI) because a branch and trunk are exactly what CI
fetches; and it answers §15's independence claim honestly, because the answer is the same
for merge and squash landings.

Two consequences to decide on rather than discover later:

- **Stacked candidates with no archive refs are ambiguous by distance alone** — measured,
  two candidates and neither has one. The metadata carries the relation: under the addendum
  a child's `base:` names the parent's archive ref, which contains the parent's ID, so the
  parent is "the candidate named as another candidate's base". The stack order is
  recoverable without refs — measured: a stack with an empty ref namespace resolves to the
  child from the tree alone, at one extra blob read per candidate.
- **`queue` has a natural enumeration again**: local branches minus trunk, two `ls-tree`
  calls each, which is the shape the previous plan's M4 wanted.

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

- How `queue` should order and label candidates under the tree rule, and what it reports
  for a changeset whose directory is on a branch that has been deleted — the archive ref
  survives, the directory does not, so the tree rule cannot see it and ref discovery can.
- Which commands need a `--changeset` escape hatch once discovery can answer
  `uninitialized` for a branch that visibly has a changeset.
- Whether discovery should combine the two rules (tree rule first, ref discovery as the
  fallback that covers deleted branches) or whether one is enough.

## Decisions this note is for

1. Which rule decides "which changeset is this": the requirements' ancestor-archive-ref
   rule as written, that rule with the landed-exclusion amendment, or the tree rule
   (directories on this revision and not on trunk), which passed all six fixtures at about
   1/200 of the cost.
2. Whether archive refs are still created at `change init` under whichever rule is chosen
   — the requirements ask for it (§11), and they are what makes a deleted branch's work
   findable, which the tree rule cannot do on its own.
3. Is `integration record` allowed to be a prerequisite for a usable repository (the
   specified rule makes it one; the tree rule does not)?
4. Implement discovery as a one-pass computation rather than per-ref ancestry calls
   (required by the cost numbers either way), and accept that the cost is then bounded by
   history depth for the specified rule and by tree size for the tree rule.
