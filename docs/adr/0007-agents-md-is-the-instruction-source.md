# 0007. AGENTS.md is the instruction source and CLAUDE.md imports it

Date: 2026-09-18
Status: accepted

## Context
- Several agent tools work on the repositories. Claude Code reads `CLAUDE.md`, not `AGENTS.md`; most other tools read `AGENTS.md`.
- Existing repositories keep instructions in both, in tool-specific folders and in rule files, and the copies drift apart.
- Spec: #3.

## Decision
`AGENTS.md` holds the project instructions, and `CLAUDE.md` contains the import line `@AGENTS.md` plus at most a short Claude-only section.

## Consequences
- `AGENTS.md` stays under 200 lines.
- A monorepo may keep one such pair per area.
- `check.sh` fails when `AGENTS.md` is missing or `CLAUDE.md` does not import it, and warns over 200 lines.
- One file to edit, and every tool reads the same text.
- Claude-specific notes stay small and visible.
- Rejected: `CLAUDE.md` as the source with `AGENTS.md` a symlink. Symlinks break on some checkouts and hide which file is authoritative.
