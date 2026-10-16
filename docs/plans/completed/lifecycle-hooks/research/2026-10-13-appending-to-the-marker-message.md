# Can a hook append to the marker message instead of adding a trailer key?

Raised in review `70e40ea` on `feat-pre-post-hooks`:

> Maybe it can't replace content in the trailer, but could append/insert content? Then it's not a schema
> change or an official alteration of the contract, but gives clients a way to persist a result.

The answer is yes, with one hard constraint, and the constraint is not where it looks. Measured on this
branch's build, five crafted marker commits plus one injection case.

## How the message is parsed

Two facts set everything up.

`marker.Message.Render` (`internal/marker/marker.go:34`) assembles subject, blank line, body, blank line,
then the trailer lines — one paragraph per region, always separated. Its own comment records why the shape
matters: a message whose trailers do not form their own paragraph has no trailer block as far as git is
concerned, and "every derived lifecycle state silently collapses to WORKING".

`lifecycle.parseTrailers` (`internal/lifecycle/lifecycle.go:713`) receives `%(trailers:only,unfold)`
(`:218`, `:326`, `:344`) — git's own extraction, not the whole message — and it keeps the **first** value it
sees for a key (`:724`: `if _, seen := out[key]; seen { continue }`).

So git decides which lines are trailers, and among duplicates the earliest wins.

## The five cases

Each is a `git-pair: ready` marker on its own branch, varying only what is appended.

|     | Appended                                                         | What git extracted as the trailer block         | Derived state |                                         |
| --- | ---------------------------------------------------------------- | ----------------------------------------------- | ------------- | --------------------------------------- |
| c1  | nothing (control)                                                | `Review-State: ready`, `Review-Changeset`       | `READY`       | baseline                                |
| c2  | a prose line below the block, blank-line separated               | **only** `hook: ran /bin/true, exit 0 at 14:32` | `WORKING`     | the real trailers fell out of the block |
| c3  | `Review-Hook-Result: pass suite 41s` at the end of the block     | all three                                       | `READY`       | safe                                    |
| c4  | `Review-State: working` **after** the real `Review-State: ready` | all three                                       | `READY`       | cannot forge from below                 |
| c5  | `Review-State: working` **before** the real one                  | all three                                       | `WORKING`     | forges                                  |

Two conclusions, one of them a trap.

**Appending cannot forge, provided it lands at the end of the block.** c4 and c5 are the same two keys in
opposite order and produce opposite states. First-wins is the protection, and it only protects keys that
appear _earlier_ than the appended one. So the append position is the security property, not a formatting
preference.

**The obvious implementation fails silently.** c2 is what any script does naturally — `printf '%s\n' "$out"

> > $MSG`puts a blank line and then prose. git then treats the prose as the trailer paragraph and the real`Review-State`and`Review-Changeset`are no longer trailers at all. Nothing errors.`change ready`still
reports success, because the commit it set out to write was written.`queue`simply stops listing the
changeset, and`status`says`WORKING`. A repository can sit on a marker that derives nothing, and the only
> > symptom is an empty queue.

That is the same failure class `Render`'s comment warns about from the other end, and it is the reason the
append has to be done by the renderer rather than by the hook.

## The adjacent question: can reviewer `-m` text reach the same parser?

Worth checking, since `review submit -m` puts arbitrary text into `Message.Body`, which sits above the
trailers. Crafted input:

```text
git pair review submit --approve -m "LGTM, ships it

Review-State: working
Review-Outcome: block
Review-Changeset: cs-inj"
```

git extracted only the real block. Derived state: `APPROVED`, outcome `approve`. The body is a separate
paragraph and git's trailer block is the last one only, so `Render`'s unconditional blank line between body
and trailers is what contains it. No change needed; recorded because the answer is "contained, by that blank
line", not "impossible".

## What makes a paragraph a paragraph in git's eyes

Asked in review on this plan: can a client manufacture a new paragraph with newlines, and what counts as one
anyway. Measured with eight marker commits varying only the appended text. "Same paragraph" means appended
with no blank line; the key column names the shape of the appended line.

| Appended                                  | git's extracted block                                      | Derived   |                                      |
| ----------------------------------------- | ---------------------------------------------------------- | --------- | ------------------------------------ |
| nothing                                   | `Review-State: ready`, `Review-Changeset`                  | `READY`   | control                              |
| `Hook-Test: pass suite`                   | all three                                                  | `READY`   | joins the block                      |
| `note: all good`                          | all three                                                  | `READY`   | key case is not significant          |
| `all tests passed` (no colon)             | **empty**                                                  | `WORKING` | whole block gone                     |
| `␣more indented detail` (leading space)   | real trailers, last value now `cs-q5 more indented detail` | `WORKING` | continuation corrupts a real trailer |
| blank line, then `Hook-Test: pass suite`  | only `Hook-Test`                                           | `WORKING` | real trailers evicted                |
| blank line, then `all tests passed`       | empty                                                      | `WORKING` | new paragraph is not trailers        |
| blank line, then two trailer-shaped lines | empty                                                      | `WORKING` | one line lacks a colon → no block    |

