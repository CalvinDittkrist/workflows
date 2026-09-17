#!/usr/bin/env bash
# Claim a GitHub issue: create a worktree + Herdr workspace and start a worker session in it.
# Usage: claim.sh <issue> [--yolo] [--sandbox] [--base <branch>]
set -euo pipefail
. "$(dirname "$0")/lib.sh"

issue="" mode="manual" sandbox=0 base=""
while [ $# -gt 0 ]; do
  case "$1" in
    --yolo) mode="yolo" ;;
    --sandbox) sandbox=1 ;;
    --base) shift; base="${1:-}" ;;
    -h|--help) sed -n '2,3p' "$0"; exit 0 ;;
    -*) wf_die "unknown flag $1" ;;
    *) issue="${1#\#}" ;;
  esac
  shift
done
[ -n "$issue" ] || wf_die "usage: claim.sh <issue> [--yolo] [--sandbox] [--base <branch>]"
printf '%s' "$issue" | grep -Eq '^[0-9]+$' || wf_die "issue must be a number, got '$issue'"
[ "${HERDR_ENV:-}" = 1 ] || wf_die "claim needs a Herdr-managed pane (HERDR_ENV=1). Start the orchestrator inside Herdr."
wf_need gh; wf_need jq; wf_need herdr; wf_need git
[ "$sandbox" = 1 ] && wf_need sbx

root=$(wf_main_root); cd "$root"
[ -n "$base" ] || base=$(wf_base_branch)

json=$(gh issue view "$issue" --json number,title,state,labels,url 2>/dev/null) || wf_die "issue #$issue not found in $(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || echo 'this repo')"
state=$(printf '%s' "$json" | jq -r .state)
[ "$state" = "OPEN" ] && : || wf_die "issue #$issue is $state, not OPEN"
title=$(printf '%s' "$json" | jq -r .title)
labels=$(printf '%s' "$json" | jq -r '[.labels[].name] | join(",")')
branch="$(wf_branch_type "$labels")/$issue-$(wf_slug "$title")"

# Reuse an existing worktree for this branch instead of creating a second one.
existing=$(wf_worktree_path_for_branch "$branch")
if [ -n "$existing" ]; then
  ws=$(wf_workspace_for_path "$existing")
  wf_kv issue "#$issue"; wf_kv branch "$branch"; wf_kv path "$existing"; wf_kv workspace "${ws:-none}"
  wf_kv status "already-claimed"
  wf_kv next "Talk to the worker in workspace ${ws:-?} or run abandon.sh $issue to drop it."
  exit 0
fi

git fetch -q origin "$base" 2>/dev/null || wf_warn "could not fetch origin/$base; branching from local $base"
baseref="origin/$base"; git rev-parse -q --verify "$baseref" >/dev/null 2>&1 || baseref="$base"

# Worktrees live inside the trusted repository (Claude's own convention), so no trust dialog blocks the worker.
wtdir="$root/.claude/worktrees"; mkdir -p "$wtdir"
grep -qx '.claude/worktrees/' "$root/.git/info/exclude" 2>/dev/null || printf '.claude/worktrees/\n' >> "$root/.git/info/exclude"
wtpath="$wtdir/$(printf '%s' "$branch" | tr '/' '-')"
created=$(wf_run herdr worktree create --cwd "$root" --branch "$branch" --base "$baseref" --path "$wtpath" --label "#$issue $(wf_slug "$title" | cut -c1-24)" --no-focus)
if [ "${WF_DRY_RUN:-0}" = 1 ]; then
  wf_kv issue "#$issue"; wf_kv branch "$branch"; wf_kv base "$baseref"; wf_kv mode "$mode"; wf_kv sandbox "$sandbox"
  wf_kv status "dry-run"; exit 0
fi
ws=$(printf '%s' "$created" | jq -r '.result.workspace.workspace_id // empty')
pane=$(printf '%s' "$created" | jq -r '.result.root_pane.pane_id // empty')
path=$(printf '%s' "$created" | jq -r '.result.worktree.path // empty')
[ -n "$ws" ] && [ -n "$pane" ] && [ -n "$path" ] || wf_die "unexpected herdr response: $created"

# Session-scoped configuration travels through --settings so hooks and skills can read it from the environment.
settings=$(jq -cn --arg m "$mode" --arg i "$issue" '{env:{WF_MODE:$m, WF_ISSUE:$i}}')
perm="${WF_WORKER_PERMISSION_MODE:-auto}"
name="issue-$issue"
# WF_CLAUDE_ARGS: extra claude flags for every worker (e.g. "--model sonnet" or "--plugin-dir /path" while developing).
extra="${WF_CLAUDE_ARGS:-}"
if [ "$sandbox" = 1 ]; then
  herdr pane run "$pane" "$(dirname "$0")/sbx-worker.sh '$path' -- --agent worker --permission-mode $perm --settings '$settings' --name '#$issue' $extra '/worker:work'" >/dev/null
  herdr agent wait "$pane" --until idle --until blocked --timeout 300000 >/dev/null || wf_warn "worker did not become ready within 5 minutes; inspect pane $pane"
else
  herdr agent start "$name" --kind claude --pane "$pane" --timeout 120000 -- --agent worker --permission-mode "$perm" --settings "$settings" --name "#$issue" $extra "/worker:work" >/dev/null \
    || wf_warn "agent start reported not-ready; inspect pane $pane"
fi

# agent start moves focus to the new pane; give it back to the orchestrator.
[ -n "${HERDR_WORKSPACE_ID:-}" ] && herdr workspace focus "$HERDR_WORKSPACE_ID" >/dev/null 2>&1 || true

wf_kv issue "#$issue $title"
wf_kv branch "$branch"
wf_kv path "$path"
wf_kv workspace "$ws"
wf_kv pane "$pane"
wf_kv agent "$name"
wf_kv mode "$mode"
[ "$sandbox" = 1 ] && wf_kv sandbox "docker"
wf_kv next "board.sh shows progress; merge.sh <pr> when the PR is ready${mode:+ (yolo merges itself)}"
