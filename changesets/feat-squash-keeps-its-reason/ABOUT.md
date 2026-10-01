# feat-squash-keeps-its-reason

## Summary

A squash landing stopped saying so the first time anything else landed on the destination. The reading was
derived from the destination's tip — "the landing is trunk's newest commit" — which is a fact about the
destination that changes every time somebody else merges, not a fact about the landing. Measured in a scratch
repository, same changeset, two unrelated commits added to `main` in between:

```text
before, while the landing is trunk's tip:
  alpha
    on main at 69e339d: the landing carried the directory in one commit, so no review markers came with it

after two unrelated landings, same changeset, same history behind it:
  alpha
    on main at 1775a8b, chain 00fd119..cb40e75: the chain carries no review verdict
```

The second line is wrong twice over. It blames a reviewer for a chain that was never in this repository, and
it prints a range that runs through the destination's own commits as though they were this changeset's work.
The report also disagrees with itself across time, which is the property that makes the heading hard to act
on: a reader who saw the first sentence cannot tell whether the second is a different fact or a lost one.

The fix keeps the reading attached to the landing. Two facts decide it, and both are read from the landing and
the chain it brought: the directory arrived in one commit that is not a merge, and that chain carries no
marker for the changeset. Neither is enough alone, which is the reason this is not the one-line change it
looks like.

## What changed

