#!/usr/bin/env bash
# The last step of a spec acceptance: post the closing comment and close the spec as completed.
# Usage: accept-close.sh <spec> --comment-file <f> [<ticket>...]   (tickets add to the native sub-issues)
# It refuses while any of them is open, so a spec whose acceptance created gap tickets stays open until
# they are closed and the acceptance has run again.
set -euo pipefail
. "$(dirname "$0")/lib.sh"
wf_need gh; wf_need jq
usage() { sed -n '2,5p' "$0"; exit "${1:-0}"; }
case "${1:-}" in -h|--help) usage 0 ;; "") wf_die "usage: accept-close.sh <spec> --comment-file <f> [<ticket>...]" ;; esac

spec=$(wf_issue_num "$1"); shift
comment=""; tickets=""
while [ $# -gt 0 ]; do
  case "$1" in
    --comment-file) [ $# -ge 2 ] || wf_die "--comment-file needs the file with the closing comment"; shift; comment="$1" ;;
    -*) wf_die "unknown argument $1" ;;
    *) tickets="$tickets $(wf_issue_num "$1")" ;;
  esac; shift
done
[ -n "$comment" ] || wf_die "accept-close.sh needs --comment-file <f>; the closing comment is what records the acceptance"
[ -f "$comment" ] || wf_die "comment file $comment not found"
[ -s "$comment" ] || wf_die "comment file $comment is empty; the closing comment records what was checked"

nwo=$(wf_repo_nwo) || wf_die "cannot read the repository; is gh authenticated here?"
sj=$(wf_issue_json "$nwo" "$spec")
wf_require_open_spec "$spec" "$sj"

# Every ticket of the spec: its native sub-issues, plus the numbers passed. Arguments add to the sub-issues
# instead of replacing them, so a gap ticket the caller forgot still refuses the close.
hint="pass the ticket numbers as further arguments: accept-close.sh $spec --comment-file $comment <ticket>..."
if subs=$(wf_sub_issues "$nwo" "$spec"); then
  tickets="$tickets $(printf '%s' "$subs" | jq -r '[.[]?.number] | join(" ")')"
elif [ -z "$tickets" ]; then
  wf_die "could not read the sub-issues of #$spec; $hint"
else
  wf_warn "could not read the sub-issues of #$spec; only the ticket numbers passed were checked, a gap ticket outside them stays unseen"
fi
tickets=$(printf '%s' "$tickets" | tr ' ' '\n' | grep -E '^[0-9]+$' | sort -un | tr '\n' ' ' || true)
[ -n "$tickets" ] || wf_die "#$spec has no native sub-issues; $hint"

# A ticket whose state cannot be read is never counted as closed: the refusal aborts the run.
open_tickets=""
for t in $tickets; do
  state=$(wf_issue_json "$nwo" "$t" | jq -r .state)
  [ "$state" = open ] || continue
  open_tickets="$open_tickets #$t"
done
[ -z "$open_tickets" ] || wf_die "#$spec still has open sub-issues:$open_tickets; the acceptance runs again once they are closed"

"$(dirname "$0")/issue.sh" close "$spec" --comment-file "$comment" --reason completed \
  || wf_die "closing #$spec failed; close it on GitHub with the comment in $comment"
wf_kv tickets "$(printf '%s' "$tickets" | wc -w | tr -d ' ') checked, all closed"
