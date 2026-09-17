#!/usr/bin/env bash
# The triage queue in three buckets: never labelled, needs-triage, and needs-info with a reply from someone else.
set -euo pipefail
. "$(dirname "$0")/lib.sh"
wf_need gh; wf_need jq
issues=$(gh issue list --state open --limit 100 --json number,title,labels,author,comments 2>/dev/null) || wf_die "could not list issues; is gh authenticated for this repository?"
me=$(gh api user -q .login 2>/dev/null || true)
printf '%s' "$issues" | jq -r --arg me "$me" '
  def state: [.labels[].name] | map(select(. == "needs-triage" or . == "needs-info" or . == "ready-for-agent" or . == "ready-for-human" or . == "wontfix")) | first // "";
  def last_by: (.comments | if length > 0 then last.author.login else "" end);
  [.[] | select(state == "" and ([.labels[].name] | index("spec") | not))] as $u
  | [.[] | select(state == "needs-triage")] as $t
  | [.[] | select(state == "needs-info" and (last_by != "" and last_by != $me))] as $i
  | "unlabeled[\($u|length)]{issue,title,author}:", ($u[] | "  \(.number),\(.title),\(.author.login)"),
    "needs-triage[\($t|length)]{issue,title,author}:", ($t[] | "  \(.number),\(.title),\(.author.login)"),
    "needs-info-replied[\($i|length)]{issue,title,last_comment_by}:", ($i[] | "  \(.number),\(.title),\(last_by)")'