| file | what it does now |
| --- | --- |
| `internal/changeset/chain.go` | `Chain.Squash` becomes `Chain.ArrivedInOneCommit`, derived from the landing commit (`parents == 1`, not a merge) instead of from `Head == landing`; the type comment says why `Head` stays at the destination's tip |
| `internal/cli/landed.go` | new `carriedNoReviewRecord(chain, summary)` — the arrival shape **and** `summary.Marker == nil`; `chainRange`, `unreviewedReason` and `landingLicence` take the summary instead of a pre-extracted verdict so all three can ask it; `landingView` summarises before it decides whether to name a range |
| `internal/factcache/factcache.go` | `format` `"1"` → `"2"`: the cached landing record's `reason` wording changed, and an entry holding a sentence this build no longer prints is read through the right struct and still says the wrong thing |
| `internal/changeset/chain_test.go` | the merge landing asserts the arrival is *not* one commit; the fast-forward landing asserts that it *is*, and that `Head` is still the destination's tip; new `TestAOneCommitArrivalSurvivesTheDestinationMovingOn` reads one squash landing before and after two trunk commits |
| `internal/cli/landed_test.go` | new `TestASquashLandingKeepsItsReasonAfterTheDestinationMovesOn` and `TestAFastForwardLandingThatCarriedItsReviewIsNotAOneCommitRecord` |
| `scripts/gates/e2e-29.sh` | after the two findings appear, two ordinary commits land on `main` and the queue is read again: each keeps the sentence that is true for it |
| `PRD.md` | §4 "Landed, unreviewed", §10.6, §11.1, §13.3 (the table's `rebase-merge` row, and the rule that empties `chain_base`/`chain_head`) |
| `README.md` | the `status` field notes, the squash walkthrough, the `queue` section, the troubleshooting reason list |
| `skills/git-pair/references/integration.md` | "what survives a landing" names the pair that decides the empty chain |

The reason sentence is reworded:

```text
the landing carried the directory in one commit, so no review markers came with it   (before)
the landing carried the directory in one commit, and the chain carries no review markers   (after)
```

The old sentence made a causal claim that the new condition can no longer support on its own, because the
condition is now also true of a linear landing whose record happened to be committed once and whose markers
did not come across. The conjunction is true in every case the reading prints. Both keep the phrase `one
commit`, which the gate and three tests grep for.

## Design decisions

- **The literal fix — set `Chain.Head` to the landing commit — is wrong, and two tests say so.** Applied to
  `main` and run, it fails `TestLandedChainOfAFastForwardLanding` (`Head = 07d4ebd4, want 6829de82: the chain
  tip is trunk's tip here`; `chain range is missing the work commit`) and
  `TestAReplayedLandingIsUnreviewedBecauseTheApprovedCommitsDidNotArrive` (`chain_head = `, `state = WORKING,
  want APPROVED`, and the reason becomes the squash sentence, so the reader cannot tell a replay from a
  squash). The reason is the shape of a linear landing: the commit that adds `changesets/<id>/` is the first of
  the run, and the ready marker and the review submissions come *after* it as empty commits that touch no file,
  so a path listing cannot see them and a range ending at the landing loses the verdict. `Head` is the bound the
  marker walk needs; narrowing it does not report the landing more honestly, it reports a reviewed
  fast-forward landing as a changeset nobody looked at.
- **Shape and markers are two facts, and the report needs both.** The destination's tree can prove the arrival
  was a single non-merge commit. It cannot prove the run was squashed: a fast-forward whose record was committed
  once, whose work commits touched only other files, arrives in exactly that shape and may carry its approval
  across. Asking the shape alone reports a reviewed landing as unreviewed. Asking the markers alone gives every
  never-reviewed changeset the squash sentence, which blames a merge shape for a review nobody ran.
  `carriedNoReviewRecord` is the conjunction, and it is the only place the two are joined.
- **`ArrivedInOneCommit` is allowed to be true of more than squashes.** Its doc says so, and the fast-forward
  test asserts it is true there. A shape fact that changes when the destination moves is not a shape fact, so
  the test that pins it is the one that reads the same landing before and after trunk moves on.
- **`chain` stays empty under this reading.** For a squash there is no run to name, and the alternative —
  printing `base..tip` — is what the second line of the summary did: it pointed a reader at the destination's
  commits. Where the linear variant of the shape has markers, the chain is printed, because then there is
  something to read.
- **The cache format is bumped rather than left to expire on its own.** Entries are keyed on the destination
  commit, so the stale wording would have disappeared at the next merge anyway. The bump is what the field
  exists for, and the comment now says an answer whose wording changed is a miss.

## Validation

- `gofmt -l internal cmd` clean, `go vet ./...` clean.
- `go test ./internal/changeset/ ./internal/cli/ ./internal/factcache/` — all pass, including the four new
  tests and the two that reject the literal fix.
- `mise run gates` — exit 0: `E2E: all checks passed`, `PTY: all checks passed`,
  `CI-INTEGRATE: all checks passed (70 checks)`. The e2e run prints the new assertion:

  ```text
  ok: the landing that carried no history still says so after the destination moved on
        on main at b8d8ddc: the landing carried the directory in one commit, and the chain carries no review markers
  ```
- Scratch repository, the scenario from the report: a reviewed changeset squashed onto `main`, then two
  unrelated commits. `queue` before and after prints the same `one commit` reading, `chain` stays empty, and
  `status --changeset alpha` says `chain: none — the landing carried the directory in one commit` with
  `reviewed: false` in `--json`. The same script against the pre-change binary prints `the chain carries no
  review verdict` and a chain range after the destination moves.

## Known limitations

- A linear landing whose record was committed once and whose markers did not come across gets this reading and
  therefore this sentence. It is true of that case — the record did arrive in one commit and no markers came
  with it — but it does not name the merge shape, and nothing in the destination's tree can: the two arrivals
  differ only in which commits after the landing belonged to the branch. This was already unanswerable, since
  the old code called that case a squash outright while the landing was the tip.
- Under this reading the reader gets no range. For the linear variant there was one — the run the markers did
  not survive — and it is not printed, because the same field must not point at the destination's commits for
  the squash case. `status --changeset` still reports `state`, so a replay of that kind is visible as
  `state` with no chain.
- Two documentation lines touched here are also touched by `feat-tidied-changesets-no-warnings`, which rewrites
  the same troubleshooting bullet and the same §13.5 prose from the other side (a filed-away landing
  acknowledging itself). Whichever merges second has a small conflict to resolve; the texts are compatible.

## Open questions

- Should the report distinguish "no markers anywhere in the destination" from "markers exist for this changeset
  but not in this chain"? It would need a second walk over the destination's history for a case that today
  reads correctly, so nothing here asks for it.
- `landingView` prints `chain: none — the landing carried the directory in one commit` in text and
  `chain_base: ""` in JSON. The text does not carry the marker half of the pair. It reads better and the field
  carries the same emptiness; a reviewer may prefer the sentence made identical.
