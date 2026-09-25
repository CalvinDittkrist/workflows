#!/usr/bin/env bash
# Print the facts of the repository the standardisation auditors share, as compact `key: value` text:
# profile (visibility, plan, branch model), languages and manifests, detected test and lint commands, CI jobs,
# every agent configuration location, the baseline files and file statistics. Reads only; changes nothing.
# GitHub is optional: without it visibility and plan are `unknown` and the branch model comes from git.
# Usage: facts.sh [<repo-root>]
set -euo pipefail
export LC_ALL=C # byte order for sort, so the output is the same on every machine
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
# The plugin's own scripts, resolved before the cd so a relative invocation never runs the audited repository's.
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=lib.sh
. "$here/lib.sh"
root="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
cd "$root" 2>/dev/null || die "cannot enter $root; pass an existing repository directory"
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || die "$root is not a git repository; run git init first"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
kv() { printf '%s: %s\n' "$1" "$2"; }
# join: stdin lines joined with ", ", or the fallback when there are none.
join() { awk -v none="${1:-none}" 'NF { s = s (n++ ? ", " : "") $0 } END { print (n ? s : none) }'; }
g() { git -c core.quotePath=false "$@"; }

# Files: tracked plus untracked-but-not-ignored, the same set check.sh judges.
tracked=$(g ls-files | sort -u)
untracked=$(g ls-files --others --exclude-standard | sort -u)
all=$(printf '%s\n%s\n' "$tracked" "$untracked" | awk 'NF' | sort -u)

# Profile. The branch model follows ADR 0009: dev plus main when the default branch is dev.
visibility=unknown plan=unknown nwo="" default="" github=""
if command -v gh >/dev/null 2>&1 && command -v jq >/dev/null 2>&1; then
  err="$tmp/err"
  if nwo=$(gh repo view --json nameWithOwner -q .nameWithOwner 2>"$err") && repo=$(gh api "repos/$nwo" 2>"$err"); then
    visibility=$(printf '%s' "$repo" | jq -r '.visibility // "unknown"')
    default=$(printf '%s' "$repo" | jq -r '.default_branch // empty')
    owner=$(printf '%s' "$repo" | jq -r --arg o "${nwo%%/*}" '.owner.login // $o')
    # GitHub shows a plan only to the account itself or to an organisation's owners.
    if [ "$(printf '%s' "$repo" | jq -r '.owner.type // empty')" = Organization ]; then
      plan=$(gh api "orgs/$owner" 2>/dev/null | jq -r '.plan.name // empty' 2>/dev/null || true)
      plan="${plan:-unknown (visible to organisation owners only)}"
    else
      me=$(gh api user 2>/dev/null || true)
      if [ -n "$me" ] && [ "$(printf '%s' "$me" | jq -r .login)" = "$owner" ]; then
        plan=$(printf '%s' "$me" | jq -r '.plan.name // "unknown"')
      else plan="unknown (owned by $owner, not the gh user)"; fi
    fi
    github="$nwo, owner $owner"
  else github="unreachable ($(tail -n1 "$err"))"; fi
else github="unreachable (gh or jq not installed)"; fi
if [ -z "$default" ]; then
  default=$(g symbolic-ref --short -q refs/remotes/origin/HEAD 2>/dev/null | sed 's#^origin/##' || true)
  [ -n "$default" ] || default=$(g symbolic-ref --short -q HEAD 2>/dev/null || true)
fi
case "$default" in
  dev) model="dev+main" ;;
  main) model=main ;;
  *) model="none (default branch ${default:-unknown} is neither main nor dev)" ;;
esac
if head=$(g rev-parse --short -q --verify HEAD 2>/dev/null); then
  head="$head on $(g symbolic-ref --short -q HEAD 2>/dev/null || echo 'detached HEAD'), $(g rev-list --count HEAD) commits"
else head="none (no commits yet)"; fi
kv github "$github"
kv visibility "$visibility"
kv plan "$plan"
kv default-branch "${default:-unknown}"
kv branch-model "$model"
kv head "$head"

