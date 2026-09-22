#!/usr/bin/env bash
# Yolo-mode finish: squash-merge the PR, notify, and remove this worktree/workspace from a detached process.
# Usage: finish.sh [<pr>]
set -uo pipefail
. "$(dirname "$0")/lib.sh"
wf_need gh; wf_need jq; wf_need git
[ "${WF_MODE:-manual}" = "yolo" ] || wf_die "finish.sh only runs in yolo mode (WF_MODE=yolo). In manual mode the orchestrator merges with /orchestrator:merge."
pr="${1:-}"; pr="${pr#\#}"; [ -n "$pr" ] || pr=$(wf_pr_for_branch)
[ -n "$pr" ] || wf_die "no open PR for branch $(wf_branch)"
# The recorded panel decides, because it is local and deterministic (ADR 0018).
verdict=$("$(dirname "$0")/panel.sh" verdict)
[ "$verdict" = ready ] || wf_die "the reviewer panel of this worktree did not pass (panel_verdict: ${verdict:-unknown}); a yolo run stops here for the maintainer, who reads the pull request body and merges by hand. Do not merge it yourself"
state=$(gh pr view "$pr" --json mergeStateStatus -q .mergeStateStatus)
case "$state" in
  CLEAN) ;;
  *) wf_die "PR #$pr is not mergeable yet ($state); run pr-wait.sh and address findings first" ;;
esac
branch=$(wf_branch)
path=$(pwd)
main_root=$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")
gh pr merge "$pr" --squash >/dev/null || wf_die "merge failed"
wf_notify "Merged #$pr (yolo)" "$branch"
wf_kv pr "#$pr merged (squash)"
wf_kv cleanup "worktree $path, branch $branch and this workspace are removed in 5 seconds"
nohup bash "$(dirname "$0")/cleanup-self.sh" "$main_root" "$path" "$branch" "${HERDR_WORKSPACE_ID:-}" >/dev/null 2>&1 &
