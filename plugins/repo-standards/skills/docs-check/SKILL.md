---
name: docs-check
description: Check the repository against the repository standard (AGENTS.md, CLAUDE.md import, make check, architecture.md, ADRs, PR template, settings, stray agent configuration) and report what is missing.
---
Run `"${CLAUDE_PLUGIN_ROOT}/scripts/check.sh"` and relay its lines. For each `fail:` propose the one command or edit that fixes it. Do not fix anything unless the user asks.
