#!/usr/bin/env bash
# Check a repository against the docs standard. Exit 1 on any failure; warnings do not fail.
# Usage: check.sh [<repo-root>]
set -uo pipefail
root="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
fail=0
ok()   { printf 'ok: %s\n' "$*"; }
bad()  { printf 'fail: %s\n' "$*"; fail=1; }
warn() { printf 'warn: %s\n' "$*"; }
lines() { wc -l < "$1" | tr -d ' '; }
if [ -f "$root/CLAUDE.md" ]; then
  ok "CLAUDE.md"
  [ "$(lines "$root/CLAUDE.md")" -gt 200 ] && warn "CLAUDE.md has $(lines "$root/CLAUDE.md") lines (>200); trim it"
  grep -q '<fill in>' "$root/CLAUDE.md" && warn "CLAUDE.md still has <fill in> placeholders"
else bad "CLAUDE.md missing"; fi
if [ -f "$root/docs/architecture.md" ]; then
  if [ "$(lines "$root/docs/architecture.md")" -ge 15 ]; then ok "docs/architecture.md"; else bad "docs/architecture.md is a stub ($(lines "$root/docs/architecture.md") lines)"; fi
else bad "docs/architecture.md missing"; fi
if [ -d "$root/docs/adr" ]; then
  if [ -f "$root/docs/adr/README.md" ]; then ok "docs/adr/README.md"; else bad "docs/adr/README.md missing"; fi
  nums=$(printf '%s\n' "$root"/docs/adr/[0-9][0-9][0-9][0-9]-*.md | sed -nE 's#.*/([0-9]{4})-.*\.md$#\1#p' | sort)
  if [ -n "$nums" ]; then ok "ADRs: $(printf '%s\n' "$nums" | wc -l | tr -d ' ')"; else warn "no ADRs yet; record the first decision with /repo-standards:adr"; fi
  dups=$(printf '%s\n' "$nums" | uniq -d); [ -z "$dups" ] || bad "duplicate ADR numbers: $(printf '%s' "$dups" | tr '\n' ' ')"
  for f in "$root"/docs/adr/[0-9][0-9][0-9][0-9]-*.md; do
    [ -e "$f" ] || continue
    grep -Eq '^Status: (proposed|accepted|deprecated|superseded)' "$f" || bad "$(basename "$f") has no Status line"
  done
else bad "docs/adr/ missing"; fi
if [ -f "$root/.github/PULL_REQUEST_TEMPLATE.md" ]; then ok ".github/PULL_REQUEST_TEMPLATE.md"; else warn ".github/PULL_REQUEST_TEMPLATE.md missing"; fi
if [ -f "$root/.claude/settings.json" ]; then
  if command -v jq >/dev/null 2>&1; then
    if jq -e '.enabledPlugins["worker@workflows"] == true' "$root/.claude/settings.json" >/dev/null 2>&1; then ok "worker plugin enabled"; else warn "worker@workflows not enabled in .claude/settings.json"; fi
    if jq -e '(.attribution.commit // "x") == ""' "$root/.claude/settings.json" >/dev/null 2>&1; then ok "commit attribution off"; else warn "attribution.commit not empty; agent co-author lines will be added"; fi
  fi
else warn ".claude/settings.json missing (workflow plugins not configured)"; fi
if [ "$fail" = 0 ]; then printf 'result: pass\n'; else printf 'result: fail\n'; fi
exit $fail
