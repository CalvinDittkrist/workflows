---
name: release
description: Release a milestone vX.Y.Z. Promotes dev to main through a pull request when both exist, then tags main, publishes the GitHub release with generated notes and closes the milestone.
argument-hint: <vX.Y.Z>
disable-model-invocation: true
---
Run this exact command and relay its output:

```
"${CLAUDE_PLUGIN_ROOT}/scripts/release.sh" $ARGUMENTS
```

The script refuses when the milestone does not exist, still has open issues, or the tag already exists. Quote the `error:` line and stop. On `status: waiting`, relay the `next:` line; do not merge the promotion pull request unless the user asks.
