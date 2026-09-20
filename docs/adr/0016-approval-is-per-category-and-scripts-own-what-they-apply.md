# 0016. Approval is per category, and scripts own what they apply

Date: 2026-09-20
Status: accepted

## Context
The report printed every finding of a category under one heading, `the run performs`, so a reader concluded that each line was an item they had approved and that nothing else would happen (#15, a follow-up of #3). That holds for `delete` and `issue` findings only. A single `configure` line had no effect of its own, and the scaffolding step created baseline files the report never listed. The audit and the apply phase are also separated in time by a pull request and its merge, so the workspace difference the maintainer read can no longer be the one that is applied.

## Decision
Approval stays per category: one answer, `approve` or `reject`, for all findings of a category, and a rejected category is left untouched. Within an approved category the run performs `delete` and `issue` findings one by one, exactly as listed, while `configure`, `create` and `replace` findings describe what a script works out for itself: `workspace.sh --apply` applies the whole difference between the GitHub workspace and the standard, and `scaffold.sh` creates every missing baseline file of the category. The report groups the findings by that split and names, per category, what approving it triggers for the describing group. Because `workspace.sh --apply` recomputes its difference after the cleanup pull request is merged, `finalize.sh` compares what it applied with the `configure` findings the report recorded and prints one line naming the settings it changed without a finding and the recorded ones it no longer needed. That line informs; it never skips a change and never aborts the run.

## Consequences
The report says what the unit of decision is, so approving a category is an informed decision, and the extra lines appear only for categories that carry a describing finding. The apply phase is unchanged, which keeps the scripts idempotent and keeps one source of truth for the workspace: the difference the script computes when it runs, never a list recorded hours earlier. A maintainer who objects to a single setting has to reject the whole category and change that setting by hand.

Rejected: per-finding approval of `configure` lines, with an exclusion parameter in `workspace.sh`. A setting left out of the standard that way is reported as drift by `check.sh` on every later run, which turns a one-time decision into a permanent warning, and it would make the applied state depend on a stored list instead of the standard. Also rejected: letting `finalize.sh` refuse or stop when the applied difference deviates from the audit. The deviation is normal — the merge of the cleanup pull request itself changes the workspace — and a run that has already deleted files and opened issues must finish.
