# Installing this skill into a repository or a machine

This skill ships inside the git-pair binary and inside its repository at `skills/git-pair/`. Both routes
put the same files where a harness looks for them; the command route also tells you when what is installed
is older than the binary you are running.

## With the command

```bash
git pair skill list                                    # what is installed where, and whether it is current
git pair skill install --harness agents --scope repo   # this repository: .agents/skills/git-pair/
git pair skill install --harness agents --scope user   # every repository on this machine
git pair skill install --dest /some/skills/dir         # anywhere else
git pair skill install --dry-run                       # resolve the destination, write nothing
```

`--scope` defaults to `repo` inside a git repository and `user` outside one. `--force` replaces the files
git-pair owns and leaves anything it did not write alone. Writing into a repository means committing the
directory, which is the point: the skill then travels with the code and every clone and CI job gets it.

Then check it worked, from the same directory you installed into:

```bash
git pair skill list
```

`current` means the installed bytes match this binary's. `stale` means they do not — re-run the install.
A harness you installed into but which still does not offer the skill was started before the files
appeared: restart it.

## By hand

Copy `skills/git-pair/` — the whole directory, with its `references/` — into the skills directory of the
harness you use. Copy the skill directory, not the repository's `skills/` container, which also holds the
Go file that embeds these files into the binary and the `AGENTS.md` stanza it prints.

## Where each harness looks

These are each harness's documented discovery rules, not a probe of what some version of it happens to
scan. They were read on 2026-09-24 from:

- **Codex CLI** — the Skills page at `developers.openai.com/codex/skills`: `.agents/skills` in the working
  directory, in every directory above it, and at the repository root; `$HOME/.agents/skills` for the user
  scope.
- **pi** — `docs/skills.md` in the pi distribution (read at pi 0.85.1): global `~/.pi/agent/skills/` and
  `~/.agents/skills/`, project `.pi/skills/`, and `.agents/skills/` in the working directory and its
  ancestors up to the repository root.
- **Claude Code** — the Skills page at `code.claude.com/docs/en/skills`: `.claude/skills/<name>/SKILL.md`
  in the project, read from the starting directory and every parent up to the repository root, and
  `~/.claude/skills/<name>/SKILL.md` for the personal scope.

If one of those documents changes, the table in `internal/cli/skill.go` changes with it — the same date and
sources are recorded in that file's `skillHarness` comment.

| Harness | Per repository | Per machine |
| --- | --- | --- |
| Codex CLI | `.agents/skills/`, in every directory from the working directory up to the repository root | `~/.agents/skills/` |
| pi | `.pi/skills/`, `.agents/skills/` in the working directory and its ancestors up to the repository root; `~/.agents/skills/` | `~/.pi/agent/skills/`, `~/.agents/skills/` |
| Claude Code | `.claude/skills/` in the starting directory and every parent up to the repository root | `~/.claude/skills/` |
| Anything else that reads a directory of skills | `--dest <dir>` | `--dest <dir>` |

`--harness agents` writes `.agents/skills/`, which Codex CLI and pi both read, so it is the default and
the cheapest thing to commit. `--harness pi` uses pi's own `.pi/skills/` and `~/.pi/agent/skills/`;
`--harness claude` uses `.claude/skills/` and `~/.claude/skills/`. `--harness codex` is accepted as a
spelling of `agents`, because Codex reads those same directories.

A harness can also be told where to look. pi documents a `skills` array in its settings, and its own example
points it at another harness's project directory — in `.pi/settings.json`:

```json
{ "skills": ["../.claude/skills"] }
```

## Without skill discovery

A harness that reads `AGENTS.md` or `CLAUDE.md` and nothing else still needs the contract. Print the
pointer stanza and append it:

```bash
git pair skill agents-md >> AGENTS.md
```

It names the installed path and the three rules that are easiest to get wrong, which is enough for an
agent to read the skill before it acts. Commit it next to the skill directory.

## Two things to know

- **The description decides when the skill loads.** Every harness reads only `name` and `description`
  until it activates the skill, so if an agent in your repository keeps failing to use git-pair, the
  fix is a more specific description, not a longer body. Keep the body short and the references focused
  for the same reason: the whole file enters the context once the skill activates.
- **A stale copy reads as a promise.** This file described `review close` and a `refs/reviews/*`
  namespace that no version of git-pair wrote, in a checkout that drifted from the tool it documented.
  Keep the skill in the same commit as the command changes it describes, run `git pair skill list` when
  you upgrade, and treat `stale` as a failing check.
