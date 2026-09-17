---
name: claim
description: Claim a GitHub issue. Creates a worktree and Herdr workspace and starts a worker session that implements it through review, PR and CI.
argument-hint: <issue> [--sandbox] [--base <branch>]
disable-model-invocation: true
---
Run this exact command and relay its output:

```
"${CLAUDE_PLUGIN_ROOT}/scripts/claim.sh" $ARGUMENTS
```

On success, answer with one line per printed key. On `error:`, quote it and stop.
