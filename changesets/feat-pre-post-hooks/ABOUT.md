# feat-pre-post-hooks

## Summary

Two planning documents for a feature that does not exist yet: repository-configured pre- and post-hooks at
the git-pair lifecycle points, so a team can commit a test suite, a lint pass, a validation or a push and
have it run at the moment a person or an agent currently has to remember to run it.

The change is worth reviewing before any code exists, because one of its decisions reaches PRD §26. The
second document is the measurement that settled whether git's own `pre-commit`/`commit-msg` hooks could be
the mechanism instead of building one, which is the question that had to be answered before committing to a
new subsystem.

No code changes. The feature is not implemented; this is the plan an implementation would follow.

## What changed

- `docs/plans/lifecycle-hooks/plan.md` — the design: hook points, discovery, execution, trust, reporting,
  five milestones (M1 one hook end to end, M2 post-hooks and the remaining author points, M3 the gate, M4
  the reviewer's submission, M5 the spec and skill), plus spikes, risks and a verification strategy.
- `docs/plans/lifecycle-hooks/research/2026-10-13-native-git-hooks.md` — the native-hook measurement, with
  the commands and their raw output so the conclusion can be re-run rather than trusted.

## Design decisions

**Hooks are client control, not repository enforcement.** git-pair publishes lifecycle events; a team, a
reviewer, or one person on one machine decides what they trigger. This is what makes the reviewer's own
hooks legitimate, and it is the principle that resolves the §26 question.

**Every write point gets hooks, including the reviewer's `submit` and the `check` gate.** Five marker
commands plus `check`. Read commands get none: `queue`, `status`, `diff`, `feedback`, `history` and `wait`
are pure derivations with cost-bound tests behind them, and a hook there makes a read cost whatever a
repository wrote. `review about`/`thread` and the review screen get none either — they write files without
recording anything, so a refusal there strands a reviewer mid-edit with no record of why.

**Pre-hooks block, post-hooks report and never reverse.** PRD §26 forbids `reset`, so a failed post-hook
cannot undo the marker it followed. That is forced rather than chosen. A post-hook that moved HEAD is
detected and reported, because a hook that commits invalidates the `Review-Head` a marker just recorded.

**Tracked hooks under `.git-pair/hooks/`, plus a per-clone root under `<gitdir>/`.** The tracked root follows
the `.git-pair/about-template.md` precedent and needs a trust step; the per-clone root is the reviewer's own
discipline attached to a repository they may not own, and needs none. Two roots is what makes this client
control rather than repository policy.

**Hooks may run git verbs git-pair may not.** §26's subject is what git-pair invokes of its own choosing. A
hook body is the client's code responding to a git-pair event, and the client owns it. `internal/hygiene`
therefore keeps its current scope and stays structurally blind to hook bodies, which is correct — but it
gains a fixture asserting the runner itself passes no git verb. M5 task 1 is the one sentence that names
§26's subject; it is a clarification of scope, not a carve-out.

**A hook result gets no review marker trailer.** `Review-Head` and `Review-Diff-Id` are claims about a commit
that `check` re-derives; a hook's exit status is a claim about a machine at a moment, and a rebase preserves
the message while the thing it described stops being true. A team that wants the evidence in the record can
have it without a schema change: `review submit` commits everything in the working tree, so a hook that
writes a log file into the changeset directory lands it in the review.

**Override follows the existing offer/gate split rather than inventing a third answer.** `change ready` has
`--allow-surviving-review-additions` because it is an offer; `check` deliberately has no override because
PRD §19.3 says a gate is the wrong place to acknowledge an exception. Hooks inherit that:
`--allow-hook-failure` on `ready` and `submit`, nothing on `check`.

**Why native git hooks do not replace the runner** (the research document; all four claims measured):

- A native hook refusal gives exit **3**, not 1, and `--json` prints nothing on stdout. The failure arrives
  wrapped in a `*git.Error` by `marker.Commit`, which `Execute` classifies as "git itself failed"
  (`root.go:41`). An agent told 3 goes to fix its git rather than its test suite. This is the finding that
  decides the question.
- `pre-commit` receives no arguments at all, so it cannot distinguish a ready marker from an ordinary
  implementation commit. The only discriminator is the message, visible only at `prepare-commit-msg` and
  `commit-msg`. Dispatch would move into every hook script.
- `check` fires nothing. It writes no commit — and it is one of the points in scope.
- Tracked hooks require `core.hooksPath`, which replaces the entire hook directory (so husky, lefthook and
  pre-commit-framework stop working in that clone) and is not carried by a clone, so a fresh clone silently
  runs no hooks at all.

What git does give for free is taken: `--no-verify` is the override (`Repo.Commit` already takes `extra`),
hook stdin is already a non-tty, and the environment set on the commit reaches every hook — so M1 exports
the resolution into the marker commit's environment anyway, which lets a repository that prefers git's own
mechanism use it without re-deriving the changeset in every script.

## Validation

- `mise run check` (gofmt, `go vet`, sharded test suite) — run on this branch before the handoff; the result
  is stated in the handoff note at the bottom of this file.
- The claims in the research document are experiments, not reasoning. Each was run against a fixture
  repository and the raw output is recorded in the document, so a reviewer can re-run any one of them. The
  exit-code finding was reproduced by driving the built binary (`go build -o /tmp/gp ./cmd/git-pair`) against
  a repository whose `.git/hooks/pre-commit` exits 1: `git pair change ready` printed
  `creating ready marker: git exited with status 1: …` and exited 3, where git-pair's own business refusal
  from the same command exits 1.
- `go test ./internal/cli/ -run Docs` and `TestTheCommandTreeIsTheCommandSet` are unaffected: this changeset
  adds no command or flag name to `PRD.md`, `README.md`, or `skills/**`, which are the documents that test
  checks. `docs/plans/` is not among them.
- Every file and line reference in the plan (`check.go:158`, `integrate.go:155`, `reviewops.go:45`,
  `git.go:982`, `marker.go:182`, `changeset.go:667`, `tidy.go:185`, `root.go:41`) was re-read at its cited
  line rather than quoted from memory.

## Known limitations

- This is a plan, not an implementation. Nothing in `internal/` changes, and no `git pair hooks` command
  exists.
- Line references will drift as `main` moves. They are cited as pointers to code that was read, not as a
  stable address.
- The §26 sentence in M5 is drafted intent, not drafted text. It is the one item that needs agreement from
  the spec's owner, and M1–M4 are written so they can proceed in parallel with it.
- The milestone order assumes the runner is worth building before any single team needs it. If the real
  demand is one team wanting a pre-ready suite, M1 alone may be the whole feature and M2–M4 should wait for
  a request.

## Open questions

1. **Should a hook result be recorded in the review marker?** Recommended no, above. This is the one decision
   that should be settled before M4 rather than during it, because a trailer is a schema change and adding
   one later is a migration.
2. **Do hooks need a manifest?** Default is no — discovery by filesystem, attributes by git config — because
   `parseMetadata` is hand-rolled `key: value` and `go.mod` has no YAML or TOML. Worth revisiting only if
   per-hook timeouts or per-hook blocking are actually wanted.
3. **Is there a form of post-hook that should block?** Deferred: under §26 a blocking post-hook cannot reverse
   anything, so the only difference is the exit code, which the plan already reports nonzero.
4. **Does `--no-cache` interact with hook state?** Expected no — hooks run once per command and nothing about
   them is memoised — but it is an assumption, and the fact-cache keys should be read before M1 is believed.

## Gate result

_To be filled in from `mise run check` immediately before `git pair change ready`._
