# 0001. Distribute as a Claude Code plugin marketplace

Date: 2026-09-17
Status: accepted

## Context
- Skills, agents and hooks must be installable per repository, versioned and updatable.
- They must work in Docker sandboxes and for other agent CLIs.
- Options: a custom npx installer, a git submodule, copied files, or the native plugin marketplace of Claude Code.

## Decision
This repository is a Claude Code marketplace (`.claude-plugin/marketplace.json`) with one plugin per role under `plugins/`, and skills keep the standard Agent Skills layout.

## Consequences
- Repositories declare the marketplace and enabled plugins in `.claude/settings.json`; users install with `claude plugin install <name>@workflows`.
- `npx skills add` and `sbx skills add` consume the skills without a Claude Code install.
- No installer to maintain: Claude Code handles caching, versions, updates and scope.
- Releases are git tags per plugin (`claude plugin tag`).
- Plugins must be self-contained, so small helpers are duplicated between them.
- Rejected: a custom npx package. It duplicates what the marketplace does and adds a second update path.
