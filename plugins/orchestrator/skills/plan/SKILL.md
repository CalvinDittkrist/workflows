---
name: plan
description: Open a planning session for an idea or an existing issue, or without an argument an open session that answers questions about the code. Creates a worktree and Herdr workspace and starts a planner.
argument-hint: [<idea words> | <#issue>] [--base <branch>]
disable-model-invocation: true
---
Run this exact command and relay its output:

```
"${CLAUDE_PLUGIN_ROOT}/scripts/plan.sh" $ARGUMENTS
```

On success, answer with one line per printed key. On `error:`, quote it and stop.
