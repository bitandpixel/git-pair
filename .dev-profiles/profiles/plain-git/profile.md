# Profile: plain-git

No review protocol. Branch, commit at milestones, push so the work survives the session, and ask before the
forge records anything.

## When this profile fits

- Scratch repositories, spikes, prototypes, dotfiles, and anything where a review step would cost more than it
  saves.
- The honest default for a repository that has not chosen a workflow.

## Why it is not a weaker citizen

The specific workflows carry guarantees this one does not promise: a human read before landing, a reviewable
record of what was approved, a gate whose exit code means something. A repository that needs those declares a
profile that provides them. `plain-git` does not fake the guarantees it cannot enforce — it says so, and names
the moment to stop and declare a workflow instead.

## Machine default

This is the profile installed at machine scope, so an unprofiled repository still gets a floor. Its description
carries the deferral: do not use it when the repository carries a `.dev-profiles.toml`.
