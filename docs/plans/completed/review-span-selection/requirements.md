# gitpr Review Span Selection — Requirements Handoff

Received from the project owner as a feature handoff. Reproduced except for heading levels, with two
amendments added during execution at the owner's request: §8.1, and the rewrite of Scenario A that
followed from it. §24 and §30 still describe `v` as a two-state toggle; §8.1 supersedes them, and they
are left as received so the handoff can be read as it was sent. There is no "audit notes" section in
this file — what was verified, and what the audit found, is in [plan.md](plan.md) and
[audits/](audits/).

## 1. Purpose

Add first-class review-span selection to `gitpr`.

A review span represents:

```text
BASE → HEAD
```

and determines which changes are shown in the review TUI and by `gitpr diff`.

Users should be able to move both ends of the span through natural review checkpoints and advanced
Git checkpoints.

The common cases must remain extremely simple:

```text
Unreviewed:
latest review → working tree

Full changeset:
changeset base → working tree
```

while advanced users can inspect arbitrary historical ranges.

---

## 2. Core Model

A span consists of:

```text
base checkpoint
head checkpoint
```

Examples:

```text
Review -1 → Working Tree
Changeset Base → Working Tree
Review -3 → Review -1
Commit abc123 → Commit def456
main → Working Tree
origin/main → Review -1
```

The selected head determines whether the session is an active review or historical inspection.

---

## 3. Head Modes

There are two fundamental modes.

### 3.1 Live Review Mode

A span is in live review mode only when:

```text
head = Working Tree
```

Example:

```text
Review -1 → Working Tree
```

Capabilities:

* inspect diffs,
* launch difftool,
* edit current working-tree files,
* edit `ABOUT.md`,
* create/update review threads,
* mark files reviewed/unreviewed,
* submit a review.

This is the normal review workflow.

### 3.2 Historical Mode

A span is historical whenever the selected head is anything other than the working tree.

Examples:

```text
Review -3 → Review -1
main → Review -1
Commit abc123 → Commit def456
```

Historical mode is read-only.

Capabilities:

* inspect diffs,
* browse files,
* use difftool against historical snapshots,
* change span.

Unavailable:

* editing product files,
* editing `ABOUT.md`,
* creating/updating review threads,
* marking files reviewed,
* submitting reviews.

The UI must clearly indicate that the session is read-only.

Example:

```text
Span: Review -3 → Review -1

HISTORICAL · READ ONLY
```

---

## 4. Checkpoint Types

Both base and head selectors should support structured checkpoint types.

### 4.1 Base Checkpoints

Supported base selections:

```text
Changeset Base
Review 0
Review 1
...
Review -2
Review -1
Commit…
Ref…
```

### 4.2 Head Checkpoints

Supported head selections:

```text
Working Tree
Review 0
Review 1
...
Review -2
Review -1
Commit…
Ref…
```

Do **not** expose `HEAD` as a normal first-class picker option.

Advanced users who need the exact current `HEAD` commit can select it through `Commit…` or `Ref…`.

This avoids presenting two similar-looking "current" targets:

```text
HEAD
Working Tree
```

when only the working tree is editable.

---

## 5. Review Index Semantics

Review submissions have stable chronological indices.

Example history:

```text
Review 0
Review 1
Review 2
Review 3
```

Negative aliases are relative to the complete changeset review history:

```text
Review -1 = Review 3
Review -2 = Review 2
Review -3 = Review 1
```

These aliases must remain stable regardless of the currently selected head.

Changing the head to `Review 1` must not cause `Review -1` to suddenly mean `Review 1`.

Review indices always refer to the complete known review history for the changeset.

---

## 6. Default Span

The default span for an active changeset depends on whether reviews already exist.

### 6.1 No prior reviews

Use:

```text
Changeset Base → Working Tree
```

### 6.2 Prior review exists

Use:

```text
Review -1 → Working Tree
```

This is equivalent to:

```text
--unreviewed
```

---

## 7. Quick Span Toggle

The existing quick-toggle behavior remains:

```text
v
```

toggles between:

```text
Unreviewed
Review -1 → Working Tree
```

and:

```text
Full changeset
Changeset Base → Working Tree
```

If no prior review exists, both may resolve to the full changeset span.

This is the primary lightweight span interaction.

---

## 8. Custom Span Picker

The advanced span picker is opened with:

```text
V
```

Uppercase `V` is intentionally paired with lowercase `v`:

```text
v    walk the spans this session has been in
V    advanced/custom span selection
```

### 8.1 Session span history

