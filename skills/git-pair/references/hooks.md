# Lifecycle hooks

A repository can run its own script at the moment work is handed on, and refuse the hand-off when that script
says no. The built-in gate can only ask what git already knows — a clean tree, an `ABOUT.md` that exists, a
standing approval. Whether the suite passed, the migration applies, the generated client is current: none of
that is in the graph, and git-pair cannot know it for a repository it does not own.

**Hooks are the client's.** git-pair publishes an event; the checkout owns the script. That is the same
relationship git has with its own hooks, and it has two consequences worth stating plainly: a hook is not a
control against the person working in the clone (they wrote it, they can delete it), and a hook may run any
command, including ones git-pair itself never invokes — `push`, `merge`, anything. The rule in PRD §26 binds
what this tool runs of its own choosing, not what you install beside your own git directory.

## Where a hook lives

Two roots, consulted in this order:

| Root | Path | Who it is for | Consent |
| --- | --- | --- | --- |
| `worktree` | `.git-pair/hooks/<point>/` | the repository's rules, committed and reviewed | runs only after a person accepts the content with `git pair hooks --trust` |
| `clone` | `<git-common-dir>/git-pair/hooks/<point>/` | your own checks, on your own machine — one directory per clone, so a worktree does not need its own | none — it is your file, never committed |

The tracked root runs first, then the per-clone root: the repository's rules are the baseline and the client
adds to them. Under a point, either a directory of scripts or one file named for the point is accepted.
Scripts run in lexicographic order by name, which is how a suite is sequenced — `01-fmt`, `02-test` — with no
format to declare order. Dot-files, `*.sample`, `*.md` and `*~` are ignored by name.

A reviewer reviewing a repository they cannot edit uses the clone root. That is the half that makes hooks
usable for review at all: a body policy, a personal lint pass, a notification, none of which need anybody's
approval because none of them leave the machine.

## The points

Twelve, in the order a changeset meets them — six actions, a side before and a side after:

| Point | Fires | A failing pre-hook |
| --- | --- | --- |
| `pre-ready`, `post-ready` | `change ready` | refuses the offer, writes no marker |
| `pre-integrate`, `post-integrate` | `change integrate`, after its own cheap gate | refuses the declaration, writes no marker |
| `pre-unready`, `post-unready` | `change unready` | refuses the withdrawal |
| `pre-abandon`, `post-abandon` | `change abandon` | refuses the abandonment |
| `pre-check`, `post-check` | `check`, around the gate | appends a reason and sets `ready: false` |
| `pre-submit`, `post-submit` | `review submit`, after staging and before the review commit | refuses the verdict, writes no review commit |

The read-path commands — `queue`, `status`, `diff`, `feedback`, `history`, `wait` — run no hooks, on purpose.
A hook on a read turns a lookup into a build, and a cost test that counts subprocesses would start measuring
your script. A command that writes nothing runs no hooks either: `unready` on a changeset never offered,
`abandon` on one already abandoned, `integrate` on a head already declared.

A misspelled point directory is never guessed at. `git pair hooks` reports it under `unknown[]`, because an
entry nobody runs is otherwise indistinguishable from no entry at all.

## Writing one

A hook is executed directly — never through a shell — with the repository root as its working directory and
nothing on stdin. It needs a shebang and the executable bit. Read the environment, not the arguments: git
gives `pre-commit` no arguments either, and a hook that re-derives which changeset it is inside would have to
reimplement rules that live in this binary.

| Variable | Meaning |
| --- | --- |
| `GIT_PAIR_HOOK_POINT` | the point that fired, e.g. `pre-ready` |
| `GIT_PAIR_CHANGESET` | the resolved changeset id |
| `GIT_PAIR_BRANCH` | the branch holding the work |
| `GIT_PAIR_BASE` | the base branch |
| `GIT_PAIR_HEAD` | the current head commit |
| `GIT_PAIR_DEFAULT_BRANCH` | the destination, when known |
| `GIT_PAIR_HOOK_ROOT` | `worktree` or `clone` |
| `GIT_PAIR_HOOKS` | `0`, inherited by anything the hook runs — a nested `git pair` runs no hooks, and `env -u GIT_PAIR_HOOKS git pair …` opts back in |

