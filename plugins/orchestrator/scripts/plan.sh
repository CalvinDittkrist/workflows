#!/usr/bin/env bash
# Open a planning session: worktree plan/<slug> + Herdr workspace running `claude --agent planner`.
# Usage: plan.sh <topic words...> | <#issue> [--base <branch>]
set -euo pipefail
. "$(dirname "$0")/lib.sh"

base="" words=""
while [ $# -gt 0 ]; do
  case "$1" in
    --base) shift; base="${1:-}" ;;
    -h|--help) sed -n '2,3p' "$0"; exit 0 ;;
    -*) wf_die "unknown flag $1" ;;
    *) words="${words:+$words }$1" ;;
  esac
  shift
done
[ -n "$words" ] || wf_die "usage: plan.sh <topic words...> | <#issue> [--base <branch>]"
[ "${HERDR_ENV:-}" = 1 ] || wf_die "plan needs a Herdr-managed pane (HERDR_ENV=1). Start the orchestrator inside Herdr."
wf_need gh; wf_need jq; wf_need herdr; wf_need git

root=$(wf_main_root); cd "$root"
[ -n "$base" ] || base=$(wf_base_branch)

issue="" topic="$words"
if printf '%s' "$words" | grep -Eq '^#?[0-9]+$'; then
  issue="${words#\#}"
  json=$(gh issue view "$issue" --json number,title,state,labels,url 2>/dev/null) || wf_die "issue #$issue not found in $(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || echo 'this repo')"
  state=$(printf '%s' "$json" | jq -r .state)
  [ "$state" = "OPEN" ] || wf_die "issue #$issue is $state, not OPEN"
  topic=$(printf '%s' "$json" | jq -r .title)
fi
slug=$(wf_slug "$topic")
[ -n "$slug" ] || wf_die "could not derive a branch name from '$topic'"
branch="plan/$slug"

existing=$(wf_worktree_path_for_branch "$branch")
if [ -n "$existing" ]; then
  ws=$(wf_workspace_for_path "$existing")
  wf_kv plan "$slug"; wf_kv branch "$branch"; wf_kv path "$existing"; wf_kv workspace "${ws:-none}"
  wf_kv status "already-open"
  wf_kv next "Talk to the planner in workspace ${ws:-?} or run abandon.sh $branch to drop it."
  exit 0
fi

git fetch -q origin "$base" 2>/dev/null || wf_warn "could not fetch origin/$base; branching from local $base"
baseref="origin/$base"; git rev-parse -q --verify "$baseref" >/dev/null 2>&1 || baseref="$base"

wf_create_worktree "$branch" "$baseref" "plan $(printf '%s' "$slug" | cut -c1-24)"
if [ "${WF_DRY_RUN:-0}" = 1 ]; then
  wf_kv plan "$slug"; wf_kv branch "$branch"; wf_kv base "$baseref"; wf_kv status "dry-run"; exit 0
fi

# The topic travels in git's branch description: restart-safe, shared by all worktrees, no file in the tree.
if [ -n "$issue" ]; then git config "branch.$branch.description" "issue: #$issue"; else git config "branch.$branch.description" "topic: $topic"; fi

settings=$(jq -cn --arg s "$slug" --arg i "$issue" '{env:{WF_PLAN:$s}} | if $i != "" then .env.WF_PLAN_ISSUE = $i else . end')
perm="${WF_PLANNER_PERMISSION_MODE:-auto}"
name="plan-$slug"
extra="${WF_CLAUDE_ARGS:-}"
# shellcheck disable=SC2086  # $extra is a flag list and must word-split
wf_start_agent "$pane" "$name" --agent planner --permission-mode "$perm" --settings "$settings" --name "plan $slug" $extra "/planner:plan"

wf_kv plan "$slug"
if [ -n "$issue" ]; then wf_kv issue "#$issue $topic"; else wf_kv topic "$topic"; fi
wf_kv branch "$branch"
wf_kv path "$path"
wf_kv workspace "$ws"
wf_kv pane "$pane"
wf_kv agent "$name"
wf_kv agent_status "${agent_status:-not-detected}"
wf_kv next "answer the planner's questions in its pane; board.sh shows the frontier once tickets exist"
