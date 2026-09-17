---
name: gh-axi
description: Operate GitHub through gh-axi (issues, PRs, checks, runs, releases, labels, secrets) with token-efficient TOON output. Use for any GitHub task the workflow scripts do not already cover.
user-invocable: false
---
gh-axi wraps `gh` for agents: compact output, next-step hints, structured errors. Prefer it over raw `gh` and over GitHub MCP tools.

Run it as `gh-axi` when installed, otherwise `npx -y gh-axi`. The CLI is the reference: `npx -y gh-axi --help` and `npx -y gh-axi <command> --help` show current commands and flags. Multi-line bodies go through `--body-file <path>`. Long CI logs are truncated to the tail with a `full_log` path to grep.
