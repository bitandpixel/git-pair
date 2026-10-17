# The headless-command advice in README stops naming a reviewer command

## Purpose of the change

README's troubleshooting entry for the terminal refusals told anyone with no terminal — "an agent, a pipe or
cron" — to "use `git pair diff`, `git pair status` and `git pair review submit`". The command does work
headless, so the advice was not nonsense. It was still wrong: a submission is a verdict, and PRD §22 keeps it on
the reviewer's side of the pair. Someone following that line at a script records a review they were not entitled
to record, and nothing in git-pair stops them, because identity is out of scope.

## What changed

One sentence, at the `opening an editor needs a terminal` entry of the refusal list. The commands an agent is
pointed at are now `git pair diff`, `git pair status` and `git pair review history`, which are reads.
`git pair review submit` keeps its own clause: it runs with no terminal, it stays the reviewer's to give, and an
author who wants the submission reads it with `git pair change feedback`. PRD §22 is cited beside the rule, the
way its neighbours cite their sections.

## Design decisions and why

Removed from the agent's list rather than deleted from the paragraph. The headless fact is true and worth
keeping: a reviewer scripting a verdict from CI or from a shell is a real case, and this is the place a person
looks it up. What was wrong was the list an agent follows, not the sentence's subject — so the clause separates
the two instead of dropping one of them.

`review history` is the replacement because it answers the same underlying question — what has been submitted
against this branch — without going near the verdict. `change feedback` is named too because the author at that
point in the document is the likelier reader, and it is the command their own loop tells them to run.

## Validation performed

`bash scripts/format.sh check`, then the docs contract tests — `TestCommandsNamedInTheDocsExist`,
`TestEveryCommandIsNamedInTheDocs`, `TestTheCommandTreeIsTheCommandSet` — which is what keeps `review history`
and `change feedback` from being commands this prose invented. Then `mise run gates`: the sharded suite plus
`e2e-29.sh`, `pty-walkthrough.sh` and `ci-integrate.sh`.

No test asserted the old sentence. `grep` for it across `internal/` and `scripts/` returns nothing, which is the
reason the line survived: the docs contract test checks that named commands exist, not that advice about who may
run them is correct.

## Known limitations and open questions

The disagreement this line came from is wider than one sentence. The general skill's reviewer-command rule and
the profile skills' Never lists were only just brought into agreement, in
`feat-discussion-file-replaces-threads`, and three dev-profiles Never lists still name the removed
`review thread` — an upstream fix, not this one.

No other mention of the reviewer's four commands advises an agent to run one. README, PRD and the shipped skill
were read in full for it: every remaining `review submit` either defines the artefact, names a hook point or a
refusal, or shows a reviewer's own invocation.
