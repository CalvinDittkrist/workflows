# 0025. The factory never deletes work on its own

Date: 2026-09-21
Status: accepted

## Context
An unattended run stops half way for reasons that have nothing in common: a blocker the worker found, an error in the session, the deadline, a power cut, a quota that ran out. Nobody is watching when it happens.

A host that cleans up after itself is tempting — it keeps the disk free and the queue moving. It is also the one behaviour that can destroy hours of work that exists nowhere else: a worktree with commits that were never pushed is gone for good once it is removed.

Retrying is the same trap one level up. A run that failed because the issue is wrong fails again, and again, at full price, until somebody notices the bill.

## Decision
The factory never deletes work on its own. Whatever a run ends with, its branch, its worktree and its assignee stay, and its record and event log stay.

It resumes by itself exactly once per issue, after an interruption. A second interruption is handled like a failure: a comment that mentions the maintainer, and the run waits. `quota` is the one exception and does not use up that resume, because its cause passes by itself and has nothing to do with the issue.

What waits, waits for a person, and the release signal is removing the assignee from an issue the factory holds: the issue matches the routing rule again, the factory finds its own branch and record, and queues a resumed run in the same worktree, which sees the new comments through its session start.

Cleanup is tied to a decision somebody made on GitHub: the worktree and the local branch go once the pull request is merged or closed, the issue is closed, or the issue no longer carries the routing label. Before it removes a worktree the factory pushes the commits that worktree holds. It deletes the remote branch only when the branch has no commit beyond its base, so a claim that produced nothing leaves nothing behind while work never does. When it lets an issue go without a pull request it also removes its assignee, so the issue is free again. A resumed run whose worktree is gone recreates it from the remote branch.

Run records and event logs are never deleted automatically.

## Consequences
A power cut, a timeout after 100 minutes or a misunderstood issue costs time, never work. Every state a run can stop in has exactly one way out that a person chose, and the factory has no loop that can spend money by itself.

The host accumulates: worktrees of open pull requests, remote branches of runs nobody has decided about, records and logs of every run ever made. That is deliberate, and it makes disk space an operator's job — as is deleting a record, which is a delete by hand or not at all.

Every branch the factory leaves behind blocks a later claim on that issue ([ADR 0023](0023-a-claim-is-the-creation-of-the-branch-through-the-api.md)), which is what makes the release signal and `--force` adoption the two ways back in.

A run that stops for a reason that would pass — a network blip in the middle of the night — still waits for a person, because the factory cannot tell that case from an issue that is simply wrong.
