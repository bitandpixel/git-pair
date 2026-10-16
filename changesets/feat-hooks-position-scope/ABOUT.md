# Ask for the hooks position only where the command runs a hook

## Why this exists

The parent changeset added two flags for a reviewer who could not submit a review. The refusal that stranded
them was not the flags' fault, and this layer removes it.

`hooksPositionReason` asked `Trust`, which hashes the whole tracked tree — all twelve points — so one unrecorded
hook anywhere in `.git-pair/hooks/` blocked every gated command, including the ones with nothing to run:

```
$ git pair init                # init runs no hooks at all
git-pair: the repository's tracked hooks under .git-pair/hooks have not been recorded in this clone …
$ git pair review submit --feedback -m "the new pre-ready hook writes outside the repo"
git-pair: the repository's tracked hooks under .git-pair/hooks changed since this clone recorded them …
```

A submission runs `pre-submit` and `post-submit`. The hook that blocked it was `pre-ready`. The command was
asking a person to read a script it could not execute, which is the pressure the consent design says produces
acceptance without reading.

## What becomes different

Each command asks about the points it runs, and only those:

| command | asks about | says nothing about |
| --- | --- | --- |
| `change ready` | `pre-ready`, `post-ready` | the other ten |
| `review submit` | `pre-submit`, `post-submit` | the other ten |
| `check` | `pre-check`, `post-check` | `pre-integrate`, and a `pre-ready` suite |
| `change integrate` | `pre-integrate`, `post-integrate` | `pre-check`, and a `pre-ready` suite |

`init` runs no point, so it warns and starts the changeset:

```
$ git pair init
git-pair: warning: this repository ships 1 hook(s) under .git-pair/hooks that this clone has not read;
         `git pair hooks` prints them, and the first command that runs one asks for a position
committed 0a756eb
$ git pair change ready      # the ask arrives where a script would actually run
git-pair: the repository's tracked hooks under .git-pair/hooks have not been recorded in this clone …
```

## What is deliberately not changing

- **The record.** `Trust` still decides over the whole tree — one hash of what can execute. A team that adds a
  second script to a point you already accepted changes the hash, and you are asked again, at the command that
  runs it. Scoping the *question* does not soften the *answer*; that is the distinction the whole change rests
  on, and it is the one worth re-reading in `hooksPositionReason`'s comment.
- `change unready` and `change abandon` still ask nothing, and `git pair hooks` is still never gated.
- The refusal's sentence names `git pair hooks --trust`, `--decline`, and — for the person reviewing a change to
  the scripts — `--trust-hooks-once`, which is what the parent layer settled. This layer adds nothing to it.
- `--trust-hooks-once` from the parent stays. It is no longer the only way a reviewer of a hook change gets through —
  with the scoping, most of those cases never refuse — but it is still how you run the scripts *at your own
  point* unrecorded, which is now the reviewer case the tests use.

## How it was implemented

`hooks.TrackedHooksAt(workTree, points...)` is the boundary: the tracked root's hooks for the points named,
clone root excluded (consent was never owed there). `hooksPositionReason` and `hooksPositionRefusal` take the
points variadically and return no reason when that list is empty. `integrationGate` gained a `points` parameter
because it is shared code with two callers that run different points — the four gate cases in the new test are
that parameter's justification. `init` calls the new `hooksUnrecordedNotice`, which counts across all points and
warns.

Cost: two `ReadDir`s per ask, no git subprocess, so `TestCheckAsksTheHookQuestionsOncePerRun` is unaffected.

`--no-hooks` reaches `check` and `change integrate` the same way `--trust-hooks-once` already did — one call to the
shared `noHooksFlag` helper in each command's flag setup, so the flag means one thing everywhere. `runPoint` and
the position question already read `a.hooksOff`, so no command logic changed; what changed is which invocations may
set it, the comments that argued the opposite, and the documentation that repeated it.

## Where to look

1. `Ask for the position only where the command runs a hook` — the code. The judgement to test is whether any
   command that *should* ask now stays quiet. The gate is the risky one: it is shared by `check` and `change
   integrate`, and if a point is missed on the caller side, a merge stops asking about a script it does run.
   `TestThePositionIsAskedOnlyWhereTheCommandRuns` is written as a command/point matrix for exactly that.
2. `Say where the position is asked, and where it is not` — the prose. Check that `init` reading as lenient does
   not leave an agent without a prompt; the skill's instruction is the answer, and it is worth a read.
3. `Let the gate be told to skip its hooks as well` — the flag's reach. The thing to argue with is the argument
   itself: if you think a local `check` verdict should still be hard to get, this is the commit that softened it,
   and the case that guards the softening is the one where the position refusal still fires.

Three existing tests moved to the point their command runs, and the `init` gate test became a warning test that
also asserts the refusal lands at the hand-off. If you think the `init` warning is too soft, that test is where
to say so.

## How we know it works

`mise run gates` (gofmt, vet, the sharded suite, the PRD §29 loop, the pty TUI walkthrough, the CI merge job),
and `go test ./...` on its own.

- `TestThePositionIsAskedOnlyWhereTheCommandRuns` — twelve command/point pairs: each command refuses for a hook
  at its own points and proceeds for one at someone else's, including `check` versus `change integrate`.
- `TestTrackedHooksAtNamesOnlyThePointsAskedFor` — the list the question is built from: its own points, tracked
  root only, nothing for an empty point.
- `TestTheCommandsThatStartWorkAskForAPosition` — the `init` case now asserts the warning, that the changeset is
  created, and that `change ready` is where the refusal arrives.
- Three cases in `TestNoHooksSkipsTheRunAndSaysSo` for the flag's new reach: `check --no-hooks` reports the rest of
  the gate while a `pre-check` hook is broken (and the same run without the flag carries the hook's refusal), the
  same command still refuses for a tree nobody recorded, and `change integrate --no-hooks` declares an approved
  head whose `pre-integrate` hook refuses.


## Also asked for in review: `--no-hooks` on the gate and the declaration

> While we're in here, can we also allow --no-hooks to run on check and integrate? I think omitting it from them
> causes more complications than value.

Done — `check` and `change integrate` carry it now, so every command that runs the repository's scripts carries
both hook flags.

The old boundary was this author's, and it rested on a claim the repository does not make: that a green local
`check` is what lands work. It is not. The integrate job runs `git pair check` without the flag, builds the merge
commit, and runs the repository's own `verify` command against that tree — the scripts a person skips locally are
the same scripts that job declines to take on trust. With that corrected, keeping the flag off those two commands
bought a guarantee nothing depends on and cost a person the verdict whenever a hook misbehaved: `check` answers
every question it has, and one broken `pre-check` was enough to stop it answering the rest.

Two things did not move with it:

- **The position refusal still fires.** `check --no-hooks` on a tree this clone never recorded still answers NOT
  READY and still names `git pair hooks --trust` and `--decline`. The off switch says a machine does not want to
  run scripts today and says nothing about what those scripts do; that rule is why the flag is not a way through
  the position, on any command.
- **Nothing is quiet.** Every skipped hook arrives as `status: "skipped-by-flag"` in `hooks[]` and on stderr, so a
  verdict that ran nothing reads as one that ran nothing. `ready` itself is unchanged: the run says what it
  checked, and the array says what it did not.

The agent-facing rule is written into the skill beside the one for `GIT_PAIR_HOOKS=0`: an agent that cannot get the
gate to pass reports that, and does not skip the scripts until it does. That is the risk this widening actually
adds, and it is the one worth reading the skill text for.
