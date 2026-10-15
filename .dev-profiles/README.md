# Development workflow profiles

This directory holds the repository's development-workflow **profiles**: named, versioned answers to "how
does a change get from an agent to the destination branch here".

`../../.dev-profiles.toml` names which profile this repository uses. That file is the only workflow signal
anything outside this directory reads.

## The two kinds of skill

|                      | Answers                                               | Examples here                                 | Who ships the skill              |
| -------------------- | ----------------------------------------------------- | --------------------------------------------- | -------------------------------- |
| **profile skill**    | what happens in this repository, in what order        | `git-pair-workflow`, `pair-stack`, `forge-pr` | this library                     |
| **capability skill** | how to drive one tool, with no opinion about workflow | `git-pair`, `gh-cli`                          | the tool itself, or this library |

A profile never says how to drive a CLI, and a capability never says what the workflow is. Keeping that line
clean is what lets `forge-pr` and `git-pair` share the GitHub rules instead of each keeping a copy.

## Layers

```
~/.pi/agent/skills, ~/.agents/skills   principles, plus a fallback that defers. No standards.
.dev-profiles.toml                     which profile, which version
.dev-profiles/profiles/…               the library: skills, CI, versions, migrations
.agents/skills/                        materialized copies the harnesses read (committed)
AGENTS.md                              this codebase: commands, architecture, traps. Not the workflow.
```

`dev-profiles install` writes the materialized copies. Selection happens once, in a commit — a harness never
resolves anything at session start; it finds whatever directories discovery can see.

## What is workflow-agnostic, and why that matters

`.dev-profiles.toml` belongs to this architecture. `changesets/`, `ABOUT.md`, `Review-*` trailers and
`git pair` commands belong to the `git-pair` profile. Nothing outside a profile names them. A fallback or a
CLI that does has turned a generic layer into a worse copy of one specific workflow, and it will be wrong the
moment the repository stops using that workflow.

Where a generic layer needs something about a tool, the tool's own manifest says it — see
`capabilities/git-pair/capability.toml`.

## Commands

```bash
.dev-profiles/bin/dev-profiles status    # what applies here, and whether it agrees with itself
.dev-profiles/bin/dev-profiles install   # write the materialized copies this profile requires
.dev-profiles/bin/dev-profiles doctor    # every check at once; exit 1 means something needs attention
.dev-profiles/bin/dev-profiles diff      # installed copies vs the library
```

`doctor` is the enforcement. Anything in a profile that no check tests is documentation, and gets labelled as
such.

## Adding a profile

1. `profiles/<name>/` with `profile.md`, `profile.toml`, `VERSION`, `CHANGELOG.md`, and `skills/<skill>/SKILL.md`.
2. Every skill's `description` is the routing mechanism — a harness reads only `name` and `description`
   before activating. Say what the skill is for and, just as importantly, when it does not apply.
3. Add it to every profile's `references/alternative-workflows.md`.
4. Never add a tool's command names to a generic layer. Add a capability manifest instead.

## Versioning

`VERSION` in the profile directory is the library's version. `version` in the marker is what this repository
has adopted. When they differ, `doctor` prints both and the `CHANGELOG.md` line for the gap — which is the
whole point of the number: a workflow change becomes a reviewable version bump instead of a quiet drift in
three files.
