# Help text names a command that is gone, and nothing could see it

## Summary

`git pair review submit --help` told the reviewer that `git pair integration record` writes the durable refs,
once, at landing. The command was deleted, the refs were deleted with it, and the screen still said so. This
changeset fixes the sentence and closes the gap that let it survive: help text is now held to the command tree
the way the documents are.

## What it does

- `review submit`'s help says what a landing is instead of naming a removed command: the integration branch
  carrying `changesets/<id>/` is the record, and no ref is written for that either. It is the sentence PRD
  §13.4 already states, in the one place the tool still contradicted it.
- `TestHelpTextNamesCommandsThisBuildAnswersTo` walks every command's `Long` and `Example` out of the built
  tree and refuses four things: a `git pair …` reference this build has no command for, a group named with a
  word that is not one of its children, a flag the named command does not declare, and a ref path under the
  git-pair namespace or a durable-ref claim.
- The flag and subcommand rules are read through `checkCommandReference`, extracted from
  `TestEmittedAdviceNamesCommandsThisBuildAnswersTo` so advice and help cannot drift into two standards. The
  ref-namespace pattern is one package-level `durableRefPath`, read by the document check and the help check
  alike.
- The comment above `TestNoDocNamesADurableRef` named a different test (`TestRefPathsInTheDocsAreOnesWeWrite`)
  and is corrected, since the sentence is what a reader follows.

## Why

Three surfaces name commands, and two were checked. `docs_contract_test.go` holds the documents to the command
tree in both directions, and `advice_contract_test.go` holds the sentences the tool emits at run time. Help text
was neither of those: not a file on disk the document checks read, not a sentence printed by a run. That is how
`git pair integration record` stayed in `--help` through the milestone (`5abeb3a`) that deleted the command, the
namespace it wrote, and the code that wrote it — the same milestone added `TestNoDocNamesADurableRef`, which
reads documents and nothing else.

Help text is the surface a person reads immediately before typing the command, so a name that is wrong there
costs them a usage error from their own tool at the moment they are least expecting one.

## Design decisions

**The two fields are read differently, because they are written in different registers.** A `Long` cites a
command inside backticks and writes English around it, so each backtick span is the reference and the words
outside are prose. That is the reading which lets "`git pair review` is `git pair review open`" mean what it
says; the naive reading reports the second mention as a subcommand of the first (`review is`, `review and`,
`review does`), which is what the first draft of this test did before the span rule went in. An `Example` block
is a command line somebody copies, so each of its lines is a reference in full, flags included.

**`adviceCommands` is not reused, and `checkCommandReference` is.** The advice scanner stops a command path
where the tree stops continuing and drops the reference outright when nothing resolved — right for a sentence
the tool assembles, wrong here, because a deleted command is exactly a reference whose first word the tree does
not continue. Reusing it would have passed the old sentence. The rules that decide whether a resolved reference
is legitimate (children, flags) are shared, so a flag that belongs to another command is one failure in one
place.

**Help states what the tool does; the spec carries what it stopped doing.** The durable-ref rule is therefore
stricter on help than on the documents, which are allowed to explain a retired mechanism — PRD §13.4 and §11.4
do exactly that. Nothing in a help screen needs to say the tool once wrote refs.

**A flag written with its value is checked by name, and a bare `--` names nothing.** `--since-review=-3` is a
reference to `--since-review`, and `--` is a separator; both were reaching the flag map as names no command
declares. This is the reading the shared helper applies to both surfaces.

## Known limitations

- It checks names, not accuracy. A help sentence can describe a real command's behaviour wrongly and pass, the
  same limit the document checks state about themselves.
- `Short` is not read. Cobra writes the command's own name into the listing around it, so a name there is
  cobra's rather than the author's.
- A command cited in a `Long` outside backticks is read as prose and can carry a bad name through. That is the
  exemption the document check already grants, for the same reason: prose running on is common and a false
  accusation here teaches the author to stop citing commands.

## How to check it

- `go test ./internal/cli -run TestHelpTextNamesCommandsThisBuildAnswersTo` passes.
- Restore the old sentence to see the guard bite: put back
  `` ` + "`git pair integration record`" + ` writes the durable refs, once, at landing. `` at the end of the
  paragraph in `internal/cli/review.go` and run the same command. It reports both the unknown command and the
  durable-ref claim.
- `git pair review submit --help` reads as a description of a commit and a branch, with no ref and no removed
  command in it.
- `mise run check` for the format check, vet, and the suite; `mise run gates` for the end-to-end and pty
  screens, which print help text.
