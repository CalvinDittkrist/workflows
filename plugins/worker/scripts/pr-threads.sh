#!/usr/bin/env bash
# List unresolved review threads of a PR with their ids, so each can be fixed and resolved.
# Usage: pr-threads.sh [<pr>]
set -uo pipefail
. "$(dirname "$0")/lib.sh"
wf_need gh; wf_need jq
pr="${1:-}"; pr="${pr#\#}"; [ -n "$pr" ] || pr=$(wf_pr_for_branch)
[ -n "$pr" ] || wf_die "no open PR for branch $(wf_branch)"
gh api graphql -F o="$(wf_repo_owner)" -F r="$(wf_repo_name)" -F n="$pr" -f query='
query($o:String!,$r:String!,$n:Int!){ repository(owner:$o,name:$r){ pullRequest(number:$n){
  reviewThreads(first:100){ nodes{ id isResolved isOutdated path line
    comments(first:20){ nodes{ author{login} body createdAt } } } } } } }' \
| jq -r '.data.repository.pullRequest.reviewThreads.nodes
  | map(select(.isResolved|not)) as $t
  | "unresolved_threads: \($t|length)",
    ($t[] | "\n## thread \(.id)\nfile: \(.path):\(.line // "?")" + (if .isOutdated then " (outdated)" else "" end) +
      "\n" + ([.comments.nodes[] | "- @\(.author.login): \(.body)"] | join("\n")))'
