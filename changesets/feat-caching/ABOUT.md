# feat-caching

## Summary

`git pair queue` spawned 639 git subprocesses on a repository with 12 local branches and 41 landed changeset
directories. `status` spawned 537 on a branch with no changeset, because the landings report walks every one
of those 41 directories. Both grew linearly with how much work has ever landed, which is the wrong thing for
a read-only command to be slow at.

Two caches are added, each with a different key and therefore a different lifetime:

| layer | key | lives for | why that is safe |
| --- | --- | --- | --- |
| read memo (`internal/git`) | a git invocation's argv and stdin | one command run | arguments name refs, and refs move — so only a run that changes nothing may reuse an answer |
| derived-fact cache (`internal/factcache`) | the commit ids the answer was derived from | until deleted | git objects are immutable, so the answer cannot go stale; a branch that moved is a different key |

Warm, `queue` is 87 subprocesses and 0.27s against 2.24s on `main` — 8.4×. The landings path in `status`
went from 537 subprocesses to 10. The output is byte-identical to the run that ignores both caches, on stdout
and on stderr.

## What changed

| file | what it does now |
| --- | --- |
| `internal/git/memo.go` | new. Classifies each invocation `pureRead` / `liveRead` / `mutation`, memoizes the first, clears on the last |
| `internal/git/git.go` | `run` splits into the memo decision and `spawn`; `Repo` carries both caches and the two switches |
| `internal/factcache/` | new. JSON entries under `<gitdir>/git-pair/cache`, one file per derived fact, pruned by mtime |
| `internal/changeset/chain.go` | `LandedChain` resolves the destination to a commit first and derives from it, then caches |
| `internal/lifecycle/lifecycle.go` | `Summarize` resolves `base` and `headRef` to commits and caches the derivation |
| `internal/cli/landed.go` | the landings report caches one record per landed changeset against the destination commit |
| `internal/cli/root.go` | `loadRepo` turns both caches on for a command run; `--no-cache` turns both off |
| `internal/tui/session.go` | the review session turns the memo off and keeps the fact cache |
| `internal/cli/change.go` | `change wait` drops the memo before each poll round |
| `internal/gittest/spawn.go` | `SpawnShim` turns both caches off, so an invocation count is a cold measurement of the algorithm |
| `scripts/gates/ci-integrate.sh` | `count_calls` sets `GIT_PAIR_NO_CACHE`, for the same reason — it is the second instrument that counts git invocations, and the first pass fixed only the Go one |
| `internal/cli/root.go` | also: `GIT_PAIR_NO_CACHE`, the environment form of `--no-cache` |
| `internal/hygiene/hygiene_test.go` | `spawn` joins `run` as a named git call site, and the detector's self-test proves it is caught |

Git subprocesses spawned, and what each layer took off them:

| | on `main` | memo only | memo + facts | warm wall clock |
| --- | --- | --- | --- | --- |
| `queue` | 639 | 555 | **87** | 2.24s → **0.27s** |
| `status`, no changeset on the branch (the landings path) | 537 | 483 | **10** | — |
| `status --changeset <slug>` | 75 | 60 | 51 | 0.19s → 0.12s |
| `status`, on a changeset branch | 31 | 28 | 19 | 0.09s → 0.05s |
| `check` | 42 | 29 | 20 | 0.11s → 0.05s |
| `change wait`, 3s at `--interval 1s` | 30 | — | 21 | — |
| `review history` | 16 | 15 | 14 | 0.04s → 0.04s |

Cold — the first run after a fetch, with an empty cache — is where the memo alone earns its keep, and it is
not a regression: `queue` measured 2.24s on `main` and 2.27s here, and `check` and `status` were slightly
faster cold than they are on `main`.

## Design decisions

- **The key decides the lifetime, so the two layers cannot be merged.** Everything cached on disk is keyed
  on commit ids, which is what lets the review session keep it across an hour of reading. The memo keys on
  argv, and argv contains ref names like `refs/remotes/origin/main`, so it is only sound while nothing moves
  one. One cache with the union of both behaviours would be a review session reading state from before a
  `git fetch`, which is the failure this tool exists to not have.
- **Ref names are resolved to commits before anything is derived.** `LandedChain` and `Summarize` took a ref
  and asked git about it several times; they now resolve once and read from the commit. That is what makes
  the answers cacheable, and it removes a repeated resolution the ref-name form needed anyway. A destination
  that has moved since produces different commit ids, which is a different key, which is a fresh
  derivation — so there is no expiry policy, no TTL, and no invalidation code.
- **`mutation` is the default classification.** A git subcommand `classify` does not recognize is treated as
  changing the repository and clears the memo. Getting that wrong once costs a slow command; getting it the
  other way costs `status` reporting a review that is not there.
- **Both caches are off until a command asks.** `loadRepo` opts in. A `Repo` built by struct literal — every
  test fixture, and any future code path that mutates mid-run — gets correct reads rather than fast ones.
  This is what the first version got wrong: with the memo on by default, eight TUI tests failed, and the
  reason they failed was correct.
