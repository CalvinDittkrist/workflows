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
# ci_check_workflow <root>: the first workflow, relative to root, with the job check; empty when none has one.
# It reads the file system, like the other baseline lookups, so an ignored workflow counts too.
ci_check_workflow() {
  local w
  for w in "$1"/.github/workflows/*.yml "$1"/.github/workflows/*.yaml; do
    [ -f "$w" ] && workflow_jobs "$w" | is_check_job && { printf '%s' "${w#"$1"/}"; return; }
  done; return 0
}

# The names the README and the licence may have, the standard's name first. facts.sh, check.sh and scaffold.sh
# accept exactly these, so the audit, the check and the scaffold agree on what exists. The other baseline files
# have one or two names each, listed where they are used (the Makefile in make's order of precedence).
# shellcheck disable=SC2034
WF_README_NAMES="README.md README.rst README.txt README readme.md" WF_LICENSE_NAMES="LICENSE LICENSE.md LICENSE.txt COPYING"

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

# The GitHub side of the apply phase. Each prints an `error:` line and returns 1 when GitHub cannot be read.
gh_fail() { printf 'error: %s: %s\n' "$1" "$(tail -n1 "$2")" >&2; rm -f "$2"; return 1; }
# github_repo: sets nwo (owner/name) and default (the default branch).
github_repo() {
  local e; e=$(mktemp)
  nwo=$(gh repo view --json nameWithOwner -q .nameWithOwner 2>"$e") || { gh_fail "cannot read the GitHub repository (run gh auth status)" "$e"; return 1; }
  default=$(gh api "repos/$nwo" 2>"$e" | jq -r '.default_branch // empty') || { gh_fail "cannot read repos/$nwo" "$e"; return 1; }
  rm -f "$e"
  [ -n "$default" ] || { printf 'error: cannot read the default branch of %s\n' "$nwo" >&2; return 1; }
}
# catalogue_issue: the number of the catalogue issue, the oldest when several issues carry its title; empty when none.
catalogue_issue() {
  local e out; e=$(mktemp)
  out=$(gh api --paginate "repos/$nwo/issues?labels=skill-candidate&state=all&per_page=100" 2>"$e") || { gh_fail "cannot list the skill-candidate issues" "$e"; return 1; }
  rm -f "$e"
  printf '%s' "$out" | jq -s -r --arg t "$WF_CATALOGUE" '[add // [] | .[] | select(.title == $t and .pull_request == null)] | sort_by(.number) | first // empty | .number'
}
# branch_pulls: the pull requests from the cleanup branch, newest first, as {number, url, body, state, sha}; state
# is open, closed (without a merge) or merged, sha the head commit.
branch_pulls() {
  local e out; e=$(mktemp)
  out=$(gh api --paginate "repos/$nwo/pulls?head=${nwo%%/*}:$WF_BRANCH&state=all&per_page=100" 2>"$e") || { gh_fail "cannot list the pull requests from $WF_BRANCH" "$e"; return 1; }
  rm -f "$e"
  printf '%s' "$out" | jq -s -c '[add // [] | .[] | {number, url: .html_url, body: (.body // ""), sha: .head.sha,
    state: (if .merged_at then "merged" else .state end)}] | sort_by(-.number)'
}
# cleanup_worktree: the path of the cleanup worktree, inside the main checkout like every workflow worktree.
cleanup_worktree() {
  printf '%s/.claude/worktrees/%s' "$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")" "$(printf '%s' "$WF_BRANCH" | tr '/' '-')"
}
# remote_ref <ref>: the commit a ref has on origin; empty when origin has no such ref, an error when origin cannot be reached.
remote_ref() {
  local e out; e=$(mktemp)
  out=$(git ls-remote origin "$1" 2>"$e") || { gh_fail "cannot reach origin" "$e"; return 1; }
  rm -f "$e"; printf '%s' "$out" | head -n1 | cut -f1
}
# ensure_label <name>: create a label of the vocabulary unless the repository has it (in any case).
ensure_label() {
  local e out; e=$(mktemp)
  out=$(gh api --paginate "repos/$nwo/labels?per_page=100" 2>"$e") || { gh_fail "cannot read the labels of $nwo" "$e"; return 1; }
  if ! printf '%s' "$out" | jq -s -e --arg n "$1" 'add // [] | any(.[]; (.name | ascii_downcase) == $n)' >/dev/null; then
    label_json "$1" | gh api --method POST "repos/$nwo/labels" --input - >/dev/null 2>"$e" || { gh_fail "cannot create the label $1" "$e"; return 1; }
  fi
  rm -f "$e"
}
