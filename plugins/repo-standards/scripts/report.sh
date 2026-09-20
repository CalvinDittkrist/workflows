#!/usr/bin/env bash
# Merge the auditors' findings into one report grouped by category, and store them for the apply phase.
# Usage: report.sh [<file>...]   (reads stdin without files)
# Input: the auditors' replies as they are. Only lines of the fixed format count, other text is ignored:
#   finding: <category> | <target> | <action> | <reason> | <confidence>
# category: files, agent-config, docs, tests-ci, workspace, security; action: delete, replace, create, configure,
# issue; confidence: high, medium, low. A target other than a GitHub setting (configure) is a path inside the
# repository: no leading / or ~, no .. segment; only the workspace category configures; no target starts with -. Any malformed finding line fails the whole report and stores nothing.
# Per category the report keeps the findings the run works through one by one (delete, replace, create, and
# issue apart) away from the `configure` ones, which only describe what workspace.sh decides for itself, and
# it says what approving the category triggers beyond its lines; approval stays per category (ADR 0016).
# The findings go to <git dir>/standardize/findings; earlier approvals and the record of what the apply phase
# already applied are cleared, because they answered another report. Nothing in the working tree or on GitHub changes.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"
for f in "$@"; do [ -f "$f" ] || { printf 'error: %s not found; pass the file the auditor replies were saved to\n' "$f" >&2; exit 1; }; done
dir=$(state_dir)

