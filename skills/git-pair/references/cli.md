# git-pair command reference for agents

Flags and shapes only. What each command is *for*, and the discipline around it, is in
[SKILL.md](SKILL.md); the landing side is in [integration.md](integration.md). `git pair <command>
--help` is authoritative for a flag list, and this file is held to the same command names as the code
by a test.

## Commands

| Command | Flags | Notes |
| --- | --- | --- |
| `init` | `--id <id>`, `--base <ref>`, `--set-base`, `--parent <branch>`, `--set-parent`, `--about <text>`, `--set-about`, `--no-commit` | creates `changesets/<id>/`, `CHANGESET.yaml`, `ABOUT.md`, commits them. Never overwrites existing content. Commits only the changeset directory, so a staged index stays staged. `--about` reads a pipe. Default base is the integration branch, recorded as its branch name where that name resolves. Refuses on the base itself, where a changeset could never hold anything. `--id` is chosen, never normalised: an id that would need rewriting is refused, and a collision refuses rather than suffixing |
| `change stack` | `--base <branch>` | records that this branch's changeset is stacked on the one <branch> carries: writes `base:` and `base-changeset:` into the child's `CHANGESET.yaml` and nowhere else, then says to offer the branch again. Refuses when the two branches share no changeset, when the level below cannot be told, or when this branch carries two changesets of its own |
| `change combine` | `--into <id>`, `--threads` | folds one changeset into another when the two are one piece of work: the disappeared directory moves whole to `changesets/<into>/.combined/<id>/`, inert to every reader; the survivor keeps its own `base:` and `base-changeset:` and gains one line pointing at the archive. Refuses while either changeset is offered or under review |
| `change ready` | `--allow-surviving-review-additions` | fully non-interactive. Checks in order: clean working tree, `ABOUT.md` exists, the repository has commits, no blocking surviving review additions |
| `change integrate` | `--allow-feedback` | declares this approved head ready to be merged: one empty `Review-State: integrating` marker naming the head with `Review-Head`, and nothing else — no ref, no push, no merge. Runs `check`'s gate first and names every failed condition, so it cannot be made for work the gate would refuse. Refuses a stacked child whose parent has not landed. Idempotent at the head it declared; a commit after it is declared next time. **Not a verdict:** the approval underneath is what permits the merge |
| `change unready` | none | withdraws the offer. Records `Review-State: working` when the changeset is in review — `READY`, `APPROVED`, `FEEDBACK`, or `INTEGRATING`, which is how a merge request is taken back — otherwise succeeds and records nothing. Checks only for a clean tree, since the marker it writes is empty. Refuses a changeset already recorded as integrated |
| `change abandon` | none | terminal `Review-State: abandoned`, and nothing else. `change ready`, `change unready` and `review submit` refuse against it afterwards. Idempotent |
| `change tidy` | `[<id>…]`, `--all-landed`, `--dry-run` | moves a landed changeset's directory to `changesets/.landed/<id>/` and commits the move, so the active listing holds work in progress and nothing else. Refuses a dirty tree and refuses an id the destination does not carry. `--all-landed` takes the ids the branch carries instead of the arguments; `--dry-run` says what would move and commits nothing |
| `change wait` | `--fetch`, `--interval <dur>` (default `10s`), `--timeout <dur>` | blocks while the changeset is `READY`, exits the moment it becomes `BLOCKED`, `FEEDBACK` or `APPROVED`. Read-only. `--fetch` runs `git fetch` before each round and also evaluates the branch's remote-tracking ref, so a review pushed from another clone ends the wait. Without `--timeout` it waits indefinitely. Exit 1 on timeout, and 1 (not a wait) on a `WORKING` changeset |
| `change feedback` | `--stat`, `--name-only`, `--changeset <slug>` | the most recent submission as a diff (`review^..review`). No JSON output |
| `change abandon` | none | terminal `Review-State: abandoned`, and nothing else. `change ready`, `change unready` and `review submit` refuse against it afterwards. Idempotent |
| `change tidy [<id>...]` | `--all-landed`, `--dry-run`, `--json` | `git pair change tidy` moves a landed changeset's directory from `changesets/<id>/` to `changesets/.landed/<id>/` as one commit of renames on this branch — no ref, no push. Refuses an id that has not landed, one a live changeset is stacked on, a dirty tree, and an id this branch does not carry; nothing moves on a refusal. `--dry-run` commits nothing, `--json` carries `changesets[]` (`id`, `from`, `to`, `action`), `commit`, `dry_run`, `destination`. A second run is a no-op naming the reason |
| `status` | `--changeset <slug>` | derived state, reason, `next_action`, and the landing (`landed`, `landed_commit`, `landed_branch`, `chain_base`, `chain_head`, `reviewed`, `stack`). Reads any changeset by slug, from whichever branch carries it, the destination's tree included |
| `queue` | none | one row per branch whose changeset is `READY`, longest wait first, plus `LANDED UNREVIEWED` findings, plus `AWAITING INTEGRATION` / `awaiting_integration` for approved work whose author asked for the merge. Read from the repository, not the checkout |
| `diff [path...]` | `--unreviewed`, `--since-review[=N]`, `--base-review[=N]`, `--base-commit`, `--base-ref`, `--head-review[=N]`, `--head-commit`, `--head-ref`, `--stat`, `--tool` | no JSON output. Paths are checked against the span first, so a typo is an error rather than an empty diff |
| `check` | `--allow-feedback` | the integration gate. `--allow-feedback` is where a repository states that a non-blocking review is enough; there is no config key for it, because the command that runs the gate is the only place the policy is known. Reports `integrating` beside the verdict, so an automatic merge gates on one command: `jq -e '.ready and .integrating'` |
| `review history` | `--changeset <slug>` | review submissions only, indexed from `0`, each naming the commit it reviewed under `REVIEWED` |
| `review open` | the span flags above | the review screen. Needs a terminal; refuses with exit 2 under an agent |
| `review reopen` | none | the screen on `<last review>..current`. Needs a terminal; refuses if no review exists |
| `review about` | none | `ABOUT.md` in the editor, creating it if missing. Needs a terminal |
| `review thread` | `[title...]` | creates or reopens a thread file under `changesets/<id>/`, then opens it. Needs a terminal — create and edit the file directly instead |
| `review submit` | one of `--block`/`--feedback`/`--approve`, `-m/--message <text>`, `--no-stage` | **the reviewer's command; never run it as the author.** Stages the whole tree by default, commits (empty commits allowed), writes nothing else: a submission is a marker commit, not a ref move. The commit names what it reviewed with `Review-Head`, which is what lets `check` refuse a rewritten history |
| `skill list` | none | the skill compiled into this binary, and every directory a harness would read it from, marked `current`, `stale`, `absent` or `unavailable`. Read-only |
| `skill show` | `[path]` | prints the compiled-in `SKILL.md`, or a file inside the skill such as `references/cli.md`. No JSON output |
| `skill install` | `--harness agents\|pi\|claude`, `--scope repo\|user`, `--dest <dir>`, `--dry-run`, `--force` | writes the compiled-in skill into a skills directory as `git-pair/`. Files that already match are left alone, files that differ are refused without `--force`, and files git-pair did not write are reported and kept. `--dest` names the directory outright and cannot be combined with `--harness` or `--scope`. See [installing-the-skill.md](installing-the-skill.md) |
| `skill agents-md` | none | prints the pointer stanza for `AGENTS.md` or `CLAUDE.md`, for a harness with no skill discovery. No JSON output |

