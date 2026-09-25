# 0026. The factory never deletes work on its own

Date: 2026-09-21
Status: accepted

## Context
- An unattended run stops half way, from a blocker to a session error, unwatched.
- A removed worktree loses its unpushed commits.
- A retry of a wrong issue fails again at full price.

## Decision
The factory never deletes work on its own: whatever a run ends with, its branch, worktree, assignee, record and event log stay.

## Consequences
- It resumes by itself once per issue after an interruption. A second one is a failure: a comment mentions the maintainer, and the run waits.
- `quota` does not use up that resume.
- The release signal, removing the assignee from an issue the factory holds, queues a resumed run in its worktree.
- Worktree and local branch go once the pull request is merged or closed, the issue closed, or the routing label removed.
- The factory pushes a worktree's commits before removing it, and deletes a remote branch with no commit beyond its base.
- Amended 2026-09-23: it also deletes a branch whose own pull request was merged at the branch's current remote commit.
- Letting an issue go removes the assignee. A rerouted issue is taken back only on a branch with work of its own.
- Records and event logs are never deleted automatically.
- A leftover branch blocks a later claim ([ADR 0024](0024-a-claim-is-the-creation-of-the-branch-through-the-api.md)); the release signal and `--force` adoption lead back.
- Rejected: automatic cleanup and retries, which destroy unpushed work and spend money unwatched.
