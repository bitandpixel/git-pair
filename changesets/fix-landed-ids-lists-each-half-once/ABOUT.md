# fix-landed-ids-lists-each-half-once

## Summary

`git pair check --json` asked the destination for its `changesets/` listing twice on every call that asked
whether work had landed. `LandedIDs` built the active listing, then called `TidiedIDs`, which listed the same
tree a second time to work out which filed directories were not also in place — the one question the first
listing already answers. Each half of the destination is now listed once. That is two fewer git invocations per
destination read, and it re-records the cost figures in `scripts/gates/ci-integrate.sh`, which had gone stale
by six invocations and were one invocation away from failing on whoever got there next.

## What changed

```console
before: ls-tree -d … -- changesets/    ls-tree -d … -- changesets/.landed/    ls-tree -d … -- changesets/
after:  ls-tree -d … -- changesets/    ls-tree -d … -- changesets/.landed/
```

`TidiedIDs` keeps its signature; the filter moved to an unexported `tidiedIDs` that takes the active listing
its definition needs, so the caller that came for both halves asks for each half once.

The ladder the CI gate bounds, one branch per stack depth:

|                                        | one level | two | three | four |
| -------------------------------------- | --------- | --- | ----- | ---- |
| recorded in the script (dated 2026-08) | 25        | 43  | 52    | 61   |
| measured when this changeset started   | 29        | 49  | 58    | 67   |
| measured on this branch                | 27        | 46  | 55    | 64   |

`STACK_CEILING` goes from 50 to 51, which is the recorded two-level figure plus the five invocations of slack
its comment has always claimed.

## Design decisions

**The removed read is the one whose answer was already in hand.** Three other reads joined this measurement
since the figures were recorded, and two of them earn their place. `901a883` resolves the destination to a
commit id so the chain can be keyed by id rather than by a ref that moves under the answer — necessary, and
budgeted for. `d03f1b1` added `config --bool git-pair.hooks` and `rev-parse --absolute-git-dir`, which are the
clone's off switch and the place the hook points live; `61cc01e` later cut each from twice per run to once. The
repeated listing is the only one of the four that asked git for something the caller was holding, so it is
removed rather than paid for, and the numbers record what is left.

**Why the repeat cost nothing where anyone noticed it.** `GIT_PAIR_NO_CACHE` switches off the in-process memo
along with the facts on disk, so under the gate's measurement no read is served from the memo and a repeat is
a subprocess. In a normal run the memo absorbs it, which is what let a comment claim "the repeat of the active
listing inside `TidiedIDs` costs nothing in one run — `ls-tree` is a memoized read" while the same file counted
it. A claim that is true in one measurement and billed in the other is why the read is simply not made, and
why the pin is `TestLandedIDsListsEachHalfOfTheDestinationOnce` — a Go test on the listing sequence, using the
same uncached shim the shell gate uses — rather than a number only the shell can see.

**What is deliberately not touched.** `config --bool git-pair.hooks` is not classified as a pure read, though
doing so saves 8 to 16 subprocesses per cached `check --json`. `hooks.Disabled` runs through the command
classifier, which defaults unknown commands to a mutation, and the memo clear that follows is what stops a
later `rev-parse HEAD` being answered from before a hook moved the head — `internal/cli/hooks_points_test.go`
"a post-hook that commits is noticed by name" fails on the build that classifies it pure. The honest fix is to
resolve the git directory once and pass it down, or to gate the memo clear on the command actually writing;
neither belongs in a changeset whose subject is one listing.

## Validation

- `go test ./internal/...` from this branch
- `mise run gates`: the markdown format check, gofmt, `go vet`, the sharded Go suite, `scripts/gates/e2e-29.sh`,
  `scripts/gates/pty-walkthrough.sh`, and `scripts/gates/ci-integrate.sh`, which now prints `measured: 27
invocations at one level, 46 at two, 55 at three, 64 at four` and keeps its quadratic guard (`FOUR - THREE`
  still equals `THREE - TWO` at nine)
- `TestLandedIDsListsEachHalfOfTheDestinationOnce` builds a destination holding one directory in place and one
  filed away, and asserts the two `ls-tree` reads by name and order rather than by count, so a formulation that
  asks for the right number of listings for the wrong reasons still fails

## Known limitations

Four landed changeset records quote `49 measured against the 50 allowed`. They are those changesets' records
and are left as written; the note in `changesets/feat-ci-integrate-candidate-shape/ABOUT.md` that asked for
this re-recording is the reason it arrived as its own change rather than inside the one that drifted.

The same trick is available twice more and was not taken. `DirectoryAt` asks `ActiveIDs` and then `LandedIDs`
of one revision, so the active half is listed twice across the pair, and `unreviewedLandings` calls
`TidiedIDs` while holding a listing of its own. Each is one call per command rather than one per stack level,
which is why they are not in this changeset, and doing them properly wants an exported form that accepts a
listing the caller built.

The cached-run cost the hook `config` read causes is unchanged, as above.

## Open questions

Whether the memo should clear on the command rather than on the classifier's guess, which is the question the
hook read raises and the reason 8 to 16 subprocesses per `check --json` are still being paid in a warm clone.
