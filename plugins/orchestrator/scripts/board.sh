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
    issue=$(wf_issue_from_branch "$branch")
    case "$branch" in plan/*) issue="plan";; esac
    [ -n "$issue" ] || continue
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
if [ "$count" = 0 ]; then printf 'help: nothing claimed. Run claim.sh <issue> or plan.sh <idea>.\n'; fi

# Frontier: agent-ready issues nobody works on and nothing blocks. Needs the REST view for the dependency summary.
nwo=$(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || true)
if [ -n "$nwo" ]; then
  claimed=$(git worktree list --porcelain | sed -nE 's#^branch refs/heads/##p' | while read -r b; do wf_issue_from_branch "$b"; done | jq -R -s -c 'split("\n") | map(select(. != "") | tonumber)')
  ready=$(gh api "repos/$nwo/issues?labels=ready-for-agent&state=open&per_page=100" 2>/dev/null || echo '[]')
  printf '%s' "$ready" | jq -r --argjson claimed "$claimed" '
    [.[] | select(.pull_request == null)] as $all
    | [$all[] | select((.assignees|length) == 0 and ((.issue_dependencies_summary.blocked_by // 0) == 0) and (.number as $n | $claimed | index($n) | not))] as $free
    | "frontier[\($free|length)]{issue,milestone,title}:",
      ($free[] | "  \(.number),\(.milestone.title // "-"),\(.title)"),
      (if ($all|length) > ($free|length) then "waiting: \(($all|length) - ($free|length)) ready-for-agent issue(s) blocked, assigned or claimed" else empty end)'

  # Ready for acceptance: open specs with native sub-issues, all of them closed. Derived per run, no state.
  specs=$(gh api "repos/$nwo/issues?labels=spec&state=open&per_page=100" 2>/dev/null || echo '[]')
  acc_rows=""; acc_count=0; acc_first=""
  while IFS= read -r spec; do
    [ -n "$spec" ] || continue
    subs=$(gh api "repos/$nwo/issues/$spec/sub_issues?per_page=100" 2>/dev/null || echo '[]')
    tally=$(printf '%s' "$subs" | jq -r 'if type == "array" then "\(length) \([.[] | select(.state == "open")] | length)" else "0 0" end' 2>/dev/null || echo '0 0')
    [ "${tally% *}" -gt 0 ] && [ "${tally#* }" -eq 0 ] || continue
    acc_rows="$acc_rows  $(printf '%s' "$specs" | jq -r --argjson n "$spec" '.[] | select(.number == $n) | "\(.number),\(.milestone.title // "-"),\(.title)"')\n"
    acc_count=$((acc_count+1)); [ -n "$acc_first" ] || acc_first="$spec"
  done < <(printf '%s' "$specs" | jq -r '.[] | select(.pull_request == null) | .number')
  printf 'acceptance[%s]{issue,milestone,title}:\n' "$acc_count"
  printf "%b" "$acc_rows"
  if [ "$acc_count" != 0 ]; then printf 'help: every ticket is closed; accept the spec in a planning session, e.g. /orchestrator:plan #%s.\n' "$acc_first"; fi
fi
