# 0006. Planning is its own session that writes issues, not code

Date: 2026-09-17
Status: accepted

## Context
- The pipeline consumed GitHub issues, but nothing produced good ones.
- The mattpocock skills (grilling, to-spec, to-tickets, triage, wayfinder, prototype) cover that job but load into every session, need a per-repository `issue-tracker.md` and carry prose the model does not need.

## Decision
A `planner` plugin with a main-thread `planner` agent runs as its own session that writes GitHub issues and nothing else.

## Consequences
- `plan.sh` opens it like a worker: worktree `plan/<slug>`, Herdr workspace, `claude --agent planner`, first turn `/planner:plan`.
- The topic or issue travels in git's branch description, so a restart needs no state file.
- The plan branch gets no commit and is never pushed. The worker of the first ticket writes the glossary terms and ADRs the spec lists.
- Prototype code leaves through `capture-prototype.sh` onto a pushed `prototype/<plan>-<name>` branch.
- Every planner skill has `disable-model-invocation: true`, so an unused skill costs no context. The user types each stage, such as `/planner:spec`.
- Skills needing the interview mechanics link to the grill skill's file rather than calling the Skill tool.
- The workflow owns the label vocabulary (`ready-for-agent`, `needs-triage`, `needs-info`, `ready-for-human`, `wontfix`, `spec`); `labels.sh` creates it.
- `issue.sh` sets sub-issues and blocking edges through GitHub's native APIs. Rejected requests are closed `wontfix` issues.
- The board gains a frontier of agent-ready, unblocked, unclaimed issues, which closes the loop from plan to claim.
- Rejected: a tracker config file, a separate knowledge base and the wayfinder map in the first version.