# Languages by file extension, most files first.
kv languages "$(printf '%s\n' "$all" | awk -F/ '
  NF { f = $NF; e = (f ~ /\./) ? tolower(f) : ""; sub(/.*\./, "", e)
    l = ""
    if (e == "py") l = "Python"; else if (e == "sh" || e == "bash" || e == "zsh") l = "Shell"
    else if (e == "ts" || e == "tsx" || e == "mts" || e == "cts") l = "TypeScript"
    else if (e == "js" || e == "jsx" || e == "mjs" || e == "cjs") l = "JavaScript"
    else if (e == "go") l = "Go"; else if (e == "rs") l = "Rust"; else if (e == "rb") l = "Ruby"
    else if (e == "java") l = "Java"; else if (e == "kt" || e == "kts") l = "Kotlin"; else if (e == "swift") l = "Swift"
    else if (e == "c" || e == "h") l = "C"; else if (e == "cc" || e == "cpp" || e == "cxx" || e == "hpp") l = "C++"
    else if (e == "cs") l = "C#"; else if (e == "php") l = "PHP"; else if (e == "ex" || e == "exs") l = "Elixir"
    else if (e == "dart") l = "Dart"; else if (e == "scala") l = "Scala"; else if (e == "lua") l = "Lua"
    else if (e == "sql") l = "SQL"; else if (e == "html" || e == "htm") l = "HTML"; else if (e == "css" || e == "scss") l = "CSS"
    else if (e == "vue") l = "Vue"; else if (e == "svelte") l = "Svelte"; else if (e == "tf") l = "Terraform"
    else if (e == "nix") l = "Nix"; else if (e == "md" || e == "mdx") l = "Markdown"
    if (l != "") n[l]++ }
  END { for (l in n) print n[l] "\t" l }' | sort -k1,1nr -k2 | awk -F'\t' 'NR <= 10 { print $2 " " $1 }' | join)"

