---
name: yolo-claim
description: Claim a ready-for-agent GitHub issue in yolo mode. The worker merges its own PR once CI and reviewers are green, then cleans up its worktree.
argument-hint: <issue> [--sandbox] [--force] [--base <branch>] [--env NAME=VALUE]
disable-model-invocation: true
---
Run this exact command and relay its output:

```
"${CLAUDE_PLUGIN_ROOT}/scripts/claim.sh" $ARGUMENTS --yolo
```

Remind the user in one sentence that this worker will merge without their review. On `error:`, quote it and stop.
