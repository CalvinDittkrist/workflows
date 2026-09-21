---
name: ci
description: Wait for CI checks and bot reviews on the open PR and report green, checks-failed, review-comments or waiting.
argument-hint: [pr]
allowed-tools: Bash(${CLAUDE_PLUGIN_ROOT}/scripts/checkpoint.sh*), Bash(${CLAUDE_PLUGIN_ROOT}/scripts/repair.sh*)
---
Context checkpoint:
!`${CLAUDE_PLUGIN_ROOT}/scripts/checkpoint.sh ci`

The checkpoint above measured this context as you entered the stage, whichever stage you came from. On `handoff: yes` wait for nothing here: follow the `next:` procedure it printed and hand the CI stage over to a fresh context. On `no` or `unavailable` continue.

Repair record:
!`${CLAUDE_PLUGIN_ROOT}/scripts/repair.sh print`

The repair record counts the repair rounds of this pull request and holds the limit across a handover, so the number is the script's and never yours. Count every repair round with `"${CLAUDE_PLUGIN_ROOT}/scripts/repair.sh" round` before you start it; when it refuses, repair nothing more and report the failing checks or the unresolved threads to the maintainer.

Run `"${CLAUDE_PLUGIN_ROOT}/scripts/pr-wait.sh" $ARGUMENTS` with the Bash tool timeout set to 600000 ms. The script returns within about nine minutes:

- `status: green` → done. Continue the pipeline.
- `status: review-comments` → count the round, then invoke `/worker:address-reviews`.
- `status: checks-failed` → count the round, then read the listed failed checks with `gh run view <id> --log-failed` (or `npx -y gh-axi run view <id> --log-failed`), fix the cause, commit, push, and run `/worker:ci` again.
- `status: waiting` (exit 3) → run the script again, not this skill; nothing is stuck. That is no repair round and no entrance into the stage, so it counts nothing and meets no checkpoint.

Never poll GitHub in a loop yourself; the script does the waiting. Never treat "no checks" as green when the repository has CI configured.