`v` is not a two-state switch. It steps through the spans this session has been in, in the
order they were first entered, and wraps:

* the span the session opened on, including one named entirely by command-line arguments,
* the full-changeset and unreviewed presets,
* every span chosen with `V`.

It is a set of spans, not a log of keystrokes: choosing the same span twice is one stop, and a
rescan after an editor or difftool closes is not a visit. The two presets are present from the
first press so the documented full/unreviewed toggle still works before anything custom has
been chosen (PRD §17.2). From a read-only span, `v` goes back to the last span that was
reviewable rather than to whatever sits next, because that is what `v` is advertised for there
(§13); comparing two historical spans is what `V` is for. A stop whose checkpoint no longer
resolves leaves the session where it was.

The picker uses a two-column model.

Example:

```text
Review Span

BASE                          HEAD
> Review -1                   Working Tree
  Review -2                   Review -1
  Review -3                   Review -2
  Changeset Base              Review -3
  Commit…                     Commit…
  Ref…                        Ref…

Selected:
Review -1 → Working Tree
LIVE · EDITABLE

Tab       switch column
j/k       navigate
Space     select highlighted checkpoint
Enter     apply selected span
u         preset: Unreviewed
f         preset: Full changeset
Esc/q     cancel
```

The exact visual layout may differ, but base and head must be independently understandable.

---

## 9. Span Picker Interaction

Inside the picker:

```text
Tab       switch active column
j/k       move through options
Space     choose highlighted checkpoint
Enter     apply the full base/head pair
u         set Unreviewed preset
f         set Full Changeset preset
Esc/q     cancel without applying
```

`Space` changes the pending selection.

`Enter` applies the full base/head pair.

This allows the user to configure both endpoints before changing the active review.

---

## 10. Commit Picker

Choosing:

```text
Commit…
```

opens a searchable commit selector.

Example:

```text
Pick Commit

> locking

e31aa72  agent: address locking review         31m
8ab932f  review: booking-transaction            35m
16fc0ca  agent: implement locking               1h
72a441e  refactor booking repository            3h
```

Requirements:

* show short SHA,
* show subject,
* preferably show age/date,
* support search/filtering,
* permit direct SHA input,
* return an immutable commit checkpoint.

A commit checkpoint does not drift.

---

## 11. Ref Picker

Choosing:

```text
Ref…
```

opens a searchable named-ref picker.

It may contain:

```text
LOCAL BRANCHES
  main
  booking-transaction
  feature/foo

REMOTE REFS
  origin/main
  origin/feature/foo

TAGS
  v0.4.0

OTHER REFS
  refs/reviews/booking-transaction
```

Requirements:

* show friendly shortened names where unambiguous,
* preserve enough information to identify the actual Git ref,
* support search/filtering,
* support direct ref entry,
* support local branches,
* support remote-tracking refs,
* support tags,
* support arbitrary/custom refs where Git can resolve them.

---

## 12. Ref Checkpoint Semantics

Refs have different semantics from immutable commits.

When the user chooses:

```text
main
```

the selected checkpoint should retain both:

```text
ref identity:    main
resolved commit: abc123
```

Conceptually:

```text
RefCheckpoint {
    ref_name: "refs/heads/main"
    resolved_oid: "abc123..."
}
```

The active review span uses the pinned `resolved_oid`.

The ref identity is retained so `gitpr` can detect whether the ref moves later.

Do not immediately normalize the checkpoint into a plain immutable commit and discard the ref
identity.

Do not automatically follow ref movement during an active review session.

---

## 13. Ref Drift Detection

While a ref-backed checkpoint is active, `gitpr` should periodically or opportunistically resolve the
ref again.

Examples of appropriate refresh opportunities include:

* after returning from `$EDITOR`,
* after returning from `git difftool`,
* after explicit TUI refresh,
* after operations known to potentially update Git refs,
* while redrawing/reloading repository state if inexpensive.

If:

```text
selected:
main @ abc123
```

and Git now reports:

```text
main → def456
```

the active span remains pinned to:

```text
abc123
```

until the user explicitly refreshes it.

`gitpr` must surface the drift.

Example banner:

```text
⚠ main moved abc123 → def456    [r] refresh
```

The notification should preferably be non-modal.

The user must be able to continue reviewing the original pinned span.

---

## 14. Refreshing a Drifted Ref

When the user chooses to refresh a drifted ref:

```text
r
```

or an equivalent contextual action, `gitpr` updates:

```text
main @ abc123
```

to:

```text
main @ def456
```

The span is recomputed using the new resolved commit.

The UI should report the refresh:

