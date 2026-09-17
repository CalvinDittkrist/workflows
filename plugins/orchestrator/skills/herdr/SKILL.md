---
name: herdr
description: Control Herdr panes, agents, workspaces and worktrees by hand. Load only when the scripted skills are not enough, for example to read a worker's screen or answer its question.
---
Herdr's own skill is authoritative and versioned with the binary. It is injected below (frontmatter stripped).

!`herdr --skill 2>/dev/null | awk 'BEGIN{n=0} /^---$/ && n<2 {n++; next} n>=2 {print}'`
