---
name: plan-execution
description: Use when implementing work from an existing execution plan. Keeps the plan synchronized with implementation progress and ensures milestones are completed in stable, verifiable increments.
---

# Plan Execution

## Goal

Treat the implementation plan as the source of truth throughout execution.

## Before Beginning

Read the current plan.

Understand:

- current milestone,
- outstanding tasks,
- dependencies and parallel work boundaries,
- workstream owners, outputs, and integration handoffs,
- open questions,
- verification requirements.

Also, quickly confirm that the current state of the project matches the state recorded in the plan.
Do not start dependent tasks before their prerequisites are resolved.

## During Execution

Treat the plan as a living document.

As work progresses:

- update task status,
- record discoveries,
- document architectural decisions,
- adjust future Milestones when assumptions change.

Run independent workstreams concurrently when ownership boundaries and verification are clear. Give each
agent a bounded output and a distinct writable area or isolated worktree. Keep one integrator responsible
for combining results and running the milestone checks. If tasks collide on a file or decision, sequence
them or revise the plan; do not silently allow concurrent writes. Revisit dependencies as discoveries
change the work, and record changed handoffs in the plan.

If new work is discovered:

- add Tasks when it belongs to the current milestone,
- create a future Milestone when appropriate,
- create a Spike if additional research is needed before implementation.

## Milestone Completion

Before marking a Milestone complete:

- all Deliverables exist and are verified,
- all Tasks are complete,
- integrated work has been checked together, not only in separate workstreams,
- verification succeeds,
- documentation has been updated,
- outstanding issues are recorded.

A completed Milestone should leave the project in a stable state that could reasonably be shipped or handed off.

## Handling Discoveries

If implementation reveals new work:

- add tasks to the current milestone when appropriate,
- otherwise create a future milestone,
- record architectural decisions,
- update risks if assumptions change.

Avoid silently diverging from the plan.

## Completion

When all Milestones are complete:

- ensure the plan reflects reality,
- summarize key implementation decisions,
- carry out final verification,
- move the plan to docs/plans/completed/.
