---
name: ci
description: Wait for CI checks and bot reviews on the open PR and report green, checks-failed, review-comments or waiting.
argument-hint: [pr]
---
Run `"${CLAUDE_PLUGIN_ROOT}/scripts/pr-wait.sh" $ARGUMENTS` with the Bash tool timeout set to 600000 ms. The script returns within about nine minutes:

- `status: green` → done. Continue the pipeline.
- `status: review-comments` → invoke `/worker:address-reviews`.
- `status: checks-failed` → read the listed failed checks with `gh run view <id> --log-failed` (or `npx -y gh-axi run view <id> --log-failed`), fix the cause, commit, push, and run `/worker:ci` again.
- `status: waiting` (exit 3) → run the script again; nothing is stuck.

Never poll GitHub in a loop yourself; the script does the waiting. Never treat "no checks" as green when the repository has CI configured.
