---
name: pr
description: Push the branch and open the pull request from a fresh context, so the description matches the code rather than the author's memory.
context: fork
agent: worker:pr-author
---
Open the pull request for this branch.

Brief:
!`"${CLAUDE_PLUGIN_ROOT}/scripts/diff-context.sh"`

Review summary from the author (may be empty): $ARGUMENTS

Follow your agent instructions and reply with `pr: <url>`.
