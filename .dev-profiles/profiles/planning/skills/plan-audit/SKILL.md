---
name: plan-audit
description: Use as a supervisor to review an implementation plan during or after execution, only on demand. Audits progress against the plan, updates the plan to reflect reality, produces an immutable audit record, and recommends next steps without performing implementation.
---

# Plan Audit

## Goal

Evaluate the current state of a project against its implementation plan.

The audit should determine:

- what has been completed,
- what remains,
- whether implementation still aligns with the plan,
- whether milestones are actually complete,
- whether verification has been performed on the integrated result,
- whether dependencies, parallel ownership boundaries, and handoffs still match the work,
- identify any undocumented issues,
- and what should happen next.

An audit should objectively assess the project rather than continue implementation.

---

# Philosophy

The implementation plan is the canonical engineering artifact.

Audit documents are historical evidence.

An audit has two mandatory outputs:

1. Update the implementation plan so it accurately reflects the current state of the project.
2. Produce an immutable audit artifact describing the findings.

Future engineers should primarily read the updated plan.

Audit artifacts provide historical detail and rationale.

---

# Audit Process

## 1. Locate the Active Plan

Locate the project's active plan.

Expected location:

```
docs/plans/active/<plan-slug>/plan.md
```

and review each section.

---

## 2. Review the Project

Compare the implementation against the plan.

Review as appropriate:

- source code,
- tests,
- documentation,
- configuration,
- build artifacts,
- deployment configuration,
- completed milestones.

Do not assume the plan is accurate.

Verify it against reality.

---

## 3. Evaluate Every Milestone

For every milestone determine:

### Deliverables

- Do all expected deliverables exist?
- Are they complete?
- Do they behave as intended?

### Tasks

- Are implementation tasks complete?
- Were additional tasks discovered?
- Were dependent tasks started only after their prerequisites, and were parallel outputs integrated?
- Are obsolete tasks still listed?

### Verification

- Has verification been completed?
- Is there evidence supporting verification?
- Are additional tests required?

### Stability

Determine whether the milestone leaves the system:

- buildable,
- testable,
- recoverable,
- handoff-ready,
- potentially shippable.

---

## 4. Identify Plan Drift

Identify differences between the implementation and the plan.

Examples include:

- undocumented architectural decisions,
- missing deliverables,
- completed work not reflected in the plan,
- obsolete tasks,
- missing verification,
- outdated assumptions,
- newly discovered work,
- outdated milestone sequencing or unsafe parallel ownership.

---

# Required Output 1 — Update the Plan

Update the implementation plan so it reflects reality.

Update as needed:

- milestone status,
- deliverables,
- task completion,
- verification status,
- assumptions,
- risks,
- discoveries,
- current milestone and workstream dependencies,
- project metadata.

Do not rewrite history.

Instead, record why important changes were made.

## Audit History

Append an entry to the plan's Audit History section.

Example:

| Date       | Audit                            | Summary                                                                |
| ---------- | -------------------------------- | ---------------------------------------------------------------------- |
| 2026-07-09 | audits/2026-07-09-milestone-2.md | Milestone 2 verified. Updated task completion and verification status. |

The updated plan should become the authoritative project status.

---

# Required Output 2 — Create an Audit Artifact

Create a new audit document under:

```
docs/plans/active/<plan-slug>/audits/
```

Name using:

```
YYYY-MM-DD-<short-summary>.md
```

Audit artifacts are immutable historical records.

Do not overwrite previous audits.

---

# Audit Artifact Structure

## Summary

Brief overview of the audit.

## Scorecard

### Overall Status

Choose one:

- 🟢 On Track
- 🟡 Minor Drift
- 🟠 Significant Drift
- 🔴 Blocked

### Project Health

| Category           | Status |
| ------------------ | ------ |
| Plan Accuracy      |        |
| Milestone Progress |        |
| Deliverables       |        |
| Verification       |        |
| Documentation      |        |
| Technical Debt     |        |
| Risks              |        |

### Milestone Status

| Milestone | Progress | Verified |
| --------- | -------- | -------- |

---

## Findings

Summarize significant observations.

Examples:

- Deliverables complete but verification missing.
- Plan missing recently completed work.
- Additional implementation required.
- Risks have changed.
- Architectural decisions were undocumented.

---

## Evidence

Describe evidence supporting the findings.

Examples:

- Code reviewed
- Tests executed
- Manual verification completed
- Documentation inspected

---

## Recommendations

Recommend the highest-value next actions.

Examples:

- Continue current milestone.
- Complete verification.
- Update implementation plan.
- Perform additional research.
- Split a milestone.
- Project is ready for completion.

---

## Confidence

Rate confidence in the audit.

Choose one:

- High
- Medium
- Low

Briefly explain why.

Examples:

- All code reviewed and verified.
- Unable to validate deployment.
- Manual testing incomplete.

---

# Completion

An audit is complete only when:

- the implementation has been reviewed,
- the implementation plan has been updated,
- the Audit History references the new audit,
- a new immutable audit artifact has been created,
- the project status accurately reflects reality,
- recommendations for next steps have been recorded.

The goal of every audit is to leave the project easier for the next engineer or agent to understand than it was before the audit began.
