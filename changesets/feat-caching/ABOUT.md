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
| `internal/gittest/spawn.go` | `SpawnShim` clears the cache, so an invocation count is a cold measurement |
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
- `internal/gittest/spawn.go` — `SpawnShim` clears the cache first. This was not cosmetic:
  `TestStatusStackChainCostsABoundedReadPerStep` measured 0 invocations for the walk it bounds, and its own
  guard fired. A cost test that reads from a warm cache asserts nothing, and would pass.
- `internal/hygiene` — the invariant test names `internal/git.run` as the shared implementation every git
  command line is built in. `run` is no longer that bottom: it decides whether the memo can answer, and
  delegates to `spawn`. `spawn` is added to `gitCallMethods`, and one `// BAD` case is added to
  `TestDetectorCatchesInjectedViolations` so the entry is proven rather than asserted.
- Manual, on this repository at 41 landed directories and 12 branches, comparing warm output to
  `--no-cache` output and to stderr byte for byte, and on a scratch clone where `refs/remotes/origin/main`
  was moved with `update-ref` the way a fetch moves it, and where a branch was rewound beneath a cache that
  held the reviewed answer.
- Full suite under `go test ./...`.

## Known limitations

- **The first run after a fetch is still cold, and pays nearly everything.** The cache removes repeated
  work, not the work: cold `queue` is 562 subprocesses against 639 on `main`, so the memo takes off the
  duplicates and nothing else. A cold run is now marginally cheaper than it was, but it is the same shape.
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
- Is `--no-cache` the right spelling, or should `GIT_PAIR_NO_CACHE` exist too for CI, where the flag has to
  reach every invocation a job makes?
- `Keep = 4000` entries is a guess. Each entry is a few hundred bytes, so the bound is roughly a megabyte
  per repository. A repository that lands continuously would want it measured against real use.
