#!/usr/bin/env bash
# Print the session facts a planner skill needs as key: value lines.
set -uo pipefail
. "$(dirname "$0")/lib.sh"

# When the session starts on an open spec, whether its tickets are all closed, so the driver can recommend
# the acceptance. Every read is fail-soft: without GitHub the line is left out, never guessed.
acceptance_line() {
  local nwo sj subs total open
  printf '%s' "$1" | grep -Eq '^[0-9]+$' || return 0
  command -v gh >/dev/null 2>&1 && command -v jq >/dev/null 2>&1 || return 0
  nwo=$(wf_repo_nwo 2>/dev/null) || return 0
  sj=$(gh api "repos/$nwo/issues/$1" 2>/dev/null) || return 0
  [ "$(printf '%s' "$sj" | jq -r .state)" = open ] || return 0
  printf '%s' "$sj" | jq -e '[.labels[]?.name] | index("spec")' >/dev/null 2>&1 || return 0
  subs=$(gh api --paginate "repos/$nwo/issues/$1/sub_issues?per_page=100" 2>/dev/null) || return 0
  total=$(printf '%s' "$subs" | jq -s -r '[add // [] | .[]?] | length')
  open=$(printf '%s' "$subs" | jq -s -r '[add // [] | .[]? | select(.state == "open")] | length')
  if [ "$total" = 0 ]; then
    wf_kv acceptance "#$1 is a spec without native sub-issues; /planner:accept takes the ticket numbers"
  elif [ "$open" = 0 ]; then
    wf_kv acceptance "#$1 is a spec with $total ticket(s), all closed; run /planner:accept"
  else
    wf_kv acceptance "#$1 is a spec with $open of $total ticket(s) open; /planner:accept refuses until they close"
  fi
}

slug=$(wf_plan_slug)
wf_kv plan "${slug:-none (not a plan/<slug> worktree)}"
issue=$(wf_plan_issue); topic=$(wf_plan_topic)
[ -n "$issue" ] && { wf_kv issue "#$issue"; acceptance_line "$issue"; }
[ -n "$topic" ] && wf_kv topic "$topic"
wf_kv base "$(wf_base_branch)"
if [ -f docs/glossary.md ]; then wf_kv glossary "docs/glossary.md"; else wf_kv glossary "missing (spec lists new terms)"; fi
if [ -d docs/adr ]; then wf_kv adrs "docs/adr ($(find docs/adr -name '[0-9][0-9][0-9][0-9]-*.md' | wc -l | tr -d ' '))"; else wf_kv adrs "missing"; fi
