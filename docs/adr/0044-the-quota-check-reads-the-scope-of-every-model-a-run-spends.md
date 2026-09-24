# 0044. The quota check reads the scope of every model a run spends

Date: 2026-09-24
Status: accepted
Amends: [0037](0037-the-quota-check-waits-below-12-percent-of-the-workers-scope.md) (which scopes the check reads)

## Context
[ADR 0037](0037-the-quota-check-waits-below-12-percent-of-the-workers-scope.md) has the check read the `all_models` scope and the scope of the worker's model, because a run was one worker session. Since the factory runs the review stage itself ([ADR 0043](0043-the-migration-runs-from-the-last-stage-to-the-first.md)), a run also starts the reviewers of its panel, and the code, docs and tests reviewers name `sonnet` in their definitions while the worker runs on `opus`. A run whose sonnet scope was used up started anyway, and a reviewer that then failed on the limit ended the run as `failed` instead of `quota`, so it was neither waited for nor resumed after the reset.

## Decision
The check reads the `all_models` scope and the `model:<family>` scope of every model a run of the issue's repository spends: the worker's, and each model a reviewer of that repository's panel names for itself. A reviewer that inherits the worker's model adds none. The check before a run and the check after a session's error read the same set. Everything else in 0037 stands: the minimum, the latest reset of the scopes below it, the `quota` outcome and the checks that fail open.

## Consequences
A run starts only when every model it will run on has quota left, and a reviewer that meets its model's limit ends the run as `quota`, which the factory resumes after the reset. A repository whose panel runs only the reviewers that inherit the worker's model is checked as before. The set follows the reviewer definitions in the factory, so a reviewer moved to another model moves the check with it.
