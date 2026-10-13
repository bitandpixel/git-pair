# feat-hooks-runner

## Summary

A repository can commit a script under `.git-pair/hooks/pre-ready/` and have it run before the ready marker
is written, refusing the handoff when the script exits nonzero.

The built-in readiness checks can only ask what git already knows: a clean tree, an `ABOUT.md` that exists, a
standing approval. Whether the suite passed, the migration applies, or the generated client is current is not
in the graph, and so the first moment a changeset's content was ever verified used to be CI — after the work
had been handed to a reviewer. This is the seam for the checks a tool cannot know about, and it is the first
milestone of the approved lifecycle-hooks plan (`docs/plans/lifecycle-hooks/plan.md`).

## What changed

-   `internal/hooks/` — the new package. `Discover` finds the hooks configured for a point across two roots;
    `Run` executes them argv-exact with a deadline and captured output; `Trusted`/`Accept` are the trust store.
-   `internal/cli/hooks.go` — `app.runHooks`, the single call site every command reaches its point through,
    plus `git pair hooks` and `git pair hooks --trust`.
-   `internal/cli/change.go` — `runChangeReady` calls the `pre-ready` point after the built-in checks and
    before `marker.Commit`, and re-checks the tree afterwards.
-   `internal/git/git.go`, `internal/marker/marker.go` — `CommitWithEnv`, so the resolution reaches the
    marker commit's environment and therefore git's own hooks.
-   `PRD.md` §11.4, the `README.md` command table, and `skills/git-pair/references/cli.md` name the command
    and the point; `PRD.md`'s command tree gained `hooks`.
-   Tests: `internal/hooks/hooks_test.go` for the discovery and environment rules, `internal/cli/hooks_test.go`
    for the behaviour of the point end to end.

## Design decisions

**Hooks are client control, not repository enforcement.** git-pair publishes an event and the client owns the
script, the same relationship as git's own hooks. This is what makes it acceptable for a hook to run build
commands that git-pair itself never invokes: the clause in PRD §26 binds what this tool runs of its own
choosing, not what a person installs in their own checkout. It follows that a hook is not a control against
the person who wrote it — they can delete it. What it guarantees is that nobody hands work on over a failing
check.

**Two roots, consulted tracked-then-clone.** `.git-pair/hooks/<point>/` is committed and shared;
`<git-dir>/git-pair/hooks/<point>/` is the person's own file under their own git directory. The repository's
rules are the baseline and the client adds to them. The per-clone root needs no consent step, because
consenting to your own file is not consent.

**The tracked tree is trusted by content, not by a flag.** A tracked file is code that runs on a clone, which
is why git keeps its hooks under `.git/` and refuses to clone them. The path, mode and bytes of the tracked
tree are hashed and recorded in `<git-dir>/git-pair/trusted-hooks`, so an edit after acceptance is a new
acceptance. A flag would approve the directory forever. An unaccepted tree does not run at all, and the run
says so on stderr rather than either surprising the clone or silently skipping the team's checks.

**A hook refusal is exit 1, never 3.** This is the reason the runner is git-pair's own rather than git's
`pre-commit`. Measured in the plan's research note: a native `pre-commit` that exits 1 makes `change ready`
report `creating ready marker: git exited with status 1` and exit **3**, which tells an agent that git broke
and sends it looking for a broken git instead of a failing suite. Exit 3 is the contract for a git failure and
a repository refusing is not one.

**The context is handed over, not derived.** A hook receives `GIT_PAIR_CHANGESET`, `_BRANCH`, `_BASE`,
`_HEAD`, `_HOOK_ROOT` and the recursion guard `GIT_PAIR_HOOKS`. The rules for "which changeset is this" are
in this binary; a script that re-implemented them would get a second, different answer. The same values are
put on the marker commit's environment, because git gives `pre-commit` no arguments and the commit's
environment is the only thing it inherits — so a repository already driven by git's own hooks gets the context
without re-deriving it.

**Output stays off stdout, where `--json` lives.** A failing hook's captured output is replayed on stderr with
the refusal, because it is the text the author needs next. A passing hook's is replayed only when
`GIT_PAIR_HOOK_OUTPUT` is set — a suite that prints four hundred lines would make every hand-off unreadable.
The test for this is that stdout still parses as JSON.

