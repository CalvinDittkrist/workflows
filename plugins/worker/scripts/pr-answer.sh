#!/usr/bin/env bash
# Answer a review summary with one comment on the pull request.
#
# A review whose objection is in its body and on no line of the diff has no thread to resolve, so
# pr-resolve.sh cannot say what was done about it. Without this the reviewer's only answer is the
# commits, and a point that was declined gets no word at all, least of all on an unattended run,
# where the pull request is the whole conversation.
# Usage: pr-answer.sh [<pr>] --body "<text>"
set -uo pipefail
. "$(dirname "$0")/lib.sh"
wf_need gh
usage='usage: pr-answer.sh [<pr>] --body "<text>"'
pr=""; body=""
while [ $# -gt 0 ]; do
  case "$1" in
    --body) shift; body="${1:-}" ;;
    -*) wf_die "$usage" ;;
    *) [ -z "$pr" ] || wf_die "$usage"; pr="${1#\#}" ;;
  esac
  shift
done
[ -n "$body" ] || wf_die "a --body saying what was done about the review, or why not, is required"
[ -n "$pr" ] || pr=$(wf_pr_for_branch)
[ -n "$pr" ] || wf_die "no open PR for branch $(wf_branch)"
gh pr comment "$pr" --body "$body" ||
  wf_die "the comment on pull request #$pr could not be made; the review is still unanswered, so say so when you report"
