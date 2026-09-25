# 0037. The quota check waits below 12 % of the worker's scope

Date: 2026-09-22
Status: accepted, amended
Amended by: [0044](0044-the-quota-check-reads-the-scope-of-every-model-a-run-spends.md)
Amends: [0028](0028-the-quota-check-is-a-courtesy-not-a-guard.md)

## Context
- The default minimum of 30 was a guess.
- quota-axi reports one row per scope: `all_models`, and `model:<family>` for a separately limited model.
- A run's stream does not say whether it ended on the quota.

## Decision
- The default minimum is 12 %, set with `quota_minimum`; `quota_axi` is the tool's absolute path. A name or `npx` line is refused at startup.
- The factory runs `quota-axi --provider claude --json --no-credential-refresh`.
- It reads schema version 5: the `all_models` row and the worker's model row, set by its agent or `--model` in `worker_args`.
- It waits for the latest reset of every scope below the minimum. Output it cannot read fails open.
- A session's error triggers one more check. Below 1 %, the outcome is `quota`, and the issue resumes after the reset.
- It neither spends nor restores the one automatic interruption resume.
- When that check cannot answer, the run fails with a warning.
- A quota resume happens once in a row; a second stop waits for a person ([ADR 0026](0026-the-factory-never-deletes-work-on-its-own.md)).

## Consequences
- A drift test binds the factory's default model to the worker agent's.
- quota-axi is pinned to 0.1.49: 0.1.50 reads usage as remaining ([kunchenguid/quota-axi#209](https://github.com/kunchenguid/quota-axi/issues/209)).
- Rejected: parsing the error text, whose wording Claude Code leaves undocumented.

## Amendment of 2026-09-25
- The check drops `--no-credential-refresh`, because it never runs beside a session.
- quota-axi renews an expired credential; a failed renewal fails open. The factory writes no credential.
