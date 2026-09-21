#!/usr/bin/env bash
# Claim a GitHub issue: create a worktree + Herdr workspace and start a worker session in it.
# Usage: claim.sh <issue> [--yolo] [--sandbox] [--force] [--base <branch>]
set -euo pipefail
. "$(dirname "$0")/lib.sh"

issue="" mode="manual" sandbox=0 force=0 base=""
while [ $# -gt 0 ]; do
  case "$1" in
    --yolo) mode="yolo" ;;
    --sandbox) sandbox=1 ;;
    --force) force=1 ;;
    --base) shift; base="${1:-}" ;;
    -h|--help) sed -n '2,3p' "$0"; exit 0 ;;
    -*) wf_die "unknown flag $1" ;;
    *) issue="${1#\#}" ;;
  esac
  shift
done
[ -n "$issue" ] || wf_die "usage: claim.sh <issue> [--yolo] [--sandbox] [--force] [--base <branch>]"
printf '%s' "$issue" | grep -Eq '^[0-9]+$' || wf_die "issue must be a number, got '$issue'"
[ "${HERDR_ENV:-}" = 1 ] || wf_die "claim needs a Herdr-managed pane (HERDR_ENV=1). Start the orchestrator inside Herdr."
wf_need gh; wf_need jq; wf_need herdr; wf_need git
wf_check_claude_args WF_WORKER_CLAUDE_ARGS
[ "$sandbox" = 1 ] && wf_need sbx

root=$(wf_main_root); cd "$root"
[ -n "$base" ] || base=$(wf_base_branch)

json=$(gh issue view "$issue" --json number,title,state,labels,url 2>/dev/null) || wf_die "issue #$issue not found in $(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || echo 'this repo')"
state=$(printf '%s' "$json" | jq -r .state)
[ "$state" = "OPEN" ] || wf_die "issue #$issue is $state, not OPEN"
title=$(printf '%s' "$json" | jq -r .title)
labels=$(printf '%s' "$json" | jq -r '[.labels[].name] | join(",")')
branch="$(wf_branch_type "$labels")/$issue-$(wf_slug "$title")"

# Reuse the worktree this issue already has instead of creating a second one. Looked up by issue number, so a
# label change since the claim, which would give the issue a different branch type today, does not hide it.
claimed=$(wf_branch_for_issue "$issue")
if [ -n "$claimed" ]; then
  existing=$(wf_worktree_path_for_branch "$claimed")
  ws=$(wf_workspace_for_path "$existing")
  wf_kv issue "#$issue"; wf_kv branch "$claimed"; wf_kv path "$existing"; wf_kv workspace "${ws:-none}"
  wf_kv status "already-claimed"
  wf_kv next "Talk to the worker in workspace ${ws:-?} or run abandon.sh $issue to drop it."
  exit 0
fi

# Only an agent-ready issue reaches a worker. Without the brief a planner writes, a worker decides the scope
# itself and answers a whole spec with one bulk pull request. Checked after the worktree lookup above, so an
# issue claimed earlier stays reportable whatever its labels are now, and before anything is created.
if ! wf_issue_has_label "$json" ready-for-agent; then
  if [ "$force" = 1 ]; then
    wf_warn "issue #$issue is not ready-for-agent (labels: ${labels:-none}); claiming it anyway because --force was given"
  elif wf_issue_has_label "$json" spec; then
    wf_die "issue #$issue is a spec (labels: $labels), not ready for an agent. Claim its tickets instead, or open a planning session on the spec when all of them are closed: /orchestrator:plan #$issue. --force claims it anyway."
  else
    wf_die "issue #$issue is not ready for an agent (labels: ${labels:-none}). Open a planning session on it to triage it: /orchestrator:plan #$issue. --force claims it anyway."
  fi
fi

