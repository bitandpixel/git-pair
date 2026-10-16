# feat/prettier-hook

## Summary

Markdown in this repository is now formatted by a tool rather than by whoever typed last. Prettier is pinned
in `.mise.toml` beside Go and goreleaser, `mise run format` rewrites the tracked documents, and `mise run
check` — so `gates`, `.github/workflows/git-pair-check.yml` and the merge job's candidate run — fails on a
document the formatter would rewrite. A pre-commit hook formats the markdown a commit stages, so nobody has to
remember the command.

Go is untouched. `gofmt` was, and remains, the only thing that speaks to `internal/` and `cmd/`.

## What changed

124 tracked documents are formatted in this pass, 3.5k lines each side. Most of that is the work two people
should never have disagreed about: bullet markers, list continuation indentation, blank lines around fences,
trailing spaces, table column alignment, and whether emphasis is written with `*` or `_`.

New files and tasks:

| Path                           | What it is                                                                                                                                                                                                                                 |
| ------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `prettier.config.mjs`          | The two options that are not cosmetic, each with its reason: `proseWrap: preserve` and `embeddedLanguageFormatting: "off"`. A config file rather than `.prettierrc.json` because JSON carries no comments and every option here needs one. |
| `.prettierignore`              | The four paths another tool owns, with why. Editors read this file, which is why the exclusions live here and not only in the script.                                                                                                      |
| `scripts/format.sh`            | One driver, two modes: `check` (the gate; rewrites nothing) and `write` (a person, and the hook). Exit 3 means "no formatter installed", which the gate reads as red and the hook reads as a warning.                                      |
| `scripts/git-hooks/pre-commit` | Formats the markdown staged for the commit and re-stages what it formatted.                                                                                                                                                                |
| `scripts/install-hooks.sh`     | Writes a two-line shim into the clone's hook directory. `mise run hooks:install` runs it.                                                                                                                                                  |
| `.mise.toml`                   | Pins `node` and `npm:prettier`, adds `format`, `format:check` and `hooks:install`, and puts `format:check` first in `check`.                                                                                                               |

Seven documents carry hand-written lines: README's new Development paragraphs, AGENTS.md's new Formatting
section, and the five repairs listed under _Pay attention_. The other 117 are the formatter's output alone.

## Example

A table and an emphasis marker, before and after — the shape every document in the pass now has:

```markdown
| Root | Path | Consent |
| --- | --- | --- |
| `worktree` | `.git-pair/hooks/` | after `--trust` |

The reviewer *submitted* a block.
```

```markdown
| Root       | Path                | Consent         |
| ---------- | ------------------- | --------------- |
| `worktree` | `.git-pair/hooks/`  | after `--trust` |

The reviewer _submitted_ a block.
```

What the formatter deliberately leaves alone: the line breaks inside the prose above (paragraphs keep the
wrap their author gave them), and anything between the fences — a fenced block in these documents is evidence,
not source.

## Design decisions

**Why Prettier.** Three alternatives were measured on the same 148 documents before choosing:

| Tool                           | Lines changed | Why it lost                                                                                                             |
| ------------------------------ | ------------- | ----------------------------------------------------------------------------------------------------------------------- |
| `deno fmt`                     | 43,047        | Also reformats code inside fences, which rewrites quoted transcripts.                                                   |
| `remark-stringify`, configured | 5,005         | Keeps `*emphasis*` and compact tables, so a smaller diff — and no editor integration anyone already has.                |
| `markdownlint-cli2 --fix`      | 195           | Safe and small, but it only fixes spacing. It aligns no table and normalises no emphasis, so two people still disagree. |

Prettier is also the one an editor already ships, which is what makes the formatting invisible.

**Why the version is pinned, and why `node` is pinned with it.** Prettier's output moved between 3.6 and 3.9
(a blank line before an HTML comment), so a floating version makes a file formatted on one machine and
unformatted on another. Prettier is an npm package, so an unpinned runtime lets a runner pick its own node and
makes the formatter pin only half an answer. `scripts/ci/prepare-toolchain.sh` already runs `mise install`, so
the CI jobs get both for free and pay one download for them.

