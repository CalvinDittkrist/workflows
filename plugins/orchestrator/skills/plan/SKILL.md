---
name: plan
description: Open a planning session for an idea or an existing issue. Creates a worktree and Herdr workspace and starts a planner that turns it into agent-ready issues.
argument-hint: <idea words> | <#issue> [--base <branch>]
disable-model-invocation: true
---
Run this exact command and relay its output:

```
"${CLAUDE_PLUGIN_ROOT}/scripts/plan.sh" $ARGUMENTS
```

On success, answer with one line per printed key. On `error:`, quote it and stop.
