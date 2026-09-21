# 0027. The factory's isolation boundary is the host

Date: 2026-09-21
Status: accepted
Extends: [0005](0005-sandboxing-strategy.md) (the layered strategy gains the unattended layer)

## Context
[ADR 0005](0005-sandboxing-strategy.md) layers isolation for workers on a developer's machine: the worktree, the permission prompts, and an optional Docker sandbox per worktree for a worker that should not touch the host.

An unattended worker breaks the middle layer. Nobody answers a prompt, so it runs with the auto permission mode and every `gh` write, every push and every command goes through unasked. The prototype's live run also showed what the first layer does not cover: the factory had inherited `HERDR_ENV` from the shell it was started in, and the worker used it to run a planner session in the maintainer's own Herdr workspace.

A container per run on the host was the obvious answer. On a Raspberry Pi it costs an image per repository, a second copy of the toolchain and a slower gate, and it would protect a machine that holds nothing worth protecting.

## Decision
The factory's isolation boundary is the host. A dedicated machine runs it, with:

- a GitHub machine user of its own, whose token reaches the connected repositories and nothing else,
- no Claude Code login, no SSH key and no credential of the maintainer's beyond the one subscription the worker runs on,
- workers started with a cleaned environment that carries no Herdr variables, so an issue that needs Herdr ends `blocked` instead of acting in somebody's session,
- each worker in a process group of its own, ended with its group when the run ends, on a deadline or on a stop, so no process of the group outlives its run,
- no container per run.

Workers run with the auto permission mode. That is a consequence of being unattended, not a decision of its own: the prompts have no one to ask.

## Consequences
The blast radius of a worker that goes wrong is the host and the repositories the token names: it cannot reach a developer's machine, a private repository nobody connected, or a Herdr session.

The process group is not a cage. A process that takes a session of its own, as a server started with `nohup` does, is outside the group, and without a container the factory cannot end it. Such a process must not be able to stop the factory, which runs one worker at a time: the factory stops reading the worker's output a moment after the group is gone, ends the run by what the worker reported, and puts a warning on the run that names what was left on the host.

On the host itself a worker is unconstrained. It runs arbitrary commands as the host user under the auto permission mode, so the host is treated as compromised by design — nothing is kept on it that is not already on GitHub, and its token is scoped so that a compromise stays inside what it was going to work on anyway.

The machine user makes the factory's work legible on GitHub: its claims, commits and pull requests are its own, and the maintainer can review a pull request they did not author.

The layered strategy of [ADR 0005](0005-sandboxing-strategy.md) keeps its shape, with a third case: the local worktree, the opt-in container, and now the dedicated host. A run that truly needs to be contained more than the host contains it is a run for a developer's machine with a sandbox.
