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

- `mise run check` (gofmt, `go vet`, sharded test suite) — run three times on this branch, most recently
  immediately before this handoff, all exit 0. Recorded under **Gate result**, including which runs came
  from Go's test cache and the check that shows a cached pass is sound here.
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

1. **Should a hook result be recorded in the review marker?** Split, after review: a key git-pair reads is
   still no, and lines a client appends are yes — with the constraint that the append is done by
   `Message.Render`, not by the hook. Full answer under **Review discussion**, and in
   `docs/plans/lifecycle-hooks/research/2026-10-13-appending-to-the-marker-message.md`, which the plan now
   cites from its spike table.

> Maybe it can't replace content in the trailer, but could append/insert content? Then it's not a schema change or an official
> alteration of the contract, but gives clients a way to persist a result. Let's discuss this further.

2. **Do hooks need a manifest?** Default is no — discovery by filesystem, attributes by git config — because
   `parseMetadata` is hand-rolled `key: value` and `go.mod` has no YAML or TOML. Worth revisiting only if
   per-hook timeouts or per-hook blocking are actually wanted.
3. **Is there a form of post-hook that should block?** Deferred: under §26 a blocking post-hook cannot reverse
   anything, so the only difference is the exit code, which the plan already reports nonzero.
4. **Does `--no-cache` interact with hook state?** Expected no — hooks run once per command and nothing about
   them is memoised — but it is an assumption, and the fact-cache keys should be read before M1 is believed.

## Gate result

`mise run check` (gofmt, `go vet`, sharded suite) run three times on this branch, the most recent
immediately before this handoff. All three exited 0.

| Run    | Duration | What it did                                                                                               |
| ------ | -------- | --------------------------------------------------------------------------------------------------------- |
| first  | 77s      | executed the suite: gofmt clean, `go vet` clean, 35 shards reported `ok`, none failed, across 15 packages |
| second | 16s      | all 35 shard results from Go's test cache, 0 failures                                                     |
| third  | 16s      | same — 35 `ok` on `(cached)` lines, 0 failures                                                            |

A cached pass is a real pass by Go's own definition: identical inputs, so the recorded result still holds.
It is not 35 fresh executions, so both are stated rather than the flattering one. What makes the two cached
runs sound is checkable rather than asserted — no Go file has been touched on this branch, and the whole
span is six markdown files:

```text
$ git diff --name-only main...HEAD | grep -E '\.go$'
(none)
```

The thing that would invalidate a cached pass is a change under `internal/` or `cmd/`, and this branch
contains none. Each run tested the working tree as it stood at the time, which was the tree of the following
commit; the runs are listed in order rather than pinned to hashes, because a gate run covers a tree, not a
commit that does not exist yet.

Relevant to a docs-only change in two of those packages: `internal/hygiene`, because the plan proposes a
second subprocess path in shipped source, and `internal/cli`, because it holds the tests that bind `PRD.md`,
`README.md` and the skill to the command tree. Neither is touched by this changeset, and neither was among
any failures.

## Review discussion

### On review `70e40ea`: appending to the marker rather than adding a trailer key

The reframe is right, and it changed the plan. I had rejected recording a hook result as though "record it"
meant one mechanism. It means two, and they get different answers — which is why the spike moved from open
to resolved rather than to agreed.

**What the parsing actually does.** `Message.Render` (`internal/marker/marker.go:34`) emits subject, body,
then trailers as separate paragraphs. `lifecycle.parseTrailers` (`internal/lifecycle/lifecycle.go:713`) is
handed git's own `%(trailers:only,unfold)` — so git, not git-pair, decides which lines are trailers — and on
duplicates it keeps the **first** value it sees (`:724`). I had assumed the risk was body text shadowing a
verdict; git's paragraph rule means the body never enters the block at all. The real risk is positional.

> can a client artificially produce a new "paragraph" via newlines, or what makes a paragraph a paragraph
> in git's eyes?

**Five crafted markers, measured.** Each a `git-pair: ready` marker, varying only what is appended:

| Appended                                          | git's extracted block | Derived   |                         |
| ------------------------------------------------- | --------------------- | --------- | ----------------------- |
| nothing                                           | real trailers         | `READY`   | control                 |
| prose below the block, blank-line separated       | **only the prose**    | `WORKING` | real trailers evicted   |
| `Review-Hook-Result: pass suite 41s` at block end | all three             | `READY`   | safe                    |
| `Review-State: working` **after** the real one    | all three             | `READY`   | cannot forge from below |
| `Review-State: working` **before** the real one   | all three             | `WORKING` | forges                  |

So your instinct holds, and for a better reason than "it isn't a schema change": **appending at the end of
the block cannot forge anything**, because first-wins protects the keys already there. The same two keys in
the opposite order flip the state, so the append position is the security property, not cosmetics.

