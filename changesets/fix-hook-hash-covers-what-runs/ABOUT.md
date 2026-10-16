# fix-hook-hash-covers-what-runs

## Why this exists

A consumer that committed a `pre-ready` hook was refused its own hand-off by a file nobody had edited:

```
the repository's tracked hooks under .git-pair/hooks changed since this clone recorded them
(recorded bd4b061, the tree is now 35f0aa7): the checks they carry have not run on this content —
read what moved, then `git pair hooks --trust` to run it or `git pair hooks --decline` to record
that you read it and chose not to
```

What had moved was `.git-pair/hooks/.pre-ready.swp` — vim's swap file, 16 KB, left by the person who opened the hook
to read it before recording a position. It was git-ignored, it was not executable, and it was not a hook: discovery
would never have run it. The gate refused anyway, and the advice it gave was to re-record a position on a tree that
still contained the swap file — which would have made the same refusal come back the moment the editor closed.

The cause was that one question was answered twice. Discovery decides what runs: at each root a point is one
executable file, or a directory whose entries are taken in lexicographic order after `ignored()` passes over dot-files,
`*.sample`, `*.md`, and `~`. The trust identity did not ask that question. It walked the directory recursively and
hashed every regular file it found. The two sets disagreed in both directions — files that could not run were inside
the acceptance, and nothing guaranteed that files which could run were.

## What becomes different

The identity covers exactly the files that would execute, and it is derived from the same function discovery
executes.

- An editor's droppings beside or inside a hooks directory can no longer invalidate a position. Neither can a note,
  a template, a nested directory, or a stray file sitting beside the point directories.
- A script that discovery would run can no longer sit outside the acceptance. A repository cannot gain a hook that
  runs on inherited consent, which is the property the widened-by-accident identity also put at risk.
- A report of `changed` is now always about content somebody wrote. `git pair hooks --json`'s `tree_hash` moves when
  a runnable entry appears, disappears, changes bytes, or changes executability — and at nothing else.
- A clean clone is not asked to read its hooks again. For a tree holding only hooks, the digest is identical to the
  one the previous release computed, so positions recorded before this ships still answer for the same content.

## What it looks like

The hooks directory a consumer actually had, with the swap file in it:

```
.git-pair/hooks/pre-ready       #!/bin/sh …   0755   ← the one hook
.git-pair/hooks/.pre-ready.swp  16 KB         0644   ← vim, open at the time
```

Before: `accepted bd4b0613…`, `tree_hash 35f0aa78…` → `changed`, and the hand-off refused.
After: `accepted bd4b0613…`, `tree_hash bd4b0613…` → `accepted`, and the hand-off runs the suite.

And the direction that has to stay true — a repository adding a check nobody read:

```
.git-pair/hooks/pre-ready/01-suite    committed, accepted last month
.git-pair/hooks/pre-ready/02-new      added, executable
```

`tree_hash` moves, the state is `changed`, and `02-new` does not run until somebody reads it.

## Design decisions

**One rule, two consumers.** `hookFiles(rootDir, point)` answers "which files here run?" Discovery executes its
result; `TreeHash` hashes its result. The old `collect()` kept the enumeration to itself and `TreeHash` reimplemented
a wider one, which is how the two drifted. `TestTreeHashCoversEveryHookDiscoveryRuns` compares the two sets on every
run and fails if they ever differ, so the drift cannot return silently.

**Why the runner's list rather than git's index.** The obvious fix for "ignored files should not count" was to ask
git which files are tracked — `git ls-files -- .git-pair/hooks` — and hash those. It was rejected for three reasons.
It leaves a hole: gitignore files are local, and `.git/info/exclude` is not shared, so a line nobody else can see
would take a real hook out of the identity while discovery kept running it — a wider gap than the one being closed.
It adds a git invocation to a hot path the cost gate measures at 49 of 50 permitted invocations for a two-level
stack, and the whole trust path currently costs zero. And it would still be a second rule: the identity would be
"what git tracks", the run would be "what the runner takes", and the two would need reconciling again the first time
an untracked script sat in the directory. Naming the set once needs no new question asked of git.

**Non-regular entries are passed over, and a vanished file is not fatal.** An entry whose metadata cannot be read is
skipped rather than made an error, in both consumers: discovery must not start failing because a file disappeared
mid-enumeration, and a hand-off must not be refused by a race in the editor.

**The executable bit stays the only mode hashed.** Unchanged from the previous release: a script becoming runnable
changes what the repository asks to run, while the rest of the permission set is whatever the umask and the writer
left, and hashing it made two worktrees of one commit hash apart.

## What is not changing

- The record itself: clone-wide, one file at `<git-common-dir>/git-pair/trusted-hooks`, a set of hashes with a
  bounded history, `--trust` and `--decline` as the two positions, and the four states.
- The per-clone hooks root under the git directory, which needs no acceptance because the client wrote it.
- Which entries discovery runs. The name rule is the existing one, unchanged; this change makes the identity read it
  instead of inventing its own.
- The refusal wording, the gate's reach (`init`, `change ready`, `review submit`, `check`, `change integrate`), and
  the fact that `GIT_PAIR_HOOKS=0` is not a position.

## Where to pay attention

- **The compatibility claim is the one to try to break.** `TestTreeHashUnchangedForATreeWithOnlyHooks` spells out the
  old walk's formula — path, exec bit, content digest, joined and hashed — and requires the new code to reproduce it
  for a tree holding only hooks. If it ever fails, every consumer with a clean hooks tree is asked to re-read its
  hooks by a release that only changed what gets ignored.
- **The narrowing is deliberate in both directions, and the safety direction is the one to review.** Dropping
  non-runnable entries from the identity is the fix; keeping runnable entries in it is the invariant. If you can
  write a file into a hooks directory that discovery executes but `hookFiles` omits, the change is wrong.
- **`ignored()` is now load-bearing for consent, not only for tidiness.** It was a cosmetic rule about what a hooks
  directory may hold. Anything added to it silently removes entries from what a person is asked to accept, so it
  belongs with the tests it now shares.

## Validation

- `go test ./internal/hooks/` — 17 tests, 0 failures, including three new ones: the swap-file and droppings case, the
  runner-set-versus-hash-set comparison, and the digest-compatibility check.
- `go test ./internal/cli/` — 0 failures (118 s), so the gate's own refusal paths still agree with the new identity.
- `mise run gates` — 132 checks, 0 failures, 142 s. The cost gate is unchanged at 29 / 49 / 58 / 67 invocations
  against a ceiling of 50, because the fix asks git nothing new.
- Mutation checks, each reverted afterwards. Removing `ignored()` from `hookFiles` fails
  `TestTreeHashIgnoresWhatDiscoveryWouldNotRun` with the identity naming `pre-ready/.01-suite.swp`,
  `pre-ready/01-suite~`, `pre-ready/02-off.sample`, and `pre-ready/README.md`. Counting directories as well fails the
  same test. Neither failure is a timeout or a fixture complaint; both name the file that should not have been there.
- The "before" half was observed in the consumer's own worktree: with vim open on the hook, the state read `changed`
  at `35f0aa78…` against a recorded `bd4b0613…`, and `git status` showed the tree clean. The "after" half is what the
  tests assert rather than what a released binary has been seen to do — the installed tool was deliberately not
  replaced, so once this ships, the same tree should read `accepted` at `bd4b0613…` with no new acceptance recorded,
  and `git pair hooks --json` is the command that shows it.