- **The review session keeps the fact cache and drops the memo**, rather than dropping both or clearing at
  each of its many observation boundaries. `reviewModel.forgetPatches` already drops diff and document
  caches on a span toggle or a tool handoff; the memo has no equivalent single boundary, and the saving it
  would offer a session is not worth hunting for one.
- **`Stale` and `Drifted` are outside the cached lifecycle answer.** They come from the working tree and the
  index, and `ReconcileStaleness` fills them in on top. The cache sits inside `Summarize`, not
  `SummarizeAgainstTree`, so no kept answer carries an observation about somebody's checkout.
- **Ages are never cached.** `lifecycle.Age(marker.When, now())` is a fact about the moment of printing, so
  the timestamp is cached and the age is formatted at print time.
- **A cache that can fail a command is a worse trade than one that misses.** Every unreadable, unparseable,
  wrong-format or wrong-key entry is a miss, silently. The file name carries a truncated hash, so a
  collision there is caught by the full key stored inside the file and read back on lookup.
- **`--no-cache` is not a correctness escape hatch.** A cached fact is derived from the same commits the
  uncached derivation reads. It exists to let a slow command be measured against a fast one, and for a
  machine that wants nothing written under its git directory.

## Validation

- `internal/factcache/factcache_test.go` — round trip, every way an entry can be unusable (missing, corrupt,
  wrong format, key collision in the file name), an off store that writes nothing, a directory that cannot
  be written, the prune bound, and a crafted key unable to escape the cache directory.
- `internal/cli/cache_invalidation_test.go` — warm output equals `--no-cache` output for four state changes:
  the destination moving, a branch tip moving, a review submission arriving, and `change ready` recording a
  marker the same command then reports. **Each test asserts that the state change altered the output**, and
  one asserts a cached absence does not render as a row. A comparison of two runs passes whether or not
  anything moved, so without those guards these tests would be green forever — the failure mode a cache is
  uniquely good at hiding.
- `internal/gittest/spawn.go` — `SpawnShim` turns both caches off before counting. This was not cosmetic,
  and it took a second pass to get right. First it only cleared the disk cache, and
  `TestStatusStackChainCostsABoundedReadPerStep` measured 0 invocations for the walk it bounds, its own guard
  firing. Then the environment variable landed and the memo was still on inside the measured run, so the
  `Cost*` tests counted 6,372 subprocesses where `main` counts 10,444 — the memo had quietly absorbed 39% of
  the work the bounds exist to see. With both caches off they count 10,349, which is the same measurement
  `main` makes.
- `internal/cli/cost_instrument_test.go` — guards that guard. Each was checked by deleting the line it
  protects: with `SpawnShim` no longer disabling the caches, the sensitivity test reports the second
  identical run costing 24 invocations against the first's 37; with `cli` no longer reading the variable, the
  file-writing test reports cache files written anyway. Both fail, and both pass once restored. The first
  draft of this file did neither — it measured a repository with nothing landed, where a queue derives
  almost no cacheable fact and the memo cannot cross two `Execute` calls, so it passed under both settings.
  A test that cannot fail is not a test, and this one is only worth having because deleting the fix makes it
  say so.
- `scripts/gates/ci-integrate.sh` — the stack-cost bound found this the way a good bound should: by failing
  on a commit that passed fifteen minutes earlier. The assertion
  `FOUR - THREE == THREE - TWO` says the chain is read one level at a time, and on the same tree CI reported
  `16/24/28/32` once, `16/24/29/32` and `16/25/28/31` twice. Locally it reproduced at the same rate: three
  runs, three different results. `main`'s binary, three runs, reported `25/43/52/61` every time — so the
  variance came from here, and the reason is that `count_calls` runs the real binary against one shared clone
  four times, letting a later run answer from the facts an earlier one left on disk. The fix is the same one
  line `SpawnShim` already had: set `GIT_PAIR_NO_CACHE` for the measurement. With it, three runs on the
  caching binary all report `25/44/53/62` and all 69 checks pass. The answers were never in question — 30 warm
  `check --json` runs were byte-identical to `--no-cache` — which is the distinction that mattered, because
  `scripts/ci/git-pair-integrate.sh` parses that JSON to decide whether to merge.
- `internal/hygiene` — the invariant test names `internal/git.run` as the shared implementation every git
  command line is built in. `run` is no longer that bottom: it decides whether the memo can answer, and
  delegates to `spawn`. `spawn` is added to `gitCallMethods`, and one `// BAD` case is added to
  `TestDetectorCatchesInjectedViolations` so the entry is proven rather than asserted.
- Manual, on this repository at 41 landed directories and 12 branches, comparing warm output to
  `--no-cache` output and to stderr byte for byte, and on a scratch clone where `refs/remotes/origin/main`
  was moved with `update-ref` the way a fetch moves it, and where a branch was rewound beneath a cache that
  held the reviewed answer.
