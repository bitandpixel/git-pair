# git-pair-manual-integration profile changelog


## 3 — 2026-10-17

- The `Never` list names `git pair review discussion` where it named the removed `git pair review thread`, so the
  commands an author may not run are the commands the tool answers to: `submit`, `open`, `reopen`, `discussion`,
  `about`. The conversation the removed command wrote to is now one file per changeset,
  `changesets/<id>/DISCUSSION.md`, and the author's route to it is `git pair change discussion`, which needs no
  terminal.

## 2 — 2026-10-14

- `git-pair-manual-integration` is now declared as a `[[router]]` entry with a `reason`, and the entry line in `AGENTS.md` carries
  that sentence: The pointer line now says why to open the skill, not just that it exists
- The driver no longer appends its own three sentences to the stanza. They described a hand-off by command and a
  gate separated from the merge — facts about this profile's workflow, printed into every repository's `AGENTS.md`
  including the ones that do not run it. The rules are not gone: they are in the skill this line points at.

## 1 — 2026-10-14

**Merging by hand, with the same gate.**

- The other half of the split from `git-pair` v4: a repository whose destination branch is moved by a person rather
  than by a job. It ships one skill and no CI payload, and it demands no command — a demanded role is a command
  something runs, and nothing here runs one.
- What it keeps is the part that mattered: the author asks with `git pair change integrate`, and the person about to
  merge reads git-pair's own gate with both answers, `.ready` and `.integrating`. A merge that skips that loses the
  only record that the reviewer approved *this* head.
- What it gives up is stated in the skill rather than hidden: with no job verifying the merge candidate, the person
  merging is the only check between a red suite and trunk. The way to get the guarantee back is to declare
  `git-pair-github-integration` instead.
