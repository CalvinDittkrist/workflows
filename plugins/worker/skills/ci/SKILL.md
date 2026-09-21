---
name: ci
description: Wait for CI checks and bot reviews on the open PR and report green, checks-failed, review-comments or waiting.
argument-hint: [pr]
allowed-tools: Bash(${CLAUDE_PLUGIN_ROOT}/scripts/checkpoint.sh*)
---
Context checkpoint:
!`${CLAUDE_PLUGIN_ROOT}/scripts/checkpoint.sh ci`

The checkpoint above measured this context as you entered the stage, whichever stage you came from. On `handoff: yes` wait for nothing here: follow the `next:` procedure it printed and hand the CI stage over to a fresh context. On `no` or `unavailable` continue.

Run `"${CLAUDE_PLUGIN_ROOT}/scripts/pr-wait.sh" $ARGUMENTS` with the Bash tool timeout set to 600000 ms. The script returns within about nine minutes:

- `status: green` → done. Continue the pipeline.
- `status: review-comments` → invoke `/worker:address-reviews`.
- `status: checks-failed` → read the listed failed checks with `gh run view <id> --log-failed` (or `npx -y gh-axi run view <id> --log-failed`), fix the cause, commit, push, and run `/worker:ci` again.
- `status: waiting` (exit 3) → run the script again; nothing is stuck.

Never poll GitHub in a loop yourself; the script does the waiting. Never treat "no checks" as green when the repository has CI configured.