`review` with no subcommand is `review open` and takes the same flags.

## Flags every command takes

- `--json` — machine-readable output where supported. `change feedback`, `diff`, `skill show` and
  `skill agents-md` have none, and say so on stderr rather than printing nothing.
- `--default-branch <ref>` — the integration branch that "has this landed?" is measured against.
  Without it git-pair reads git's own answer (`refs/remotes/origin/HEAD`, then a sole `origin/main` or
  `origin/master`, then a local `main` or `master`) and refuses if there is nothing to compare against.
  CI passes this: a job that cloned with `init` and one `fetch` has no recorded remote default.
- `--changeset <slug>` — on `status`, `review history` and `change feedback` only. A marker is a commit,
  and a commit lands wherever `HEAD` is, so nothing that records one takes the flag.
- `--no-cache` — derive everything from git again, ignoring the local caches git-pair keeps under the
  repository's git directory. It is not a correctness switch: a cached fact is keyed on the commit ids it was
  derived from, and a key that no longer matches is a miss rather than a stale answer. Use it to measure a
  slow command against a fast one, to rule the cache out while debugging, or on a machine that wants nothing
  written under its git directory. `GIT_PAIR_NO_CACHE` set to anything non-empty is the same thing for every
  invocation in a process environment, which is the form a CI job can reach without threading the flag
  through each call.

## Span flags

`--base-*` and `--head-*` name the two ends of the diff you want: `<name>-review[=N]` counts review
submissions from the newest, `--base-commit` / `--head-commit` name a commit, `--base-ref` / `--head-ref`
name a branch tip. `--unreviewed` is `<last review>..current`, and `--since-review[=N]` is the same idea
counted from a submission further back. `diff` and `review open` take all of them; a `--head-*` flag
opens a historical span, which is read-only.

## Exit codes

| Code | Meaning | Seen as |
| --- | --- | --- |
| 0 | success | — |
| 1 | a git-pair rule or the repository state refused the operation | surviving additions; dirty working tree; missing `ABOUT.md`; `check` printing `NOT READY:`; `change integrate` refusing work the gate would refuse, or a child whose parent has not landed; `change wait` timing out or finding a `WORKING` changeset; `change tidy` naming a changeset the destination does not carry; `skill install` meeting a file that differs, with no `--force` |
| 2 | usage error | unknown flag, command or subcommand; `no changeset for this branch`; detached HEAD; more than one changeset and none named with `--changeset`; `cannot tell which branch is the integration branch`; a path outside the span; editor or TUI commands without a terminal; `skill install` with an unknown `--harness` or `--scope`, or with `--dest` alongside either |
| 3 | the repository or git itself failed | `not a git repository`; a git subprocess failing for a reason other than an unresolvable revision |

