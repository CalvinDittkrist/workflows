---
name: pr
description: Push the branch and open the pull request from a fresh context, so the description matches the code rather than the author's memory.
context: fork
agent: worker:pr-author
allowed-tools: Bash(${CLAUDE_PLUGIN_ROOT}/scripts/diff-context.sh), Bash(${CLAUDE_PLUGIN_ROOT}/scripts/panel.sh*)
---
Open the pull request for this branch.

Brief:
!`${CLAUDE_PLUGIN_ROOT}/scripts/diff-context.sh`
!`${CLAUDE_PLUGIN_ROOT}/scripts/panel.sh print`

Review summary from the author, which overrides the recorded one when it is not empty: $ARGUMENTS

Follow your agent instructions and reply with `pr: <url>`.
