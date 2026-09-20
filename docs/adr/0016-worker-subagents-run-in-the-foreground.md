# 0016. Worker sessions run subagents in the foreground, and the agent never waits by polling

Date: 2026-09-20
Status: accepted

## Context
In an interactive session Claude Code launches a subagent in the background: the Agent call returns at once and the report arrives later as a notification. A worker whose review stage says "collect the reports" therefore has nothing to collect, does not know that ending its turn is how it waits, and cannot sleep in the foreground because the harness blocks it. The observed result is a background sleep loop: one measured session ran 328 of them in 2 h 20 min, every one a full turn over the whole context, and grew from 120k to 412k tokens (about 80 million cache-read tokens) through the loop alone. Seen in 3 of 10 sessions since Claude Code 2.1.278.

## Decision
The session settings a claim builds for a worker carry `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1`, next to the mode and the issue, in the normal and in the sandboxed start. Subagent calls then block and return the report as the tool result, in every kind of session, while a panel launched in one message still runs its reviewers concurrently. The worker prompt states the matching rule: never wait with a sleep, a timer or a polling loop; every other wait belongs to a pipeline script, which does it in one blocking call (`pr-wait.sh`). Planner sessions keep background subagents, because their research stage works while a subagent runs.

Verified against Claude Code 2.1.278: with the variable unset the Agent tool result is `Async agent launched successfully` and the answer needs a second turn; with it set the subagent's report is the tool result of the launch.

## Consequences
A review round costs the reviewers' tokens and nothing else, and the worker's context grows by the reports alone. The worker cannot work while a subagent runs, which it never did usefully: it waited. The variable also takes the Bash tool's `run_in_background` parameter away from the worker, so a sleep loop is not available at all. The switch is a Claude Code environment variable, not a documented settings field, so a rename in the harness silently restores the old behaviour; the sleep-call count in the context report is what would show it.
