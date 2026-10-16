# Repository-configured lifecycle hooks

## Goal

A repository can commit deterministic scripts — a test suite, a lint pass, a validation, a push — and
have git-pair run them at the points in a changeset's lifecycle where a person or an agent currently
has to remember to run them by hand.

git-pair today verifies only what it can derive from git: a clean tree, an existing `ABOUT.md`, a
standing approval, an unchanged diff (`internal/cli/check.go`). Nothing between "the author commits"
and "the reviewer reads" checks the content, and the only place repository-owned validation runs is CI,
after the handoff. A hook is the missing extension point: git-pair supplies the moment and the context,
the client supplies the command.

The mechanism is **client control**, not repository enforcement. git-pair publishes lifecycle events; a
team, a reviewer, or one person on one machine decides what those events trigger. That governs both ends
of the design. It is why a reviewer can install a hook that refuses to record a submission whose comments
break the house style, in a repository they cannot edit. It is also why a hook may run git verbs git-pair
itself may not: §26 constrains what git-pair invokes of its own choosing, and a hook body is the client's
own code responding to a git-pair event. The client owns it and owns what it does.

Decided during exploration (2026-10-13), and reflected throughout this plan:

| Question                                     | Decision                                                                                                                                                  |
| -------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Which points get hooks in v1                 | Every write point: the author's (`ready`, `integrate`, `unready`, `abandon`), the reviewer's (`submit`), and the `check` gate                             |
| Where hooks are defined, and who trusts them | Tracked executables under `.git-pair/hooks/`, refused until a person records acceptance, plus a per-clone root under `<gitdir>/` that needs no trust step |
| What a hook may run                          | Anything. §26 constrains git-pair's own invocations, not client code triggered by a git-pair event                                                        |
| Blocking behaviour                           | Pre-hooks block; post-hooks report and do not reverse                                                                                                     |

Row two's second half is what makes this client control rather than repository policy: a reviewer who has
no write access to the repository can still attach their own discipline to the events. Row three needs no
carve-out in §26, only a sentence saying who the subject of that rule is.

## Success criteria

- A repository containing `.git-pair/hooks/pre-ready` that exits 1 causes `git pair change ready` to
  refuse: exit 1, no marker commit written, and the reason names the hook that failed rather than
  suggesting a git-pair rule did.
- The same hook exiting 0 lets `change ready` write its marker exactly as it does today.
- `git pair change ready --json` remains parseable end to end. Hook output never reaches stdout. The
  verdict carries a `hooks` array naming each hook that ran, its status, and its duration.
- `git pair check` in CI runs the repository's `pre-check` hooks and folds a failure into the existing
  verdict: `ready: false` with a reason that names the hook, exit 1.
- A hook is not executed on a clone that has not accepted it. `git pair hooks` reports why, and one
  command records acceptance. Editing a hook invalidates the acceptance.
- Setting `GIT_PAIR_HOOKS=0` runs no hook, so a hook that invokes `git pair` cannot recurse.
- A reviewer installs `<gitdir>/git-pair/hooks/pre-submit` in a clone of a repository that tracks no hooks
  at all, and it runs: a hook that rejects a submission body which breaks their style guide refuses
  `git pair review submit --approve` and permits it once the body is corrected.
- A `pre-submit` hook sees the reviewer's uncommitted edits from the review screen, because it reads the
  index the review commit will be built from.
- PRD.md, README.md, and `skills/git-pair/references/cli.md` name every new command and flag;
  `go test ./internal/cli/ -run Docs` passes, as does `TestTheCommandTreeIsTheCommandSet`.
  (The checks that bind prose to the command tree are `TestCommandsNamedInTheDocsExist`,
  `TestEveryCommandIsNamedInTheDocs`, and `TestTheCommandTreeIsTheCommandSet` in
  `internal/cli/docs_contract_test.go`.)

## Non-goals

- Hooks on read commands (`queue`, `status`, `diff`, `change feedback`, `review history`, `change wait`).
  Those are pure derivations with cost-bound tests behind them, and a hook there makes a read cost
  whatever the repository wrote.
- Hooks on the reviewer's _writing_ commands other than `submit` — `review about`, `review thread`, and
  the review screen. Those write files without recording anything, and a refusal there strands a reviewer
  mid-edit with an answer that has no record. `submit` is the one reviewer point that produces state, so it
  is the one that gets hooks.
- Hooks on `init`, `change tidy`, `change stack`, `change combine`. Same mechanism, lower value; the
  runner is point-agnostic so these are a name and a call site later.
- A hook that reverses an operation. PRD §26 forbids `reset`, so no post-hook failure can undo a marker
  commit. Post-hooks report.
- A manifest format, a hook DSL, or a new config dependency. Discovery is the filesystem; attributes
  are git config.
- Ordering guarantees between clones, environments, or CI. A hook is deterministic on the machine that
  runs it and nowhere else, and the documentation says so.

## Context

**Write points, as the code has them.** `init` (`internal/cli/init.go`), `change ready`
(`change.go:45`), `change unready` (`change.go:202`), `change abandon` (`change.go:324`),
`change integrate` (`integrate.go`), `change stack`/`change combine`, `change tidy` (the only git write
git-pair performs), and the reviewer's `review submit` (`review.go:212`) and `review about`/`thread`.

**The gate is shared code, and that decides where check hooks attach.** `a.integrationGate`
(`check.go:350`) is called by `check` (`check.go:158`) and by `change integrate` (`integrate.go:155`).
Integrate's help says the gate is "the same code rather than an imitation of it". Hooks therefore
attach at the **command entry point**, never inside `integrationGate`. `git pair check` runs `check`
hooks; `git pair change integrate` runs `integrate` hooks and does not run `check`'s. Without that rule
one `change integrate` would run the same script twice.

**Repo-local config already has two precedents.** `.git-pair/about-template.md` is tracked repository
configuration resolved by `changeset.ResolveAboutTemplate` (`changeset.go:926`), and local state lives
under the git directory: `<gitdir>/git-pair/cache` (`internal/factcache`), `<gitdir>/git-pair/marks/<slug>`
(`internal/reviewmark`). A hook trust record at `<gitdir>/git-pair/trusted-hooks` follows both.

**Subprocess precedents exist.** `internal/git/git.go:136` spawns every git invocation with
`exec.CommandContext`. `internal/console/console.go:86` runs a configured command through `/bin/sh -c`
for the editor, and `console.go:134` runs argv-exact commands for the difftool. The editor path is the
cautionary one: its comment documents how shell interpolation over a user-supplied value mangles a path
containing `$`, backticks, or spaces. The hook runner takes the difftool path — argv-exact exec of the
tracked file — and leaves the shell to the script's own shebang.

**No config parser exists to extend.** `parseMetadata` (`changeset.go:667`) is a hand-written
`key: value` reader, and `go.mod` pulls in no YAML or TOML. A manifest with per-hook lists, timeouts and
blocking flags would cost a parser or a dependency.

