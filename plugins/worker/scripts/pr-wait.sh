#!/usr/bin/env bash
# Wait for CI checks and bot reviews on a PR. Returns within --max-seconds so a tool call never hangs.
# Usage: pr-wait.sh [<pr>] [--max-seconds N]
# Exit: 0 done (see status line), 3 still waiting (call again), 1 error.
# Status: conflicts (the branch cannot be merged into its base; comes first, because GitHub runs no
# pull_request workflow for such a branch and an empty rollup would read as green), checks-failed,
# review-comments, green, waiting.
#
# review-comments is what a reviewer is still asking for, and that is not only the unresolved threads:
# "request changes" with the whole objection in the review body and no comment on a line leaves no
# thread behind, and counting threads alone reported such a pull request as green. The stage would then
# never open the listing that shows the summary (pr-threads.sh), and the objection would stand
# unanswered while the worker reported itself ready.
set -uo pipefail
. "$(dirname "$0")/lib.sh"
wf_need gh; wf_need jq
pr="" max=$(wf_wait_slice)
while [ $# -gt 0 ]; do case "$1" in --max-seconds) shift; max="$1";; *) pr="${1#\#}";; esac; shift; done
[ -n "$pr" ] || pr=$(wf_pr_for_branch)
[ -n "$pr" ] || wf_die "no open PR for branch $(wf_branch)"
bots="${WF_PR_BOT_REVIEWERS-chatgpt-codex-connector}"  # no colon: an empty value means "no bot reviewer"
review_wait="${WF_PR_REVIEW_WAIT:-1200}"
checks_grace="${WF_CHECKS_GRACE:-600}"  # seconds after the last push to wait for CI to register its checks
owner=$(wf_repo_owner); repo=$(wf_repo_name)
start=$(date +%s)

snapshot() {
  view=$(gh pr view "$pr" --json number,url,state,mergeable,mergeStateStatus,statusCheckRollup,reviews,commits) || wf_die "cannot read PR #$pr"
  # Whether the branch merges into its base: MERGEABLE, CONFLICTING, or UNKNOWN while GitHub is still
  # trying the merge after a push, which is waited out like a pending check and never read as clean.
  mergeable=$(printf '%s' "$view" | jq -r '.mergeable // ""')
  head_at=$(printf '%s' "$view" | jq -r '.commits[-1].committedDate')
  head_epoch=$(wf_epoch "$head_at" || date +%s)
  checks_total=$(printf '%s' "$view" | jq -r '.statusCheckRollup | length')
  checks_fail=$(printf '%s' "$view" | jq -r '[.statusCheckRollup[] | (.conclusion // .state // "") | select(. == "FAILURE" or . == "ERROR" or . == "CANCELLED" or . == "TIMED_OUT" or . == "ACTION_REQUIRED" or . == "STARTUP_FAILURE")] | length')
  checks_pending=$(printf '%s' "$view" | jq -r '[.statusCheckRollup[] | select(((.status // "COMPLETED") != "COMPLETED") or ((.state // "") == "PENDING" or (.state // "") == "EXPECTED"))] | length')
  # Every review a listed bot left on the pull request, on whichever commit. A bot reviews a pull request
  # once and not again on every push, so a review on an older commit ends the wait too: a repair push must
  # not spend the review window a second time on a review that is not coming. The login is bound to $l
  # before the list is searched: inside `index(...)` the input is the list, so a `.author` there reads the
  # list and not the review, and jq fails as soon as a PR has any review at all.
  bot_reviews=$(printf '%s' "$view" | jq -r --arg bots ",$bots," '[.reviews[] | ((.author.login // "") | sub("\\[bot\\]$";"")) as $l | select(($bots | index("," + $l + ",")) != null)] | length')
  # What one reviewer says about the pull request is their latest review that states anything, and an
  # objection stands until that author approves or it is dismissed: the same rule pr-threads.sh lists the
  # summaries by, so the wait ends on exactly what the listing then shows. A review with nothing written
  # in it is not one of them: the listing has nothing to show for it, and the stage would loop on it.
  changes_requested=$(printf '%s' "$view" | jq -r '[.reviews[] | select(.state != "COMMENTED" and .state != "PENDING")]
    | group_by(.author.login) | map(max_by(.submittedAt))
    | map(select(.state == "CHANGES_REQUESTED" and ((.body // "") | test("\\S")))) | length')
  unresolved=$(gh api graphql -f query='query($o:String!,$r:String!,$n:Int!){repository(owner:$o,name:$r){pullRequest(number:$n){reviewThreads(first:100){nodes{isResolved}}}}}' -F o="$owner" -F r="$repo" -F n="$pr" -q '[.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved|not)] | length' 2>/dev/null || echo 0)
  merge_state=$(printf '%s' "$view" | jq -r .mergeStateStatus)
  # When the last check finished, from GitHub, so the review wait survives across calls of this script.
  last_check=$(printf '%s' "$view" | jq -r '[.statusCheckRollup[] | .completedAt // empty] | max // empty')
  checks_done_at=$( [ -n "$last_check" ] && wf_epoch "$last_check" || printf '%s' "${checks_done_at:-}" )
}

report() {
  wf_kv pr "#$pr $(printf '%s' "$view" | jq -r .url)"
  wf_kv status "$1"
  wf_kv checks "total=$checks_total pass=$((checks_total-checks_fail-checks_pending)) fail=$checks_fail pending=$checks_pending"
  wf_kv bot_reviews "$bot_reviews on the pull request (expected from: $bots)"
  wf_kv unresolved_threads "$unresolved"
  wf_kv changes_requested "$changes_requested"
  wf_kv merge_state "$merge_state (mergeable: ${mergeable:-unknown})"
  if [ "$checks_fail" -gt 0 ]; then
    printf 'failed_checks:\n'; printf '%s' "$view" | jq -r '.statusCheckRollup[] | select((.conclusion // .state // "") as $c | $c == "FAILURE" or $c == "ERROR" or $c == "CANCELLED" or $c == "TIMED_OUT") | "  - \(.name // .context): \(.detailsUrl // .targetUrl // "")"'
    printf 'help: gh run view <run-id> --log-failed  (run id is the number in the details URL)\n'
  fi
  # The fix is printed with the answer, so a context that entered the stage without the skill's text has
  # it too. A merge and not a rebase: the branch is pushed, and the worker never rewrites pushed history.
  if [ "$1" = conflicts ]; then
    base=$(wf_base_branch)
    printf 'help: the branch conflicts with %s, so no pull_request workflow ran. Count a repair round, then git fetch origin %s && git merge origin/%s, resolve the conflicts, commit the merge, push, and run /worker:ci again. Never rebase or force-push.\n' "$base" "$base" "$base"
  fi
}

checks_done_at=""
while :; do
  snapshot
  now=$(date +%s)
  # Right after a push the rollup is empty until GitHub registers the workflow run. With CI configured,
  # treat that as pending for a grace period instead of reporting a false green (would self-merge in yolo).
  if [ "$checks_total" -eq 0 ] && [ $((now-head_epoch)) -lt "$checks_grace" ] && [ -n "$(find .github/workflows -maxdepth 1 \( -name '*.yml' -o -name '*.yaml' \) 2>/dev/null | head -n 1)" ]; then
    checks_pending=1
  fi
  if [ "$mergeable" = CONFLICTING ]; then report "conflicts"; exit 0; fi
  [ "$mergeable" != UNKNOWN ] || checks_pending=1
  if [ "$checks_pending" -eq 0 ]; then
    [ -n "$checks_done_at" ] || checks_done_at=$now  # no completedAt in the rollup (statuses): count from first sight
    if [ "$checks_fail" -gt 0 ]; then report "checks-failed"; exit 0; fi
    if [ "$bot_reviews" -gt 0 ] || [ -z "$bots" ] || [ $((now-checks_done_at)) -ge "$review_wait" ]; then
      if [ "$unresolved" -gt 0 ] || [ "$changes_requested" -gt 0 ]; then report "review-comments"; else report "green"; fi
      exit 0
    fi
  fi
  if [ $((now-start)) -ge "$max" ]; then report "waiting"; printf 'help: call pr-wait.sh again; nothing is stuck yet.\n'; exit 3; fi
  sleep "${WF_POLL_SECONDS:-30}"
done
