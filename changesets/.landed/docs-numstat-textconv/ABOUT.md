# docs-numstat-textconv

## Summary

The preview asks git twice about one file: once for the patch, once for the `+N −M` beside its name. The
patch call passes `--no-textconv`; the counts call does not. That asymmetry reads like a bug — as though a
repository with a `textconv` driver could get a header whose numbers describe converted bytes while the diff
under it describes the file — so the next reader either "fixes" it or wastes an hour proving it need not be
fixed. Measured here: git computes `--numstat` from the two sides it is comparing and never consults the
filter, so the two calls already answer the same question. The change is a comment on the counts call saying
that, and what makes it true, so the asymmetry stops inviting a repair. No behaviour changes, and no flag git
would discard is added.

## What changed

- `internal/tui/session.go` — one comment on the `diff --no-ext-diff --numstat` call inside the session's diff:
  why this call takes no `--no-textconv`, and what would have to change for that to stop being true.

No flags, no behaviour, no docs beyond this file.

## Design decisions

### The claim is git's, so it is stated as git's and shown by measurement

The comment says git works numstat out from the two sides being compared rather than from what a filter turns
them into. That was checked rather than assumed, in two throwaway repositories each with a `.gitattributes`
filter that hides the change from the patch:

```
textconv = head -n 1     git diff            →  no changed rows (the first line is untouched)
                         git diff --numstat  →  2  0
                         with --no-textconv  →  2  0

textconv = tr a-z A-Z    git diff --numstat  →  2  1
                         with --ext-diff     →  2  1
```

A filter that empties the patch leaves the counts where they were, which is the only disagreement the header
could have suffered from. The comment is written as git's behaviour, not as git-pair's, because nothing in this
repository produces those numbers — git does.

### Passing the flag anyway was the cheaper mistake

`--no-textconv` on the counts call would be harmless at run time and cheap to write, and it would make the two
calls read as one query. It would also be an argument git ignores, sitting in a list of arguments that are all
load-bearing, where the next reader has to work out which kind it is. A comment costs the same lines and says
something true.

### The invariant is worth keeping, so the comment says what would break it

The property the pane depends on is that the size beside a path describes the diff beneath it. That holds today
by git's construction. The comment states the mechanism, so if a future git starts applying conversions to
numstat the code has a sentence to check against, and the reader learns the flag was left out on purpose rather
than by omission.

## Validation

- `mise run check`: gofmt, `go vet`, and the unit suites pass — comment-only, so nothing new asserts.
- `mise run gates`: unit suites plus `e2e-29` and the pty walkthrough, all passing, so the claim was not
  written against a tree that no longer builds.
- The measurements above were taken by hand in temporary repositories, not committed: they describe git, not
  this repository, and a test that asserts another tool's behaviour would fail on someone else's git for a
  reason that has nothing to do with a review.

## Known limitations

- The comment is a claim about git, and git can change. It is written as git's behaviour for exactly that
  reason, but nothing here re-checks it. Should git ever apply `textconv` to `--numstat`, the header counts
  would disagree with the patch underneath them and the pane would show it — which is the failure the comment
  predicts, not one it prevents.
- No test pins the invariant. A fixture with a `textconv` driver could: it would assert the counts the pane
  shows while a filter empties the patch. It would pass with or without any flag, because the behaviour is
  git's, so it guards against a future git or a refactor that drops one of the other flags rather than against
  today's code.

## Open questions

- None for this change. If the repo later wants the invariant pinned in CI, the fixture test described above
  is the shape of it.
