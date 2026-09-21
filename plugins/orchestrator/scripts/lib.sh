#!/usr/bin/env bash
# Shared helpers for orchestrator scripts. Sourced, not executed. Bash 3.2 compatible.

wf_die() { printf 'error: %s\n' "$*" >&2; exit 1; }
wf_warn() { printf 'warning: %s\n' "$*" >&2; }
wf_kv() { printf '%s: %s\n' "$1" "$2"; }
wf_need() { command -v "$1" >/dev/null 2>&1 || wf_die "$1 is required but not on PATH"; }

# The factory's routing label, as the label vocabulary defines it (plugins/planner/scripts/labels.sh). An issue
# that carries it belongs to the factory host, which claims it on GitHub, so a local claim leaves it alone.
# shellcheck disable=SC2034  # read by the scripts that source this file
WF_ROUTING_LABEL=factory

# Root of the main checkout, even when called from a linked worktree.
wf_main_root() {
  local common
  common=$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null) || wf_die "not inside a git repository"
  dirname "$common"
}

wf_base_branch() {
  if [ -n "${WF_BASE_BRANCH:-}" ]; then printf '%s\n' "$WF_BASE_BRANCH"; return; fi
  local ref
  ref=$(git symbolic-ref -q --short refs/remotes/origin/HEAD 2>/dev/null || true)
  if [ -n "$ref" ]; then printf '%s\n' "${ref#origin/}"; return; fi
  gh repo view --json defaultBranchRef -q .defaultBranchRef.name 2>/dev/null || printf 'main\n'
}

# Branch-safe slug: URLs dropped, German umlauts transliterated, ASCII lower-case, at most 40 chars.
wf_slug() {
  printf '%s' "$1" | sed -E 's#https?://[^ ]*##g' \
    | sed -e 's/ä/ae/g; s/ö/oe/g; s/ü/ue/g; s/Ä/ae/g; s/Ö/oe/g; s/Ü/ue/g; s/ß/ss/g' \
    | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//' | cut -c1-40 | sed -E 's/-+$//'
}

# Branch convention: <type>/<issue>-<slug>. The issue number is the only contract the worker hook relies on.
# A plan branch (plan/<slug>) carries a topic, so its slug may start with a number without being an issue.
wf_issue_from_branch() { printf '%s\n' "$1" | sed -nE '\#^plan/#d; s#^[a-z]+/([0-9]+)-.*#\1#p'; }

# True when the issue JSON $1 (as gh prints it) carries the label $2. Compared name by name, because a
# GitHub label may itself contain a comma and a joined list would then match on a substring.
wf_issue_has_label() { printf '%s' "$1" | jq -e --arg l "$2" 'any(.labels[]?; .name == $l)' >/dev/null 2>&1; }

# Map issue labels (comma separated) to a branch type.
wf_branch_type() {
  case ",$1," in
    *,bug,*|*,fix,*) printf 'fix\n' ;;
    *,docs,*|*,documentation,*) printf 'docs\n' ;;
    *,chore,*|*,maintenance,*) printf 'chore\n' ;;
    *) printf 'feat\n' ;;
  esac
}

wf_notify() {
  [ "${HERDR_ENV:-}" = 1 ] || return 0
  herdr notification show "$1" --body "${2:-}" --sound "${3:-done}" >/dev/null 2>&1 || true
}

# Herdr workspace id whose worktree checkout path equals $1, or empty.
wf_workspace_for_path() {
  [ "${HERDR_ENV:-}" = 1 ] || return 0
  herdr workspace list 2>/dev/null | jq -r --arg p "$1" '.result.workspaces[]? | select(.worktree.checkout_path == $p) | .workspace_id' | head -n1
}

# Branch of the linked worktree that belongs to issue $1, or empty. Derived from the branch names, not from
# the issue's labels, so a claim or an abandon still finds the worktree after the labels changed.
wf_branch_for_issue() {
  local b found=""
  while read -r b; do
    if [ -z "$found" ] && [ "$(wf_issue_from_branch "$b")" = "$1" ]; then found="$b"; fi
  done < <(git worktree list --porcelain | sed -nE 's#^branch refs/heads/##p')
  printf '%s\n' "$found"
}

# Path of the linked worktree checked out on branch $1 (from the main root), or empty.
wf_worktree_path_for_branch() {
  git worktree list --porcelain | awk -v b="refs/heads/$1" '
    /^worktree /{p=substr($0,10)} /^branch /{if($2==b){print p; exit}}'
}

