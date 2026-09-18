#!/usr/bin/env bash
# Bring the GitHub workspace of this repository to the standard (docs/repo-standard.md, ADR 0011).
# Usage: workspace.sh [--apply] [--snapshot <file>]
# Without --apply nothing changes: it derives the profile, reads the current state and prints one
# `diff:` line per difference. --apply first writes the previous state to the snapshot file (default:
# a new temporary file, printed as `snapshot:`), then makes exactly those changes. Run it again to verify.
# Env: WF_PROJECT_TEMPLATE=<owner>/<number>, the project copied when the repository has none linked.
set -euo pipefail
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

apply=0 snap=""
while [ $# -gt 0 ]; do
  case "$1" in
    --apply) apply=1 ;;
    --snapshot) [ $# -ge 2 ] || die "--snapshot needs a file"; snap="$2"; shift ;;
    -h|--help) sed -n '2,7p' "$0"; exit 0 ;;
    *) die "unknown argument $1; usage: workspace.sh [--apply] [--snapshot <file>]" ;;
  esac
  shift
done
for c in gh jq; do command -v "$c" >/dev/null 2>&1 || die "$c is required but not on PATH"; done
tpl="${WF_PROJECT_TEMPLATE:-}"
if [ -n "$tpl" ] && ! printf '%s' "$tpl" | grep -Eq '^[A-Za-z0-9-]+/[0-9]+$'; then
  die "WF_PROJECT_TEMPLATE must look like <owner>/<number>, got '$tpl'"
fi