**Native git hooks were measured as the alternative and do not replace the runner.** Every marker is
`git commit --allow-empty` (`git.go:982`, via `marker.Commit` at `marker.go:182`), so git's own commit hooks
already fire at every write point — which is what makes the question worth asking. They give nothing at
`check`, `pre-commit` gets no arguments at all, and a native refusal reaches the user as exit 3 with empty
`--json` because it arrives wrapped in a `*git.Error`. The one thing worth taking from git is free: the
override is `--no-verify` (`Repo.Commit` already takes `extra`), and git's own hook environment carries
whatever git-pair sets on the commit. Measurements in
[research/2026-10-13-native-git-hooks.md](research/2026-10-13-native-git-hooks.md).

**The submit path decides what a reviewer-side hook can see.** `reviewops.Submit` (`reviewops.go:45`)
validates the outcome, calls `repo.StageAll` (`:54`), snapshots HEAD (`:59`), then writes the marker
(`:66`). `review submit --help` says the command turns "everything in the working tree" into a submission:
source edits, inline comments, ABOUT.md edits and thread files all become part of the review commit. So a
`pre-submit` hook attached after staging reads the index, which is exactly the content about to be
recorded — the same choice git's own `pre-commit` makes, and the reason it can say "this is what you
recorded" rather than "this is what was in your tree at some point". It is also what makes the reviewer's
own edits checkable: they are in that index.

**The hygiene test cannot police hook bodies, and does not need to.**
`internal/hygiene/hygiene_test.go` reads shipped source as syntax and reports forbidden git verbs in
argv literals, bare verb strings, and shell text passed to `exec.Command`. A hook body is runtime data
read from a file, so the scan is structurally blind to it — correctly, because the scan's subject is what
git-pair invokes. The runner must stay inside that subject: it execs the client's file and passes no git
verb of its own, which is a property hygiene _can_ check and should, via a fixture. PRD §26 states "There
is no carve-out. **git-pair may not push anything**, at any point in a lifecycle", which stays true of
git-pair; the edit needed is the sentence that says so explicitly, plus an update to §27, which lists
pre-push hooks among the deferred mechanisms.

## Constraints

- **Exit codes are a published contract** (`root.go:41`): 0 success, 1 business-rule refusal, 2 usage,
  3 git failed. A failing hook is a repository refusal — exit 1 — and the message must name the hook,
  because an agent that reads exit 1 goes looking for a git-pair rule that refused nothing.
- **`--json` is machine-readable and arrays are never null** (`orEmpty`, `root.go`). Hook output goes to
  captured buffers and is replayed to stderr, never stdout.
- **Never block on a terminal.** Agents run these commands. Hooks get `/dev/null` on stdin unless they
  ask for the terminal, and each hook runs under a context deadline.
- **A marker records its head.** `Review-Head`, `Review-Base-Head` and `Review-Diff-Id` (`model.go`)
  name the commit a marker spoke about. A hook that commits after the marker, or that edits files before
  the clean-tree check, breaks the alignment the whole gate depends on.
- **`change ready` requires a clean tree first** (`change.go:79`). Hooks therefore run after the built-in
  checks and before the marker commit, and a pre-hook that dirties the tree is a refusal rather than a
  surprise.
- **A reviewer's submission is built from the index.** `reviewops.Submit` stages everything then commits
  (`reviewops.go:54`, `:66`), so `pre-submit` attaches after staging to see what will be recorded. That
  leaves staged content behind on refusal, which the refusal message has to state.
- **Docs are checked prose.** Any new command or flag must appear in `PRD.md`, `README.md`, and
  `skills/git-pair/references/cli.md` or `docs_contract_test.go` fails the build.
- **Read-path cost bounds stay intact.** `cost_test.go` and `cost_instrument_test.go` bound subprocess
  work; hooks touch only write-path commands.

## Design

### Discovery

`.git-pair/hooks/<point>` is either an executable file or a directory whose executable entries run in
lexicographic order. One rule covers the single-script case and the several-scripts case without a
parser, and matches how git names hooks by the event they answer.

Point names use the existing lifecycle vocabulary — the marker kinds in `internal/lifecycle/lifecycle.go`
plus `check` and the reviewer's submission:

```text
pre-ready     post-ready
pre-integrate post-integrate
pre-unready   post-unready
pre-abandon   post-abandon
pre-check     post-check
pre-submit    post-submit
```

`<point>` is matched exactly; an unknown name under a hook root is reported by `git pair hooks` rather
than ignored silently, because a typo'd hook is otherwise indistinguishable from an absent one.

Two roots, both using that layout, run in order at each point:

```text
<worktree>/.git-pair/hooks/<point>   # tracked, shared, needs the trust step
<gitdir>/git-pair/hooks/<point>      # per-clone, never shared, trusted by construction
```

The second root is what makes this a client-control feature rather than repository policy: a reviewer
attaches their own checks to `pre-submit` without committing anything to a repository they may not own,
and it is the same mechanism the author uses, so there is one runner and one layout. It follows git's own
division, where `.git/hooks` is yours and `core.hooksPath` is somebody's choice. Tracked hooks run first:
the repository's rules are the shared baseline, and the client's run after them and can add to them.

Attributes that have no filesystem home come from git config, which needs no parser and is per-clone:
`git-pair.hookTimeout` (default 10m), and `git-pair.hooks = false` as the per-clone off switch.

### Execution

Execve the tracked file directly, argv-exact, with:

- cwd = `repo.Dir`;
- stdin = `/dev/null`;
- stdout and stderr captured, replayed to git-pair's stderr when the hook failed or when `-v` was asked for;
- a context deadline from `git-pair.hookTimeout`;
- environment extended with the context the hook needs, following the existing `GIT_PAIR_*` convention
  (`GIT_PAIR_DEFAULT_BRANCH`, `GIT_PAIR_NO_CACHE`):

```text
GIT_PAIR_HOOKS=1              # or 0 to disable, inherited by the hook's own children
GIT_PAIR_HOOK_POINT=pre-ready
GIT_PAIR_CHANGESET=<slug>
GIT_PAIR_BRANCH=<branch>
GIT_PAIR_BASE=<base ref>
GIT_PAIR_HEAD=<head sha>
GIT_PAIR_DEFAULT_BRANCH=<ref> # already set by the app
```

Reviewer-side points add the submission's own context, which a hook cannot derive for itself because the
outcome is a flag value and the body is a string until the marker is written:

```text
GIT_PAIR_REVIEW_OUTCOME=approve|feedback|block
GIT_PAIR_REVIEW_BODY_FILE=<path>   # the -m body, in a file, so quoting cannot mangle it
GIT_PAIR_REVIEW_HEAD=<sha>         # the commit the submission will name in Review-Head
GIT_PAIR_HOOK_ROOT=<worktree|clone>
```

