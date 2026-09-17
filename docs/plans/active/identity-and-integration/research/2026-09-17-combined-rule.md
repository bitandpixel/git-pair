# The combined rule, enumerated

Date: 2026-09-17

Follows `2026-09-17-archive-ref-discovery.md`, which measured the requirements' rule and the
tree rule. This note specifies the rule that was chosen — directories first, archive refs as
the fallback — precisely enough to implement, with the cases measured in
`../artifacts/discovery-spike/` (`TestCombinedRule`, `TestCostOfCombinedRule`).

## Inputs

- `R` — the revision being asked about (usually `HEAD`).
- `T` — the integration branch revision. **Nothing in the product resolves this today**; the
  only branch logic in the codebase is `symbolic-ref --short HEAD` for the current branch. See
  "The integration branch" below, which is the one genuinely new dependency this rule brings.
- `A` — `for-each-ref refs/git-pair/changesets/*/archive`, and the same for `*/integration`.
- The changeset directories in `tree(R)` and `tree(T)` (`ls-tree --name-only <rev> changesets/`).

## The rule, in order

**Step 0. Terminal and integrated are excluded everywhere.** A changeset with an
`integration` ref, or whose archive ref points at a commit carrying a terminal marker
(`Review-State: abandoned`), is not a candidate at any rank. The terminal test reads the ref
target only — `change abandon` moves the ref onto its own marker, so no walk is needed.

**Step 1. Rank 0 — the work in hand.**
`candidates = { id : changesets/<id>/ ∈ tree(R) and changesets/<id>/ ∉ tree(T) }`

A directory that has reached the integration branch is landed work, so it drops out with no
integration ref, no branch name, and no dependence on merge versus squash. A directory that
exists only here is work in progress, whichever branch line it sits on — which is what makes
a diverged parent and its child resolve to the same changeset instead of one of them
inventing a second answer.

**Step 2. Order rank 0.** Nearest archive tip wins (distance from the tip to `R`), which is
§18's rule kept where it works. If two candidates survive with no distance between them, use
the metadata: a candidate named as another candidate's `base:` is the parent, so it is not
what this branch is working on. If that still leaves two, **report ambiguity** — two
unrelated changesets on one branch is a state the tool should not guess about, and refusing is
the honest answer. Both paths are measured: the stack resolves without any archive refs, and
two sibling directories stay ambiguous.

**Step 3. Rank 1 — only when rank 0 is empty.** Ask the refs about this branch's *history*:

```
candidate = an archive ref whose target is in `git rev-list T..R`
            and whose changeset directory is not in tree(T)
            and not integrated and not terminal
```

This is the only thing the fallback is for: **the directory is gone but the branch still
carries the work.** The realistic causes are a deletion (author's cleanup, a merge that
dropped it, a rebase that dropped the init commit) and a CI checkout that has the code but
not the metadata. The answer is reported with a distinguishing note — *changeset directory is
not in the working tree* — because it is a degraded state worth seeing, not a normal one to
hide.

The two filters are deliberately redundant, and each carries a case the other does not:

- `rev-list T..R` says the work is on *this* line and not in trunk's history. On trunk itself
  that set is empty, so the fallback costs one listing there — this is why the fallback is
  not the 1,802-invocation walk again.
- `changesets/<id>/ ∉ tree(T)` catches work that landed **by content** rather than by
  commit — a squash landing has the directory on trunk while its archive target is nowhere in
  trunk's history. Without this filter, continued work on a squash-landed branch would
  resurrect a changeset that has landed. Measured: *"a landed changeset is not resurrected by
  its own ref"* resolves to uninitialized.

**Step 4. Nothing matched → uninitialized.** With the standard hint (`git pair change init`).

Rank 1 never outranks rank 0, and rank 1 is not consulted when rank 0 has a hit. Stacking is
entirely a rank-0 question: the child carries both its own directory and its parent's, and
neither is on trunk.

## What each rank is good at, and where each is blind

| Situation | Rank 0 (tree) | Rank 1 (ref) |
| --- | --- | --- |
| Normal work on a branch | answers | not consulted |
| Stacked child, parent unlanded | answers (nearest wins) | not consulted |
| Parent and child diverged, one shared ref | answers on both branches | not consulted |
| Parent landed, child continues | answers the child | not consulted |
| Directory deleted, history intact | blind | answers, with a note |
| Changeset landed, branch keeps working | correctly uninitialized | correctly excluded |
| Branch deleted entirely | blind | blind from `HEAD`; visible to `queue`, which enumerates refs as well as branches |
| Two unrelated changesets on one branch | ambiguous, by design | — |

The union is what makes the pair worth building: neither rule alone covers the deleted
directory, and the ref rule alone gets four fixtures wrong.

## The integration branch

The rule needs `T`, and there is no concept of it in the code yet. Proposed resolution order,
recorded as a decision rather than left to the implementation:

1. `pair.integrationBranch` config, if set.
2. The ref `origin/HEAD` points at, when a remote exists and that ref is present.
3. A local `main`, else a local `master`, if exactly one exists.
4. Otherwise **refuse**, naming the config to set. Not "assume no trunk": an unresolvable
   trunk would make every directory on the branch a candidate, and a wrong "you are working on
   three changesets" is worse than an error telling you to fetch.

Preferring the remote-tracking ref over the local branch is deliberate: what counts as landed
is what has been *published*, and a local trunk that is a week stale would otherwise keep
landed changesets looking active. Measured consequence, not a preference test: with the local
branch as `T`, a changeset merged upstream and not yet fetched resolves as active.

Open sub-question worth an explicit call during M2: whether `change init` should refuse to run
on the integration branch. The rule already makes a stray init on trunk inert — measured, it
answers `uninitialized` for trunk and every branch off it — so the refusal is now a clarity
guard ("this branch is the integration branch; start your own") rather than a correctness one.

## Cost

Measured with 300 archive refs present:

| Position | Invocations |
| --- | --- |
| Requirements' rules as written, one resolution | 1,802 |
| Combined rule, on a changeset branch | 8 |
| Combined rule, on trunk (fallback fires, matches nothing) | 12 |

The rank-1 filter is one `rev-list T..R`, bounded by the commits unique to this branch rather
than by repository history or ref count. Per-candidate work (terminal trailer, distance) runs
only on the survivors, which is normally zero or one. The spike keeps a dead second rev-list
(`head..trunk`) that the product implementation should drop.

## What this changes in the product

- `changeset.ForBranch` / `AtCommit` become resolution by this rule; `ForID` stays for
  `--changeset` reads. The M1 `branch:` claim and its stale-claim machinery go away, so
  `CHANGESET.yaml` returns to `id` + `base`.
- `queue` gets a natural enumeration: local branches, each resolved against `T`, plus archive
  refs whose changeset has no branch — reported as a distinct condition rather than as an
  active changeset, which is the case that made the previous plan's M4 refuse to enumerate
  refs at all.
- `status` gains why it resolved what it resolved (`rank`/`note`), so a rank-1 answer is not
  mistaken for a normal one.
- `change init` writes `base: refs/git-pair/changesets/<parent>/archive` for a stacked parent
  and creates the archive ref immediately, per §11.