Git's rule, as the results read:

1. The trailer block is the **last paragraph** of the message, a paragraph being a run of non-blank lines.
2. Every line in that paragraph must be trailer-shaped (`key: value`) or a continuation (leading whitespace).
   A single line that is neither — no colon, as in row four — removes the **entire** block, including the
   authoritative trailers above it. Row eight is the same trap in disguise: two plausible-looking trailer
   lines, one of them missing its colon, and nothing is extracted.
3. Key case carries no significance: lowercase `note: all good` was extracted as a trailer.
4. A continuation line appends to the preceding trailer's value. Appended after the real block, it extended
   `Review-Changeset` to `cs-q5 more indented detail`, which then failed the `changeset == slug` test in
   `parseEvent` (`lifecycle.go:377`) and voided the marker.

So the answer to the question is yes, and cheaply: one blank line is enough to make a new paragraph, and the
consequence is not that the client's line sits apart — it is that the authoritative trailers stop being
trailers. Three independent injections destroy the marker, and the third is the quiet one, because the block
still _lists_ `Review-State` and `Review-Changeset` and the damage is inside a value.

Note also that sanitising after the fact does not work: appending a blank line following the client's own
line is row six or row eight. Separation is not a mitigation. Order and shape are.

## The validation list this implies

`Message.Render` already refuses a multi-line subject (`marker.go:41`) and errors on a malformed trailer
(`:57`), so the plumbing is there. A client-supplied append needs four checks, each traceable to a row above:

1. **Reject `\n` and `\r` in the value** — rows six and eight, the manufactured paragraph.
2. **Require the rendered line to be trailer-shaped** — `Key: value`, non-empty key, colon present — row four
   and row eight.
3. **Reject a rendered line beginning with whitespace** — row five, the value corruption that still looks
   healthy.
4. **Reject reserved keys and the `Review-` prefix** — rows c4/c5 above; ordering protects today, and the
   rejection is what keeps a future git-pair key from colliding with a client's chosen name.

Anything rejected is a refusal naming the hook and the row it hit, before the commit is written — not a
silently dropped line, which would leave the repository believing it recorded a result.

## What this changes in the plan

The reviewer's framing is right: appending is not a schema change, needs no migration, and gives clients a
place to persist a result. The recommendation against a _new trailer key that git-pair reads_ stands; this is
a different mechanism, and it is acceptable subject to:

1. **The renderer performs the append, never the hook.** `Message` gains a field for hook-supplied lines
   rendered after `Trailers` in the same paragraph, with no blank line. Combined with the four validations
   above, that makes every destructive row unreachable rather than documented-against — which is the only
   reason to build this at all, since a hook doing it by hand cannot see git's paragraph rule.
2. **Stay out of the `Review-` namespace.** A client-owned prefix (`Hook-`, or `X-Hook-` for the conventional
   "not part of the schema" reading) keeps the reserved prefix reserved, so a key added to git-pair in a later
   release cannot collide with a name a team picked last quarter.
3. **The appended line names the head it describes** — `Hook-Test: pass head=<sha> dur=41s`. A rebase
   preserves the message, so a claim that does not name its commit reads as true about history that no longer
   exists. Naming the head makes it visibly stale, which is the same trick `Review-Head` exists for — and it
   is what makes the client-side gate check in the next section able to test anything.
4. **`check` never reads these keys.** The moment the gate branches on one, it becomes part of the contract,
   and the "not a schema change" argument is gone. Persisting a result and letting it gate anything are
   different things, and only the first is being agreed here.

## The escape hatch, and the three caveats that come with it

Also asked in review: if `check` will not read a client key, is the answer for a team that wants that gate to
be a `pre-check` hook checking its own trailer? Yes — and it is a good pattern, with a property worth naming:
the thing it reads is a trailer in a marker commit, which is history. Unlike everything else about hooks, it
is stable across clones, so this is the best-shaped hook a team can write for CI, not the worst.

Three caveats, all of them the client's to own:

- **Staleness is now their test to write.** A trailer written by an earlier marker can name a head a rebase
  removed, and the message survives the rebase. The hook has to do the ancestry question itself — which is
  why item 3 above is a requirement rather than a nicety.
- **`check` has no override, on purpose.** PRD §19.3 puts it: a gate is the wrong place to acknowledge an
  exception. So a client whose own `pre-check` hook refuses has no flag to proceed past it. They fix the work
  or remove the hook. Worth saying plainly, because it is the asymmetry they will hit first.
- **Absent is a decision, not a default.** A missing trailer can mean the hook never ran, or ran against a
  different head. The client's script has to choose fail-open or fail-closed, and fail-open is how a gate
  quietly stops gating.

And the recursion guard applies here: a `pre-check` hook that shells out to `git pair check` re-enters, so it
inherits `GIT_PAIR_HOOKS=0` for exactly that case.
