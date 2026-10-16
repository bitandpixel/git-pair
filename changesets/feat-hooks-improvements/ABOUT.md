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
git pair change ready --trust-hooks-once     # run the tracked ones with no position recorded, and record nothing
```

`--no-hooks` is the per-command form of the off switch and goes exactly as far: it never clears the refusal for an
`unrecorded` or `changed` tree, and it is absent from `check` and `change integrate`, so a green verdict is never
one flag from "the repository's checks did not run". `--trust-hooks-once` goes the other way — it clears the position
refusal and runs the scripts, and it does not let a failing hook pass. Neither is quiet: a skipped set arrives in
the `hooks[]` array as `status: "skipped-by-flag"`, and a hook that ran unrecorded carries `"consent": "once"`.
**The name says what it trusts.** `--trust-hooks-once`, not `--trust-once`: a flag whose only noun is "trust" reads
like a general decision to trust something, and it is nothing of the kind — it is this repository's hook scripts,
for one invocation, with nothing recorded. The flag is spelled the same way in every surface, including the JSON,
where a hook that ran this way carries `"consent": "once"`.

**The refusal names the way through.** The sentence that stops an unrecorded or moved tree already named `git pair
hooks --trust` and `git pair hooks --decline`; it now adds one clause, addressed to the person whose task is to
read the scripts in order to review a change to them: `--trust-hooks-once` runs them for this command alone and
records nothing. The alternative was a person who meets the refusal, knows a way through must exist, and has to go
and read the manual to submit a review. Naming a command in a refusal is not an instruction to run it — `--trust`
and `--decline` have been named there since the feature began, and nobody's gets to type them on another person's
behalf, which is a rule the skill carries and not one this sentence can enforce.

The reviewer's case works:

```
$ git pair review submit --feedback -m "the rewritten hook is wrong"        # exit 1, position refusal
$ git pair review submit --feedback -m "the rewritten hook is wrong" --trust-hooks-once
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

`--no-hooks` with `--trust-hooks-once` is refused in `PersistentPreRunE`, before any command body runs, the way
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
   position refusal, and `--trust-hooks-once` being offered on `check` and `change integrate` where `--no-hooks` is not.
5. The docs, including the skill's rule that neither flag is an agent's to type, and the sentence in the position
   refusal that names the flag anyway.

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
  together as exit 2. Its first two cases pin the refusal's own words for both states: the missing position, the
  moved tree, and the clause naming `--trust-hooks-once` — with the hook's own marker file asserted absent, so the
  sentence cannot quietly become a run.
