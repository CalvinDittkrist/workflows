# 0037. The quota check waits below 12 % of the worker's scope

Date: 2026-09-22
Status: accepted
Amends: [0028](0028-the-quota-check-is-a-courtesy-not-a-guard.md) (the default minimum, which scope is the worker's, and when a run ran out of quota)

## Context
[ADR 0028](0028-the-quota-check-is-a-courtesy-not-a-guard.md) decided the check: before every run, the smaller remaining percentage of the all-models scope and the worker's model scope, a configured minimum, a wait until the reported reset, and a check that fails open. Building it left four questions that ADR did not answer.

Its default of 30 was a guess, and it said so. Sharing one Max subscription with the maintainer, a run of the factory costs a few percent of the five-hour window, and a factory that stops at 30 % gives up almost a third of every window. The maintainer's own work needs a margin, not a share.

quota-axi 0.1.50 reports in schema version 5, with one row per scope under `quotaSemantics.effectiveAvailability`: `all_models`, and `model:<family>` for each model the provider limits apart, such as `model:fable`. A model the provider does not limit apart has no row. The worker's model is the worker agent's `model:` unless the host passes another with `--model` in `worker_args`, and it can be spelled as an alias (`opus`) or as a full name (`claude-opus-4-1`).

quota-axi renews an expired session's credential by calling the vendor CLI unless `--no-credential-refresh` is given. The factory's checks run beside a worker's Claude Code, which renews the same credential.

A run that meets the limit ends in an error like any other. The stream does not say whether the cause was the quota, and parsing its text for that would tie the factory to wording Claude Code does not document.

## Decision
The default minimum is 12 %, set with `quota_minimum` (0 to 100) beside `quota_axi`, the absolute path of the installed tool. A name or an `npx` line is refused when the factory starts.

The factory runs `quota-axi --provider claude --json --no-credential-refresh` and reads schema version 5 only. It takes the `all_models` row and the `model:<family>` row whose family is a word of the worker's model name, so `opus`, `claude-opus-4-1` and `opus[1m]` all read `model:opus`. Without such a row the all-models scope stands alone. It waits until the latest reset of the limiting windows of every scope below the minimum, because the next run needs all of them back. A scope below the minimum that names no reset counts as output the factory cannot read, so the check fails open. The same applies to another schema version, an unknown row, an error exit, a missing binary and a check that takes longer than 30 seconds.

A session that ends in an error is followed by one more check. When a scope then has less than 1 % left, the outcome is `quota` instead of `failed`. The factory waits for that reset, and after it the issue is resumed in its worktree on the signal `quota`, which neither spends nor restores the one automatic resume after an interruption. When this check cannot answer, the run has failed and carries a warning.

## Consequences
The factory stops for the maintainer's sake only when the window is nearly gone, which leaves most of each window to the line. Whether 12 % is enough margin is measured on the host, as 0028 said of 30.

Reading the scope from the model name keeps `worker_args` the one place a host changes the model. A drift test holds the factory's default to the worker agent's frontmatter, so moving the worker to another model moves the check with it.

The factory never writes a credential. A check that meets an expired one fails open, and the next worker session renews it.

Telling a quota stop from a failure by the quota after the error means a session that failed for another reason while the quota happened to be used up is resumed after the reset instead of waiting for a person. That costs one more run, which ends `failed` if the error was the issue's. The other way round, a real quota stop read as `failed`, would leave a held issue waiting for a person who has nothing to decide.
