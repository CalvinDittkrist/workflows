#!/usr/bin/env bash
# SessionStart hook: print the gh-axi dashboard (repo, open issues, open PRs) as session context.
# Runs only on startup, only in repositories with a GitHub remote. Silent on any failure.
set -uo pipefail
input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
[ -z "$(printf '%s' "$input" | jq -r '.agent_id // empty')" ] || exit 0
[ "$(printf '%s' "$input" | jq -r '.source // "startup"')" = "startup" ] || exit 0
cwd=$(printf '%s' "$input" | jq -r '.cwd // empty'); if [ -n "$cwd" ]; then cd "$cwd" 2>/dev/null || exit 0; fi
git remote get-url origin 2>/dev/null | grep -q github || exit 0
if command -v gh-axi >/dev/null 2>&1; then bin="gh-axi"; elif command -v npx >/dev/null 2>&1; then bin="npx -y gh-axi"; else exit 0; fi
out=$($bin 2>/dev/null) || exit 0
[ -n "$out" ] || exit 0
printf 'GitHub state (gh-axi dashboard). Use `%s` for GitHub operations; it returns compact TOON.\n%s\n' "$bin" "$out" | grep -v '^bin: '
