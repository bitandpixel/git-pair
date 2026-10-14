---
name: git-pair-github-integration
description: Use when a changeset is approved and ready to land in a repository whose merge is performed by CI — what the author declares, what the job verifies, and what nobody else may do. git-pair-github-integration, not git-pair-manual-integration.
---

# Landing a declared change

The author asks. A GitHub Actions run verifies the change and merges it. Read this with the loop skill.

## The author's half

    git pair change integrate    # one commit: Review-State: integrating, naming the head it covers
    git push

That commit is the whole ask. It is not a merge, not a queue and not a verdict — the approval underneath it is what
permits the merge, so `git pair check` keeps passing after you run this and keeps refusing after anything that would
have made it refuse before. A rebase rewrites the head the marker names, and the stale name is what makes the gate
refuse the rewrite rather than bless it: re-declare after a rewrite.

Never run `git pair review submit|open|reopen|thread|about`, never `change tidy`, never merge into the destination,
and never ask for the merge in prose you already asked for with the command.

## What the job is allowed to merge

The job reads git-pair's gate whole, both answers together:

    git pair check --json | jq -e '.ready and .integrating'

`ready` answers "may this be merged". `integrating` answers "did the author ask". Merging on the first alone takes the
decision out of the author's hands.

## What it verifies, and of which tree

The merge commit is created locally, the consumer's `[commands] verify` runs against that merged tree, and the push happens
only if the suite is green. This is deliberate: green builds on a branch head say nothing about the merge commit the
destination is about to acquire.

A failing verification is always a refusal. It puts the local destination back, merges nothing, and exits 1 — it is
not a skip, not a retry, and not a signal to push anyway. If the suite is failing, the work is not ready and the
author fixes it on the branch, then re-declares.

The scripts under `scripts/ci/` are this profile's bytes, installed and byte-compared. Edit them in the library, not
in a checkout.

What runs as the verification is not this profile's choice, and no filename here is a requirement. The profile demands
one role — `verify` — and the consumer answers it in the marker's `[commands]` table with any command that runs its own
checks; `dev-profiles command verify` prints what was chosen. A checkout may keep its answer in
`scripts/ci/verify-candidate.sh`, or in a Makefile target, or in a one-liner calling the project's test command. The
demand is a role and the marker holds the answer, which is what stops the profile shipping a file it would then have to
overwrite.
