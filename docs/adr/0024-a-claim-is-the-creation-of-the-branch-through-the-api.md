# 0024. A claim is the creation of the branch through the GitHub API

Date: 2026-09-21
Status: accepted
Amends: [0003](0003-herdr-worktree-per-issue.md) (the branch name stays the contract; this says who may create it and when)

## Context
As soon as a second claimer exists — a factory beside a developer, or two factories — two of them can read the same agent-ready issue in the same second and both start working it. The loser wastes a worker session and leaves a branch, a worktree and an assignee behind.

The candidates for the decisive act were measured against GitHub on 2026-09-21:

| Act | Has exactly one winner |
| --- | --- |
| Assigning the issue | no, assigning twice succeeds |
| A create-only `git push --force-with-lease=<ref>:` | no, from the same base commit the second push is "up to date" and succeeds |
| `POST /repos/{owner}/{repo}/git/refs` | yes, 422 "Reference already exists", even for the same commit |

Only the last one refuses the second caller, and it refuses whatever commit the ref names, which is what a race needs.

## Decision
A remote claim is the creation of the issue's branch `<type>/<issue>-<slug>` through the GitHub API. The claimer that gets 201 owns the issue and only then assigns it to itself and creates its worktree. A claimer that gets "Reference already exists" has lost: it records the outcome `lost`, leaves the issue alone, and does not try it again while the branch exists.

A push is not a claim. Nothing may treat "my push succeeded" as ownership.

The local claim respects remote claims: `claim.sh` refuses an issue that carries the routing label, and an issue for which a remote branch of the contract's shape already exists, naming the fix in its error line. `--force` is the exception for both, as it already is for a missing `ready-for-agent` ([ADR 0014](0014-claims-require-ready-for-agent.md)); with `--force` on an existing remote branch the local claim adopts that branch instead of starting from the base, so work the factory saved can be continued by hand.

## Consequences
Exactly one claimer works an issue, without a lock, a lease or a database: the atomicity is GitHub's, and it is the same fact the rest of the workflow already reads — the branch.

A claim is visible before a single commit exists. A branch with no commit beyond its base is what a claim looks like, and tools that list branches will show them.

The claim outlives the claimer. A factory that dies after the claim leaves a branch that refuses every later claimer, which is what the release signal and the cleanup rules of [ADR 0026](0026-the-factory-never-deletes-work-on-its-own.md) exist for.

A local claim now has two more ways to refuse, and both are about a machine the developer cannot see. The error lines say which one it is, and `--force` answers both — by adopting the remote branch rather than ignoring it, because ignoring it is how work gets lost.
