# 0030. Agents verify Claude Code facts against the live documentation, and the worker reads it through a pinned script

Date: 2026-09-21
Status: accepted

## Context
- Most of what this repository writes is Claude Code surface, which moves between releases, and agents answer from training data.
- The worker's context reads issue bodies, comments and CI logs, so a fetch tool there is the widest injection surface.
- A measured finding: a subagent's tools are granted, not intersected with the parent's, wider than [sub-agents](https://code.claude.com/docs/en/sub-agents.md) describes.

## Decision
`AGENTS.md` requires verifying Claude Code facts against the live documentation, and the worker reads it only through a pinned script behind `/worker:docs`.

## Consequences
- The rule triggers when a change touches Claude Code surface, and the page is cited in the issue or pull request.
- `/planner:research` reads the index `https://code.claude.com/docs/llms.txt`; its facts travel in the ticket's `## Sources`.
- `claude-docs.sh` builds the URL from a slug and prints nothing from outside `https://code.claude.com/docs/`.
- A read-only `docs-lookup` subagent runs it, so pages stay out of the worker's context, which has no `WebFetch`.
- The worker's `Agent` tool allows only the plugin's own subagents; a test finds drift against `plugins/worker/agents/`.
- `Bash` can still call `curl`: containment is the permission layer and `allowedDomains` ([security.md](../security.md)).
- Rejected: `WebFetch` in the worker, an open fetch tool in the context that reads others' text.
- Rejected: `claude-code-guide`, absent from headless sessions, which the factory starts ([ADR 0022](0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md)).
