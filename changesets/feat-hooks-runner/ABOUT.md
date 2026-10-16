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

- `internal/hooks/` — the new package. `Discover` finds the hooks configured for a point across two roots;
  `Run` executes them argv-exact with a deadline and captured output; `Trusted`/`Accept`/`Trust` are the
  trust store, where `Trust` reports the clone's state — accepted, changed, or unrecorded — beside both
  hashes.
- `internal/cli/hooks.go` — `app.runHooks`, the single call site every command reaches its point through,
  plus `git pair hooks` and `git pair hooks --trust`.
- `internal/cli/change.go` — `runChangeReady` calls the `pre-ready` point after the built-in checks and
  before `marker.Commit`, and re-checks the tree afterwards.
- `internal/git/git.go`, `internal/marker/marker.go` — `CommitWithEnv`, so the resolution reaches the
  marker commit's environment and therefore git's own hooks.
- `PRD.md` §11.4, the `README.md` command table, and `skills/git-pair/references/cli.md` name the command
  and the point; `PRD.md`'s command tree gained `hooks`.
- Tests: `internal/hooks/hooks_test.go` for the discovery and environment rules, `internal/cli/hooks_test.go`
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

- `go build ./...` clean; `gofmt -l internal cmd` empty.
- `go test ./internal/hooks/ ./internal/cli/ ./internal/git/` green. The CLI file covers: a passing hook
  writes the marker and is reported; a failing hook refuses with exit 1, writes no commit, and its output
  reaches the author; the missing executable bit, the deadline, and the dirty-tree case each refuse with
  their own reason; an unaccepted tracked hook does not run and the run says why; `--trust` accepts and
  prints what it approved; editing a hook invalidates the acceptance; the per-clone hook needs no
  acceptance; tracked-then-clone ordering and stop-at-first-failure ordering; both off switches; the
  environment a hook receives; git's own `pre-commit` reading the resolution; and `git pair hooks`
  reporting configuration, including a misspelled point and a hook under a point this build does not run.
- `go test ./internal/cli/ -run 'Docs|CommandTree'` green, which is what forced the new name into all three
  documents rather than one.
- `go test ./internal/hygiene/` green — the new package introduces no literal for the git verbs git-pair
  itself must not invoke, and no read-path command gained a hook.
- `Trust` is the new answer and `Trusted` is now expressed through it, so one comparison decides and the
  report cannot disagree with the runner. The cases: no tracked tree is accepted by vacuity with neither hash
  set; a tree nobody accepted is `unrecorded` with only the tree hash; after `Accept` both hashes equal the
  approved value; after a rewrite it is `changed`, the recorded hash still the approved one, and `Trusted`
  false.
- End to end in the CLI: a clone that never accepted gets the plain `NOT trusted` sentence and no `accepted`
  field; a clone the repository moved under gets `CHANGED since you accepted them` with both hashes, on
  stderr, and `trust_state: "changed"` with `tree_hash` in the JSON. The two hashes are asserted distinct, so
  the report cannot print one value twice and call it a comparison.
- Checked against the built binary rather than only the tests: a fixture repository at the three states —
  never accepted, `hooks --trust`, then a committed rewrite of the hook — printed the two sentences and the
  hash pair this changeset claims.
- `mise run check` exited 0 on the committed tree (`f96271e`): 35 test shards ok, 27 of them served from
  cache, no `FAIL`, and the gate's own `gofmt -l internal cmd` step reported nothing. Run from the
  repository root, which is the task that runs build, gofmt, vet and every package's tests including the
  sharded `internal/cli` and `internal/tui` ones.
- After the review feedback, `mise run check` exited 0 again on `1f06e27`: 36 test shards ok, 31 served from
  cache, no `FAIL`, `gofmt` silent. The change since the run above is documentation only — two answered
  reviewer questions and the paragraph they exposed as missing — so no test was added and none of the
  existing ones changed.

## Known limitations

- Only `pre-ready` is wired. The runner serves one point at a time, and `git pair hooks` reports a hook
  under any other point as `active: false` rather than pretending to run it. The remaining points arrive
  with the milestones that run them.
