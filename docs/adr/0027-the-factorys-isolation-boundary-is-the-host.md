# 0027. The factory's isolation boundary is the host

Date: 2026-09-21
Status: accepted
Extends: [0005](0005-sandboxing-strategy.md) (the layered strategy gains the unattended layer)

## Context
- [ADR 0005](0005-sandboxing-strategy.md) layers isolation on a developer's machine: the worktree, the permission prompts, an optional Docker sandbox.
- Unattended, every command goes through unasked.
- The prototype's worker used an inherited `HERDR_ENV` to start a session in the maintainer's Herdr workspace.

## Decision
The factory's isolation boundary is a dedicated host, with a machine user, a cleaned environment, a process group per worker and no container per run.

## Consequences
- The machine user's token reaches the connected repositories and nothing else.
- The host holds no Claude Code login, SSH key or maintainer credential beyond the worker's subscription.
- Workers start without Herdr variables, so an issue that needs Herdr ends `blocked`.
- A worker's process group ends with its run. A process that leaves the group, as with `nohup`, gets a warning on the run.
- Workers run with the auto permission mode. The host is treated as compromised and keeps nothing not already on GitHub.
- The blast radius is the host and the token's repositories, never a developer's machine or a Herdr session.
- The machine user's commits and pull requests are its own, so the maintainer can review them.
- The layers of ADR 0005 gain a third case, the dedicated host. A run needing more containment belongs in a sandbox.
- Rejected: a container per run. On a Raspberry Pi it costs an image per repository, a second toolchain and a slower gate.
