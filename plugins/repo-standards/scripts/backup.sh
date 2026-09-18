#!/usr/bin/env bash
# The first step of the apply phase: secure the state before anything changes (ADR 0010).
# Usage: backup.sh
# 1. Tag pre-standard on the head of the default branch on GitHub and push it. An existing tag, on GitHub or
#    local, is kept and never moved.
# 2. Protect it with the tag ruleset of the standard (the one workspace.sh sets), unless one exists.
# 3. Open the catalogue issue (label skill-candidate) with one row per skill the approved deletions remove:
#    name, description, origin, files and size, restore command. A second run updates the same issue.
# Needs every category of the last report answered (approve.sh). Changes nothing in the working tree.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
for c in gh jq git; do command -v "$c" >/dev/null 2>&1 || die "$c is required but not on PATH"; done
answers=$(decisions) || exit 1
err=$(mktemp); tmp=$(mktemp); trap 'rm -f "$err" "$tmp"' EXIT
github_repo || exit 1

# 1. The tag.
remote=$(git ls-remote --tags origin "refs/tags/$WF_TAG" 2>"$err" | cut -f1) || die "cannot reach origin: $(tail -n1 "$err")"
if [ -n "$remote" ]; then
  if local=$(git rev-parse -q --verify "refs/tags/$WF_TAG^{commit}" 2>/dev/null); then
    git fetch -q origin "refs/tags/$WF_TAG" 2>"$err" || die "cannot fetch the tag $WF_TAG from origin: $(tail -n1 "$err")"
    [ "$local" = "$(git rev-parse 'FETCH_HEAD^{commit}')" ] \
      || die "the local tag $WF_TAG differs from the one on origin; the one on origin is the backup, so delete the local one (git tag -d $WF_TAG) and run backup.sh again"
  else git fetch -q origin "refs/tags/$WF_TAG:refs/tags/$WF_TAG" 2>"$err" || die "cannot fetch the tag $WF_TAG: $(tail -n1 "$err")"; fi
  printf 'tag: %s kept at %s (pushed before)\n' "$WF_TAG" "$(git rev-parse --short "$WF_TAG^{commit}")"
else
  git fetch -q origin "refs/heads/$default" 2>"$err" || die "origin has no branch $default: $(tail -n1 "$err"); push a first commit (git push -u origin HEAD), then run backup.sh again"
  if git rev-parse -q --verify "refs/tags/$WF_TAG" >/dev/null; then
    # A local tag from a run that stopped before the push; it only backs up what the default branch has.
    git merge-base --is-ancestor "$WF_TAG^{commit}" FETCH_HEAD 2>/dev/null \
      || die "the local tag $WF_TAG is not on $default, so it backs up something else; delete it (git tag -d $WF_TAG) and run backup.sh again"
    how="kept at the local tag"
  else git tag "$WF_TAG" FETCH_HEAD; how="the head of $default"; fi
  git push -q origin "refs/tags/$WF_TAG" 2>"$err" || die "cannot push the tag $WF_TAG: $(tail -n1 "$err")"
  printf 'tag: %s pushed at %s (%s)\n' "$WF_TAG" "$(git rev-parse --short "$WF_TAG^{commit}")" "$how"
fi

# 2. The protection. Without it the tag still exists; the reason becomes a manual step.
want=$(tag_ruleset); name=$(printf '%s' "$want" | jq -r .name)
if rulesets=$(gh api --paginate "repos/$nwo/rulesets?includes_parents=false&per_page=100" 2>"$err"); then
  if printf '%s' "$rulesets" | jq -s -e --arg n "$name" 'add // [] | any(.[]; .name == $n)' >/dev/null; then
    printf 'protection: ruleset %s kept\n' "$name"
  elif printf '%s' "$want" | gh api --method POST "repos/$nwo/rulesets" --input - >/dev/null 2>"$err"; then
    printf 'protection: ruleset %s created (no deletion, no moving)\n' "$name"
  else printf 'manual: protect the tag %s: creating the ruleset failed (%s); workspace.sh --apply creates it later\n' "$WF_TAG" "$(tail -n1 "$err")"; fi
else printf 'manual: protect the tag %s: rulesets cannot be read (%s); workspace.sh --apply creates it later\n' "$WF_TAG" "$(tail -n1 "$err")"; fi