Hooks at one point run in order and stop at the first failure — a suite that fails at minute one should
not be followed by three more suites.

A missing `+x` bit is a refusal with the fix in the message, not a skip: a hook nobody made executable
is the most likely way a repository ends up believing it has validation it does not run.

### Trust

A tracked file is code that runs on clone, and `git pair init` in a stranger's repository should not
execute it. Acceptance is recorded at `<gitdir>/git-pair/trusted-hooks` as the hash of the
`.git-pair/hooks/` tree. git-pair runs tracked hooks only when the recorded hash matches the tree; a
changed or new hook file invalidates it. An untrusted tree means those hooks do not run and the command
says so on stderr — the operation itself proceeds, so a person can still work in a clone they have not
approved yet, which is the same asymmetry `--configure-fetch` uses for consent.

The per-clone root needs no such step. Code the person wrote, under their own git directory, is trusted by
construction; requiring consent for it would be consent to oneself.

`git pair hooks` reports both roots, the point names found, their order, the trust state, and the last
result of each. `git pair hooks --trust` records the hash, and prints the tree it is approving so the
person consenting can see it.

### Reporting

Every command that runs hooks reports them, in prose and in JSON:

```json
"hooks": [
  {"point": "pre-ready", "name": "01-test", "status": "passed", "duration_ms": 41234}
]
```

`status` is `passed`, `failed`, `timeout`, `skipped-untrusted`, or `not-executable`. The array follows
`orEmpty`: `[]` when no hook is configured. `check`'s JSON gains the same array beside `reasons`; a
failed pre-check hook also appends a reason naming it, so a CI job that reads only `ready` and `reasons`
still learns what refused the merge.

