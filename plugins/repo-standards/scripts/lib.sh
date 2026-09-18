#!/usr/bin/env bash
# Helpers shared by the repo-standards scripts. Sourced, never run.

# has <dir> <name>: a file or directory of exactly this name is in dir. macOS file systems ignore case,
# so [ -e ] would accept claude.md for CLAUDE.md.
has() { local e; for e in "$1"/*; do [ "${e##*/}" = "$2" ] && return 0; done; return 1; }
# first_of <dir> <name>...: the first name present in dir, exact case.
first_of() { local dir=$1 n; shift; for n in "$@"; do has "$dir" "$n" && { printf '%s' "$n"; return; }; done; }
# The standardisation run: the six finding categories, one per auditor, in report order, and the state
# directory inside the git directory, so the audit never changes the working tree.
# shellcheck disable=SC2034 # used by the scripts that source this file
WF_CATEGORIES="files agent-config docs tests-ci workspace security"
state_dir() {
  local d
  d=$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null) || { printf 'error: not inside a git repository; run git init first\n' >&2; return 1; }
  printf '%s/standardize' "$d"
}

# workflow_jobs <file>: the jobs of a GitHub Actions workflow as `id` or `id ("name")`, comma separated,
# name only when it differs from the id (GitHub shows the name as the check).
workflow_jobs() {
  awk '
    /^jobs:[[:space:]]*$/ { in_jobs = 1; ind = 0; next }
    in_jobs && /^[^[:space:]#]/ { in_jobs = 0 }
    !in_jobs || /^[[:space:]]*(#|$)/ { next }
    { match($0, /^ */); d = RLENGTH }
    ind == 0 { ind = d }
    d == ind && /^ *[A-Za-z0-9_-]+:/ { id = $0; sub(/^ */, "", id); sub(/:.*/, "", id); ids[++n] = id; next }
    n && d > ind && sd[n] == "" { sd[n] = d }
    n && d == sd[n] && /^ *name:/ { v = $0; sub(/^ *name:[[:space:]]*/, "", v); sub(/[[:space:]]+$/, "", v); gsub(/^["\047]|["\047]$/, "", v); nm[n] = v }
    END { for (i = 1; i <= n; i++) printf "%s%s%s", (i > 1 ? ", " : ""), ids[i], (nm[i] != "" && nm[i] != ids[i] ? " (\"" nm[i] "\")" : "") }
  ' "$1"
}
# is_check_job: the workflow_jobs output on stdin has a job GitHub reports as the check `check`.
is_check_job() { tr ',' '\n' | sed -E 's/^ +//' | grep -Eq '^check$|\("check"\)$'; }

# The apply phase. Everything it creates on GitHub is found again by these names, so a second run updates
# instead of duplicating: the tag, its ruleset, the catalogue issue, the cleanup branch.
# shellcheck disable=SC2034
WF_TAG=pre-standard WF_BRANCH=chore/standardize WF_CATALOGUE="Standardisation: removed skills and how to restore them"
# The ruleset that protects the tag from deletion and moving; workspace.sh wants the same one.
tag_ruleset() {
  jq -cn --arg t "$WF_TAG" '{name: ("standard: " + $t), target: "tag", enforcement: "active", bypass_actors: [],
    conditions: {ref_name: {include: ["refs/tags/" + $t], exclude: []}}, rules: [{type: "deletion"}, {type: "update"}]}'
}
# The workflow's label vocabulary (plugins/planner/scripts/labels.sh) plus skill-candidate: name|color|description.
WF_LABELS='ready-for-agent|0E8A16|Fully specified; an agent can take it
needs-triage|FBCA04|A maintainer has to evaluate this
needs-info|D876E3|Waiting on the reporter
ready-for-human|1D76DB|Needs a human to implement
wontfix|FFFFFF|Will not be actioned; the closing comment says why
spec|5319E7|Spec issue; its tickets carry the work
bug|D73A4A|Something is broken
enhancement|A2EEEF|New feature or improvement
skill-candidate|C5DEF5|A removed skill that could move into the marketplace'
# label_json <name>: the create body of one vocabulary label.
label_json() {
  printf '%s\n' "$WF_LABELS" | awk -F'|' -v n="$1" '$1 == n' | { IFS='|' read -r name color desc
    jq -cn --arg n "$name" --arg c "$color" --arg d "$desc" '{name: $n, color: $c, description: $d}'; }
}

# decisions: the recorded answer per category of the last report, `<category>\t<approve|reject>`. Fails while
# the audit has not run or a category is still pending, so nothing is applied that was not answered.
decisions() {
  local dir cats c v out=""
  dir=$(state_dir) || return 1
  [ -f "$dir/findings" ] || { printf 'error: no findings recorded; run /repo-standards:standardize first\n' >&2; return 1; }
  cats=$(cut -f1 "$dir/findings" | awk '!seen[$0]++')
  for c in $cats; do
    v=$(awk -F'\t' -v c="$c" '$1 == c { v = $2 } END { print v }' "$dir/approvals" 2>/dev/null)
    [ -n "$v" ] || { printf 'error: %s is still pending; record it with approve.sh %s=approve|reject\n' "$c" "$c" >&2; return 1; }
    out="$out$c"$'\t'"$v"$'\n'
  done
  printf '%s' "$out"
}
# approved_findings <decisions> <action>: the findings of approved categories with this action, as stored.
approved_findings() {
  awk -F'\t' -v ok="$(categories "$1" approve)" -v a="$2" 'BEGIN { n = split(ok, c, " "); for (i = 1; i <= n; i++) O[c[i]] = 1 }
    ($1 in O) && $3 == a' "$(state_dir)/findings"
}
# categories <decisions> <approve|reject>: the categories with this answer, space separated.
categories() { printf '%s' "$1" | awk -F'\t' -v v="$2" '$2 == v { printf "%s%s", (n++ ? " " : ""), $1 }'; }
