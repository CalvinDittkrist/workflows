---
name: board
description: Show all claimed worktrees with agent state, PR number, checks and review status.
disable-model-invocation: true
---
Run `"${CLAUDE_PLUGIN_ROOT}/scripts/board.sh"` and show the table as printed, one line per worktree. Add nothing except a one-line hint when a row needs the user (checks failed, review requested, agent blocked).
