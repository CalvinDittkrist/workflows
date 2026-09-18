---
name: merge
description: Squash-merge a ready PR and remove its worktree, Herdr workspace and branch. A promotion PR from dev gets a merge commit and keeps dev.
argument-hint: <pr> [--allow-unstable] [--ignore-threads]
disable-model-invocation: true
---
Run this exact command and relay its output:

```
"${CLAUDE_PLUGIN_ROOT}/scripts/merge.sh" $ARGUMENTS
```

The script refuses when checks are failing or pending, review threads are unresolved, or changes were requested. Quote the `error:` line; do not add flags the user did not ask for.
