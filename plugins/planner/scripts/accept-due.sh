#!/usr/bin/env bash
# One line for the planner's driver: whether the issue this session started on is a spec whose tickets are
# all closed, so it can recommend the acceptance. Silent for anything else, and for every read that fails:
# the driver may not guess an acceptance that is due.
# Usage: accept-due.sh [<spec>]   (the session's issue by default)
set -uo pipefail
. "$(dirname "$0")/lib.sh"
issue="${1:-$(wf_plan_issue)}"
printf '%s' "${issue#\#}" | grep -Eq '^[0-9]+$' || exit 0
issue="${issue#\#}"
command -v gh >/dev/null 2>&1 && command -v jq >/dev/null 2>&1 || exit 0
nwo=$(wf_repo_nwo 2>/dev/null) || exit 0
sj=$(gh api "repos/$nwo/issues/$issue" 2>/dev/null) || exit 0
[ "$(printf '%s' "$sj" | jq -r .state)" = open ] || exit 0
printf '%s' "$sj" | jq -e '[.labels[]?.name] | index("spec")' >/dev/null 2>&1 || exit 0
subs=$(wf_sub_issues "$nwo" "$issue") || exit 0
total=$(printf '%s' "$subs" | jq -r 'length')
open=$(printf '%s' "$subs" | jq -r '[.[]? | select(.state == "open")] | length')
if [ "$total" = 0 ]; then
  wf_kv acceptance "#$issue is a spec without native sub-issues; /planner:accept takes the ticket numbers"
elif [ "$open" = 0 ]; then
  wf_kv acceptance "#$issue is a spec with $total ticket(s), all closed; run /planner:accept"
else
  wf_kv acceptance "#$issue is a spec with $open of $total ticket(s) open; /planner:accept refuses until they close"
fi
