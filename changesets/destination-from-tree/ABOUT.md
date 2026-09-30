# destination-from-tree

## Summary

`git pair` answers "where does this work land" from the destination's tree even when the changeset's own
`base:` has gone stale, and the sentence a human reads is computed from that answer instead of from the
field. This is M2 of `docs/plans/derive-destination-from-tree/plan.md`, stacked on M1.

## What changed

-   `internal/changeset/destination.go`: both proofs - does this name anything here, and does the destination
    already carry the changeset it names - are asked of every hop, the first included. Landedness is asked
    before existence, and wins: a base naming a landed changeset is not a destination, so the walk continues
    through that changeset's own record and stops at the first hop that is still live work. A base that
    resolves to nothing is reported (`Unreachable`, `Overrode`) and the answer falls back to the integration
    branch.
-   `Why` gained `base-landed`, and `Destination` gained `Overrode`, so a substitution of an authored field is
    visible in `--json` and in the sentence rather than something a reader infers.
-   `internal/cli/status.go`: `landingNextAction` takes the computed destination, and so do the four commands
    that print it - `status`, `check`, `review submit` and `change wait` - with the same note `change
    integrate` prints. The sentence and the declaration cannot disagree any more, because there is one string.
-   `internal/cli/integrate.go`: `unlandedParentReason` asks the destination's tree whether the parent landed
    when the stack names no parent changeset. It refused for lack of the field before, which is the shape this
    milestone exists to serve.
-   `internal/changeset/reads.go`: `changeset.Reads`, a per-command memo of the destination's directory
    listing and each ancestor's `CHANGESET.yaml`, passed from `status` to both the chain surface and the
    destination answer.
-   `internal/cli/report.go`, `internal/cli/status.go`: the `Base:` line passed branch names to `short`, which
    is a sha-width helper, so a parent branch called `feature/auth` printed as `feature`. Names now print in
    full; the commit a landed parent leaves behind still abbreviates. Found while probing what the two
    spellings of a stack link each answer, for the review question on M1.
-   PRD §13.1: rule 1 is not taken on trust, and the tie-break is written down - landedness wins over a branch
    that still resolves.
-   `scripts/gates/ci-integrate.sh`: the replay of the trap - a stack created against a live parent, the
    parent landed with ordinary git, then `check`, `change integrate` and the CI job all naming trunk, with no
    hand edit. A second child carries the legacy plain-`base:` file. 60 checks became 76.

## Design decisions

**Landedness wins the tie.** A base can name a branch that is live work and a changeset that has landed; the
destination's tree decides, and the matched id is printed. The alternative - the branch wins while this clone
can resolve it - makes the answer depend on which refs a clone holds, the property this repository deleted its
durable ref layer to get rid of.

**The tree fact goes first at every hop.** An id-shaped base resolves to nothing, so proving existence first
would misreport it as a branch that went away. It is also one fewer read per hop.

**Cost is met by sharing the walk, not by lifting the bound.** `status` already walks the ancestors to
describe the stack; `Reads` is what stops the destination answer from walking them again. The memo is an
argument, not a field on `git.Repo`, because commands write markers and move refs while they run.

**The refusal that became unnecessary was changed, not left.** `change integrate` declined the plain-base
child because the file recorded no parent changeset; the destination can prove the landing, so it asks. The
refusal still fires when the directory is not in the destination, which is the case the rule was written for.

## Validation

-   The five shapes from the plan, in `internal/changeset/destination_test.go`, each asserting the answer and
    the reason for it together: the base naming a landed changeset; the base naming nothing this clone can
    resolve, with the measurement base asserted unchanged; the base naming live work, unchanged; the same base
    written branch-shaped and id-shaped, both matching the same id; and a walk that continues past a landed
    parent and stops at the live one above it.
-   `TestTheLandingSentenceFollowsTheDestination` asserts `check --json`, `status --json`, `review submit` and
    `change integrate` print one destination string on the trap's shape, and that none of them offers the
    merged branch.
-   `TestStatusStackChainCostsABoundedReadPerStep` passes on its original bound of five invocations per extra
    ancestor.
-   `TestStatusNamesAParentBranchAtFullLength` asserts the parent branch is named in full on the `Base:` line
    and that the sha-width abbreviation is gone from it.

-   `go test ./...` green; `scripts/gates/ci-integrate.sh` 76 checks green; `mise run gates` reported below.

## Known limitations

-   `Overrode` is reported, not repaired. The file still says what it said; `change tidy` and the author decide
    whether to rewrite it.
-   The recognition is by `SlugFromBranch`, which is many-to-one. A collapsed name can match a changeset the
    author did not mean, which is why the matched id is what surfaces print and the refused spelling travels
    beside it rather than in its place.

## Open questions

None. The tie-break the plan left open is decided and written into §13.1.
