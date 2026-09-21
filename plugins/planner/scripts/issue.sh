#!/usr/bin/env bash
# GitHub issue operations the planner skills need, with the fiddly parts (sub-issues, blocking edges) inside.
# Usage: issue.sh create --title <t> --body-file <f> [--label <l>]... [--parent <n>] [--milestone <vX.Y.Z>]
#        issue.sh block <n> --by <m>[,<m>...]
#        issue.sh milestones
#        issue.sh milestone <vX.Y.Z> [--description <goal>]
#        issue.sh attach <n> --milestone <vX.Y.Z>
#        issue.sh label <n> [--add <l>]... [--remove <l>]...
#        issue.sh comment <n> --body-file <f>
#        issue.sh close <n> [--comment-file <f>] [--reason completed|not-planned]
#        create with --parent and --milestone also attaches the parent to that milestone when it carries none
#        create and label refuse the routing label factory without ready-for-agent, or next to ready-for-human
set -euo pipefail
. "$(dirname "$0")/lib.sh"
wf_need gh; wf_need jq
usage() { sed -n '3,12p' "$0"; exit "${1:-0}"; }
cmd="${1:-}"; [ -n "$cmd" ] || usage 1; shift
version() { printf '%s' "$1" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || wf_die "milestone must be named vX.Y.Z, got '$1'"; printf '%s' "$1"; }
# The milestone titled $1 as JSON (open or closed), or empty.
milestone_json() { gh api --paginate "repos/$(wf_repo_nwo)/milestones?state=all&per_page=100" | jq -s -c --arg t "$1" '[.[][] | select(.title == $t)] | first // empty'; }
# Refuse attaching to a milestone that does not exist or was already released (closed).
open_milestone() {
  local m; m=$(milestone_json "$1") || wf_die "cannot read milestones"
  [ -n "$m" ] || wf_die "milestone $1 does not exist; create it with issue.sh milestone $1 --description <goal>"
  [ "$(printf '%s' "$m" | jq -r .state)" = open ] || wf_die "milestone $1 is closed (released); pick a new version"
}
# A release closes a milestone only once every issue on it is closed, so the parent ($1, the spec) joins the
# milestone of its sub-issue ($2). A parent that already carries a different milestone keeps it.
attach_parent() {
  local have; have=$(gh api "repos/$(wf_repo_nwo)/issues/$1" --jq '.milestone.title // ""') \
    || { wf_warn "cannot read the milestone of #$1; attach it to $2 on GitHub"; return 0; }
  case "$have" in
    "$2") wf_kv parent-milestone "#$1 already on $2" ;;
    "") if gh issue edit "$1" --milestone "$2" >/dev/null; then wf_kv parent-milestone "#$1 attached to $2"
        else wf_warn "attaching #$1 to $2 failed; attach it on GitHub"; fi ;;
    *) wf_warn "#$1 stays on milestone $have while its sub-issues go to $2; move it if $2 releases this work" ;;
  esac
}

