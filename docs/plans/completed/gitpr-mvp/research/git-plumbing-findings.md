# Spike findings: Git plumbing for gitpr MVP

Validated against `git 2.43.0` on Linux. Reproduce with `spike-git-plumbing.sh` in this
directory; edge cases (binary/rename/deleted/NUL bytes) in `/tmp` spikes reproduced below.

## Confirmed working

| Need | Command | Notes |
| --- | --- | --- |
| Read one trailer in log | `git log --format='%(trailers:key=GitPR-Outcome,only,unfold)'` | Works. Use `%x09` (not `%09`) for tab separators in `--format`. |
| Changeset diff span | `git diff <base>...HEAD` | Three-dot == `merge-base(base,HEAD)..HEAD`. Matches PRD §17.1. |
| Review-relative span | `git diff <review>..HEAD` | Two-dot. Matches PRD §17.2/§17.3. |
| Enumerate branch-only history | `git log --reverse <base>..HEAD` | Two-dot set equals merge-base-anchored set. |
| Empty review commit (`--approve`) | `git commit --allow-empty` | Works with multi-line message + trailers. |
| Construct trailer block | `git interpret-trailers --trailer 'K=V'` | Clean round-trip; avoids hand-formatted messages. |
| Lifecycle commit classification | `%(trailers:only,unfold)` empty ⇒ implementation commit | Gives PRD §12 "implementation commit invalidates marker" for free. |
| Durable review ref | `git update-ref refs/reviews/<cs> <sha>` | Verified: after `branch -D`, the full 5-commit chain stayed reachable via the ref. |
| List review refs | `git for-each-ref --format='%(refname:short) %(objectname:short) %(committerdate:relative)' refs/reviews` | Good fit for `review queue`. |
| Missing ref probe | `git rev-parse --verify -q refs/reviews/x` | Exit 128; use `-q` to suppress stderr. |
| Dirty tree check | `test -z "$(git status --porcelain -uall)"` | Reports ` M` and `??`; respects `.gitignore`. |
| Branch → slug | `tr '/' '-'` then squeeze/strip non `[A-Za-z0-9._-]` | Deterministic: `feature/x/y` → `feature-x-y`. |
| Read HEAD blob | `git show HEAD:<path>` | Fails cleanly for deleted paths. |

## Added-line extraction (basis of the surviving-additions diagnostic)

`git diff --no-color -U0 <R>^ <R>` parsed line-by-line gives `(path, added text)`.
Parser rules that matter:

1. Path comes from `diff --git a/<old> b/<new>` — take the `b/` side.
2. Line number from the `@@ ... +<n>` hunk header; blank-only additions are excluded
   from the check (they are noise and produce false positives).
3. Skip lines starting with `\` (`\ No newline at end of file`).
4. **Rename detection must be disabled or handled**: `diff --git a/b.txt b/c.txt`
   plus `rename from/to` headers are fine for `-U0`, but `--numstat` reports renames as
   `b.txt => c.txt`, which breaks naive path splitting. Use `--find-renames` off
   (`--no-renames`) for the surviving-additions diagnostic so a rename is seen as
   delete + add; that is the conservative, correct reading.
5. **Binary detection is `--numstat` `-  -  <path>`**, not text scanning. Verified with
   NUL-containing files: `git diff -U0` emits `Binary files ... differ` and **no** `+`
   data lines, so binaries cannot produce false survivors — but they are still skipped
   explicitly via numstat so a future `diff.binary` config change cannot corrupt the check.

Survival test: `text in git show HEAD:<path>`.splitlines()` using exact (non-trimmed)
line content; report `path:<line-number-at-HEAD>`.

## Design problem found (needs a product decision)

The diagnostic as specified in PRD §19.1 includes `ABOUT.md` and review-thread text.
Measured on a realistic review commit that created the changeset directory:

```
added=6  surviving_all=6  surviving_impl=2
   IMPL a.txt:2                     '// review comment here'
   ART  changesets/cs/ABOUT.md:1    '# About'
   ART  changesets/cs/ABOUT.md:3    '## Validation'
   ART  changesets/cs/ABOUT.md:5    '- unit tests'
   ART  changesets/cs/CHANGESET.yaml:1 'base: main'
   IMPL e.txt:1                     'gone'
```

4 of 6 "surviving additions" were changeset artifacts. Worse, the failure mode is
structural rather than incidental:

- A reviewer creates `thread.md` (5 new lines). The author responds by **appending**
  `## Response`. Appending does not alter the original lines, so they still survive ⇒
  `gitpr change ready` fails forever on any thread the author merely answered.
- `change init`/first review creates `ABOUT.md` and `CHANGESET.yaml`; every line survives
  by definition unless the author rewrites the reviewer's description text.

So a literal implementation of §19 makes `change ready` fail on almost every real
changeset and pushes users toward reflexive use of
`--allow-surviving-review-additions`, which destroys the safety property the check exists
to provide.

Recommended resolution (see plan D3): scope the blocking set to paths **outside**
`changesets/<changeset>/`, and report surviving additions *inside* the changeset directory
as a separate, non-blocking "review artifacts still present" list. Rationale: the
mechanism exists to stop forgotten **code** feedback (`// Please use a transaction here`)
from silently returning to the reviewer; `ABOUT.md` and threads are conversation surface
whose survival is normal and is already visible in the `--unreviewed` diff span.

## Other decisions informed by the spike

- **State derivation** (PRD §12): read `<base>..HEAD` newest-first; the first commit
  carrying `GitPR-Outcome:` or `GitPR-State:` decides the state; commits with no GitPR
  trailers after it ⇒ `WORKING`. No mutable state file needed.
- **Ready marker staleness**: the ready marker is a commit, so "later implementation
  commit makes it stale" falls out of ordering with no extra logic.
- **Queue**: enumerate changeset dirs on disk + resolve each one's branch; a changeset is
  queue-eligible when its derived state is `READY`. No index file needed.
- **Review index semantics**: `git rev-list --reverse --grep='^GitPR-Outcome: '` gives an
  ordered array; index `i` ⇔ `count-1-i` for negative indexes.
- **Root-commit review** (review as the first commit) needs `git show --format= -U0`
  instead of `<R>^`; detect via `git rev-parse --verify -q <R>^`.