```text
Span refreshed:
main abc123 → def456
```

Any local review-progress state affected by the changed diff must be invalidated.

For MVP, it is acceptable to conservatively reset all per-file reviewed markers when either span
endpoint changes.

Example:

```text
Span refreshed to main @ def456.
3 reviewed-file markers were reset.
```

---

## 15. Why Refs Are Pinned Per Session

This behavior intentionally sits between two alternatives.

Do not make refs permanently immutable by converting them to commits at selection time.

Do not automatically follow refs when they move.

Instead:

> Named refs are pinned when selected, monitored for drift, and advanced only after explicit user
> refresh.

This provides:

* stable review semantics,
* no silent changes to already-reviewed files,
* visibility when branches/remotes move,
* intuitive behavior after fetch/pull,
* user control over when the active comparison changes.

---

## 16. Friendly Ref Display

The TUI should continue showing the ref name as the primary identity.

Example:

```text
Span:
main → Working Tree
```

rather than:

```text
abc123 → Working Tree
```

The pinned commit may be shown secondarily:

```text
Span:
main (abc123) → Working Tree
```

If drift exists:

```text
Span:
main (abc123) → Working Tree

⚠ main now points to def456    [r] refresh
```

---

## 17. Top-Bar / Header Representation

The current span should always be visible.

Live review:

```text
booking-transaction

Span: [Review -1] → [Working Tree]    LIVE
```

Ref-based live review:

```text
Span: [main @ abc123] → [Working Tree]    LIVE
```

Historical:

```text
Span: [Review -3] → [Review -1]    HISTORICAL · READ ONLY
```

Drifted ref:

```text
Span: [main @ abc123] → [Working Tree]    LIVE

⚠ main moved to def456    [r] refresh
```

---

## 18. Editing Rules

Editing is allowed only when:

```text
head == Working Tree
```

When live:

```text
e
```

may open the current working-tree file in the configured editor.

Review-document commands such as:

```text
a
t
T
```

remain available.

When historical, editing actions must be unavailable.

The action should either:

* be hidden,
* be disabled,
* or show:

```text
Editing is only available when the review head is Working Tree.
```

Do not silently edit historical temp copies.

---

## 19. Difftool Behavior

### 19.1 Live Review

For:

```text
BASE → Working Tree
```

the difftool should compare the resolved base checkpoint against the current working tree.

If the base is a ref checkpoint, use its pinned OID rather than re-resolving the ref at launch time.

This ensures the external difftool sees the same span as the TUI.

### 19.2 Historical Review

For:

```text
BASE → HISTORICAL_HEAD
```

both endpoints resolve to immutable commit OIDs for the duration of the selected span.

Temporary snapshots are acceptable because historical mode is explicitly read-only.

---

## 20. File Review State

File-level reviewed/unreviewed state applies only to live review mode.

Example:

```text
○ service.go
✓ repository.go
○ service_test.go
```

When switching to historical mode:

* hide or disable review-state toggling,
* do not interpret historical inspection as review completion.

Any span refresh or endpoint change may conservatively reset file-review state in MVP.

---

## 21. CLI Span Semantics

The CLI and TUI must share one canonical span-resolution implementation.

Existing behavior:

```bash
gitpr diff                                    # Changeset Base → Working Tree
gitpr diff --unreviewed                       # Review -1 → Working Tree
gitpr diff --since-review=-3                  # Review -3 → Working Tree
```

Future explicit checkpoint selection should use the same internal model.

Potential syntax:

```bash
gitpr diff --base-review=-3
gitpr diff --base-ref=main
gitpr diff --base-commit=abc123

gitpr diff --head-review=-1
gitpr diff --head-ref=origin/main
gitpr diff --head-commit=def456
```

Exact public CLI syntax may be refined later.

The important requirement is one canonical span/checkpoint implementation.

---

## 22. Span Representation in Code

Implement a first-class span model.

Conceptually:

```text
Span {
    Base Checkpoint
    Head Checkpoint
}
```

Checkpoint types should remain explicit.

Conceptually:

```text
Checkpoint =
    ChangesetBase
    WorkingTree
    Review(index)
    Commit(oid)
    Ref(name, resolvedOid)
```

A ref checkpoint must preserve both:

```text
symbolic ref identity
pinned resolved OID
```

The span layer should expose operations such as:

```text
Resolve()
IsLive()
IsHistorical()
CanEdit()
CanSubmitReview()

CheckRefDrift()
RefreshRef()
```

Do not scatter ref-resolution/drift logic throughout the TUI.

---

## 23. Mode Capability Matrix

