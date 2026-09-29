## git-pair

This repository puts a human review step on top of an agent's commits with
[git-pair](https://github.com/dcasper/git-pair). If you are an agent working on a change here, read
the git-pair skill before you commit: `git pair skill list` shows where the installed copy is, and
`git pair skill show` prints it without one. If neither is installed, the skill is at
`skills/git-pair/SKILL.md` in the git-pair repository.

The three rules that cost the most to get wrong:

- `git pair change ready` is the handoff. Do not also ask for review in prose.
- `git pair change feedback` is how a review is read. `git pair diff --unreviewed` is the reviewer's
  question, and it reads empty immediately after a submission.
- `git pair check` is where you stop. The merge belongs to whoever owns the destination branch, and to CI:
  git-pair merges nothing, pushes nothing, and writes no ref, so the pushed branch is the record.

Exit codes are part of the interface: 1 means the repository said no to a correct command, and 2 means
the command was wrong and will fail again unchanged.
