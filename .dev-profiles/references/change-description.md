---
name: change-description
description: Use when writing the record a reviewer reads before opening a change — a git-pair changeset's ABOUT.md or a GitHub pull request body. The reviewer's seven questions in order, what to write against, and what to leave out.
---

# Writing the change record

One standard, two vehicles: a git-pair changeset's `changesets/<id>/ABOUT.md`, and a GitHub pull request body. The
vehicle differs; the craft does not. Write the description to orient a reviewer to the change, not to summarize the
diff.

## What the description has to let a reviewer answer

In roughly this order:

1. Why does this change exist?
2. What becomes different because of it?
3. What does that look like in a concrete example?
4. What is intentionally not changing?
5. How was it implemented, and why this way?
6. Where should I pay particular attention?
7. How do we know it works?

Every section below exists to answer one of those. The order is the reading order, so write in it: a reviewer who
stops after question 3 should already know what they are looking at.

## Keep simple changes simple

Omit sections that add no useful information, rather than mechanically filling out a template. A typo fix, a
dependency bump, or a test-only change is a few sentences, not seven headings. Padding a record to satisfy a
checklist costs the reviewer more than the missing sections would have — and in git-pair, `change ready` refuses a
changeset with no `ABOUT.md`, which is pressure to pad. Three accurate sentences pass the handoff.

## Start from the outside (questions 1–2)

Describe the change first in terms of observable behaviour and external concepts.

The "user" may be an end customer, developer, API consumer, CLI user, another component, or another system. Explain
what changes from that perspective before discussing implementation details.

Prefer:

> A caller can now resolve a branch or arbitrary Git ref as either endpoint of a review span.

over:

> Adds `resolveRef()` and extends `SpanTarget` with a new variant.

The implementation belongs later.

## Position the change in its larger context (question 1)

If this change is one step in a larger feature, migration, or architectural plan, briefly explain that plan first
and identify where this change fits.

Keep this high-level. Give the reviewer enough context to understand why this particular slice exists without
requiring them to read the entire plan.

## Show the change concretely (question 3)

Default toward including at least one representative example of the behaviour.

Choose the clearest form for the change:

- a short user workflow
- example CLI commands and output
- an API request/response
- before/after behaviour
- a small state transition or sequence diagram
- screenshots for visual changes

A reviewer should ideally be able to understand the essential behaviour from the example before reading the
implementation.

For workflows, show the important path through the system rather than merely listing components.

## Explain the implementation second (question 5)

After establishing the external behaviour, explain the important internal design.

Focus on information that cannot be easily learned by reading the diff:

- important architectural decisions
- why this approach was chosen
- meaningful alternatives considered
- constraints that shaped the implementation
- non-obvious tradeoffs
- intentionally deferred work

Do not narrate the diff file-by-file or function-by-function unless that structure itself is important.

## Define the boundaries (question 4)

Call out important things that are deliberately outside the scope of this change.

If the change establishes infrastructure for later work, distinguish clearly between what works now and what it
enables later.

Mention compatibility implications, migrations, feature flags, or rollout constraints when relevant.

## Guide the review (question 6)

Call out areas where reviewer attention would be particularly valuable. Examples include:

- an API or abstraction whose shape is difficult to change later
- concurrency or consistency assumptions
- subtle edge cases
- a tradeoff where another design may be preferable
- code that is intentionally temporary
- areas where the author has lower confidence

Do not manufacture review concerns merely to fill this section.

## Explain verification (question 7)

State how the behaviour was validated, emphasizing meaningful scenarios rather than simply saying that tests pass.

Mention important edge cases or failure modes covered by tests. Include manual verification steps when they provide
useful additional confidence.

Name the command that produced the claim, so the reviewer can run it: an assertion that a suite passed is worth the
line that re-runs it.