The submission points add three, because a verdict has content of its own:

| Variable | Meaning |
| --- | --- |
| `GIT_PAIR_REVIEW_OUTCOME` | `approve`, `block`, or `feedback` |
| `GIT_PAIR_REVIEW_HEAD` | the commit being reviewed |
| `GIT_PAIR_REVIEW_BODY_FILE` | a file holding the reviewer's body; git-pair deletes it when the command returns, so copy it if you mean to keep it |

A hook's stdout and stderr never reach git-pair's stdout — that is where `--json` lives. A failing hook's
output is replayed on stderr with the refusal, because that is the text the author needs next to the word
"failed"; at the reviewer's submission points run from the review screen it is shown on the screen instead,
because the screen owns the terminal while the submission runs and text written beside it is painted over.
A passing hook's output is replayed only when `GIT_PAIR_HOOK_OUTPUT=1`, so a suite that prints four
hundred lines does not make every hand-off unreadable.

Each hook gets a deadline: `GIT_PAIR_HOOK_TIMEOUT` seconds, then `git-pair.hookTimeout` in git config, then
ten minutes. A hook that leaves a background process running is waited on for five seconds after its deadline
and then cut off, so a runaway child does not turn the deadline into a suggestion.

```sh
#!/bin/sh
# .git-pair/hooks/pre-check/02-suite — the repository's own conditions, inside the gate's answer.
if ! mise run test > /tmp/git-pair-suite.log 2>&1; then
  echo "the suite failed; full output in /tmp/git-pair-suite.log" >&2
  exit 1
fi
exit 0
```

## What a failure means

**A pre-hook that exits nonzero refuses the operation with exit 1** — the same class of refusal as a dirty
tree or a missing `ABOUT.md`, and never exit 3, which means git itself failed. Read the hook's own output and
fix the failure. Nothing is written: no marker, no review commit, no ref.

**A post-hook that fails does not reverse anything.** The command still exits nonzero and prints the hook's
output, and the message names the state the changeset is in, the hook, and that nothing was undone:

```text
booking-transaction is READY, but the post-ready hook "01-notify" failed with exit 1:
webhook refused
nothing was undone — the marker is committed, so judge this by the state named above.
```

So judge a post-hook failure by what that line says about the marker, not by the exit code alone. The marker
was written.

**At `check` the two points join the verdict** instead of refusing, because `check` writes nothing: a failing
`pre-check` or `post-check` appends a reason and sets `ready: false`, so
`git pair check --json | jq -e '.ready and .integrating'` still answers the whole question. Note the exit-code
shape: `check --json` exits 0 and carries the verdict in `ready`; the human form exits 1 with `NOT READY:`.

## Trust

A tracked file is code that runs on a clone, which is why git keeps its hooks under `.git/` and refuses to
clone them. git-pair's tracked hooks are committable because consent is taken by content: the path, the
executable bit, and the bytes of the files the runner would run are hashed, and the hash is added to the set of
content this clone has taken a position on, recorded in `<git-common-dir>/git-pair/trusted-hooks` (and
`declined-hooks` beside it).

**The hashed set and the running set are one list.** Discovery decides what executes — the single-script form, or a
point directory's entries that a reader would call a hook — and the identity hashes exactly that. An entry the runner
skips is outside the identity as well, so a vim swap file left beside a hook cannot revoke a position a person took
about other people's scripts; an entry the runner would execute is inside it, so a script nobody has read cannot
inherit somebody else's acceptance. When you report a `changed` state, name what moved between the two hashes: under
this rule the movement is content somebody wrote, never a file the machine dropped beside it.

