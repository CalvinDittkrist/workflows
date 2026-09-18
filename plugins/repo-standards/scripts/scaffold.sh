#!/usr/bin/env bash
# Create the repository baseline and bring .claude/settings.json to the template. Never overwrites a file;
# prints created/kept/updated per file.
# Usage: scaffold.sh [--skip <category>]... [--name <repo>] [--default <branch>] [<repo-root>]
# --name and --default default to the directory name and the branch origin/HEAD names (else the current one);
# the apply phase passes both, because it scaffolds a worktree.
# --skip leaves the files of a category alone: agent-config (AGENTS.md, CLAUDE.md, .claude/settings.json),
# docs (docs/, the PR template), tests-ci (Makefile, the CI job check), workspace (.github/dependabot.yml).
# Settings: the marketplace and the workflow plugins go in through `claude plugin ... --scope project`, every
# other plugin enabled at project scope is disabled, and the template's attribution, env and permissions
# are merged in (existing env values win, permission lists are joined).
set -euo pipefail
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
skip=" " root="" repo="" default=""
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"
while [ $# -gt 0 ]; do
  case "$1" in
    --skip) [ $# -ge 2 ] || die "--skip needs a category"
      case " $WF_CATEGORIES " in *" $2 "*) ;; *) die "unknown category $2; use one of $WF_CATEGORIES" ;; esac
      skip="$skip$2 "; shift ;;
    --name|--default) [ $# -ge 2 ] || die "$1 needs a value"; if [ "$1" = --name ]; then repo=$2; else default=$2; fi; shift ;;
    -*) die "unknown argument $1; usage: scaffold.sh [--skip <category>]... [--name <repo>] [--default <branch>] [<repo-root>]" ;;
    *) root=$1 ;;
  esac
  shift
done
root="${root:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
tpl="$(cd "$(dirname "$0")/../templates" && pwd)"
repo=${repo:-$(basename "$root")}
[ -n "$default" ] || default=$(git -C "$root" symbolic-ref --short -q refs/remotes/origin/HEAD 2>/dev/null | sed 's#^origin/##' || true)
[ -n "$default" ] || default=$(git -C "$root" symbolic-ref --short -q HEAD 2>/dev/null || echo main)
# CI runs check on every push to the default branch, and to main too in the dev plus main model.
if [ "$default" = dev ]; then branches="main, dev"; else branches=$default; fi
esc() { printf '%s' "$1" | sed 's/[\\&|]/\\&/g'; }
skipped() { case "$skip" in *" $1 "*) return 0 ;; esac; return 1; }
put() { # put <category> <template> <target> [<name make or GitHub also reads instead>...]
  local t="$root/$3" alt
  skipped "$1" && return
  for alt in "$3" "${@:4}"; do # exact case, so macOS reports the name that is really there
    if has "$(dirname "$root/$alt")" "${alt##*/}"; then printf 'kept: %s\n' "$alt"; return; fi
  done
  if [ -e "$t" ]; then printf 'kept: %s (exists with a different case)\n' "$3"; return; fi
  mkdir -p "$(dirname "$t")"
  sed -e "s|{{REPO}}|$(esc "$repo")|g" -e "s|{{BRANCHES}}|$(esc "$branches")|g" -e "s|{{RUN_CMD}}|<fill in>|g" "$tpl/$2" > "$t"
  printf 'created: %s\n' "$3"
}
put agent-config AGENTS.md.tpl AGENTS.md
put agent-config CLAUDE.md.tpl CLAUDE.md
put tests-ci Makefile Makefile GNUmakefile makefile
put docs architecture.md docs/architecture.md
put docs adr-README.md docs/adr/README.md
put docs adr-template.md docs/adr/template.md
put docs glossary.md docs/glossary.md
put docs PULL_REQUEST_TEMPLATE.md .github/PULL_REQUEST_TEMPLATE.md .github/pull_request_template.md
put workspace dependabot.yml .github/dependabot.yml .github/dependabot.yaml
# The CI job named check, unless a workflow already has one.
if ! skipped tests-ci; then
  gate=""
  for w in "$root"/.github/workflows/*.yml "$root"/.github/workflows/*.yaml; do
    [ -f "$w" ] && workflow_jobs "$w" | is_check_job && { gate=${w#"$root"/}; break; }
  done
  if [ -n "$gate" ]; then printf 'kept: %s (has the job check)\n' "$gate"; else put tests-ci check.yml .github/workflows/check.yml; fi
fi

# Settings through the plugin commands, which write .claude/settings.json of the directory they run in.
skipped agent-config && { printf 'next: fill the <fill in> placeholders; run check.sh\n'; exit 0; }
command -v claude >/dev/null 2>&1 || die "claude is not on PATH; install Claude Code (npm install -g @anthropic-ai/claude-code), it registers the marketplace and enables the plugins"
command -v jq >/dev/null 2>&1 || die "jq is required but not on PATH"
s="$root/.claude/settings.json"
[ ! -e "$s" ] || jq -e 'type == "object"' "$s" >/dev/null 2>&1 || die ".claude/settings.json is not a JSON object; fix it by hand, then run scaffold.sh again"
before=$(cat "$s" 2>/dev/null || true)
err=$(mktemp); trap 'rm -f "$err"' EXIT
run() { (cd "$root" && claude plugin "$@" --scope project) >/dev/null 2>"$err" || die "claude plugin $* --scope project failed: $(tail -n1 "$err")"; }
run marketplace add "$(jq -r '.extraKnownMarketplaces.workflows.source.repo' "$tpl/settings.json")"
wanted=$(jq -r '.enabledPlugins | keys[]' "$tpl/settings.json")
for p in $wanted; do
  [ "$(jq -r --arg p "$p" '.enabledPlugins[$p] // empty' "$s" 2>/dev/null)" = true ] || run install "$p"
done
for p in $(jq -r '.enabledPlugins // {} | to_entries[] | select(.value == true) | .key' "$s"); do
  printf '%s\n' "$wanted" | grep -qxF -- "$p" || run disable "$p"
done
jq --slurpfile t "$tpl/settings.json" '$t[0] as $t
  | .attribution = $t.attribution
  | .env = ($t.env + (.env // {}))
  | .permissions.allow = ((.permissions.allow // []) as $a | $a + ($t.permissions.allow - $a))
  | .permissions.deny = ((.permissions.deny // []) as $d | $d + ($t.permissions.deny - $d))' "$s" > "$s.tmp" && mv "$s.tmp" "$s"
if [ -z "$before" ]; then printf 'created: .claude/settings.json\n'
elif [ "$(jq -S . <<<"$before")" = "$(jq -S . "$s")" ]; then printf 'kept: .claude/settings.json\n'
else printf 'updated: .claude/settings.json\n'; fi
printf 'next: fill the <fill in> placeholders; run check.sh\n'
