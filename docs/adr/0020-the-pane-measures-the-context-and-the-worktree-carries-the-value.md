# 0020. The pane's status line measures a worker's context, and the worktree carries the value

Date: 2026-09-21
Status: accepted; the 200 000 safety net is superseded by [ADR 0031](0031-the-workflow-pins-the-size-at-which-a-worker-session-compacts.md)

## Context
- A worker session's context size was invisible, so nothing could decide when to hand work to a fresh session ([ADR 0017](0017-worker-subagents-run-in-the-foreground.md)).
- Claude Code states the size only in the JSON it pipes into the `statusLine` command, which the orchestrator configures at the claim.
- The worker, a different plugin, needs the number.

## Decision
The orchestrator's `statusline.sh` renders the pane line and writes the size to `<this worktree's git directory>/worker/context` (ADR 0018), which the worker's `checkpoint.sh` reads.

## Consequences
- That file is the whole contract between the two plugins.
- `checkpoint.sh` answers `context_tokens`, `threshold` (`WF_HANDOFF_TOKENS`, default 120 000) and `handoff`.
- A missing value, or one older than `WF_CONTEXT_MAX_AGE`, reads as `handoff: yes`.
- Without a status line, such as in the Docker sandbox, the checkpoint answers `unavailable`.
- A claimed worker sets `autoCompactWindow: 200000`; the status line shows the size against it and `model_context_window` names the model's window.
- `statusline.sh` makes one `jq` call, writes by rename and prints a line whatever its input. It writes nothing before the first response.
- `refreshInterval: 60` keeps the value fresh through a long gate.
- The value describes a worktree, not a session, and a stage that acts on it comes later ([#37](https://github.com/CalvinDittkrist/workflows/issues/37)).
- Rejected: code shared between the plugins, and a context size the worker guesses from its own transcript.
