# What a declared head is told to do next

Captured 2026-10-14, from a session in the main checkout on `feat/ci-verify-cmd`, whose changeset
`feat-ci-verify-cmd` was approved and declared.

## The observation

```console
$ git pair check
OK: feat-ci-verify-cmd is integration-ready
head:  ff79a41
declared: ff79a41 (`git pair change integrate`)
next:  `git pair check`, then merge into main with ordinary git
```

The report was "that looks like the old note" — the second line the last changeset added, the hand-off, was
not there. Two explanations fit that output, and they have opposite meanings: an old binary, or a new binary
in a state where the line is deliberately absent.

## It was the new binary

```console
$ ~/.local/bin/git-pair --version
git-pair version 0.0.0-dev+ef5fa30
```

`ef5fa30` carries `d93cc34` — the merge that brought in the hand-off line — as an ancestor:

```console
$ git merge-base --is-ancestor d93cc34 ef5fa30 && echo contains
contains
```

So the screen had the code to print the second line and chose not to.

## And it was the right choice

The head the gate reported was the declaration commit itself:

```console
$ git log -1 --format=%B ff79a41
git-pair: integrate feat-ci-verify-cmd

Review-State: integrating
Review-Changeset: feat-ci-verify-cmd
Review-Head: 600b5907f4f0af41f500f996fb6f632f9bd4bda5
```

`alreadyDeclared(s)` is "HEAD is the declaration commit itself", the test `git pair change integrate` uses to
write nothing on a re-run and the test `declarable` uses to keep the hand-off line off a head already handed
over. The absence was correct, and it is a tested state
(`TestHandoffLineStaysQuietWhereTheDeclarationIsRefused`, "head already declared").

## What was actually wrong

Nothing on the screen said what a declared head is waiting for. Three surfaces, three answers:

| Surface | Declared head | Correct? |
| --- | --- | --- |
| `git pair check` | `` `git pair check`, then merge into main with ordinary git `` | no — the marker says the request is written; the step is the push |
| `git pair status` | `push the branch so whoever merges can see the request; then <landing sentence>` | half — the push is named whether or not the branch is on the remote, and it ends on the author's own merge |
| `git pair change integrate` (re-run) | prints `already declared … nothing recorded` and no next step | no — it computes `out.NextAction` and drops it on that path |

`grep` put the declared-path logic in two of the three files:

```console
$ grep -n "StateIntegrating\|integrateNextAction\|alreadyDeclared" internal/cli/{status,check,integrate}.go
internal/cli/status.go:717:  case model.StateIntegrating:
internal/cli/integrate.go:176:       if at := alreadyDeclared(s); at != "" {
internal/cli/integrate.go:281:               out.NextAction = integrateNextAction(s, dest)
internal/cli/integrate.go:317: func integrateNextAction(s *session, dest changeset.Destination) string {
```

`check.go` had none of them in its next-step path: `out.NextAction = landingNextAction(lc)` ran whatever the
marker said.

## The convention to follow

The `READY` state already spells a wait, and it names a read rather than a loop:

```text
waiting for a reviewer: `git pair review open` (author: `git pair change wait` to block on it)
```

and `change wait`'s timeout says the same kind of thing:

```text
still waiting for review activity; run `git pair change wait` again or check `git pair status --json`
```

There is no blocking command for a merge — `change wait` belongs to review (§9.4), and landing has no command
of its own (§13) — so the waiting sentence names the read: `git pair status --changeset <id>`, which reports
the landing when it happens.

The author's own merge stays available. `next_action` carries one instruction, so it goes on the human screens
as a second line, mirroring the hand-off line's shape: `or merge it yourself with ordinary git: …`.

## Decision

Build the declared path once in `landing_advice.go`, read it from `check`, `status` and the
already-declared path of `change integrate`, and test it by the absence of the merge sentence. Details in
`../plan.md`.
