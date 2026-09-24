# git-pair command reference for agents

Flags and shapes only. What each command is *for*, and the discipline around it, is in
[SKILL.md](SKILL.md); the landing side is in [integration.md](integration.md). `git pair <command>
--help` is authoritative for a flag list, and this file is held to the same command names as the code
by a test.

## Commands

| Command | Flags | Notes |
| --- | --- | --- |
| `init` | `--id <id>`, `--base <ref>`, `--set-base`, `--parent <branch>`, `--set-parent`, `--about <text>`, `--set-about`, `--no-commit` | creates `changesets/<id>/`, `CHANGESET.yaml`, `ABOUT.md`, commits them. Never overwrites existing content. Commits only the changeset directory, so a staged index stays staged. `--about` reads a pipe. Default base is the integration branch, recorded as its branch name where that name resolves. Refuses on the base itself, where a changeset could never hold anything. `--id` is chosen, never normalised: an id that would need rewriting is refused, and a collision refuses rather than suffixing |
| `change use` | `<id>` | records which changeset a branch carrying more than one is working on, by writing `ignores:` into that changeset's `CHANGESET.yaml`. Idempotent; refuses an id the branch does not offer |
| `change ready` | `--allow-surviving-review-additions` | fully non-interactive. Checks in order: clean working tree, `ABOUT.md` exists, the repository has commits, no blocking surviving review additions |
| `change unready` | none | withdraws the offer. Records `Review-State: working` when the changeset is in review, otherwise succeeds and records nothing. Checks only for a clean tree, since the marker it writes is empty. Refuses a changeset already recorded as integrated |
| `change abandon` | none | terminal `Review-State: abandoned`, and nothing else — no ref, since an abandoned changeset has no landing to record. `change ready`, `change unready` and `review submit` refuse against it afterwards. Idempotent |
| `change wait` | `--fetch`, `--interval <dur>` (default `10s`), `--timeout <dur>` | blocks while the changeset is `READY`, exits the moment it becomes `BLOCKED`, `FEEDBACK` or `APPROVED`. Read-only. `--fetch` runs `git fetch` before each round and also evaluates the branch's remote-tracking ref, so a review pushed from another clone ends the wait. Without `--timeout` it waits indefinitely. Exit 1 on timeout, and 1 (not a wait) on a `WORKING` changeset |
| `change feedback` | `--stat`, `--name-only`, `--changeset <slug>` | the most recent submission as a diff (`review^..review`). No JSON output |
| `status` | `--changeset <slug>`, `--fetch` | derived state, reason, `next_action`. Reads any changeset by slug, from whichever branch carries it, falling back to the durable refs |
| `queue` | `--fetch` | one row per branch whose changeset is `READY`, longest wait first, plus `LANDED, UNRECORDED` and `RECORDED, NOT PUBLISHED` findings. Read from the repository, not the checkout |
| `diff [path...]` | `--unreviewed`, `--since-review[=N]`, `--base-review[=N]`, `--base-commit`, `--base-ref`, `--head-review[=N]`, `--head-commit`, `--head-ref`, `--stat`, `--tool` | no JSON output. Paths are checked against the span first, so a typo is an error rather than an empty diff |
| `check` | `--allow-feedback`, `--fetch` | the integration gate. `--allow-feedback` is where a repository states that a non-blocking review is enough; there is no config key for it, because the command that runs the gate is the only place the policy is known |
| `review history` | `--changeset <slug>` | review submissions only, indexed from `0`, each naming the commit it reviewed under `REVIEWED` |
| `review open` | the span flags above | the review screen. Needs a terminal; refuses with exit 2 under an agent |
| `review reopen` | none | the screen on `<last review>..current`. Needs a terminal; refuses if no review exists |
| `review about` | none | `ABOUT.md` in the editor, creating it if missing. Needs a terminal |
| `review thread` | `[title...]` | creates or reopens a thread file under `changesets/<id>/`, then opens it. Needs a terminal — create and edit the file directly instead |
| `review submit` | one of `--block`/`--feedback`/`--approve`, `-m/--message <text>`, `--no-stage` | **the reviewer's command; never run it as the author.** Stages the whole tree by default, commits (empty commits allowed), writes nothing else: a submission is a marker commit, not a ref move. The commit names what it reviewed with `Review-Head`, which is what lets `check` refuse a rewritten history |
| `integration record` | `--source <sha>`, `--commit <sha>`, `--target <ref>`, `--changeset <id>`, `--allow-feedback`, `--configure-fetch` | **not an agent command.** The only command in git-pair that writes a ref: the create-only pair `refs/git-pair/archive/<id>` and `refs/git-pair/integrations/<id>`, after verifying the verdict and the landing. Re-running it with the same pair succeeds and changes nothing |
| `integration publish` | `[<changeset>…]`, `--remote <name>` | **not an agent command.** Sends a changeset's two refs to the shared remote, unforced — the only git-pair command that pushes, and the only thing it may push is `refs/git-pair/*`. Idempotent |
| `skill list` | none | the skill compiled into this binary, and every directory a harness would read it from, marked `current`, `stale`, `absent` or `unavailable`. Read-only |
| `skill show` | `[path]` | prints the compiled-in `SKILL.md`, or a file inside the skill such as `references/cli.md`. No JSON output |

