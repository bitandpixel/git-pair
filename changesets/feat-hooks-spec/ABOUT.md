# feat-hooks-spec

## Summary

The last milestone of the lifecycle-hooks plan: it puts in writing what the feature changed about the
product's two hardest-won rules, and ships the reference an agent reads when a repository has a hook that
did not run.

PRD §26 now names its own subject. It said "There is no carve-out. **git-pair may not push anything**, at any
point in a lifecycle", and a lifecycle hook is client code that may run `push`. The sentence added says the
rule binds what this tool invokes of its own choosing, and that a hook is the client's own code responding to a
git-pair event under the client's authority — a clarification of scope, not an exception. §27 says which half
of the deferred pre-push work this closes. A new skill page, `skills/git-pair/references/hooks.md`, carries the
contract for the person diagnosing a hook; the README gains the same material in Concepts.

This is the fifth milestone, stacked on `feat/hooks-reviewer-submit`.

## What changed

-   `PRD.md` §26 — the scope sentence after the no-carve-out paragraph; §27 "Remote record enforcement" — the
    pre-push entry now says what closes and what stays deferred.
-   `skills/git-pair/references/hooks.md` — new; `SKILL.md` links it. `git pair skill list` reports 5 files.
-   `README.md` — a **Lifecycle hooks.** entry in Concepts: discovery, ordering, environment, trust, what a
    failure means, and the CI pointer.
-   `internal/hygiene/hygiene_test.go` — the hook runner's shapes in the self-test fixture, both directions.

No production code changed. The behaviour these documents describe shipped in M1–M4.

## Design decisions

**§26 states scope; it does not ask for a carve-out.** The list in §26 is the reason the tool can be trusted
not to move a branch under anyone, and the sentence "there is no carve-out" is what makes it worth anything.
Adding an exception for hooks would have emptied it. The honest statement is narrower and stronger: the rule
binds git-pair's own invocations, and the hygiene test that enforces it reads shipped source, which is the same
subject. A hook running `git push` is the client's act, in the same way that a person's own `pre-commit` hook
calling `git push` is not git's doing. Drafted before the rest of the milestone, as the plan required, and
agreed with the reviewer first.

**The sentence goes after the strong claim rather than qualifying the bullet.** The alternative was to attach
the qualification to the bullet that names the hygiene test. That keeps "there is no carve-out" bare, and
leaves a reader who stops at the paragraph with a claim the next milestone contradicts. Scope stated next to
the rule is the version that survives re-reading.

**§27 stays one entry.** "pre-push hooks" was deferred because the tool owned no hooks at all. It now publishes
lifecycle events and runs the client's scripts, which closes the half where a team that wants a push check can
write one; what stays deferred is git-pair owning one and everything server-side. Splitting the bullet would
suggest two features where one changed state.

**The skill page exists for two rules the command reference does not carry.** `git pair hooks --trust` is a
person's consent act — "I read this script and I allow it to run on my machine" — so an agent reports the
unaccepted state and names the command instead of running it. And `GIT_PAIR_HOOKS=0` belongs to whoever owns
the machine; an agent who sets it has removed the check the repository put there. Both are the kind of thing
that reads as harmless convenience and is actually the feature being bypassed, which is why they are in the
page an agent reads while diagnosing, not only in a trust section. The page also states the per-clone cost
plainly: a check under `<git-dir>/` runs on one machine, so a rule that must hold for the team is the committed
one.

**The hygiene fixture covers both directions for the hook runner.** The product scan already walks `cmd` and
`internal`, so "`internal/hooks` invokes no forbidden verb" is a fact about the shipped tree, not something a
fixture can add. What the fixture adds is that the scan neither goes vacuous nor turns over-eager on the
feature's own shapes: a hook run by path with no argv, environment and root-name literals, and a config key
containing the tool's name are each marked OK, and a planted `exec.Command("git", "push", "origin")` in a
hook-shaped function is marked BAD. The OK cases are the point — when a scan starts flagging legitimate code,
the next engineer loosens the rule rather than the code.

## Validation

-   `go build ./...` clean; `gofmt -l internal cmd` empty.
-   `go test ./internal/hygiene/` green, including the fixture additions: the planted hook-shaped `git push` is
    reported on its marked line, and none of the four hook shapes are reported.
-   `go test ./internal/cli/ -run Docs` and `-run TestTheCommandTreeIsTheCommandSet` green, which is what holds
    the new PRD and README prose to the commands that exist.
-   `go test ./internal/cli/ -run 'Skill|Docs'` green, and `git pair skill list` reports the skill at 5 files
    with the new page among them.
-   `git pair skill show references/hooks.md` renders the new page from the compiled-in copy.
-   Every command and flag the new pages name (`git pair hooks`, `--trust`, `check --json`, the variable names)
    was checked against the built binary rather than from memory. The hooks output example this milestone's
    pages describe was regenerated from a fixture run in M4, which is how its earlier, invented version was
    caught; the new pages were written against the same run.
-   `mise run check` — result recorded below.

## Known limitations

-   The §26 sentence describes scope in prose. The hygiene test still enforces exactly what it always
    enforced — shipped source — and nothing in the test can distinguish "git-pair pushing" from "a hook
    pushing", because a hook is not shipped code and never appears to the scanner.
-   `references/hooks.md` duplicates the environment table that `references/cli.md` also carries. Both are
    true and both are read: `cli.md` while driving commands, `hooks.md` while writing a script. A shared
    include does not exist in this skill format.
-   The README's Concepts entry points at the CI section for the recipe rather than repeating it, so a reader
    who arrives at Concepts and stops there gets the rules without the pipeline.

## Open questions

-   M1's question still stands: should a repository that has committed tracked hooks refuse outright in a clone
    that has not accepted them? The skill page now tells an agent to report the state rather than run `--trust`,
    which makes the unaccepted state more visible but leaves the product answering "ready" without the
    repository's own checks having run.
-   Nothing persists a hook result, so no page can promise a reader will be able to find out later what ran.
    If that ever matters, the results store M1 declined to invent is the piece.
