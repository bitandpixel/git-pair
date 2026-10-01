# docs-marks-are-remembered-locally

## Summary

Three documents said reviewed marks die with the TUI session. They have not done that
since the mark store landed: marks are written under the git directory per changeset and
commit, and restored when the same commit is reopened. This changeset corrects the
claims and nothing else — no behavior changed, and no behavior needed changing.

## What changed

- `README.md` — the "What it is not" bullet read "persist per-file review checkmarks
  beyond the current TUI session". It now refuses the thing that is actually refused:
  treating a mark as review state. The accurate persistence paragraph further down the
  file (`git-pair/marks/<changeset>/<commit>.json`, keyed per commit with each file's
  diff key beside it) was already right and is untouched.
- `PRD.md` §16 — dropped "For MVP it may be in-memory only" and the paragraph that
  deferred diff-change invalidation to after the minimal TUI. In their place: where the
  marks live, what they are keyed on, the 12-commit retention, and what makes a mark
  stop applying. The `✓ → ○` diagram stays.
- `PRD.md` §26 — the non-goal is now the review record rather than persistence.
- `PRD.md` §27 — file-level progress recorded as delivered; the list narrowed to the
  hunk-level half (hunk state, folding reviewed hunks, jumping to the next unreviewed
  hunk, hunk-level invalidation).
- `internal/tui/session_test.go` — the comment above
  `TestSessionResetsReviewedMarkWhenFileDiffChanges` quoted §16 verbatim, so it quotes
  the new sentence instead of one that no longer exists.

## Design decisions

§16 was rewritten rather than trimmed. The PRD is a living document in this repository —
the archived plans record edits to it alongside README edits — and §16 already describes
shipped behavior in the present tense (the directory row's mark, marking a whole subtree),
so the delivered contract belongs there rather than in a note beside it.

§27 keeps its section with the remaining items rather than deleting it, because the
delivered half is file-level only. Marks expire at file granularity, keyed on the diff of
a file within the span; the hunk-level items are genuinely unwritten.

The two non-goals were restated instead of deleted. "Does not persist checkmarks" was
never the refusal — "is not part of the review record" was, which is what §16 still opens
with and what `git status` cannot see.

## Validation

- `mise run check` green: gofmt, `go vet`, all 14 packages.
- Every claim checked against the implementation before it was written: `reviewmark.New`
  for the path, `reviewmark.Keep` for the retention, `Session.SaveMarks` for the keying
  on the commit the span ends at, and a grep over `internal/cli` for the assertion that
  no command reports marks.
- Evidence class, stated plainly: this is a documentation change and no test reads README
  or PRD text. The only code touched is a comment, so the suite proves nothing about the
  prose beyond compiling — which is why the claims above are cited to functions rather
  than to line numbers.

## Known limitations

§16 now quotes a number that lives in code as `reviewmark.Keep = 12`. Nothing links the
two, so a change to the retention makes the requirement stale — the failure mode that
produced this changeset in the first place.

The prose is also accurate only when read together: "remembered locally" plus "each mark
is stored with the diff key" is what means a rebase forgets marks silently. Read alone,
the first sentence promises more than the pair delivers.

## Open questions

Is 12 commits the right retention, and should it be configurable? It is a guess about how
far back a reviewer returns, and it is now written into §16 as though it were a decision.

The README bullet is an accurate statement about a delivered feature inside a list of
things the MVP deliberately does not do. Worth a reviewer's judgement whether it reads
better where it is or folded into the review-progress prose.
