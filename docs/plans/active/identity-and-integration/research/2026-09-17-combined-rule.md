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

**Step 0. Terminal and integrated are excluded everywhere.** *(Superseded — see "Step 0, amended
in implementation" below: they are reported as facts and filtered by the layers that list work.)*
A changeset with an
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

The rule needs `T`, and there is no concept of it in the code yet — the only branch logic in
the product is `symbolic-ref --short HEAD`. **Decided: a flag plus git's own answer, and no
config key.**

There is already a trunk guess in the product — `defaultBase` at `internal/cli/change.go:316`,
local `main` then `master`, no remote involvement, used as `change init --base`'s default. It has to
become the same resolver as this one, or a changeset's recorded base and the landed-test can
disagree. `change init --base` stays as an override of it, since a stacked parent is a different
thing from the branch that means landed. Order of resolution:

1. `--default-branch <ref>` on the commands that resolve (`status`, `queue`, `diff`, `review`,
   `check`), used verbatim. Not `--target`, which the requirements use for `integration record`:
   they are different concepts. `integration record --target release/2.x` states where a backport
   landed, while the branch that defines "landed" for discovery is still `main`. One name could not
   say both, and `--default-branch` cannot serve the record command — `integration record
   --default-branch release/2.x` would be a false sentence.
2. `refs/remotes/origin/HEAD`, when present.
3. A unique `origin/main` or `origin/master`; a local `main`/`master` when there is no remote.
4. Otherwise **refuse**, naming both fixes: the flag, and `git remote set-head origin --auto`.
   Not "assume no trunk" — with no trunk, every directory on the branch is a candidate, and a
   confident "you are working on three changesets" is worse than an error.

Measured, because the whole proposal leans on this:

| Setup | `refs/remotes/origin/HEAD` |
| --- | --- |
| `git clone` of a remote whose HEAD names an existing branch (path and `file://` transports) | set, e.g. `origin/trunk` |
| CI shape: `git init` + `git remote add` + `git fetch origin <branch>` | **not set** |
| …then `git remote set-head origin --auto` | set |
| Remote HEAD names a branch that does not exist | git declines to guess (`Cannot determine remote HEAD`) |

So a human clone needs no configuration at all, and a runner needs one flag or one extra
command. Preferring the remote-tracking ref over any local branch is deliberate: what counts
as landed is what has been published, and a local trunk a week stale keeps landed changesets
looking active.

No config key, for three reasons. The product reads no git config today, and that is easier to
keep true than to claw back. The requirements never propose one; they externalise the ref at
the call site. And the argument for rejecting git config as the home of the `branch:` claim was
that it is machine-local — the same argument applies here, where it is worse: two clones of one
repository could disagree about what has landed, which is exactly the disagreement this rule
exists to remove.

Open sub-question worth an explicit call during M2: whether `change init` should refuse to run
on the integration branch. The rule already makes a stray init on trunk inert — measured, it
answers `uninitialized` for trunk and every branch off it — so the refusal is now a clarity
guard ("this branch is the integration branch; start your own") rather than a correctness one.
**Called: it refuses**, on the strength of that guard alone. Nothing else about the behaviour
changes, which is the point — the refusal exists so the mistake is named where it is made.

**Step 0, amended in implementation: terminal and integrated are reported, not excluded.** The
spec above removed them from the candidate set. Implemented that way, `status` on a branch whose
changeset was abandoned answers "no changeset here" about a directory still sitting in its tree —
the regression M1's abandonment reporting exists to avoid, and one the fixtures caught. A terminal
candidate is now `Candidate.Terminal`, reported beside `state`; the layers that list work (`queue`,
`check`) act on it. The integration ref follows the same shape in M4, when something writes one,
so "prune only what carries a terminal record" still holds — via reported facts and filtered
listings rather than via exclusion.

**Cost, corrected after rank 1 was dropped:** the "on trunk, fallback fires" row of the table
below (12) belonged to the fallback. The shipped rule measures 8 on a changeset branch with 300
review refs present, and `internal/changeset/resolve_test.go` asserts ≤10.

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

---

## Amended 2026-09-17 after review: rank 1 is dropped, and ambiguity gets a recorded decision

The reviewer read the fallback correctly — it exists for one situation, and two of the three
justifications I gave for it do not hold:

- **"CI has the code but not the metadata" is false.** A PR or merge-ref checkout gets the
  branch's tree, directory included.
- **"A rebase drops the init commit" is false.** Dropping that commit rewrites every descendant,
  so the archive ref stops being an ancestor of the new line and the fallback would not fire.
- **What is left is a committed deletion** of `changesets/<id>/` on a branch that was never
  rebased. Measured loss: the branch reports `uninitialized`; the archive ref stops advancing,
  which is inert rather than harmful, and the files come back with
  `git checkout <sha> -- changesets/<id>/`.

Resolution is therefore **rank 0 alone**. The cost is one confusing message in a case the author
caused, and it buys back the notes field, the rank in `--json`, and the ordering asymmetry.

### Merging an unlanded sibling is the case rank 0 cannot answer

Measured: merging a sibling changeset branch into yours puts *their* directory in your tree, and
both are absent from trunk, so the branch is ambiguous (`[mine@4 theirs@4]` — equal distance,
nothing to order them by). The claim model could not hit this, because their directory claimed
their branch. Merging trunk stays clean (landed directories drop out), so this is specifically
sibling-into-feature.

The answer is a recorded decision rather than a heuristic: `git pair change use <id>` refuses
anything that is not a candidate on this branch, and records the choice in the changeset that was
chosen — `ignores: <id> [...]` in `changesets/<id>/CHANGESET.yaml`. A candidate named in another
candidate's `ignores:` stops being a candidate.

**Where the field goes was measured, and the intuition was wrong.** Putting `inactive: true` in
the *losing* changeset's metadata does travel: land the winner, and the losing branch inherits an
`inactive: true` in its own directory after it catches up with trunk. But a control run with no
decision recorded at all resolves identically — that branch's directory is already on trunk,
carried in by the winner's landing, so the trunk test ended it regardless of the flag. The leak is
textual, not behavioural.

The reason to host the field in the winner is different and more solid: an edit under
`changesets/theirs/` is outside your changeset directory, so `change archive` counts it as
implementation drift and refuses to advance, and your landing carries a path belonging to someone
else's review. An edit under `changesets/mine/` is exempt from that rule by construction.

### The property this accepts, which needs documenting

If someone merges your unlanded changeset branch and lands it, **your changeset reads as landed on
your own branch** — your directory is in trunk's tree, so it stops being a candidate. Confirmed
with and without any recorded decision. It is defensible (your commits are in trunk) and it is
surprising from the author's seat. The remedy is `change init` for the follow-up work, or an
explicit `change abandon` of the original; it should be in the README troubleshooting rather than
discovered.
