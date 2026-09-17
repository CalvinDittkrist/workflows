---
name: finish
description: End the planning session. Refuses while work would be lost, then removes this worktree, workspace and branch.
disable-model-invocation: true
argument-hint: [--force]
---
First list the issues this session created or changed, one line each with number and title, so the user has the summary. Then run:

    "${CLAUDE_PLUGIN_ROOT}/scripts/finish.sh" $ARGUMENTS

Relay its output. On `error:` quote it and stop; the usual fix is `/planner:prototype` to capture code, or `--force` to drop it.