Post-hooks run after the operation is visible. Their failure exits nonzero and prints the hook's output;
it does not reverse anything, and the printed line says what actually happened ("READY — post-ready hook
02-notify failed"), because a reader who is told only "failed" will assume the marker was not written.
A post-hook that moved HEAD is detected (HEAD before against HEAD after) and reported as a warning that
names the marker it invalidated.

### What the record carries, and what it does not

Two mechanisms, and they get different answers.

**A new key that git-pair reads: no.** `Review-Head`, `Review-Base-Head` and `Review-Diff-Id` are claims
about a commit that `check` can re-derive and compare. A hook's exit status is a claim about whatever a
machine did at 14:32, and a rebase preserves the message while the thing it described stops being true. A
record that is verifiable from history and one that is environment-conditional do not mix, so nothing
`check` branches on is added.

**Lines a client appends: yes, under constraints.** Raised in review on this plan and measured in
[research/2026-10-13-appending-to-the-marker-message.md](research/2026-10-13-appending-to-the-marker-message.md).
`Message.Render` (`marker.go:34`) emits subject, body, then trailers as separate paragraphs, and
`parseTrailers` (`lifecycle.go:713`) receives git's own `%(trailers:only,unfold)` and keeps the first value
seen for a key (`:724`). Two properties follow, both measured:

- Appending a key at the **end** of the block cannot forge anything, because the real keys are already
  earlier and first-wins protects them. The same two keys in the opposite order do change the derived state,
  so append position is the security property rather than a formatting preference.
- Git takes the trailer block to be the **last paragraph**, and requires every line in it to be trailer-shaped
  or a continuation. One line that is neither removes the whole block, including the authoritative trailers
  above it. So a client destroys the marker three independent ways, and one is quiet: a blank line
  manufactures a new paragraph and evicts the real trailers; a colon-less line empties the block; and a line
  beginning with whitespace is a continuation that appends itself onto the preceding real trailer's value,
  leaving a block that still _lists_ `Review-State` and `Review-Changeset` while corrupting one of them out
  of matching its slug. Nothing errors in any of the three; `change ready` still exits 0, and the changeset
  simply stops appearing in the queue.
- Sanitising after the fact does not work. Appending a blank line _after_ the client's own line is the
  eviction case again. Separation is not a mitigation; order and shape are.

So the append is git-pair's job, not the hook's: `Message` gains a field for client lines rendered after
`Trailers` in the same paragraph, and four validations run on each before the commit — no `\n` or `\r` in the
value, the rendered line must be trailer-shaped with a non-empty key, the rendered line must not begin with
whitespace, and reserved keys and the `Review-` prefix are refused. Each maps to a measured row, and a
rejection names the hook and the row it hit rather than dropping the line silently, which would leave a
repository believing it had recorded a result. A client prefix (`Hook-`, or `X-Hook-`) keeps the reserved
namespace reserved, and the line names the head it describes (`Hook-Test: pass head=<sha> dur=41s`) so a
rebase leaves a claim visibly stale rather than silently true.

The other route stays available and needs no design at all: `review submit` commits everything in the working
tree, so a hook that writes a log file into the changeset directory lands it in the review commit. That is
the client-control principle applied consistently, and it suits a result with bulk in it, where a trailer
line would be the wrong shape.

One line holds both mechanisms apart from becoming a contract: `check` never reads client-appended keys.
The moment the gate branches on one, it is part of the contract and the "not a schema change" argument is
gone. Persisting a result and letting it gate something are different things.

A team that wants a gate on their own result writes it themselves, as a `pre-check` hook reading their own
trailer. That is the escape hatch and it is a good one — the thing it reads is history rather than the
environment, so unlike every other hook it behaves identically on every clone and is the best-shaped hook to
run in CI. Three caveats come with it and all three are the client's to own: the hook must do its own
ancestry test, because a rebase preserves the trailer's message; there is no override flag on `check`, by
PRD §19.3's reasoning, so a client whose own hook refuses has to fix the work or remove the hook; and a
missing trailer is a decision rather than a default, since it can mean the hook never ran. Details in the
research note.

### Override

A pre-hook that blocks with no escape gives a client no way to record a verdict when their own hook is
wrong. The project already splits this cleanly: `change ready` has `--allow-surviving-review-additions`
because it is an offer, and `check` deliberately has no override because PRD §19.3 says a gate is the wrong
place to acknowledge an exception. Hooks follow that split rather than inventing a third answer:

```text
git pair change ready   --allow-hook-failure
git pair review submit  --allow-hook-failure
git pair check          (no override; a failed pre-check hook refuses the merge, by design)
```

The flag proceeds and says so in prose and in the `hooks` array (`status: "overridden"`), so a record of
the exception survives in the output of the run that took it. `GIT_PAIR_HOOKS=0` remains the blunt bypass
for a hook that hangs, and differs from the flag in that it does not run the hook at all.

## Milestones

### M1 — One hook, end to end

The vertical slice: runner, discovery, trust, one point.

Deliverables

- `.git-pair/hooks/pre-ready` runs before the ready marker and blocks it on nonzero exit.
- `git pair hooks` and `git pair hooks --trust` exist and report the trust state.
- `change ready --json` carries the `hooks` array.
- PRD, README, and the skill name the directory, the two commands, and `GIT_PAIR_HOOKS`.

Tasks

1. New `internal/hooks` package: `Plan(ctx, repo, point) ([]Hook, error)` for discovery over both roots,
   and `Run(ctx, repo, point, env) (Result, error)` for execution. argv-exact exec, `/dev/null` stdin,
   captured output, context deadline.
   Note for the runner's own source: hygiene Layer B reports a string literal _equal_ to `push`, `merge`,
   `rebase` or `reset` anywhere in a shipped file, so the runner cannot carry those words as data, and
   error text about them has to be written so it does not become a bare literal.
2. Trust store beside the other two git-directory stores: `<gitdir>/git-pair/trusted-hooks`.
3. Export the resolution (`GIT_PAIR_CHANGESET`, `_BRANCH`, `_BASE`, `_HEAD`) into the environment of the
   marker commit so a repository that prefers git's own `pre-commit`/`commit-msg` hooks gets the context
   `pre-commit` cannot derive for itself. `spawn` already sets `cmd.Env` (`git.go:140`), and the
   measurement in the research note confirms the values reach every hook.
4. `app.runHooks(ctx, s, point) error` as the single call site, plus the `GIT_PAIR_HOOKS=0` and
   `git-pair.hooks=false` off switches.
5. Call it in `runChangeReady` after the built-in checks (`change.go:104` `refuseIfAbandoned`, then the
   survival check at 108) and before `marker.Commit` at 129.
6. Verify the tree is still clean after pre-hooks; a hook that dirtied it is a refusal naming the hook.
7. `newHooksCommand` in `internal/cli/hooks.go`, registered in `newRootCommand` (`root.go`).
8. Tests in `internal/cli/hooks_test.go` on `gittest.New(t)`: passes, fails, missing exec bit, timeout,
   untrusted tree, `GIT_PAIR_HOOKS=0`, dirty-tree-after-hook, hook stdout kept out of `--json`.

Verification

- `go test ./internal/hooks/ ./internal/cli/` green.
- Manual, in a fixture repo: a failing `pre-ready` leaves `git log` unchanged and exits 1; a passing one
  writes the marker; `--json` parses through `jq` with hook output on stderr.
- `go test ./internal/cli/ -run Docs` and `-run TestTheCommandTreeIsTheCommandSet` green with the new
  names in all three documents.
- `go test ./internal/hygiene/` green.

### M2 — Post-hooks and the remaining author points

Deliverables

- `post-ready`, `pre-unready`/`post-unready`, `pre-abandon`/`post-abandon`,
  `pre-integrate`/`post-integrate`.
- Post-hook failure reported without reversal, and HEAD-move detection against the marker just written.

Tasks

1. Split `app.runHooks` into `runPre` (blocking) and `runPost` (reporting, returns a report struct).
2. Snapshot HEAD before the marker commit and compare after the post-hooks; warn when they moved it.
3. Wire the four author commands. Each keeps its own JSON contract and gains the `hooks` array.
4. Same test table per point, plus: a post-hook that commits produces the warning and leaves the marker
   intact; a post-hook that fails exits nonzero and leaves state as it is.

Verification

- Table test per point asserts refusal leaves no marker and success writes exactly one.
- Manual: `git pair change integrate` with a failing `pre-integrate` writes no declaration; with a
  failing `post-integrate` writes it and exits nonzero.

### M3 — The gate

Deliverables

- `pre-check` / `post-check`, run at the `check` entry point only.
- A failed pre-check hook becomes a `reasons` entry naming the hook, so the CI contract is unchanged in
  shape: `git pair check --json | jq -e '.ready and .integrating'` still answers the whole question.
- A documented CI example that runs the repository's suite inside the gate.

Tasks

1. Call `runPre` in `runCheck` (`check.go:153`) before `a.integrationGate`, and `runPost` after the
   verdict is built.
2. Append the hook reason to `out.Reasons` and set `out.Ready = false`; keep the reason wording distinct
   from git-pair's own conditions so a reader can tell who refused.
3. Confirm `change integrate`'s path (`integrate.go:155`) does not reach the hook call — a test that
   counts invocations, in the style of `gittest.SpawnShim`.
4. `check --json` test: a failing hook yields `ready: false`, a hook-naming reason, exit 1, and parseable
   JSON.

Verification

- The hook runs exactly once per `git pair check` and zero times per `git pair change integrate`.
- CI example in README exercised against a fixture repository.

### M4 — The reviewer's submission

Deliverables

- `pre-submit` / `post-submit`, run at the `review submit` entry point.
- `pre-submit` runs after staging and sees the index: the reviewer's source edits, inline comments,
  ABOUT.md edits and thread files, exactly as the review commit will carry them.
- The reviewer-side environment contract (`GIT_PAIR_REVIEW_OUTCOME`, `GIT_PAIR_REVIEW_BODY_FILE`,
  `GIT_PAIR_REVIEW_HEAD`, `GIT_PAIR_HOOK_ROOT`).
- The per-clone root `<gitdir>/git-pair/hooks/<point>`, so a reviewer attaches their own checks to a
  repository they cannot edit.
- `review submit --allow-hook-failure`, and `--json` carrying the `hooks` array with `overridden`.

Tasks

1. Wire `runPre` into `runReviewSubmit` (`review.go:273`) after `refuseIfAbandoned` and around
   `reviewops.Submit`: staged, then hooks, then the marker commit. Keep `Submit` itself free of hook calls
   so the review screen's own submit path stays covered by one call site.
2. Pass the outcome, the `-m` body in a temp file, and the HEAD the submission will name.
3. Leave the index as the hook left it on refusal, and say so in the message — a reviewer who runs a
   blocking hook and gets a refusal has staged content and no commit, and needs to know which.
4. Add the per-clone root to `Plan` alongside the tracked one, ordered after it, with no trust check.
5. Test table per point, plus: a `pre-submit` hook that reads the index sees a reviewer edit made in the
   review screen; a hook that rejects a body pattern blocks `--approve` and permits it once the body is
   fixed; `--allow-hook-failure` records the submission and reports `overridden`; a per-clone hook runs
   where the tracked tree has none.

Verification

- A `pre-submit` hook that rejects `approve` unless a suite passes leaves the branch with no review commit
  when the suite fails, and with the reviewer's expected `Review-Head` when it passes.
- The same hook in `<gitdir>/git-pair/hooks/` with nothing tracked runs, and `git pair hooks` shows it
  under the clone root.
- Manual, following the reviewer flow from README: edit a file in the review screen, submit, see the hook
  refuse on the style violation and accept on the corrected one.

### M5 — Spec, skill, and the §26 sentence

Deliverables

- PRD §26 restated to name its subject: git-pair invokes no push, merge, or ref write of its own
  choosing, and a hook is the client's own code responding to a git-pair event, under the client's
  authority. A clarification of scope, not a carve-out.
- PRD §27's deferred-pre-push-hooks entry updated to say which half this feature closes and which stays
  deferred.
- A skill reference page covering both author and reviewer points, the environment contract, the two
  roots, and the per-clone-determinism caveat.
- A hygiene fixture asserting the hook runner itself invokes no forbidden git verb.

Tasks

1. Draft the §26 wording change first and get it agreed. It is a sentence about who the rule binds rather
   than an exception to it, which is why M1–M4 can proceed in parallel, but nothing merges before it lands.
2. `skills/git-pair/references/hooks.md`; `skillDocs` walks the directory, so coverage is automatic.
3. Add the hook-runner case to the `hygiene_test.go` fixture list, alongside the existing benign
   lookalikes.
4. README hook reference: discovery, ordering, environment, trust, exit-code meaning, CI recipe.

Verification

- `go test ./internal/cli/ -run Docs`, `-run TestTheCommandTreeIsTheCommandSet`, and
  `go test ./internal/hygiene/` green.
- `git pair skill show` renders the new page.

### M6 — The unaccepted state, made a decision rather than an absence — landed as `d4c8853`

Arises from the open question M1 carried through the stack, put to the reviewer during M5: should a repository
that has committed tracked hooks refuse outright in a clone that has not accepted them. The reviewer's answer on
2026-10-14 was to lean towards making it mandatory, with an explicit `declined` state as the escape hatch, and
to record where `declined` lives: per-clone, beside `trusted-hooks` in the git directory, keyed by the same tree
hash acceptance is keyed by.

The store is what constrained the shape, and it is worth keeping on the record because it is the part a later
reader will ask about. A record under `<git-dir>/` is a fact about one machine, exactly like acceptance. A verdict
another clone has to reach cannot read it, and a CI checkout is a clone that has accepted nothing — so making the
gate a refusal means a fresh clone refuses everything, and the way a pipeline passes is to take a position in the
job. The reviewer took that trade knowingly: no repository has tracked hooks yet, so the mandate lands on nobody
who is using the tool today, and the alternative — visibility on every surface with no refusal — was the position
that could not say "this team requires the suite".

Deliverables

- The four trust states named and reported: `accepted` (tree hash matches), `changed` (the tree moved since
  acceptance), `declined` (hash matches what this person declined), and `unrecorded` (never seen). The state names
  are the code's; the decision text above called the fourth `unaccepted` before the code existed.
- `git pair hooks --decline` as the human act beside `--trust`, printing what it records, and the report showing
  all four with the hash each position was taken against.
- The commands that start or advance work asking for a position: `init` before a changeset directory exists,
  `change ready` before the hand-off, `review submit` before the reviewer's content is staged, and the gate behind
  `check` and `change integrate`. One sentence, naming both ways out, from one helper so the surfaces cannot drift.
  `accepted` and `declined` clear it; `change unready` and `change abandon` deliberately do not ask, because they
  take work out of the queue, and `git pair hooks` is never gated so that reading precedes deciding.
- The PRD's hook contract, the README, and `references/cli.md` carrying the fourth state and the demand.
- The skill's hook reference carrying the difference between `GIT_PAIR_HOOKS=0` (the machine owner's escape hatch,
  which is not a position and does not lift the refusal) and `declined` (a stated position about this repository's
  scripts), and the rule that neither `--trust` nor `--decline` is an agent's command to run.

