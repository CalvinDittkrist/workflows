# 0001. Distribute as a Claude Code plugin marketplace

Date: 2026-09-17
Status: accepted

## Context
Skills, agents and hooks must be installable per repository, versioned, updatable, and usable in Docker sandboxes and by other agent CLIs. Options: a custom npx installer, a git submodule, copying files, or Claude Code's native plugin marketplace.

## Decision
This repository is a Claude Code marketplace (`.claude-plugin/marketplace.json`) with one plugin per role under `plugins/`. Repositories declare the marketplace and enabled plugins in `.claude/settings.json`; users install with `claude plugin install <name>@workflows`. Skills stay in the standard Agent Skills layout so `npx skills add` and `sbx skills add` can consume them without a Claude Code install.

## Consequences
No installer to maintain; Claude Code handles caching, versions, updates and scope. Releases are git tags per plugin (`claude plugin tag`). Plugins must be self-contained, so small helpers are duplicated between them. A custom npx package was rejected because it would duplicate what the marketplace already does and add a second update path.
