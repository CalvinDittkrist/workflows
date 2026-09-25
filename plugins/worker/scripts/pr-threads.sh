#!/usr/bin/env bash
# What a pull request's reviewers are still asking for: the unresolved review threads with their ids,
# so each can be fixed and resolved, and the review summaries that ask for changes.
#
# The summaries are here because a review is not only its inline comments: "request changes" with the
# whole objection in the review body and no comment on a line is an ordinary gesture, and a listing of
# threads alone answers it with nothing. What one reviewer says is their latest review that states
# anything: GitHub leaves an older entry in the list with the state it was submitted with, so an
# objection its author later approved away is not one that still stands, and a review that only
# commented states nothing and leaves the one before it standing. That fold below is the rule; the
# states argument of the query is what keeps a page from being spent on reviews that state nothing,
# which a bot that comments on every push posts by the dozen.
#
# The reviews are read to the first of them and not only the newest page: the rule is the latest review
# per author, and an author whose word lies before a page boundary would otherwise be read as silent:
# while the factory, which paginates the whole list, queues a follow-up run for exactly that review and
# would then record it as answered by a session that was shown nothing. A pull request with a hundred
# reviews that state something is rare, so this is one request on all but those.
# Usage: pr-threads.sh [<pr>]
set -uo pipefail
. "$(dirname "$0")/lib.sh"
wf_need gh; wf_need jq
pr="${1:-}"; pr="${pr#\#}"; [ -n "$pr" ] || pr=$(wf_pr_for_branch)
[ -n "$pr" ] || wf_die "no open PR for branch $(wf_branch)"
owner=$(wf_repo_owner); repo=$(wf_repo_name)
stateful='states:[APPROVED, CHANGES_REQUESTED, DISMISSED]'

answer=$(gh api graphql -F o="$owner" -F r="$repo" -F n="$pr" -f query='
query($o:String!,$r:String!,$n:Int!){ repository(owner:$o,name:$r){ pullRequest(number:$n){
  reviews(last:100, '"$stateful"'){
    pageInfo{ hasPreviousPage startCursor } nodes{ state body submittedAt author{login} } }
  reviewThreads(first:100){ nodes{ id isResolved isOutdated path line
    comments(first:20){ nodes{ author{login} body createdAt } } } } } } }') \
  || wf_die "cannot read the reviews of #$pr"

page='.data.repository.pullRequest.reviews'
reviews=$(printf '%s' "$answer" | jq -c "$page.nodes // []")
more=$(printf '%s' "$answer" | jq -r "$page.pageInfo.hasPreviousPage // false")
cursor=$(printf '%s' "$answer" | jq -r "$page.pageInfo.startCursor // empty")
while [ "$more" = true ] && [ -n "$cursor" ]; do
  earlier=$(gh api graphql -F o="$owner" -F r="$repo" -F n="$pr" -F before="$cursor" -f query='
  query($o:String!,$r:String!,$n:Int!,$before:String!){ repository(owner:$o,name:$r){ pullRequest(number:$n){
    reviews(last:100, before:$before, '"$stateful"'){
      pageInfo{ hasPreviousPage startCursor } nodes{ state body submittedAt author{login} } } } } }') \
    || wf_die "cannot read the reviews of #$pr before $cursor"
  # The older page first: the list stays in GitHub's order, oldest review first, which the fold reads.
  reviews=$(printf '%s' "$earlier" | jq -c --argjson newer "$reviews" "($page.nodes // []) + \$newer")
  more=$(printf '%s' "$earlier" | jq -r "$page.pageInfo.hasPreviousPage // false")
  cursor=$(printf '%s' "$earlier" | jq -r "$page.pageInfo.startCursor // empty")
done

printf '%s' "$answer" | jq -r --argjson reviews "$reviews" '.data.repository.pullRequest as $pr
  | ($pr.reviewThreads.nodes | map(select(.isResolved|not))) as $t
  | ([$reviews[] | select(.state != "COMMENTED" and .state != "PENDING")]
     | group_by(.author.login) | map(max_by(.submittedAt))
     | map(select(.state == "CHANGES_REQUESTED" and ((.body // "") | test("\\S"))))
     | sort_by(.submittedAt)) as $r
  | "unresolved_threads: \($t|length)",
    "changes_requested: \($r|length)",
    ($r[] | "\n## review by @\(.author.login // "a deleted account") (\(.submittedAt))\n\(.body)"),
    ($t[] | "\n## thread \(.id)\nfile: \(.path):\(.line // "?")" + (if .isOutdated then " (outdated)" else "" end) +
      "\n" + ([.comments.nodes[] | "- @\(.author.login): \(.body)"] | join("\n")))'
