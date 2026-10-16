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
  the scripts — `--trust-hooks-once`, which is what the parent layer settled. This layer adds `--no-hooks` to the
  same sentence, so the way past a command that is in the way is visible from the refusal that stops it.
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
  hand-off and the gate both get through an unrecorded tree and leave the clone's record untouched — asserted
  against `git pair hooks`, which still says `NOT trusted` — and `change integrate --no-hooks` declares an approved
  head whose `pre-integrate` hook refuses.
- The pair refusal now runs over `change ready`, `check` and `change integrate` in
  `TestTrustOnceRunsTheUnrecordedTreeAndRecordsNothing`: eight commands carry both flags, and one rule in
  `PersistentPreRunE` is what has to hold them all.


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

Two things did not move with it, and a third did in the round after:

- **Nothing is quiet.** Every skipped hook arrives as `status: "skipped-by-flag"` in `hooks[]` and on stderr, so a
  verdict that ran nothing reads as one that ran nothing. `ready` itself is unchanged: the run says what it
  checked, and the array says what it did not.
- **The pair stays a usage error.** `--no-hooks` with `--trust-hooks-once` asks to run none of the scripts and to
  run the unread ones at the same time, so it is refused before the command body runs.
- **The position refusal moved,** in the next round. That one is its own section below, because it is the part of
  this layer most likely to be revisited.

The agent-facing rule is written into the skill beside the one for `GIT_PAIR_HOOKS=0`: an agent that cannot get the
gate to pass reports that, and does not skip the scripts until it does. That is the risk this widening actually
adds, and it is the one worth reading the skill text for.

### Can the two flags be combined to get past the position?

> would --no-hooks plus --trust-hooks-once allow getting through this? do they work together like that?

No. The pair is a usage error — exit 2, before the command body runs — with a message naming the opposition: one
runs none of them, the other runs the ones this clone has not recorded. It is refused in `hookOverridesAgree`, in
`PersistentPreRunE`, which is one rule for the eight commands that now carry both flags rather than eight rules; the
case now runs over `change ready`, `check` and `change integrate`, because the widening made the pair reachable on
the last two. `git pair hooks --trust --decline` is refused for the same reason — opposite positions about the same
scripts.

Neither flag needs the other to get a command through: `--trust-hooks-once` runs the scripts, `--no-hooks` skips
them, and either answers the position question for that invocation. The pair is refused because the two requests
contradict each other, not because either one is dangerous. The three positions that are offered, and what each
leaves behind:

| you want | you type | what it leaves behind |
| --- | --- | --- |
| the scripts to run, this once, though unrecorded | `--trust-hooks-once` | nothing; the next command asks again, and the hooks that ran carry `"consent": "once"` |
| this one command to run nothing | `--no-hooks` | a report naming every hook that did not run, and the same question from the next command |
| to stop being asked, having read them | `git pair hooks --decline` | a position recorded against the content hash, readable by the next person with `git pair hooks` |

The middle row is the new one, and it does not replace the third. `--decline` is still what to use when the question
should stop being asked, because it is the only one of the three that leaves a record — and the record is what the
next person reads when they wonder whether this team opted out or never knew the scripts existed.

### `--no-hooks` now answers the position question as well

The round after the one above asked why the flag should be refused at all when the tree is unrecorded, if skipping
is an escape hatch worth having. It is not refused now: any tree state, any of the eight commands.

The case that carried the old rule was "the off switch is not a position", and three questions from the review
found how little of it was load-bearing:

- *An agent optimising for a green answer will skip the hooks on a trusted tree too.* Correct. The refusal was
  never what stopped that; the report and the skill rule are, and they apply to every command equally.
- *The skipped hooks are listed in the payload either way.* Correct, and this is the question that finished it. The
  argument had been that a bypass which cleared the question leaves nothing to spot — but the run still reports
  `skipped-by-flag` for every configured hook, and `git pair hooks` still reports the tree as unrecorded, because
  the flag writes nothing. There was no lost signal to protect.
- *Is preventing abuse of the flag worth a refusal?* Not on the above.

What survives is friction: while the refusal stood, reading the scripts was the cheapest way through, and now
nothing requires it. That is a decision about how hard the tool should push, not a safety property, and it was made
towards the escape hatch — the failure the consent design worries about is a person or a team blocked by a script
they cannot use and cannot remove, and nobody can be blocked that way now.

Two properties keep this a widening rather than a removal, and they are why the flag is not simply a cheaper
`--decline`:

- It writes nothing. `git pair hooks` reads the same afterwards, so the report can still tell an opt-out from
  silence, which is the distinction the `declined` state exists to preserve.
- It lasts one command. The next command asks again, so the pressure returns for everything that did not come
  through the flag.

`GIT_PAIR_HOOKS=0` deliberately still refuses. It is ambient — one line in a shell profile or a CI image silences
a whole clone — and it reports nothing, which is the property that let the flag answer and does not let the
variable.

If this is revisited, the argument worth having is the queue one. A READY marker is a durable claim a reviewer
reads to decide where to spend attention, and under this rule a queue can fill with work whose suite was never run
and never read. That is a weaker claim than the one this section began with, and it is the one that survived.
