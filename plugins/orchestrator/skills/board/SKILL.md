---
name: board
description: Show all claimed worktrees with agent state, PR number, checks and review status, the frontier and the specs ready for acceptance.
disable-model-invocation: true
---
Run `"${CLAUDE_PLUGIN_ROOT}/scripts/board.sh"` and show its output as printed: the table (one line per worktree), the frontier and the specs ready for acceptance. Add nothing except a one-line hint when a row needs the user (checks failed, review requested, agent blocked).
