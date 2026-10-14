# Capability manifests

One directory per capability. A manifest owns everything generic about it — how it installs, how to tell
whether the installed copy is current, and which word _that tool_ uses for each scope. The repository marker
says only which capabilities it needs and where; nothing outside this directory names a tool's flags.

## Format

```toml
name = "git-pair"
source = "tool"          # tool: the skill ships inside the tool. library: this library owns the files.
default_scope = "repo"

[install]                # commands to run per scope. {harness} is substituted from marker options.
repo = "git pair skill install --harness {harness} --scope repo"
machine = "git pair skill install --harness {harness} --scope user"

[drift]
kind = "command"         # command: prints current | stale | absent. diff: compares directories.
command = "git pair skill list --json | jq -r '…select(.scope==\"{scope}\")…|.state'"
scope_repo = "repo"      # this tool's word for the repository scope
scope_machine = "user"   # this tool's word for the machine scope
```

For `kind = "diff"` the manifest names the two trees instead:

```toml
[drift]
kind = "diff"
target_repo = ".agents/skills/{name}"
target_machine = "$HOME/.agents/skills/{name}"
source_path = ".dev-profiles/capabilities/{name}/SKILL.md"
```

`source_path` is the convention, not a requirement. A library-sourced capability whose content is prose several
skills point at keeps it in `.dev-profiles/references/` and points the rule there, so there is one document to
review and one byte-for-byte check — `change-description` does that.

## Rules

- `install` runs the drift check before writing and never writes where the marker did not ask. A current copy
  at a scope the marker does not require is left alone; a `stale` copy at a required scope is reinstalled.
- Marker config reaches the tool through `{placeholders}` only. Adding a capability means adding a manifest,
  never editing `bin/dev-profiles`.
- `required = true` at a scope where the capability is absent is a `doctor` failure. Absence at a scope the
  marker never asked for is `n/a`, which is not a finding.
- A capability skill describes driving one tool. It never states what the repository's workflow is.
- The exception is a capability the library writes rather than a tool: `change-description` is prose about the
  record a reviewer reads, which belongs to no tool and to both routes. What the shape guarantees stays the same —
  the library owns the source, `install` copies it, and `diff` says whether the copy is current — and the rule
  against stating repository workflow still holds: it says _what_ a change record must answer, never _how_ this
  repository lands a change.
