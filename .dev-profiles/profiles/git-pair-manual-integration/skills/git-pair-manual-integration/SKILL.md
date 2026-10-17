---
name: git-pair-manual-integration
description: Use when a changeset is approved and a person performs the merge — no CI job lands it. The gate is unchanged; the merge is somebody with the destination's rights. git-pair-manual-integration, not git-pair-github-integration.
---

# Landing a change by hand

Nothing here replaces the loop skill. This covers the last step, for a repository where no job is allowed to touch the
destination branch.

## The author's half is identical

    git pair change integrate    # declares the ask, naming the head it covers
    git push

Asking is still a command and still the author's. Whether a person or a job performs the merge does not change what
the author may do: never `git pair review submit|open|reopen|discussion|about`, never `change tidy`, never merge into the
destination yourself.

## The gate is the same one

The person about to merge runs git-pair's own gate and reads both answers:

    git pair check --json | jq -e '.ready and .integrating'

A merge that skips it loses the only record that the reviewer approved *this* head. If the branch was rebased after
the approval, the gate refuses — re-review, do not merge anyway.

## What the person does, and what must not be skipped

The merge is ordinary git, performed by someone with the destination's rights, and the pushed branch is the whole
record of the landing. What a repository gives up by having no job is the verification the candidate would have
received: with nothing running `[commands] verify` against the merge commit, the person merging is the only check
between a red suite and trunk. Run the suite on the candidate before pushing, or say in the change record why the
destination's own CI will catch it. If a repository finds itself needing that guarantee, declaring
`git-pair-github-integration` instead is the way to get it.
