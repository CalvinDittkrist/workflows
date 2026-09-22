#!/usr/bin/env bash
# Squash-merge a ready PR, then remove its worktree, Herdr workspace and branch.
# A promotion PR from dev (release.sh) gets a merge commit and keeps dev; a fork PR keeps local branches.
# Usage: merge.sh <pr> [--allow-unstable] [--ignore-threads]
set -euo pipefail
. "$(dirname "$0")/lib.sh"

pr="" allow_unstable=0 ignore_threads=0
while [ $# -gt 0 ]; do
  case "$1" in
    --allow-unstable) allow_unstable=1 ;;
    --ignore-threads) ignore_threads=1 ;;
    -h|--help) sed -n '2,4p' "$0"; exit 0 ;;
    -*) wf_die "unknown flag $1" ;;
    *) pr="${1#\#}" ;;
  esac
  shift
done
[ -n "$pr" ] || wf_die "usage: merge.sh <pr> [--allow-unstable] [--ignore-threads]"
wf_need gh; wf_need jq; wf_need git
root=$(wf_main_root); cd "$root"

read_pr() { gh pr view "$pr" --json number,title,url,state,isDraft,mergeable,mergeStateStatus,headRefName,baseRefName,isCrossRepository,reviewDecision,statusCheckRollup; }
json=$(read_pr) || wf_die "PR #$pr not found"
# After the base moves GitHub recomputes mergeability asynchronously and reports UNKNOWN for a while.
waited=0
while [ "$(printf '%s' "$json" | jq -r .mergeable)" = "UNKNOWN" ] && [ "$waited" -lt "${WF_MERGEABLE_WAIT:-60}" ]; do
  sleep 5; waited=$((waited+5)); json=$(read_pr) || wf_die "PR #$pr not found"
done
state=$(printf '%s' "$json" | jq -r .state)
[ "$state" = "OPEN" ] || wf_die "PR #$pr is $state"
[ "$(printf '%s' "$json" | jq -r .isDraft)" = "false" ] || wf_die "PR #$pr is a draft; lift it with gh pr ready $pr and merge again"
branch=$(printf '%s' "$json" | jq -r .headRefName)
mergeable=$(printf '%s' "$json" | jq -r .mergeable)
[ "$mergeable" != "UNKNOWN" ] || wf_die "GitHub is still computing mergeability of PR #$pr after ${waited}s; try again in a moment"
[ "$mergeable" = "MERGEABLE" ] || wf_die "PR #$pr is $mergeable; resolve conflicts in the worker first"
ms=$(printf '%s' "$json" | jq -r .mergeStateStatus)
case "$ms" in
  CLEAN) ;;
  UNSTABLE) [ "$allow_unstable" = 1 ] || wf_die "PR #$pr has failing non-required checks (UNSTABLE); pass --allow-unstable to merge anyway" ;;
  *) wf_die "PR #$pr merge state is $ms (needs CLEAN)" ;;
esac
failed=$(printf '%s' "$json" | jq -r '[.statusCheckRollup[]? | select((.conclusion // .state) as $c | $c == "FAILURE" or $c == "ERROR" or $c == "CANCELLED" or $c == "TIMED_OUT" or $c == "ACTION_REQUIRED")] | length')
pending=$(printf '%s' "$json" | jq -r '[.statusCheckRollup[]? | select((.status // "COMPLETED") != "COMPLETED" or (.state // "") == "PENDING")] | length')
[ "$failed" = 0 ] || wf_die "PR #$pr has $failed failed checks"
[ "$pending" = 0 ] || wf_die "PR #$pr still has $pending pending checks"
[ "$(printf '%s' "$json" | jq -r .reviewDecision)" != "CHANGES_REQUESTED" ] || wf_die "PR #$pr has changes requested"

if [ "$ignore_threads" = 0 ]; then
  owner=$(gh repo view --json owner -q .owner.login); repo=$(gh repo view --json name -q .name)
  unresolved=$(gh api graphql -f query='query($o:String!,$r:String!,$n:Int!){repository(owner:$o,name:$r){pullRequest(number:$n){reviewThreads(first:100){nodes{isResolved}}}}}' -F o="$owner" -F r="$repo" -F n="$pr" -q '[.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved|not)] | length' 2>/dev/null || echo 0)
  [ "$unresolved" = 0 ] || wf_die "PR #$pr has $unresolved unresolved review threads; let the worker address them or pass --ignore-threads"
fi

# A promotion PR (dev -> main, opened by release.sh) gets a merge commit so dev stays an ancestor of main,
# and dev survives. A PR from a fork has no local worktree or branch; its name may clash with ours, so leave both alone.
path="" ws="" method=squash keep=""
if [ "$(printf '%s' "$json" | jq -r .isCrossRepository)" = true ]; then keep="$branch (fork)"
else case "$branch" in dev|main) method=merge; keep="$branch (long-lived)" ;; esac; fi
if [ -n "$keep" ]; then
  wf_run gh pr merge "$pr" "--$method" >/dev/null
else
  # Tear down the worktree before merging so gh can delete the local branch.
  path=$(wf_worktree_path_for_branch "$branch")
  if [ -n "$path" ]; then
    ws=$(wf_workspace_for_path "$path")
    if [ -n "$ws" ]; then
      wf_run herdr worktree remove --workspace "$ws" --force >/dev/null
    else
      wf_run git worktree remove --force "$path"
    fi
  fi
  wf_run gh pr merge "$pr" --squash --delete-branch >/dev/null
  git worktree prune
  git branch -D "$branch" >/dev/null 2>&1 || true
fi
git fetch -q --prune origin || true
current=$(git rev-parse --abbrev-ref HEAD)
basebr=$(printf '%s' "$json" | jq -r .baseRefName)
# Untracked files (notes, scratch) never block a fast-forward; only modified tracked files do.
if [ "$current" = "$basebr" ] && [ -z "$(git status --porcelain --untracked-files=no)" ]; then
  git pull -q --ff-only origin "$basebr" 2>/dev/null || wf_warn "could not fast-forward $basebr"
fi

wf_kv pr "#$pr $(printf '%s' "$json" | jq -r .title)"
wf_kv merged "$method into $basebr"
if [ -n "$keep" ]; then wf_kv branch "$keep kept"; else wf_kv branch "$branch deleted (remote + local)"; fi
wf_kv worktree "${path:-none} removed"
wf_kv workspace "${ws:-none} closed"
wf_notify "Merged #$pr" "$branch → $basebr"
