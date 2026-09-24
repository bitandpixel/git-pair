# feat-skill-guidance

git-pair now ships the contract an agent follows, and can say whether the copy an agent is reading is the
one that matches the binary. Three surfaces: `skills/git-pair/` (the skill itself, in this repository),
`//go:embed` plus `git pair skill list | show` (the same bytes compiled in, and a check against them), and
`git pair skill install | agents-md` (putting those bytes where a harness reads them).

## Summary

**The agent contract had no home in this repository.** PRD §22 and the README describe the loop, and the
only agent-facing document that existed lived in a sibling repository — where it drifted into documenting
`review close`, which this code removed, `refs/reviews/*`, which this code never wrote, and `change init`,
which was never this tool's name for `init`. A document beside the wrong code is read as a promise, so the
contract is now a surface of the tool: `skills/git-pair/SKILL.md` with the loop, the roles, the exit codes
and the prohibitions, and `references/` beside it for flags, JSON shapes, the landing contract, and
installing the skill.

**The skill is compiled into the binary, and an installed copy is checkable against it.**
`git pair skill list` prints every skills directory git-pair knows — `.agents/skills`, `.pi/skills`,
`.claude/skills`, in the repository and in the home directory — each marked `current`, `stale`, `absent` or
`unavailable`. `current` is byte equality with this binary's copy, which is the only version claim that
does not depend on somebody remembering to write one. `git pair skill show` prints the compiled copy, so the
contract can be read without a checkout.

**Installation is a command with a refusal in it.** `git pair skill install` writes the skill into a skills
directory as `git-pair/`: `--harness agents|pi|claude`, `--scope repo|user`, `--dest <dir>`, `--dry-run`,
`--force`. It never deletes. Files that match are left alone, files that differ are refused until `--force`
says otherwise, and files it did not write are reported as unmanaged and kept. `git pair skill agents-md`
prints the pointer stanza for a harness that reads `AGENTS.md` and nothing else.

**The skill is a checked document now.** `docs_contract_test.go` walks every `.md` under `skills/` and
applies the check it already ran over PRD and README: every command name and ref path the prose uses must be
one the code answers to. Its other direction — no command may leave the documents — runs over the documents
that claim to be catalogues: PRD, README, and `skills/git-pair/references/cli.md`. Skill frontmatter is
checked too, because a `SKILL.md` whose description does not parse is not loaded and only warns.

## What changed

**`skills/git-pair/`** — `SKILL.md` and three reference pages. The skill is self-contained rather than a
pointer into the PRD: an agent activates a skill to be told what to do, and a page that says "see PRD §22"
sends it somewhere it cannot open. `references/cli.md` is the catalogue (every command, its flags, the exit
codes, the JSON shapes); `references/integration.md` covers the landing contract for an agent who reads
about it and does not run it; `references/installing-the-skill.md` covers the harness directories.

**`skills/embed.go`** — `//go:embed all:git-pair` and the `AGENTS.md` stanza, plus `Name`, `Files()` and
`Read()`. The package lives in `skills/` rather than under `internal/` because `go:embed` cannot reach above
its own package directory, and the skill has to stay where a person walking the repository finds it. The
consequence — one Go file inside a skills directory — is written up in the package comment.

**`internal/cli/skill.go`** — the `skill` group and its four commands. `skillTarget` is the one shape both
the table and the install report; `inspectSkill` (for `list`) and `planSkillInstall` (for `install`) ask the
same question of a directory — what matches, what differs, what is not ours — and `unmanagedFiles` is shared
between them. `skillRepoRoot` deliberately does not carry `loadRepo`'s exit 3 for an absent repository: these
commands read the filesystem, and no repository is a row in the table rather than a failure.

**`internal/cli/docs_contract_test.go`** — `docFiles(t)` now includes every page under `skills/`, found by
walking the directory so a new reference page is covered without anyone adding it; a walk that finds no
markdown fails rather than quietly dropping coverage. `completeDocs(t)` is the narrower list the
"every command is named" check runs over. `TestSkillFrontmatterIsLoadable` parses the frontmatter block.

**README** gains an "The agent skill" section with the two session transcripts, the harness table and the
`AGENTS.md` stanza route; the command reference and the `--json` sentence name the four commands; **PRD**
gains §11.5, which states the same contract as requirements, including the exit codes each refusal answers
with.

## Design decisions