# Parse and validate: one tab-separated record per finding, duplicates dropped.
parsed=$(cat "$@" | CATS="$WF_CATEGORIES" awk '
  function trim(s) { gsub(/^[[:space:]]+|[[:space:]]+$/, "", s); gsub(/\t/, " ", s); return s }
  BEGIN { n = split(ENVIRON["CATS"], c, " "); for (i = 1; i <= n; i++) C[c[i]] = 1
          split("delete replace create configure issue", a, " "); for (i in a) A[a[i]] = 1
          split("high medium low", k, " "); for (i in k) K[k[i]] = 1 }
  { line = $0; sub(/\r$/, "", line); sub(/^[[:space:]]*([-*][[:space:]]+)?`?/, "", line); sub(/`[[:space:]]*$/, "", line) }
  line !~ /^finding:/ { next }
  {
    body = line; sub(/^finding:[[:space:]]*/, "", body)
    n = split(body, f, "|"); for (i = 1; i <= n; i++) f[i] = trim(f[i])
    why = ""
    if (n != 5) why = "has " n " fields, needs 5: category | target | action | reason | confidence"
    else if (!(f[1] in C)) why = "unknown category " f[1] "; use one of " ENVIRON["CATS"]
    else if (f[2] == "") why = "empty target"
    else if (!(f[3] in A)) why = "unknown action " f[3] "; use delete, replace, create, configure or issue"
    else if (f[3] == "configure" && f[1] != "workspace") why = "configure is for GitHub settings, which only the workspace category proposes"
    else if (f[3] != "configure" && (f[2] ~ /^[\/~]/ || f[2] ~ /(^|\/)\.\.(\/|$)/)) why = "target " f[2] " leaves the repository; use a path relative to its root"
    else if (f[2] ~ /^-/) why = "target " f[2] " starts with -; name the path without a leading dash"
    else if (f[4] == "") why = "empty reason"
    else if (!(tolower(f[5]) in K)) why = "unknown confidence " f[5] "; use high, medium or low"
    if (why != "") { print "error: " why ": " line > "/dev/stderr"; bad = 1; next }
    key = f[1] "\t" f[2] "\t" f[3]
    if (!(key in seen)) { seen[key] = 1; print f[1] "\t" f[2] "\t" f[3] "\t" f[4] "\t" tolower(f[5]) }
  }
  END { exit bad }') || { printf 'error: malformed findings, nothing stored; correct those lines and run report.sh again\n' >&2; exit 1; }

mkdir -p "$dir"
printf '%s\n' "$parsed" | awk 'NF' > "$dir/findings"
# A new report answers for a new run: the approvals and the settings the last run worked on go.
rm -f "$dir/approvals" "$dir/workspace-handled"

total=$(awk 'END { print NR }' "$dir/findings")
if [ "$total" = 0 ]; then
  printf 'findings: 0; the repository matches the standard, nothing to approve\n'
  exit 0
fi
CATS="$WF_CATEGORIES" SCAFFOLDED="$WF_SCAFFOLD_CATEGORIES" awk -F'\t' '
  BEGIN { nc = split(ENVIRON["CATS"], order, " ")
          ns = split(ENVIRON["SCAFFOLDED"], s, " "); for (i = 1; i <= ns; i++) S[s[i]] = 1 }
  # First pass: which categories delete each target, so a target two auditors delete is shown in both.
  FNR == NR { if ($3 == "delete") by[$2] = by[$2] (by[$2] == "" ? "" : ", ") $1; next }
  { total++; n[$1]++; act[$1, $3]++; if ($3 == "issue") issues++; else runs++
    r = "    " $3 " " $2 ": " $4 " (" $5 ")"
    # The action decides the group, not the category: delete, replace and create name a target the run works
    # through one by one, configure names a setting workspace.sh decides for itself (ADR 0016).
    if ($3 == "issue") iss[$1] = iss[$1] r "\n"
    else if ($3 == "configure") dec[$1] = dec[$1] r "\n"
    else per[$1] = per[$1] r "\n"
    if ($3 == "delete") { o = by[$2]; gsub("(^|, )" $1 "(, |$)", ", ", o); gsub(/^, |, $/, "", o)
      del[$1] = del[$1] (del[$1] == "" ? "" : ", ") $2 (o == "" ? "" : " (also " o ")") } }
  END {
    cats = 0; for (i = 1; i <= nc; i++) if (order[i] in n) cats++
    printf "findings: %d in %d categor%s; %d for the run, %d as issues\n", total, cats, (cats == 1 ? "y" : "ies"), runs, issues
    split("delete replace create configure issue", acts, " ")
    for (i = 1; i <= nc; i++) {
      c = order[i]; if (!(c in n)) continue
      counts = ""; for (j = 1; j <= 5; j++) if ((c, acts[j]) in act) counts = counts (counts == "" ? "" : ", ") acts[j] " " act[c, acts[j]]
      printf "\n%s: %d finding%s (%s)\n", c, n[c], (n[c] == 1 ? "" : "s"), counts
      printf "  deletes: %s\n", (c in del) ? del[c] : "nothing"
      if (c in per) printf "  the run performs, one by one:\n%s", per[c]; else print "  the run performs, one by one: nothing"
      if (c in dec) {
        printf "  workspace.sh decides these; the lines are what it found at the audit:\n%s", dec[c]
        printf "  approving %s applies the whole difference between the GitHub workspace and the standard, recomputed after the cleanup pull request is merged, so it can differ from the lines above\n", c
      }
      if (c in S) printf "  approving %s %screates every baseline file of the category that is missing, whether a finding above lists it or not\n",
                         c, ((c in per) || (c in dec)) ? "also " : ""
      if (c in iss) printf "  become issues:\n%s", iss[c]; else print "  become issues: none"
    }
    # A category is left alone only when it is rejected, and a category without findings cannot be answered at
    # all, so the apply phase scaffolds it. The report names it; what the apply phase applies is unchanged.
    miss = ""; mn = 0
    for (i = 1; i <= nc; i++) { c = order[i]; if ((c in S) && !(c in n)) { miss = miss (mn++ ? ", " : "") c } }
    if (miss != "") printf "\nalso: %s %s no findings, so the report does not ask about %s; the apply phase still creates %s missing baseline files, because only a rejected category is left alone\n",
                           miss, (mn == 1 ? "has" : "have"), (mn == 1 ? "it" : "them"), (mn == 1 ? "its" : "their")
  }' "$dir/findings" "$dir/findings"
printf '\nnext: ask for approval per category, then record the answers with approve.sh <category>=approve|reject ...\n'
