---
name: hunt-tests
description: Open a test hunt. Creates a worktree and Herdr workspace and starts a worker that removes the tests of this repository that prove nothing and opens one pull request for them.
argument-hint: [--sandbox] [--base <branch>]
disable-model-invocation: true
---
Run this exact command and relay its output:

```
"${CLAUDE_PLUGIN_ROOT}/scripts/hunt.sh" $ARGUMENTS
```

On success, answer with one line per printed key. On `error:`, quote it and stop.