err=$(mktemp); trap 'rm -f "$err"' EXIT
# get <path>: a GitHub API GET; every page of a list merged into one array. Any failure is fatal.
get() {
  local out
  out=$(gh api --paginate "$1" 2>"$err") || die "cannot read $1: $(tail -n1 "$err")"
  printf '%s' "$out" | jq -s 'if length > 1 then add else (.[0] // null) end'
}
# get_opt <path>: like get, but GitHub's 404 prints nothing instead of failing.
get_opt() {
  local out
  if out=$(gh api "$1" 2>"$err"); then printf '%s' "${out:-true}"
  elif grep -q 'HTTP 404' "$err"; then return 0
  else die "cannot read $1: $(tail -n1 "$err")"; fi
}
# send <method> <path> [<json body>]: one change; the body goes to gh on stdin.
send() {
  if [ $# -ge 3 ]; then printf '%s' "$3" | gh api --method "$1" "$2" --input - >/dev/null 2>"$err"
  else gh api --method "$1" "$2" >/dev/null 2>"$err"; fi || die "$1 $2 failed: $(tail -n1 "$err"); the snapshot has the state before this run"
}

nwo=$(gh repo view --json nameWithOwner -q .nameWithOwner 2>"$err") || die "cannot read the GitHub repository: $(tail -n1 "$err"); run gh auth status"
owner=${nwo%%/*}
repo=$(get "repos/$nwo")
[ "$(printf '%s' "$repo" | jq -r '.permissions.admin // false')" = true ] || die "admin rights on $nwo are needed to read and change its settings"
visibility=$(printf '%s' "$repo" | jq -r .visibility)
default=$(printf '%s' "$repo" | jq -r .default_branch)
# The profile (ADR 0009): dev plus main when the default branch is dev, otherwise main alone.
if [ "$default" = dev ]; then model="dev+main"; branches="main dev"; else model=main; branches=main; fi

diffs="" n=0 manual=""
found() { diffs="$diffs$*"$'\n'; n=$((n + 1)); } # one difference to the standard
step() { manual="$manual$*"$'\n'; }          # something only a person can change

# Merge settings, wiki and discussions. With dev plus main the promotion needs merge commits.
merge_commit=false; [ "$model" = main ] || merge_commit=true
want_repo=$(jq -cn --argjson mc "$merge_commit" '{allow_squash_merge: true, allow_merge_commit: $mc, allow_rebase_merge: false,
  delete_branch_on_merge: true, squash_merge_commit_title: "PR_TITLE", has_wiki: false, has_discussions: false}')
repo_patch=$(printf '%s' "$repo" | jq -c --argjson w "$want_repo" '. as $r | $w | with_entries(select($r[.key] != .value))')
while IFS= read -r l; do [ -z "$l" ] || found "repo $l"; done <<EOF
$(printf '%s' "$repo" | jq -r --argjson p "$repo_patch" '. as $r | $p | to_entries[] | "\(.key): \($r[.key]) -> \(.value)"')
EOF

# Rulesets, named "standard: <ref>" so they are recognised on the next run. Only the pull request
# rule and the required checks carry parameters; canon drops what GitHub adds and the standard leaves open.
ruleset() { # ruleset <branch> <linear true|false> <merge methods as JSON>
  jq -cn --arg b "$1" --argjson lin "$2" --argjson mm "$3" '{name: ("standard: " + $b), target: "branch", enforcement: "active",
    bypass_actors: [], conditions: {ref_name: {include: ["refs/heads/" + $b], exclude: []}},
    rules: ([{type: "deletion"}, {type: "non_fast_forward"},
      {type: "pull_request", parameters: {required_approving_review_count: 0, dismiss_stale_reviews_on_push: false,
        require_code_owner_review: false, require_last_push_approval: false, required_review_thread_resolution: true,
        allowed_merge_methods: $mm}},
      {type: "required_status_checks", parameters: {strict_required_status_checks_policy: false,
        required_status_checks: [{context: "check"}]}}] + (if $lin then [{type: "required_linear_history"}] else [] end))}'
}
canon='def canon: {target, enforcement, bypass_actors: (.bypass_actors // []),
  include: (.conditions.ref_name.include // [] | sort), exclude: (.conditions.ref_name.exclude // [] | sort),
  rules: ([.rules[] | {type} + (
    if .type == "pull_request" then {p: (.parameters | {required_approving_review_count, dismiss_stale_reviews_on_push,
      require_code_owner_review, require_last_push_approval, required_review_thread_resolution,
      m: (.allowed_merge_methods // ["merge", "rebase", "squash"] | sort)})}
    elif .type == "required_status_checks" then {p: (.parameters | {s: .strict_required_status_checks_policy,
      c: ([.required_status_checks[].context] | sort)})}
    else {} end)] | sort_by(.type))};'
want_rulesets=""
for b in $branches; do
  if [ "$model" = "dev+main" ] && [ "$b" = main ]; then want_rulesets="$want_rulesets$(ruleset main false '["merge","squash"]')"$'\n'
  else want_rulesets="$want_rulesets$(ruleset "$b" true '["squash"]')"$'\n'; fi
done
want_rulesets="$want_rulesets$(jq -cn '{name: "standard: pre-standard", target: "tag", enforcement: "active", bypass_actors: [],
  conditions: {ref_name: {include: ["refs/tags/pre-standard"], exclude: []}}, rules: [{type: "deletion"}, {type: "update"}]}')"
existing=$(get "repos/$nwo/rulesets?includes_parents=false&per_page=100")
old_rulesets="[]" ruleset_plan="" branch_rules=0
while IFS= read -r want; do
  name=$(printf '%s' "$want" | jq -r .name)
  id=$(printf '%s' "$existing" | jq -r --arg n "$name" '[.[] | select(.name == $n)] | first | .id // empty')
  if [ -z "$id" ]; then
    found "ruleset $name: missing -> create"; ruleset_plan="${ruleset_plan}POST repos/$nwo/rulesets"$'\t'"$want"$'\n'
  else
    cur=$(get "repos/$nwo/rulesets/$id")
    old_rulesets=$(printf '%s' "$old_rulesets" | jq -c --argjson c "$cur" '. + [$c]')
    if [ "$(jq -n --argjson a "$cur" --argjson b "$want" "$canon"' ($a | canon) == ($b | canon)')" = true ]; then continue; fi
    found "ruleset $name: differs -> replace"; ruleset_plan="${ruleset_plan}PUT repos/$nwo/rulesets/$id"$'\t'"$want"$'\n'
  fi
  [ "$(printf '%s' "$want" | jq -r .target)" = tag ] || branch_rules=1
done <<EOF
$want_rulesets
EOF

# Classic branch protection is replaced by the ruleset; it is removed after the ruleset is in place.
old_protection="{}" protection_plan=""
for b in $branches; do
  p=$(get_opt "repos/$nwo/branches/$b/protection")
  [ -n "$p" ] || continue
  old_protection=$(printf '%s' "$old_protection" | jq -c --arg b "$b" --argjson p "$p" '.[$b] = $p')
  found "branch-protection $b: classic -> removed (the ruleset replaces it)"; protection_plan="$protection_plan $b"
done

# The required check must exist before a ruleset requires it, or nothing could merge.
blocked=""
if [ "$branch_rules" = 1 ]; then
  runs=$(get "repos/$nwo/commits/$default/check-runs?check_name=check&per_page=1" | jq -r '.total_count // 0')
  [ "$runs" != 0 ] || blocked="the default branch $default has no CI job named check; add a CI job named check that runs make check, merge it into $default so it runs there, then run workspace.sh --apply again"
fi

# Labels: the workflow vocabulary (plugins/planner/scripts/labels.sh) plus skill-candidate. name|color|description.
vocabulary='ready-for-agent|0E8A16|Fully specified; an agent can take it
needs-triage|FBCA04|A maintainer has to evaluate this
needs-info|D876E3|Waiting on the reporter
ready-for-human|1D76DB|Needs a human to implement
wontfix|FFFFFF|Will not be actioned; the closing comment says why
spec|5319E7|Spec issue; its tickets carry the work
bug|D73A4A|Something is broken
enhancement|A2EEEF|New feature or improvement
skill-candidate|C5DEF5|A removed skill that could move into the marketplace'
labels=$(get "repos/$nwo/labels?per_page=100")
label_plan=""
while IFS='|' read -r name color desc; do
  printf '%s' "$labels" | jq -e --arg n "$name" 'any(.[]; .name == $n)' >/dev/null && continue
  found "label $name: missing -> create"; label_plan="$label_plan$name|$color|$desc"$'\n'
done <<EOF
$vocabulary
EOF

# Dependabot alerts (204 when on, 404 when off) and security updates; read-only Actions token.
alerts=$(get_opt "repos/$nwo/vulnerability-alerts"); if [ -n "$alerts" ]; then alerts=on; else alerts=off; fi
[ "$alerts" = on ] || found "dependabot alerts: off -> on"
fixes=$(get "repos/$nwo/automated-security-fixes" | jq -r 'if .enabled then "on" else "off" end')
[ "$fixes" = on ] || found "dependabot security-updates: off -> on"
token=$(get "repos/$nwo/actions/permissions/workflow" | jq -r .default_workflow_permissions)
[ "$token" = read ] || found "actions default-token: $token -> read"

# Public repositories: secret scanning, push protection, private vulnerability reporting.
sa_patch="{}" pvr=""
if [ "$visibility" = public ]; then
  sa_patch=$(printf '%s' "$repo" | jq -c '.security_and_analysis // {} | [("secret_scanning", "secret_scanning_push_protection") as $k
    | select(.[$k].status != "enabled") | {($k): {status: "enabled"}}] | add // {}')
  for k in $(printf '%s' "$sa_patch" | jq -r 'keys[]'); do
    found "$(printf '%s' "$k" | tr _ -): $(printf '%s' "$repo" | jq -r --arg k "$k" '.security_and_analysis[$k].status // "disabled"') -> enabled"
  done
  pvr=$(get_opt "repos/$nwo/private-vulnerability-reporting")
  pvr=$(printf '%s' "${pvr:-null}" | jq -r 'if .enabled == true then "on" else "off" end')
  [ "$pvr" = on ] || found "private-vulnerability-reporting: off -> on"
elif [ "$(printf '%s' "$repo" | jq -r '.security_and_analysis.secret_scanning.status // "unavailable"')" = unavailable ]; then
  step "secret scanning and push protection: not offered for this private repository (GitHub Secret Protection needs an organisation on GitHub Team or Enterprise); make the repository public or move it to such an organisation to get them"
fi

# Milestones: close open ones that are empty, or orphaned (not named vX.Y.Z, so never released, and
# nothing open). Never creates one.
close=$(get "repos/$nwo/milestones?state=open&per_page=100" | jq -c '[.[] | select(.open_issues == 0 and
  (.closed_issues == 0 or (.title | test("^v[0-9]+\\.[0-9]+\\.[0-9]+$") | not)))
  | {number, title, open_issues, closed_issues, why: (if .closed_issues == 0 then "empty" else "orphaned" end)}] | sort_by(.number)')
while IFS= read -r l; do [ -z "$l" ] || found "$l"; done <<EOF
$(printf '%s' "$close" | jq -r '.[] | "milestone \(.title): open, \(.why) -> closed"')
EOF

# Project: copy the template when none is linked. The API cannot create or turn on project workflows.
q='query($o: String!, $n: String!) { repository(owner: $o, name: $n) { projectsV2(first: 20) { nodes {
  number title url closed workflows(first: 50) { nodes { name enabled } } } } } }'
projects=$(gh api graphql -f query="$q" -f o="$owner" -f n="${nwo#*/}" 2>"$err") || die "cannot read the projects of $nwo: $(tail -n1 "$err")"
projects=$(printf '%s' "$projects" | jq -c '[.data.repository.projectsV2.nodes[] | select(.closed | not)]')
autoadd="turn on Workflows > Auto-add to project with the filter is:issue,pr is:open for $nwo (the API cannot create or turn on project workflows)"
project_plan=""
if [ "$(printf '%s' "$projects" | jq length)" = 0 ]; then
  if [ -n "$tpl" ]; then found "project: none linked -> copy of $tpl"; project_plan=1; step "project (the copy): $autoadd"
  else step "project: none linked; set WF_PROJECT_TEMPLATE=<owner>/<number> and run again to copy the template project, or create one by hand"; fi
else
  while IFS= read -r url; do [ -z "$url" ] || step "project $url: $autoadd"; done <<EOF
$(printf '%s' "$projects" | jq -r '.[] | select(any(.workflows.nodes[]; .name == "Auto-add to project" and .enabled) | not) | .url')
EOF
fi

printf 'repository: %s\n' "$nwo"
printf 'profile: %s, %s\n' "$visibility" "$model"
printf '%s' "$diffs" | sed 's/^/diff: /'
printf '%s' "$manual" | sed 's/^/manual: /'
[ -z "$blocked" ] || printf 'blocked: %s\n' "$blocked"
printf 'differences: %s\n' "$n"
if [ "$apply" = 0 ]; then
  [ "$n" = 0 ] || [ -n "$blocked" ] || printf 'next: run workspace.sh --apply to make these changes\n'
  exit 0
fi
[ "$n" != 0 ] || { printf 'applied: 0\n'; exit 0; }
[ -z "$blocked" ] || die "refusing to apply: $blocked"

# The snapshot holds everything this run replaces, written before the first change.
[ -n "$snap" ] || snap=$(mktemp "${TMPDIR:-/tmp}/workspace-snapshot.XXXXXX")
jq -n --arg nwo "$nwo" --arg at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --argjson repo "$repo" --argjson rs "$old_rulesets" \
  --argjson bp "$old_protection" --argjson labels "$labels" --arg alerts "$alerts" --arg fixes "$fixes" --arg token "$token" \
  --arg pvr "$pvr" --argjson ms "$close" --argjson projects "$projects" \
  '{repository: $nwo, taken_at: $at,
    repo: ($repo | {visibility, default_branch, allow_squash_merge, allow_merge_commit, allow_rebase_merge, delete_branch_on_merge,
      squash_merge_commit_title, squash_merge_commit_message, has_wiki, has_discussions, security_and_analysis}),
    rulesets: $rs, branch_protection: $bp, labels: [$labels[].name], dependabot: {alerts: $alerts, security_updates: $fixes},
    actions_default_token: $token, private_vulnerability_reporting: (if $pvr == "" then null else $pvr end),
    milestones_closed: $ms, projects: [$projects[] | {number, title, url}]}' > "$snap" || die "cannot write the snapshot to $snap; nothing changed"
