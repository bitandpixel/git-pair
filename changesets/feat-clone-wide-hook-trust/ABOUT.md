# feat-clone-wide-hook-trust

## Summary

A person who works in several worktrees had to record a position on a repository's tracked hooks once per
worktree, while the refusal that asked for it said the position belonged to the clone. It did belong to the clone;
the code had put it in `.git/worktrees/<name>`. The record now lives under the git directory a clone shares with
its worktrees, and it holds a set of versions rather than one hash, so sharing it cannot overwrite anybody's
answer. The client's own hooks root moved with it, for the same reason: a check attached to a machine is
attached once, not once per checkout.

This is the change that came out of adopting the profile-owned `pre-ready` hook: the hook made the question get
asked, and thirteen worktrees made it get asked thirteen times.

## What changed

- `internal/git/git.go` — `CommonDir`, which resolves the git directory a clone shares with its worktrees from
  the `commondir` pointer git writes when it creates one.
- `internal/hooks/trust.go` — the record's location and shape: written to and read from the directory the clone
  shares, held as one hash per line, consulted by membership. `TreeHash` now hashes the executable bit rather than
  the permission set.
- `internal/hooks/hooks.go` — the run memoizes the record directory beside the git directory it already memoized,
  and `CloneRootDir` resolves through the same shared directory, so a script attached from any worktree is the
  clone's script.
- `internal/cli/hooks.go` — `--trust` and `--decline` print the path they actually wrote to and say the answer is
  the clone's; the `changed` sentence says what the clone *recorded* rather than what the reader accepted; the
  command's help says both roots are the clone's.
- `internal/hooks/trust_test.go` (new) — six tests: the record's location, the set under two versions, the
  cancellation rule, the bound, what the hash is allowed to depend on, and the shared client root.
  `internal/cli/hooks_test.go` — one test at the surface a person stands at, including a worktree cut after the
  reading. `internal/hooks/hooks_test.go`, `internal/cli/hooks_gate_test.go` — assertions reworded with the
  sentence, not around it.
- `PRD.md`, `README.md`, `skills/git-pair/references/hooks.md` — the paragraphs that named `<git-dir>` and one
  hash now name the shared directory, a set, and a per-clone root that is per clone.

## Design decisions

**The clone is the scope, because the answer is about content.** What a person read is the bytes of
`.git-pair/hooks/`, and every worktree that has them has the same bytes. Asking per working directory made the
safety step into a tax, and a tax of that shape is paid by running `--trust` without reading — which is the
failure mode the whole consent mechanism exists to prevent. The refusal sentence already said "have not been
recorded in this clone"; this makes the code mean it.

**Sharing a record requires it to be a set.** Each record was one hash compared for equality. In one worktree
that is enough; shared, it is not — two worktrees of one clone can stand on two versions of the directory, trunk
and the branch that edits the suite, and a single slot means the second person to record makes the first
person's checkout read as `changed`, i.e. "the repository moved under you", when all that moved was somebody
else's branch. So each file is one hash per line, `TrustAt` asks membership, and the order of lines exists only to
decide which hash a report prints.

**Recording a position now cancels one hash instead of clearing the other file.** The old rule was deliberate, and
it is the one thing here that relaxes an invariant, so it is the place to look hardest. Clearing was needed by the
single-slot reading: with one hash stored, the mere presence of an acceptance made content that was not that hash
report as `changed`, so a person who declined the tree in front of them could be told to re-read something they
had just read. Membership removes that reading — an entry answers for the content it names and for no other — so
what has to be cleared is the *same hash* in the other record, which is the actual contradiction. Clearing the
whole file is not free any more either: a person declining the branch they are standing on would switch off, in
every other worktree, the check somebody accepted on trunk. The test that pinned the old rule still pins its
purpose — the state after declining is `declined`, not `changed` — and now also pins that the older acceptance
survives.