case "$cmd" in
  create)
    title="" body="" parent="" milestone=""; labels=()
    while [ $# -gt 0 ]; do
      case "$1" in
        --title) shift; title="${1:-}" ;;
        --body-file) shift; body="${1:-}" ;;
        --label) shift; labels+=("$1") ;;
        --parent) shift; parent=$(wf_issue_num "${1:-}") ;;
        --milestone) shift; milestone=$(version "${1:-}") ;;
        *) wf_die "unknown argument $1" ;;
      esac; shift
    done
    if [ -z "$title" ] || [ -z "$body" ]; then wf_die "create needs --title and --body-file"; fi
    [ -f "$body" ] || wf_die "body file $body not found"
    wf_require_routable "the new issue" "leave --label $WF_ROUTING_LABEL off" "${labels[@]+"${labels[@]}"}"
    args=(); for l in "${labels[@]+"${labels[@]}"}"; do args+=(--label "$l"); done
    if [ -n "$milestone" ]; then open_milestone "$milestone"; args+=(--milestone "$milestone"); fi
    url=$(gh issue create --title "$title" --body-file "$body" "${args[@]+"${args[@]}"}") || wf_die "gh issue create failed (missing label? run labels.sh)"
    n="${url##*/}"
    wf_kv issue "#$n"; wf_kv url "$url"
    [ -z "$milestone" ] || wf_kv milestone "$milestone"
    if [ -n "$parent" ]; then
      id=$(wf_issue_db_id "$n")
      if [ -n "$id" ] && gh api --method POST "repos/$(wf_repo_nwo)/issues/$parent/sub_issues" -F sub_issue_id="$id" >/dev/null 2>&1; then
        wf_kv parent "#$parent (sub-issue)"
      else
        wf_kv parent "#$parent (body only; sub-issues unavailable here)"
      fi
      # The spec hangs on the milestone of its tickets, so the release waits for its acceptance.
      [ -z "$milestone" ] || attach_parent "$parent" "$milestone"
    fi ;;
  block)
    n=$(wf_issue_num "${1:-}"); shift; by=""
    while [ $# -gt 0 ]; do case "$1" in --by) shift; by="${1:-}" ;; *) wf_die "unknown argument $1" ;; esac; shift; done
    [ -n "$by" ] || wf_die "block needs --by <m>[,<m>...]"
    nwo=$(wf_repo_nwo)
    for m in $(printf '%s' "$by" | tr ',' ' '); do
      m=$(wf_issue_num "$m"); id=$(wf_issue_db_id "$m")
      if [ -n "$id" ] && gh api --method POST "repos/$nwo/issues/$n/dependencies/blocked_by" -F issue_id="$id" >/dev/null 2>&1; then
        wf_kv blocked "#$n by #$m (native)"
      else
        wf_kv blocked "#$n by #$m (body only; dependencies unavailable here)"
      fi
    done ;;
  milestones)
    gh api --paginate "repos/$(wf_repo_nwo)/milestones?state=open&per_page=100" | jq -s -r 'add // [] |
      "milestones[\(length)]{title,open,closed,description}:",
      (.[] | "  \(.title),\(.open_issues),\(.closed_issues),\(.description // "" | gsub("\n"; " "))")' ;;
  milestone)
    v=$(version "${1:-}"); shift; desc=""
    while [ $# -gt 0 ]; do case "$1" in --description) shift; desc="${1:-}" ;; *) wf_die "unknown argument $1" ;; esac; shift; done
    m=$(milestone_json "$v") || wf_die "cannot read milestones"
    if [ -n "$m" ]; then
      [ "$(printf '%s' "$m" | jq -r .state)" = open ] || wf_die "milestone $v is closed (released); pick a new version"
      wf_kv milestone "$v (existing, $(printf '%s' "$m" | jq -r '"\(.open_issues) open, \(.closed_issues) closed"'))"
    else
      gh api --method POST "repos/$(wf_repo_nwo)/milestones" -f title="$v" -f description="$desc" >/dev/null || wf_die "creating milestone $v failed"
      wf_kv milestone "$v (created)"
    fi ;;
  attach)
    n=$(wf_issue_num "${1:-}"); shift; milestone=""
    while [ $# -gt 0 ]; do case "$1" in --milestone) shift; milestone=$(version "${1:-}") ;; *) wf_die "unknown argument $1" ;; esac; shift; done
    [ -n "$milestone" ] || wf_die "attach needs --milestone <vX.Y.Z>"
    open_milestone "$milestone"
    gh issue edit "$n" --milestone "$milestone" >/dev/null || wf_die "gh issue edit failed"
    wf_kv milestone "#$n attached to $milestone" ;;
  label)
    n=$(wf_issue_num "${1:-}"); shift; args=(); add=(); remove=()
    while [ $# -gt 0 ]; do
      case "$1" in
        --add) shift; add+=("$1"); args+=(--add-label "$1") ;;
        --remove) shift; remove+=("$1"); args+=(--remove-label "$1") ;;
        *) wf_die "unknown argument $1" ;;
      esac; shift
    done
    [ "${#args[@]}" -gt 0 ] || wf_die "label needs --add or --remove"
    # The routing rule holds over the labels the issue ends up with, not over the ones this call names, so
    # what it carries now is read first: routing an issue that is already agent-ready is one --add.
    resulting=("${add[@]+"${add[@]}"}")
    current=$(wf_issue_labels "$n")
    while IFS= read -r l; do
      [ -n "$l" ] || continue
      if ! wf_labels_have "$l" "${remove[@]+"${remove[@]}"}"; then resulting+=("$l"); fi
    done <<EOF
$current
EOF
    # The fix line names the route this call can drop: the one it adds, or the one the issue already carries.
    if wf_labels_have "$WF_ROUTING_LABEL" "${add[@]+"${add[@]}"}"; then drop="leave --add $WF_ROUTING_LABEL off"
    else drop="take the routing label off with --remove $WF_ROUTING_LABEL"; fi
    wf_require_routable "#$n" "$drop" "${resulting[@]+"${resulting[@]}"}"
    gh issue edit "$n" "${args[@]}" >/dev/null || wf_die "gh issue edit failed (missing label? run labels.sh)"
    wf_kv labels "#$n updated" ;;
  comment)
    n=$(wf_issue_num "${1:-}"); shift; body=""
    while [ $# -gt 0 ]; do case "$1" in --body-file) shift; body="${1:-}" ;; *) wf_die "unknown argument $1" ;; esac; shift; done
    [ -f "$body" ] || wf_die "comment needs --body-file <existing file>"
    gh issue comment "$n" --body-file "$body" >/dev/null || wf_die "gh issue comment failed"
    wf_kv comment "#$n posted" ;;
  close)
    n=$(wf_issue_num "${1:-}"); shift; body="" reason=""
    while [ $# -gt 0 ]; do case "$1" in --comment-file) shift; body="${1:-}" ;; --reason) shift; reason="${1:-}" ;; *) wf_die "unknown argument $1" ;; esac; shift; done
    args=(); [ -n "$body" ] && args+=(--comment "$(cat "$body")"); [ -n "$reason" ] && args+=(--reason "$reason")
    gh issue close "$n" "${args[@]+"${args[@]}"}" >/dev/null || wf_die "gh issue close failed"
    wf_kv closed "#$n${reason:+ ($reason)}" ;;
  -h|--help) usage 0 ;;
  *) wf_die "unknown command $cmd" ;;
esac
