# 0002. Scripts do, agents decide

Date: 2026-09-17
Status: accepted

## Context
- A model that discovers facts with many tool calls costs tokens and throughput.
- Behaviour that lives only in prompts is hard to test.
- GitHub, git and Herdr interactions are deterministic.

## Decision
Every deterministic step is a bash script in the plugin's `scripts/` directory, and skills are one-screen prompts that call these scripts and interpret the result.

## Consequences
- Output is fixed and compact: `key: value` lines and small tables, and `error:` lines name the fix.
- Skills use `!`command`` injection to load facts at invocation time.
- Tests run the real scripts against `gh` and `herdr` shims, so behaviour is testable without a model.
- Token use per step is fixed, and a script change needs no re-prompting.
- The cost is two languages, bash and prompts, and a bash 3.2 constraint for macOS.
- Rejected: Python scripts. Bash composes better with `gh`, `git` and `herdr` and needs no interpreter inside sandboxes.