**The hash covers the executable bit, not the permission set.** Found while building the worktree tests, and the
reason the per-worktree behaviour was worse than a nuisance: git checks a file out at the machine's umask (0664
here) while an editor, `tar`, or a fixture writes one at 0644, so two worktrees of *one commit*, holding one
byte-for-byte script, hashed apart. Every new worktree therefore arrived looking like content that had moved under
an acceptance. The executable bit stays, because that is the bit that changes what the repository is asking to
run; who may write the file is a fact about the machine.

**`commondir` rather than `git rev-parse --git-common-dir`.** The plain clone is the case that must not pay: it
has no `commondir` file, because its git directory *is* the common one, and `CommonDir` returns false without
touching git, so the change costs zero extra invocations on the path every author and every CI job walks — which
is the quantity `TestCheckAsksTheHookQuestionsOncePerRun` guards. A linked worktree pays one file read beside the
records it was going to read anyway. A checkout whose git directory was pointed at by hand has no such file and
gets the old behaviour, which is the correct fallback rather than a guess.

**There is no compatibility read.** The first version of this read the record in both places — the shared
directory and the worktree's own — on the grounds that a person who had read the scripts should not be asked
again. There is no such person: no release ever wrote a record beside a worktree, so the only holders of such a
file are running a checkout of this repository. Carrying a second location would leave every reader of this code
working out which one answers, for the benefit of nobody, so there is one place and one rule.

**The record is bounded at 32 entries, oldest trimmed.** The state machine asks only about the tree in front of it,
so the entries that matter are the current one and the versions a team has actually run. Unbounded would mean a
repository that edits its hooks weekly leaves every clone it touches carrying a file that gets read before a
hand-off can happen.

**The client's own root is the clone's too.** `CloneRootDir` resolves through the same shared directory, so a
check a reviewer attaches to a repository they cannot edit is attached once rather than once per checkout. This
was left as an open question in the first revision and taken up on review: the argument for keeping it per
worktree — that a directory is a place to attach something to *this* checkout — is weaker than the argument
against, which is asking the same act twice for no extra protection. The honest cost is named in Known
limitations: a directory shared by the clone changes which scripts run in a checkout, and that is a bigger claim
than moving a record file. It is bounded by what the root already is — your own file, outside the working tree,
never committed, never somebody else's — which is why it needs no consent step and why the second claim is safe
to make.

## Validation

- `mise run gates` — the repository's definition of done — three times across the two rounds, all exit 0 (134.5 s
  on the rebased tree, 119.8 s after the review, 66.1 s on the final tree). That is `check` (gofmt, `go vet`, the
  sharded suite) and the three replays — the PRD §29 loop, the pty walkthrough, and the CI merge job (132 checks).
  The cost gate inside it measured 29, 49, 58 and 67 git invocations for a one- to four-level stack on every run,
  with the two-level figure against its ceiling of 50. That 49 is the number this change was written not to move,
  and it did not move: resolving the shared directory costs a plain clone no git call at all.
- The third ladder run reused cached Go results, so the final tree is also covered on its own:
  `go clean -testcache && go test ./...` — every package, nothing cached — exit 0 in 134 s.
- `go test -count=1 ./internal/hooks/ ./internal/cli/`: 14 and 396 tests, all passing.
- What each new test pins:
  - `TestTrustRecordCoversEveryWorktree` — accepting in a worktree writes to the shared directory, writes nothing
    beside the worktree, and makes the main checkout answer `accepted` with the same hash.
  - `TestWorktreesOnDifferentVersionsBothKeepTheirPosition` — two versions, two positions, both `accepted`; a
    third version nobody read is `changed`; the untouched branch keeps its acceptance.
  - `TestCloneRootIsSharedByEveryWorktree` — the two checkouts agree on where the client's hooks live, and a script
    attached from one worktree is discovered by the other.
  - `TestRecordBesideAWorktreeIsInert` — a record file written beside one worktree's git directory changes nothing,
    which is what makes having only one place to look safe to assert.
  - `TestPositionsCancelOneHashAndIgnoreJunk` — accepting removes that hash from the decline record and leaves the
    others; a file of non-hash lines accepts nothing.
  - `TestRecordStaysBounded` — 44 recordings leave at most 32 entries, and the newest is still the newest.
  - `TestTreeHashCoversContentAndExecutabilityOnly` — 0644/0600/0664 hash alike, 0755/0775 hash alike, exec and
    non-exec differ, and an edit to the bytes still invalidates.
  - `TestHookTrustIsRecordedForTheWholeClone` — `--trust` in one worktree, then `hooks --json` says
    `trust_state: accepted` in the main checkout *and* in a worktree created afterwards, and `git status` stays
    clean.