**Why `embeddedLanguageFormatting: "off"`, which is not the default.** With the default, the first run
reformatted a JSON example inside a README transcript to `"parent_notes": [ "waitlist-…`, adding a space the
command never printed. A fence in these documents is captured evidence.

**Why AGENTS.md is excluded.** Its profile stanza is written by `dev-profiles install`, which puts no blank
line before the closing `<!-- /dev-profiles:profile -->`; Prettier puts one there. Measured:
`dev-profiles install --dry-run` reports `AGENTS.md refreshed` on the formatted copy. Two formatters owning
one file is a loop, so one of them stays out. The same reasoning excludes `.dev-profiles/`, `.agents/` (both
byte-compared by `dev-profiles doctor`) and the generated `packaging/THIRD-PARTY-NOTICES.md` (byte-compared by
`mise run release:check`).

**Why a shim rather than a copy or a symlink.** This repository is worked in several worktrees at once, so a
copy goes stale and a symlink runs whichever branch was installed last. The shim resolves
`git rev-parse --show-toplevel` at commit time, so the hook that runs is the one in the worktree being
committed from. `core.hooksPath` was rejected for the same reason git-pair gives for its tracked hook root: it
would redirect every hook point, silently disabling whatever else a clone runs there.

**Why the hook refuses a partially staged file.** Formatting such a file and re-adding it would put the
unstaged half into the commit. It names the files and both ways out instead. When Prettier is not installed the
commit proceeds with a warning, because the hook is the convenience and `check` is the rule.

**Merging `main` in, twice, and what conflicted both times.** `feat/hooks-position-scope` landed while this was
open and rewrote the two hook-flag rows in README's command table and in `references/cli.md` and
`references/hooks.md` — the same tables this pass pads, so all three conflicted, one region each. The merge
keeps that change's wording and lets the formatter re-pad it, so nothing from either side is dropped. Then
`chore/file-finished-plans` landed and moved nine finished plan directories from `docs/plans/` into
`docs/plans/completed/`; four of those files conflicted because this pass had repadded them at the old path and
git followed the rename into a content conflict. Both merges take main's text at main's path and let the
formatter re-pad it. Each run also brought a record that is unformatted on `main`
(`changesets/feat-hooks-position-scope/ABOUT.md`, then `changesets/chore-file-finished-plans/ABOUT.md`), which
this gate formats — a landed change's record is tracked markdown like any other. A merge rather than a rebase
each time, because the changeset is declared: a rebase would rewrite the marker and leave a declaration naming
history this branch no longer carries.

Each resolved file was then checked against main with an independent markdown parser at three levels — same
tree shape, same sequence of top-level blocks, same normalized text — so "the formatter only re-padded it" is a
checked claim rather than an assumption. One file needed more than that: see the note under Pay attention about
`base-changeset-collapse/plan.md`.

## Pay attention

Six documents were hand-repaired because their markdown was already malformed in a way the formatter made
worse. Each is a quoted sentence containing inline code, written as a single-backtick span nested in a
single-backtick span — which markdown cannot express. Before, the inner backticks were swallowed at render and
Prettier dropped the spaces around them; `TestDocumentsQuoteTheLandingSentencesTheCodeBuilds` was the thing that
caught it. Now each quote uses double-backtick delimiters, which renders the sentence with its backticks
visible, the way the builder prints it:

- `README.md`, the stacked child's waiting sentence (§near "A parent that has not landed yet").
- `skills/git-pair/references/cli.md`, the same sentence — here the inner backticks were already missing from
  the rendered text.
- `docs/plans/completed/base-changeset-collapse/plan.md`, `docs/plans/completed/declared-next-step/plan.md`,
  `docs/plans/completed/landing-handoff-guidance/plan.md`, `docs/plans/completed/publish-the-records/plan.md` — a
  code span wrapped across a line break inside a list item, which Prettier re-prints differently on each pass, so
  one `mise run format` is not enough and `format:check` fails the file forever. The second one also used `\``
escapes inside a code span, which do not work there. The first moved into `completed/` with the second merge and
  needed the repair again there: taking main's copy of a moved file brings the defect back with it, which is why
  the rule is written in README and not only in a commit message.

Review those six diffs by hand. The rest of the pass is machine output.

