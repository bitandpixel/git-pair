# feat-integration-configure

Configuring a clone to carry the durable refs was a flag on `integration record`, so a clone that wanted
it and had nothing to record had no way to ask. It is now `git pair integration configure`, and it writes
the push half as well as the fetch half.

## Summary

**`git pair integration configure` is a command, not a flag.** `record --configure-fetch` could only be run
by a clone holding the two SHAs a landing produced, and `record` refuses without them. A CI runner handed a
branch, a colleague's fresh checkout, or a repository whose owner decided last week has neither, so the
consent it was willing to give had no surface. Configuration is a property of the clone, so it now has a
command of the clone's own, and every clone can run it. `record` writes refs and nothing else, and prints
the line naming the command it did not run.

**It configures both directions.** The mirror refspec goes into `remote.<remote>.fetch`, as before, so an
ordinary `git fetch` keeps a clone able to tell published from unpublished. `refs/git-pair/*:refs/git-pair/*`
goes into `remote.<remote>.push`, so an ordinary `git push` publishes what the clone records — the
configuration PRD §13.4 previously documented as a line to type by hand, and which nothing wrote.
`--fetch-only` takes the read half and declines the write half, for a clone that should be able to compare a
record against the remote without being the thing that makes it public.

**`--help` stopped inventing a column.** Cobra reads backquoted words in a flag's usage text as placeholders,
prints them in the flag column, and strips them from the description. `--allow-feedback` named
`git pair check --allow-feedback` in backticks, so every `integration record --help` painted that phrase in
the column and widened it, and `--source` and `change wait --fetch` did the same with shorter names. Those
three usage strings no longer use backticks.

## What changed

**`git pair integration configure`** (`internal/cli/configure.go`) — `--remote <name>`, `--fetch-only`, no
positional arguments. Resolves the remote the same way `publish` does, appends each refspec with
`git config --add` once, and reports each half with its key, its value, and whether it was already there.
Human output is a table with the headline `origin: configured for the durable refs`; `--json` is
`{"remote", "fetch": {key, refspec, already_configured}, "push": {...}}` with a half absent when it was not
offered. Nothing prompts and nothing blocks: PRD §22's reason for consent-as-argument applies unchanged to
consent-as-command, and the pty replay covers it.

**`reviewref.PushRefspec`** (`internal/reviewref/reviewref.go`) — `refs/git-pair/*:refs/git-pair/*`, the same
spelling as `FetchRefspec`, named separately because the two live in different keys. No `+`.

**`record` loses the flag** (`internal/cli/integration.go`) — the flag, `integrationRecordInput.configureFetch`,
the `fetch_config` key of its JSON answer, and the two report lines. `recordReportExtras` keeps the hint and
`configureHint` repoints it at the new command, so the clone that never configured anything still hears
about it once, and still stays quiet in the clone that already did.

**`namedRemote`** (`internal/cli/configure.go`, used by `publish.go`) — the `--remote must exist` check both
commands now share. The refusals keep their own wording; only the lookup moved.

**Docs** — PRD §13.4 and §27's automatic-pushing bullet, README's handoff sample, command table, JSON
contracts, and the publishing section, and `skills/git-pair/references/cli.md`'s record and publish rows.

**Three surfaces name the command** (`internal/cli/configure.go`, `publish.go`, `published.go`) — the answer
to the review's question, which was whether anything nudges a user toward setting this up. `record` did
already; `publish` and the read path did not, and now do:

| Surface | What it says |
| --- | --- |
| `integration record` | the `configure:` line naming both keys — the two refspecs it did not write |
| `integration publish` | the `configure:` line naming `remote.<name>.push` alone, printed after a run that sent or confirmed a pair |
| `status`, `queue` | the "nothing here can say whether a record reached `<remote>`" note ends with the `--fetch` that asks once *and* `git pair integration configure`, which stops the asking |

Each is silent where the line is already written, which is what keeps a repeated pipeline run readable.
`publish` prints nothing on a run that sent nothing and nothing on the failure path, where the report is
already a refusal. The two hints are human-surface only, as `record`'s has always been; the read-path
remedy is in `unpublished_note` because that note is the answer to the question, not an add-on to it.