Exit 1 says the invocation was right and the repository said no: fix the state and retry. Exit 2 says the
invocation was wrong: retrying unchanged fails again.

## JSON

Output is indented two spaces. An array is `[]` for "asked, and none", never null; a missing key means
the build did not ask. Two fields answer null on purpose, and neither is a list: `status`'s
`latest_review` (no object before the first review) and `uncommitted` (the question belongs to a checkout
this command does not stand in). The examples below name the keys an agent branches on, not every key the
command emits — README's JSON contracts is the complete shape, and a field missing here says nothing about
whether it exists.

`git pair status --json`, before the first review:

```json
{
  "changeset": "booking-transaction",
  "branch": "booking-transaction",
  "base": "main",
  "default_branch": "main",
  "default_branch_commit": "9c41f0b",
  "default_branch_source": "sole-candidate",
  "state": "READY",
  "head": "8065dae",
  "head_full": "8065dae53c0475596bfc174927075895d9fb8b76",
  "latest_review": null,
  "landed": false,
  "landed_commit": "",
  "landed_branch": "",
  "chain_base": "",
  "chain_head": "",
  "reviewed": false,
  "uncommitted": false,
  "abandoned": false,
  "reviews": 0,
  "reason": "marked ready by 8065dae",
  "span": "main...current",
  "stack": [],
  "next_action": "waiting for a reviewer: `git pair review open` (author: `git pair change wait` to block on it)"
}
```

Once a review exists, `latest_review` is `{"index": 0, "outcome": "block", "commit": "332887c",
"reviewed_head": "1a2b3c4"}`. `abandoned` and `abandoned_commit` are facts beside the state, not extra
state values: the state stays `WORKING` after `change abandon`. `landed`, `landed_commit` and
`landed_branch` are the destination's answer — the branch carrying `changesets/<id>/` is what makes a
changeset landed — and `chain_base`, `chain_head` and `reviewed` say what history came with it, which a
squash landing leaves empty and false. `integrating` and `integrate_commit` are the exception that is a
state: `INTEGRATING` while a declaration is the newest marker.

`git pair check --json` carries the verdict in `ready` and exits 0 either way, so a gate asks `jq`
rather than `$?`. `reasons` names every failed condition, empty when it passed; `policy` is
`approve-only` or `approve-or-feedback`; `next_action` appears only when `ready` is true. `integrating`
is the separate question of whether the author asked for this merge — true only while the declaration is
the newest marker, so a re-offer or a re-review stops an automatic merge without a rebase.

```json
{
  "changeset": "feat",
  "ready": false,
  "state": "APPROVED",
  "head": "941266b18686624cb624722b4e8c348bf03451a7",
  "reviewed_head": "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
  "reasons": ["content outside changesets/feat/ changed since 1a2b3c4: src/service.ts"],
  "policy": "approve-only",
  "integrating": false
}
```

`git pair change wait --json` — `ref` is where the activity appeared (`HEAD`, or a remote-tracking
branch brought in by `--fetch` but not yet merged), `review_commit` and `review_commit_full` appear only
when a submission ended the wait, and `timed_out` is the difference between "a reviewer acted" and "gave
up". A timeout exits 1, an answered wait exits 0.

```json
{
  "changeset": "booking-transaction",
  "previous_state": "READY",
  "state": "FEEDBACK",
  "review_commit": "332887c",
  "review_commit_full": "332887cf6413e66d45d0ad5d59d93f7d30484ffd",
  "ref": "HEAD",
  "fetches": 3,
  "waited_seconds": 91,
  "timed_out": false,
  "next_action": "`git pair change feedback`; feedback is non-blocking, `git pair check`, then merge into main with ordinary git"
}
```

`git pair queue --json` — `ready_for_review` (full SHAs in `head` and `ready_commit`),
`awaiting_integration` (approved work handed over for the merge, with `integrate_commit`, `declared_age`
and the `destination` somebody would merge into), `skipped` for
changesets the queue cannot explain, `parent_notes` for a READY child whose parent has moved or landed,
and `landed_unreviewed` for a landing whose chain carries no approving verdict — a finding no command
closes, so each row says what the chain said (`changeset`, `commit`, `chain`, `reason`).

`git pair change ready --json` prints `changeset`, `branch`, `base`, `state`, `head`, `ready_commit`,
`review_queue_visible` and `acknowledged_survivors`; `surviving_review_artifacts` appears only when a
surviving-additions report existed.
`git pair change integrate --json` prints the same identity fields plus `recorded` (false when this head
was already declared), `integrate_commit`, `destination` with `destination_source` (`base`, `parent`,
`default`), `destination_via`, `destination_unreachable`, `reasons` and `next_action`.
`git pair change unready --json` reports `was` and `state`, with `recorded` false when there was nothing
to withdraw. `git pair review history --json` lists the submissions with `index`, `outcome`, `sha`,
`short`, `reviewed_head`, `subject`, `author`, `when`, `age`.
