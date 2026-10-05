# feat-default-branch-env

## Summary

`GIT_PAIR_DEFAULT_BRANCH` was the CI runner's documented way of naming the integration branch, and this
binary never read it: `--default-branch` was the only interface, and the variable's only reader was
`scripts/ci/git-pair-integrate.sh`, which forwards the flag on each `git pair` call it makes. The variable
is now the environment form of that flag — read once per run, outranked by the flag, and reported wherever
the flag's own answer is reported.

## What changed

| File | Change |
| --- | --- |
| `internal/cli/root.go` | `DefaultBranchEnv = "GIT_PAIR_DEFAULT_BRANCH"` beside `NoCacheEnv`, `defaultBranchOverride` (flag, then environment, empty is not set), `resolveDefaultBranch` as the single place a command asks the destination question, `noteDefaultBranchEnv` for the once-per-run stderr note, and `--default-branch`'s help text names the variable (built from the constant, so help and code cannot drift) |
| `internal/changeset/resolve.go` | `DefaultBranchEnvironment = "env"` added to the source vocabulary, with the reason a caller is allowed to set `Source` at all; both `ErrNoDefaultBranch` refusals now name the variable beside the flag |
| `internal/cli/{init,queue,status,tidy}.go` | every destination resolution goes through `a.resolveDefaultBranch`; `defaultBase` became a method on `app` so `init`'s inferred base follows the same rule as every other command's |
| `internal/cli/default_branch_env_test.go` | the environment form: it supplies the destination and reports `env`, the flag outranks it and silences the note, an empty value is not set, a value that does not resolve is refused with its source named, it replaces the flag in a one-branch CI checkout, and the name in the code is the name README and the skill teach |
| `README.md` | the flag section gains the variable's paragraph, the CI runner paragraph no longer says the command never reads it, and the troubleshooting entry for `cannot tell which branch is the integration branch` offers it |
| `skills/git-pair/SKILL.md`, `skills/git-pair/references/cli.md` | the flag bullet, the `default_branch_source` value list (`flag`, `env`, `origin-head`, `sole-candidate`), and the skill's CI sentence |

## Design decisions

- **This reverses the decision in `changesets/docs-readme-default-branch-variable`, and its argument was
  right.** That changeset refused the variable because an inherited value moves the destination — what
  "has this landed?" is measured against, and what a declaration merges into — "without leaving any trace
  in the command line". That risk is real and is not removed here. What changed is who carries it: a
  `GIT_PAIR_*`-prefixed variable is read by a reader as git-pair's own interface, so the tree carried a
  name whose reader lived elsewhere, and README needed three sentences to explain a mismatch instead of one
  to describe a flag. The fix the earlier changeset called for — make the destination visible in the output
  rather than inherited silently — is now implemented instead of argued for: `status --json` reports
  `default_branch_source: "env"` where the identical command line would report `"flag"`, and the run says
  once on stderr which variable named the branch and what it named.
- **The flag outranks the environment, not the reverse.** The command line is the statement about this
  invocation. The case that decides it is the CI job that exports the variable for its merge script and
  then runs one `git pair` command against a different destination: the inheritance must not contradict the
  typed value, and it must not be announced either, so the note is suppressed when the flag won.
- **An empty value is not set.** `GIT_PAIR_DEFAULT_BRANCH=` is what a shell leaves behind when a job meant
  to leave it unset; resolving an empty ref would be a refusal nobody asked for. This differs from
  `GIT_PAIR_NO_CACHE`, where anything non-empty counts as set: there the value's only content is "on", and
  here it is a revision expression.
- **The environment is read in `internal/cli`, not in `internal/changeset`.** Neither `changeset` nor `git`
  reads the environment anywhere today — everything they consult arrives as a parameter — and that is what
  makes them testable without a fixture per spelling. `DefaultBranch`'s comment now records that it labels
  every caller-supplied ref `flag` and that the caller relabels the environment's.
- **`Source` is rewritten rather than threaded in as a parameter.** `DefaultBranch` answers the same
  question either way; provenance is a fact only the caller has. One function (`resolveDefaultBranch`)
  does the relabelling, so a command cannot honour the variable and forget to report it, and the eight call
  sites that used to pass `a.defaultBranch` themselves now cannot pass anything else.
