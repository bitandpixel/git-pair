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

| | Appended | What git extracted as the trailer block | Derived state | |
| --- | --- | --- | --- | --- |
| c1 | nothing (control) | `Review-State: ready`, `Review-Changeset` | `READY` | baseline |
| c2 | a prose line below the block, blank-line separated | **only** `hook: ran /bin/true, exit 0 at 14:32` | `WORKING` | the real trailers fell out of the block |
| c3 | `Review-Hook-Result: pass suite 41s` at the end of the block | all three | `READY` | safe |
| c4 | `Review-State: working` **after** the real `Review-State: ready` | all three | `READY` | cannot forge from below |
| c5 | `Review-State: working` **before** the real one | all three | `WORKING` | forges |

Two conclusions, one of them a trap.

**Appending cannot forge, provided it lands at the end of the block.** c4 and c5 are the same two keys in
opposite order and produce opposite states. First-wins is the protection, and it only protects keys that
appear *earlier* than the appended one. So the append position is the security property, not a formatting
preference.

**The obvious implementation fails silently.** c2 is what any script does naturally — `printf '%s\n' "$out"
>> $MSG` puts a blank line and then prose. git then treats the prose as the trailer paragraph and the real
`Review-State` and `Review-Changeset` are no longer trailers at all. Nothing errors. `change ready` still
reports success, because the commit it set out to write was written. `queue` simply stops listing the
changeset, and `status` says `WORKING`. A repository can sit on a marker that derives nothing, and the only
symptom is an empty queue.

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

## What this changes in the plan

The reviewer's framing is right: appending is not a schema change, needs no migration, and gives clients a
place to persist a result. The recommendation against a *new trailer key* stands; this is a different
mechanism and it is acceptable, subject to:

1. **The renderer performs the append, never the hook.** `Message` gains a field for hook-supplied lines
   rendered after `Trailers` in the same paragraph, with no blank line. That makes c2 structurally
   unreachable instead of documented-against.
2. **Reject reserved keys in hook input.** Any key git-pair reads (`Review-Outcome`, `Review-State`,
   `Review-Changeset`, `Review-Head`, `Review-Parent-Head`, `Review-Base-Head`, `Review-Diff-Id`) is refused
   before the commit. c4 shows the ordering already protects against them, but a client key that happens to
   match a key added to git-pair in a later release is the collision to close now, not discover later.
3. **Stay out of the `Review-` namespace.** A client-owned prefix (`Hook-`, or `X-Hook-` for the
   conventional "not part of the schema" reading) keeps the reserved prefix reserved.
4. **One line per value.** A newline in hook-supplied text creates a second trailer line; a blank line ends
   the block. Reject rather than fold.
5. **The appended line should name the head it describes** — `Hook-Test: pass head=<sha> dur=41s`. A rebase
   preserves the message, so a claim that does not name its commit reads as true about history that no longer
   exists. Naming the head makes it visibly stale, which is the same trick `Review-Head` exists for.
6. **`check` must never read these keys.** The moment the gate branches on one, it becomes part of the
   contract, and the "not a schema change" argument is gone. Persisting a result and letting it gate anything
   are different things, and only the first is being agreed here.
