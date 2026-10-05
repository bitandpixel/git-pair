# destination-credited-content

## Summary

Two rules stop charging the author for content they did not write.

-   The identity an approval records becomes two hashes in one trailer — `Review-Diff-Id: 2:<raw>+<patch>` — and a
    stacked child that has taken the destination in keeps its approval when the *patch* half is unchanged and
    the branch still merges cleanly. Today the whole-file blob OIDs move when trunk edits a file the child
    also edits, however far the line is, and the approval dies.
-   The rule that counts content the review never saw stops counting the destination's own content. A path is
    not the author's when head's copy is byte-identical to the destination's, and drift is suppressed entirely
    when head's tree is what `git merge-tree` produces from the reviewed state and the destination.

Both were measured before being written down; the numbers are under `What was measured`.

## The two refusals this removes

A child that merges trunk in — the thing a reviewer most wants before a merge — was refused for content it
did not write. Run against the built binary:

```
L. the parent landed, so trunk holds its authhelper.go; the child merged trunk in
   refuses: content outside changesets/booking-tests/ changed since 479ab12: authhelper.go
   authhelper.go  head c82a2b1  main c82a2b1   ← byte-identical to trunk's copy
```

and, for the shared file, the content rule refused on the blob identity:

```
H. parent and child both edit config.yaml; the parent landed; the child merged trunk in
   recorded raw  d31a8b51…   now raw 12a9c579…   ← differs only in the blob OIDs
   clean merge: yes
```

The first is the unreviewed-content rule reading a tree and not knowing who wrote a line. The second is the
digest hashing whole-file blob OIDs, which is exact about content and blind to whose content it is.

## The identity: two halves, one trailer

One `git diff` produces both values; the output is split at the first `diff --git`.

```
git diff --raw -z --no-renames --no-ext-diff --no-textconv -U3 <from> <to> -- . :(exclude)…
  raw half    → sha256                                    → the claim about content
  patch half  → git patch-id --verbatim                   → the claim about position
```

`--verbatim` rather than `--stable`, measured on git 2.43.0: `--stable` and `--unstable` gave the same value
for a Makefile recipe line added with a tab versus the same line with spaces, which `make` parses
differently; `--verbatim` distinguishes them, and still ignores `@@` line numbers — which is the property that
keeps H cheap.

Measured semantics of the patch half, from controlled inputs:

| what differs | patch value |
| --- | --- |
| `@@` line numbers (a trunk line landed above the hunk) | unchanged — this is what keeps H's approval |
| `index <oid>..<oid>` for a file that has hunks | unchanged |
| `index <oid>..<oid>` for a file with no hunks (binary) | moves — binary payloads are visible |
| file path, `old mode`/`new mode`, `new file`/`deleted file` | moves |
| a whitespace-only added line | moves (with `--stable` it did not) |

The raw half is unchanged from the previous changeset, and stays the primary claim: `patch-id`'s algorithm is
git's, its documentation says it may change, and the table above shows behaviour that depends on whether a
file happens to produce a hunk. A value this build cannot interpret reads as absence, never as a difference —
the same rule as an absent trailer.

Two things had to be true for the second half to cost nothing to the approvals already written, and both are
tested rather than argued:

-   Adding `-U3` and `--default-prefix` does not move the raw half. git puts one further NUL between the last
    name-status record and the patch, the raw section is everything before it, and hashing exactly those
    bytes reproduces the value the previous version wrote — `TestDiffIdentityRawHalfIsTheShippedDigest` holds
    the two implementations against each other, so a `2:` marker can be compared with a `1:` one.
-   The version accepts both spellings. This build writes `2:`; a `1:` value is read as a content identity
    with no rendered half, so an approval written before this change is compared on what it recorded rather
    than treated as unreadable (`TestAnIdentityFromTheFirstVersionComparesOnItsHalf`, and at the gate
    `TestAnApprovalFromTheFirstVersionDoesNotRelaxOnTheRenderedHalf`, which is the conservative side of the
    same fact: no rendered half, no relaxation).

## What the surface gains

Two values, both readings of a verdict rather than new verdicts:

-   `parent_comparison` gains `contribution-patch`: the content half moved, the rendered half did not, and the
    branch still merges into the destination cleanly. A reader who does not believe the pass can see which of
    the two halves answered (PRD §21.1).
-   `check --json` gains `drift_credited`, the paths the destination was credited for when the unreviewed-content
    rule let a merge through. What it refused stays in `reasons` (PRD §11.3).

The PRD carries the rules in §10.4 (the compound value and its versions), §11.3 (the credit, and the field),
§21.1 (what each half covers, and why `--verbatim`), and §27 (the sibling rule this takes).

## Crediting the destination

`ReconcileStaleness` compared the reviewed tree with head and called every difference the author's. Now:

-   a path is not drift when head's blob equals the destination's blob — that content is in the destination,
    was reviewed in its own changeset, and the author did not write it;
-   drift is suppressed when head's tree equals `git merge-tree --write-tree <reviewed marker> <destination>`
    — head is then the reviewed state with trunk mechanically merged in, so nothing beyond a merge entered.

The second clause is what lets the first do its job on a shared file: `config.yaml` at head is nobody's copy
but the child's own, and it is still just what `git merge main` produced.

What stays refused, deliberately: an author's own new content (the copy matches neither the destination nor
the trivial merge), content merged in from a third branch, a conflict resolved by hand, and the same file
arriving from a parent that has not landed — measured as scenario K, `authhelper.go head == parent, main
absent`, refused.

## What was measured

| scenario | raw | patch (`--verbatim`) | merge | verdict |
| --- | --- | --- | --- | --- |
| H — trunk edited the same file, 14 lines away | moves | equal | clean | **stands** |
| J — trunk edited a line inside the reviewed hunk's context | moves | moves | clean | refused |
| Makefile recipe line, tab → spaces | moves | moves | clean | refused |
| L — child merged trunk, trunk holds the parent's new file | — | — | clean | **stands** |
| K — same file, parent unlanded | — | — | clean | refused |

## Cost

The two halves come out of the one `git diff` the previous changeset already ran, so the identity costs one
extra `git patch-id` per measurement taken — a read of a pipe, classified `pureRead` beside `merge-tree` so
the memo holds its answer rather than clearing itself. Without that classification the default (`mutation`)would clear the memo on every identity, which is the kind of cost that shows up as a slow command rather than
as a bug.

The drift rule gains one `merge-tree`, one `rev-parse` and one `diff` where it previously asked nothing, and
only where a path moved outside the changeset directory.

Measured with the suite's spawn shim, the bound `TestStatusStackChainCostsABoundedReadPerStep` pins is
unmoved by this changeset: `status --changeset` on the five-deep landed stack costs 7 invocations per extra
ancestor here and before it (budget 10).

## Risks named

-   `git patch-id --verbatim` is newer than the rest of the invocation. An older git that rejects the flag
    makes the patch half absent, which returns the gate to strict raw comparison rather than refusing.
-   If git changes what `--verbatim` keeps, the value moves for everyone and every landed-parent comparison
    reads as absence. That is the conservative direction, and the version prefix is what makes it read that
    way rather than as a content difference.
-   Crediting the destination trusts trunk's own review and CI. An unlanded parent's content is not credited,
    which is why K stays refused.
