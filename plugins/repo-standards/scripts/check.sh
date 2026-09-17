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
[ -f "$root/CLAUDE.md" ] && ok "CLAUDE.md" || bad "CLAUDE.md missing"
[ -f "$root/CLAUDE.md" ] && [ "$(lines "$root/CLAUDE.md")" -gt 200 ] && warn "CLAUDE.md has $(lines "$root/CLAUDE.md") lines (>200); trim it"
grep -q '<fill in>' "$root/CLAUDE.md" 2>/dev/null && warn "CLAUDE.md still has <fill in> placeholders"
if [ -f "$root/docs/architecture.md" ]; then
  [ "$(lines "$root/docs/architecture.md")" -ge 15 ] && ok "docs/architecture.md" || bad "docs/architecture.md is a stub ($(lines "$root/docs/architecture.md") lines)"
else bad "docs/architecture.md missing"; fi
if [ -d "$root/docs/adr" ]; then
  [ -f "$root/docs/adr/README.md" ] && ok "docs/adr/README.md" || bad "docs/adr/README.md missing"
  nums=$(ls "$root/docs/adr" | sed -nE 's/^([0-9]{4})-.*\.md$/\1/p' | sort)
  [ -n "$nums" ] && ok "ADRs: $(printf '%s\n' "$nums" | wc -l | tr -d ' ')" || warn "no ADRs yet; record the first decision with /repo-standards:adr"
  dups=$(printf '%s\n' "$nums" | uniq -d); [ -z "$dups" ] || bad "duplicate ADR numbers: $(printf '%s' "$dups" | tr '\n' ' ')"
  for f in "$root"/docs/adr/[0-9][0-9][0-9][0-9]-*.md; do
    [ -e "$f" ] || continue
    grep -Eq '^Status: (proposed|accepted|deprecated|superseded)' "$f" || bad "$(basename "$f") has no Status line"
  done
else bad "docs/adr/ missing"; fi
[ -f "$root/.github/PULL_REQUEST_TEMPLATE.md" ] && ok ".github/PULL_REQUEST_TEMPLATE.md" || warn ".github/PULL_REQUEST_TEMPLATE.md missing"
if [ -f "$root/.claude/settings.json" ]; then
  if command -v jq >/dev/null 2>&1; then
    jq -e '.enabledPlugins["worker@workflows"] == true' "$root/.claude/settings.json" >/dev/null 2>&1 && ok "worker plugin enabled" || warn "worker@workflows not enabled in .claude/settings.json"
    jq -e '(.attribution.commit // "x") == ""' "$root/.claude/settings.json" >/dev/null 2>&1 && ok "commit attribution off" || warn "attribution.commit not empty; agent co-author lines will be added"
  fi
else warn ".claude/settings.json missing (workflow plugins not configured)"; fi
[ "$fail" = 0 ] && printf 'result: pass\n' || printf 'result: fail\n'
exit $fail
