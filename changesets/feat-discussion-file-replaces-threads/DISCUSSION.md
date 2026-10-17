# Discussion

## Skill edits: accuracy pass

> I've added some substantial edits to the skills file. Can you look it over and make sure it's accurate? I'm
> trying to make sure we have a nice concise skill without extranneous or duplicative content, except for when
> really wanted for emphasis.

### Response

I checked every changed sentence against the code and against PRD §22 and §26. Your edits stay — the concision
is real, and "the one file that accumulates what review cycles talk about" is a better line than mine. Nine
things were inaccurate, unfinished or self-contradicting, all in `skills/git-pair/SKILL.md`:

1. `git pair change discussion` had a placeholder where its description should be. Filled in, keeping
   `references/cli.md`'s wording so the two descriptions do not drift apart.
1. `Avoid using review <subcommand>` became a Never again, naming the four commands. The profile skills' Never
   lists say Never for the same commands, so "avoid" left the general skill and the loop skills disagreeing;
   and the blanket phrasing covered `review history`, which this same file lists two sections later as a
   command the author runs. It now also says both reasons, which no other sentence in the skill gives together:
   those commands are the reviewer's role, and they need a terminal and refuse with exit 2 against a pipe or a
   file.
1. "An alternative is a --no-ff merge of the parent or destination branch" — the destination half is right, the
   parent half is not. What preserves an approval is not rewriting history, not `--no-ff`; merging the
   destination in passes, while merging an unlanded parent in is refused, because content is credited only when
   the destination carries it. `internal/lifecycle/drift_credit_test.go:32` is the passing case,
   `internal/cli/contribution_gate_test.go:46` the refused one.
1. "the `Review-Head` marker commit is an ancestor of HEAD" — the commit named by the approval's `Review-Head`
   trailer, not the marker commit, and only an approval is tested this way. `internal/cli/check.go:690-726`.
1. "changes since that most recent approval review are only in `changesets/<slug>`" — the anchor is the newest
   marker's tree, which is the approval only when nothing has been said since; and the exclusion is exactly this
   changeset's directory, so another changeset's directory counts as drift.
   `internal/lifecycle/lifecycle.go:571-624`.
1. The third bullet was the exception to the second rather than a third condition, so the three are now two with
   the credit rule inside them.
1. "Push it so the owner or their CI can be notified" — nothing is notified. `change integrate` writes a commit;
   the push is what makes it readable to whoever polls or reads the queue.
1. "Trust that the owner of the destination branch will land it, unless explicitly instructed otherwise" — see
   the note below, this is the one I want your ruling on.
1. Two sentences had lost their endings: the `git pair change discussion` clause had no full stop, and the
   authorship sentence read as two fragments. It now says why authorship needs annotating: history does record
   who wrote each line, and a reader of the file cannot see that.

Your edit also exposed a gap that predates it: the skill names `.git-pair/about-template.md` and never mentions
`.git-pair/discussion-template.md`, although the scaffold works the same way. One clause added.

### The one I want your ruling on

`unless explicitly instructed otherwise` is the only text in the repository that loosens the no-merge rule, and
PRD §22 step 15 ("stop there. Landing is not the agent's step"), PRD §26 ("There is no carve-out"), README's
agent contract, `references/integration.md` and both integration skills' Never lists all state it
unconditionally. The only flexibility any document admits is keyed to the repository's declared workflow
(`.dev-profiles/references/alternative-workflows.md`: "Never merge unless the repository's workflow says the
merge is yours"), not to a session instruction.

I kept what I think you meant — an instruction to "land" means `check`, `change integrate`, push, and report —
and keyed the exception to the workflow rather than to the instruction:

> Asked to land a change, you declare it: `check`, `change integrate`, push, then report what is waiting. The
> merge becomes yours only where the repository's own workflow says it is; a session instruction does not make
> it yours, and `git pair change integrate` is never the merge.

If you intended a real escape hatch for "the human said just merge it", the honest version needs PRD §22 to say
so too, because that is the list an agent actually follows.

> Ok sounds good.

### Two things I found and did not touch

- `README.md:2426` advises, for an agent or a cron job with no terminal: "edit the files directly and use
  `git pair diff`, `git pair status` and `git pair review submit`." Correct about terminals, wrong about roles —
  it is the sharpest of several places where "avoid" and "never" disagree across the documents. It is not about
  threads, so it is not in this changeset; say the word and it becomes a one-line follow-up.
- The dev-profiles Never lists still name `review thread`, which this changeset deletes. Recorded in `ABOUT.md`
  as an upstream fix, since those files are vendored.

### A second pass, after the offer was refused

`change ready` came back with 14 of your lines still standing in `skills/git-pair/SKILL.md` — the survival check
working on a document rather than on a comment. They are all wording you improved and I kept deliberately: the
description of the file in the opener, the landing bullet, the stacked-child paragraph, the `Review-Head`
warning, the "ask for review with `change ready`" mistake. Each was read, so the offer goes in with
`--allow-surviving-review-additions`, which is what that flag exists to state.

Going through them that way turned up one your rewrite left behind. The bullet under _Reviewer text in an
implementation file_ said to "append the reviewer content and your reply under the relevant heading" — three
lines under a paragraph that says "do not transcribe the comment before deleting it". Both cannot be the rule.
The bullet now says to write your reply under the heading and to quote their line only when the exact wording is
what is in question.

One thing you will see that is not in this branch: the refusal quoted "in `ABOUT.md` or in a new thread". That
came from `~/.local/bin/git-pair`, which is built at `0cb0d1d` and still has the old thread vocabulary; the code
here prints `DISCUSSION.md; git pair change discussion writes the scaffold`, and the handoff was done with a
binary built from this branch.

I deleted `skill-edits.md` after answering here. It was the per-topic file shape this changeset removes, and the
review commit already keeps your wording in history.
