# 0017. Worker sessions run subagents in the foreground, and the agent never waits by polling

Date: 2026-09-20
Status: accepted

## Context
- In an interactive session Claude Code launches a subagent in the background, and its report arrives later as a notification.
- A worker told to collect the reports has nothing to collect and cannot sleep in the foreground.
- It fell into a background sleep loop, a full turn over the whole context each time.

## Decision
The session settings a claim builds for a worker carry `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1`, and the worker never waits with a sleep, a timer or a polling loop.

## Consequences
- The variable is set in the normal and the sandboxed start.
- A subagent call blocks and returns the report; a panel launched in one message still runs concurrently.
- A background subagent is waited for by ending the turn. A longer wait belongs to a script that blocks for one slice, such as `pr-wait.sh`.
- Planner sessions keep background subagents for their research stage.
- The variable reads `1`, `true`, `yes` and `on`; `facts.sh` prints `subagents: foreground | background`.
- A standalone start or `--continue` has no session settings, so the review stage waits by `subagents:`.
- A review round costs the reviewers' tokens alone.
- `run_in_background` is gone too, so a command beyond the Bash tool's ten-minute limit, such as a slow `make check`, must be split.
- A harness rename of the variable shows in `subagents:`.
- Rejected: background subagents in workers, which the worker waits for by polling.
