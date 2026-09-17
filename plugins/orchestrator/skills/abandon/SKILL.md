---
name: abandon
description: Drop a claimed worktree without merging. Refuses to delete unpushed work unless --force.
argument-hint: <issue|branch> [--force]
disable-model-invocation: true
---
Run `"${CLAUDE_PLUGIN_ROOT}/scripts/abandon.sh" $ARGUMENTS` and relay the output. Never add `--force` on your own; if the script refuses, tell the user why and let them decide.
