#!/usr/bin/env bash
# Wait for CI checks and bot reviews on a PR. Returns within --max-seconds so a tool call never hangs.
# Usage: pr-wait.sh [<pr>] [--max-seconds N]
# Exit: 0 done (see status line), 3 still waiting (call again), 1 error.
set -uo pipefail
. "$(dirname "$0")/lib.sh"
wf_need gh; wf_need jq
pr="" max="${WF_WAIT_SLICE:-540}"
while [ $# -gt 0 ]; do case "$1" in --max-seconds) shift; max="$1";; *) pr="${1#\#}";; esac; shift; done
[ -n "$pr" ] || pr=$(wf_pr_for_branch)
[ -n "$pr" ] || wf_die "no open PR for branch $(wf_branch)"
bots="${WF_PR_BOT_REVIEWERS-chatgpt-codex-connector}"  # no colon: an empty value means "no bot reviewer"
review_wait="${WF_PR_REVIEW_WAIT:-600}"
owner=$(wf_repo_owner); repo=$(wf_repo_name)
start=$(date +%s)

snapshot() {
  view=$(gh pr view "$pr" --json number,url,state,isDraft,mergeStateStatus,statusCheckRollup,reviews,commits) || wf_die "cannot read PR #$pr"
  head_at=$(printf '%s' "$view" | jq -r '.commits[-1].committedDate')
  checks_total=$(printf '%s' "$view" | jq -r '.statusCheckRollup | length')
  checks_fail=$(printf '%s' "$view" | jq -r '[.statusCheckRollup[] | (.conclusion // .state // "") | select(. == "FAILURE" or . == "ERROR" or . == "CANCELLED" or . == "TIMED_OUT" or . == "ACTION_REQUIRED" or . == "STARTUP_FAILURE")] | length')
  checks_pending=$(printf '%s' "$view" | jq -r '[.statusCheckRollup[] | select(((.status // "COMPLETED") != "COMPLETED") or ((.state // "") == "PENDING" or (.state // "") == "EXPECTED"))] | length')
  bot_reviews=$(printf '%s' "$view" | jq -r --arg bots ",$bots," --arg h "$head_at" '[.reviews[] | select(($bots | index("," + (.author.login|sub("\\[bot\\]$";"")) + ",")) != null and .submittedAt > $h)] | length')
  unresolved=$(gh api graphql -f query='query($o:String!,$r:String!,$n:Int!){repository(owner:$o,name:$r){pullRequest(number:$n){reviewThreads(first:100){nodes{isResolved}}}}}' -F o="$owner" -F r="$repo" -F n="$pr" -q '[.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved|not)] | length' 2>/dev/null || echo 0)
  merge_state=$(printf '%s' "$view" | jq -r .mergeStateStatus)
}

report() {
  wf_kv pr "#$pr $(printf '%s' "$view" | jq -r .url)"
  wf_kv status "$1"
  wf_kv checks "total=$checks_total pass=$((checks_total-checks_fail-checks_pending)) fail=$checks_fail pending=$checks_pending"
  wf_kv bot_reviews "$bot_reviews since last push (expected from: $bots)"
  wf_kv unresolved_threads "$unresolved"
  wf_kv merge_state "$merge_state"
  if [ "$checks_fail" -gt 0 ]; then
    printf 'failed_checks:\n'; printf '%s' "$view" | jq -r '.statusCheckRollup[] | select((.conclusion // .state // "") as $c | $c == "FAILURE" or $c == "ERROR" or $c == "CANCELLED" or $c == "TIMED_OUT") | "  - \(.name // .context): \(.detailsUrl // .targetUrl // "")"'
    printf 'help: gh run view <run-id> --log-failed  (run id is the number in the details URL)\n'
  fi
}

checks_done_at=""
while :; do
  snapshot
  now=$(date +%s)
  if [ "$checks_pending" -eq 0 ]; then
    [ -n "$checks_done_at" ] || checks_done_at=$now
    if [ "$checks_fail" -gt 0 ]; then report "checks-failed"; exit 0; fi
    if [ "$bot_reviews" -gt 0 ] || [ -z "$bots" ] || [ $((now-checks_done_at)) -ge "$review_wait" ]; then
      if [ "$unresolved" -gt 0 ]; then report "review-comments"; else report "green"; fi
      exit 0
    fi
  fi
  if [ $((now-start)) -ge "$max" ]; then report "waiting"; printf 'help: call pr-wait.sh again; nothing is stuck yet.\n'; exit 3; fi
  sleep "${WF_POLL_SECONDS:-30}"
done
