# feat-slow-tests

`mise run check` took about two minutes, and the wait was not the tests computing anything. It is
waiting for git: the suite starts **37,560 git processes** in `internal/cli` alone, ~2.1 ms each on this
box, which is ~78s of that package's 97s. Everything that does not talk to git finishes in under four
seconds.

The change removes a fifth of those subprocesses, stops the pty gate sleeping for 42s of its 47s, and
runs the suite over several `go test` processes instead of one.

```text
internal/cli git subprocesses   37,560 -> 30,580        (measured, not timed: no noise)
internal/changeset              11.0s  ->  3.6s
pty walkthrough                 47s    -> 32s
git pair status                 24     -> 21 git calls
mise run gates, warm            ~170s  -> 68s
go test ./..., cold             106-135s -> 98-102s sharded
```

The plan was measurement first: a `git` shim on `PATH` that logs every invocation, and a
`EPOCHREALTIME`-stamped `bash -x` over the gate scripts. Where the time went is in `## Design
decisions` below, because it is what makes the four changes the short list rather than a longer one.

## Summary

**`DefaultBranch` asked git up to seven questions per command and now asks one.** It resolved
`refs/remotes/origin/HEAD`'s target, `refs/remotes/origin/{main,master}` and `refs/heads/{main,master}`
with one `rev-parse --verify` each. `git for-each-ref --format=%(refname)%09%(symref) refs/remotes/origin
refs/heads` answers all of them in one spawn. It goes into `internal/git` as `ListRefs`, which maps a ref
name to the ref it is symbolic for.

**The fixture wrote its repository config with seven git subprocesses.** `gittest.New` ran
`git config --local` once per key. The suite builds over three hundred fixtures, so this was 2,409
subprocesses of pure set-up. The keys are constants this package owns, so they are written into
`.git/config` as text.

**The pty walkthrough now stops reading an idle terminal.** It ran ~28 pty sessions and waited a fixed
1s settle plus a fixed 0.8s tail on every one, which was ~42s of its 47s. Only `--settle` shortens now,
and only on silence; the reasoning for why the *others* may not is in `## Design decisions`.

**`scripts/test-sharded.sh` runs the suite over several `go test` processes.** A shard is one `-run`
regex over names read from `go test -list`, so the script keeps no list of its own and cannot drift from
the suite. `mise run test` and `check` use it; `mise run test:serial` is `go test ./...` for when the
plain, package-ordered output is the thing you want.

## What changed

**`ListRefs`** (`internal/git/git.go`). One `for-each-ref` over prefixes, returning name → symbolic
target. It exists because `rev-parse --verify` is one subprocess per ref, and the resolution code asks
several existence questions in a row. It also documents the behaviour this change now leans on: git
leaves a symbolic ref whose target does not resolve out of the listing entirely, so an entry is a ref
that resolves — the same fact the follow-up `rev-parse` used to prove.

**`DefaultBranch` reads the listing** (`internal/changeset/resolve.go`). The decision rules, the two
refusal messages and `DefaultBranchRef.Source` are untouched; only the way each answer is obtained moved.
The listing's own error is swallowed rather than returned, because the probes it replaced each swallowed
theirs: callers branch on `ErrNoDefaultBranch`, and a repository git will not talk to keeps answering
that rather than a git error they would not recognise.

**`TestDefaultBranchIgnoresADanglingRemoteHead`** (`internal/changeset/resolve_test.go`).
`git remote set-head` records a symbolic ref, and deleting the branch it pointed at leaves the pointer
behind. Before, `symbolic-ref` reported it and the follow-up `rev-parse` failed, so the search fell
through; now git simply omits the ref. Same outcome, different mechanism, so the mechanism is pinned —
there was no test for this case and the change rests on it.

**`writeLocalConfig`** (`internal/gittest/gittest.go`). One append of a constant block to the config
`git init` just wrote. `Config` stays: two tests set a key of their own, and `TestFixtureSetsIdentityLocally`
already asserts the keys are local, which is what would notice a bad write.

**`pump(until, quiet=0.0)`** (`scripts/gates/pty-tui.py`). With `quiet` set it stops once something has
painted and nothing has arrived since; `until` becomes a ceiling rather than a promise, so a program that
paints continuously or never paints at all is still waited for exactly as long as before. The select
timeout went 0.05 → 0.02 so the silence threshold has something to resolve against.