**The record belongs to the clone, not to one working directory.** What it answers for is content, and every
worktree of the clone checks the same content out; recording it per worktree made a person who keeps a dozen
worktrees read the same dozen lines a dozen times, and the tenth reading is not consent. One `--trust` or
`--decline` covers every worktree of the clone, including ones cut afterwards. There is no second place to look:
nothing ever shipped a per-worktree record, so there is none to migrate and none to keep in step.

**Each entry answers for the content it names.** Two worktrees can stand on two versions of the directory — one
on trunk, one on the branch that edits the suite — and each keeps its own entry, so recording a position in one
does not invalidate the other's. Only the executable bit of the mode is hashed: an executable script is a
different thing to run, while the rest of the permission set is whatever the machine's umask and the writer chose,
and two checkouts of one commit would otherwise hash apart.

An unaccepted tree does not run at all, and the skipped hooks are named on stderr, so a clone is never surprised
by an unreviewed script and never silently left without the checks it thinks it has. Editing a hook changes the
hash, so an edit after acceptance is a new acceptance.

**`trust_state` carries four states, because the reasons a tracked tree does not run are different facts.**
`unrecorded` means nobody on this clone has recorded anything about the tree — they have not looked. `changed`
means this clone accepted a version and the repository has since shipped another — the team moved a check
underneath them. `declined` means somebody read the scripts and chose not to run them here, which is a position
rather than an absence, recorded beside the acceptance in `<git-dir>/git-pair/declined-hooks` against the same
content hash. Act on them differently: the first is "read these", the second is "read what moved, then decide",
and the third needs nobody — but check the hash beside it, because a new version of a declined script is
unrecorded, not declined, and the report shows both hashes so you can tell which you have. Accepting whatever is
there because a note asked you to is how a repository ends up running a check nobody read.

**The commands that start or advance work ask for a position.** While the tracked tree is `unrecorded` or
`changed`, `git pair init` refuses before the changeset directory exists, `git pair change ready` before the
hand-off, `git pair review submit` before the reviewer's content is staged, and the gate refuses the merge —
`git pair check`, and `git pair change integrate` with it. Each says the same sentence and names the ways out —
`git pair hooks --trust`, `git pair hooks --decline`, and, in a clause addressed to the person whose task is to
read the scripts in order to review a change to them, `--trust-hooks-once` — so
meeting it twice is not two different problems. None of it is an obstacle to route around: the checks the
repository asked for have not run. `accepted` and `declined` both clear it — one because the scripts ran, one
because a person answered a question about them.

Two doors stay open with nothing recorded, on purpose. `change unready` and `change abandon` work, because they
take work out of the queue and a person who cannot run the checks still has to be able to put the work down. And
`git pair hooks` never asks, because reading the scripts is the act that comes *before* the decision — a command
that refused until you had decided would make the reading impossible.

A clone sitting on `changed` has a third option besides accepting and declining: copy the version it did read into
the per-clone root, which runs with no consent step. That is the vendored model applied by one person for one
point, and it is the honest way to keep working while the team's new version awaits you.

```bash
git pair hooks            # what is configured, both roots, executable or not
git pair hooks --json     # + tracked_root, clone_root, trusted, trust_state, accepted, declined, tree_hash, hooks[], unknown[]
git pair hooks --trust    # a person's act: records the tracked tree as accepted, printing names and hash
git pair hooks --decline  # a person's act: records it as read and chosen not to run, printing names and hash
```

**`--trust` and `--decline` are a person's decisions, not an agent's.** The first says "I read this script and I
allow it to run on my machine"; the second says "I read this script and I am choosing not to run it here." Either
way the sentence is about what a person read, so an agent cannot truthfully say it. When tracked hooks have no
position recorded — which you will now find at the first command you run, because `init` and `change ready` ask as
well as the gate — report the state, print the refusal as it came, and name both commands. That is the whole of
your part, and it is a stop, not a detour: an agent that records a position has said something about what it read. Do not run either one yourself, and do not reach for `GIT_PAIR_HOOKS=0` to get a green answer: that
switch belongs to the person whose machine it is, for the moment their own script is misbehaving, and it is not a
position — the verdict refuses with hooks switched off, because the off switch says a machine does not want to run
scripts right now and says nothing about what those scripts do.

