---
name: project-planning
description: Use when beginning multi-step work or when significant design decisions are required. Only when a related plan does not already exist. Produces an execution plan organized into verifiable milestones with supporting research and implementation tasks.
---

# Project Planning

## Goal

Create an implementation plan that reduces uncertainty, communicates intent, and guides execution through small, stable, verifiable milestones.

A good plan should allow another engineer or agent to understand:

- what is being built,
- why it is being built,
- how implementation will proceed,
- how success will be measured.

## When to Create a Plan

Create a plan whenever work:

- spans multiple implementation sessions,
- requires architectural or product decisions,
- affects multiple systems,
- introduces meaningful uncertainty,
- or cannot reasonably be completed in a single focused implementation session.

Small, straightforward changes generally do not require a dedicated plan.

## Plan Directory

Each project should have its own directory.

Example:

- docs/plans/active/
    - feature-name/
        - plan.md
        - audits/
        - research/
        - artifacts/

The plan is the canonical project document.

Supporting material should live alongside it.

Completed project dirs are archived to `docs/plans/completed`.

## Plan Structure

Each plan should contain the following sections.

### Goal

Describe the overall objective.

### Success Criteria

Describe what must be true for the project to be considered complete.

These should describe observable outcomes rather than implementation details.

### Context

Summarize relevant background information.

### Constraints

Capture technical, product, or operational constraints that influence the solution.

### Assumptions

Document assumptions that could change during implementation.

### Milestones

Break implementation into incremental milestones.

Each milestone should produce a stable, testable, potentially shippable state of the system.

Favor vertical slices over horizontal implementation phases.

Milestones express dependency order; do not force independent tasks into a serial sequence. Within a
milestone, identify workstreams that can start from the same known state and proceed concurrently. Prefer
separate files or modules with clear owners and integration points. Do not split a tightly coupled task or
have two agents edit the same files just to claim parallelism. If there is no safe parallel work, say why.

Each milestone contains:

#### Deliverables

Describe the capabilities or artifacts that should exist once the milestone is complete.

Deliverables describe **what exists**, not how it was built.

Examples:

- Authentication works.
- REST API exists.
- Documentation updated.
- New deployment pipeline operational.

#### Tasks

Tasks describe the implementation work required to produce the deliverables.

Tasks should be:

- concrete,
- actionable,
- scoped to the milestone,
- easy to verify.

#### Dependencies and Parallel Work

Name prerequisite milestones or tasks, if any, and state what must be true before each workstream starts.
For independent tasks, name the expected output, file or subsystem ownership, and handoff to the integrator.
Mark shared files, design decisions, or stateful resources that require one writer or a sequencing decision.
Group work by dependencies, not by agent count; assign agents only when execution begins and capacity is known.

#### Verification

Describe how the milestone will be validated, including integration checks after parallel work is combined.

Verification should demonstrate that the deliverables actually function correctly.

### Spikes / Research

Separate research from implementation.

Examples include:

- evaluating libraries,
- comparing architectures,
- measuring performance,
- prototyping approaches.

A spike should answer a question or reduce uncertainty. If other tasks need its answer, make the dependency
explicit; independent research can proceed in parallel.

Capture conclusions before dependent implementation begins.

### Risks

Document known risks and mitigation strategies.

### Verification Strategy

Describe the overall testing and validation approach for the project.

### Audit History

A list of links to audit artifacts as planning audits are performed.

Example:

#### Audit History

| Date       | Audit                   | Summary                             |
| ---------- | ----------------------- | ----------------------------------- |
| 2026-07-09 | audits/2026-07-09-m1.md | Milestone 1 verified. Plan updated. |

## Planning Philosophy

Plans should optimize for:

- incremental delivery,
- low implementation risk,
- frequent verification,
- easy handoff,
- safe concurrent work where dependencies allow it,
- adaptability as new information is discovered.

Avoid planning implementation in one large milestone.

Prefer many small milestones that each leave the project in a working state.
