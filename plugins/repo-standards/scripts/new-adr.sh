#!/usr/bin/env bash
# Create the next numbered ADR from the template and add it to the index.
# Usage: new-adr.sh <title words...>
set -euo pipefail
root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
dir="$root/docs/adr"; [ -d "$dir" ] || { echo "error: docs/adr missing; run scaffold.sh first" >&2; exit 1; }
[ $# -gt 0 ] || { echo "usage: new-adr.sh <title>" >&2; exit 1; }
title="$*"
last=$(printf '%s\n' "$dir"/[0-9][0-9][0-9][0-9]-*.md | sed -nE 's#.*/([0-9]{4})-.*\.md$#\1#p' | sort | tail -n1); last=${last:-0000}
num=$(printf '%04d' $((10#$last + 1)))
slug=$(printf '%s' "$title" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//' | cut -c1-60 | sed -E 's/-+$//')
file="$dir/$num-$slug.md"
tpl="$dir/template.md"; [ -f "$tpl" ] || tpl="$(dirname "$0")/../templates/adr-template.md"
sed -e "s/{{NUMBER}}/$num/g" -e "s/{{TITLE}}/$(printf '%s' "$title" | sed 's/[&/\]/\\&/g')/g" -e "s/{{DATE}}/$(date +%Y-%m-%d)/g" "$tpl" > "$file"
idx="$dir/README.md"
if [ -f "$idx" ] && grep -q '^| ADR |' "$idx"; then printf '| [%s](%s) | %s | proposed |\n' "$num" "$(basename "$file")" "$title" >> "$idx"; fi
printf 'created: docs/adr/%s\n' "$(basename "$file")"
