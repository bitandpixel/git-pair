# Child declarations and the main-side trigger

Two questions from review `906332c`, and the round that moved them here. They were answered inline at the
time — one as a blockquote in `docs/plans/change-integrate-cmd/plan.md`, one as a comment in
`.github/workflows/git-pair-integrate.yml` — and review `17df992` asked for that text to live in a thread
instead. It is here now, and neither file keeps a copy.

## A child whose parent has not landed: refuse the declaration, or record early?

Inline on the plan's success criteria, next to the refusal it asks about:

> I'm wondering about this, ("the parent branch has not landed") - does it make sense to enforce this at
> integration-ready-time? should instead we allow the integration record be made, and then it isn't actually
> "integratable" until it's parent changeset(s) are integrated?

### Response

Refused at the declaration, not deferred. Three reasons, in the order they bite.

**The record is the wrong instrument for an intention.** It is create-only, it states that a merge already
happened, and `integration record` verifies that statement against the destination's tree (`--commit` must be
the commit that added `changesets/<id>/` to `--target`). A record written for an unmerged child is a false
claim, and there is no command that takes one back.

**A deferred declaration rots on the event that unblocks it.** The parent's landing changes what the child's
merge is against, while both halves of the gate speak about the head the author declared: `--expect-head`
refuses anything but that commit, and the probe is asked about that commit's checks. The green that licenses
the merge was earned against the parent's old tip, and nothing re-tests it, because the child got no new
commit — README's "Green before the merge" says the same thing plainly: the guarantee is about the head, not
about the merge.

**The destination is not knowable yet.** `DestinationFor` (`internal/changeset/destination.go`) walks the
parent's record to find where the parent actually went. Before the parent lands there is no record, so the
answer would be a guess, and guessing the destination of a create-only ref is the one thing this path refuses
to do.

What the refusal costs is small and it is the author's, not the reviewer's: the parent lands, the author runs
`change integrate` again, and the merge is then requested against a head CI has actually run. The human path
never closes — `check` still answers yes to such a child, and `recordCarried` still lands a changeset carried
into another branch — which is why the rule lives in `change integrate` and not in `integrationReasons`.

### Round two

> please move these both to a separate comment thread or the ABOUT file

Done — here rather than in `ABOUT.md`, because the question and its answer want to stay together and the plan
is already long. What stays in the plan is the Decisions row ("A child whose parent has not landed is refused
at the declaration, not deferred"), since a decision is the plan's own content and the row is where a future
reader looks for the rule rather than the discussion. Say if you would rather that row came here too.

## A child that becomes eligible because `main` moved

Inline on the merge workflow's `workflow_run.branches`:

> if we also allow stacked child changesets to be INTEGRATING before their parents have landed, we'll want to
> consider running these on `main` changing as well, in case the child feature branch is stable and passing
> (no new commits or workflows) but becomes eligible for integration because of a `main`-side change. Or maybe
> the backstop could be good enough?

### Response

The backstop is good enough, and the case is unreachable today.

The scheduled poll is not a fallback that reads a stale list: each pass re-reads `queue --json`'s
`awaiting_integration` and re-derives every destination through `DestinationFor`, so a changeset that became
eligible without a commit of its own is picked up within one interval — fifteen minutes here — with the same
`--require-ci` probe and the same refusals. A `workflow_run` on `main` would shorten that wait and change the
answer in no other way, and it would arrive carrying main's sha while `--expect-head` must name the declared
head, so the sha it offers is the wrong one to require.

Unreachable while the previous answer holds: a child cannot be declared before its parent lands, so no child
is sitting in that queue waiting for `main`. Recorded as an open question in `ABOUT.md` for the day the parent
rule changes, which is what the workflow comment was for.

### Round two

> Please remove this inline comment and you can add it to open questions in ABOUT or a unique comment thread

Both — the comment is out of the YAML, and the standing question is in `ABOUT.md` under Open questions.
