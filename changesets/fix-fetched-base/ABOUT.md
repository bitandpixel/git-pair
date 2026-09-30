# fix-fetched-base

## Summary

A changeset merged on the remote, and `git pair review` still showed a diff built on a trunk three weeks
old. The recorded `base: main` is the whole reason: a bare name resolves under `refs/heads/` first, and in
the ordinary clone that branch is where trunk stood the day the branch was cut. `git fetch` moves
`refs/remotes/origin/main` and leaves `refs/heads/main` alone — updating a branch nobody has checked out is
not what fetch does. So every surface that measures — `diff`, the review span, `status`'s `Span:` line —
measured from a commit the rest of the product had already stopped calling trunk.

Reproduced in a scratch clone with a remote: a third party merges work into `main`, `git fetch`, rebase the
changeset onto the fetched trunk, and the diff gains their file.

```
local main e0b2ca4   origin/main 66c25c4
merge-base main        HEAD = e0b2ca4
merge-base origin/main HEAD = 66c25c4
git pair diff ->  a.txt  b.txt  changesets/demo/*     # b.txt is not this changeset's work
```

`BaseFor` already answered with the fetched ref for a stack whose parent landed, and `DefaultBranch` already
prefers it for the landing test (§13). An unstacked changeset — every changeset that is not stacked — was the
case left out, which is the one that most people have.

## What changed

- `internal/changeset/base.go` — `BaseFor` rule 1 answers with the integration branch's fetched copy when the
  base names that branch, and `MeasureBase` is the same answer for a caller that pins a commit and has nowhere
  to put the rule behind it.
- `internal/changeset/resolve.go` — `DefaultBranchRef.Fetched()`, which is the one fact the rule reads.
- `internal/cli/root.go`, `diff.go`, `review.go`, `status.go`, `internal/tui/session.go` — the six span
  resolutions go through `MeasureBase`, so the range a reviewer reads and the base printed above it are one
  answer.
- `internal/cli/status.go` — `Base:` names the copy it measured against, through the `base_ref`/`base_why`
  fields that already carried that job for derived bases. `base` itself stays the record.
- `internal/span/span.go` — the changeset-base endpoint of a span label prints through `ShortRef`, so the
  screen says `origin/main...current` rather than `refs/remotes/origin/main...current`.
- `internal/cli/init.go` — `baseDivergence`'s sentence, which claimed the opposite ("the diff git-pair
  measures is against the copy in this clone").
- `PRD.md` §4 and §11.1 — the rule, and the two `status --json` fields it now populates.

## Design decisions

**Resolution changes; the record does not.** `base: main` stays what `init` writes. `CHANGESET.yaml` is
committed content other machines read, a clone may hold trunk only under `refs/heads/` or only under the fetch
root, and CI passes `--default-branch`. Writing a per-clone ref into the file makes the answer depend on which
machine ran `init`, which is the failure PRD §4 already records. What a diff is measured against is a
per-clone question and belongs in resolution, where it can follow what this clone has fetched.

**Only the integration branch switches, and `Fetched()` is the guard.** A stack's parent branch is the other
answer rule 1 gives, and preferring its remote copy would be backwards: that work is written on a branch of
this clone, where the local branch is ahead of anything pushed. The condition is therefore "the base names the
integration branch *and* this clone reaches it through a fetch ref". Where there is no fetch ref — a local-only
repository, or a clone that has never fetched — the recorded name is the whole answer and nothing changes.

**Measured from a fetch ref even when both copies are the same commit.** The alternative is a rule that
changes what `status` prints the moment someone fetches, which makes the same branch answer two ways depending
on how recently a command touched the network. One stable answer, named on screen, is the better trade.

**Lifecycle's marker range stays on the recorded base.** `lifecycle.Summarize(slug, base, head)` asks which
commits in this branch's history carry markers, not what the diff contains. A wider range can only find more
markers; narrowing it to the fetched trunk would let a marker disappear from `state` because of what this clone
happened to fetch. The two questions are different, and only the measuring one moved.

**`status` prints both, `--json` keeps `base` as the record.** `base_ref` and `base_why` already existed for
derived bases and already carried the sentence, so the new case rides the same fields rather than inventing a
third spelling of the base anywhere.

## Validation

The scenario above, run against a scratch clone with a remote before and after (the same repository, two
binaries):

```text
# before
Base: main                       Span: main...current
git pair diff  ->  a.txt  b.txt  changesets/demo/*
# after
Base: origin/main — the base names the integration branch, so the copy of it this clone has fetched
Span: origin/main...current
git pair diff  ->  a.txt  changesets/demo/*
```

`status --json` beside it: `base` stays `main`, `base_ref` is `refs/remotes/origin/main`, `base_why` carries
the sentence, and `span` reads `origin/main...current`.

- `internal/changeset/base_test.go` — the rebase case end to end (`changedBetween` asserts the *files*, not the
  ref string), the same repository with no fetch ref, a stack that keeps its parent branch when trunk is
  fetched, and `MeasureBase` against `BaseFor` plus both fallbacks.
- `internal/span/span_test.go` — the label, and that the checkpoint still keeps the qualified ref.
- `internal/cli/fetched_base_test.go` — `diff` and `status` against the scratch scenario, including that
  `CHANGESET.yaml` still holds no fetch ref and that a clone with one copy of trunk prints no second answer.
- `internal/tui/fetched_base_test.go` — the headless session with `Trunk` set to the fetched ref: the base the
  box prints, the span label, and the file list. The live screen is not driven by a Go test; the pty
  walkthrough (`scripts/gates/pty-walkthrough.sh`) is what paints it.
- `go test ./...` over the whole repository.

## Known limitations

- Nothing fetches. A clone that has not fetched since the merge still measures from its stale fetch ref, and
  now says so on the `Base:` line rather than silently. A notification and a refresh key were deliberately held
  back from this changeset; `Repo.Fetch` and `change wait --fetch` are the precedent to build on.
- The review screen's `base` box and its span label both name the measured ref. The drift check still watches
  only `KindRef` endpoints, so `r` cannot re-pin a changeset base that moved — the base endpoint cannot drift by
  the current definition, and a fetch is what moves it.
- `queue` still lists each branch's recorded `base:` (`internal/cli/queue.go:425`). That column names what the
  file records, and the queue reads every branch in the repository — paying a containment read and a
  `merge-base` per row for a label nobody compares side by side is a cost this changeset did not take on.
