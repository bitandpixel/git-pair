# feat-inline-review-guidance

The loop told an author to inspect a reviewer's inline comment. It never said the comment should leave the
file once the work it asked for was done, so an answered `// Please use a transaction here.` could ride into
`main` as a permanent annotation. PRD §19.5 states the policy — apply it, delete it, and record it only when
it needs discussion — and the skill, the README and the refusal the author actually reads now say the same
thing.

## Summary

**The check existed; the resolution did not.** `change ready` refuses while a non-blank line added by the
latest review submission survives unchanged at `HEAD` (PRD §19). The guidance around it said "address,
respond to, remove, or consciously retain each one", which leaves a reviewer's comment in the source as a
legible outcome. It is not one: a comment in an implementation file is a request written into somebody else's
code, and the request is finished when the work is done and the line is gone.

**The record already exists, so transcription is not required.** The review commit introduced the line and a
later commit removes it, which is the whole exchange, findable in history. A written record earns its place
only when the point needs further discussion, elaboration, or another person. Its two homes are an
`Addressed feedback` section of `ABOUT.md` and a thread under `changesets/<id>/`.

**Nothing enforces this, and that is stated rather than hidden.** Deleting an addressed comment makes the
addition disappear from `HEAD`, so the diagnostic stops reporting it — and an author can delete a comment
without doing the work. The check guarantees inspection; §19.5 says what inspection should conclude.

## What changed

**`PRD.md` §19.5 "Resolving a surviving addition"** — the policy, in four cases: an addition the author
resolves is applied and then deleted in the same commit; the author does not copy it first; the substance is
recorded when it needs discussion, elaboration or collaboration, in `ABOUT.md` §6 or a thread §7, with the
choice following the content; an addition the author keeps is not resolved by deletion and goes to §19.2's
override. The section closes by naming its own unenforced-ness. §9.1 gains one line pointing at it from the
`change ready` example, and §20 gains one stating that the absence of a comment/code distinction in MVP does
not make the two answers equal.

**`skills/git-pair/SKILL.md`** — a "Reviewer text in an implementation file" subsection after the
surviving-additions check, holding the same rule plus two things an agent will otherwise do: answer in place
with a `// done` reply, which leaves reviewer text in the file and is a worse answer than the one in
`ABOUT.md` or the thread; and transcribe the comment before deleting it, which history has already done. The
`Mistakes that cost a review cycle` list gains "Leaving an answered reviewer comment in a source file."

**`internal/cli/report.go`** — the surviving-additions refusal's closing line. It read `Review these
additions before continuing.` and now carries the resolution and the recording rule. This is the moment the
author hits the rule, so it is the moment they should be told it exists; a hint that only says "review
these" sends the policy to a document they are not looking at.

**README** — the loop narrative after `change feedback` states the deletion and where a record goes; the
`change ready` transcript matches the new output; the error-reference entry for
`N additions from review <sha> still survive unchanged` says what resolving means, beside the sentence that
already says what the flag is for.

## Design decisions

**The runtime message carries the policy.** Four surfaces state this rule and only one of them is guaranteed
to be in front of the author at the moment it applies. The others are read earlier and remembered worse. The
cost is a longer refusal, which is the right trade for a check whose purpose is to make the author read
something.

**No heading in the `init` scaffold.** An `## Addressed feedback` heading in every `ABOUT.md` is a section
that is empty most of the time, and an empty section is an invitation to write nothing in it. The scaffold
stays on the §6 headings; the guidance says to add the heading the first time there is something to put
under it.

**History is the record, so deletion is not loss.** The alternative — keep the comment, or move its text into
`ABOUT.md` before deleting — makes the source file a review transcript and duplicates a conversation git
already dates and attributes. The rule inverts the default: recording is the exception and needs a reason
(discussion, elaboration, collaboration), because the review commit is already the durable copy.

**Thread versus `ABOUT.md` follows the content, not a rule.** A thread is a conversation with a future
reply; `ABOUT.md` describes the change to a reader who will not reply. Deciding that by rule ("always threads")
would put facts about the change where a reviewer looking at the change will not look.

**The PRD's own example transcripts are untouched.** §9.1 and §19.2 illustrate the refusal with
`Review these additions before marking the change ready.`, wording the implementation never had
("before continuing"), and the README is the document that mirrors real output. Rewriting the spec's
illustration to quote bytes is a different change from adding the policy, and §19.5 is now the place the
spec states it.

**The wording is "remove the comment it came from", not "delete the addition".** Some additions are code
the reviewer wrote, and the answer for those is usually to keep them and acknowledge. The hint keeps the two
outcomes apart in one clause and leaves the rest to the `To intentionally preserve them:` block beneath it.

## Validation

- `go test ./...` green across all packages, including `docs_contract_test.go` over PRD, README and every
  page under `skills/` — the new prose names only commands and paths this build answers to.
- Manual, in a scratch repository (`/tmp/scratch-inline`), with the check driven end to end: a review
  submission adding two `//` lines to `src/service.ts`, then `git pair change ready` printing both
  `path:line` entries, the new closing paragraph, and the override hint, exiting 1.
- Manual, same repository: applying the fix and deleting both comments in one commit, then
  `git pair change ready` succeeding and returning the changeset to the queue — the deletion the guidance
  recommends is the resolution the check accepts, with no flag involved.
- The gate scripts under `scripts/gates` exercise the review loop and the TUI; nothing in this changeset
  changes either path, only the text printed when one of them refuses.

## Known limitations

- Nothing verifies an author deleted a comment because they addressed it. The diagnostic's guarantee is
  inspection; this changeset changes what the inspection is told, not what is enforced.
- The hint is prose in a Go string, so no test pins it. `README.md` quotes it and its docs check verifies
  command names, not output bytes; a rewording can drift out of the README silently. That is the same
  limitation every other transcript in the README has.
- The rule is now stated in four places (PRD §19.5, the skill, the README, the refusal). The docs contract
  test keeps the names honest across them and does not keep the sentences identical.
- `git pair skill list` reports an installed copy `stale` after this lands, because the skill is compiled
  into the binary and these bytes changed. `git pair skill install --force` is the refresh.

## Open questions

- Should `git pair change feedback` print the same closing paragraph, or is one mention per review round
  enough? `change feedback` is where the author first sees the comment; `change ready` is where they are
  stopped by it.
- Should the reviewer-facing side say anything — that a comment left in an implementation file is expected to
  be deleted once answered? There is no reviewer document to put it in today: `review open` is a TUI and the
  reviewer edits files in their own editor.
- Is `Addressed feedback` the right name for the section? It reads as a list of comments received; the
  alternative framing is a "Responses to review" section, which invites prose and duplicates the threads.
