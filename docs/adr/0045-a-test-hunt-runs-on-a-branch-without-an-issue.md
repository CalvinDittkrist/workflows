# 0045. A test hunt runs on a branch without an issue

Date: 2026-09-24
Status: accepted

## Context
- Repositories collect tests that prove nothing; no reviewer reads a whole suite. Spec #170.
- What a hunt removes is known only at its end.
- Every worker session was bound to an issue through its branch `<type>/<n>-<slug>`.

## Decision
`/orchestrator:hunt-tests` opens a worktree of `hunt/tests-<date>` and starts a worker session with the settings of a manual claim and `/worker:hunt-tests` as its first prompt. No issue is read or created; its pull request is its trace.

- The worker hook stays silent on the branch, except to hand a waiting handoff note to a fresh context.
- The factory's branch contract does not match it.
- The board shows `hunt`, and the status line `no issue`.
- It is merged by pull request number and abandoned by branch name.
- One hunt is open at a time, here and on origin.
- The hunt record in the worktree's git directory stands where the issue stands ([ADR 0018](0018-worker-stages-hand-facts-over-through-the-worktree-git-dir.md)). `hunt.sh print` puts it in the diff context.
- The scripts that print the issue print `none`.
- The pull request carries no closing keyword.
- The hunt is local only and takes no path argument.

## Consequences
- From the review stage on, a hunt runs a ticket's pipeline.
- A hunt that removes nothing opens no pull request; its worktree stays until the maintainer abandons it.
- A drift test binds the two plugins' copies of the test-file rule.
- Rejected: an issue per hunt, which cannot describe the work beforehand.
