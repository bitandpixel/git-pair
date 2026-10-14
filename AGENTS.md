# git-pair

A command-line tool that puts a human review step on top of an agent's commits. The product is described in
`README.md` and specified in `PRD.md`; `docs/` holds the design notes. The library this checkout vendors under
`.dev-profiles/` declares the workflow the repository runs, and `dev-profiles doctor` says whether what is
installed still matches it.

<!-- dev-profiles:profile -->
## Development workflow

This repository declares `git-pair-loop` v2 in `.dev-profiles.toml`. Read the installed skills before committing:

- `git-pair-workflow`: how work gets written and handed off for review: branch, commit, changeset, review (`.agents/skills/git-pair-workflow/SKILL.md`)
<!-- /dev-profiles:profile -->
