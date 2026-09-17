---
name: herdr
description: Control Herdr panes, agents, workspaces and worktrees by hand. Load only when the scripted skills are not enough, for example to read a worker's screen or answer its question.
allowed-tools: Bash(${CLAUDE_PLUGIN_ROOT}/scripts/herdr-skill.sh)
---
Herdr's own skill is authoritative and versioned with the binary. It is injected below (frontmatter stripped).

!`${CLAUDE_PLUGIN_ROOT}/scripts/herdr-skill.sh`
