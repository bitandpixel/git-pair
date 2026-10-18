# Help text names a command that is gone, and nothing could see it

## Summary

`git pair review submit --help` tells the reviewer that `git pair integration record` writes the durable refs
at landing. Neither exists: the durable-ref layer was deleted, and that screen is the last place in the built
tool still saying otherwise.

## What it does

- Rewrites the sentence in the `review submit` help to say what a landing is now, with no command named that
  this build does not have.
- Adds a contract test that walks every command's `Long` and `Example` out of the built tree and refuses a
  `git pair ...` reference the build does not answer to, a flag the named command does not declare, a ref
  path under the git-pair namespace, and a durable-ref claim.

## Why

The documents are already held to the command tree in both directions, and emitted advice is held to it too.
Help text is neither a document nor advice: it is the surface a person reads immediately before running the
command, and it is the one with no check over the commands it names. That is how a deleted command kept
shipping in `--help` through the milestone that deleted it.

## How to check it

- `go test ./internal/cli -run TestHelp` for the new contract, which fails on the old sentence and passes on
  the new one.
- `git pair review submit --help` reads as a description of a commit and a branch, with no ref and no
  removed command in it.
- `mise run check` for the whole suite.
