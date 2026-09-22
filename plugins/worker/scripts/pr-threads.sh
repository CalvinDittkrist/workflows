#!/usr/bin/env bash
# What a pull request's reviewers are still asking for: the unresolved review threads with their ids,
# so each can be fixed and resolved, and the review summaries that ask for changes.
#
# The summaries are here because a review is not only its inline comments: "request changes" with the
# whole objection in the review body and no comment on a line is an ordinary gesture, and a listing of
# threads alone answers it with nothing. What one reviewer says is their latest review that states
# anything — GitHub leaves an older entry in the list with the state it was submitted with, so an
# objection its author later approved away is not one that still stands, and a review that only
# commented states nothing and leaves the one before it standing.
# Usage: pr-threads.sh [<pr>]
set -uo pipefail
. "$(dirname "$0")/lib.sh"
wf_need gh; wf_need jq
pr="${1:-}"; pr="${pr#\#}"; [ -n "$pr" ] || pr=$(wf_pr_for_branch)
[ -n "$pr" ] || wf_die "no open PR for branch $(wf_branch)"
gh api graphql -F o="$(wf_repo_owner)" -F r="$(wf_repo_name)" -F n="$pr" -f query='
query($o:String!,$r:String!,$n:Int!){ repository(owner:$o,name:$r){ pullRequest(number:$n){
  reviews(last:50){ nodes{ state body submittedAt author{login} } }
  reviewThreads(first:100){ nodes{ id isResolved isOutdated path line
    comments(first:20){ nodes{ author{login} body createdAt } } } } } } }' \
| jq -r '.data.repository.pullRequest as $pr
  | ($pr.reviewThreads.nodes | map(select(.isResolved|not))) as $t
  | ([$pr.reviews.nodes[] | select(.state != "COMMENTED" and .state != "PENDING")]
     | group_by(.author.login) | map(max_by(.submittedAt))
     | map(select(.state == "CHANGES_REQUESTED" and ((.body // "") | test("\\S"))))
     | sort_by(.submittedAt)) as $r
  | "unresolved_threads: \($t|length)",
    "changes_requested: \($r|length)",
    ($r[] | "\n## review by @\(.author.login // "a deleted account") (\(.submittedAt))\n\(.body)"),
    ($t[] | "\n## thread \(.id)\nfile: \(.path):\(.line // "?")" + (if .isOutdated then " (outdated)" else "" end) +
      "\n" + ([.comments.nodes[] | "- @\(.author.login): \(.body)"] | join("\n")))'
