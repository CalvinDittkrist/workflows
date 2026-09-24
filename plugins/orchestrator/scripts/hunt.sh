#!/usr/bin/env bash
# Open a test hunt: worktree hunt/tests-<date> + Herdr workspace running a worker on /worker:hunt-tests.
# Usage: hunt.sh [--sandbox] [--base <branch>]
set -euo pipefail
. "$(dirname "$0")/lib.sh"

sandbox=0 base=""
while [ $# -gt 0 ]; do
  case "$1" in
    --sandbox) sandbox=1 ;;
    --base) [ $# -gt 1 ] || wf_die "--base needs a branch name, e.g. --base dev"; shift; base="$1" ;;
    -h|--help) sed -n '2,3p' "$0"; exit 0 ;;
    *) wf_die "unknown argument $1; a test hunt takes no issue and no path. Usage: hunt.sh [--sandbox] [--base <branch>]" ;;
  esac
  shift
done
[ "${HERDR_ENV:-}" = 1 ] || wf_die "hunt needs a Herdr-managed pane (HERDR_ENV=1). Start the orchestrator inside Herdr."
wf_need jq; wf_need herdr; wf_need git
wf_check_claude_args WF_WORKER_CLAUDE_ARGS
[ "$sandbox" = 1 ] && wf_need sbx

root=$(wf_main_root); cd "$root"

# One hunt at a time: a second one would hunt the same tests and open a second pull request removing them.
existing_branch=$(git worktree list --porcelain | sed -nE 's#^branch refs/heads/(hunt/.*)#\1#p' | head -n1)
if [ -n "$existing_branch" ]; then
  existing=$(wf_worktree_path_for_branch "$existing_branch")
  ws=$(wf_workspace_for_path "$existing")
  wf_kv branch "$existing_branch"; wf_kv path "$existing"; wf_kv workspace "${ws:-none}"
  wf_kv status "already-open"
  wf_kv next "Talk to the worker in workspace ${ws:-?} or run abandon.sh $existing_branch to drop it."
  exit 0
fi

given_base="$base"
[ -n "$base" ] || base=$(wf_base_branch)
branch="hunt/tests-$(date +%Y-%m-%d)"

# A hunt another clone or machine runs is on origin as its branch, which this clone's worktrees do not show.
# A repository without origin, or one that cannot be reached, has none this clone can see.
remote_hunt=$( (git ls-remote --heads origin 'refs/heads/hunt/*' 2>/dev/null || true) | sed -nE 's#^[0-9a-f]+[[:space:]]+refs/heads/##p' | head -n1)
[ -z "$remote_hunt" ] || wf_die "a test hunt is already open on origin as $remote_hunt, from another clone or machine; finish it there, or delete the branch on origin when it was dropped, then hunt again"

git fetch -q origin "$base" 2>/dev/null || wf_warn "could not fetch origin/$base; branching from local $base"
baseref="origin/$base"; git rev-parse -q --verify "$baseref" >/dev/null 2>&1 || baseref="$base"

# Refused before anything is created: a base whose files follow none of the conventions gives the hunters
# nothing to read, and the board would carry a hunt for nothing. The files are the base's, which the worktree
# starts from, not those of this checkout.
files=$(git -c core.quotePath=false ls-tree -r --name-only "$baseref" 2>/dev/null | wf_test_paths)
[ -n "$files" ] || wf_die "no test file on $baseref matches the conventions of a test hunt: $wf_test_file_rule. There is nothing to hunt here."
count=$(printf '%s\n' "$files" | wc -l | tr -d ' ')
dirs=$(printf '%s\n' "$files" | awk '{ if (!sub(/\/[^\/]*$/, "")) $0 = "."; print }' | sort -u | wc -l | tr -d ' ')

wf_create_worktree "$branch" "$baseref" "hunt tests"
if [ "${WF_DRY_RUN:-0}" = 1 ]; then
  wf_kv branch "$branch"; wf_kv base "$baseref"; wf_kv sandbox "$sandbox"; wf_kv status "dry-run"; exit 0
fi

# The session of a manual claim, without an issue: the branch is the unit of work, and the pull request at
# its end is the hunt's trace (ADR 0045).
settings=$(wf_worker_settings manual "" '{}' "$given_base")
wf_start_worker "$sandbox" "hunt-tests" "hunt tests" "$settings" /worker:hunt-tests

wf_kv hunt "tests"
wf_kv test_files "$count in $dirs directories"
wf_kv branch "$branch"
wf_kv path "$path"
wf_kv workspace "$ws"
wf_kv pane "$pane"
wf_kv agent "$agent_name"
wf_kv agent_status "${agent_status:-not-detected}"
wf_kv mode "manual"
[ "$sandbox" = 1 ] && wf_kv sandbox "docker"
wf_kv next "board.sh shows progress; merge.sh <pr> when the hunt's PR is ready, or abandon.sh $branch when it removed nothing"
