# 0016. Approval is per category, and scripts own what they apply

Date: 2026-09-20
Status: accepted; that a scaffolded category without findings cannot be answered is superseded by [ADR 0035](0035-every-category-the-apply-phase-scaffolds-is-answerable.md)

## Context
- The findings report listed a category under `the run performs` (#15, after #3).
- A reader took each line for an approved item and nothing more.
- `delete`, `issue`, `replace` and `create` findings are worked one by one; `cleanup.sh` turns the last two into `todo:` lines.
- `workspace.sh --apply` applies the whole difference it computes, and `scaffold.sh` creates every missing baseline file of a category.

## Decision
Approval stays one answer per category, `approve` or `reject`, and the report states what the scripts apply for it.

## Consequences
- The report groups a category's findings by action; approving a category applies the workspace difference recomputed after the cleanup pull request merges.
- For `agent-config`, `docs`, `tests-ci` and `workspace` it says every missing baseline file is created; `agent-config` also rewrites `.claude/settings.json`.
- A scaffolded category without findings cannot be answered, and the report names it.
- `finalize.sh` prints one line on settings changed without a finding and audited ones already at the standard. It never skips or aborts.
- `<git dir>/standardize/workspace-handled` keeps what a run handled; `report.sh` clears it.
- `WF_SCAFFOLD_CATEGORIES` in `lib.sh` lists the scaffolded categories, and a test catches drift from `scaffold.sh`.
- Objecting to one setting means rejecting the category.
- Rejected: approval per `configure` finding, and a `finalize.sh` that stops on a deviation from the audit.