Verification

- Unit tests for the four states, and one that a hook added to a declined tree is `unrecorded` again rather than
  still declined — the hash rule is the whole safety of the state.
- `check --json` per state: the two silent ones refuse with the reason in `reasons` and `ready: false`, the two
  recorded ones pass, and the off switch does not change either answer.
- `git pair hooks --decline` human and JSON, and `--trust` with `--decline` as a usage error (exit 2).
- The tracked root at the integrate point, which is the one place a hook is ordinary reviewed content rather than a
  fixture: the repository shipping the suite while the change is in review is the unrecorded case the early
  refusals cannot catch, and the gate is what meets it; declined lets the declaration through with the hook skipped.
- `TestTheCommandsThatStartWorkAskForAPosition`: `init` refuses without writing a directory or a commit, the
  hand-off refuses a moved script as well as an unseen one, a refused `review submit` stages nothing and leaves the
  reviewer's own edit untouched, and `change unready`/`change abandon` still work with nothing recorded.

## Spikes / research

| Question                                                                         | Status                         | Conclusion                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| -------------------------------------------------------------------------------- | ------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Could git's own `pre-commit`/`commit-msg` be the mechanism instead?              | Resolved                       | No, and the cost is measurable. They cover the five commit points and nothing at `check`, `pre-commit` receives no arguments so it cannot tell a marker from an ordinary commit, tracked hooks need `core.hooksPath` (one flat directory, not cloned), and a native refusal gives exit 3 with empty `--json` instead of exit 1. git-pair keeps its own runner and exports context to native hooks regardless. Full measurements: [research/2026-10-13-native-git-hooks.md](research/2026-10-13-native-git-hooks.md).                                                                                                                                                                                                                                                                                                                                     |
| Does a manifest format earn a config dependency?                                 | Open                           | Default is no: discovery by filesystem, attributes by git config. Revisit only if per-hook timeouts or per-hook blocking are actually wanted.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| Do post-hooks need a per-point opt-in to blocking?                               | Open                           | Deferred. Under §26 a blocking post-hook cannot reverse anything, so the only difference is exit code, and the plan already reports nonzero.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| Is there a safe form of reviewer-side hooks?                                     | Resolved                       | Yes, and they are in scope as of 2026-10-13. Hooks are client control, so the reviewer owns their own hooks and what they enforce. The safety question moves from "may a hook gate a verdict" to "what may a hook write into the record", and the answer there is: client-appended lines yes, anything the gate reads no — see _What the record carries, and what it does not_.                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| Should a hook result be recorded in the review marker?                           | Resolved                       | Split, after review feedback. A key git-pair reads: still no. Lines the client appends: yes — `Message` gains a field rendered after `Trailers` in the same paragraph, with reserved keys rejected and a client-owned prefix. Measured: appending at the end of the block cannot forge (first-wins protects the real keys), while appending prose below the block silently evicts `Review-State` and collapses the changeset to `WORKING`. See [research/2026-10-13-appending-to-the-marker-message.md](research/2026-10-13-appending-to-the-marker-message.md).                                                                                                                                                                                                                                                                                         |
| Should a repository with committed hooks refuse outright in an unaccepted clone? | **Adopted, implemented in M6** | Mandatory, with a `declined` state as the way out: the tracked tree is recorded per-clone beside `trusted-hooks` and keyed by the tree hash, and the commands that start or advance work refuse while it is `unrecorded` or `changed` — `init`, `change ready`, `review submit`, and the gate behind `check` and `change integrate` — while `change unready`, `change abandon` and `git pair hooks` stay free. The consequence the reviewer took knowingly: a CI checkout is a clone that has recorded nothing, so a job takes a position in its own script (`git pair hooks --trust`, or `--decline` where a team has genuinely chosen that), and `GIT_PAIR_HOOKS=0` is deliberately not a substitute — it says a machine does not want to run scripts, not what the scripts do. Nothing breaks on upgrade because no repository has tracked hooks yet. |
| Does `--no-cache` interact with hook state?                                      | Open                           | Expected no: hooks run once per command and nothing about them is memoised. Confirm no fact-cache key covers them.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |

