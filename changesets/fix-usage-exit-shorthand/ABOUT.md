# fix-usage-exit-shorthand

## Summary

Three kinds of mistyped command reported the wrong cause. The exit-code table says 1 means "the
repository refused a correctly used command" and 2 means "the invocation was wrong", and these three came
back as 1 — so an agent that typed `git pair -V` was told to go re-read a repository that had refused
nothing, instead of fixing its own command:

| invocation | message | before | now |
| --- | --- | --- | --- |
| `git pair -V`, `git pair status -x` | `unknown shorthand flag: 'V' in -V` | 1 | 2 |
| `git pair init --id` (value missing) | `flag needs an argument: --id` | 1 | 2 |
| `git pair ---x` | `bad flag syntax: ---x` | 1 | 2 |

The printed message was always right; only the code was wrong. `git pair --nope` and `git pair bogus`
already exited 2, which is what made the three above visible by contrast.

## What changed

- `internal/cli/root.go` — `isUsageError` gains the three message shapes it was missing:
  `unknown shorthand flag`, `flag needs an argument`, `bad flag syntax`. Plus a comment saying what the
  match is against and why the clauses are named one by one.
- `internal/cli/contract_test.go` — four cases in the exit-2 table: one per shape, and the root-level
  `-V` that prompted this.

Nothing else in the classifier changed, and no flag, command or message text changed.

## Design decisions

**Matching on the message is kept, not replaced.** pflag builds these errors with `fmt.Errorf` and exports
no sentinel types, so there is no `errors.As` to write. The comment on the function says so, because
"why is an exit code decided by a substring" is the first question a reader asks, and the answer needs to
be next to the code rather than in a review thread.

**Each shape is added by name, with the invocation that produces it pinned in the test.** The tempting
loosening — match `unknown`, or match anything cobra returned — is what let the gap exist. A broad match
would also start classifying messages that mean something else as usage errors, and the table would stop
being the contract and become a guess.

**The git case stays matched first in `Execute`.** That is what keeps substring matching safe: a `git`
subprocess failing with the phrase `unknown flag` in its stderr still exits 3, because the git case is
decided before this function is consulted. Worth stating, since it is the obvious failure mode of the
mechanism.

**No `-v` shorthand was added for `--version`.** The report was a person typing `git pair -V`, and the
fix they need is a correct exit code with `--version` in the message, not a second spelling of a flag that
cobra registers itself. It is a separate question, in Open questions.

## Validation

- The four new cases are the evidence, taken both ways. With `root.go` stashed they fail as expected —
  `[status -x] exited 1, want 2`, `[-V] exited 1, want 2`, `[init --id] exited 1, want 2`,
  `[---x] exited 1, want 2` — and with the fix `TestExitCodeUsageError` passes.
- `gofmt` clean, `go vet` clean, `mise run gates` green: `check` plus the scripted replays.
- Spot-checked against a build of this branch rather than only under `go test`: `git-pair -V` prints
  `git-pair: unknown shorthand flag: 'V' in -V` and exits 2.

## Known limitations

- Classification remains substring matching, so a pflag upgrade that rewords one of these strings turns
  that shape back into exit 1 quietly. The four pinned cases notice for the shapes they name and nothing
  else; the other pflag messages (`invalid argument`, the arity phrases) were already covered and are
  unchanged.
- This changes no message text. `git pair -V` still does not suggest `--version`; the suggestion is what
  a shorthand would buy, and it is left as a question.

## Open questions

- Should `-v` be a shorthand for `--version`? cobra registers the long form by itself once the command
  has a `Version`, and no shorthand comes with it. Adding one is a line, but it is a new public spelling
  chosen for the convenience of a typo, and this changeset deliberately fixes only the classification.