- Two mutation checks, each reverted afterwards with `git status` and `gofmt -l` clean:
  - Making `RecordDir` return the git directory it was given — the behaviour before this change — fails
    `TestTrustRecordCoversEveryWorktree` on all three of its claims (nothing in the shared directory, something
    beside the worktree, and the main checkout back to `unrecorded`), and fails
    `TestHookTrustIsRecordedForTheWholeClone` at the CLI. `TestLegacyPerWorktreeRecordStillAnswers` still passes,
    which is what that test is for.
  - Resolving only the client's root back to the worktree's git directory — leaving the record clone-wide — fails
    `TestCloneRootIsSharedByEveryWorktree` alone: the two checkouts disagree on the path, and the main checkout does
    not find the check attached from the worktree. The record tests keep passing, so the two properties are covered
    separately rather than by one assertion.
  - Replacing membership with the old newest-entry equality fails
    `TestWorktreesOnDifferentVersionsBothKeepTheirPosition` twice — the branch reads `changed` once trunk records
    its own version, and again after trunk moves twice — and leaves the other tests passing. That is the ping-pong
    the set exists to prevent, and the failure names it by asking whether one worktree's position may answer for
    another's.

## Known limitations

- **Every clone re-records once.** The permission change alters the tree hash of content nobody edited, so
  acceptances taken before this ship read as `changed` once. That is the mechanism working — the hash it prints
  names bytes it has not hashed before — but it is a one-time cost a reviewer should not be surprised by, and
  cadence's not-yet-adopted hook means no consumer is mid-flight.
- A `changed` report prints the *newest* hash in the set, which may name a version another worktree recorded. The
  sentence now says what the clone recorded rather than what the reader accepted, which is the true statement; it
  still cannot say who, or where.
- `CommonDir` trusts git's `commondir` file. A hand-pointed `GIT_DIR`, or a worktree directory copied rather than
  moved by git, has no such file and keeps the pre-change behaviour: the record lives where you were asked.

## Open questions

- Should `hooks --json` report the whole set (`accepted_hashes`) rather than the newest? Nothing reads it today;
  the field's absence would also be the honest answer to "which version did this clone actually read".
- Is the shared client root worth a `hooks --json` field naming the directory it resolved to, so a reader
  diagnosing "why did this script run here" does not have to know the rule?

## Addressed feedback

Review 7080856 (feedback) left three notes.

- *"we dont really have adoption yet so you can simplify and remove this read fallback"*, on the compatibility
  read. Removed: `readPositions`, `TrustAt`, `AcceptedAt`, `DeclinedAt` and `recordPosition` all take one
  directory now, `Resolution` memoizes one string instead of a slice, and `TestLegacyPerWorktreeRecordStillAnswers`
  is gone. The design paragraph above says why there is nothing to be compatible with.
- *"yes that makes sense to me, for it to be shared by the whole clone"*, on the open question about the
  per-clone root. Done: `CloneRootDir` resolves through `RecordDir`, the help text and both tables name
  `<git-common-dir>`, and `TestCloneRootIsSharedByEveryWorktree` pins that a script attached from one worktree is
  discovered from the other and that the two checkouts agree on the path.
- *"recorded newer seems best"*, on how `changed` should read. The three sentences that said "since you accepted
  them" / "since this clone accepted them" now say "since this clone recorded them", with the parenthetical
  labelled `recorded`. The assertions that guarded them were updated to the new wording rather than relaxed: they
  still require the note and the refusal to name the move and print both hashes.
