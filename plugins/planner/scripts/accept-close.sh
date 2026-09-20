#!/usr/bin/env bash
# The last step of a spec acceptance: post the closing comment and close the spec as completed.
# Usage: accept-close.sh <spec> --comment-file <f> [<ticket>...]   (tickets only where native sub-issues are unavailable)
# It refuses while a sub-issue of the spec is open, so a spec whose acceptance created gap tickets stays
# open until they close and the acceptance has run again.
set -euo pipefail
. "$(dirname "$0")/lib.sh"
wf_need gh; wf_need jq
usage() { sed -n '2,5p' "$0"; exit "${1:-0}"; }
case "${1:-}" in -h|--help) usage 0 ;; "") wf_die "usage: accept-close.sh <spec> --comment-file <f> [<ticket>...]" ;; esac

spec=$(wf_issue_num "$1"); shift
comment=""; tickets=""
while [ $# -gt 0 ]; do
  case "$1" in
    --comment-file) shift; comment="${1:-}" ;;
    -*) wf_die "unknown argument $1" ;;
    *) tickets="$tickets $(wf_issue_num "$1")" ;;
  esac; shift
done
[ -n "$comment" ] || wf_die "accept-close.sh needs --comment-file <f>; the closing comment is what records the acceptance"
[ -f "$comment" ] || wf_die "comment file $comment not found"
[ -s "$comment" ] || wf_die "comment file $comment is empty; the closing comment records what was checked"

nwo=$(wf_repo_nwo) || wf_die "cannot read the repository; is gh authenticated here?"
issue_json() { gh api "repos/$nwo/issues/$1" 2>/dev/null || wf_die "could not read issue #$1 in $nwo; does it exist, and is gh authenticated for this repository?"; }

sj=$(issue_json "$spec")
labels=$(printf '%s' "$sj" | jq -r '[.labels[]?.name] | join(",")')
case ",$labels," in
  *,spec,*) ;;
  *) wf_die "#$spec is not labelled spec (labels: ${labels:--}); only a spec is closed by an acceptance" ;;
esac
[ "$(printf '%s' "$sj" | jq -r .state)" = open ] || wf_die "#$spec is closed already; nothing to accept"

if [ -z "$tickets" ]; then
  subs=$(gh api --paginate "repos/$nwo/issues/$spec/sub_issues?per_page=100" 2>/dev/null) \
    || wf_die "could not read the sub-issues of #$spec; pass the ticket numbers as further arguments: accept-close.sh $spec --comment-file $comment <ticket>..."
  tickets=$(printf '%s' "$subs" | jq -s -r '[add // [] | .[]?.number] | join(" ")')
  [ -n "$tickets" ] || wf_die "#$spec has no native sub-issues; pass the ticket numbers as further arguments: accept-close.sh $spec --comment-file $comment <ticket>..."
fi

open_tickets=""
for t in $tickets; do
  [ "$(issue_json "$t" | jq -r .state)" = open ] || continue
  open_tickets="$open_tickets #$t"
done
[ -z "$open_tickets" ] || wf_die "#$spec still has open sub-issues:$open_tickets; the acceptance runs again once they are closed"

gh issue close "$spec" --comment "$(cat "$comment")" --reason completed >/dev/null \
  || wf_die "closing #$spec failed; close it on GitHub with the comment in $comment"
wf_kv closed "#$spec (completed)"
wf_kv tickets "$(printf '%s' "$tickets" | wc -w | tr -d ' ') checked, all closed"