# Manifests: build and package files at any depth, shallowest first.
manifests=$(printf '%s\n' "$all" | awk -F/ '
  NF && NF <= 4 && ($NF ~ /^(package\.json|pyproject\.toml|setup\.py|setup\.cfg|Pipfile|Cargo\.toml|go\.mod|Gemfile|pom\.xml|build\.gradle(\.kts)?|composer\.json|mix\.exs|Package\.swift|pubspec\.yaml|deno\.jsonc?|CMakeLists\.txt|flake\.nix|justfile|Justfile|Makefile|GNUmakefile|makefile|Dockerfile|tox\.ini|noxfile\.py)$/ || $NF ~ /^requirements.*\.txt$/ || $NF ~ /\.(csproj|sln)$/) { print NF "\t" $0 }' | sort -k1,1n -k2 | cut -f2)
kv manifests "$(printf '%s\n' "$manifests" | awk 'NF && NR <= 15' | join)$(n=$(printf '%s\n' "$manifests" | awk 'NF' | wc -l | tr -d ' '); [ "$n" -le 15 ] || printf ' (+%d more)' $((n - 15)))"

# Test and lint commands, each with the file it comes from. Detection only; nothing is run.
tests="" lints="" gate=""
add_test() { tests="$tests$1 ($2)"$'\n'; }
add_lint() { lints="$lints$1 ($2)"$'\n'; }
while IFS= read -r m; do
  [ -f "$m" ] || continue # tracked but deleted from the working tree
  d=$(dirname "$m"); f=${m##*/}; at=""; [ "$d" = . ] || at=" -C $d"
  case "$f" in
    Makefile|GNUmakefile|makefile)
      # Targets of the form `name:` (not `name :=`), one line each.
      while IFS= read -r t; do
        case "$t" in
          check) [ -n "$gate" ] || gate="make$at check ($m)" ;;
          test|tests|unit|integration|e2e|test-*) add_test "make$at $t" "$m" ;;
          lint|fmt|format|format-check|typecheck|vet|lint-*) add_lint "make$at $t" "$m" ;;
        esac
      done < <(sed -nE 's/^([A-Za-z0-9_.-]+)[[:space:]]*::?([^=]|$).*/\1/p' "$m" | sort -u) ;;
    package.json)
      pm=npm
      for lock in pnpm-lock.yaml:pnpm yarn.lock:yarn bun.lockb:bun bun.lock:bun; do
        [ -e "$d/${lock%%:*}" ] && { pm=${lock##*:}; break; }
      done
      command -v jq >/dev/null 2>&1 || continue
      for s in $(jq -r '(.scripts // {}) | keys[]' "$m" 2>/dev/null | grep -E '^[A-Za-z0-9:_.-]+$' || true); do
        case "$s" in
          test) add_test "$pm test" "$m" ;;
          test:*|e2e|e2e:*) add_test "$pm run $s" "$m" ;;
          lint|lint:*|typecheck|type-check|tsc|format:check|fmt:check|check) add_lint "$pm run $s" "$m" ;;
        esac
      done ;;
    pyproject.toml|setup.cfg|tox.ini)
      grep -q '^\[tool\.pytest\|^\[tool:pytest\]\|^\[pytest\]' "$m" && add_test pytest "$m"
      grep -q '^\[tool\.ruff' "$m" && add_lint "ruff check" "$m"
      grep -q '^\[tool\.mypy\]\|^\[mypy\]' "$m" && add_lint mypy "$m"
      [ "$f" = tox.ini ] && add_test tox "$m" ;;
    Cargo.toml) add_test "cargo test" "$m"; add_lint "cargo clippy" "$m" ;;
    go.mod) add_test "go test ./..." "$m"; add_lint "go vet ./..." "$m" ;;
    Gemfile) grep -q rspec "$m" && add_test "bundle exec rspec" "$m"; grep -q rubocop "$m" && add_lint "bundle exec rubocop" "$m" ;;
    pom.xml) add_test "mvn test" "$m" ;;
    build.gradle|build.gradle.kts) add_test "gradle test" "$m" ;;
    justfile|Justfile)
      while IFS= read -r t; do
        case "$t" in test|tests) add_test "just $t" "$m" ;; lint|fmt|check) add_lint "just $t" "$m" ;; esac
      done < <(sed -nE 's/^([A-Za-z0-9_-]+)[^:=]*:([^=]|$).*/\1/p' "$m" | sort -u) ;;
  esac
done <<EOF
$manifests
EOF
while IFS= read -r c; do
  case "$c" in
    pytest.ini) add_test pytest "$c" ;;
    ruff.toml|.ruff.toml) add_lint "ruff check" "$c" ;;
    .pre-commit-config.yaml) add_lint "pre-commit run --all-files" "$c" ;;
    .golangci.yml|.golangci.yaml) add_lint "golangci-lint run" "$c" ;;
    .shellcheckrc) add_lint shellcheck "$c" ;;
    .rubocop.yml) add_lint rubocop "$c" ;;
    eslint.config.*|.eslintrc*) add_lint eslint "$c" ;;
  esac
done <<EOF
$(printf '%s\n' "$all" | grep -E '^(pytest\.ini|\.?ruff\.toml|\.pre-commit-config\.yaml|\.golangci\.ya?ml|\.shellcheckrc|\.rubocop\.yml|eslint\.config\.[a-z]+|\.eslintrc(\.[a-z]+)?)$' || true)
EOF
kv gate "${gate:-none (no check target in a Makefile)}"
kv test "$(printf '%s' "$tests" | awk '!seen[$0]++' | join)"
kv lint "$(printf '%s' "$lints" | awk '!seen[$0]++' | join)"

# CI: jobs of each GitHub Actions workflow (id, and the name GitHub shows as the check when it differs),
# plus the configuration files of other CI systems.
ci="" has_check=no
for w in $(printf '%s\n' "$all" | grep -E '^\.github/workflows/[^/]+\.ya?ml$' || true); do
  [ -f "$w" ] || continue
  jobs=$(workflow_jobs "$w")
  ci="$ci$w: ${jobs:-no jobs found}"$'\n'
  printf '%s' "$jobs" | is_check_job && has_check=yes
