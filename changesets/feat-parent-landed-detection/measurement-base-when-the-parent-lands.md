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

## Round two

> i think its okay that they disagree. they agree deterministically given the same refs are present. what are
> the practical issues if this disagreement materializes?

Three, and the second and third need a guard rather than an argument.

**Notes and chain lines: harmless.** `queue`'s landed-parent note, `status`'s `Stack:` ancestor lines, and
`check`'s note beside the verdict appear in the fetched clone and not in the unfetched one. The unfetched
clone already answers "no record in this clone" for those and prints the fetch remedy, which is what
`landed.go`'s `unrecordedHedge` and `namespaceEmpty` exist for. Nothing branches on them.

**The diff a reviewer opens.** `git pair diff` and `/pair-review` resolve their span from `base:`, so the
fetched clone's reviewer reads the child's own files and the unfetched clone's reviewer reads the child's
files plus the parent's plus trunk's — probe row two. That is the same defect the relink fixes, now
conditioned on whether the reader fetched. The guard: when the base stays the branch because the record did
not resolve, `status`, `check` and the review surface print the fetch remedy on that line, so nobody reads the
noisy diff believing it is the quiet one. It is not a new failure mode — `relinkStacks` has had this shape
since `feat-lineage-in-the-surface` — but milestone 4 makes the branch-present case common enough to be worth
spelling the line out.

**What a review submission records.** `Review-Parent-Head` is the parent's tip as `changeset.ParentOf`
computes it at submission time. If a landed parent's "tip" becomes the integration commit, two submissions of
the same child from two clones record two different values, and a later movement check then invalidates an
approval nobody moved. Deterministic agreement given the same refs is enough for reading; a value a submission
writes has to be the value every clone would write. The guard, in milestone 4: `Review-Parent-Head` keeps
naming the branch tip while the branch exists, and the landing is recorded beside it as its own fact.

One more, found while checking that one and already latent on trunk: `derivationDestinations`
(`internal/cli/integration.go:252-275`) offers `cur.Base` as the default `--target` for `integration record`.
Once `base:` can be `refs/git-pair/integrations/<parent>`, that candidate is a ref holding one commit that can
never contain the child's landing. The fallback to the default branch still finds the real destination, so
nothing is wrong today, but the refusal text names a destination that is not one. Milestone 4 skips a base
under `refs/git-pair/` when deriving destinations.

> yep lets keep an approval if the diff is identical

Adopted. Milestone 4's rule is: relink when the record exists and resolves; compare `base...head` under the
old base and the new one; identical content leaves the approval standing and the relink is a label, and
different content means the approval was taken against a diff that was never the child's, so the child goes
back for review. PRD §21 gets that sentence in place of the reading that treats all parent movement alike, and
the two cases get separate tests instead of one conservative default.

> Ok sounds good.

Recorded, and milestone 4 is no longer blocked. It still has its tests written first, so the §21 wording lands
against passing expectations.
