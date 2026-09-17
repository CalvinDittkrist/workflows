#!/usr/bin/env bash
# Show every claimed worktree with agent state, PR and checks.
set -euo pipefail
. "$(dirname "$0")/lib.sh"
wf_need git; wf_need jq
root=$(wf_main_root); cd "$root"

workspaces="[]"; agents="[]"
if [ "${HERDR_ENV:-}" = 1 ] && command -v herdr >/dev/null 2>&1; then
  workspaces=$(herdr workspace list 2>/dev/null | jq -c '.result.workspaces // []' || echo '[]')
  agents=$(herdr agent list 2>/dev/null | jq -c '.result.agents // []' || echo '[]')
fi
prs="[]"
if command -v gh >/dev/null 2>&1; then
  prs=$(gh pr list --state open --limit 100 --json number,headRefName,isDraft,url,statusCheckRollup,reviewDecision,mergeStateStatus 2>/dev/null || echo '[]')
fi

rows=""
count=0
while IFS= read -r line; do
  case "$line" in worktree\ *) path="${line#worktree }";; branch\ *) branch="${line#branch refs/heads/}"
    [ "$path" = "$root" ] && continue
    issue=$(wf_issue_from_branch "$branch"); [ -n "$issue" ] || continue
    ws=$(printf '%s' "$workspaces" | jq -r --arg p "$path" '.[] | select(.worktree.checkout_path == $p) | .workspace_id' | head -n1)
    agent=$(printf '%s' "$agents" | jq -r --arg p "$path" '[.[] | select(.cwd == $p)] | first | .agent_status // empty')
    prj=$(printf '%s' "$prs" | jq -c --arg b "$branch" '[.[] | select(.headRefName == $b)] | first // empty')
    if [ -n "$prj" ]; then
      prn=$(printf '%s' "$prj" | jq -r '"#\(.number)" + (if .isDraft then " draft" else "" end)')
      checks=$(printf '%s' "$prj" | jq -r '
        [.statusCheckRollup[]? | (.conclusion // .state // "PENDING")] as $c
        | if ($c|length)==0 then "none"
          elif ([$c[] | select(. == "FAILURE" or . == "ERROR" or . == "CANCELLED" or . == "TIMED_OUT")] | length) > 0 then "fail"
          elif ([$c[] | select(. == "PENDING" or . == "EXPECTED" or . == "QUEUED" or . == "IN_PROGRESS")] | length) > 0 then "pending"
          else "pass" end')
      review=$(printf '%s' "$prj" | jq -r '.reviewDecision // "" | ascii_downcase | if . == "" then "-" else . end')
    else
      prn="-"; checks="-"; review="-"
    fi
    rows="$rows  $issue,$branch,${agent:-none},$prn,$checks,$review,${ws:--}\n"
    count=$((count+1));;
  esac
done < <(git worktree list --porcelain)

wf_kv repo "$(basename "$root") ($(git rev-parse --abbrev-ref HEAD))"
printf 'worktrees[%s]{issue,branch,agent,pr,checks,review,workspace}:\n' "$count"
printf "%b" "$rows"
if [ "$count" = 0 ]; then printf 'help: nothing claimed. Run claim.sh <issue>.\n'; fi