# The factory owns a routed issue: it claims the issue on its host and opens the pull request from there.
# Claiming it here as well would mean two workers on one issue and one of the two results thrown away.
if wf_issue_has_label "$json" "$WF_ROUTING_LABEL"; then
  if [ "$force" = 1 ]; then
    wf_warn "issue #$issue is routed to the factory (labels: $labels); claiming it locally anyway because --force was given. The factory may work it at the same time."
  else
    wf_die "issue #$issue is routed to the factory (labels: $labels). Remove the label $WF_ROUTING_LABEL to work on it locally, or claim it anyway with --force."
  fi
fi

# A claim on the remote is the creation of the issue's branch there, so a remote branch of the contract's
# shape belongs to another claimer. Looked up by issue number like the worktree above, because the branch
# type follows the labels and the other claimer may have seen different ones. Only read when there is an
# origin to read; a remote that answers with an error is a warning, so a claim still works offline.
remote_branch=""
if git remote get-url origin >/dev/null 2>&1; then
  remote_branch=$(wf_remote_branch_for_issue "$issue") \
    || wf_warn "could not read the branches of origin; claiming #$issue without checking whether it is claimed there"
fi
if [ -n "$remote_branch" ]; then
  # The branch is the factory's, a second machine's, or one this machine abandoned: abandon.sh removes the
  # worktree and the local branch and leaves the remote one, so the common single-machine case lands here too.
  if [ "$force" = 1 ]; then
    wf_warn "issue #$issue is already claimed on origin by branch $remote_branch; --force adopts that branch, so this worktree continues its work instead of starting from $base."
  else
    wf_die "issue #$issue is already claimed on origin: the branch $remote_branch exists there, left by the factory, by another machine or by a claim of your own you abandoned. Wait for its pull request, delete it with git push origin --delete $remote_branch to start over, or continue its work here with --force, which starts the worktree from it."
  fi
fi

git fetch -q origin "$base" 2>/dev/null || wf_warn "could not fetch origin/$base; branching from local $base"
baseref="origin/$base"; git rev-parse -q --verify "$baseref" >/dev/null 2>&1 || baseref="$base"

# The forced claim of a remotely claimed issue adopts its branch: work the other claimer already pushed is
# continued here instead of being started again from the base.
if [ -n "$remote_branch" ]; then
  git fetch -q origin "+refs/heads/$remote_branch:refs/remotes/origin/$remote_branch" 2>/dev/null \
    || wf_die "could not fetch $remote_branch from origin, so the work on it cannot be continued here"
  branch="$remote_branch"; baseref="origin/$remote_branch"
  # A local branch of that name (an earlier claim whose worktree is gone) would be checked out at its own tip
  # instead of the base, so the worktree would silently not carry the work this claim says it continues.
  stale=$(git rev-parse -q --verify "refs/heads/$branch" || true)
  if [ -n "$stale" ] && [ "$stale" != "$(git rev-parse "$baseref")" ]; then
    wf_die "the local branch $branch exists at $(git rev-parse --short "$stale") and is not what origin has, so the worktree would start from it instead of from the work on origin. Continue that branch by hand, or remove it with git branch -D $branch and claim again."
  fi
fi

wf_create_worktree "$branch" "$baseref" "#$issue $(wf_slug "$title" | cut -c1-24)"
if [ "${WF_DRY_RUN:-0}" = 1 ]; then
  wf_kv issue "#$issue"; wf_kv branch "$branch"; wf_kv base "$baseref"; wf_kv mode "$mode"; wf_kv sandbox "$sandbox"
  wf_kv status "dry-run"; exit 0
fi

