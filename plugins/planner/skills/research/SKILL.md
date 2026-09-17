---
name: research
description: Settle a fact from outside the repo (docs, specs, a dependency's source) with a background subagent and bring the answer into the session.
disable-model-invocation: true
argument-hint: <question>
---
Spawn one background subagent with the Agent tool for this question: $ARGUMENTS

Brief it: answer from primary sources only (official docs, the dependency's source, the spec). Cite every claim with its URL or path. Say what could not be verified. Reply in under 300 words.

Keep working on the rest of the frontier while it runs. When the report arrives, quote the answer with its sources to the user and carry it into the spec or ticket where the decision lands. Nothing is written to the repo.