## Risks

| Risk                                                                                                                                                                 | Mitigation                                                                                                                                                                                                                                           |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| PRD §26's wording is a product decision, not a code change, and a hook that pushes makes the current absolute sentence read wrongly if its subject is left unstated. | Do it in M5 task 1, agreed before anything merges. The change names who the rule binds rather than excepting anything, so a refusal costs only the freedom to run git verbs from a hook — the rest of the feature stands.                            |
| A hook that commits or rewrites invalidates the `Review-Head`/`Review-Diff-Id` a marker just recorded.                                                               | Detect and report the HEAD move. Document that hooks which rewrite history invalidate approvals by design, since that is git-pair's rebase rule anyway.                                                                                              |
| A `pre-submit` hook mutates the index after inspecting it, so the review commit records content the hook never checked.                                              | Compare the staged tree (`git write-tree`) before and after the hook and refuse on a change, naming the hook — the same shape as HEAD-move detection.                                                                                                |
| A reviewer's own broken hook makes their verdict unreachable, with no way to record a review in a repository they cannot fix.                                        | `review submit --allow-hook-failure`, reported as `overridden` in the `hooks` array. `GIT_PAIR_HOOKS=0` for a hook that hangs.                                                                                                                       |
| A per-clone hook is invisible to teammates, so an approval that a local hook gated reads as unconditional.                                                           | Client-appended lines are visible to everyone who reads the commit, and the gate reads none of them (see _What the record carries, and what it does not_); the skill says plainly that the clone root is local discipline rather than shared policy. |
| Hooks make `change ready` slow in an agent loop, and a hang blocks it forever.                                                                                       | Context deadline from `git-pair.hookTimeout`, plus `GIT_PAIR_HOOKS=0` and `git-pair.hooks=false`.                                                                                                                                                    |
| The trust step becomes friction and people blanket-approve without reading.                                                                                          | `hooks --trust` prints the tree it is approving; the hash covers content, so an edit re-prompts. The clone root needs no consent step precisely because it is the person's own code.                                                                 |
| A hook corrupts the clean-tree precondition of `change ready`.                                                                                                       | Clean-tree check runs before and after pre-hooks; the second one refuses and names the hook.                                                                                                                                                         |
| `check` hooks run repository scripts in CI, where the toolchain differs from the author's.                                                                           | Documented: the gate runs what the repository configured, on whatever the runner has. Not determinism across clones, and the prose says so.                                                                                                          |
| Someone reads hooks as git-pair performing the push, and the distinction in §26 is lost.                                                                             | The restated §26 names its subject; `hooks` output names the script, not git-pair, as the thing that ran, and `GIT_PAIR_HOOK_ROOT` says which root it came from.                                                                                     |

## Verification strategy

Per milestone, `go test ./...` plus the targeted gates: `./internal/hooks/`, `./internal/cli/`
(with `-run Docs` and `TestTheCommandTreeIsTheCommandSet`), and `./internal/hygiene/`.

CLI tests use `gittest.New(t)` and drive the real command tree, so they cover wiring rather than a
helper in isolation. The table that matters is per-point: pass, fail, missing exec bit, timeout,
untrusted (tracked root only), disabled by env, overridden by flag, dirty-tree-after-hook, and index
changed by a `pre-submit` hook.

Two properties need whole-surface tests rather than unit tests, because they are contract properties and
not function behaviour: hook output never reaches stdout in `--json` mode (assert by parsing the captured
stdout as JSON in every hook test), and `check` hooks run exactly once per `check` and never from
`change integrate` (assert by counting subprocesses, the `gittest.SpawnShim` technique). A third belongs
to M4: a `pre-submit` hook sees the reviewer's uncommitted edit, which is the property the reviewer-facing
use case rests on and the one a refactor of `reviewops.Submit` could silently break.

Manual verification per milestone, in a fixture repository, for the paths a person actually reads: the
refusal message, the trust prompt, the post-hook failure line, the CI recipe from README, and one full
reviewer run — edit in the review screen, have the hook refuse, fix, submit.

## Audit history

| Date | Audit | Summary          |
| ---- | ----- | ---------------- |
| —    | —     | Not yet audited. |

## Execution status

M1 through M4 are done, each on its own branch, each stacked on the one below. The reviewer points and the
per-clone root were added on 2026-10-13 after the first draft deferred them, and both landed in M4. M5 is the
last milestone: the PRD §26 sentence, the PRD §27 contract summary, the skill's hook reference, and the hygiene
fixture. M5 task 1 (the §26 wording) is the gate on the rest.

**M1 — done** on `feat/hooks-runner`, stacked on the approved plan branch.

What landed: `internal/hooks` (discovery over both roots, trust by content hash, argv-exact execution with a
deadline), `app.runHooks` as the single call site called from `runChangeReady` before `marker.Commit`, the
post-hook clean-tree re-check, `git pair hooks` with `--trust`, and the `hooks` array in `change ready
--json`. PRD §11.4, the README command table and the skill's `cli.md` all name the command, which is what
`TestEveryCommandIsNamedInTheDocs` and `TestTheCommandTreeIsTheCommandSet` hold.

Two deviations from the plan, both deliberate:

- **`git pair hooks` reports configuration, not the last result.** The plan's reporting section asks for
  "the last result of each". There is no results store, and building one in M1 would have meant inventing
  the persistence that the plan's own "what the record carries, and what it does not" section leaves open.
  The command therefore answers what is configured, what is executable, and whether this build invokes the
  point at all — which is the question a person asks first, and the one that makes a misspelled point
  visible. A results store stays open.
- **`GIT_PAIR_HOOK_OUTPUT` instead of a `-v` flag.** A flag on `change ready` would put an option on the
  author's command for a diagnostic about someone else's script, and `change ready` already refuses to grow
  flags the gate does not need. The variable is read once, on the CLI side.

Task 3 (the resolution onto the marker commit) landed as `git.Repo.CommitWithEnv` and
`marker.CommitWithEnv`, reached through `runEnv` rather than a second spawn path so `run` stays the one place
the memo is consulted. A test installs git's own `pre-commit` and asserts it reads `GIT_PAIR_CHANGESET`.

