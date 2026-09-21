# 0020. The pane's status line measures a worker's context, and the worktree carries the value

Date: 2026-09-21
Status: accepted

## Context
A worker session's context size was invisible while it worked: the maintainer saw panes, not sizes, and the worker itself could not say how full its window was, so nothing could decide when to hand work over to a fresh session. The measured runs behind [ADR 0017](0017-worker-subagents-run-in-the-foreground.md) had grown from 120k to 412k tokens before anyone noticed. Claude Code answers this question only in one place: the JSON it pipes into the `statusLine` command on every render, which carries `context_window.total_input_tokens`. That is a per-session hook of the session's own settings, which the orchestrator writes when it claims an issue — while the number is needed in the worker session, in a different plugin, whose skills and agents the worker session is the only one to load.

## Decision
The orchestrator's `statusline.sh` renders the pane line (issue, mode, context size) and writes the size it just read to `<this worktree's git directory>/worker/context`, next to the worker's own records (ADR 0018): `total_input_tokens`, `context_window_size` and the time of the reading. That file is the whole contract between the two plugins; no code crosses between them, as everywhere else in this repository. The worker's `checkpoint.sh` reads it and answers `context_tokens`, `threshold` and `handoff`, with the threshold from `WF_HANDOFF_TOKENS` (default 120 000).

A value that is missing, unreadable or older than `WF_CONTEXT_MAX_AGE` (default 900 s) reads as `handoff: yes`: the status line renders on every turn, so its silence means the measurement stopped, not that the context stopped growing, and handing over costs one fresh session while growing on costs the run. Outside a Herdr pane — a session started by hand, a worker inside the Docker sandbox — no status line runs at all, and the checkpoint answers `unavailable` instead: there the absence is expected and says nothing.

Under all of it, a claimed worker session sets `autoCompactWindow: 200000`, so a session nobody hands over compacts instead of growing until the model refuses. The claim passes that same number to the status line, which shows the size against the smaller of it and the model's window: on a million-token model 78k is 39 % of what the session really has, and the model's 7 % would read as room it never gets. The value file keeps the model's window as the status line read it, and the checkpoint names it `model_context_window`, so the two percentages cannot be confused.

## Consequences
The maintainer reads every worker's context size from the workspace without entering a pane, and a worker can ask a script rather than guess from its own transcript. Both answers come from the same reading, so they cannot disagree.

The status line runs on every render, in the critical path of the terminal: `statusline.sh` therefore makes one `jq` call, writes the value with a rename so a concurrent read never sees half a file, and prints a line whatever its input is — a field it does not find costs a segment, never the line. Before the session's first response the input carries no numbers, and nothing is written then: a zero would read as an almost empty context.

The value describes a worktree, not a session: a second session started in the same worktree reads the previous one's size until its own first render overwrites it, a few seconds of a handoff during which the verdict is the old session's. The verdict is a report here, so the cost is bounded; the stage that acts on it ([#37](https://github.com/CalvinDittkrist/workflows/issues/37)) is where a session identity in the file would earn its keep. The status line also renders only when the message list changes, so `refreshInterval: 60` keeps the value fresh while one long tool call runs — without it a gate run would age the value past the limit and report a handoff for a context that never grew.

The value file is per worktree, like every other worker record, so two workers never read each other's size, and it disappears with the worktree. The cost of it being wrong is bounded by what reads it: this decision only reports, and the 200k window is the safety net under the report.

The command is this plugin's script by absolute host path. A worker session has the orchestrator plugin disabled, which hides its skills and agents, not its files, so the path resolves; inside the Docker sandbox it does not exist, so that pane carries no context line and the checkpoint in it answers `unavailable` — the sandbox's isolation, paid for in visibility.
