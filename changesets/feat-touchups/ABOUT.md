# feat-touchups

`git pair review history` already knew who made each submission and showed it nowhere a reviewer would
look: the name was in `--json`, under a key called `author` — the word git-pair otherwise reserves for the
person who wrote the change. The name is now on the screen as `REVIEWER`, and `--json` calls it `reviewer`.

## Summary

**The table has a `REVIEWER` column.** `review history` printed `INDEX SHA REVIEWED OUTCOME AGE SUBJECT`.
A changeset read by two people could not say which of them submitted which verdict, because the one fact
that distinguishes them — the author of the review commit — was left out of the row.

**`--json` renamed `author` to `reviewer`.** Same value, the value the human table now prints. One word for
one thing, said the same way on both surfaces.

## What changed

**The column and the key** (`internal/cli/review.go`). The header row gained `REVIEWER` between `OUTCOME`
and `AGE`, and the row prints `r.Author`, which `lifecycle` already read from `%an` for every event. The
JSON object's `author` key became `reviewer`. The command's own help says what the column is, because
`git pair review history --help` is where a reviewer checks what they are about to read.

**A test with two reviewers** (`internal/cli/review_test.go`). `TestReviewHistoryNamesTheReviewer` submits a
block as one named reviewer and an approve as another, then reads both surfaces: `reviews[i].reviewer` per
submission, the `REVIEWER` header, both names in the table, and no `author` key anywhere. It also asserts the
table does not contain the fixture's own identity (`gittest.AuthorName`), which is the changeset author's —
the column that appears has to be the reviewer's, not a fallback to the branch. The new `submitAs` helper
sets `GIT_AUTHOR_*` around `review submit` and reads the author back out of the commit with
`git show -s --format=%an`, because a test of a printed name proves nothing if the fixture never changed it.

**The JSON key contract** (`internal/cli/contract_test.go`). `review history`'s row asserted `reviewer`
alongside the other eight keys; the retired `author` was never in that list, which is how the two lists had
drifted from what the command emits.

**The documents.** PRD §10.5's example table gained `REVIEWER`, and `REVIEWED` with it — the code has printed
that column since the review-architecture pass and the spec's example never caught up, so the two columns
would have looked like an invented set. README's command table names both columns, and its
`review history --json` example renames the key and lists the object in the order `encoding/json` emits it.

## Design decisions

**`REVIEWER`, not `AUTHOR`, on the screen.** Everywhere else in git-pair the author is the person who wrote
the change — `status`'s next actions say "author: `git pair change feedback`", the review loop is
author-then-reviewer. A column that holds the reviewer's name under a header reading `AUTHOR` asks the
reader to remember which of the two meanings applies here.

**The JSON key moved with it rather than keeping both.** A row that carried `author` and `reviewer` with one
value would be two contracts to keep, and the disagreement between them is the failure this rename prevents.
Nothing in this repository reads the key: the only readers are README's example, the tests, and
`skills/git-pair/references/cli.md` in the packaged skill, which is a separate repository and still shows
`author` — it needs the same one-word change.

**No `Review-By` trailer.** The submission commit's author is already the reviewer: `review submit` commits
as whoever ran it. A trailer naming the reviewer would be a second answer to one question, free to disagree
with the commit it travels with, and the first would have to win anyway.

**`REVIEWER` sits beside the verdict, not beside the subject.** The column answers *who said this*, so it
belongs with `OUTCOME`; `SUBJECT` stays last because it is the only field of unbounded length.

## Validation

- `mise run check` — gofmt clean, `go vet ./...` clean, `go test ./...` green.
- The two history tests run alone: `go test ./internal/cli/ -run 'TestReviewHistory|TestReviewSubmitRecordsTheReviewedHead'`.
- The table read by eye against the built binary (`mise run build`, then `review history` on a scratch
  changeset with submissions from two identities): `INDEX SHA REVIEWED OUTCOME REVIEWER AGE SUBJECT`.

## Known limitations

- A long commit-author name widens the column and pushes `SUBJECT` right. `tabwriter` sizes columns to the
  widest row, and an author name is not truncated: it is the reviewer's own git identity, and shortening it
  would be guessing where a name ends.
- The name is whatever `git config user.name` said on the machine that ran `review submit`. git-pair does not
  verify identities anywhere, so the column is as trustworthy as `git log`'s author field — which is what it
  reads.
- `--json`'s `author` key is gone, not deprecated alongside `reviewer`. Anything that parsed it reads `null`
  now. It was never documented as stable beyond README's example, and README now says `reviewer`.

## Open questions

- `status`'s `Latest review` section prints `outcome`, `commit` and `reviewed` but no reviewer, so the
  command a reviewer runs most often is the one that still does not say who reviewed. It is out of scope here
  because the ask was the history command; `status --json`'s `latest_review` would take the same one field.
