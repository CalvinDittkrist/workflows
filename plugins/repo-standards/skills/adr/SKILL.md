---
name: adr
description: Record an architecture decision as the next numbered ADR in docs/adr. Use when a change alters a boundary, dependency, or convention.
argument-hint: <decision title>
---
1. Run `"${CLAUDE_PLUGIN_ROOT}/scripts/new-adr.sh" $ARGUMENTS`.
2. Fill the created file: Context (forces, links), Decision (one decision, present tense), Consequences (including the rejected alternative). At most 250 words, headings included. Set `Status: accepted` only if the user confirmed the decision; otherwise leave `proposed`.
3. If the decision changes `docs/architecture.md`, update that too.
