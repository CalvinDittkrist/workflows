# 0014. Claiming requires ready-for-agent, with --force as the only exception

Date: 2026-09-20
Status: accepted

## Context
- `/orchestrator:board` lists only `ready-for-agent` issues in its frontier, but `/orchestrator:claim <n>` took any open issue.
- A worker needs the brief a planner writes, which is why the label vocabulary of [ADR 0006](0006-planner-session-writes-issues-not-code.md) exists.
- A worker claimed spec #3, which had no acceptance criteria, and shipped one oversized pull request (#17).

## Decision
`claim.sh` refuses an open issue without `ready-for-agent` before it creates anything, and `--force` is the only exception.

## Consequences
- The refusal leaves no worktree, branch, Herdr workspace or assignment. The error names the labels and the fix.
- For a `spec` the fix is to claim its tickets, or to plan on it when they are all closed. Otherwise it is triage in a planning session.
- `--force` claims anyway with a warning that names the labels; the yolo claim passes it through.
- An issue claimed earlier still reports `already-claimed`. A `spec` that fits one session is claimed once it carries `ready-for-agent`.
- An issue reaches a worker only through triage or `/planner:tickets`, so its pull request is reviewable against it.
- Repositories without the planner label their issues or pass `--force`; `labels.sh` creates the label on demand.
- The board reports and the claim is the gate, on one rule.
- Rejected: filtering in the board alone, an allow list of labels, and a refusal in the worker's session hook.
