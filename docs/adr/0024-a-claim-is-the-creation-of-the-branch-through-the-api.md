# 0024. A claim is the creation of the branch through the GitHub API

Date: 2026-09-21
Status: accepted; amended 2026-09-23 (the factory's own orphaned branch, and a lost claim answers one routing)
Amends: [0003](0003-herdr-worktree-per-issue.md) (the branch name stays the contract; this says who may create it and when)

## Context
- With a second claimer, two can start the same issue, and the loser wastes a session and leaves a branch behind.
- Assigning twice succeeds, and so does a second create-only push.
- `POST /repos/{owner}/{repo}/git/refs` refuses a second caller with 422, whatever commit the ref names.

## Decision
A remote claim is the creation of the issue's branch `<type>/<issue>-<slug>` through the GitHub API, and a push is never a claim.

## Consequences
- The winner assigns itself and makes its worktree. The loser records the outcome `lost` and touches nothing.
- A lost run answers only its routing; the routing label set again queues the issue like any routed issue.
- An unrecorded branch is the factory's orphaned claim when it has commits, the factory's login last touched it, and no pull request is open (#110).
- The factory takes such a branch up ([ADR 0026](0026-the-factory-never-deletes-work-on-its-own.md)).
- Each host needs its own login ([ADR 0027](0027-the-factorys-isolation-boundary-is-the-host.md)).
- An empty branch blocks until someone deletes it; the release signal handles a dead claimer.
- The local claim refuses a routed issue or an existing remote branch; `--force` adopts the branch ([ADR 0014](0014-claims-require-ready-for-agent.md)).
- Rejected: assignment and push as the claim; neither has one winner.
