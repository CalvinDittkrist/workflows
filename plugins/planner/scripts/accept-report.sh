#!/usr/bin/env bash
# The report of a spec acceptance: the checker's item lines, counted per section and per verdict, with every
# item that is not met, and a warning for a checkable section of the spec the checker left out.
# Usage: accept-report.sh <spec> [<file>...]   (reads stdin without files)
# Input: the spec checker's reply as it is. Only lines of the fixed format count, other text is ignored:
#   item: <section> | <statement> | <verdict> | <evidence> | <confidence>
# section: User stories, Decisions, Testing, Vocabulary, ADRs to write; verdict: met, missing, deviates,
# untested; confidence: high, medium, low. One malformed item line fails the whole report: a dropped or
# reworded verdict must never pass as a report. Nothing is written, on GitHub or in the repository.
set -euo pipefail
. "$(dirname "$0")/lib.sh"
usage() { sed -n '2,4p' "$0"; exit "${1:-0}"; }
case "${1:-}" in -h|--help) usage 0 ;; "") wf_die "usage: accept-report.sh <spec> [<file>...]" ;; esac
spec=$(wf_issue_num "$1"); shift
for f in "$@"; do [ -f "$f" ] || wf_die "$f not found; pass the file the checker's reply was saved to"; done

# The sections a spec can be checked against, in the order the report prints them.
SECTIONS='User stories|Decisions|Testing|Vocabulary|ADRs to write'

# Parse and validate: one tab-separated record per item, in the checker's order.
parsed=$(cat "$@" | SECTIONS="$SECTIONS" awk '
  function trim(s) { gsub(/^[[:space:]]+|[[:space:]]+$/, "", s); gsub(/\t/, " ", s); return s }
  BEGIN { n = split(ENVIRON["SECTIONS"], s, "|"); for (i = 1; i <= n; i++) S[tolower(s[i])] = s[i]
          split("met missing deviates untested", v, " "); for (i in v) V[v[i]] = 1
          split("high medium low", k, " "); for (i in k) K[k[i]] = 1 }
  { line = $0; sub(/\r$/, "", line); sub(/^[[:space:]]*([-*][[:space:]]+)?`?/, "", line); sub(/`[[:space:]]*$/, "", line)
    gsub(/[[:cntrl:]]/, " ", line) }
  line !~ /^item:/ { next }
  {
    body = line; sub(/^item:[[:space:]]*/, "", body)
    n = split(body, f, "|"); for (i = 1; i <= n; i++) f[i] = trim(f[i])
    why = ""
    if (n != 5) why = "has " n " field(s), needs 5: section | statement | verdict | evidence | confidence"
    else if (!(tolower(f[1]) in S)) why = "unknown section " f[1] "; use one of " ENVIRON["SECTIONS"]
    else if (f[2] == "") why = "empty statement"
    else if (!(tolower(f[3]) in V)) why = "unknown verdict " f[3] "; use met, missing, deviates or untested"
    else if (f[4] == "") why = "empty evidence"
    else if (!(tolower(f[5]) in K)) why = "unknown confidence " f[5] "; use high, medium or low"
    if (why != "") { print "error: " why ": " line > "/dev/stderr"; bad = 1; next }
    print S[tolower(f[1])] "\t" f[2] "\t" tolower(f[3]) "\t" f[4] "\t" tolower(f[5])
  }
  END { exit bad }') || wf_die "malformed item line(s), no report; correct their format and run accept-report.sh again"
[ -n "$parsed" ] || wf_die "no item: lines in the reply; the checker must answer in the fixed format, one line per checkable statement"

# The checkable sections of the spec itself, so a section the checker left out is visible. A section that is
# absent or says "none" is not checkable. An unreadable spec costs the comparison, not the report.
present=""
nwo=""
if command -v gh >/dev/null 2>&1 && command -v jq >/dev/null 2>&1; then nwo=$(wf_repo_nwo 2>/dev/null) || nwo=""; fi
if [ -n "$nwo" ] && sj=$(gh api "repos/$nwo/issues/$spec" 2>/dev/null); then
  present=$(printf '%s' "$sj" | jq -r '.body // ""' | SECTIONS="$SECTIONS" awk '
    function trim(s) { gsub(/^[[:space:]\r]+|[[:space:]\r]+$/, "", s); return s }
    BEGIN { n = split(ENVIRON["SECTIONS"], s, "|"); for (i = 1; i <= n; i++) S[tolower(s[i])] = s[i] }
    /^#+ / { sec = trim(substr($0, index($0, " ") + 1)); first[tolower(sec)] = ""; next }
    { t = trim($0)
      if (t != "" && tolower(sec) in S && first[tolower(sec)] == "") first[tolower(sec)] = t }
    END { for (k in first) if (first[k] != "" && tolower(first[k]) !~ /^none([[:space:][:punct:]]|$)/) print S[k] }')
else
  wf_warn "could not read #$spec; the report cannot say whether the checker left a section of the spec out"
fi

printf '%s\n' "$parsed" | SECTIONS="$SECTIONS" PRESENT="$present" SPEC="$spec" awk -F'\t' '
  BEGIN { ns = split(ENVIRON["SECTIONS"], order, "|")
          nv = split("met missing deviates untested", verdicts, " ")
          np = split(ENVIRON["PRESENT"], p, "\n"); for (i = 1; i <= np; i++) if (p[i] != "") checkable[p[i]] = 1 }
  { total++; n[$1]++; per[$1, $3]++; v[$3]++
    if ($3 != "met") { open++; rows = rows sprintf("  %s (%s) %s | %s | %s\n", $3, $5, $1, $2, $4) } }
  END {
    secs = 0; for (i = 1; i <= ns; i++) if (order[i] in n) secs++
    for (i = 1; i <= nv; i++) if (!(verdicts[i] in v)) v[verdicts[i]] = 0   # a count of zero prints as 0
    printf "items: %d in %d section(s); %d met, %d open\n", total, secs, v["met"], open
    line = ""; for (i = 1; i <= nv; i++) line = line (line == "" ? "" : ", ") verdicts[i] " " v[verdicts[i]]
    printf "verdicts: %s\n", line
    for (i = 1; i <= ns; i++) {
      c = order[i]
      if (!(c in n)) { if (c in checkable) printf "warning: section %s of #%s has no item; the checker left it out\n", c, ENVIRON["SPEC"] > "/dev/stderr"
                       continue }
      counts = ""; for (j = 1; j <= nv; j++) if ((c, verdicts[j]) in per) counts = counts (counts == "" ? "" : ", ") verdicts[j] " " per[c, verdicts[j]]
      printf "%s: %d item(s) (%s)\n", c, n[c], counts
    }
    if (open) { printf "\nopen[%d]{verdict,confidence,section,statement,evidence}:\n%s", open, rows
                print "\nnext: decide per open item: a gap ticket, an accepted deviation, or no finding" }
    else print "\nnext: nothing is open; the spec can be closed with accept-close.sh once every decision is recorded"
  }'
