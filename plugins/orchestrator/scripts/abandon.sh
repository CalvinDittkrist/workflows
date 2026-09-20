#!/usr/bin/env bash
# Drop a claimed worktree without merging. Refuses to delete unpushed commits unless --force.
# Usage: abandon.sh <issue|branch> [--force]
set -euo pipefail
. "$(dirname "$0")/lib.sh"
target="" force=0
while [ $# -gt 0 ]; do
  case "$1" in --force) force=1 ;; -h|--help) sed -n '2,3p' "$0"; exit 0 ;; -*) wf_die "unknown flag $1" ;; *) target="${1#\#}" ;; esac; shift
done
[ -n "$target" ] || wf_die "usage: abandon.sh <issue|branch> [--force]"
wf_need git; wf_need jq
root=$(wf_main_root); cd "$root"

branch="$target"
if printf '%s' "$target" | grep -Eq '^[0-9]+$'; then
  branch=$(wf_branch_for_issue "$target")
  [ -n "$branch" ] || wf_die "no worktree branch for issue #$target"
fi
path=$(wf_worktree_path_for_branch "$branch")
[ -n "$path" ] || wf_die "branch $branch has no worktree"

if [ "$force" = 0 ]; then
  unpushed=$(git rev-list --count "origin/$branch..$branch" 2>/dev/null || git rev-list --count "$(wf_base_branch)..$branch" 2>/dev/null || echo 0)
  [ "$unpushed" = 0 ] || wf_die "$branch has $unpushed commits not on origin; push them or pass --force"
  [ -z "$(git -C "$path" status --porcelain)" ] || wf_die "$path has uncommitted changes; commit, stash or pass --force"
fi

ws=$(wf_workspace_for_path "$path")
if [ -n "$ws" ]; then wf_run herdr worktree remove --workspace "$ws" --force >/dev/null; else wf_run git worktree remove --force "$path"; fi
git worktree prune
wf_run git branch -D "$branch" >/dev/null
wf_kv branch "$branch deleted locally"
wf_kv worktree "$path removed"
wf_kv workspace "${ws:-none} closed"
wf_kv note "remote branch and the issue assignment are untouched"
