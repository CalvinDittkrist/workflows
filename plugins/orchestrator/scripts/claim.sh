#!/usr/bin/env bash
# Claim a GitHub issue: create a worktree + Herdr workspace and start a worker session in it.
# Usage: claim.sh <issue> [--yolo] [--sandbox] [--force] [--base <branch>] [--env NAME=VALUE]...
set -euo pipefail
. "$(dirname "$0")/lib.sh"

# The worker knobs a claim may set for the one session it starts. They are the variables the worker plugin's
# scripts read and the README's configuration table documents; a test fails when the two drift apart. The
# session's own three are not here: the claim itself sets WF_MODE and WF_ISSUE, and the base branch has --base.
env_accepted="WF_REVIEWERS WF_REVIEW_ROUNDS WF_CI_REPAIR_ROUNDS WF_PR_BOT_REVIEWERS WF_PR_REVIEW_WAIT WF_HANDOFF_TOKENS WF_CONTEXT_MAX_AGE WF_HANDOFF_SESSION_MS WF_HANDOFF_POLL_SECONDS WF_DOCS_TIMEOUT"
env_shape="--env takes NAME=VALUE, e.g. --env WF_HANDOFF_TOKENS=5000; an empty value (--env WF_PR_BOT_REVIEWERS=) is allowed"

# Validate one --env argument and remember it. Nothing is created yet when this refuses, and the value is
# only ever read as data from here on: it reaches the session through jq, never through a shell.
wf_read_env_arg() {
  local arg="$1" name
  case "$arg" in *=*) ;; *) wf_die "--env $arg has no '='. $env_shape" ;; esac
  name="${arg%%=*}"
  # A name is one word of A-Z, 0-9 and _, and is checked for that before it is looked up: the lookup below
  # asks whether the accepted list contains " $name ", which a name of two words could otherwise span.
  case "$name" in
    "") wf_die "--env $arg has no name. $env_shape" ;;
    *[!A-Z0-9_]*) wf_die "--env $arg has no usable name: a name is A-Z, 0-9 and _. $env_shape" ;;
  esac
  case " $env_accepted " in
    *" $name "*) ;;
    *) wf_die "--env $name is not a worker knob a claim can set. Accepted names: $env_accepted" ;;
  esac
  case " $env_names " in
    *" $name "*) wf_die "--env $name was given twice. Pass it once, with the value you mean" ;;
  esac
  env_names="${env_names:+$env_names }$name"
  env_pairs[${#env_pairs[@]}]="$arg"
}

issue="" mode="manual" sandbox=0 force=0 base="" env_names="" env_pairs=()
while [ $# -gt 0 ]; do
  case "$1" in
    --yolo) mode="yolo" ;;
    --sandbox) sandbox=1 ;;
    --force) force=1 ;;
    --base) [ $# -gt 1 ] || wf_die "--base needs a branch name, e.g. --base dev"; shift; base="$1" ;;
    --env) [ $# -gt 1 ] || wf_die "--env needs an argument. $env_shape"; shift; wf_read_env_arg "$1" ;;
    -h|--help) sed -n '2,3p' "$0"; exit 0 ;;
    -*) wf_die "unknown flag $1" ;;
    *) issue="${1#\#}" ;;
  esac
  shift
done
[ -n "$issue" ] || wf_die "usage: claim.sh <issue> [--yolo] [--sandbox] [--force] [--base <branch>] [--env NAME=VALUE]..."
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
  # This claim starts no session, so it applied nothing: the running one keeps the settings it was started
  # with. Said here rather than left to be read into "already-claimed" as if the values had arrived.
  [ -n "$env_names" ] && wf_kv env "not applied ($env_names): this claim started no session, and the running one keeps the values it was started with"
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
  [ -n "$env_names" ] && wf_kv env "$env_names"
  wf_kv status "dry-run"; exit 0
fi

# The worker knobs of --env ride in the session's env block (wf_worker_settings). Each value enters as a jq
# argument and leaves as JSON: no shell inside the claim reads it, whatever it contains.
env_extra='{}'
for pair in ${env_pairs[@]+"${env_pairs[@]}"}; do
  env_extra=$(printf '%s' "$env_extra" | jq -c --arg n "${pair%%=*}" --arg v "${pair#*=}" '.[$n] = $v')
done
settings=$(wf_worker_settings "$mode" "$issue" "$env_extra")
wf_start_worker "$sandbox" "issue-$issue" "#$issue" "$settings" /worker:work

wf_kv issue "#$issue $title"
wf_kv branch "$branch"
wf_kv path "$path"
wf_kv workspace "$ws"
wf_kv pane "$pane"
wf_kv agent "$agent_name"
wf_kv agent_status "${agent_status:-not-detected}"
wf_kv mode "$mode"
[ -n "$env_names" ] && wf_kv env "$env_names"
[ "$sandbox" = 1 ] && wf_kv sandbox "docker"
if [ "$mode" = yolo ]; then
  wf_kv next "board.sh shows progress; this worker merges its own PR when it is green"
else
  wf_kv next "board.sh shows progress; merge.sh <pr> when the PR is ready"
fi