# Log every mutating command when WF_DRY_RUN=1 instead of running it.
wf_run() {
  if [ "${WF_DRY_RUN:-0}" = 1 ]; then printf 'dry-run: %s\n' "$*"; return 0; fi
  "$@"
}

# Check WF_CLAUDE_ARGS and the per-session VAR (WF_PLANNER_CLAUDE_ARGS or WF_WORKER_CLAUDE_ARGS)
# before a session is started. They are word-split into claude flags, and claude takes both the
# "--flag value" and the "--flag=value" spelling, so both are handled here.
wf_check_claude_args() {
  local var="$1" prev="" w dir
  # shellcheck disable=SC2086
  for w in ${WF_CLAUDE_ARGS:-} ${!var:-} ""; do
    # A truncated --plugin-dir would kill the session at start, so it is refused, not warned about.
    dir=""
    if [ "$prev" = "--plugin-dir" ]; then
      if [ -z "$w" ] || [ "${w#-}" != "$w" ]; then wf_die "WF_CLAUDE_ARGS/$var: --plugin-dir needs a path (got '$w'). Use absolute paths, e.g. WF_PLANNER_CLAUDE_ARGS=\"--plugin-dir /repo/plugins/planner\""; fi
      dir="$w"
    fi
    case "$w" in
      --plugin-dir=*)
        dir="${w#--plugin-dir=}"
        if [ -z "$dir" ] || [ "${dir#-}" != "$dir" ]; then wf_die "WF_CLAUDE_ARGS/$var: --plugin-dir needs a path (got '$w'). Use absolute paths, e.g. WF_PLANNER_CLAUDE_ARGS=\"--plugin-dir /repo/plugins/planner\""; fi ;;
      # claude keeps only the last --settings and does not merge, and these flags come after the ones
      # the script builds, so one here replaces the whole object. Warn: it is a legitimate override.
      --settings|--settings=*)
        wf_warn "WF_CLAUDE_ARGS/$var: your --settings replaces the settings this script builds for the session (plugin isolation, its WF_* environment and every other key it sets), because claude keeps only the last one. Put those keys into your own JSON." ;;
    esac
    [ -z "$dir" ] || [ -d "$dir" ] || wf_die "WF_CLAUDE_ARGS/$var: --plugin-dir $dir is not a directory"
    prev="$w"
  done
}