## Design decisions

**Both halves by default, `--fetch-only` to decline one.** Narrowing is the caller's job and declining is the
common case for a read-only clone; making a caller name both flags to get the behaviour most clones want
would be a default that punishes the majority. `--push-only` was left out deliberately: a clone that pushes
records it cannot compare is a clone that cannot tell a reviewer whether its own record has travelled, and
nothing asks for that combination yet.

**The push refspec carries no `+`,** which contradicts PRD §13.4's previous hand-typed recipe and the PRD now
says so. A forced refspec in `remote.<name>.push` would let a plain `git push` overwrite a remote durable
ref that disagrees — the one thing §13.3's create-only rule exists to prevent, and something no git-pair
command can do. Unforced, a repository gets automatic publishing *and* git's own rejection of the
disagreement; `TestConfiguredPushSendsTheRecordsAndNeverMovesOne` asserts both halves of that with a real
push and a real remote.

**The command pushes nothing itself.** `publish` publishes: it names a changeset, sends one pair, re-reads
the remote's copies and reports what actually arrived. `configure` changes what the repository's own git
does from here on. Keeping the verification in `publish` is what lets the hint after a write say
`next: git pair integration publish, to send what this clone already holds` — configuration alone leaves the
records this clone already holds exactly where they were.

**`--fetch-only` and `--remote` are the only flags.** No `--check`, no `--unset`, no `--all-remotes`: a
`git config --get-all` answers the first, `git config --unset-all` is git's, and a repository with more than
one remote knows which one it publishes to. Each would be a second way to say something git already says.

**Failure between the halves is reported, not hidden.** If the fetch line is written and the push write
fails, the error names the key that did get written and says to re-run. The write is idempotent, so a re-run
finishes it; a swallowed error would leave a clone half-configured with a log line claiming success.

## Validation

- `go test ./...` (via `mise run test`, sharded) — green.
- New: `internal/reviewref`'s `TestPushRefspecSendsTheNamespace` pins the namespace mapping, the absence of
  `+`, and the deliberate equality with `FetchRefspec`.
- Rewritten: `internal/cli/configure_test.go` — that `record` writes no config and names the command; that
  `configure` appends both lines and leaves the clone's own refspecs intact; the JSON shape and the
  `already_configured` transition; a second run leaving the config *file* byte-identical; `--fetch-only`
  writing one key and omitting the other half from JSON; `--remote` writing the named remote and refusing an
  unknown one; the no-remote refusal writing nothing; the configured-clone comparison test, now driven by
  the command rather than by hand-typed `git config`; and the push test above.
- `scripts/gates/pty-walkthrough.sh` — the config step drives `integration configure` instead of
  `integration record --configure-fetch`, and asserts the push refspec lands once as well as the fetch one.
  Run: `mise run gates`.
- `--help` for `integration record`, `integration configure` and `change wait` inspected by eye: the flag
  column is one column now.
- New after the review: `TestTheSurfacesNudgeTowardConfiguring` — `status` and its `unpublished_note`,
  `queue`, `publish` before and after the push line exists, and that `publish --json` carries findings and
  no prose; `TestPublishWithoutAnythingToSendDoesNotNudge` — a clone with no record publishes nothing and
  advertises nothing.

## Known limitations

- The nudges are one line each and appear on the surfaces where the absence of the configuration is already
  the story. A clone that never records, never publishes and never asks about publication hears nothing
  about `integration configure`, which is the intended silence: a command that mentions its own option in
  every report trains people to skim the rest.
- The configuration is per-clone and per-remote, and there is no command that reports which clones of a
  repository are configured. `status` can only ever speak for the clone it ran in, and it already does.
- `git push` honours `remote.<name>.push` only when the command line gives no refspec, which is git's rule
  and not this one. A caller who types `git push origin main` sends branches and not the durable refs;
  `git pair integration publish` is the command that always sends the pair and verifies it.

## Open questions

- Does the push half belong in the same command as the fetch half, or should a repository be able to opt in
  to one direction at two separate times? Today one command writes both unless told otherwise, and nothing
  has yet asked for the split beyond `--fetch-only`.
