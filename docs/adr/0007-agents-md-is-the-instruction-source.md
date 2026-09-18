# 0007. AGENTS.md is the instruction source and CLAUDE.md imports it

Date: 2026-09-18
Status: accepted

## Context
Repositories are worked on by several agent tools. Claude Code reads `CLAUDE.md` and not `AGENTS.md`; most other tools read `AGENTS.md`. Existing repositories keep instructions in both, in tool-specific folders and in rule files, and the copies drift apart. Spec: #3.

## Decision
`AGENTS.md` holds the project instructions, under 200 lines. `CLAUDE.md` contains the line `@AGENTS.md`, the documented import, and at most a short section that only applies to Claude Code. A monorepo may keep one such pair per area. `check.sh` fails when `AGENTS.md` is missing or `CLAUDE.md` does not import it, and warns over 200 lines.

## Consequences
One file to edit, and every tool reads the same text. Claude-specific notes stay small and visible. Keeping `CLAUDE.md` as the source and symlinking `AGENTS.md` was rejected: symlinks break on some checkouts and hide which file is authoritative.
