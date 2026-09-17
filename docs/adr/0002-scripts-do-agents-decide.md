# 0002. Scripts do, agents decide

Date: 2026-09-17
Status: accepted

## Context
Throughput and token cost suffer when a model discovers facts with many tool calls, and behaviour is hard to test when it lives only in prompts. GitHub, git and Herdr interactions are deterministic.

## Decision
Every deterministic step is a bash script in the plugin's `scripts/` directory with a fixed, compact text output (`key: value`, small tables) and `error:` lines that name the fix. Skills are one-screen prompts that call these scripts and interpret the result. Skills use `!`command`` injection to load facts at invocation time. Tests run the real scripts against `gh` and `herdr` shims.

## Consequences
Behaviour is testable without a model, token use per step is fixed, and a script change does not require re-prompting. The cost is two languages (bash and prompts) and a bash 3.2 constraint for macOS. Python scripts were rejected because bash composes more naturally with `gh`, `git` and `herdr` and needs no interpreter inside sandboxes.
