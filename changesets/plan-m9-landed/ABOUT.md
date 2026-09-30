# The plan's last status line

## Summary

One line in `docs/plans/base-changeset-collapse/plan.md`. M9's status said "offered as
`feat/prd-says-the-rule-in-order`", which was true when M9 was written and stopped being true at the merge. It reads
"landed as `0a4e1ed`" now, and the sentence says the plan is closed, so a person who finds this document later does
not have to work out from git history whether the work it describes ever shipped.

## Why a changeset for one line

Every other milestone's landing was folded into the commit that followed it, which is why none of them needed a
changeset of its own. Nothing follows M9, so the alternative to this is a plan whose last entry predates its own
merge.

## Validation

`mise run gates`: 22 packages, `E2E`, `PTY`, `CI-INTEGRATE: all checks passed`. No code, test, or document other than
the plan is touched.
