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

- `PRD.md` §26 — the scope sentence after the no-carve-out paragraph; §27 "Remote record enforcement" — the
  pre-push entry now says what closes and what stays deferred.
- `skills/git-pair/references/hooks.md` — new; `SKILL.md` links it. `git pair skill list` reports 5 files.
  It carries the three acceptance states and the third option — keep running the version you read, by copying
  it into the per-clone root.
- `README.md` — a **Lifecycle hooks.** entry in Concepts: discovery, ordering, environment, trust, what a
  failure means, and the CI pointer.
- `internal/hygiene/hygiene_test.go` — the hook runner's shapes in the self-test fixture, both directions.

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

- `go build ./...` clean; `gofmt -l internal cmd` empty.
- `go test ./internal/hygiene/` green, including the fixture additions: the planted hook-shaped `git push` is
  reported on its marked line, and none of the four hook shapes are reported.
- `go test ./internal/cli/ -run Docs` and `-run TestTheCommandTreeIsTheCommandSet` green, which is what holds
  the new PRD and README prose to the commands that exist.
- `go test ./internal/cli/ -run 'Skill|Docs'` green, and `git pair skill list` reports the skill at 5 files
  with the new page among them.
- `git pair skill show references/hooks.md` renders the new page from the compiled-in copy.
- Every command and flag the new pages name (`git pair hooks`, `--trust`, `check --json`, the variable names)
  was checked against the built binary rather than from memory. The hooks output example this milestone's
  pages describe was regenerated from a fixture run in M4, which is how its earlier, invented version was
  caught; the new pages were written against the same run.
- `mise run check` exited **0**, twice. The run that counts re-ran every package: 36 test shards ok, none
  served from cache, no `FAIL`, and its `gofmt -l internal cmd` step reported nothing. The run after this
  file was written exited 0 with all 36 shards served from cache, which is the expected shape when the only
  change since is markdown.
- Restacked onto M1's answers to the reviewer's feedback, twice — once for the two questions, once for the
  `trust_state` work that came out of the vendoring discussion — and re-run each time: exit 0, 36 shards ok,
  no `FAIL`, `gofmt` silent. The second round reached this milestone as content rather than as a fact: the
  hook reference and the Concepts entry both have to state the three acceptance states, because a page that
  tells a person to run `--trust` when the repository moved a check under them is the page that gets them to
  approve a script they never read.
- Restacked onto M3's landing (`29f49e8`) and then onto M4's restacked head, and re-run: `mise run gates`
  exited **0** with 90 checks — the test shards, the end-to-end script, the PTY walkthrough and the cost
  gate, which measured 49 invocations for a two-level stack against the ceiling of 50. Nothing collided: the
  two changes under this milestone since it was cut are M3's `check` gate and the landing-advice work that
  reached `main` while the stack was in review, and this milestone documents code rather than editing it.
- One sentence in each of the two pages it owns moved on the restack, because M4's second feedback round
  settled it after they were written: a refused submission's hook output is shown **on the review screen**
  when the submission came from the screen, not on stderr, where the frame would paint over it. Both pages
  said "replayed on stderr with the refusal" and nothing more, which after M4 was a description of the CLI
  path only. The PRD's hook section and the reference's output paragraph now carry the exception.

## Known limitations

- The §26 sentence describes scope in prose. The hygiene test still enforces exactly what it always
  enforced — shipped source — and nothing in the test can distinguish "git-pair pushing" from "a hook
  pushing", because a hook is not shipped code and never appears to the scanner.
- `references/hooks.md` duplicates the environment table that `references/cli.md` also carries. Both are
  true and both are read: `cli.md` while driving commands, `hooks.md` while writing a script. A shared
  include does not exist in this skill format.
- The README's Concepts entry points at the CI section for the recipe rather than repeating it, so a reader
  who arrives at Concepts and stops there gets the rules without the pipeline.

## Open questions

- M1's question stands, with a direction recorded and one decision still owed: should a repository that has
  committed tracked hooks refuse outright in a clone that has not accepted them? The skill page tells an agent
  to report the state rather than run `--trust`, which makes the unaccepted state visible but leaves the
  product answering "ready" without the repository's own checks having run. The direction, the store chosen
  for `declined`, and the decision that still has to be made are answered under _Addressed feedback_ below,
  and written up as M6 in `docs/plans/lifecycle-hooks/plan.md`.
- Nothing persists a hook result, so no page can promise a reader will be able to find out later what ran.
  If that ever matters, the results store M1 declined to invent is the piece.

## Addressed feedback

- **"I'm leaning towards making this mandatory. We could perhaps create an explicit declined state as an
  escape hatch?"** — on the open question about a repository that has committed tracked hooks in a clone that
  has not accepted them. Answered 2026-10-14, and the answer is a handoff rather than a change to this
  milestone.

  The direction is agreed: make the unaccepted state cost something, and give a person who means to opt out an
  explicit `declined` to choose instead of a silence that looks the same from outside. Where `declined` is
  recorded is decided: per-clone, beside `trusted-hooks` in the git directory
  (`trustRecordPath`, `internal/hooks/trust.go`), keyed by the same tree hash acceptance is keyed by — a hook
  added to a declined tree is new code and returns to `unaccepted`.

  What that store decides about the gate, and why it is not this milestone's to answer: a per-clone record is
  a fact about one machine, exactly like acceptance. `status`, `queue` and `git pair hooks` can all report it,
  and it can stop nagging the person who chose it. A verdict another clone has to reach cannot read it, and CI
  is a clone that has never accepted anything — so "`check` refuses while a tracked hook is unaccepted" would
  refuse everything in a fresh clone, and the way for a pipeline to pass would be to run
  `git pair hooks --trust`, which hands consent to the runner. M1 refused that shape for the same reason and
  says so where the trust rule is written: "refusing to work in a clone one has not yet approved would make
  the safety mechanism the thing people route around".

  So the enforcement change needs one more decision before it can be written — whether mandatory lives inside
  the verdict (which needs a shared, reviewed statement that a repository's hooks are declined, with the
  per-clone record as the personal layer over it) or stays out of the verdict and lives in reporting. It is
  written up as **M6 — The unaccepted state, made a decision rather than an absence** in
  `docs/plans/lifecycle-hooks/plan.md`, with the four states it implies (`accepted`, `changed`, `declined`,
  `unaccepted`) and the hash-keyed return to `unaccepted`. What this milestone changed is the open question
  above and the plan; the PRD still describes what the code does today, which is the rule M5 exists to write
  down.