`review` with no subcommand is `review open` and takes the same flags.

## Flags every command takes

- `--json` — machine-readable output where supported. `change feedback`, `diff` and `skill show` have
  none, and say so on stderr rather than printing nothing.
- `--default-branch <ref>` — the integration branch that "has this landed?" is measured against.
  Without it git-pair reads git's own answer (`refs/remotes/origin/HEAD`, then a sole `origin/main` or
  `origin/master`, then a local `main` or `master`) and refuses if there is nothing to compare against.
  CI passes this: a job that cloned with `init` and one `fetch` has no recorded remote default.
- `--changeset <slug>` — on `status`, `review history` and `change feedback` only. A marker is a commit,
  and a commit lands wherever `HEAD` is, so nothing that records one takes the flag.

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
| 1 | a git-pair rule or the repository state refused the operation | surviving additions; dirty working tree; missing `ABOUT.md`; `check` printing `NOT READY:`; `change wait` timing out or finding a `WORKING` changeset; `integration record` verifying nothing |
| 2 | usage error | unknown flag, command or subcommand; `no changeset for this branch`; detached HEAD; more than one changeset and none named with `--changeset`; `cannot tell which branch is the integration branch`; `--fetch` with no remote configured; a path outside the span; editor or TUI commands without a terminal |
| 3 | the repository or git itself failed | `not a git repository`; a git subprocess failing for a reason other than an unresolvable revision |

Exit 1 says the invocation was right and the repository said no: fix the state and retry. Exit 2 says the
invocation was wrong: retrying unchanged fails again.

## JSON

Output is indented two spaces. An array is `[]` for "asked, and none", never null; a missing key means
the build did not ask. Two fields answer null on purpose, and neither is a list: `status`'s
`latest_review` (no object before the first review) and `uncommitted` (the question belongs to a checkout
this command does not stand in).

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
  "archive_ref": "",
  "archive_commit": "",
  "uncommitted": false,
  "abandoned": false,
  "integrated": false,
  "reviews": 0,
  "reason": "marked ready by 8065dae",
  "span": "main...current",
  "next_action": "waiting for a reviewer: `git pair review open` (author: `git pair change wait` to block on it)"
}
```

Once a review exists, `latest_review` is `{"index": 0, "outcome": "block", "commit": "332887c",
"reviewed_head": "1a2b3c4"}`. `abandoned`, `integrated`, `archive_ref`, `archive_commit`,
`integrated_commit` and `integration_ref` are facts beside the state, not extra state values: the state
stays `WORKING` after `change abandon`, and untouched by a record.

`git pair check --json` carries the verdict in `ready` and exits 0 either way, so a gate asks `jq`
rather than `$?`. `reasons` names every failed condition, empty when it passed; `policy` is
`approve-only` or `approve-or-feedback`; `next_action` appears only when `ready` is true.

```json
{
  "changeset": "feat",
  "ready": false,
  "state": "APPROVED",
  "head": "941266b18686624cb624722b4e8c348bf03451a7",
  "reviewed_head": "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
  "reasons": ["content outside changesets/feat/ changed since 1a2b3c4: src/service.ts"],
  "policy": "approve-only",
  "integrated": false
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
  "next_action": "`git pair change feedback`; feedback is non-blocking, `git pair check`, then merge into main with ordinary git, then `git pair integration record`"
}
```

`git pair queue --json` — `ready_for_review` (full SHAs in `head` and `ready_commit`), `skipped` for
changesets the queue cannot explain, `parent_notes` for a READY child whose parent has moved or landed,
and `landed_unrecorded` carrying the `integration record` invocation that closes each gap.

`git pair change ready --json` returns the `status` fields plus `ready_commit`,
`review_queue_visible`, `acknowledged_survivors` and `surviving_review_artifacts`.
`git pair change unready --json` reports `was` and `state`, with `recorded` false when there was nothing
to withdraw. `git pair review history --json` lists the submissions with `index`, `outcome`, `sha`,
`short`, `reviewed_head`, `subject`, `author`, `when`, `age`.