**M2 — done** on `feat/hooks-author-points`, stacked on M1.

What landed: `runHooks` split into `runPre`/`runPost` over a shared `runPoint` with an explicit `runMode`,
`hooks.RunAll` beside `hooks.Run`, both sides of the four author transitions wired, the HEAD-move check, and
the `hooks` array in the `unready`, `abandon` and `integrate` JSON contracts.

Three things the plan did not predict, each of which changed the shape of the code:

- **A tracked hook is content the approval has to cover, which gives the install a place in the sequence.** A
  tracked hook is a commit, and the declaration gate compares what the reviewer saw with what the branch now
  carries. Installed after the approval, the install is the thing it refuses —
  `content outside changesets/checkout-flow/ changed since <approval>: .git-pair/hooks/pre-integrate/01-suite`
  — which is a statement about the install, not about the point under test. Installed before the approval the
  hook is ordinary reviewed content, consent is recorded against the tree, and the point fires from the
  tracked root like any other; `TestTrackedRootAtTheIntegratePoint` runs it three ways since M6 — unrecorded (the
  declaration refuses, no marker, both position commands named), declined (the declaration proceeds and the hook
  stays skipped) and accepted (blocking the declaration with no marker written). The table of
  author points still installs at the per-clone root, so the four points read identically and none of them
  depends on a consent step that belongs to the root rather than the point. Trust is a property of the root,
  not of the point.
- **A post-hook failure needs its own message, not just a nonzero exit.** The plan asks for a nonzero exit
  and a line that says what happened; writing it as `postHookError` made the second half concrete — the
  state the changeset is in, the hook, and "nothing was undone". M1's PRD paragraph said a post-hook failure
  "does not change the exit code", which contradicts this section of the plan; the paragraph now matches the
  plan.
- **The HEAD-move warning cannot name the hook that committed.** A hook is a child process that can run any
  git command and git-pair does not watch the index between them, so the warning names the hooks that ran
  and the marker they invalidated, which is the honest version of the same information.

Two things the plan left open, decided here: a command that writes nothing runs no hooks — `unready` on a
changeset never offered, `abandon` on one already abandoned, `integrate` on a head already declared — because
a transition that did not happen is not an event; and post-hooks do not stop at the first failure, because
there is nothing left to prevent.

**M3 — done** on `feat/hooks-check-gate`, stacked on M2.

What landed: `pre-check` running before the gate and `post-check` after the verdict is built, both
contributing reasons so `ready` stays the single answer; the `hooks` array beside `reasons` in `check --json`;
the two gate points activated; and a test that `change integrate` runs neither, so the repository's suite
starts once per author action rather than twice.

Three things worth recording:

- **`check --json` exits 0 when the gate refuses.** The verdict travels in `ready`, and `check_test.go:307`
  asserts that deliberately. The milestone's first draft asserted exit 1 in JSON mode and was wrong. The
  tests now assert both forms — the JSON for `ready: false`, the human form for exit 1 and `NOT READY:` —
  because the two carry the answer differently and a reader uses one of them at a time.
- **A hook that spawned a background process held the command open past its deadline.** `exec.Cmd.Wait`
  waits for the captured pipes to close, and the write ends belong to whatever the hook left running, so a
  `sleep 30` behind a one-second deadline cost thirty seconds. Fixed with `cmd.WaitDelay`: five seconds of
  grace for output genuinely still arriving, then the pipes close and the verdict is reported. The gate's
  timeout case went from 30s to 6s, and M1's equivalent case had been paying the same 30s unnoticed.
- **A post-hook joins the verdict at the gate, and nowhere else.** At a marker a post-hook cannot reverse
  the transition and does not try; at `check` there is nothing to reverse, so its failure is a condition
  like any other. The invariant kept in both places is that the exit code and `ready` never answer different
  questions.

The CI example in the README was exercised against a fixture repository rather than written from the shape of
the JSON: the hook, the `--trust`, the refusal, the reason wording, and the `hooks` entry are the output that
run produced.

**M4 — done** on `feat/hooks-reviewer-submit`, stacked on M3.

What landed: `pre-submit` and `post-submit` at the one path that makes a submission, run after staging and
before the review commit exists; the reviewer environment (`GIT_PAIR_REVIEW_OUTCOME`, `GIT_PAIR_REVIEW_HEAD`,
`GIT_PAIR_REVIEW_BODY_FILE`); `review submit --allow-hook-failure` with `overridden` in the JSON; the
`hooks` array in `review submit --json`; and the point vocabulary complete, which removed the `active` flag
from `git pair hooks` (see below).

Three things worth recording:

- **The review screen had a second submission path, and task 1 was written without knowing it.** The plan
  says to keep `reviewops.Submit` free of hook calls "so the review screen's own submit path stays covered
  by one call site" — but the screen called `reviewops.Submit` directly, so attaching hooks to the CLI's
  call site would have left the surface reviewers actually use hook-free. Fixed by injecting the submission
  into the screen: `tui.Options.Submit` is a function the CLI supplies (`a.submitReview`), and the screen has
  no other way to submit. A hook now fires wherever a verdict is recorded. The screen opened without a
  submission path says so on the status line rather than inventing its own.
- **`active` was removed rather than flipped to always-true.** M1 added it so a hook under a point nobody
  ran stayed visible while the milestones wired the rest. With the last two points wired, every declared
  point is invoked, so the field could never again be false — and a JSON field that can never be false is
  data that misleads. `AllPoints()` still carries the vocabulary, `unknown[]` still catches a misspelled
  directory, and the CLI tests run every point end to end, which is the stronger claim.
- **The reviewer's index is the hook's input, and a refusal has to say where the content is.** Two tests
  caught the shape of this by being wrong first: a fixture whose "reviewer edit" was byte-identical to what
  the commit already held staged nothing, and the hook correctly saw nothing. The index is real input here,
  so the refusal names it — the review was _not recorded_ and the content is _still staged_ — because that
  state has no other symptom: the next plain `git commit` would land the reviewer's source edits as ordinary
  work with no verdict attached.

The open spike the plan carried into this milestone — whether a hook result belongs in the review marker — is
closed as no. The review commit carries what the reviewer did (the outcome, the body, what was reviewed and
where it was measured), and a hook result is a fact about a machine, produced by a script that machine's owner
can edit or delete. Recording it would make a verdict look like it was verified somewhere the reader has no
reason to trust, and it would be unverifiable by anyone reading the review later. The `hooks` array in
`review submit --json` is where the result lives: with the answer that produced it, not with the artifact.

The per-clone root needed no new code: M1 discovered both roots and M2's `runPre`/`runPost` reach them for
every point, so the reviewer's own hooks came with the wiring. What was reviewer-specific was the trust rule,
which is asserted rather than assumed: a tracked hook from the repository under review stays skipped until
accepted, and the same command runs it after `git pair hooks --trust`.

