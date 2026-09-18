#!/usr/bin/env bash
# Record the maintainer's answer per category of the last report (report.sh), and print the state of all.
# Usage: approve.sh [<category>=approve|reject ...]   (no arguments: print the state only)
# The answers go to <git dir>/standardize/approvals; a later answer for a category replaces the earlier one.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
dir=$(state_dir)
[ -f "$dir/findings" ] || die "no findings recorded; run the audit and report.sh first"
cats=$(cut -f1 "$dir/findings" | awk '!seen[$0]++')
[ -n "$cats" ] || die "the last report has no findings, so there is nothing to approve"

# Validate every argument before recording any, so a typo records nothing.
for a in "$@"; do
  c=${a%%=*}; v=${a#*=}
  [ "$c" != "$a" ] || die "$a: use <category>=approve or <category>=reject"
  printf '%s\n' "$cats" | grep -qxF -- "$c" || die "$c has no findings in the last report; categories with findings: $(printf '%s' "$cats" | tr '\n' ' ')"
  case "$v" in approve|reject) ;; *) die "$a: the answer is approve or reject" ;; esac
done
touch "$dir/approvals"
for a in "$@"; do
  c=${a%%=*}; v=${a#*=}
  { awk -F'\t' -v c="$c" '$1 != c' "$dir/approvals"; printf '%s\t%s\n' "$c" "$v"; } > "$dir/approvals.tmp"
  mv "$dir/approvals.tmp" "$dir/approvals"
done

approved="" rejected="" pending=""
for c in $WF_CATEGORIES; do
  printf '%s\n' "$cats" | grep -qxF -- "$c" || continue
  case "$(awk -F'\t' -v c="$c" '$1 == c { v = $2 } END { print v }' "$dir/approvals")" in
    approve) approved="$approved $c" ;;
    reject) rejected="$rejected $c" ;;
    *) pending="$pending $c" ;;
  esac
done
list() { if [ -n "$1" ]; then printf '%s' "${1# }" | sed 's/ /, /g'; else printf 'none'; fi; }
printf 'approved: %s\nrejected: %s\npending: %s\n' "$(list "$approved")" "$(list "$rejected")" "$(list "$pending")"