### For one command instead of one clone

Two flags carry the same two questions to a single invocation, so a person does not have to reach for a
process-wide switch to get through one command.

| Flag | On | What it does | What it does not do |
| --- | --- | --- | --- |
| `--no-hooks` | `change ready`, `change unready`, `change abandon`, `review submit`, `review open`, `review reopen` | runs none of them, either root, `pre-` and `post-` both; every configured hook appears as `status: "skipped-by-flag"` | does not clear the refusal for an `unrecorded` or `changed` tree — it is `GIT_PAIR_HOOKS=0` for one command, and the off switch was never a position |
| `--trust-hooks-once` | those six, plus `check` and `change integrate` | runs the tracked hooks with no position recorded, and records nothing, so the next command asks again; those hooks carry `"consent": "once"` | does not let a failing hook pass — it says these may run, not that they ran clean |

Asking for both at once is a usage error: one runs no scripts, the other runs the scripts nobody has read, and
the combination would produce a hand-off that looks checked and is not.

**Neither is an agent's flag to type.** `--no-hooks` is how a hand-off gets made with the team's suite silently
not run, which is the failure the whole feature exists to prevent; `--trust-hooks-once` executes content on the person's
machine that they have not agreed to execute. Report the refusal, print it as it came, and name both flags beside
`git pair hooks --trust` and `--decline` as what a person can do — including for the case those flags were written
for, a changeset whose own diff edits `.git-pair/hooks/`, where the submission that would record an opinion about
the scripts is the one the position refusal stops. Whether to run someone's script, or to skip yours, is their
decision, and typing the flag makes it yours. The position refusal itself names all of them — `--trust` and
`--decline` because they are the answer, `--trust-hooks-once` so the reviewer of a hook change does not have to go
looking for it — and a refusal that names a command is not an instruction to run it.

## Per-clone, and what that costs

The clone root is not shared. A check that lives only in `<git-dir>/git-pair/hooks/` runs on one machine,
which is right for a personal convenience and wrong for a team rule — the team rule is the tracked one, so it
is reviewed, versioned, and present in everybody's clone. If a check has to hold for everyone, commit it under
`.git-pair/hooks/` and have each person accept it once.

The same applies to results. Nothing here persists a hook's outcome: it is not in the marker, not in the
review commit, and not on any ref. `hooks[]` in the `--json` of the command that ran is where a result lives,
with `point`, `name`, `path`, `root`, `status`, `exit` and `duration_ms`, plus `consent` — `once` for a tracked
hook that ran on `--trust-hooks-once`, and nothing for one the clone had recorded a position on. A repository that wants its gate to
credit a run it performed records that itself, as a `pre-check` hook that writes what it observed.

## In CI

The tracked root is what CI gets, because CI checks the tree out — and it needs one line to take a position,
since a runner has recorded nothing about the hash:

```yaml
- run: git pair hooks --trust
- run: git pair check --json --default-branch origin/main | jq -e '.ready and .integrating'
```

`--trust` is the usual line, because a runner that is going to be told the suite passed has to run it. A team that
has genuinely decided not to run the tracked scripts on runners records `git pair hooks --decline` in the job
instead; what does not work is neither, and what does not count is `GIT_PAIR_HOOKS=0` standing in for either. Put
the line where the first gated command runs: a job that only calls `git pair check` needs it once, and a job that
runs `git pair init` or `change ready` needs it before those, or they refuse.

Run the hooks through `check`, not by calling the scripts: that is the one command whose exit code and whose
`ready` agree about every condition, yours included. Pass `--default-branch` in CI — a job that cloned with
`init` and one `fetch` has no recorded remote default to read, and git-pair refuses rather than guess what has
landed.