**A hook that dirties the tree is refused.** The marker is written only over a clean tree, so files a hook
generated and nobody staged would otherwise travel into the span a reviewer is about to read. Committing them
on the author's behalf is not something git-pair does.

**Execution is argv-exact.** The file is execved as configured with the repository root as cwd and no stdin;
any shell a script wants is its own shebang. `internal/console` carries the cautionary tale for the
alternative: its editor path expands a user-supplied value through `eval`, and its own comment records how a
reviewed path containing `$`, a backtick, or a space is read as shell.

**`runEnv` rather than a second spawn path.** `run` is the one place every git subprocess is spawned and the
one place the memo is consulted. `CommitWithEnv` threads an environment overlay through it instead of
reaching around it, so the classification and the memo stay in one place.

**`GIT_PAIR_HOOK_TIMEOUT` rather than a flag, and no force flag for a timeout.** A flag on `change ready`
would put an option on the author's command for a diagnostic about someone else's script. And waiting ten
minutes and then running the operation anyway is how a hung check becomes a habit, so there is nothing to
override.

## Validation

-   `go build ./...` clean; `gofmt -l internal cmd` empty.
-   `go test ./internal/hooks/ ./internal/cli/ ./internal/git/` green. The CLI file covers: a passing hook
    writes the marker and is reported; a failing hook refuses with exit 1, writes no commit, and its output
    reaches the author; the missing executable bit, the deadline, and the dirty-tree case each refuse with
    their own reason; an unaccepted tracked hook does not run and the run says why; `--trust` accepts and
    prints what it approved; editing a hook invalidates the acceptance; the per-clone hook needs no
    acceptance; tracked-then-clone ordering and stop-at-first-failure ordering; both off switches; the
    environment a hook receives; git's own `pre-commit` reading the resolution; and `git pair hooks`
    reporting configuration, including a misspelled point and a hook under a point this build does not run.
-   `go test ./internal/cli/ -run 'Docs|CommandTree'` green, which is what forced the new name into all three
    documents rather than one.
-   `go test ./internal/hygiene/` green — the new package introduces no literal for the git verbs git-pair
    itself must not invoke, and no read-path command gained a hook.
-   `mise run check` exited 0 on the committed tree (`f96271e`): 35 test shards ok, 27 of them served from
    cache, no `FAIL`, and the gate's own `gofmt -l internal cmd` step reported nothing. Run from the
    repository root, which is the task that runs build, gofmt, vet and every package's tests including the
    sharded `internal/cli` and `internal/tui` ones.

## Known limitations

-   Only `pre-ready` is wired. The runner serves one point at a time, and `git pair hooks` reports a hook
    under any other point as `active: false` rather than pretending to run it. The remaining points arrive
    with the milestones that run them.
-   There is no results store, so `git pair hooks` reports configuration rather than the last result of each
    hook. The plan leaves the record's contents open and this milestone did not need it; adding it is still
    open.
-   `git-pair.hookTimeout` accepts a plain number of seconds, the same shape as `core.hooksPath`-adjacent
    git config values elsewhere in this tool; it is not git's `10s`/`5m` duration grammar.
-   A tracked hook that has never been accepted does not refuse the operation — the operation proceeds and
    names the skipped hooks on stderr. That is deliberate, so a fresh clone is not blocked by a file nobody
    has read yet, and it is the same asymmetry `--configure-fetch` uses for consent. It does mean a repository
    cannot rely on a hook being enforced in a clone that has not accepted it, which is the honest reading of
    "client control".
-   Hooks are not part of `check`. That is M3, and it lands only on the command's entry point: `change
    integrate` runs `check`'s gate as the same code, and hooks firing there would double-run a suite inside
    one author action.

## Open questions

-   M5 task 1 rewords PRD §26 to say that its list binds what git-pair invokes of its own choosing. This
    milestone depends on that reading, and the wording is the reviewer's to sign off.
-   Whether a hook result belongs in the review marker is the open spike before M4. Nothing here writes hook
    state into any marker.
-   Whether an untrusted tracked tree should refuse outright once a repository has committed hooks — today it
    proceeds with a note. The current answer favours the fresh clone; a team that wants enforcement will want
    the other one.
