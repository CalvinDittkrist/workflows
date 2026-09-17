---
name: docs-check
description: Check the repository against the docs standard (CLAUDE.md, architecture.md, ADRs, PR template, workflow settings) and report what is missing.
---
Run `"${CLAUDE_PLUGIN_ROOT}/scripts/check.sh"` and relay its lines. For each `fail:` propose the one command or edit that fixes it. Do not fix anything unless the user asks.
