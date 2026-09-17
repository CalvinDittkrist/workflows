# 0006. Planning is its own session that writes issues, not code

Date: 2026-09-17
Status: accepted

## Context
The pipeline consumed GitHub issues but nothing produced good ones. The mattpocock skills (grilling, to-spec, to-tickets, triage, wayfinder, prototype) cover that job, but they load into every session, depend on a per-repo `issue-tracker.md`, and carry prose the model does not need. Token cost per session and one uniform but flexible flow were the constraints.

## Decision
A `planner` plugin with a main-thread `planner` agent. The orchestrator's `plan.sh` opens it like a worker: worktree `plan/<slug>`, Herdr workspace, `claude --agent planner`, first turn `/planner:plan`. The topic or issue travels in git's branch description, so a restarted session recovers it without a state file.

The planner writes GitHub issues and nothing else. It never commits on the plan branch and the branch is never pushed; glossary terms and ADRs a plan needs are listed in the spec issue, and the worker of the first ticket writes them. Prototype code leaves through `capture-prototype.sh` onto a pushed `prototype/<plan>-<name>` branch.

Every planner skill has `disable-model-invocation: true`. The user invokes stages in any order; the driver only recommends a route. Skills that need the interview mechanics link to the grill skill's file instead of invoking it through the Skill tool. The label vocabulary (`ready-for-agent`, `needs-triage`, `needs-info`, `ready-for-human`, `wontfix`, `spec`) belongs to the workflow and `labels.sh` creates it; no tracker config file exists. Sub-issues and blocking edges use GitHub's native APIs through `issue.sh`, with a body-text fallback. Rejected requests are closed `wontfix` issues; no separate knowledge base.

## Consequences
No session pays for skills it does not use: a skill with `disable-model-invocation` puts not even its description into context, so the planner plugin can be enabled repo-wide. The orchestrator's board gains a frontier (agent-ready, unblocked, unclaimed issues), which closes the loop from plan to claim. The cost is that the model cannot chain stages itself; the user types `/planner:spec`. The wayfinder map (multi-session planning with decision tickets) was left out of the first version; it fits the same shape (`issue.sh` already handles sub-issues and blocking) and can be added as a `map` skill later.
