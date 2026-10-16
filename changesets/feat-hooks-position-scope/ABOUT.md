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
- The refusal's sentence is unchanged, and still names only `git pair hooks --trust` and `--decline`.
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

## Where to look

1. `Ask for the position only where the command runs a hook` — the code. The judgement to test is whether any
   command that *should* ask now stays quiet. The gate is the risky one: it is shared by `check` and `change
   integrate`, and if a point is missed on the caller side, a merge stops asking about a script it does run.
   `TestThePositionIsAskedOnlyWhereTheCommandRuns` is written as a command/point matrix for exactly that.
2. `Say where the position is asked, and where it is not` — the prose. Check that `init` reading as lenient does
   not leave an agent without a prompt; the skill's instruction is the answer, and it is worth a read.

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
