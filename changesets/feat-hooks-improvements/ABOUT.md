# Two opt-outs for lifecycle hooks, and two bugs they exposed

## Why this exists

Dogfooding git-pair on another repository produced a dead end. A changeset added `.git-pair/hooks/pre-ready`,
the reviewer opened `review open` to comment on it, and the submission was refused:

```
git-pair: the repository's tracked hooks under .git-pair/hooks changed since this clone recorded them
(recorded e4796f2, the tree is now dbfba65): the checks they carry have not run on this content — read what
moved, then `git pair hooks --trust` to run it or `git pair hooks --decline` to record that you read it
```

The content the reviewer was asked to accept was the content they were being asked to review. `--allow-hook-failure`
does not reach it, because it covers scripts that ran and refused, not scripts nobody has read.

Two things came out of that, plus two defects found on the way:

- there was no way to say "not this command" about hooks. `GIT_PAIR_HOOKS=0` and `git-pair.hooks=false` are
  switches for a machine, not for an invocation, and neither leaves anything behind saying what it skipped.
- the refusal was also **stale**. Deleting the comment changed nothing; a second submit read the same refusal;
  quitting the screen and reopening it fixed it. `hooks.Resolution` kept the clone's position for the life of the
  process, and the review screen is a process that lives as long as a review — long enough for a second terminal
  to run `git pair hooks --trust`.
- the recursion guard did not guard. A `pre-ready` hook that shells out to `git pair check` fired `pre-check`.

## What becomes different

Two flags, at the two boundaries the feature already draws:

```
git pair change ready --no-hooks       # run none of them, either root, pre- and post- both
git pair change ready --trust-once     # run the tracked ones with no position recorded, and record nothing
```

`--no-hooks` is the per-command form of the off switch and goes exactly as far: it never clears the refusal for an
`unrecorded` or `changed` tree, and it is absent from `check` and `change integrate`, so a green verdict is never
one flag from "the repository's checks did not run". `--trust-hooks-once` goes the other way — it clears the position
refusal and runs the scripts, and it does not let a failing hook pass. Neither is quiet: a skipped set arrives in
the `hooks[]` array as `status: "skipped-by-flag"`, and a hook that ran unrecorded carries `"consent": "once"`.
^ let's rename the flag to be clear that the trust-once applies specifically to hooks. And should (or is it already) a 
hint in the error message when trying to run `git pair change ready` when there are untrusted hooks?

The reviewer's case works:

```
$ git pair review submit --feedback -m "the rewritten hook is wrong"        # exit 1, position refusal
$ git pair review submit --feedback -m "the rewritten hook is wrong" --trust-once
review: feedback …                                    # and the clone's record is untouched
```

And a screen that has been open since before a `--trust` now sees it, instead of refusing content the clone has
already settled.

The recursion guard now works, in the direction the docs always claimed:

```
pre-ready saw GIT_PAIR_HOOKS=0
(pre-check does not run)
```

## What is deliberately not changing

- **No new record.** Both flags are reported on stderr and in `--json` and nothing more. A marker trailer
  (`Review-Hooks: skipped:…`) was the alternative and is not here; see the review question below.
- **The refusal sentence is unchanged.** It still names `git pair hooks --trust` and `--decline` and not these
  flags, because whether somebody's script runs on this machine — or is skipped on it — is the person's decision.
  The skill says so twice, in the same voice as the existing rule about `--trust`.
- **`init` gets neither flag.** It runs no hooks, so a flag there could only mean "proceed without anybody
  reading them", which is the one meaning worth denying.
- The four trust states, the tree hash, and what a position covers are untouched — that is last week's
  changeset, not this one.

## How it was implemented

**The memo was the bug, and the fix is to keep less.** `Resolution` now caches only what cannot move inside a
process — the off switch and where the clone's git directory is, both git reads, both the cost gate counts — and
re-reads the two records and the tree hash whenever a command asks. `Plan` gained `TrustState`, `Accepted`,
`Current` and `Declined`, because a point that decided the tracked hooks do not run and then asked again to build
the warning could read the record twice and get two answers. `runPoint` reads the plan.

**The guard was a value, not a mechanism.** `hookContext.env` set `GIT_PAIR_HOOKS=1` and `Disabled` reads `1` as
"run them", so the child re-entered. The child now inherits `hooks.GuardValue`, and `TestGuardValueSwitchesHooksOff`
pins the pair in the package that owns both halves, so either side drifting fails there rather than only in a
scenario test. PRD:2062 and PRD:2131 had disagreed about the value; the implementation matched neither.

**Consent is per-result, not per-command**, because a run mixes roots: one report can hold a clone hook that
always runs, a tracked hook accepted months ago, and a tracked hook nobody recorded.

`--no-hooks` with `--trust-once` is refused in `PersistentPreRunE`, before any command body runs, the way
`git pair hooks --trust --decline` is: opposite requests about the same scripts, and the combination would
produce a hand-off that looks checked and is not.

## Where to look

The commits are four separable layers; reviewing them in order is easier than reviewing the combined diff.

1. `Read the hook record again while the process is open` — the memo. The judgement to check is the cost: the
   trust answer is now recomputed per call, which is `sha256` over a handful of small files per point (`git pair
   hooks` walks twelve). It costs no git subprocesses, which is what `TestCheckAsksTheHookQuestionsOncePerRun`
   measures, but it is a filesystem read that used to happen once.
2. `Make the hook recursion guard guard` — one value and the two spec sentences that disagreed about it.
3. / 4. The two flags. The boundary decisions are the part worth arguing with: `--no-hooks` not clearing the
   position refusal, and `--trust-once` being offered on `check` and `change integrate` where `--no-hooks` is not.
5. The docs, including the skill's rule that neither flag is an agent's to type.

The open question is whether a skipped or once-run set should also leave a trace in history. This changeset
reports it in the output of the run that made the decision and writes nothing, which fits "nothing to write" and
means a hand-off that skipped the suite is distinguishable only while that command's output survives. A trailer
on the ready marker would let `check` — and a reviewer reading `git log` a week later — see it. I did not add one;
say so if you want it.

## How we know it works

`mise run gates` (gofmt, vet, the sharded suite, the PRD §29 loop, the pty TUI walkthrough, and the CI merge job),
and `go test ./...` on its own.

The behaviours are pinned where they can fail:

- `TestResolutionReadsTheTrustRecordAgainEachTime` — a position recorded in another terminal, content moved under
  a recorded position, and a decline recorded in another terminal. All three answered from the memo before this.
- `TestGuardValueSwitchesHooksOff` (the pair) and `TestHooksSwitches/a hook calling git pair fires no hooks of its
  own` (the consequence, plus `env -u GIT_PAIR_HOOKS` as the way back).
- `TestNoHooksSkipsTheRunAndSaysSo` — both roots, both halves, the JSON entry, and the refusal it does not clear.
- `TestTrustOnceRunsTheUnrecordedTreeAndRecordsNothing` — the reviewer of a hook change submitting, the failing
  hook still refusing, the gate running the unrecorded checks, nothing written to the record, and the two flags
  together as exit 2.
