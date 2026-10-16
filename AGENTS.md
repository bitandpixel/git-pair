# git-pair

A command-line tool that puts a human review step on top of an agent's commits. The product is described in
`README.md` and specified in `PRD.md`; `docs/` holds the design notes. The library this checkout vendors under
`.dev-profiles/` declares the workflow the repository runs, and `dev-profiles doctor` says whether what is
installed still matches it.

<!-- dev-profiles:profile -->
## Development workflow

This repository declares `git-pair-loop` v2, `git-pair-github-integration` v7 in `.dev-profiles.toml`. Read the installed skills before committing:

- `git-pair-workflow`: how work gets written and handed off for review: branch, commit, changeset, review (`.agents/skills/git-pair-workflow/SKILL.md`)
- `git-pair-github-integration`: the last step of a `git-pair-loop`: a job verifies the merge candidate in the tree that would land, then lands it with ordinary git (`.agents/skills/git-pair-github-integration/SKILL.md`)
<!-- /dev-profiles:profile -->

## Formatting

Markdown is formatted, the way Go is formatted by gofmt. `mise run check` fails a document Prettier would
rewrite; `mise run format` rewrites it. Two rules when writing markdown here, both about inline code: keep a
code span inside one line, and quote a sentence that contains inline code with double backticks
(``like (`git pair status` here)``). A wrapped span never settles under the formatter, and a single-backtick
span nested in another loses its backticks when the sentence renders. Table columns are padded by the
formatter, so do not pad them by hand. README's Development section carries the reasoning and the list of
documents another tool owns.
