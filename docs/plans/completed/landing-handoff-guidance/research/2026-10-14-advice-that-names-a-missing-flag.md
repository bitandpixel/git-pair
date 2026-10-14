# Advice that names a flag this build does not answer to

Question: two sentences git-pair emits as advice tell the author to run `git pair change ready --changeset <id>`.
Does that command exist, and what should the advice say instead?

## What is emitted

| Where | Sentence |
| --- | --- |
| `internal/cli/stacked.go:358` | "this head is already on `<landing>`, so what it now contributes was never read — re-offer it with `git pair change ready --changeset <id>` and have the result reviewed again" |
| `internal/cli/stacked.go:600` | "`git merge <destination> && git pair change ready --changeset <id>`" (the reason a child that conflicts with the destination is refused) |

Both came in with `feat-next-action-fixes`, and both were asserted verbatim by its tests — the tests pinned the
string, so the runnability was never asked.

## Is it runnable?

No. `--changeset` is a flag on some commands and not on `change ready`:

```console
$ git-pair change ready --changeset whatever
git-pair: unknown flag: --changeset
$ git-pair change ready --help | sed -n '/Flags:/,$p'
      --allow-surviving-review-additions   acknowledge surviving review additions and mark ready anyway
  -h, --help                               help for ready
```

Which commands carry it (`--help` on each):

```
status --changeset      yes      change feedback --changeset   yes      review history --changeset  yes
check  --changeset      yes      change ready  --changeset     no       change wait --changeset    no
                                                                                change integrate    no
```

The commands that act on a changeset the reader names take the flag; the commands that act on *the current*
changeset — `ready`, `unready`, `integrate`, `abandon`, `wait` — do not, because their subject is the branch
(`git pair change ready` finds the changeset this branch carries). So the advice is not merely wrong, it asks
for a selector the command deliberately refuses: the branch-shape rule is what makes `change ready` unambiguous.

## What the advice should say

Plain `git pair change ready`. On the branch the author is standing on, the changeset is already determined.
That holds for both emitted cases: the child whose head is already on the landing, and the child that must
merge the destination before re-offering — each is standing on its own branch, which carries one unlanded
changeset of its own.

The shape where "the current changeset" is genuinely ambiguous is a branch carrying two unlanded directories of
its own, and that shape already has its own answer, so the advice should not invent a third:

- `change stack` refuses and names `change combine --into <survivor>` or taking a directory out of the branch
  (`internal/cli/change_stack.go:141`);
- `change combine` refuses when the branch carries one or more than two (`internal/cli/change_combine.go:100,109`);
- the tie itself is a usage error, exit 2, on the reasoning that both ways out are things the user can type
  (`internal/cli/root.go:688`).

So the correction is a deletion, and the multi-changeset case stays with the commands that already explain it.

## The question this raises that outlives the fix

Nothing checks an emitted sentence. `docs_contract_test.go` checks that commands named in the PRD, the README
and the skill exist, and that no command leaves those documents — prose against the tree, in both directions. An
advice string assembled with `fmt.Sprintf` at the point where a verdict is composed is not prose in a document,
so it is invisible to both checks, and a test that asserts the string verbatim will happily pin a command that
fails.

M1 therefore builds the per-command version of the same check for emitted advice: collect the `git pair …`
strings from the four advice surfaces, resolve each against `commandPaths(newRootCommand(&app{}))`, and then
check the flags against *that* command rather than against the union. The union would pass this bug —
`--changeset` exists somewhere — which is exactly the mistake the two sentences made.
