# An approved child was advised to rewrite the history its approval names

## The observation

`feat-hooks-runner` (M1 of the lifecycle-hooks stack) was APPROVED at `d8689c1`. Its parent, the
lifecycle-hooks plan changeset `feat-pre-post-hooks`, had just landed in `main` as `3308070`, and the
parent's branch was still present. `git pair check --json` answered:

```text
ready: True
integrating: False
reasons: []
next_action: `git pair check`, then merge into refs/remotes/origin/main with ordinary git;
             parent feat-pre-post-hooks landed as 3308070 — rebase onto it:
             `git rebase --onto 3308070 feat/pre-post-hooks feat/hooks-runner`
```

The advice was followed. The tree came out byte-identical — `git rev-parse HEAD^{tree}` was `c7f4c52b…`
before the rebase and `c7f4c52b…` after — and the gate then refused:

```text
ready: False
reasons: ["review af7ffc8 reviewed 7c3e3b4, which is no longer in this history: the branch was
          rewritten since the review, so the approval does not license integration"]
```

The changeset had to be reset back to `d8689c1` to restore the approval. The work of re-offering it was
avoided only because nothing had been pushed in between.

## Why the two rules disagree

Both behaviours are correct on their own.

An approval is a claim about content, and `Review-Head` is what lets the gate refuse a rewritten history
(PRD §21). A rebase rewrites, so the approval stops licensing the merge — even when the resulting tree is
identical, because what the reviewer certified was a commit, and a tool that quietly accepted a different
commit would be accepting a state nobody read.

And a child still carrying a landed parent's commits is genuinely carrying something finished: the parent
branch it sits on is now stale, and `git rebase --onto <landing> <parent-branch> <child>` is the step that
takes the child onto the destination.

The disagreement is about **timing and cost**, and neither rule mentions the other:

- The advice fires whenever the head is not on the landing commit — `owesLandingStep` is
  `st.Landed != "" && !(st.LandingUnderHead && st.Tip == "")`, a predicate about where commits are.
- The refusal fires whenever the head is not the reviewed commit.

So the tool can print, on a passing verdict, a command whose only effect on that verdict is to make it
fail. Nothing in between asks whether following the advice costs anything.

## Where the two halves live

| Piece | Where | What it does |
| --- | --- | --- |
| The advice | `internal/cli/stacked.go` `parentLanded` → `landedParentStep` | When the parent's branch is still present and the head is not on the landing, the note carries the rebase command. No content question is asked. |
| The comparison | same file, `parentGone` → `landedApprovalStillStands` → `landedWorkUnchanged` | When the parent's branch is **gone**, the child's contribution above the landing is measured and compared with the identity the approval recorded; equal and clean-merging means a note and **no step**, different means a reason and the rebase-and-re-review step. |
| The print | `internal/cli/check.go:210-216` and `:280-285` | On a ready verdict, appends `landedParentStep` to `next_action` whenever `owesLandingStep()`; and on the printed verdict, the `parent: … landed as …` line prints the step, which also displaces the `parent.Note` that the comparison wrote. |

The asymmetry is the finding: the case where the parent's branch has been deleted asks "does the content
still measure the same" before advising a rewrite, and the case where the branch is still here — the more
common one, and the one that was implemented first, in the 2026-09-24 parent-landing work — advises without
asking.

Two consequences follow from the print site rather than the read:

- `check`'s `next_action` can contain a self-invalidating instruction while reporting `ready: true`.
- When the parent has landed, the comparison sentence the read carefully composed (`"measured above the
  landing, this branch still contributes the content review <sha> approved"`) never reaches the human
  output, because the `parent: … landed as …` branch prints the step instead of the note.

## What the correct answers were

For this case: merge `d8689c1` into `main`. `git merge-base HEAD origin/main` is `7f55a82` (the landing the
approval measured from), the contribution above it is the approved content, and the merge is clean. The
rebase was housekeeping that could equally have run after the merge, on a branch nobody had to certify.

For the squash-landing case the comparison was written for: the content the reviewer saw arrives as commits
the child never had, the measured contribution differs, and the answer must be "rebase and have the result
reviewed again". That path already works on the vanished-parent side and must keep working.

## Open measurement

Whether carrying the parent's commits affects anything besides the parent branch's staleness — the merge
shape of the eventual landing, `change tidy`'s staleness finding, the child's review span, and the queue's
wait measurement — is not established by this note. See the plan's spikes.