| Head         | Mode       | Diff | Edit Code | Edit Review Artifacts | Mark Reviewed | Submit Review |
| ------------ | ---------- | ---: | --------: | --------------------: | ------------: | ------------: |
| Working Tree | Live       |  Yes |       Yes |                   Yes |           Yes |           Yes |
| Review N     | Historical |  Yes |        No |                    No |            No |            No |
| Commit       | Historical |  Yes |        No |                    No |            No |            No |
| Ref          | Historical |  Yes |        No |                    No |            No |            No |

A ref used as the base with Working Tree as head remains live/editable.

A ref used as the head produces historical/read-only mode.

---

## 24. Primary Keybindings

Global review TUI:

```text
v        toggle Unreviewed ↔ Full Changeset
V        open advanced span picker
s        submit review

j/k      navigate files
Enter    open selected file in difftool
e        edit selected current file
Space    toggle reviewed

a        open ABOUT.md
t        create thread
T        browse threads

q        quit
```

When a ref drift notification is present:

```text
r        refresh drifted ref checkpoint
```

Exact handling of `r` may be contextual.

---

## 25. Span Picker Keybindings

Inside `V`:

```text
Tab       switch Base / Head
j/k       navigate
Space     choose highlighted checkpoint
Enter     apply span

u         preset: Unreviewed
f         preset: Full Changeset

Esc/q     cancel
```

---

## 26. Interaction With Existing Review Semantics

Existing review boundaries remain unchanged.

Example:

```text
A -- R1 -- B -- C -- R2 -- D
```

Then:

```text
Unreviewed:            R2 → Working Tree
Since review -2:       R1 → Working Tree
Historical:            R1 → R2
Advanced ref base:     main@abc123 → Working Tree
Advanced commit base:  B → R2
```

All use the same span abstraction.

---

## 27. Interaction With Surviving Review Additions

Span selection does not alter lifecycle validation.

Commands:

```bash
gitpr change ready
gitpr review close
```

still inspect additions introduced by the latest review submission.

The currently selected TUI span has no effect on that validation.

Ref drift also has no effect on which review commit is considered the latest lifecycle boundary.

---

## 28. MVP Non-Goals

Do not implement:

* editing historical commits,
* automatic ref following,
* silent span mutation when refs move,
* persistent arbitrary span bookmarks,
* multiple simultaneous spans,
* cherry-picking from historical views,
* history rewriting from the TUI,
* DAG visualization,
* multi-reviewer span filtering,
* author-specific review spans,
* complex semantic checkpoint inference.

---

## 29. Acceptance Scenarios

### Scenario A: Walking the session's spans

The session opened on `Review -1 → Working Tree` (named on the command line), so the ring is
that span plus the two presets. Pressing `v` steps to the next stop, and the status names both
the span and its position: `span main..HEAD (2 of 3)`. A turn of the ring arrives back at the
span it started from. With nothing but the two presets on the ring — the ordinary case — `v` is
the full/unreviewed toggle PRD §17.2 describes, and the position is left out.

### Scenario B: Custom historical span

User presses `V`, chooses Base: `Review -3`, Head: `Review -1`, presses Enter. Result:
`Review -3 → Review -1`, `HISTORICAL · READ ONLY`.

### Scenario C: Advanced ref base

User selects Base: `Ref… → main`, Head: `Working Tree`. At selection `main → abc123`. Active span:
`main @ abc123 → Working Tree`.

### Scenario D: Ref moves

During review `main: abc123 → def456`. The active diff remains based on `abc123`. The TUI displays
`⚠ main moved abc123 → def456    [r] refresh`. Existing review progress remains valid for the pinned
span.

### Scenario E: User refreshes

User presses `r`. The base becomes `main @ def456`. The diff is recalculated. Affected review-state
markers are reset.

### Scenario F: User ignores drift

The review continues against `main @ abc123` until the session/span changes.

### Scenario G: Immutable commit

User chooses `Commit… → abc123`. No drift tracking occurs. The checkpoint remains permanently pinned
to `abc123`.

---

## 30. Summary

The primary UX remains:

```text
v    quick toggle Unreviewed / Full
V    advanced span picker
```

The advanced picker supports:

```text
Changeset Base
Review N
Commit…
Ref…
Working Tree
```

`HEAD` is intentionally omitted from the normal picker.

Refs use hybrid semantics:

> **Keep the ref identity, pin its resolved commit for the active span, detect later drift, and
> require explicit refresh before changing the comparison.**

This keeps active reviews stable without making branch/ref selection feel unexpectedly immutable.
