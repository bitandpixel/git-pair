# feat-simplify-architecture

## Summary

Adds the execution plan for removing git-pair's durable ref layer.

git-pair writes two create-only refs per changeset at landing and carries the machinery that serves
them: a recorder with a derivation and a verifier, a publisher, a refspec consent command, a mirror
namespace for published-vs-unpublished, a detector for landings nobody recorded, and two `internal/hygiene`
invariants that exist only because refs are written at all. Roughly 2.7k production lines and 3k test lines
answer questions the destination branch can answer by inspecting its own tree.

The plan proposes deleting the concept rather than reducing it to one ref. This changeset is the plan
document alone; no code changes are included.

## What changed

- `docs/plans/simplify-architecture/plan.md` — six milestones, each ending green under `mise run gates`.

The four mechanisms the plan replaces:

| today | planned |
| --- | --- |
| `refs/git-pair/integrations/<id>` says the changeset landed | `changesets/<id>/` present in the destination's tree — `resolver.onTrunk`, already built and cached per scan at `internal/changeset/resolve.go:244-255` |
| `refs/git-pair/archive/<id>` names the unsquashed chain | the contiguous run on trunk's first-parent line carrying this changeset's directory |
| a child of a landed parent is measured against `refs/git-pair/integrations/<parent>` | one exported `changeset.BaseFor` returning `merge-base(head, destination)`, cached in the scan |
| `LANDED, UNRECORDED` — a finding about a command nobody ran | `LANDED UNREVIEWED` — a finding about work that reached trunk without a verdict |

`git pair change tidy` is added to move landed directories from `changesets/<id>/` to `changesets/.landed/<id>/`,
so tidying clutter does not also erase the tree's statement that a changeset existed and the metadata the
destination walk reads.

## Design decisions

- **Landed means on trunk.** A child merged into its parent branch is carried, not landed: its directory
  reaches trunk only when the parent's does.
- **Squash landings lose the review record, and the plan says so.** A merge-commit, fast-forward, or rebase
  landing leaves the whole conversation — markers, verdicts, threads, interleaved commits — in trunk's own
  ancestry, so nothing is lost. A squash leaves nothing, and past git's reflog expiry it survives nowhere.
  `PRD.md` §13 currently promises the archive for exactly that case, and the plan rewrites it as a stated
  limitation rather than softening it. The deferred cheap answer is a `verdict:` field written into
  `changesets/.landed/<id>/CHANGESET.yaml`, which trunk replicates for free.
- **Nothing writes a ref, enforced.** The single-`update-ref`-call-site invariant is replaced by one that
  fails the build if shipped code touches `update-ref`, `symbolic-ref`, or `git tag`, in the same
  syntax-reading style `internal/hygiene` already documents at `:16-32`.
- **Reads move before writes are deleted.** M1-M4 build and exercise the tree-based reads while the refs are
  still written; M5 deletes the subsystem. A fixture repository seeded with inconsistent `refs/git-pair/*`
  refs must produce byte-identical output to one without them, which is what makes M5 safe.
- **`--fetch` leaves `status`, `queue`, and `check`.** It was added for the durable refs and their mirrors;
  `change wait --fetch` polls for somebody else's commits and stays.
- **The publish, lease, and push-refspec questions close by deletion.** They only exist if something needs
  publishing, and nothing does once there is no ref.
- **Out of scope, recorded as such:** a child's `check` waiting on a parent's landing, and any publication
  requirement. The second has no subject once refs are gone.

## Validation

Documentation only, so the validation here is that the plan is checkable rather than that a behaviour works:

- Every claim about current behaviour cites `file:line`, and each citation was read in this checkout —
  including the ones that decided the shape: `internal/cli/integration.go:657,671` (the recorder verifies a
  tree fact), `internal/changeset/resolve.go:596-610` and `internal/cli/root.go:326-333` (the ref-driven
  relink), `internal/changeset/destination.go:60-125` (the walk whose target the tree already carries),
  `internal/cli/stacked.go:381-402` (the comparison a derived base feeds).
- `DirsAt` (`internal/changeset/changeset.go:361-385`) was verified to have no dotfile exclusion, which is
  why the `.landed` fix is milestone M1 rather than a note under tidy.
- Milestone gates are the repository's own: `mise run check` per commit, `mise run gates` — `check`,
  `e2e-29.sh`, `pty-walkthrough.sh`, `ci-integrate.sh` — per milestone. The plan states that the replays are
  contracts rewritten with the behaviour, and 43 assertions in `e2e-29.sh` plus most of
  `scripts/gates/ci-integrate.sh` are expected to change.
- The landing-shape table was reasoned through per shape rather than asserted: merge commit, fast-forward,
  rebase merge, squash, with the rebase case checked against `ffChainTip`'s existing refusal at
  `internal/cli/integration.go:319-329`.

## Known limitations

- The plan is a proposal. `docs/plans/review-architecture-v2/`, `docs/plans/publish-the-records/`, and
  milestone M1 of `docs/plans/legacy-refs-and-remote-branches/` contradict parts of it, and the plan marks
  them superseded rather than arguing with them inline.
- The squash-merge trade is the whole price and it is not recoverable by anything in the plan. If a
  squash-merged trunk turns out to matter, the plan needs a milestone, not a footnote.
- `BaseFor` replaces one stored value with a value derived at three call sites. The plan names that as its
  own created risk and mitigates with one helper plus a cached scan value, because two surfaces agreeing only
  because they print the same literal is a weakness this repository has already recorded once, in
  `docs/plans/lineage-in-the-surface/plan.md` M2.
- Repositories already holding the refs keep them. Nothing reads them and git-pair will not delete them, so
  they linger as inert refs until somebody removes them by hand.

## Open questions

1. Is any trunk that matters squash-merged? This decides whether D2 is a limitation or a blocker, and it is
   the only question whose answer can grow the plan.
2. Should `--fetch` disappear outright, or stay on those three commands meaning "refresh remote-tracking
   branches"? The plan takes it out; the counter-argument is that `origin/main` staleness then has no
   git-pair surface at all.
3. Is `changesets/.landed/` the right name? It is a hidden directory under `changesets/`, which makes it
   invisible to `git pair status` and to `DirsAt` in the same breath, and that is the point — but a visible
   `changesets-landed/` beside it would be equally simple and would not need the exclusion rule to be
   remembered by the next author who adds a directory there.
