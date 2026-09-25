# 0037. The quota check waits below 12 % of the worker's scope

Date: 2026-09-22
Status: accepted
Amends: [0028](0028-the-quota-check-is-a-courtesy-not-a-guard.md)

## Context
- [ADR 0028](0028-the-quota-check-is-a-courtesy-not-a-guard.md) set a default minimum of 30, a guess that gives up a third of every shared window.
- quota-axi reports one row per scope: `all_models`, and `model:<family>` for a model the provider limits apart.
- The stream of a run does not say whether it ended on the quota.

## Decision
- The default minimum is 12 %, set with `quota_minimum`; `quota_axi` is the absolute path of the tool.
- The factory runs `quota-axi --provider claude --json --no-credential-refresh`.
- It reads schema version 5: the `all_models` row and the row of the worker's model, set by its agent or `--model` in `worker_args`.
- It waits until the latest reset of every scope below the minimum. Output it cannot read fails open.
- After a session's error, one more check runs. Below 1 %, the outcome is `quota`, and the factory resumes the issue after the reset.
- A quota resume happens once in a row; a second stop waits for a person ([ADR 0026](0026-the-factory-never-deletes-work-on-its-own.md)).

## Consequences
- A drift test binds the factory's default model to the worker agent's.
- quota-axi is pinned to 0.1.49: 0.1.50 reads usage as remaining ([kunchenguid/quota-axi#209](https://github.com/kunchenguid/quota-axi/issues/209)).
- Rejected: parsing the error text for the quota, whose wording Claude Code does not document.

## Amendment of 2026-09-25
- The factory runs the check without `--no-credential-refresh`, because it never checks beside a session.
- quota-axi renews an expired credential through `claude doctor`; a failed renewal fails open. The factory writes no credential.
