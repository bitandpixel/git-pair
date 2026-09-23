# Measurement base when the parent lands

From review `a87ae0b`, inline on the plan's first non-goal:

> deleting the branch changes the measurement base though too right? so if a parent branch is integrated I
> dont see a ton of benefit to keeping the measurement pure as the base/parent is going to change anyhow.
> lets move this to a thread to discuss.

## Response

You are right, and the purity argument does not hold. Deleting the branch relinks the base
(`relinkStacks`, `internal/changeset/resolve.go:535-549`), so the base moves either way. The current trigger
decides only *when*, and the delay is where the wrong diff lives.

Probed in a scratch repository — trunk, `parent` off trunk, trunk advances independently with `t1`, `p1` on
`parent`, `parent` merged into trunk as `L`, child off `p1` with `c.txt`:

| child state | base = parent branch tip | base = integration ref |
|---|---|---|
| child not yet on the landing commit | `c.txt` | `c.txt` |
| child rebased onto trunk | `c.txt`, `p.txt`, `trunk.txt` | `c.txt` |

The second row is the ordinary state of a child a few minutes after its parent landed, and it is the case the
trigger gets wrong today: with the base at the parent's branch tip, the child's diff carries the parent's own
work *and* unrelated trunk work, because `tree(B)` predates both. `git diff --name-only` there printed
`c.txt p.txt trunk.txt` against `c.txt` for the integration ref. The integration ref is the base that
produces the honest diff, and the child reaches it only once somebody deletes the branch.

So the non-goal is withdrawn. Two things still need a decision, and they are why I would not fold this into
milestone 1:

1. **Resolution.** `refs/git-pair/integrations/<id>` is not fetched by default. `relinkStacks` already guards
   with a `RevParse`, so an unfetched clone keeps the branch base, and the same guard works with the branch
   present. The cost is that two clones of the same commits can print different `Base:` values, which cuts
   against the resolution rule in README's Concepts ("two clones of the same commits cannot disagree").
2. **PRD §21's movement rule.** If a landed parent's current tip is the integration commit, the recorded
   `Review-Parent-Head` no longer matches it. Reading the probe: invalidation should follow the diff rather
   than the ref. Where the child's content is unchanged the relink is a label; where it changes (row two) the
   approval was made against a diff that was never the child's, and a re-review is the honest outcome rather
   than a penalty. That is a stated change to §21's "intentionally conservative: git-pair does not attempt to
   tell a metadata-only parent commit from an implementation change", so it is yours to accept rather than
   mine to assume.

Milestone 4 carries the shape: trigger on the record existing and resolving, compare the two spans, and pick
one of the two readings of §21. Milestones 1–3 stay as written — the notes, the JSON fields and the advisory
are needed under either answer.