**M5 — done** on `feat/hooks-spec`, stacked on M4.

**M6 — the unaccepted-hooks decision** on `feat/hooks-declined-state`, stacked on M5 (both edit the hook contract
and the skill page, and the reviewer's question arrived while reading M5).

The reviewer's two follow-up choices are the reason the shape is what it is: the refusal is per-clone and
content-keyed rather than tracked, and the decline is taken with `git pair hooks --decline` beside `--trust`. What
that leaves, and what the code settles: `accepted` and `declined` clear the verdict and the two silent states do
not, the two records clear each other so a position about one version cannot answer for another, and a decline
whose hash no longer matches stays reportable — "declined 3f1a…, the tree is now 82b0…" is a different situation
from nobody having looked, and it is the one a team needs to see before it re-runs a script it had said no to.

The gate is `integrationGate`, not a check-only bolt, because `change integrate` asks the same question and a
declaration that reads "ready" over unrun checks is the failure being closed. `hooks.Resolution` memoizes the trust
read so a run that asks the state for the gate and again for each hook point asks once.

`review dc0956c` (feedback) moved the ask earlier: "it should be obvious as early as possible that there are hooks
that repo owner wants run", naming init/ready/review. The helper left `check.go` for `hooks.go` and the three
commands call it before they write or stage anything. Two exceptions were kept and documented rather than argued
away: `change unready` and `change abandon` stay free, because the person who cannot run the checks has to be able
to put the work down; and `git pair hooks` stays ungated, because a command that refuses until you have decided
would make the reading that is supposed to precede the decision impossible. The fixtures that reached a hand-off
with no position now record one first — which is the change working, not a test being accommodated — and the
integrate-point's unrecorded case became the one the early refusals cannot catch: hooks shipped while the change is
already in review.

What landed: the §26 scope sentence, the §27 entry that says which half of the deferred pre-push work this
closes, `skills/git-pair/references/hooks.md`, a Concepts entry in the README, and hook shapes added to the
hygiene self-test fixture.

**The §26 sentence names the rule's subject; it grants nothing.** Drafted first, as task 1 required, and
agreed before the rest of the milestone was written. It says the rule binds what git-pair invokes of its own
choosing, and that a hook is the client's own code under the client's authority — so the hygiene test binding
shipped code is the subject the rule always had, not an exception added for the feature. The sentence sits
after "There is no carve-out" rather than qualifying the bullet list, so the strong sentence stays whole and
its scope is stated next to it. §11.4 already carried the same claim in one line; §26 is where it belongs.

**§27 names which half closes.** The deferred list said "pre-push hooks". What this feature closes is the
client's half — a person can attach their own script to a lifecycle event, which is where a push check belongs
if a team wants one. What stays deferred is git-pair owning or running one, and everything server-side. Left as
one entry rather than split, so the list still reads as the shape of the product.

**The skill page carries the agent-facing rules the command reference does not.** Two of them are new to the
skill and are the reason the page exists beside `cli.md`: `git pair hooks --trust` is a person's consent act,
so an agent reports the unaccepted state and names the command rather than running it; and `GIT_PAIR_HOOKS=0`
is the machine owner's escape hatch, not a way for an agent to get past a check. The page also carries the
per-clone caveat plainly — a check that lives under `<git-dir>/` runs on one machine, so a team rule is the
committed one.

**The hygiene fixture asserts both directions for the hook runner.** The product scan already covers
`internal/hooks` (it walks `cmd` and `internal`), so the runner invoking no forbidden verb is a fact about the
shipped tree. What the fixture adds is that the scan is not vacuous _and_ not over-eager on the feature's
shapes: a hook run by path, environment and root-name literals, and a config key that names the tool are all
marked OK, and a planted `exec.Command("git", "push", "origin")` in a hook-shaped function is marked BAD. Without
the OK cases the feature's own code would be the thing that eventually gets a rule relaxed.

**M5's review round left one point as a decision, and it is recorded rather than answered.** The reviewer asked
whether the unaccepted state should be mandatory and suggested an explicit `declined` state as the escape hatch
(`review 1af9b87`). The reviewer chose mandatory with a declined escape, and M6 below is the implementation:
the store is per-clone and keyed by the tree hash, and the mandate lives in the verdict rather than in reporting.
M5 changed its own open question and this plan; the PRD M5 shipped described the behaviour the code had at the
time, which was the rule M5 was cut to write down, and M6 updates that contract alongside the code. The spike the
plan carried as open is therefore closed by M6 rather than left standing.

Landed in the integration branch as of 2026-10-14: M1 at `9abcfc7`, M2 at `c77633e`, M3 at `29f49e8`, M4 at
`6768da6`, M5 at `984f797`, M6 at `d4c8853` (approved `d9ed281`, declared `a697b3a`). Six milestones, six
changesets, every one reviewed before it landed.

## Closure

The plan is complete: six milestones, each its own changeset, each reviewed before it landed.

What shipped:

- A hook runner that executes the client's script directly — never through a shell — with the repository root as
  its working directory, the resolution the command already made in the environment, and output that cannot reach
  `--json` (`internal/hooks`).
- Twelve points, the six actions a changeset meets on both sides, including the reviewer's `pre-submit` and
  `post-submit`, with the read-path commands deliberately running none: a hook on a read turns a lookup into a
  build.
- Consent by content for the tracked root — path, mode and bytes hashed, recorded per clone — four states, and a
  per-clone record for each position a person can take.
- The repository's own conditions inside the verdict: a failing pre-hook refuses with exit 1 like a dirty tree and
  writes nothing, a failing post-hook reports without reversing what it followed, and `check` keeps one answer for
  both because it writes nothing to undo.
- The position a clone owes about the repository's scripts, asked where work starts (`init`, the hand-off, the
  reviewer's submission) and again at the gate that licenses the merge.

What the plan left undone, from its non-goals and the milestones' open questions:

- No bridge from the native git hooks, so a clone's own `pre-commit` still knows nothing about the resolution.
- No shared, reviewed statement that a repository's hooks are declined. A decline is this clone's answer; a team
  that wants one on record writes it into its own CI, and a tracked file for it would be inherited by the next
  clone, which is the thing it must not become.
- `status` and `queue` report the state only through `git pair hooks`. They do not mark a row for a clone that has
  recorded nothing, which is a surface a person can read but cannot fix from.
- `--decline` records that the list was shown, not that it was read. An optional note (`--reason`) would make a
  position reviewable without making it tracked.

The number to beat if the feature grows: the stack-cost gate measures 49 git invocations at two levels against the
50 allowed, and the trust read is a filesystem walk through the run's memoized `Resolution`. A change that turns
that read into a git question has to say so in the gate's paragraph.
