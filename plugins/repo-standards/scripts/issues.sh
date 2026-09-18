#!/usr/bin/env bash
# Turn the approved findings with the action issue into agent-ready issues (label ready-for-agent), one per
# finding, so code changes go through the worker pipeline instead of the cleanup pull request.
# Usage: issues.sh
# An issue is found again by its title, `Standard (<category>): <target>`, so a second run opens nothing twice.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
for c in gh jq; do command -v "$c" >/dev/null 2>&1 || die "$c is required but not on PATH"; done
answers=$(decisions) || exit 1
err=$(mktemp); trap 'rm -f "$err"' EXIT
todo=$(approved_findings "$answers" issue)
[ -n "$todo" ] || { printf 'issues: none approved\n'; exit 0; }
github_repo || exit 1
labels=$(gh api --paginate "repos/$nwo/labels?per_page=100" 2>"$err") || die "cannot read the labels of $nwo: $(tail -n1 "$err")"
if ! printf '%s' "$labels" | jq -s -e 'add // [] | any(.[]; (.name | ascii_downcase) == "ready-for-agent")' >/dev/null; then
  label_json ready-for-agent | gh api --method POST "repos/$nwo/labels" --input - >/dev/null 2>"$err" || die "cannot create the label ready-for-agent: $(tail -n1 "$err")"
fi
titles=$(gh api --paginate "repos/$nwo/issues?state=all&per_page=100" 2>"$err" | jq -s -c '[add // [] | .[] | select(.pull_request == null) | {number, title}]') \
  || die "cannot list the issues of $nwo: $(tail -n1 "$err")"
opened=0 kept=0
while IFS=$'\t' read -r cat target _ reason confidence; do
  title="Standard ($cat): $target"
  n=$(printf '%s' "$titles" | jq -r --arg t "$title" '[.[] | select(.title == $t)] | first // empty | .number')
  if [ -n "$n" ]; then printf 'kept: #%s %s\n' "$n" "$title"; kept=$((kept + 1)); continue; fi
  # The reason is an auditor's judgement of repository content: quoted, so it reads as a finding, not a brief.
  body=$(printf '## What to build\nResolve this finding of the standardisation run (`/repo-standards:apply`), from its `%s` audit of `%s`, confidence %s:\n\n> %s\n\nThe state before the run is tagged `%s`.\n\n## Acceptance criteria\n- [ ] `%s` no longer has the problem the finding describes.\n- [ ] `make check` passes.\n' \
    "$cat" "$target" "$confidence" "$reason" "$WF_TAG" "$target")
  n=$(jq -n --arg t "$title" --arg b "$body" '{title: $t, body: $b, labels: ["ready-for-agent"]}' \
    | gh api --method POST "repos/$nwo/issues" --input - 2>"$err" | jq -r .number) || die "cannot open the issue $title: $(tail -n1 "$err")"
  printf 'opened: #%s %s\n' "$n" "$title"; opened=$((opened + 1))
done <<<"$todo"
printf 'issues: %s opened, %s kept\n' "$opened" "$kept"
