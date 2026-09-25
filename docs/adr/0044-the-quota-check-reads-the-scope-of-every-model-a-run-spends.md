# 0044. The quota check reads the scope of every model a run spends

Date: 2026-09-24
Status: accepted
Amends: [0037](0037-the-quota-check-waits-below-12-percent-of-the-workers-scope.md): which scopes the check reads

## Context
- [ADR 0037](0037-the-quota-check-waits-below-12-percent-of-the-workers-scope.md) reads the `all_models` scope and the worker model's scope, because a run was one worker session.
- Since the factory runs the review stage ([ADR 0043](0043-the-migration-runs-from-the-last-stage-to-the-first.md)), a run also starts reviewers, some on `sonnet` while the worker runs on `opus`.
- A reviewer that met its limit ended the run with the outcome `failed` instead of `quota`, so it was not resumed after the reset.

## Decision
The check reads the `all_models` scope and the `model:<family>` scope of every model a run of the issue's repository spends. That is the worker's model and each model a reviewer of that repository's panel names. A reviewer that inherits the worker's model adds none. The checks before a run and after a session's error read the same set. The rest of 0037 stands.

## Consequences
- A run starts only when every model it runs on has quota left.
- A reviewer at its limit ends the run as `quota`, which the factory resumes after the reset.
- The set follows the reviewer definitions, so a reviewer moved to another model moves the check.
- Rejected: reading only the worker's scope, which starts runs a reviewer cannot finish.
