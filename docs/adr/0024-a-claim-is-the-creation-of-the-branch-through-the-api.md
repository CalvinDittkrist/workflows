# 0024. A claim is the creation of the branch through the GitHub API

Date: 2026-09-21
Status: accepted; amended 2026-09-23 (the factory's own orphaned branch, and a lost claim answers one routing)
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

Amended 2026-09-23: a factory that lost its data directory also lost the records that name the branches it holds, and read its own branches as foreign claims. On the verification host (issue #110) two released issues were routed again, their claims met the branches the previous host had pushed, and both ended `lost`; the work stayed on the branches and nothing took it up again. A branch of the issue that no record of the factory names is therefore its own orphaned claim when three readings agree: it carries commits beyond the base, the latest activity GitHub records on it (`GET /repos/{owner}/{repo}/activity?ref=…`, its creation or a push) was made by the login the factory's gh is signed in as, and no pull request of it is open. The factory then assigns itself, makes the worktree from that branch and runs the worker on its commits, as it does for an issue it let go and that was routed again ([ADR 0026](0026-the-factory-never-deletes-work-on-its-own.md)). Any other branch, and any reading that fails, is still a foreign claim: the run is `lost` and touches nothing. A branch that carries nothing is never taken up this way, because that is what a claim another claimer made a moment ago looks like; a claim the factory died on before its first push therefore stays in the way until somebody deletes the branch. Two factories signed in as one login are one claimer to this reading, which is one more reason each host has a machine user of its own ([ADR 0027](0027-the-factorys-isolation-boundary-is-the-host.md)).

A lost run answers the routing it was started for and no later one. "Does not try it again while the branch exists" had become "never": a lost claim lets nothing go, so its issue stayed out of the line after the branch was deleted and the label was set again. The routing label set again after a lost run's signal queues the issue like any routed issue, and the claim then finds the name free, or its own orphaned branch, or loses once more; the label that was on the issue when the claim was lost queues nothing.

The local claim respects remote claims: `claim.sh` refuses an issue that carries the routing label, and an issue for which a remote branch of the contract's shape already exists, naming the fix in its error line. `--force` is the exception for both, as it already is for a missing `ready-for-agent` ([ADR 0014](0014-claims-require-ready-for-agent.md)); with `--force` on an existing remote branch the local claim adopts that branch instead of starting from the base, so work the factory saved can be continued by hand.

## Consequences
Exactly one claimer works an issue, without a lock, a lease or a database: the atomicity is GitHub's, and it is the same fact the rest of the workflow already reads — the branch.

A claim is visible before a single commit exists. A branch with no commit beyond its base is what a claim looks like, and tools that list branches will show them.

The claim outlives the claimer. A factory that dies after the claim leaves a branch that refuses every later claimer, which is what the release signal and the cleanup rules of [ADR 0026](0026-the-factory-never-deletes-work-on-its-own.md) exist for.

A local claim now has two more ways to refuse, and both are about a machine the developer cannot see. The error lines say which one it is, and `--force` answers both — by adopting the remote branch rather than ignoring it, because ignoring it is how work gets lost.
