# The two files that run this repository's CI are not this repository's

## Why this change exists

The layer below this one made `.github/workflows/git-pair-check.yml`, `.github/workflows/git-pair-integrate.yml`,
`scripts/ci/git-pair-integrate.sh` and `scripts/ci/install-git-pair.sh` installed bytes — written by `install`,
byte-compared by `doctor`. The documentation still described them as files this repository wrote, and PRD invited a
reader who wanted that pipeline to _copy one_. That advice is how the shape went stale in the first place: the
library's own copy of the merge job had a `timeout-minutes` GitHub would not schedule and an installer that never
told anyone where to put the binary, and both survived every text assertion pointed at them. A copy is the artifact
that drifts; the profile is the thing that gets fixed.

## What becomes different

A reader can tell, from the page they are on, which bytes they are allowed to edit.

README's landing table gains a **whose** column, and now lists all five files rather than three — the four the
profile installs, and the replay this repository keeps:

| file                                                                                                                                   | whose             |
| -------------------------------------------------------------------------------------------------------------------------------------- | ----------------- |
| `.github/workflows/git-pair-check.yml`, `git-pair-integrate.yml`, `scripts/ci/git-pair-integrate.sh`, `scripts/ci/install-git-pair.sh` | the profile's     |
| `scripts/gates/ci-integrate.sh`                                                                                                        | this repository's |

README's CI paragraph used to say `.github/workflows/ci.yml` runs the ladder "— one line, because the task is the
definition". It now names `git-pair-check.yml`, and says what both files actually do: neither repeats the command,
each resolves it from `.dev-profiles.toml`, and both run this repository's `toolchain` answer first. PRD's pointer —
"nothing here performs the merge… a repository that wants a pipeline to perform it can copy one: `.github/workflows/…`"
— now sends that reader to declaring the profile, and keeps the claim that made the distinction worth stating: the
replay stays the consumer's own, because a replay somebody else owns cannot fail this repository's build.
`.mise.toml`'s goreleaser comment named `ci.yml` in passing; it names the check workflow instead.

## How it was implemented, and why this way

The edits stay at the level of behaviour and ownership. Nothing in them describes how the YAML is maintained, which
is the profile's business and is in the profile's skill — a README that re-explains the profile is a second copy of
the same prose, which is the thing this change is removing.

PRD keeps its section references (`§26`, `§11.3`, `§13`) and its argument intact; only the direction a reader is
sent changed, so a spec reader who has never heard of `dev-profiles` still gets the product's own claims and one
clause about where the pipeline lives. The `whose` column was preferred over deleting the table, because the table's
job is orientation and the ownership question is exactly what a reader arriving at it needs answered.

Left alone deliberately: `docs/plans/` and the landed changeset records, which are history and would be falsified by
editing them; and the historical prose inside `scripts/gates/ci-integrate.sh` that names `gh workflow run ci.yml`
while describing six dispatches a past fixture actually made. That text is accurate about the past, and rewriting it
to name a file that did not exist then would make it false.

## What is deliberately not changing

No behaviour, no Go file, no task, no version. The parent layer's four installed paths are untouched here — this
branch changes three documents and one comment, 50 inserted lines, 18 removed.

## Where to look hardest

**One sentence is an argument, not a fact.** PRD now says the replay stays the consumer's own "because a replay
somebody else owns cannot fail this repository's build". That is the reasoning I would defend — the gate is how this
repository proves the merge job still works, and a byte-compared copy could not fail a build — but a reviewer who
thinks the replay should also come from the profile should say so now, because the alternative is a consumer whose
gate drifts from the thing it gates.

**How much `dev-profiles` belongs in product docs at all.** README's reader is someone evaluating the tool, not
adopting the workflow; I kept the mentions to ownership and one pointer. If the reviewer thinks the README should
not name the library outside the landing section, that is a cut, not a rewrite.

## How we know it works

```bash
sed -n '/^| file | whose | role |/,/^$/p' README.md | awk -F'|' 'NF>2 {print NF-2}' | sort -u   # one number: well-formed table
grep -c "github.com/bitandpixel/git-pair" README.md                                              # release:check's slug line, intact
grep -rn "workflows/ci\.yml" README.md PRD.md .mise.toml .dev-profiles.toml                      # nothing
MISE_TRUSTED_CONFIG_PATHS=$PWD mise run gates                                                    # exit 0 in 116s
```

No test in the suite reads either document — the `README.md` and `PRD.md` strings in `internal/` belong to tests that
create their own fixtures — so the ladder is the check that the branch did not disturb anything that is checked, and
the greps above are the checks for the thing that is not: a stale file name where a reader would take it as an
instruction.
