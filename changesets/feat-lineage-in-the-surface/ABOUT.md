# feat-lineage-in-the-surface

## Summary

Three surfaces said something true and left out the thing that made it true. `integration record` refused a
commit that carried the record it was being asked for; `status` of a recorded child said the work was
integrated and kept quiet about the parent it was stacked on, so a child's integration was invisible when
its parent's was not; and `status --changeset <id>` of a landed changeset reported `WORKING`, `no commits
above the base yet`, `Latest review: none yet`, and told the reader to run the command whose evidence was on
the screen.

This changeset teaches the recorder to read a commit that carries the record as the record landing, prints
the chain a recorded changeset was stacked on, and makes a record read answer as a record read. It comes from
the two landing mistakes David made on 2026-09-22, both at the same moment, both recorded in the plan: the
first was a backport to the wrong branch, which the recorder correctly refused, and the second was recording
the child at trunk's head, which made the child look integrated while its parent's integration stayed
invisible.

## What changed

**A commit that carries the record is not a second landing** (`internal/cli/integration.go`). The durable
write stays create-only. The refusal is now conditioned on what the asked-for commit contains: when the
integration ref already exists, its archive is the same `--source`, the asked commit is a descendant of the
recorded integration, and that commit's *first parent* is not already a descendant of it, the command
succeeds, reports the ref it did not write, and writes nothing. A backport of the same commit onto the same
branch keeps its first parent on that branch, so it stays the refusal it was — the distinction is whether the
commit was made to carry the record or merely contains it. The archive is not re-verified on that path: the
ref already names it, so it is not what the call is deciding, and re-verifying would make a changeset
unrecordable-after-the-fact if its source were rewritten later. `--json` gained `carried_by`.

**`status` prints the chain a recorded changeset was stacked on** (`internal/cli/status.go`). For a recorded
changeset, one line per ancestor, nearest first, read from `parent-changeset:` through each ancestor's own
record: the commit the record holds, whether that commit is in the integration branch's history, and whether
the branch the ancestor was stacked on is still in this clone. An ancestor with no record here is printed as
absent rather than skipped — that is the finding, and `--fetch` is the answer to it. The walk stops at eight
steps or a repeated id, each with its own `stack_note`. `--json` reports the walk as `stack` (never null)
plus `stack_note`. It is one `Stack:` section, not two: `parent:` was the stack as the approval was recorded
against it, and the records are the stack as this clone can still read it.

**A record read reads like a record** (`internal/lifecycle/lifecycle.go`, `internal/cli/root.go`,
`internal/cli/status.go`). The stale "(`git pair integration record`)" hint is gone from the `Integrated:`
block — it was unreachable anyway, set exactly where the ref was known to exist. The reviews a record read
reports come from `lifecycle.ReviewsInLineage`, the whole archived ancestry, because after a merge landing
the archived head sits *below* the base and `base..head` is empty for exactly the changeset whose verdicts
matter most. `reason` says the read was of the durable refs instead of reciting an empty span. `state` is
untouched, so `status` and `review history` agree and PRD §13.4's rule holds.

## Design decisions

**Create-only is kept, not relaxed.** The rule is what makes a record trustworthy as an answer to "where did
this land", and §13.4's fetch carries no `+` for the same reason. What changed is which situations the rule
is asked about, not what it does. The interpretation lives in the CLI read where the destination is known;
`reviewref.Conflict` and `CreateOnly` are untouched.

**Descent alone would have been too weak.** The plan predicted this and the two pre-existing backport tests
proved it — `TestIntegrationRecordIsCreatedOnce` and `TestIntegrationRecordRefusesACommitAheadOfTheLanding`
both build a backport onto the same branch and expect refusal, and every commit added to a branch is a
descendant of what was recorded there. The first-parent condition is what separates the two cases, and it is the same question
`deriveLanding` asks.

**The archive is not a second source of state.** The record read fills reviews but not state. Walking the
whole lineage would have made a landed child report `READY`, which is worse than the `WORKING` it replaces:
`state` is the field markers move, and no marker moved. The split is documented in PRD §13.4 and pinned by
`TestStatusOfARecordReadKeepsStateWithTheSpan`.

**The chain reads records, not branches.** `reviewref.List` once, then one small `git show` per step. Never
`refs/remotes/**` (§13), never the network unless `--fetch` was passed. An ancestor's record missing here is
reported rather than fetched or guessed, because the pair is what makes a claim answerable, and a chain read
from a half-populated namespace is a true report of a half-populated namespace.

**A `Stack:` section that mixes two kinds of line** was judged against splitting it in two. `check`'s
`Stack:` is one section with one kind of line and is readable; two sections would mean a reader comparing an
approval's stack against a record's stack has to hold both on screen. The two kinds are also about different
things on purpose — a landed parent has no branch tip to compare an approval against, and a moved tip is not
a landed parent — so they answer different halves of "is the stack I rest on still there" and print together
under the heading that already existed.

Two fixes fell out of dogfooding against the real repository rather than out of the plan:

- The record-read path built its base straight from `parent:`, so `status --changeset <id>` **refused
  outright** once the parent branch had been tidied away: `cannot resolve changeset base "alpha": unknown
  revision`. Reading a landed child after tidy is the normal case, so the read relinks the base to the
  parent's integration ref exactly as `changeset.relinkStacks` does on the branch path — kept as a ref rather
  than resolved to a sha, so the provenance stays visible in `Base:`, with the branch name still in
  `ParentBranch` so "landed" and "merely moved" stay tellable apart.
- `refIndex` knew ids and commits but not the refs holding them, and the retired layout spells its ref
  differently than its id would suggest. Naming a ref that does not exist is worse than naming none, so the
  index gained `IntegratedRef` and the chain names the ref it read.

## Validation

`mise run check`, `e2e-29.sh` (`E2E: all checks passed`), `pty-walkthrough.sh` (`PTY: all checks passed`).

Tests: `internal/cli/integration_carried_test.go` (carrying succeeds with no write and says so; a backport
onto the same branch still refuses; a mismatched archive is still a conflict);
`internal/cli/status_stack_chain_test.go` (the two-deep chain in both surfaces, order and commits and
`branch_exists` and `in_default_branch`; a deleted parent branch; an ancestor with no record here; a cycle;
an unstacked changeset printing nothing while `stack` stays `[]`; and the walk's cost — four invocations for
a third ancestor, pinned at ≤5 so a per-step namespace read or a chain walked twice fails the build);
`internal/cli/status_record_read_test.go` (the archived verdict reported, `none yet` gone, the record-it hint
gone, `review history` agreeing, and `state` staying `WORKING` beside `integrated: true`).

Against the real repository, `git pair status --changeset feat-publish-the-records` went from `WORKING` /
`no commits above the base yet` / `Latest review: none yet` / "run `git pair integration record`" to a reason
naming the read, the approve marker at `6e1200c` with its age and reviewed head, no hint, and the parent
chain line `record: feat-two-frozen-refs (branch feat/two-frozen-refs) -> ba921b0, reachable from
origin/main`. PRD §11.4, §13.4, §21 and README's `integration record` and status prose move in the same
commits as the behaviour.

## Known limitations

- The chain is capped at eight steps and stops at a repeated id. A stack deeper than eight prints what it
  read plus a note; the cap is about not printing twenty lines, not about cost.
- `stack` reports what this clone's namespace holds. A CI job that has not fetched `refs/git-pair/*` sees
  every ancestor as "no record in this clone", which is true and is what §13.4's fetch fixes.
- The reach phrase is shared by wording, not by code: `landing.reach()` needs a `landing` value the status
  assembly does not build, so the chain phrases containment itself with the same literal.
- Only the two surfaces that take `--changeset <id>` were audited for the record-read mistake. `check` is
  branch-bound and takes no `--changeset`, so it cannot make it; `queue` reads the durable refs directly.

## Open questions

- Should `queue`'s `LANDED, UNRECORDED` section gain the same chain for a stacked landing? It is the same
  fact, asked at the moment the record is still cheap to write, and it is out of scope here.
- The `fix-exit-code-wording` changeset is in trunk with no record, so the queue keeps printing the command
  to write one. Whether a landed changeset with no record should also print its chain is a question for that
  record.
