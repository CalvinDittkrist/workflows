#!/usr/bin/env bash
# Create the repository baseline. Never overwrites; prints created/kept per file.
# Usage: scaffold.sh [<repo-root>]
set -euo pipefail
root="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
tpl="$(cd "$(dirname "$0")/../templates" && pwd)"
repo=$(basename "$root")
put() { # put <template> <target> [<name make or GitHub also reads instead>...]
  local t="$root/$2" alt e
  for alt in "$2" "${@:3}"; do # exact case, so macOS reports the name that is really there
    for e in "$(dirname "$root/$alt")"/*; do
      if [ "${e##*/}" = "${alt##*/}" ]; then printf 'kept: %s\n' "$alt"; return; fi
    done
  done
  if [ -e "$t" ]; then printf 'kept: %s\n' "$2"; return; fi
  mkdir -p "$(dirname "$t")"
  sed -e "s/{{REPO}}/$repo/g" -e "s/{{RUN_CMD}}/<fill in>/g" "$tpl/$1" > "$t"
  printf 'created: %s\n' "$2"
}
put AGENTS.md AGENTS.md
put CLAUDE.md CLAUDE.md
put Makefile Makefile GNUmakefile makefile
put architecture.md docs/architecture.md
put adr-README.md docs/adr/README.md
put adr-template.md docs/adr/template.md
put PULL_REQUEST_TEMPLATE.md .github/PULL_REQUEST_TEMPLATE.md .github/pull_request_template.md
if [ -e "$root/.claude/settings.json" ]; then
  printf 'kept: .claude/settings.json (merge templates/settings.json by hand: marketplace, enabledPlugins, env, permissions)\n'
else
  mkdir -p "$root/.claude"; cp "$tpl/settings.json" "$root/.claude/settings.json"; printf 'created: .claude/settings.json\n'
fi
printf 'next: fill AGENTS.md, the Makefile check target and docs/architecture.md; run check.sh\n'
