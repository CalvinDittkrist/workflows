#!/usr/bin/env bash
# Reply to a review thread and mark it resolved.
# Usage: pr-resolve.sh <thread-id> --reply "<text>"
set -uo pipefail
. "$(dirname "$0")/lib.sh"
wf_need gh; wf_need jq
id="${1:-}"; [ -n "$id" ] || wf_die "usage: pr-resolve.sh <thread-id> --reply <text>"
shift; reply=""
while [ $# -gt 0 ]; do case "$1" in --reply) shift; reply="${1:-}";; esac; shift; done
[ -n "$reply" ] || wf_die "a --reply explaining the fix or the disagreement is required"
gh api graphql -F id="$id" -F body="$reply" -f query='mutation($id:ID!,$body:String!){ addPullRequestReviewThreadReply(input:{pullRequestReviewThreadId:$id, body:$body}){ comment{ url } } }' -q '.data.addPullRequestReviewThreadReply.comment.url' || wf_die "reply failed"
gh api graphql -F id="$id" -f query='mutation($id:ID!){ resolveReviewThread(input:{threadId:$id}){ thread{ isResolved } } }' -q '"resolved: \(.data.resolveReviewThread.thread.isResolved)"' || wf_die "resolve failed"
