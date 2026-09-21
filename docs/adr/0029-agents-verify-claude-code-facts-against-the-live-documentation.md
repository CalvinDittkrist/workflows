# 0029. Agents verify Claude Code facts against the live documentation, and the worker reads it through a pinned script

Date: 2026-09-21
Status: accepted

## Context
This repository builds Claude Code plugins, so most of what it writes is Claude Code surface: manifest fields, skill and agent frontmatter, hook events, settings keys, permission syntax, model aliases, CLI flags. That surface moves between releases, and an agent answers such a question from training data unless it is told not to. Until now the maintainer said so by hand, once per session.

Who can reach the documentation at all differs per agent. The planner has `WebFetch`. The worker's main context has no web tool, and it should not have one: its context is where the issue body, the PR comments, the CI logs and the review comments land, the four places where text written by someone else arrives, so a free fetch tool there is the widest part of the prompt-injection and exfiltration surface the pipeline has.

Issue #44 assumed the worker's tool list also bounds what it can spawn, reading "Subagents inherit the built-in tools and MCP tools available in the main conversation" (https://code.claude.com/docs/en/sub-agents.md, checked 2026-09-21) as a ceiling. Two runs in a real worker session (Claude Code 2.1.x, 2026-09-21) show that is wrong: a subagent whose own file lists `WebFetch` and `WebSearch` got exactly those and fetched `code.claude.com`, and an agent with the wildcard tool set got `WebFetch` as a deferred tool and loaded it. A subagent's declared tool list is granted, not intersected with the parent's. What a tool list on the main agent buys is therefore the surface of that one context, not a network boundary; the enforced boundaries are the permission layer and the sandbox's `allowedDomains`.

## Decision
The rule is written down once, in `AGENTS.md`, and it is triggered rather than permanent: when a change touches Claude Code surface, the fact is verified against the current documentation before memory is relied on, and the page is cited in the issue or the pull request. `AGENTS.md` loads into every session and every subagent, so the rule reaches read-only agents too, where it costs two lines and asks nothing they cannot do.

Each session verifies with what it has. A planning session fetches with `/planner:research`, whose subagent is told that Claude Code facts come from the index `https://code.claude.com/docs/llms.txt` and its pages as `.md`, and the answer travels on in the ticket's `## Sources` section with its URL and the date it was checked, so the implementing session inherits the fact instead of looking it up again.

A worker reads the same documentation through `plugins/worker/scripts/claude-docs.sh` and `/worker:docs <question>`. The script takes a page slug of lowercase letters, digits and hyphens, or nothing, builds the URL itself from a hard-coded origin, speaks https only before and after a redirect, and prints nothing when the answer came from outside `https://code.claude.com/docs/`. The skill spawns one read-only `docs-lookup` subagent that runs that script, so the pages stay in that context and only a short answer with its page URLs returns. The worker's own tool list keeps no `WebFetch` and no `WebSearch`, and a test fails if one appears.

## Consequences
Every agent carries the rule, and the two sessions that act on it have a way to follow it that matches what they are: the planner fetches freely, the worker asks a subagent that can reach one origin through one command. A documentation page never lands in the context that also holds issue text, which is both a token and an injection argument, and a page the script prints is data like any other repository output.

The honest limit is the one the two runs established: this is surface reduction, not containment. A worker that wanted the open web could declare a subagent with `WebFetch` and get it, and `/worker:docs` does not stop it. Containment is the permission layer and the sandbox's `allowedDomains`, which is why `code.claude.com` is in the example list in [security.md](../security.md) and why the network argument for the script is stated there as defence in depth rather than as a wall.

The rejected alternative was giving the worker `WebFetch` and a prompt rule about where to point it. It is simpler, and it is what the planner does, but it puts an unconstrained fetch tool in the one context that reads four kinds of text written by others, and it would pull whole documentation pages (115 KB for `sub-agents.md` on 2026-09-21) into the context whose budget the pipeline is built around.

What we must watch: the slug set and the `.md` convention are a property of the documentation site, so a change there breaks the script rather than the rule, with an `error:` line naming the index as the fix. And the tool-inheritance finding is a fact about one Claude Code version; if a later release does intersect a subagent's tools with its parent's, the boundary becomes real and this ADR becomes conservative rather than wrong.
