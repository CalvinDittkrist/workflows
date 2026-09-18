#!/usr/bin/env bash
# Check a repository against the standard (docs/repo-standard.md). Exit 1 on any failure; warnings do not fail.
# Usage: check.sh [<repo-root>]
set -uo pipefail
root="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
fail=0
ok()   { printf 'ok: %s\n' "$*"; }
bad()  { printf 'fail: %s\n' "$*"; fail=1; }
warn() { printf 'warn: %s\n' "$*"; }
lines() { wc -l < "$1" | tr -d ' '; }
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

# Every file of the repository, relative to root: tracked plus untracked-but-not-ignored, so local
# ignored files (settings.local.json, worktrees) never count. Outside git, every file but .git.
files() {
  if git -C "$root" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    git -C "$root" -c core.quotePath=false ls-files --cached --others --exclude-standard
  else
    (cd "$root" && find . -name .git -prune -o -type f -print | sed 's#^\./##')
  fi
}
all=$(files | sort -u)

# Instruction files: AGENTS.md is the source, CLAUDE.md next to it imports it. Root always; one pair
# per area in a monorepo. Hidden directories are left to the agent configuration rule below.
pair_dirs=$( { printf '.\n'; printf '%s\n' "$all" | grep -E '(^|/)(AGENTS|CLAUDE)\.md$' | grep -Ev '(^|/)\.' | sed -E 's#(^|/)[^/]+$##; s#^$#.#'; } | sort -u)
while IFS= read -r d; do
  p=""; [ "$d" = . ] || p="$d/"
  if has "$root/$d" AGENTS.md; then ok "${p}AGENTS.md"; else bad "${p}AGENTS.md missing; it is the instruction source, move the project instructions there"; fi
  if ! has "$root/$d" CLAUDE.md; then bad "${p}CLAUDE.md missing; create it with the line @AGENTS.md"
  elif grep -Eq '^[[:space:]]*@(\./)?AGENTS\.md[[:space:]]*$' "$root/${p}CLAUDE.md"; then ok "${p}CLAUDE.md imports AGENTS.md"
  else bad "${p}CLAUDE.md does not import AGENTS.md; add the line @AGENTS.md"; fi
  for f in AGENTS.md CLAUDE.md; do
    has "$root/$d" "$f" || continue
    n=$(lines "$root/$p$f"); [ "$n" -gt 200 ] && warn "$p$f has $n lines (>200); trim it"
    grep -q '<fill in>' "$root/$p$f" && warn "$p$f still has <fill in> placeholders"
  done
done <<EOF
$pair_dirs
EOF

# The gate: make check runs everything CI gates on.
mk=$(first_of "$root" GNUmakefile makefile Makefile)
if [ -n "$mk" ] && grep -Eq '^([^:#=[:space:]][^:#=]*[[:space:]])?check([[:space:]][^:#=]*)?::?([^=]|$)' "$root/$mk"; then
  ok "$mk has a check target"
  grep -q '<fill in>' "$root/$mk" && warn "$mk still has <fill in> placeholders"
else bad "no check target in a Makefile; add one that runs everything CI gates on (make check is the gate)"; fi

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
pr=$(first_of "$root/.github" PULL_REQUEST_TEMPLATE.md pull_request_template.md)
if [ -n "$pr" ]; then ok ".github/$pr"; else warn ".github/PULL_REQUEST_TEMPLATE.md missing"; fi
if [ -f "$root/.claude/settings.json" ]; then
  if command -v jq >/dev/null 2>&1; then
    if jq -e '.enabledPlugins["worker@workflows"] == true' "$root/.claude/settings.json" >/dev/null 2>&1; then ok "worker plugin enabled"; else warn "worker@workflows not enabled in .claude/settings.json"; fi
    if jq -e '(.attribution.commit // "x") == ""' "$root/.claude/settings.json" >/dev/null 2>&1; then ok "commit attribution off"; else warn "attribution.commit not empty; agent co-author lines will be added"; fi
  fi
else warn ".claude/settings.json missing (workflow plugins not configured)"; fi

# Agent configuration the standard does not define: anything in .claude/ but the settings files and
# worktrees, configuration of other agent tools, skill lock files. One line per path, cut to three
# levels below the offending folder (.claude/skills/<name>), so each skill or command is named once.
extra=$(printf '%s\n' "$all" | awk -F/ '
  function name(i,   j, n, p) { n = i + 2; if (n > NF) n = NF; p = $1; for (j = 2; j <= n; j++) p = p "/" $j; sub(/\/$/, "", p); print p }
  $0 == "" { next }
  {
    for (i = 1; i <= NF; i++) {
      c = $i
      if (c == ".claude" && i < NF) {
        if ($(i+1) == "settings.json" || $(i+1) == "settings.local.json" || $(i+1) == "worktrees") next
        name(i); next
      }
      if (c ~ /^\.aider/ || c ~ /^(\.agents|\.amazonq|\.augment|\.clinerules|\.codex|\.continue|\.cursor|\.cursorignore|\.cursorindexingignore|\.cursorrules|\.gemini|\.goose|\.goosehints|\.junie|\.kilocode|\.kiro|\.opencode|\.qwen|\.roo|\.roomodes|\.roorules|\.trae|\.windsurf|\.windsurfrules|GEMINI\.md|opencode\.json|skills-lock\.json|\.skill-lock\.json)$/) { name(i); next }
      if (i == 1 && c == ".github" && NF > 1 && $2 ~ /^(copilot-instructions\.md|instructions|prompts|chatmodes|agents)$/) { name(i); next }
    }
  }' | sort -u)
if [ -z "$extra" ]; then ok "no agent configuration outside the standard"
else
  while IFS= read -r p; do bad "$p: agent configuration the standard does not define; remove it"; done <<EOF
$extra
EOF
fi

if [ "$fail" = 0 ]; then printf 'result: pass\n'; else printf 'result: fail\n'; fi
exit $fail
