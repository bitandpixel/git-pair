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
| `clone` | `<git-dir>/git-pair/hooks/<point>/` | your own checks, on your own machine | none — it is your file, never committed |

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
| `GIT_PAIR_HOOKS` | `1`, inherited by anything the hook runs — a nested `git pair` runs no hooks |

The submission points add three, because a verdict has content of its own:

| Variable | Meaning |
| --- | --- |
| `GIT_PAIR_REVIEW_OUTCOME` | `approve`, `block`, or `feedback` |
| `GIT_PAIR_REVIEW_HEAD` | the commit being reviewed |
| `GIT_PAIR_REVIEW_BODY_FILE` | a file holding the reviewer's body; git-pair deletes it when the command returns, so copy it if you mean to keep it |

A hook's stdout and stderr never reach git-pair's stdout — that is where `--json` lives. A failing hook's
output is replayed on stderr with the refusal, because that is the text the author needs next to the word
"failed". A passing hook's output is replayed only when `GIT_PAIR_HOOK_OUTPUT=1`, so a suite that prints four
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
clone them. git-pair's tracked hooks are committable because consent is taken by content: the path, mode and
bytes of the tracked hooks tree are hashed, and the hash is recorded per clone in
`<git-dir>/git-pair/trusted-hooks`.

An unaccepted tree does not run at all. The operation proceeds and the skipped hooks are named on stderr, so a
clone is never surprised by an unreviewed script and never silently left without the checks it thinks it has.
Editing a hook changes the hash, so an edit after acceptance is a new acceptance.

**`trusted: false` carries two different facts, and `trust_state` tells them apart.** `unrecorded` means nobody
on this clone has ever accepted the tree — they have not looked. `changed` means this clone accepted a version
and the repository has since shipped another — the team moved a check underneath them. Act on them differently:
the first is "read these", the second is "read what moved, then decide", and the answer prints the hash that
was approved beside the tree's current one so the comparison is against something you saw. Accepting whatever is
there because the note asked you to is how a repository ends up running a check nobody read.

A clone sitting on `changed` has a third option besides accepting and working without the check: copy the
version it did read into the per-clone root, which runs with no consent step. That is the vendored model
applied by one person for one point, and it is the honest way to keep working while the team's new version
awaits you.

```bash
git pair hooks            # what is configured, both roots, executable or not
git pair hooks --json     # + tracked_root, clone_root, trusted, trust_state, accepted, tree_hash, hooks[], unknown[]
git pair hooks --trust    # a person's act: records the tracked tree as accepted, printing names and hash
```

**`--trust` is a person's decision, not an agent's.** It says "I read this script and I allow it to run on my
machine." If tracked hooks are unaccepted, report that and name the command; do not run it yourself. The same
applies to `GIT_PAIR_HOOKS=0`: that switch belongs to the person whose machine it is, for the moment their own
script is misbehaving. An agent who sets it has removed the check the repository put there, which is the
opposite of the work.

## Per-clone, and what that costs

The clone root is not shared. A check that lives only in `<git-dir>/git-pair/hooks/` runs on one machine,
which is right for a personal convenience and wrong for a team rule — the team rule is the tracked one, so it
is reviewed, versioned, and present in everybody's clone. If a check has to hold for everyone, commit it under
`.git-pair/hooks/` and have each person accept it once.

The same applies to results. Nothing here persists a hook's outcome: it is not in the marker, not in the
review commit, and not on any ref. `hooks[]` in the `--json` of the command that ran is where a result lives,
with `point`, `name`, `path`, `root`, `status`, `exit` and `duration_ms`. A repository that wants its gate to
credit a run it performed records that itself, as a `pre-check` hook that writes what it observed.

## In CI

The tracked root is what CI gets, because CI checks the tree out — and it needs one line of consent, since a
runner has never accepted the hash:

```yaml
- run: git pair hooks --trust
- run: git pair check --json --default-branch origin/main | jq -e '.ready and .integrating'
```

Run the hooks through `check`, not by calling the scripts: that is the one command whose exit code and whose
`ready` agree about every condition, yours included. Pass `--default-branch` in CI — a job that cloned with
`init` and one `fetch` has no recorded remote default to read, and git-pair refuses rather than guess what has
landed.