- **The note is once per run, and it is printed on the refusal path too.** A command resolves the
  destination several times (`status` does it in `load` and again for its trunk fields), so a latch is what
  keeps visibility from becoming noise. On the refusal path the value is exactly what the refusal is about,
  and a reader sent to fix a command line that was never wrong learns nothing from the visit.
- **`scripts/ci/git-pair-integrate.sh` is unchanged.** It takes `BASE` from the variable for its own git
  merge as well as for git-pair, and it passes `--default-branch` explicitly on each call. That is the
  explicit form and still the better one inside a script that has the value in hand; the variable now helps
  the `git pair` calls the script does not make.

## Validation

- `go test ./internal/cli/ -run TestDefaultBranchEnv` — six tests, covering: the variable alone answers the
  destination question and reports `env`; the flag wins and prints no note; an empty value is unset;
  an unresolvable value is refused with its source named on stderr; in a clone that fetched one branch,
  `queue` refuses naming both escapes and succeeds once the variable supplies `refs/remotes/origin/main`;
  and the constant equals the name README and the shipped skill teach, so a rename reaching two of the
  three fails.
- Manual replay in a scratch clone holding `origin/main` and `origin/master` with no `origin/HEAD` (the
  ambiguous state nothing may guess about): with the variable the run names that destination and prints the
  note once; with the flag set to a different branch the flag's value is used and no note appears; with the
  variable empty the ambiguity refusal returns; with the variable naming a ref that does not exist the
  refusal is `integration branch "refs/heads/nope" does not resolve` preceded by the note. The same refusal
  and the same exit code (1) come back from `--default-branch refs/heads/nope`, so the two forms differ only
  in provenance.
- Manual `init` in a repository holding `main` and `master` on a fresh branch: with
  `GIT_PAIR_DEFAULT_BRANCH=refs/heads/master` the run prints the note first, then `base: master (pass --base
  to choose a different ref)`, and `CHANGESET.yaml` records `base: master`; `status --json` on that
  changeset reports `default_branch: "master"` with `default_branch_source: "env"`. This is what the open
  question below describes, observed rather than assumed.
- `mise run check` (gofmt, `go vet ./...`, the sharded suite): passed.
- `mise run gates`: passed — `E2E: all checks passed` (PRD §29 replay), `PTY: all checks passed`
  (the TUI walkthrough), and `CI-INTEGRATE: all checks passed` (70 checks, the merge job this variable
  belongs to). The merge script still passes `--default-branch` explicitly, so the replay covers the flag
  path and the flag-beats-environment rule rather than the variable itself.

## Known limitations

- `default_branch_source` reports which source was *used*, not which were present. A run where the flag
  overrode an exported variable is indistinguishable in the JSON from one where no variable was set. That
  is the shape the field already had (`flag` never said whether git could have answered), and a second
  field for "what else was there" would be a new contract rather than an extension of this one.
- The risk the earlier changeset named still exists: two clones of one repository can disagree about what
  has landed when one of them has the variable exported. The note and `default_branch_source` make the
  disagreement explainable; they do not prevent it.
- The `change stack` and `change combine` refusals still say "pass `--default-branch` or set the
  repository's integration branch" without naming the variable. They name the flag, which works, and their
  wording predates this change; broadening four messages into two was left out to keep the diff on the
  interface rather than on every sentence that mentions it.

## Open questions

- `GIT_PAIR_BASE`, the repository variable that feeds the merge job's `GIT_PAIR_DEFAULT_BRANCH`, still has
  no entry of its own in Configuration. It is named only where it is used, in the CI section.
- Should `init` accept the environment for the base it records into `CHANGESET.yaml`? It does now, because
  the destination is one answer per run, and the base a run records comes from the same resolution as the
  base it measures against. A repository whose trunk is not `main` therefore gets a `base:` from whoever's
  shell ran `init`, which the note makes visible at the moment it happens and which
  `git pair init --base <ref> --set-base` corrects afterwards.
> can you explain your view of the tradeoffs here?