**The job queue** (`scripts/test-sharded.sh`). It lists each package's tests, shards by name
round-robin, and runs the shards through `xargs -P`. A shard's `-run` regex arrives as a file named after
its job rather than as an argument — it runs to ~4 KB, and an argument that long is one more thing that
can be mangled between the queue and the process. Each shard buffers its own output so concurrent shards
cannot interleave reports, prints the one `ok`/`FAIL` line `go test` would have printed, and hands the
captured detail to the parent, which replays it only for a shard that failed.

**`mise run test`, `check`** use the sharded runner; `test:serial` does not. `README.md`'s Development
section says which is which and why a second one exists.

## Design decisions

**Sharding across processes, not `t.Parallel()` inside one.** `internal/cli` and `internal/tui` call
`cli.Execute` inside the test binary, which means they drive it through the process working directory and
the process standard streams — the harness swaps `os.Chdir` and `os.Stdout/Stderr/Stdin` around each run.
Making those injectable is a real change to `cli.Execute` and to every test that reaches the CLI, for an
overlap that splitting processes gets for free: each shard process still runs one test at a time, so the
shared-cwd assumption holds exactly as it did. `gittest.New`'s `t.Setenv` is a second, independent blocker
for `t.Parallel()`, and would have pushed process-wide isolation setup into a `TestMain`.

**Round-robin over test names, not over measured durations.** Names and costs are unrelated here, and the
shards landed within four seconds of each other (38.6 / 42.1 / 42.4). Carrying timings would mean a cache
the script has to invalidate.

**Only `--settle` shortens, and that is the whole argument.** `pty-plain --after N` returns everything
from keystroke N to the *end* of the capture, so a window is what a `refuse` check reads — "did this *not*
paint from here on" — and a window shortened on silence can only ever make such a check pass more easily.
The tail closes the last window, the per-keystroke `--gap` bounds the window a keystroke opens, and an
explicit `~<seconds>` waits for a timer the harness cannot observe, so silence proves nothing there. All
three stay fixed wall clock. `--settle` is the one window where shortening is defensible, because what it
can lose is a late *second* frame rather than the first one those checks are about — and it is still a
trade, so the threshold is generous (0.4s) and `--settle` remains the ceiling.

**No cache in `internal/git`.** A scoped read-only memo looked like the largest available win, and
measuring it properly said no. The 22% figure that motivates it — duplicate argv within one command — is
not the recoverable number. A duplicate is only removable if nothing wrote between the two calls, and
under that rule:

```text
git spawns in internal/cli        : 29363
duplicate argv, per command       : 6459  (22%)
  of those, inside one read phase : 2554  (9%)     <- recoverable, ~5s of CPU
commands that mutate at all       : 1164 of 1199
```

The 22% collapses to 9% because most commands write something, and most duplicate pairs straddle that
write — the code re-reads *because* it just changed the repository. For ~5s it would put two headline
behaviours at risk, both of which observe another process writing:

- `change wait` polls `observeReview` → `SummarizeHEAD` (`internal/cli/change.go:850`). Without
  `--fetch` the review arrives from another clone, so nothing in this process would invalidate anything:
  the poll would never see it and would always time out.
