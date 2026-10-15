# git-pair-loop profile changelog


## 2 — 2026-10-14

- `git-pair-workflow` is now declared as a `[[router]]` entry with a `reason`, and the entry line in `AGENTS.md` carries
  that sentence: The pointer line now says why to open the skill, not just that it exists
- The driver no longer appends its own three sentences to the stanza. They described a hand-off by command and a
  gate separated from the merge — facts about this profile's workflow, printed into every repository's `AGENTS.md`
  including the ones that do not run it. The rules are not gone: they are in the skill this line points at.

## 1 — 2026-10-14

**The loop, on its own, so a repository can adopt the review workflow without adopting anybody's merge job.**

- This is the loop half of what was the `git-pair` profile (v4): the branch → commit → changeset → review →
  hand-off skills, and the housekeeping command that reports landed changesets waiting to be tidied. The skills
  themselves are unchanged, so an installed copy from `git-pair` v4 is byte-identical here.
- Why it exists: a repository declares the set of profiles it runs, in the order they take precedence. Splitting the
  loop from the destination's merge machinery is what lets one repository adopt the review workflow and a second
  profile for how its changes get merged — or adopt neither merge profile and keep the loop. Duplicating the loop
  into a merge-shaped profile instead would have meant two copies of one skill, each committed by consumers and
  byte-compared, and the first rewording that reached only one of them would make installed guidance depend on which
  profile a repository happened to adopt.
- Nothing was added. A profile that ships the loop demands no command: `git pair check` is the gate, and it needs no
  answer from the consumer's marker.