# 3. The catalogue. A skill is a directory with a SKILL.md, or a command file (commands are skills too); rows
# come from the tag, so they describe exactly what the restore command brings back.
human() { awk '{ b = $1; print (b >= 1048576 ? sprintf("%.1f MB", b / 1048576) : b >= 1024 ? sprintf("%.1f KB", b / 1024) : b " B") }'; }
cell() { tr '\n|' '  ' | sed -E 's/[[:space:]]+/ /g; s/^ //; s/ $//' | cut -c1-200; }
front() { # front <key> <blob>: the value of a key of the YAML frontmatter, a folded block joined into one line
  git show "$WF_TAG:$2" 2>/dev/null | awk -v k="$1" '
    NR == 1 && $0 != "---" { exit }
    NR > 1 && $0 == "---" { exit }
    block { if ($0 ~ /^[[:space:]]/) { t = $0; gsub(/^[[:space:]]+|[[:space:]]+$/, "", t); v = v (v == "" ? "" : " ") t; next } exit }
    NR > 1 && index($0, k ":") == 1 { v = substr($0, length(k) + 2); gsub(/^[[:space:]]+|[[:space:]]+$/, "", v)
      if (v ~ /^[>|][-+]?$/) { v = ""; block = 1; next }
      gsub(/^["\047]|["\047]$/, "", v); exit }
    END { print v }'
}
locks=$(git ls-tree -r --name-only "$WF_TAG" | grep -E '(^|/)\.?skills?-lock\.json$' || true)
rows="" count=0 seen=" "
while IFS=$'\t' read -r _ target _ reason _; do
  [ -n "$target" ] || continue
  while IFS= read -r f; do
    case "$f" in
      SKILL.md|*/SKILL.md) path=$(dirname "$f"); md=$f ;;
      commands/*.md|*/commands/*.md) path=$f; md=$f ;;
      *) continue ;;
    esac
    case "$seen" in *" $path "*) continue ;; esac; seen="$seen$path "
    fallback=${path##*/}; fallback=${fallback%.md}
    nm=$(front name "$md"); nm=${nm:-$fallback}
    desc=$(front description "$md")
    origin=""
    for l in $locks; do
      src=$(git show "$WF_TAG:$l" | jq -r --arg n "$nm" --arg d "$fallback" \
        'try ((.skills // .) | (.[$n] // .[$d]) | .source // .sourceUrl // .url // empty) catch empty' 2>/dev/null || true)
      [ -z "$src" ] || { origin="upstream: $src (from $l)"; break; }
    done
    if [ -z "$origin" ] && [ "$path" != "$md" ]; then
      lic=$(git ls-tree --name-only "$WF_TAG" -- "$path/" | awk -F/ 'toupper($NF) ~ /^(LICEN[CS]E|COPYING)/ { print $NF; exit }')
      [ -z "$lic" ] || origin="upstream (carries $lic)"
    fi
    [ -n "$origin" ] || origin="audit: $reason"
    size=$(git ls-tree -r -l "$WF_TAG" -- "$path" | awk '{ n++; s += $4 } END { print n + 0 "\t" s + 0 }')
    rows="$rows| $(printf '%s' "$nm" | cell) | $(printf '%s' "${desc:-none}" | cell) | $(printf '%s' "$origin" | cell) | $(cut -f1 <<<"$size" | awk '{ print $1 ($1 == 1 ? " file" : " files") }'), $(cut -f2 <<<"$size" | human) | \`git checkout $WF_TAG -- $path\` |"$'\n'
    count=$((count + 1))
  done < <(git ls-tree -r --name-only "$WF_TAG" -- ":(literal)$target")
done < <(approved_findings "$answers" delete)
{
  printf 'The standardisation run (`/repo-standards:apply`) removes these skills from `%s`; they may move into a plugin marketplace later. The tag `%s` keeps the state before the run: fetch it with `git fetch origin tag %s`, then run a restore command in a checkout.\n\n' "$default" "$WF_TAG" "$WF_TAG"
  if [ "$count" = 0 ]; then printf 'No skills are removed.\n'
  else printf '| Skill | Description | Origin | Files | Restore |\n| --- | --- | --- | --- | --- |\n%s' "$rows"; fi
  printf '\nWhen the run configures the GitHub workspace, it adds the previous settings as a comment here.\n'
} > "$tmp"

labels=$(gh api --paginate "repos/$nwo/labels?per_page=100" 2>"$err") || die "cannot read the labels of $nwo: $(tail -n1 "$err")"
if ! printf '%s' "$labels" | jq -s -e 'add // [] | any(.[]; (.name | ascii_downcase) == "skill-candidate")' >/dev/null; then
  label_json skill-candidate | gh api --method POST "repos/$nwo/labels" --input - >/dev/null 2>"$err" || die "cannot create the label skill-candidate: $(tail -n1 "$err")"
fi
issue=$(catalogue_issue) || exit 1; how="" current=""
if [ -z "$issue" ]; then
  issue=$(jq -n --arg t "$WF_CATALOGUE" --rawfile b "$tmp" '{title: $t, body: $b, labels: ["skill-candidate"]}' \
    | gh api --method POST "repos/$nwo/issues" --input - 2>"$err" | jq -r .number) || die "cannot open the catalogue issue: $(tail -n1 "$err")"
  how=opened
else
  current=$(gh api "repos/$nwo/issues/$issue" 2>"$err" | jq -r '.body // ""') || die "cannot read the catalogue issue #$issue: $(tail -n1 "$err")"
fi
if [ "$how" = opened ]; then :
elif [ "$current" = "$(cat "$tmp")" ]; then how=unchanged
else
  jq -n --rawfile b "$tmp" '{body: $b}' | gh api --method PATCH "repos/$nwo/issues/$issue" --input - >/dev/null 2>"$err" \
    || die "cannot update the catalogue issue #$issue: $(tail -n1 "$err")"
  how=updated
fi
printf 'catalogue: #%s %s, %s skill%s\n' "$issue" "$how" "$count" "$([ "$count" = 1 ] || echo s)"
printf 'next: cleanup.sh prepare\n'
