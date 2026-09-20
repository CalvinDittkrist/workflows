# 0014. Claiming requires ready-for-agent, with --force as the only exception

Date: 2026-09-20
Status: accepted

## Context
`/orchestrator:board` lists only `ready-for-agent` issues in its frontier, but `/orchestrator:claim <n>` took any open issue. The label vocabulary of [ADR 0006](0006-planner-session-writes-issues-not-code.md) exists because a worker needs the brief a planner writes: acceptance criteria, scope, blocking edges. Claiming by number bypassed the frontier's filter, so a `spec`, `needs-triage`, `needs-info` or `ready-for-human` issue could start a worker. That happened with #3: a worker claimed the spec, had no acceptance criteria, decided the gaps itself and shipped one pull request of 400 lines across two plugins (#17). The board was the only thing that knew which issues were ready, and it is a report, not a gate.

## Decision
The gate moves into `claim.sh`, where the worktree is created. A claim refuses an open issue that does not carry `ready-for-agent` before anything exists: no worktree, no branch, no Herdr workspace, no assignment. The error names the labels the issue carries and the fix. For a `spec` it says to claim the spec's tickets, or to open a planning session on the spec when all of them are closed; for anything else it says to open a planning session on the issue to triage it.

`--force` is the only exception and claims the issue anyway, with a warning that names the labels. The yolo claim passes the flag through. The check sits after the lookup of an existing worktree, so an issue claimed earlier still reports `already-claimed` whatever its labels are now, and a `spec` that fits one session is claimed like any other issue once it carries `ready-for-agent`.

## Consequences
The label is now load-bearing: an issue reaches a worker only through triage or through `/planner:tickets`, which is what makes a ticket's pull request reviewable against its issue. Claiming an issue the planner has not seen costs one extra flag, which is the deliberate exception the error names. Repositories that do not use the planner must label their issues or claim with `--force` every time; the label is created on demand by `labels.sh`, so no configuration is involved. The board's frontier and the claim now agree on one rule, in two places by design: the board reports, the claim refuses. Rejected: filtering in the board alone (a report cannot stop a claim by number), refusing every label outside an allow list (new labels would break claims), and letting the worker's session hook refuse (the worktree, workspace and assignment already exist by then).
