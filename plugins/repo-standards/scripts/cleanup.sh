#!/usr/bin/env bash
# The cleanup pull request of the apply phase: the approved deletions and the missing baseline files on the
# branch chore/standardize, in a worktree inside the repository, so the checkout stays untouched.
# Usage: cleanup.sh prepare | open
# prepare  Needs the backup (backup.sh): the tag pre-standard on origin and the catalogue issue. Creates or
#          resumes the worktree, removes the targets of approved delete findings (tracked files only), runs
#          scaffold.sh with --skip for each rejected category, and prints `todo:` lines for what needs
#          judgement: the approved replace and create findings and the <fill in> placeholders.
# open     Refuses while a <fill in> placeholder is left on the branch. Commits the worktree, pushes the branch
#          without force and opens the pull request, or updates its description. Nothing to change: no PR.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=lib.sh
. "$here/lib.sh"
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
step=${1:-}
case "$step" in prepare|open) ;; *) die "usage: cleanup.sh prepare | open" ;; esac
[ $# = 1 ] || die "usage: cleanup.sh prepare | open"
for c in gh jq git; do command -v "$c" >/dev/null 2>&1 || die "$c is required but not on PATH"; done
answers=$(decisions) || exit 1
err=$(mktemp); tmp=$(mktemp); trap 'rm -f "$err" "$tmp"' EXIT
nwo=$(gh repo view --json nameWithOwner -q .nameWithOwner 2>"$err") || die "cannot read the GitHub repository: $(tail -n1 "$err"); run gh auth status"
default=$(gh api "repos/$nwo" 2>"$err" | jq -r '.default_branch // empty') || die "cannot read repos/$nwo: $(tail -n1 "$err")"
root=$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")
wt="$root/.claude/worktrees/$(printf '%s' "$WF_BRANCH" | tr '/' '-')"
g() { git -C "$wt" -c core.quotePath=false "$@"; }
# The head of the default branch on origin, fresh.
base() { git fetch -q origin "refs/heads/$default" 2>"$err" || die "cannot fetch $default from origin: $(tail -n1 "$err")"; git rev-parse FETCH_HEAD; }
# The pull requests from the branch, newest first, as number, state (open, closed, merged), url, body.
pulls() {
  gh api --paginate "repos/$nwo/pulls?head=${nwo%%/*}:$WF_BRANCH&state=all&per_page=100" 2>"$err" \
    | jq -s -c '[add // [] | .[] | {number, url: .html_url, body: (.body // ""), state: (if .merged_at then "merged" else .state end)}] | sort_by(-.number)' \
    || die "cannot list the pull requests from $WF_BRANCH: $(tail -n1 "$err")"
}
rejected=$(categories "$answers" reject)
# placeholders <base>: the files the branch adds or changes (staged) that still hold a <fill in> placeholder.
placeholders() {
  g diff --cached --name-only --diff-filter=AM "$1" | while IFS= read -r f; do
    [ -f "$wt/$f" ] && grep -qF '<fill in>' "$wt/$f" && printf '%s\n' "$f"
  done; return 0
}

if [ "$step" = prepare ]; then
  [ -n "$(git ls-remote --tags origin "refs/tags/$WF_TAG" 2>/dev/null)" ] || die "the tag $WF_TAG is not on origin; run backup.sh first, nothing is deleted before the backup"
  catalogue=$(gh api --paginate "repos/$nwo/issues?labels=skill-candidate&state=all&per_page=100" 2>"$err" \
    | jq -s -r --arg t "$WF_CATALOGUE" '[add // [] | .[] | select(.title == $t)] | first // empty | .number') || die "cannot list the skill-candidate issues: $(tail -n1 "$err")"
  [ -n "$catalogue" ] || die "the catalogue issue is missing; run backup.sh first, nothing is deleted before the backup"

  mkdir -p "$root/.claude/worktrees"
  exclude="$(git rev-parse --path-format=absolute --git-common-dir)/info/exclude"
  grep -qx '.claude/worktrees/' "$exclude" 2>/dev/null || { mkdir -p "$(dirname "$exclude")"; printf '.claude/worktrees/\n' >> "$exclude"; }
  if [ "$(git -C "$wt" rev-parse --abbrev-ref HEAD 2>/dev/null)" = "$WF_BRANCH" ]; then
    printf 'worktree: %s (resumed)\n' "$wt"
  else
    [ ! -e "$wt" ] || die "$wt exists but is not a worktree on $WF_BRANCH; move it away, then run cleanup.sh prepare again"
    if [ -n "$(git ls-remote --heads origin "refs/heads/$WF_BRANCH" 2>/dev/null)" ]; then
      [ "$(pulls | jq -r '.[0].state // empty')" != merged ] \
        || die "$WF_BRANCH on origin belongs to a merged pull request; run finalize.sh, which deletes it, or delete it with git push origin --delete $WF_BRANCH"
      git fetch -q origin "refs/heads/$WF_BRANCH" 2>"$err" || die "cannot fetch $WF_BRANCH: $(tail -n1 "$err")"
      from="the pushed $WF_BRANCH"
    else base >/dev/null; from="$default"; fi
    git worktree add -q -B "$WF_BRANCH" "$wt" FETCH_HEAD 2>"$err" || die "cannot create the worktree $wt: $(tail -n1 "$err")"
    printf 'worktree: %s (new, from %s)\n' "$wt" "$from"
  fi

  # Deletions: the targets of approved delete findings. Only tracked files go through the pull request; the
  # tag holds them. An untracked target is left for the maintainer, since no backup has it.
  while IFS= read -r t; do
    [ -n "$t" ] || continue
    if [ -n "$(g ls-files -- ":(literal)$t" | head -n1)" ]; then
      g rm -r -q -- ":(literal)$t"; printf 'deleted: %s\n' "$t"
    elif [ -e "$root/$t" ] && [ -z "$(git -C "$root" ls-files -- ":(literal)$t" | head -n1)" ]; then
      printf 'local: %s is not tracked, so the pull request cannot remove it and the tag does not keep it; delete it in the checkout yourself\n' "$t"
    else printf 'gone: %s\n' "$t"; fi
  done < <(approved_findings "$answers" delete | cut -f2 | awk '!seen[$0]++')

  skips=(); for c in $rejected; do skips+=(--skip "$c"); done
  out=$(bash "$here/scaffold.sh" ${skips[@]+"${skips[@]}"} "$wt") || exit 1
  printf '%s\n' "$out" | grep -v '^next:' || true
  g add -A

  # What needs judgement, for the agent to do in the worktree.
  for a in replace create; do
    approved_findings "$answers" "$a" | awk -F'\t' '{ print "todo: " $1 " " $3 " " $2 ": " $4 }'
  done
  placeholders "$(base)" | sed 's/^/todo: fill the <fill in> placeholders in /'
  [ -z "$rejected" ] || printf 'untouched: %s (rejected)\n' "$(printf '%s' "$rejected" | sed 's/ /, /g')"
  printf 'next: do the todo lines in %s, run make check there, then cleanup.sh open\n' "$wt"
  exit 0
fi

# open
[ "$(git -C "$wt" rev-parse --abbrev-ref HEAD 2>/dev/null)" = "$WF_BRANCH" ] || die "no worktree on $WF_BRANCH at $wt; run cleanup.sh prepare first"
main=$(base)
g add -A
left=$(placeholders "$main")
[ -z "$left" ] || die "<fill in> placeholders are left in $(printf '%s' "$left" | tr '\n' ' ')in $wt; fill them in, then run cleanup.sh open again"
if ! g diff --cached --quiet; then
  g commit -q -m "chore: bring the repository to the standard" -m "Removes what the standardisation audit found outside the standard and adds the missing baseline files. The tag $WF_TAG keeps the previous state." \
    || die "cannot commit in $wt"
fi
head=$(g rev-parse HEAD)
if [ -z "$(git diff --name-only "$main" "$head")" ]; then
  printf 'pr: none needed, %s already has every change\n' "$default"; exit 0
fi
g push -q origin "HEAD:refs/heads/$WF_BRANCH" 2>"$err" || die "cannot push $WF_BRANCH: $(tail -n1 "$err"); integrate origin/$WF_BRANCH in $wt without force, then run cleanup.sh open again"

# The description: what goes, by category, with the restore command; what is added or changed; what stays.
deleted=$(git diff --no-renames --diff-filter=D --name-only "$main" "$head")
catalogue=$(gh api --paginate "repos/$nwo/issues?labels=skill-candidate&state=all&per_page=100" 2>/dev/null \
  | jq -s -r --arg t "$WF_CATALOGUE" '[add // [] | .[] | select(.title == $t)] | first // empty | .number' || true)
{
  printf '## What\nBrings the repository to the standard of the workflow plugins: removes what the audit found outside it and adds the missing baseline files. Nothing outside this pull request changes; the GitHub workspace is configured after the merge.\n\n'
  printf '## Removed\nThe tag `%s` keeps the state before the run. Fetch it with `git fetch origin tag %s`; each restore command brings a path back into a checkout.%s\n' \
    "$WF_TAG" "$WF_TAG" "${catalogue:+ Removed skills are listed in #$catalogue.}"
  any=0
  for c in $WF_CATEGORIES; do
    rows=$(approved_findings "$answers" delete | awk -F'\t' -v c="$c" '$1 == c' | while IFS=$'\t' read -r _ t _ why _; do
      printf '%s\n' "$deleted" | awk -v t="$t" '$0 == t || index($0, t "/") == 1 { f = 1; exit } END { exit !f }' || continue
      printf -- '- `%s`: %s. Restore: `git checkout %s -- %s`\n' "$t" "$why" "$WF_TAG" "$t"
    done)
    [ -n "$rows" ] || continue
    printf '\n### %s\n%s\n' "$c" "$rows"; any=1
  done
  [ "$any" = 1 ] || printf '\nNothing.\n'
  added=$(git diff --no-renames --diff-filter=AM --name-status "$main" "$head" | awk -F'\t' '{ print "- " ($1 == "A" ? "added" : "changed") " `" $2 "`" }')
  printf '\n## Added and changed\n%s\n' "${added:-Nothing.}"
  printf '\n## Left alone\n%s\n' "$( [ -n "$rejected" ] && printf 'Rejected in the audit, untouched: %s.' "$(printf '%s' "$rejected" | sed 's/ /, /g')" || printf 'No category was rejected.')"
  printf '\n## Verification\n- [ ] `make check` passes on this branch (CI job `check`).\n'
} > "$tmp"
open=$(pulls | jq -c '[.[] | select(.state == "open")] | first // empty')
if [ -z "$open" ]; then
  url=$(jq -n --arg h "$WF_BRANCH" --arg b "$default" --rawfile body "$tmp" \
    '{title: "chore: bring the repository to the standard", head: $h, base: $b, body: $body}' \
    | gh api --method POST "repos/$nwo/pulls" --input - 2>"$err" | jq -r .html_url) || die "cannot open the pull request: $(tail -n1 "$err")"
  printf 'pr: %s opened\n' "$url"
elif [ "$(printf '%s' "$open" | jq -r .body)" = "$(cat "$tmp")" ]; then
  printf 'pr: %s unchanged\n' "$(printf '%s' "$open" | jq -r .url)"
else
  jq -n --rawfile body "$tmp" '{body: $body}' | gh api --method PATCH "repos/$nwo/pulls/$(printf '%s' "$open" | jq -r .number)" --input - >/dev/null 2>"$err" \
    || die "cannot update the pull request: $(tail -n1 "$err")"
  printf 'pr: %s updated\n' "$(printf '%s' "$open" | jq -r .url)"
fi
printf 'next: merge the pull request once check passes, then run finalize.sh\n'
