# 0022. The factory is a second driver over the worker pipeline

Date: 2026-09-21
Status: superseded by [0040](0040-the-factory-owns-the-delivery-lifecycle-in-go.md); reading the outcome from the final report is superseded by [0039](0039-every-session-reports-through-a-structured-result.md)

## Context
- A specified issue that needs no screen still waits for the maintainer to claim it into a Herdr session.
- A prototype (branch `prototype/factory-headless-ui`) ran `/worker:work` headless with `claude -p` up to a real pull request.
- The driver needs a static binary for a Raspberry Pi and cannot source `lib.sh`.
- [ADR 0002](0002-scripts-do-agents-decide.md) says deterministic work is a program.

## Decision
The factory is a Go service in the directory `factory`, not a plugin, driving the unchanged worker pipeline beside the orchestrator's local claim.

## Consequences
- It starts the worker in print mode with stream output, in a process group of its own.
- It reads everything from the stream: stages, subagent events, cost, and the outcome from a final report naming `ready:` or `blocked:`.
- It restates the branch contract ([ADR 0003](0003-herdr-worktree-per-issue.md)), the base branch rule and the frontier rule, each bound by a drift test like the label vocabulary.
- A fix to a worker skill reaches the factory when the host updates the plugin.
- A change to the stream shape reads runs as `failed`; fake mode replays the recorded shape.
- Go adds a toolchain and its part of the gate ([ADR 0008](0008-make-check-is-the-single-gate.md)).
- Rejected: another plugin driven by an agent session, which puts the host's decisions into a context window.