# Sanity-check WF_PLANNER_LANGUAGE before a planning session is created.
# Quoting is not the risk: jq escapes the value into the --settings JSON and herdr passes that as one
# argv element. What matters is that claude copies the value verbatim into the session's system prompt,
# so a control character would corrupt the prompt and a long value is a pasted sentence, not a language.
# Any name claude can read is accepted, `francais` and non-Latin names included.
wf_check_planner_language() {
  local v="${WF_PLANNER_LANGUAGE:-}" ctrl
  [ -n "$v" ] || return 0
  # -dc keeps only control characters; the trailing x makes a trailing one visible, because command
  # substitution strips trailing newlines.
  ctrl=$(printf '%s' "$v" | LC_ALL=C tr -dc '[:cntrl:]'; printf x)
  [ "$ctrl" = x ] || wf_die "WF_PLANNER_LANGUAGE contains a line break or a control character. Use a plain language name or locale code, e.g. WF_PLANNER_LANGUAGE=german"
  [ ${#v} -le 32 ] || wf_die "WF_PLANNER_LANGUAGE is ${#v} characters long, which is a sentence, not a language. Use a name or a locale code of at most 32, e.g. WF_PLANNER_LANGUAGE=german or WF_PLANNER_LANGUAGE=pt-br"
}

# Create a worktree and Herdr workspace for branch $1 from ref $2 with label $3.
# Sets ws, pane and path. Under WF_DRY_RUN=1 it only logs and leaves them empty.
wf_create_worktree() {
  local branch="$1" baseref="$2" label="$3" root wtdir wtpath created
  root=$(wf_main_root)
  # Worktrees live inside the trusted repository (Claude's own convention), so no trust dialog blocks the session.
  wtdir="$root/.claude/worktrees"; mkdir -p "$wtdir"
  grep -qx '.claude/worktrees/' "$root/.git/info/exclude" 2>/dev/null || printf '.claude/worktrees/\n' >> "$root/.git/info/exclude"
  wtpath="$wtdir/$(printf '%s' "$branch" | tr '/' '-')"
  created=$(wf_run herdr worktree create --cwd "$root" --branch "$branch" --base "$baseref" --path "$wtpath" --label "$label" --no-focus)
  ws=""; pane=""; path=""
  [ "${WF_DRY_RUN:-0}" = 1 ] && return 0
  ws=$(printf '%s' "$created" | jq -r '.result.workspace.workspace_id // empty')
  pane=$(printf '%s' "$created" | jq -r '.result.root_pane.pane_id // empty')
  path=$(printf '%s' "$created" | jq -r '.result.worktree.path // empty')
  if [ -z "$ws" ] || [ -z "$pane" ] || [ -z "$path" ]; then wf_die "unexpected herdr response: $created"; fi
}

# Wait until Herdr detects a claude agent in pane $1 (sets agent_status), then give focus back to the orchestrator.
# Returns 1 when the session died right after start (unknown agent, missing plugin); start_error holds the reason.
# shellcheck disable=SC2034  # start_error and agent_status are read by the callers
wf_wait_agent() {
  local pane="$1" i=0 limit out
  agent_status=""; start_error=""
  limit=$(( ${WF_AGENT_WAIT:-60} / 5 ))
  while :; do
    agent_status=$(herdr agent list 2>/dev/null | jq -r --arg p "$pane" '.result.agents[] | select(.pane_id == $p) | .agent_status' 2>/dev/null | head -n 1)
    [ -n "$agent_status" ] && break
    [ $i -ge $limit ] && break
    i=$((i+1)); sleep 5
  done
  # agent start moves focus to the new pane; give it back to the orchestrator.
  if [ -n "${HERDR_WORKSPACE_ID:-}" ]; then herdr workspace focus "$HERDR_WORKSPACE_ID" >/dev/null 2>&1 || true; fi
  [ -n "$agent_status" ] && return 0
  out=$(herdr pane read "$pane" --source recent --lines 20 --format text 2>/dev/null | sed '/^[[:space:]]*$/d' || true)
  if printf '%s' "$out" | grep -q "not found"; then
    start_error="claude exited: $(printf '%s' "$out" | grep "not found" | tail -n 1). The agent's plugin is not loaded in new sessions: install it (claude plugin install <plugin>@workflows) or set WF_CLAUDE_ARGS=\"--plugin-dir <path>\" for the orchestrator."
    return 1
  fi
  # A bare shell prompt as the last line means claude exited (bad flag, crash); a slow start would still show claude.
  if printf '%s' "$out" | tail -n 1 | grep -Eq '[%$#] ?$'; then
    start_error="claude exited right after start; the pane is back at the shell prompt. Last output: $(printf '%s' "$out" | tail -n 3 | tr '\n' ' '). Check WF_CLAUDE_ARGS (${WF_CLAUDE_ARGS:-empty}) and run the command by hand in that pane."
    return 1
  fi
  wf_warn "no claude agent detected in pane $pane after ${WF_AGENT_WAIT:-60}s; inspect the pane"
  return 0
}

# Undo a fresh worktree after a failed session start: workspace (or worktree) and local branch.
wf_rollback_worktree() {
  local ws="$1" path="$2" branch="$3"
  if [ -n "$ws" ]; then herdr worktree remove --workspace "$ws" --force >/dev/null 2>&1 || git worktree remove --force "$path" >/dev/null 2>&1 || true
  else git worktree remove --force "$path" >/dev/null 2>&1 || true; fi
  git worktree prune
  git branch -D "$branch" >/dev/null 2>&1 || true
}

# Start `claude <args...>` as Herdr agent $2 in pane $1 and wait for it. Sets agent_status.
# Herdr agent names: lowercase letter first, then lowercase letters, digits, - or _, at most 32 characters.
wf_agent_name() {
  printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9_-]+/-/g; s/^[^a-z]+//' | cut -c1-32 | sed -E 's/[-_]+$//'
}
wf_start_agent() {
  local pane="$1" name="$2" out code; shift 2
  # The session starts working immediately (its first turn is a skill), so Herdr's "ready for input"
  # wait can time out although the agent is fine. Ignore a timeout and detect the agent ourselves;
  # any other error means nothing was started at all, so report it right away instead of waiting.
  out=$(herdr agent start "$name" --kind claude --pane "$pane" --timeout 30000 -- "$@" 2>&1) || true
  code=$(printf '%s' "$out" | jq -r '.error.code // empty' 2>/dev/null || true)
  if [ -n "$code" ] && [ "$code" != timeout ]; then
    agent_status=""
    # shellcheck disable=SC2034  # read by the caller after a non-zero return
    start_error="herdr agent start failed ($code): $(printf '%s' "$out" | jq -r '.error.message // empty'). Nothing was started in pane $pane."
    return 1
  fi
  wf_wait_agent "$pane"
}