- `Session.CheckDrift` (`internal/tui/session.go:432`) asks where the span's endpoints point *now*,
  repeatedly, while a session is open. The walkthrough's `session drift
  "!git update-ref refs/heads/probe HEAD,~3.5,r,q"` is that behaviour, checked.

A memo on `Repo` cannot tell those apart from a one-shot `status`, because they use the same type the same
way. The fix for the remaining 9% is to stop asking twice — resolve `HEAD` once and pass the SHA down
(1,311 of the 2,554 are `rev-parse --verify --quiet HEAD`), which is plumbing with no cache semantics and
no invalidation question. Deliberately not in this changeset: it is a different change, and it is worth
3s rather than this much prose.

**Two shards per core, three workers on two cores.** Swept: 3/2 → 121s, 4/2 → 105s, 6/2 → 83s, 4/3 → 74s,
8/3 → 84s on one pass, and the same configuration measured 98-127s on another. The machine's own load
moves these numbers more than the configuration does, so the default is the value that was never worse
rather than a tuned optimum, and `GIT_PAIR_TEST_SHARDS` / `GIT_PAIR_TEST_WORKERS` override it.

**Small packages are not split.** A shard pays the binary start-up like any other `go test`, so
`SHARD_MIN_TESTS` (40) leaves the eleven small packages in one process each.

## Validation

- `mise run gates` twice green: gofmt clean, `go vet ./...` clean, the whole suite sharded, `e2e-29.sh`
  (`E2E: all checks passed`) and `scripts/gates/pty-walkthrough.sh` (`PTY: all checks passed`), against a
  binary built from this branch (`git-pair-slow-tests`, which is also what both scripts default to).
- `scripts/gates/pty-walkthrough.sh` run three times, all green, at 32s. It has ~28 pty sessions in it and
  each one is a real terminal with a real TUI, so a change to the waits is exactly the kind of thing that
  would show up as an intermittent failure rather than a wrong assertion.
- The subprocess counts are the measurements to believe, because they have no timing noise: a `git` shim on
  `PATH` logs every invocation, and `internal/cli` goes 37,560 → 30,580 with the same 297 tests. The
  per-command figure comes the same way — `git pair status` over a fixture repo, 24 calls → 21.
- `ListRefs` is checked by every existing `DefaultBranch` test: `TestDefaultBranchReadsWhatGitRecords`
  (the remote's answer beats a local `main` beside it), `TestDefaultBranchFallsBackAndRefuses` (sole
  origin branch, and two origin branches as a coin flip), and the two override cases — and by the new
  dangling-pointer test. Nothing in `resolve_test.go` or `internal/cli` asserts an exact spawn count that
  this moves: `fetch_test.go`'s `+3` is a *difference* between two runs, so it is unchanged by both
  commands getting cheaper.
- Sharding's correctness argument is structural rather than observed: each shard is an ordinary
  single-process `go test`, and `GIT_PAIR_TEST_SHARDS=1` reproduces `go test ./...` exactly. Verified by
  running it that way and reading the output.
- Timings, for the record, on a box whose CPU stall (`PSI cpu some avg300`) sat between 6% and 63% during
  this work: cold `go test ./...` 106-135s against 98-102s sharded, and `mise run gates` warm at 68s. The
  clearest single measurement of what sharding buys is `internal/cli` alone at three shards on a quieter
  machine: 97s → 44s.
- One number is worth stating because it is a regression: with everything cached, `mise run test` is 34s
  against `test:serial`'s 26s, because the script pays `go test -list` for every package. `test:serial`
  exists partly for that case.

## Known limitations

- **The suite's wall clock on this box is CPU-bound by a machine that is not free.** The suite needs ~110s
  of CPU and the run gets ~1.07 of 2 cores, so wall ≈ CPU and no amount of parallelism helps. That is the
  honest reason `check` is still ~100s here rather than under a minute: the floor with two genuinely idle
  cores is ~55s. Nothing in this changeset claims otherwise, and the timings above are ranges for that
  reason.
- The pty gate's `--settle` shortening can lose a repaint arriving more than 0.4s after a screen already
  fully painted. It is the one window where that is acceptable, and 0.4s is chosen generously rather than
  for the last second of saving.
- `DefaultBranch` is still called two or three times per command (`changeset.go:393`, `changeset.go:437`,
  `resolve.go:299`), so its listing appears 69 times redundantly across the suite. Each call is one spawn
  now instead of up to seven, which is most of the win; the rest is the hoist described above.
- The read-phase analysis splits on argv shape (`add`, `commit`, `update-ref`, `fetch`, `switch`,
  `config`, …). That is coarse, so the 9% is approximate. Its direction is not: 1,164 of 1,199 commands
  write something.
- A shard is a `go test` process, so a build failure in one package surfaces as a failing shard rather
  than in the position `go test ./...` would have put it. The parent replays the captured output, so
  nothing is lost, but the ordering is no longer package order.
- `SHARD_MIN_TESTS` is a constant. A future package that grows past 40 tests starts getting split, which
  is the intent, and a package of 39 slow tests stays in one process, which is the cost.

## Open questions

- The remaining recoverable spawns are one fact asked repeatedly: `rev-parse --verify --quiet HEAD`, 1,311
  of the 2,554. `sessionFor` already builds a struct holding `head` for one caller — the question is
  whether that value should be threaded through the resolution and lifecycle code instead of each layer
  re-resolving it. That is worth about 3s and a fair amount of diff, so it is left here rather than done.
- Is `internal/cli`'s 97s mostly *tests* or mostly one-time cost per process? Three shards of the same
  package sum to ~54s of reported test time against 97s serial, which does not add up, and the difference
  is unexplained. Per-test durations from `-v` sum to 70.5s against 97s of package time. Worth
  understanding before spending more on sharding, since it decides whether the floor is the tests or
  something per-process.
- Should `change wait` and `CheckDrift` say so in their types? Both are reads whose whole purpose is to see
  a change made elsewhere, and both currently look like ordinary reads through an ordinary `*git.Repo`. If
  a second "several questions, one spawn" helper arrives, that distinction is the thing a helper must not
  paper over.