done
for o in $(printf '%s\n' "$all" | grep -E '^(\.gitlab-ci\.yml|\.circleci/config\.yml|Jenkinsfile|azure-pipelines\.yml|\.travis\.yml|bitbucket-pipelines\.yml|\.buildkite/pipeline\.yml)$' || true); do
  ci="$ci$o: not GitHub Actions"$'\n'
done
if [ -n "$ci" ]; then printf 'ci:\n'; printf '%s' "$ci" | sed 's/^/  /'; else kv ci none; fi
kv ci-check-job "$has_check"

# Agent configuration: every location Claude Code reads plus those of other agent tools, ignored files
# included, one line per location with its file count, git status and whether the standard defines it.
prune='-name .git -o -name node_modules -o -name .venv -o -name venv -o -name vendor -o -name target -o -name __pycache__ -o -path ./.claude/worktrees'
# shellcheck disable=SC2086 # $prune is a list of find operators
# Symlinks count too: CLAUDE.md -> AGENTS.md is common, and a linked skill directory is configuration all the same.
agent=$(find . \( $prune \) -prune -o \( -type f -o -type l \) -print 2>/dev/null | sed 's#^\./##' | awk -F/ '
  function loc(i,   j, n, p) { n = i + 2; if (n > NF) n = NF; p = $1; for (j = 2; j <= n; j++) p = p "/" $j; return p }
  {
    for (i = 1; i <= NF; i++) {
      c = $i
      if (c == ".claude" && i == NF) { print $0 "\toutside the standard (a symlinked .claude)\t" $0; next }
      if (c == ".claude" && i < NF) {
        s = $(i+1)
        std = (i == 1 && (s == "settings.json" || s == "settings.local.json")) ? "standard" : "outside the standard"
        print loc(i) "\t" std "\t" $0; next
      }
      if (i == NF && (c == "CLAUDE.md" || c == "AGENTS.md")) { print $0 "\tstandard\t" $0; next }
      if (c == ".claude-plugin") { print loc(i) "\tplugin source, not loaded as configuration\t" $0; next }
      if (i == NF && (c == "CLAUDE.local.md" || c == "AGENT.md" || c == ".worktreeinclude" || c == ".rules")) { print $0 "\toutside the standard\t" $0; next }
      if (i == NF && c == ".mcp.json") { print $0 "\tneeds judgement (stays when something in the repository uses it)\t" $0; next }
      if (c ~ /^\.aider/ || c ~ /^(\.agents|\.amazonq|\.augment|\.clinerules|\.codex|\.continue|\.cursor|\.cursorignore|\.cursorindexingignore|\.cursorrules|\.gemini|\.goose|\.goosehints|\.junie|\.kilocode|\.kiro|\.opencode|\.qwen|\.roo|\.roomodes|\.roorules|\.trae|\.windsurf|\.windsurfrules|GEMINI\.md|opencode\.json|skills-lock\.json|\.skill-lock\.json)$/) { print loc(i) "\toutside the standard\t" $0; next }
      if (i == 1 && c == ".github" && NF > 1 && $2 ~ /^(copilot-instructions\.md|instructions|prompts|chatmodes|agents)$/) { print loc(i) "\toutside the standard\t" $0; next }
    }
  }' | sort)
if [ -z "$agent" ]; then kv agent-config none
else
  # Status of the agent configuration files only, through files: the whole tree can exceed the argument limit.
  printf '%s\n' "$agent" | cut -f3 > "$tmp/paths"
  printf '%s\n' "$tracked" > "$tmp/tracked"
  g check-ignore --stdin < "$tmp/paths" > "$tmp/ignored" 2>/dev/null || true
  printf 'agent-config:\n'
  printf '%s\n' "$agent" | awk -F'\t' '
    FILENAME == ARGV[1] { T[$0] = 1; next }
    FILENAME == ARGV[2] { I[$0] = 1; next }
    { st = ($3 in T) ? "tracked" : ($3 in I) ? "ignored" : "untracked"
      if (!($1 in files)) order[++k] = $1
      files[$1]++; std[$1] = $2; if (index(" " sts[$1] " ", " " st " ") == 0) sts[$1] = sts[$1] (sts[$1] == "" ? "" : "+") st }
    END { for (i = 1; i <= k; i++) { p = order[i]
      printf "  %s: %d file%s, %s, %s\n", p, files[p], (files[p] == 1 ? "" : "s"), sts[p], std[p] } }' "$tmp/tracked" "$tmp/ignored" -
fi

# Baseline files of the standard (docs/repo-standard.md), present or missing. Alternative names count.
present="" missing=""
base() { # base <label> <name>...: the first name found, exact case
  local n d
  for n in "${@:2}"; do d=$(dirname "$n"); [ -d "$d" ] && has "$d" "${n##*/}" && { present="$present$n"$'\n'; return; }; done
  missing="$missing$1"$'\n'
}
# shellcheck disable=SC2086 # a list of names
base README.md $WF_README_NAMES
base AGENTS.md AGENTS.md
base CLAUDE.md CLAUDE.md
base Makefile Makefile GNUmakefile makefile
base docs/architecture.md docs/architecture.md
base docs/adr/README.md docs/adr/README.md
base docs/glossary.md docs/glossary.md
base .github/PULL_REQUEST_TEMPLATE.md .github/PULL_REQUEST_TEMPLATE.md .github/pull_request_template.md
base .github/dependabot.yml .github/dependabot.yml .github/dependabot.yaml
base .claude/settings.json .claude/settings.json
if [ "$visibility" = public ]; then # shellcheck disable=SC2086 # a list of names
  base LICENSE $WF_LICENSE_NAMES; base SECURITY.md SECURITY.md .github/SECURITY.md; fi
kv baseline-present "$(printf '%s' "$present" | join)"
kv baseline-missing "$(printf '%s' "$missing" | join)"

# The writing rules, counted by writing.sh as check.sh counts them, so the docs auditor proposes the rewrite
# from the findings instead of counting words itself. The first 20 are listed; a rewrite issue needs no more.
writing=$(printf '%s\n' "$all" | bash "$here/writing.sh" . | cut -f2-) || die "cannot count the writing rules; fix the error above"
if [ -z "$writing" ]; then kv writing-findings none
else
  printf 'writing-findings:\n'
  printf '%s\n' "$writing" | awk 'NR <= 20 { print "  " $0 } END { if (NR > 20) printf "  (+%d more)\n", NR - 20 }'
fi

# File statistics over the tracked and untracked files: count, size, top directories, the largest files.
if stat -c %s / >/dev/null 2>&1; then sizefmt=(-c '%s %n'); else sizefmt=(-f '%z %N'); fi
sizes=$(printf '%s\n' "$all" | awk 'NF' | while IFS= read -r f; do [ -f "$f" ] && printf '%s\0' "$f"; done | xargs -0 stat "${sizefmt[@]}" 2>/dev/null || true)
human='function h(b) { return b >= 1048576 ? sprintf("%.1f MB", b / 1048576) : b >= 1024 ? sprintf("%.0f KB", b / 1024) : b " B" }'
kv files "$(printf '%s\n' "$tracked" | awk 'NF' | wc -l | tr -d ' ') tracked, $(printf '%s\n' "$untracked" | awk 'NF' | wc -l | tr -d ' ') untracked, $(printf '%s\n' "$sizes" | awk "$human"' NF { s += $1 } END { print h(s + 0) }')"
kv top-dirs "$(printf '%s\n' "$all" | awk -F/ 'NF > 1 { n[$1]++ } END { for (d in n) print n[d] "\t" d }' | sort -k1,1nr -k2 | awk -F'\t' 'NR <= 8 { print $2 "/ " $1 }' | join)"
kv largest "$(printf '%s\n' "$sizes" | awk 'NF' | sort -k1,1nr | awk "$human"' NR <= 5 { s = $1; sub(/^[0-9]+ /, ""); print $0 " (" h(s) ")" }' | join)"