- There is no results store, so `git pair hooks` reports configuration rather than the last result of each
  hook. The plan leaves the record's contents open and this milestone did not need it; adding it is still
  open.
- `git-pair.hookTimeout` accepts a plain number of seconds, the same shape as `core.hooksPath`-adjacent
  git config values elsewhere in this tool; it is not git's `10s`/`5m` duration grammar.
- A tracked hook that has never been accepted does not refuse the operation — the operation proceeds and
  names the skipped hooks on stderr. That is deliberate, so a fresh clone is not blocked by a file nobody
  has read yet, and it is the same asymmetry `--configure-fetch` uses for consent. It does mean a repository
  cannot rely on a hook being enforced in a clone that has not accepted it, which is the honest reading of
  "client control".
- Hooks are not part of `check`. That is M3, and it lands only on the command's entry point: `change
integrate` runs `check`'s gate as the same code, and hooks firing there would double-run a suite inside
  one author action.
- **Acceptance answers three states, because two failures wear one sentence.** `trusted: false` covered both
  a clone where nobody has run `--trust` and a clone whose accepted hash the repository moved under by
  shipping a new version of the check. The second needs its own words: it is the moment a check starts not
  running for everybody who pulled, and from the clone it looks identical to the first. `git pair hooks`
  now reports `trust_state` (`accepted`, `changed`, `unrecorded`) and, when the two differ, both the
  accepted hash and the tree's current one — the values `--trust` printed when the person approved, so the
  comparison is against something they saw. A state rather than a second boolean, because the reader's next
  action differs: one is "read these", the other is "read what moved, then decide". The warning goes to
  stderr beside the note a run makes when it skips the same hooks; the table stays the answer on stdout.

## Open questions

- M5 task 1 rewords PRD §26 to say that its list binds what git-pair invokes of its own choosing. This
  milestone depends on that reading, and the wording is the reviewer's to sign off.
- Whether a hook result belongs in the review marker is the open spike before M4. Nothing here writes hook
  state into any marker.
- Whether an untrusted tracked tree should refuse outright once a repository has committed hooks — today it
  proceeds with a note. The current answer favours the fresh clone; a team that wants enforcement will want
  the other one.
- Whether the `changed` state should be louder than `unrecorded` at run time. Today `change ready` says the
  same sentence in both cases and `git pair hooks` is the command that explains which; if the enforcement
  question above resolves toward refusing, the run-time note has to carry the distinction too, because "you
  are working without the check the team shipped yesterday" is not a fact to relegate to a second command.
- Whether keeping the version you read deserves a command. It works today by copying the old content into
  the per-clone root, which is git's model applied by hand (`git show <commit>:.git-pair/hooks/…` and a
  `chmod +x`). A first-class `hooks --pin` would be a real convenience, and it is the piece this milestone
  deliberately did not add: it needs a decision about what happens when a pinned copy and a later-accepted
  tracked tree both match a point, and that decision belongs with the enforcement question above.

## Addressed feedback

### "for multiple hooks in the same lifecycle dir, do they run in parallel by default? or sequentially?"

Sequentially, one at a time, in lexicographic order by name: `01-fmt`, then `02-test`, then `03-lint`.
`hooks.Run` walks the collected list in order and returns as soon as one hook blocks, so a suite that fails at
minute one is not followed by the next suite. Post-hooks are the exception to the stopping rule and not to the
ordering — `hooks.RunAll` runs all of them whatever the outcomes, because once the marker is written there is
nothing left to prevent.

Order is why the naming convention exists, and it has no meaning if the scripts overlap. Two suites racing on
one working tree, one build cache, or one scratch database produce a failure neither author can reproduce, and
the replayed output would arrive in whatever order the scheduler chose. There is no flag for parallel
execution: a repository that wants two suites to overlap can run them itself from one script, where it can
also say what the shared state is.

### "was it considered to have all the hooks live in the \<git-dir\> location, and trust is established by copying vendored hooks there?"

Yes — that is git's own model, where `.git/hooks` is not cloned and a team vendors a script into each checkout.
It buys a single execution surface, and it costs a second copy of the check. The copy drifts from the source
the moment somebody edits it, so the tool then needs a drift report saying which of the two bytes are running.
This repository has that shape once already: `git pair skill list` answers `current`, `stale`, `absent` and
`unavailable` by comparing bytes, and it is a command whose entire job is the comparison a vendoring model
would need for hooks.

The tracked root avoids the second copy by making consent the step rather than installation. The recorded hash
covers the tracked tree's path, mode and bytes, per clone, in `<git-dir>/git-pair/trusted-hooks` — so what a
person accepts is the content they read, an edit is a new acceptance, and the file that executes is the one a
reviewer saw. It also keeps a check and the code it checks in one reviewable commit: the change that alters a
schema carries the migration check beside it, and the check's version is the version of the code it inspects,
which a copy under `<git-dir>` cannot claim by itself.

The per-clone root is the git-dir-only model, and it is where that model belongs:
`<git-dir>/git-pair/hooks/<point>/` is a person's own file, never committed, run without a consent step, and it
is how a reviewer attaches a check to a repository they cannot edit. The two roots are therefore not two ways
of installing the same script — one is the repository's baseline, the other is git's model kept for the client.

The concern underneath the question is enforcement, and it is the open question above: a clone that has not
accepted the tracked tree runs none of its hooks and says so on stderr rather than refusing. If the right
answer is that a committed check must run or the command must fail, that changes consent's consequence, not
where the files live.

### "the second copy may be a feature rather than a bug in some cases … with the acceptance-persisted model, the only option is to accept the new version of the hooks or skip them, right?"

Not only those two. A clone whose repository shipped a version it does not want can keep running the version it
did read: copy that content into the per-clone root (`<git-dir>/git-pair/hooks/<point>/`), which runs directly
with no acceptance. That is the vendored model, applied by one developer for one point, and it is available
today — `git show <commit>:.git-pair/hooks/pre-check/01-suite` into the clone root, `chmod +x`, and the check
that was read is the check that runs. What git-pair does not do is keep that copy for everybody, which is where
the two models actually differ.

What the reviewer's scenario did expose was not the model but the signal, and that is fixed in this round.
Under the single boolean, a team shipping a breaking new version produced the same sentence as a clone that had
never accepted anything, so the developer who pulled could not tell "there is a new check to read" from "I have
not run `--trust` yet", and the default outcome was that the check stopped running without anyone deciding it
should. `git pair hooks` now answers `trust_state` — `unrecorded`, `changed`, `accepted` — and the changed case
names the accepted hash beside the tree's current one, so the move is visible and the developer chooses.

The tradeoffs, both ways, as they landed:

- **Staleness is invisible under vendoring, visible here.** A vendored copy can enforce a rule the repository
  no longer has, and produce a pass nobody can attribute; the acceptance model runs either the current content
  or nothing, and the report says which.
- **A false pass is the expensive failure for a check.** A developer on an old copy of `02-migration-check`
  while the schema has moved gets a green hook that tested something else. Under acceptance the same situation
  is a red flag on stderr with two hashes.
- **Rollback is cheaper here.** When a team reverts a bad hook version, the tracked hash returns to the value
  clones already accepted and everyone is running it again with no action; vendored copies have to be re-copied
  one developer at a time.
- **Coupling to the code is the tracked root's point.** A check that ships with the commit it checks cannot lag
  it. A vendored check can be a rule written for a version of the tree this clone no longer holds.
- **Adoption timing is the vendored model's real win, and it is the case above.** Its cost there is that the
  repository cannot see which clones are behind, which is the property a team that wants its check enforced
  cares most about.

The two decisions are coupled, and it is worth naming: if the open question above resolves toward refusing when
a tracked tree is unaccepted, a breaking hook version would stop every clone until each developer accepted, and
the copy-into-the-clone-root escape becomes the only way to keep working. That is when a first-class pin command
would earn its place, and it is recorded as an open question rather than built here — a command that promises a
way to keep working should be designed together with the rule it routes around.