printf 'snapshot: %s\n' "$snap"

if [ "$repo_patch" != "{}" ]; then
  # GitHub validates the squash title together with the message, so the current message goes along.
  send PATCH "repos/$nwo" "$(printf '%s' "$repo_patch" | jq -c --argjson r "$repo" \
    'if has("squash_merge_commit_title") then .squash_merge_commit_message = $r.squash_merge_commit_message else . end')"
fi
while IFS=$'\t' read -r call body; do
  [ -z "$call" ] || send "${call%% *}" "${call#* }" "$body"
done <<EOF
$ruleset_plan
EOF
for b in $protection_plan; do send DELETE "repos/$nwo/branches/$b/protection"; done
while IFS='|' read -r name color desc; do
  [ -z "$name" ] || send POST "repos/$nwo/labels" "$(jq -cn --arg n "$name" --arg c "$color" --arg d "$desc" '{name: $n, color: $c, description: $d}')"
done <<EOF
$label_plan
EOF
[ "$alerts" = on ] || send PUT "repos/$nwo/vulnerability-alerts"
[ "$fixes" = on ] || send PUT "repos/$nwo/automated-security-fixes"
[ "$token" = read ] || send PUT "repos/$nwo/actions/permissions/workflow" '{"default_workflow_permissions":"read"}'
[ "$sa_patch" = "{}" ] || send PATCH "repos/$nwo" "$(jq -cn --argjson s "$sa_patch" '{security_and_analysis: $s}')"
[ -z "$pvr" ] || [ "$pvr" = on ] || send PUT "repos/$nwo/private-vulnerability-reporting"
for num in $(printf '%s' "$close" | jq -r '.[].number'); do send PATCH "repos/$nwo/milestones/$num" '{"state":"closed"}'; done
if [ -n "$project_plan" ]; then
  copy=$(gh project copy "${tpl#*/}" --source-owner "${tpl%%/*}" --target-owner "$owner" --title "${nwo#*/}" --format json 2>"$err") \
    || die "copying project $tpl failed: $(tail -n1 "$err")"
  num=$(printf '%s' "$copy" | jq -r .number)
  gh project link "$num" --owner "$owner" --repo "$nwo" >/dev/null 2>"$err" \
    || die "project $(printf '%s' "$copy" | jq -r .url) was copied but linking it to $nwo failed: $(tail -n1 "$err"); link it with gh project link $num --owner $owner --repo $nwo"
  printf 'project: %s\n' "$(printf '%s' "$copy" | jq -r .url)"
fi
printf 'applied: %s\n' "$n"
