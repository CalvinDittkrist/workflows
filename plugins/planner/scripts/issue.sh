#!/usr/bin/env bash
# GitHub issue operations the planner skills need, with the fiddly parts (sub-issues, blocking edges) inside.
# Usage: issue.sh create --title <t> --body-file <f> [--label <l>]... [--parent <n>]
#        issue.sh block <n> --by <m>[,<m>...]
#        issue.sh label <n> [--add <l>]... [--remove <l>]...
#        issue.sh comment <n> --body-file <f>
#        issue.sh close <n> [--comment-file <f>] [--reason completed|not-planned]
set -euo pipefail
. "$(dirname "$0")/lib.sh"
wf_need gh; wf_need jq
usage() { sed -n '3,7p' "$0"; exit "${1:-0}"; }
cmd="${1:-}"; [ -n "$cmd" ] || usage 1; shift
num() { local n="${1#\#}"; printf '%s' "$n" | grep -Eq '^[0-9]+$' || wf_die "issue must be a number, got '$1'"; printf '%s' "$n"; }

case "$cmd" in
  create)
    title="" body="" parent=""; labels=()
    while [ $# -gt 0 ]; do
      case "$1" in
        --title) shift; title="${1:-}" ;;
        --body-file) shift; body="${1:-}" ;;
        --label) shift; labels+=("$1") ;;
        --parent) shift; parent=$(num "${1:-}") ;;
        *) wf_die "unknown argument $1" ;;
      esac; shift
    done
    [ -n "$title" ] && [ -n "$body" ] || wf_die "create needs --title and --body-file"
    [ -f "$body" ] || wf_die "body file $body not found"
    args=(); for l in "${labels[@]+"${labels[@]}"}"; do args+=(--label "$l"); done
    url=$(gh issue create --title "$title" --body-file "$body" "${args[@]+"${args[@]}"}") || wf_die "gh issue create failed (missing label? run labels.sh)"
    n="${url##*/}"
    wf_kv issue "#$n"; wf_kv url "$url"
    if [ -n "$parent" ]; then
      id=$(wf_issue_db_id "$n")
      if [ -n "$id" ] && gh api --method POST "repos/$(wf_repo_nwo)/issues/$parent/sub_issues" -F sub_issue_id="$id" >/dev/null 2>&1; then
        wf_kv parent "#$parent (sub-issue)"
      else
        wf_kv parent "#$parent (body only; sub-issues unavailable here)"
      fi
    fi ;;
  block)
    n=$(num "${1:-}"); shift; by=""
    while [ $# -gt 0 ]; do case "$1" in --by) shift; by="${1:-}" ;; *) wf_die "unknown argument $1" ;; esac; shift; done
    [ -n "$by" ] || wf_die "block needs --by <m>[,<m>...]"
    nwo=$(wf_repo_nwo)
    for m in $(printf '%s' "$by" | tr ',' ' '); do
      m=$(num "$m"); id=$(wf_issue_db_id "$m")
      if [ -n "$id" ] && gh api --method POST "repos/$nwo/issues/$n/dependencies/blocked_by" -F issue_id="$id" >/dev/null 2>&1; then
        wf_kv blocked "#$n by #$m (native)"
      else
        wf_kv blocked "#$n by #$m (body only; dependencies unavailable here)"
      fi
    done ;;
  label)
    n=$(num "${1:-}"); shift; args=()
    while [ $# -gt 0 ]; do case "$1" in --add) shift; args+=(--add-label "$1") ;; --remove) shift; args+=(--remove-label "$1") ;; *) wf_die "unknown argument $1" ;; esac; shift; done
    [ "${#args[@]}" -gt 0 ] || wf_die "label needs --add or --remove"
    gh issue edit "$n" "${args[@]}" >/dev/null || wf_die "gh issue edit failed (missing label? run labels.sh)"
    wf_kv labels "#$n updated" ;;
  comment)
    n=$(num "${1:-}"); shift; body=""
    while [ $# -gt 0 ]; do case "$1" in --body-file) shift; body="${1:-}" ;; *) wf_die "unknown argument $1" ;; esac; shift; done
    [ -f "$body" ] || wf_die "comment needs --body-file <existing file>"
    gh issue comment "$n" --body-file "$body" >/dev/null || wf_die "gh issue comment failed"
    wf_kv comment "#$n posted" ;;
  close)
    n=$(num "${1:-}"); shift; body="" reason=""
    while [ $# -gt 0 ]; do case "$1" in --comment-file) shift; body="${1:-}" ;; --reason) shift; reason="${1:-}" ;; *) wf_die "unknown argument $1" ;; esac; shift; done
    args=(); [ -n "$body" ] && args+=(--comment "$(cat "$body")"); [ -n "$reason" ] && args+=(--reason "$reason")
    gh issue close "$n" "${args[@]+"${args[@]}"}" >/dev/null || wf_die "gh issue close failed"
    wf_kv closed "#$n${reason:+ ($reason)}" ;;
  -h|--help) usage 0 ;;
  *) wf_die "unknown command $cmd" ;;
esac
