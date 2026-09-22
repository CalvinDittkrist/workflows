---
name: ci
description: Wait for CI checks and bot reviews on the open PR and report green, conflicts, checks-failed, review-comments or waiting.
argument-hint: [pr]
allowed-tools: Bash(${CLAUDE_PLUGIN_ROOT}/scripts/checkpoint.sh*), Bash(${CLAUDE_PLUGIN_ROOT}/scripts/repair.sh*)
---
Context checkpoint:
!`${CLAUDE_PLUGIN_ROOT}/scripts/checkpoint.sh ci`

The checkpoint above measured this context as you entered the stage, whichever stage you came from. On `handoff: yes` wait for nothing here: follow the `next:` procedure it printed and hand the CI stage over to a fresh context. On `no` or `unavailable` continue.

Repair record:
!`${CLAUDE_PLUGIN_ROOT}/scripts/repair.sh print`

The repair record counts the repair rounds of the pull request this branch has open and holds the limit across a handover, so the number is the script's and never yours. It is the branch's budget: the script reads which pull request that is itself, and an argument you pass this skill selects what to wait on, not what to count. Count every repair round with `"${CLAUDE_PLUGIN_ROOT}/scripts/repair.sh" round` before you start it; when it refuses, repair nothing more and report the conflict, the failing checks or the unresolved threads to the maintainer.

Run `"${CLAUDE_PLUGIN_ROOT}/scripts/pr-wait.sh" $ARGUMENTS` with the Bash tool timeout set to 600000 ms. The script returns within about nine minutes:

- `status: green` → done. Continue the pipeline.
- `status: conflicts` → the branch cannot be merged into its base, and GitHub ran no workflow for it. Count the round, then follow the `help:` line: fetch the base, `git merge origin/<base>`, resolve every conflict, commit the merge, push, and run `/worker:ci` again; CI runs the gate on the push. Never rebase and never force-push: the branch is pushed, and its history stays.
- `status: review-comments` → count the round, then invoke `/worker:address-reviews`.
- `status: checks-failed` → count the round, then read the listed failed checks with `gh run view <id> --log-failed` (or `npx -y gh-axi run view <id> --log-failed`), fix the cause, commit, push, and run `/worker:ci` again.
- `status: waiting` (exit 3) → run the script again, not this skill; nothing is stuck. That is no repair round and no entrance into the stage, so it counts nothing and meets no checkpoint.

Never poll GitHub in a loop yourself; the script does the waiting. Never treat "no checks" as green when the repository has CI configured.