- The interaction with `fix-fetched-base`, which landed on `main` while this was in review and added
  `changeset.MeasureBase` to `NewSession` — the same function that now drops the memo. Measured as the slope
  across repeated rescans so the one-time session open and the fixture setup drop out: dropping the memo costs
  **5 git subprocesses per `Rescan`**, against 0 with the memo kept. That is not a per-tick cost. `Rescan`
  runs from `NewSession` and from `Reload`, and `Reload` has one caller — the branch in `tui.go` that fires
  when an editor or difftool exits, per PRD §15. Five subprocesses once per tool handoff is beneath notice,
  and it is the price of the correctness the memo removal buys: `Reload` exists so a reviewer sees what the
  tool changed, and answering it from a memo would defeat it. The expensive derivations are unaffected, since
  the fact cache stays on for the session.
- Full suite under `go test ./...`.

## Known limitations

- **One duplicated `rev-parse` per stack hop, which the memo absorbs and a cold run pays.** Resolving a
  destination to a commit before deriving from it is what makes the answer cacheable, and it means the
  destination ref gets resolved in two places in one process. Measured on a two-level stack with caching off:
  44 invocations against `main`'s 43, the extra one being a second
  `rev-parse --verify --quiet refs/remotes/origin/main`. With caching on the memo answers it, so this is a
  cold-path cost of one process per hop, inside the recorded ceiling. Removing it means giving `LandedChain` an
  already-resolved commit instead of a ref, which changes an exported signature and its callers — a larger
  API question than this changeset should settle alongside everything else in it.
- **`main` moved during review, and integrating it is part of this change.** `fix-fetched-base` landed at
  `70b5c27`, eight commits past this branch's base, and rewrote how a base and span are derived — the same
  ground this changeset works on. `git pair check` said integration-ready throughout, because it validates the
  tree against what the reviewer saw and not against a destination that has since moved. The merge conflicts
  once, in `internal/tui/session.go`, on adjacent lines rather than in intent: `fix-fetched-base` added
  `s.measure = changeset.MeasureBase(...)` and this branch added `opts.Repo.Memoize(false)`, both immediately
  after the `Session` literal. Both are kept, measure base first, because `Rescan` reads it. The three other
  overlapping files auto-merged, and each was checked by confirming that every line either side contributed is
  still present in the result, which is a stronger check than a clean merge. A reviewer reading the approval at
  `75ffaff` should read the merge commit with it: the two changesets were never built together before it.
- **The first run after a fetch is still cold, and pays nearly everything.** The cache removes repeated
  work, not the work: cold `queue` is 562 subprocesses against 639 on `main`, so the memo takes off the
  duplicates and nothing else. A cold run is now marginally cheaper than it was, but it is the same shape.
- **The test suite gets no faster in wall-clock terms, and the reason is worth knowing.** Measured over
  `internal/cli`, `internal/tui`, `internal/changeset` and `internal/lifecycle`: 24% pass-to-pass variance on
  identical `main` code, so wall time here cannot resolve an effect this size. The deterministic instrument —
  total git subprocesses during `go test ./internal/cli/`, counted from outside — says 39,236 on `main`
  against 30,048 here with the new tests excluded: 23% fewer spawns. It does not become faster in
  proportion, because each test fixture is its own temporary git directory, so the disk cache cannot cross
  fixture boundaries, and the memo is per-process while most tests make one CLI call. The gain is confined to
  tests that call the CLI several times against one fixture.
- **`resolver.at` is not cached**, so `queue` and every `--changeset` still resolve each of the N local
  branches on a warm run — 12 branches is roughly 40 subprocesses here. It is keyed on `(branch tip,
  destination tip)` in principle and is the obvious next increment.
- **`landingView` is not cached as a unit.** It gets the chain and lifecycle answers from the two caches
  underneath it, which is why `status` on one changeset is already fast; the remaining reads are per-single-
  changeset and do not scale with anything the author controls.
- **`change tidy` turned out not to be a target.** It was expected to inherit a win from `ScanBranches`,
  and in the repositories measured it spawns one git subprocess before finishing, so there is nothing there
  to save. It is listed under `What changed` only because it goes through the shared layer, not because it
  got faster.
- **`check`'s gate scripts are outside both caches' knowledge.** A gate script that runs git and changes the
  repository would leave the memo holding answers from before it. The gate runs after the reads it matters
  for, and `--no-cache` is there; nothing enforces it.
- **Concurrency across worktrees shares one cache directory and is unprotected beyond atomic rename.**
  Two `queue` runs in sibling worktrees write the same files. Rename means the reader gets one entry or the
  other, never half, so the worst case is a miss.

## Open questions

- Should the cache be shared across worktrees of one repository, as it is now, or per worktree? Sharing
  means a fetch in one worktree warms the others, which is the common case for a bare-plus-worktrees
  layout, and it means one directory to clean up.
- `Keep = 4000` entries is a guess. Each entry is a few hundred bytes, so the bound is roughly a megabyte
  per repository. A repository that lands continuously would want it measured against real use.
- Separate to this changeset, and untouched by it: `README.md:1266` says CI names the integration branch
  with `GIT_PAIR_DEFAULT_BRANCH` or `--default-branch`. git-pair never reads that variable — the integrate
  workflow uses it as a shell variable and passes the flag — so the sentence promises a feature that does
  not exist. Found while following the `GIT_PAIR_*` naming for `GIT_PAIR_NO_CACHE`. It wants its own
  changeset, either to implement the variable or to correct the sentence.
