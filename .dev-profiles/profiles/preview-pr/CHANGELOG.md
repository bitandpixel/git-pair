# preview-pr profile changelog


## 2 — 2026-10-14

- `pair-preview-pr` is now declared as a `[[router]]` entry with a `reason`, and the entry line in `AGENTS.md` carries
  that sentence: The pointer line now says why to open the skill, not just that it exists
- The driver no longer appends its own three sentences to the stanza. They described a hand-off by command and a
  gate separated from the merge — facts about this profile's workflow, printed into every repository's `AGENTS.md`
  including the ones that do not run it. The rules are not gone: they are in the skill this line points at.

## 1 — 2026-10-14

First release, taken out of `git-pair` rather than written. Until now the preview loop shipped inside the loop profile,
which meant a repository with no preview environment had to `exclude_skills` one of its skills — a consumer undoing an
adoption decision that was never its job to make. Declaring this profile is the only way to get the behaviour, and
declaring nothing is the way to not want it. The skill text is unchanged.
