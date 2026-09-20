---
name: plan
description: Driver of a planning session. Shows the session facts and the routes, recommends one, then hands over to the stage skills the user invokes.
disable-model-invocation: true
allowed-tools: Bash(${CLAUDE_PLUGIN_ROOT}/scripts/facts.sh), Bash(${CLAUDE_PLUGIN_ROOT}/scripts/labels.sh)
---
Session:
!`${CLAUDE_PLUGIN_ROOT}/scripts/facts.sh`
!`${CLAUDE_PLUGIN_ROOT}/scripts/labels.sh`

Routes:
- **Idea to tickets.** `/planner:grill` asks question rounds until nothing is open, `/planner:spec` writes one spec issue, `/planner:tickets` cuts it into agent-ready issues with blocking edges.
- **Existing issue.** `/planner:triage` verifies the claim, grills when needed, posts the agent brief and sets the labels.
- **Finished spec.** `/planner:accept [spec]` lets a read-only checker judge the spec against the code, then turns the gaps into tickets, accepted deviations or nothing, and closes the spec when nothing is left open.
- **Tools for any route.** `/planner:research` settles a fact from outside the repo. `/planner:prototype` builds throwaway code for a question that talking does not settle.
- `/planner:finish` ends the session.

Do now:
1. Read AGENTS.md, docs/architecture.md and docs/glossary.md when they exist. Nothing else yet.
2. If the session starts from an issue, summarize it in three lines. Otherwise restate the topic in one line.
3. Recommend one route in one sentence and stop. When the facts say the session's issue is a spec whose tickets are all closed, the route is `/planner:accept`. The user invokes the stage skills; you never start a stage on your own.
