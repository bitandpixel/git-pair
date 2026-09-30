# infer-parent-changeset

## Summary

`git pair init` records `parent:` and `parent-changeset:` when the base it is handed names a branch carrying
exactly one unlanded changeset, and `git pair check` recommends the same declaration for changesets whose
file predates the rule. This is M1 of `docs/plans/derive-destination-from-tree/plan.md`.

## What changed

-   `internal/cli/init.go`: once the base is resolved, a base naming a branch that is not the integration
    branch is asked the question `--parent` already asks. One unlanded changeset on it means the work is
    stacked, so the pair PRD §21 spells is recorded and the run says so. Two or more means nothing can tell
    which one is the parent, so the base stands as written and every candidate is named.
    `parentChangesetOn` now takes the resolved integration branch and returns the candidates it looked at,
    which is also what lets the existing `--parent` warning name them instead of saying only that it could
    not pick.
will we be able to support multiple
levels of parents? for example,
grandparent A with changeset G, parent B
with changeset P, child C with changeset
C. if B is the base for C, it will see
both G and P as unlanded changesets. we
should filter out the parents unlanded
ancestors when applicable.
-   `internal/cli/check.go`: `recommendations` in `--json` — an array in both verdicts, for the reason
    `reasons` is one — and `recommend:` lines on the human surface. Computed after the gate, never inside it.
-   PRD §21: the rule, and why a base naming a branch is not the same fact as a parent naming one.

## Design decisions

**The pair is recorded, not the id alone.** `parent-changeset:` beside a plain `base:` would break the
measurement base rather than improve it. `BaseFor` rule 1 keeps measuring against a live parent only when
`parent:` names it; with an id and no branch, the derivation falls to `merge-base(child, destination)`, which
while the parent is still unlanded is the point the whole stack forked from trunk — the child's diff would
then contain the parent's work. `renderMetadata` dropping `parent-changeset:` when `parent:` is absent is the
same rule from the writer's side.
can you explain why we would have both
parent: and base: ?

**A recommendation, not a reason.** A changeset written before this rule existed must not be held by it, and
which key a file chose to name its parent with is not a condition a merge should depend on. `check` prints
advice the reader can refuse.

**Trunk answers without reading the repository.** The recommendation returns early for a base naming the
integration branch, which is the common case, so `check` costs what it cost before.

**`--set-parent` stays the author's flag.** The inference records a stack that was always there. It does not
restack a changeset that had recorded a different parent, which remains an explicit act.

## Validation

-   `go test ./...` green on this tree. The six new tests: the pair recorded from a one-candidate base, no
    guess and both candidates named at two, trunk still a plain base, the recorded file and the measured base
    unmoved when the parent later gains a second changeset, `check` recommending the declaration in both
    surfaces without writing it as a reason, and `check` silent when the parent is recorded or there is no
    stack.
-   `internal/cli/change_test.go`'s `TestChangeInitRecordsRequestedBase` asserted the old spelling (`base:`)
    for the shape this changeset now records as a stack; it asserts the pair now.
-   `scripts/` has no `init --base <branch carrying a changeset>` usage, so the gate scripts needed no change.
-   `mise run gates` on this head: reported in the branch history below.

## Known limitations

-   The lookup is `refs/heads/<base>`. A base naming a remote-only branch (`origin/feature-x`, or the
    qualified ref) is recorded as written and gets no recommendation. The same question could be asked of that
    tree; it is left alone rather than answered by a guess about which spelling was meant.
-   The recommendation costs one `Resolve` per `check` on a non-trunk base. `internal/cli/cost_test.go` bounds
    `queue`, not `check`, so nothing asserted the old ceiling.

## Open questions

None here. The live-and-landed tie — a base whose branch is still live while a same-slug changeset landed
elsewhere — belongs to M2 of the plan and is recorded there as the decision it needs.
