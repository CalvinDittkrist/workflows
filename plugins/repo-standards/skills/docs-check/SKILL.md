---
name: docs-check
description: Check the repository against the repository standard (README, AGENTS.md, CLAUDE.md import, make check and the CI job check, architecture.md, ADRs, PR template, settings, licence and security policy when public, stray agent configuration, AI reviewer actions) and report what is missing.
---
Run `"${CLAUDE_PLUGIN_ROOT}/scripts/check.sh"` and relay its lines. For each `fail:` propose the one command or edit that fixes it. Do not fix anything unless the user asks.
