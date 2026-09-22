#!/usr/bin/env bash
# Record the maintainer's answer per category of the last report (report.sh), and print the state of all.
# Usage: approve.sh [<category>=approve|reject ...]   (no arguments: print the state only)
# The answers go to <git dir>/standardize/approvals; a later answer for a category replaces the earlier one.
# An unanswered category is listed as `pending` when it has findings, which stops the apply phase, and as
# `unanswered` when it is only scaffolded, which does not: the apply phase scaffolds it as an approval would,
# and rejecting it is what keeps the apply phase out (ADR 0035).
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
dir=$(state_dir)
[ -f "$dir/findings" ] || die "no findings recorded; run the audit and report.sh first"
# The categories the report asked about: those with findings and the scaffolded ones, which the apply phase
# creates baseline files for whether a finding lists them or not (ADR 0035).
cats=$(answerable)

# Validate every argument before recording any, so a typo records nothing.
for a in "$@"; do
  c=${a%%=*}; v=${a#*=}
  [ "$c" != "$a" ] || die "$a: use <category>=approve or <category>=reject"
  in_list "$c" "$cats" || die "$c is not in the last report and is not scaffolded, so there is nothing to answer for it; the report asks about: $cats"
  case "$v" in approve|reject) ;; *) die "$a: the answer is approve or reject" ;; esac
done
touch "$dir/approvals"
for a in "$@"; do
  c=${a%%=*}; v=${a#*=}
  { awk -F'\t' -v c="$c" '$1 != c' "$dir/approvals"; printf '%s\t%s\n' "$c" "$v"; } > "$dir/approvals.tmp"
  mv "$dir/approvals.tmp" "$dir/approvals"
done

approved="" rejected="" pending="" unanswered=""
for c in $cats; do
  case "$(awk -F'\t' -v c="$c" '$1 == c { v = $2 } END { print v }' "$dir/approvals")" in
    approve) approved="$approved $c" ;;
    reject) rejected="$rejected $c" ;;
    *) if has_findings "$c"; then pending="$pending $c"; else unanswered="$unanswered $c"; fi ;;
  esac
done
list() { if [ -n "$1" ]; then printf '%s' "${1# }" | sed 's/ /, /g'; else printf 'none'; fi; }
printf 'approved: %s\nrejected: %s\npending: %s\nunanswered: %s\n' \
  "$(list "$approved")" "$(list "$rejected")" "$(list "$pending")" "$(list "$unanswered")"
[ -z "$unanswered" ] || printf 'note: the unanswered categories have no findings; the apply phase scaffolds them unless they are rejected\n'
