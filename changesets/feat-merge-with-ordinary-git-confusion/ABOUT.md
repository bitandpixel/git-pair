# feat-merge-with-ordinary-git-confusion

## Summary

The landing advice told every reader to "merge into the destination with ordinary git", and a repository whose
merge is a CI job's had no way to say otherwise. An agent that takes the primary instruction at its word merges
straight to the destination and past the verification the job exists to run — the failure that motivated this
change. The repository can now declare what happens next in `.git-pair/config`, and every landing sentence
follows it.

## What changed

> can you also adopt the automated landing route for this very project to dogfood the feature?

`.git-pair/config` gains a `[landing]` table with one decision in it:

```toml
[landing]
route = "default" # | "manual" | "automated" | "custom"
next  = "..."     # custom only
```

- `default` — also what an absent file means — is today's behavior: the merge as the next step, the hand-off
  beside it, one instruction in `next_action`.
- `manual` names the merge as the whole answer; the hand-off line prints nowhere.
- `automated` names the hand-off as the whole answer, and the merge sentence is removed from every surface —
  not demoted to an "or" line, removed — including the declared path's "or land it yourself" and the tail of a
  stacked child's waiting sentence.
- `custom` replaces both with the repository's own sentence, with `{branch}`, `{destination}` and `{changeset}`
  substituted; the destination is the one the walk resolved, so a stacked child's template names where its work
  actually goes.

The screen under `automated`:

```text
Next: `git pair check`, then hand it to main's owner: `git pair change integrate`, then `git push origin booking-transaction`
```

A declaration this build cannot honor — an unknown route value, an unknown key or section, `custom` without
`next`, a `next` under any other route, a template naming a placeholder git-pair does not fill — shows the
default view with the defect named on stderr, once per run. Never a gate failure. `check --json`,
`status --json`, `change wait --json` and `review submit --json` carry the resolved route as `landing_route`.

## Design decisions

- **The declaration is data, read from a tracked file, never detected.** The PRD's rule that no sentence names
  a forge or depends on detecting one still holds: a stated fact is not a detected one. The repository names
  the tool — a `custom` template may say `gh pr create` — and git-pair never does.
- **A hook was the wrong mechanism.** The sentence prints on read-path surfaces (`status`, `change wait`
  polls), where the hooks package deliberately runs nothing, and the merge route is repository policy rather
  than a client's own check.
- **The fallback for a broken declaration is the default route plus a loud warning.** The alternative —
  falling back toward the hand-off, the direction that cannot cause a bypass — was considered and declined:
  the unopinionated view is the honest answer for a file this build only partly understands, and the warning
  names the defect on every run until it is fixed.
- **The waiting sentence's tail is route-aware.** A repository with a merge job must never be told, at any
  point of a stack, that its own merge is the step.
- **Hand-parsed, like `parseMetadata`.** One table, two keys, every deviation a defect; no format library for
  a file this small.

## Validation

- `internal/landingconfig` — parse, resolve, each defect named, placeholder vocabulary, route predicates.
- `internal/cli/landing_route_test.go` — each route across all four surfaces, human and `--json`; the
  alternative-line suppression; the declared wait under `automated`; the waiting sentence's tail under
  `automated` and `custom`; the explicit-default equivalence; the broken-declaration fallback with its
  warning.
- `scripts/gates/landing-route.sh` (new, wired into `mise run gates`) — drives the installed binary through
  declared scratch repositories: the hand-off as the whole answer, the template substitution, the wait without
  the merge exit, and both defect fallbacks.
- `mise run check` passes: markdown format, gofmt, vet, full suite. The landing-route gate passes:
  `bash scripts/gates/landing-route.sh`.

## Known limitations

- Under `automated` and `custom`, the declared path's wait is the only exit offered — the PRD's "waiting is
  never the only exit offered" rule is withdrawn by the repository's own declaration. `change unready` remains
  the way to withdraw the request.
- The defect report is stderr-only; no JSON field carries it. `landing_route` shows the resolved value
  (`default` while the declaration is broken).

## Open questions

- Should a landing that arrives without a declaration be reportable in an `automated` repository — the
  detective control for a bypass that already happened? Deferred; it needs the route this change provides.