Also worth a decision of your own: `.mise.toml` now installs node and prettier on every checkout and every CI
run, and the hook is installed into the clone's shared hook directory (it is installed on this machine now,
which is why these commits came out formatted).

## Validation

- `mise run format:check` exits 0, and a second `mise run format` changes nothing — the property that matters,
  since a formatter that never settles is worse than none.
- `mise run check` green: format, gofmt, vet, the sharded suite. The document contract tests are the ones that
  could have caught a formatter rewriting a sentence, and they pass:
  `go test ./internal/cli/ -run 'TestDocumentsQuoteTheLandingSentencesTheCodeBuilds|TestCommandsNamedInTheDocsExist|TestEveryCommandIsNamedInTheDocs|TestTheCommandTreeIsTheCommandSet|TestStackedExamplesShowTheWrittenPair|TestSkillFrontmatterIsLoadable|TestDefaultBranchEnvIsTheNameTheDocsTeach|TestNoDocNamesADurableRef'`
- `mise run gates` green three times: at the head this was offered at (86s), at the merged head after `main` came
  in (185s), and again after the second merge (96s). That is `dev-profiles command verify` — what
  `.github/workflows/git-pair-check.yml` runs on a pull request, and what the merge job runs against the tree it
  is about to land: `E2E: all checks passed`, `PTY: all checks passed`, and
  `CI-INTEGRATE: all checks passed (132 checks)`. The replays build their own scratch repositories, so the hook
  installed in this clone is not what they exercise; the scratch-clone runs below are.
- Formatting was proved text-preserving against a parser that is not Prettier: every tracked document, rendered
  to text with micromark (`remark-parse` + `remark-gfm` + `mdast-util-to-string`, whitespace runs collapsed)
  before and after, from `git show HEAD:<path>` to the working tree. 144 of 148 are identical. The four that
  differ are the repairs above, and each difference is the backticks coming back. That harness is a throwaway
  script outside the repository, not a committed test — see limitations.
- The hook was exercised in a scratch clone, not by eye: unformatted staged markdown comes out formatted and
  staged; a file staged in one state and edited in another is refused with the index and worktree untouched; a
  path under `.prettierignore` is left alone; a commit with no markdown never starts the formatter; with
  Prettier absent the commit proceeds with the warning. `scripts/install-hooks.sh` was tested for the idempotent
  reinstall, the refusal against a foreign hook, and `--force` moving it aside to `pre-commit.bak`.
- The AGENTS.md ping-pong was checked with the tool that would cause it:
  `.dev-profiles/bin/dev-profiles install --dry-run` says `pointer AGENTS.md already carries the stanza`.

## Known limitations

- Two Prettier behaviours are not configurable and the repository now lives with them: table columns are padded
  (so editing one cell re-pads its row), and emphasis is written `_like this_`. Both were declined upstream.
  `prettier.config.mjs` and README's Development section say so where someone will look for the switch.
- Nothing in CI proves a future `mise run format` preserved what the documents say. The text-preservation audit
  above ran once, here. The standing guard is the pair of document contract tests, which read PRD, README and
  the skill for quoted sentences and command names — strong where documents quote code, silent elsewhere.
- A code span that wraps across a line break is a convergence hazard. 63 documents still contain one, all
  convergent today; the four that were not had to be repaired. `scripts/format.sh check` prints the diagnosis
  when a file survives a format run, but the pattern is only caught when somebody hits it.
- An editor with its own bundled Prettier version can disagree with the pinned one. `mise run format` is the
  authority, and the pin is what the gate compares against.

## Open questions

- Should the duty also sit on the product's own hook point? `.git-pair/hooks/pre-ready/01-format` would refuse
  `git pair change ready` on unformatted markdown and dogfood the feature, at the cost of one
  `git pair hooks --trust` per clone. The native pre-commit hook was chosen because it also catches commits
  that never reach a hand-off.
- Should a test pin the built-in ABOUT.md scaffold (`internal/changeset.aboutTemplateBuiltIn`) as
  prettier-clean, so a changeset created in this repository never arrives unformatted? It is clean today, by
  inspection only.
- 63 documents still contain a code span whose delimiters do not do what the author meant — pre-existing, and
  neither fixed nor broken here. A follow-up could clean them the way these five were, one reviewable pass.