**The trap is the obvious implementation.** Row two is what a script does naturally — `printf '%s\n' "$out"

> > $MSG`— and it silently kills the changeset. Nothing errors,`change ready`still exits 0 because the
commit it meant to write was written,`queue`stops listing the changeset, and`status`says`WORKING`. A
repository can sit on a marker that derives nothing, with an empty queue as the only symptom. `Render`'s own
> > comment already warns about this failure class from the subject-line end; this is the same collapse from the
> > other end.

**What that does to the design.** The append becomes git-pair's job, not the hook's — `Message` gains a field
rendered after `Trailers` in the same paragraph, which makes the eviction case unreachable instead of
documented-against. Hook-supplied keys matching anything git-pair reads are rejected before the commit, and
the `Review-` prefix stays reserved for git-pair, so a client picks `Hook-` or `X-Hook-` and cannot collide
with a key added in a later release. Values single-line, and the line names the head it describes
(`Hook-Test: pass head=<sha> dur=41s`) so a rebase leaves a claim that is visibly stale rather than silently
true — the same trick `Review-Head` exists for.

**The one thing I'd still refuse, and why it isn't a disagreement about your idea.** `check` must never read
client-appended keys. Everything above is about persisting a result; the moment the gate branches on one, it
becomes part of the contract and "not a schema change" stops being true of it. That's a line between two
different things, not a veto on the mechanism.

> sure and the escape hatch if a customer really wants to do this is have their pre-check hook check that custom trailer yes?

**Also checked, since it was adjacent:** can reviewer `-m` text reach that parser? It sits in `Message.Body`,
above the trailers. Crafted `-m` containing `Review-State: working` / `Review-Outcome: block` derived
`APPROVED` with the real outcome — git's block is the last paragraph only, and `Render`'s unconditional blank
line is what contains it. Contained by that blank line rather than impossible by construction, so it's
recorded, and it's one more reason the renderer owns the shape.

### On the follow-up: what makes a paragraph a paragraph in git's eyes

Yes — cheaply. One blank line is enough, and the consequence is not that the client's line sits apart but
that the authoritative trailers stop being trailers. Measured with eight markers varying only the appended
text; the full table is in the research note, and git's rule reads:

1. The trailer block is the **last paragraph** — a run of non-blank lines.
2. Every line in it must be trailer-shaped (`key: value`) or a continuation (leading whitespace). One line
   that is neither removes the **entire** block, including the real trailers above it.
3. Key case is not significant — lowercase `note: all good` was extracted as a trailer.
4. A continuation appends to the _preceding_ trailer's value.

The four rows that matter:

| Appended                                 | git's block                                                              | Derived   |
| ---------------------------------------- | ------------------------------------------------------------------------ | --------- |
| `Hook-Test: pass suite`                  | all three                                                                | `READY`   |
| `all tests passed` (no colon)            | **empty**                                                                | `WORKING` |
| <code>␣more indented detail</code>       | real trailers, `Review-Changeset` value now `cs-q5 more indented detail` | `WORKING` |
| blank line, then `Hook-Test: pass suite` | only `Hook-Test`                                                         | `WORKING` |

That middle row is the one I had not considered and the one worth your attention: the block still _lists_
`Review-State` and `Review-Changeset`, so it looks healthy in `git log`, while the slug no longer matches and
`parseEvent` (`lifecycle.go:377`) voids the marker. Three independent injections destroy a changeset this
way, all of them silent, all of them with `change ready` exiting 0.

And sanitising afterwards does not help: putting a blank line after the client's own line is row four again.
Separation is not a mitigation, which is the argument for the renderer owning the append — a hook cannot see
this rule, and the four validations in the note each map to a row above.

### On the follow-up: the escape hatch

Yes, exactly — a `pre-check` hook reading their own trailer is the answer, and it deserves naming as a good
pattern rather than a workaround: the thing it reads is a trailer in a marker commit, which is history. So
unlike every other hook in this design, it behaves identically on every clone, which makes it the best-shaped
hook to run in CI rather than the worst.

Three caveats, and all of them land on the client:

- The hook must write its own ancestry test. A rebase preserves the trailer's message, so `Hook-Test: pass`
  can be a true statement about a commit that no longer exists — which is why naming the head in the line is
  a requirement rather than a nicety.
- `check` has no override flag, deliberately (PRD §19.3: a gate is the wrong place to acknowledge an
  exception). A client whose own `pre-check` hook refuses has no flag past it; they fix the work or remove the
  hook. That asymmetry is the first thing they will hit, so it belongs in the skill.
- A missing trailer is a decision, not a default. It can mean the hook never ran or ran against a different
  head, and fail-open is how a gate quietly stops gating.

One implementation note: a `pre-check` hook that shells out to `git pair check` re-enters, so it inherits
`GIT_PAIR_HOOKS=0` for exactly that case.
