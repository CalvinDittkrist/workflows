#!/usr/bin/env bash
# Release milestone vX.Y.Z: tag main, publish the GitHub release with generated notes, close the milestone.
# Usage: release.sh <vX.Y.Z>
# With dev plus main it first opens (or finds) the promotion PR dev -> main and tags its merge commit
# once it is merged; until then it prints status: waiting. Run it again after the merge.
set -euo pipefail
. "$(dirname "$0")/lib.sh"

v=""
while [ $# -gt 0 ]; do
  case "$1" in
    -h|--help) sed -n '2,5p' "$0"; exit 0 ;;
    -*) wf_die "unknown flag $1" ;;
    *) [ -z "$v" ] || wf_die "one version only, got '$v' and '$1'"; v="$1" ;;
  esac
  shift
done
[ -n "$v" ] || wf_die "usage: release.sh <vX.Y.Z>"
printf '%s' "$v" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || wf_die "version must look like v1.2.3, got '$v'"
wf_need gh; wf_need jq
nwo=$(gh repo view --json nameWithOwner -q .nameWithOwner) || wf_die "cannot read the GitHub repository; run gh auth status"

ms=$(gh api "repos/$nwo/milestones?state=all&per_page=100" | jq -c --arg t "$v" '[.[] | select(.title == $t)] | first // empty') \
  || wf_die "cannot read the milestones of $nwo"
[ -n "$ms" ] || wf_die "milestone $v does not exist; create it and attach its issues first (planner: issue.sh milestone $v)"
[ "$(printf '%s' "$ms" | jq -r .state)" = open ] || wf_die "milestone $v is already closed"
open=$(printf '%s' "$ms" | jq -r .open_issues)
[ "$open" = 0 ] || wf_die "milestone $v has $open open issue(s); finish them or move them to a later milestone (gh issue list --milestone $v)"
if gh api "repos/$nwo/git/ref/tags/$v" >/dev/null 2>&1; then wf_die "tag $v already exists; release a new version instead"; fi

branch_sha() { gh api "repos/$nwo/branches/$1" --jq .commit.sha 2>/dev/null; }
main=$(branch_sha main) || wf_die "$nwo has no main branch; releases are tagged on main"
wf_kv milestone "$v ($(printf '%s' "$ms" | jq -r .closed_issues) closed issues)"

if branch_sha dev >/dev/null; then
  wf_kv model "dev+main"
  title="chore(release): $v"
  prs=$(gh pr list --base main --head dev --state all --limit 100 --json number,title,state,url,mergeCommit) \
    || wf_die "cannot list promotion pull requests"
  pr=$(printf '%s' "$prs" | jq -c --arg t "$title" '[.[] | select(.title == $t and .state != "CLOSED")] | first // empty')
  if [ -z "$pr" ]; then
    body="Promotes \`dev\` to \`main\` for milestone $v. After the merge, \`/orchestrator:release $v\` tags the merge commit and publishes the release."
    url=$(wf_run gh pr create --base main --head dev --title "$title" --body "$body") \
      || wf_die "opening the promotion pull request dev -> main failed; check that dev is ahead of main"
    wf_kv promotion "$url (opened)"
  elif [ "$(printf '%s' "$pr" | jq -r .state)" = OPEN ]; then
    url=$(printf '%s' "$pr" | jq -r .url)
    wf_kv promotion "$url (open)"
  else
    target=$(printf '%s' "$pr" | jq -r '.mergeCommit.oid // empty')
    [ -n "$target" ] || wf_die "promotion PR $(printf '%s' "$pr" | jq -r .url) is merged but GitHub reports no merge commit"
    wf_kv promotion "$(printf '%s' "$pr" | jq -r .url) (merged)"
  fi
  if [ -z "${target:-}" ]; then
    wf_kv status waiting
    wf_kv next "merge the promotion PR (/orchestrator:merge ${url##*/}), then run /orchestrator:release $v again"
    exit 0
  fi
else
  wf_kv model main
  target="$main"
fi

wf_kv target "$target"
release=$(wf_run gh release create "$v" --target "$target" --title "$v" --generate-notes) \
  || wf_die "creating release $v failed; see the gh error above"
wf_kv release "$release"
number=$(printf '%s' "$ms" | jq -r .number)
wf_run gh api --method PATCH "repos/$nwo/milestones/$number" -f state=closed >/dev/null \
  || wf_die "release $v is published but closing milestone $v failed; close it on GitHub"
wf_kv status released
wf_notify "Released $v" "$nwo"