# Session-scoped configuration travels through --settings so hooks and skills can read it from the environment.
# The worker session disables the planner and orchestrator plugins so their skills and agents stay out of its context.
# CLAUDE_CODE_DISABLE_BACKGROUND_TASKS keeps subagents in the foreground: the reviewer reports come back as the
# results of the Agent calls, so the worker never spends turns waiting for them (ADR 0017).
# The status line makes the worker's context size visible in its pane and writes it into the worktree, where
# the worker's own checkpoint reads it (ADR 0020). It is this plugin's script by absolute path: the worker
# session has this plugin disabled, which hides its skills and agents, not its files.
# The safety net under that is the compact trigger: a session that is not handed over in time compacts instead
# of growing until the model refuses. Claude Code compacts at a percentage of autoCompactWindow, and that
# percentage is not documented, so the claim pins both numbers here and nowhere else (ADR 0031). The trigger is
# their product, and it is the number the status line is given, so the pane shows the size against the point
# this session really compacts at and not against a window it never reaches. refreshInterval keeps the value
# fresh while one long tool call runs, which changes no message and would otherwise render nothing.
here=$(cd "$(dirname "$0")" && pwd)
compact_window=200000
# CLAUDE_AUTOCOMPACT_PCT_OVERRIDE can only lower the percentage ("values above the default percentage are
# ignored", https://code.claude.com/docs/en/env-vars.md), and 80 is under the default the measured worker
# sessions compacted at, so it is the percentage that applies rather than a request Claude Code drops.
compact_pct=80
compact_trigger=$((compact_window * compact_pct / 100))
# claude runs statusLine.command through a shell, so the path is quoted: a checkout under "/Users/John Smith"
# would otherwise split into words, nothing would render, and the worker's checkpoint would read a missing
# value as a handoff for the rest of the run.
sl="$(wf_shell_quote "$here/statusline.sh") $compact_trigger"
settings=$(jq -cn --arg m "$mode" --arg i "$issue" --arg sl "$sl" \
  --argjson w "$compact_window" --arg p "$compact_pct" \
  '{env:{WF_MODE:$m, WF_ISSUE:$i, CLAUDE_CODE_DISABLE_BACKGROUND_TASKS:"1",
         CLAUDE_AUTOCOMPACT_PCT_OVERRIDE:$p},
    enabledPlugins:{"planner@workflows":false, "orchestrator@workflows":false},
    statusLine:{type:"command", command:$sl, padding:0, refreshInterval:60},
    autoCompactWindow:$w}')
perm="${WF_WORKER_PERMISSION_MODE:-auto}"
name=$(wf_agent_name "issue-$issue")
# WF_CLAUDE_ARGS applies to every session, WF_WORKER_CLAUDE_ARGS to workers only (e.g. "--model sonnet", "--plugin-dir /path" while developing).
extra="${WF_CLAUDE_ARGS:-} ${WF_WORKER_CLAUDE_ARGS:-}"
if [ "$sandbox" = 1 ]; then
  # A command line for the shell in the new pane: every value is quoted for it (the settings JSON carries
  # quotes of its own since the status line moved in), the script by its full path because that pane's
  # working directory is not this script's, and only $extra stays bare, because it is a list of flags.
  herdr pane run "$pane" "$(wf_shell_quote "$here/sbx-worker.sh") $(wf_shell_quote "$path") -- --agent worker --strict-mcp-config --permission-mode $(wf_shell_quote "$perm") --settings $(wf_shell_quote "$settings") --name $(wf_shell_quote "#$issue") $extra '/worker:work'" >/dev/null
  herdr agent wait "$pane" --until idle --until blocked --timeout 300000 >/dev/null || wf_warn "worker did not become ready within 5 minutes; inspect pane $pane"
  wf_wait_agent "$pane"
else
  # shellcheck disable=SC2086  # $extra is a flag list and must word-split
  if ! wf_start_agent "$pane" "$name" --agent worker --strict-mcp-config --permission-mode "$perm" --settings "$settings" --name "#$issue" $extra "/worker:work"; then
    wf_rollback_worktree "$ws" "$path" "$branch"
    wf_die "$start_error Worktree and branch $branch were removed."
  fi
fi

wf_kv issue "#$issue $title"
wf_kv branch "$branch"
wf_kv path "$path"
wf_kv workspace "$ws"
wf_kv pane "$pane"
wf_kv agent "$name"
wf_kv agent_status "${agent_status:-not-detected}"
wf_kv mode "$mode"
[ "$sandbox" = 1 ] && wf_kv sandbox "docker"
if [ "$mode" = yolo ]; then
  wf_kv next "board.sh shows progress; this worker merges its own PR when it is green"
else
  wf_kv next "board.sh shows progress; merge.sh <pr> when the PR is ready"
fi