**Reversing the earlier "no skill surface here".** `docs/plans/review-architecture-v2/plan.md:637` records
the opposite decision, and the reason it gave — a surface that duplicates the contract rots beside the code
— is the reason this one is written the way it is, not a reason to skip it. The rot it predicted had already
happened, one repository away, where nothing could see it. `docs/plans/review-architecture-v2/reconciliation.md`
§0.8.1 argued for this repository, with the canonical wording staying in the requirements and the skill held
to the same names; that is what landed, except that the skill restates the loop instead of pointing at it.

**A self-contained skill, not a pointer into the PRD.** The loop is 40 lines and an agent needs it in front
of it. The alternative — a page that names the PRD sections — optimises for one copy of the text and
delivers an agent that has read nothing. What keeps the two copies honest is the test, not the duplication.

**`current` is bytes, not a version.** The version string is stamped by the build and is untouched by the
person editing the skill. Byte equality answers the question an agent is asking — "is what I'm reading the
contract this tool enforces?" — without a convention that has to be remembered.

**Embed in `skills/`, not `internal/`.** `go:embed` reads from the package's directory tree. The choices were
a Go file in the skills directory or a skill directory buried where no harness or human looks; the Go file is
invisible to every skill scanner, which looks for `SKILL.md`.

**The check runs over all prose in one direction and over catalogues in the other.** Requiring every leaf
command in every page would make `SKILL.md` a second command table, which is the opposite of what a short
contract is for. Requiring them in `references/cli.md` costs nothing, because that page is the table.

**Three harnesses, not a search.** The locations are each harness's documented discovery rules; guessing at a
fourth would install the skill somewhere nothing reads. `--dest` is the answer to any harness git-pair has no
row for, and `codex` resolves to `agents` because that is the truth for Codex CLI rather than a fourth copy.

**An install that cannot delete.** `--force` replaces files git-pair wrote and leaves the rest, reporting
what it left. Deleting is a different operation, the skill directory may hold a team's own notes, and a
command whose worst case is "it removed something" will not be run on somebody's setup step.

**The refusal is exit 1, not a warning.** An overwritten skill is the failure this feature exists to prevent,
so overwriting needs to be a decision somebody made with `--force`. Exit 1 also fits the table: the invocation
was right, the repository said no.

## Validation

- `go test ./...` green, including 14 tests in `skill_test.go` and `skill_install_test.go`: the `list` table
  and its four states, byte-for-byte install, idempotence, the refusal, `--force`, unmanaged files kept,
  harness/scope/`--dest` resolution and each usage error, `show` against the compiled copy, `agents-md`
  against the compiled stanza.
- The JSON contract tests gained `skill list --json` in the null-array sweep and both viewers in the
  "the flag changed nothing" test.
- `docs_contract_test.go` green with the skill in `docFiles`, and green for the new commands in `completeDocs`.
- Manual: `skill list` and `skill install` in a scratch repository, the second install reporting nothing to
  write, `--dry-run` writing nothing, the refusal against a hand-edited `SKILL.md` followed by `--force`,
  `--harness cursor` refusing with the list and `--dest`, and `skill agents-md`.
- The gate scripts under `scripts/gates` exercise the review loop and the TUI; nothing in this changeset
  touches either.

## Known limitations

- `stale` does not say which file differs, or whether the installed copy is older or newer. Byte equality
  answers the question that matters; a diff of two skill copies does not have a direction.
- `install` writes no manifest, so a hand-copied skill is indistinguishable from an installed one. That is
  deliberate — the bytes are the contract, not the route they took — but it means `unmanaged` is a name for
  "not in this binary's skill", not "written by a human".
- The skill restates the loop, so a command change can leave the prose accurate about names and wrong about
  behaviour. The test catches renamed and removed commands; semantic drift is still a reviewer's job, exactly
  as it is for the README.
- `--dest` is not remembered, so `git pair skill list` cannot report on a custom directory. `--dry-run` on
  the same `--dest` is the check.

## Open questions

- `bitandpixel/pi-git-pair` still carries its own `skills/git-pair/SKILL.md`, which now duplicates this one
  and still says `review close`, `refs/reviews/*` and `git pair change init`. The case for reducing it to a
  pi-only addendum — `git_pair_wait`, `git_pair_working_dir`, the `/pair-*` commands — pointing here for the
  contract is the argument this changeset makes about itself. Worth a decision in that repository.
- Should `git pair skill install` grow a `--remove`, or is `rm -rf` honest enough now that `unmanaged` is
  reported at every install?
