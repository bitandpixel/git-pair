# Can git's own hooks replace a git-pair hook runner?

Question from 2026-10-13, before implementation started: rather than build hooks into the tooling, could
git's native `pre-commit` / `commit-msg` / `post-commit` be the mechanism? The answer is measured below, on
git in this repository's environment, with `git-pair` built from `cmd/git-pair`.

## Why the question is worth asking

Every git-pair write point is a commit. `marker.Commit` (`internal/marker/marker.go:182`) calls
`repo.Commit`, which is `git commit --allow-empty -m <message>` (`internal/git/git.go:982`). So
`change ready`, `change unready`, `change abandon`, `change integrate`, `review submit` and `change tidy`
(`internal/cli/tidy.go:185`, via `marker.CommitPaths`) all pass through git's commit machinery, and git
already provides exec, cwd, blocking exit semantics, hook ordering, and a per-clone hook directory.

## What was measured

### 1. All four commit hooks fire, the index is staged, stdin is not a tty

A fixture repository with logging `pre-commit`, `prepare-commit-msg`, `commit-msg` and `post-commit` hooks.
Results at a marker-shaped commit (`git commit --allow-empty -m "git-pair: ready feat-demo"` with
`Review-State: ready` trailers):

| Hook                 | Arguments received               | Message visible        | Index visible | stdin a tty |
| -------------------- | -------------------------------- | ---------------------- | ------------- | ----------- |
| `pre-commit`         | none (`argv: 0`)                 | no                     | yes           | no          |
| `prepare-commit-msg` | `.git/COMMIT_EDITMSG`, `message` | yes                    | yes           | no          |
| `commit-msg`         | `.git/COMMIT_EDITMSG`            | yes                    | yes           | no          |
| `post-commit`        | none                             | yes (file still there) | no (consumed) | no          |

Two consequences. The `pre-submit` reviewer case works natively: `reviewops.Submit` stages at
`reviewops.go:54` before the commit, so the hook sees the reviewer's staged edits and comments. And
`stdin-tty: no` on every hook means the "hook blocks on a prompt" concern is already handled by git, so a
git-pair runner does not need to own that policy either.

### 2. Environment set on the commit reaches every hook

```
GIT_PAIR_CHANGESET=feat-demo GIT_PAIR_HOOK_POINT=ready git commit --allow-empty -m "…"
```

Both variables appeared in the environment of all four hooks. `spawn` already sets `cmd.Env`
(`internal/git/git.go:140`), so git-pair can supply the resolution to native hooks at the marker commit for
a few lines of code.

### 3. A native hook refusal produces exit 3 and no JSON — the deciding finding

Repository with a `pre-commit` hook printing `suite: 3 tests failed` to stderr and exiting 1.

```
$ git pair change ready
git-pair: creating ready marker: git exited with status 1: suite: 3 tests failed
EXIT=3

$ git pair change ready --json
(nothing on stdout)
EXIT=3
```

For contrast, git-pair's own business refusal, from the same command:

```
$ git pair change ready        # dirty working tree
git-pair: working tree must be clean before marking feat ready; …
EXIT=1
```

`root.go:41` publishes 3 as "git itself failed". The path there is `marker.Commit` wrapping
`repo.Commit`'s `*git.Error` with `creating ready marker: %w`, which `Execute` classifies through
`errors.As(err, &ge)` into `exitGitError`. A hook refusal is therefore reported as a git malfunction, which
is the misclassification the exit-code constraint exists to prevent: an agent told 3 goes looking for a
broken git rather than a failing suite. `--json` makes it worse — stdout is empty, so a machine reading the
verdict reads nothing at all.

### 4. `--no-verify` is the override, for free

| Invocation                      | Hooks that ran                                          |
| ------------------------------- | ------------------------------------------------------- |
| `git commit -m one`             | pre-commit, prepare-commit-msg, commit-msg, post-commit |
| `git commit -m two --no-verify` | prepare-commit-msg, post-commit                         |

`--no-verify` skips exactly the two blocking hooks and keeps the two non-blocking ones. `Repo.Commit` takes
`extra ...string`, so `--allow-hook-failure` is a pass-through rather than a new mechanism.

### 5. `core.hooksPath` is one flat directory, and a clone does not carry it

```
$ git config core.hooksPath .git/alt
$ git commit -m three
ALT_PRE_COMMIT          # .git/hooks/pre-commit did not run at all

$ git clone . clone && cd clone
$ git config core.hooksPath
                        # empty
$ ls .git/hooks | grep -c '\.sample$'
14
```

Two properties, both bad for tracked hooks. `core.hooksPath` replaces the whole hook directory, so adopting
a tracked `.git-pair/hooks/` disables whatever else the clone had — husky, lefthook, pre-commit framework.
And it is not cloned, so a fresh clone runs no hooks until somebody performs a setup step, silently. A
repository then believes it has validation it is not running, which is the same failure mode a missing `+x`
bit produces, at a larger scale.

## Conclusion

Native hooks cover the five commit points and nothing else, and they cost the exit-code contract.

**What git supplies at no cost:** the exec itself, cwd, blocking exit semantics, stdin policy, hook
ordering, a per-clone hook location, and the `--no-verify` override.

**What it cannot supply:**

- Any hook at `check`. It writes nothing, so nothing fires — and `check` is one of the points in scope.
- The event name where it is needed. `pre-commit` receives no arguments, so a hook cannot distinguish a
  ready marker from an implementation commit; the only discriminator, the message, is visible only at
  `prepare-commit-msg` and `commit-msg`. Dispatch moves into every hook script.
- The resolved changeset. `slug`, `base`, `head` and the reviewer's outcome would each be re-derived by
  every script, through the ambiguity rules git-pair already implements and discards.
- Tracked hooks without `core.hooksPath`, and with it, without the trust boundary git put under `.git/`
  specifically to avoid executing a clone's code.
- Timeouts, and any machine-readable report of which hooks ran.

**Thin wrapper hooks that call back into `git pair hook run <point>` do not rescue this.** The failure
still travels through `git commit`, so the exit code is still 3, and the repository now also owns installed
files that can drift from the tracked ones.

**Adopted position:** git-pair keeps its own runner, and additionally exports the `GIT_PAIR_*` context to
the marker commit's environment. That is roughly four lines at the existing `spawn` boundary and it makes
native hooks a supported alternative for a repository that prefers git's mechanism, instead of leaving each
script to re-derive the resolution. The runner is not what git gives for free; the `exec` is.
