#!/usr/bin/env bash
# Shared helpers for orchestrator scripts. Sourced, not executed. Bash 3.2 compatible.

wf_die() { printf 'error: %s\n' "$*" >&2; exit 1; }
wf_warn() { printf 'warning: %s\n' "$*" >&2; }
wf_kv() { printf '%s: %s\n' "$1" "$2"; }
wf_need() { command -v "$1" >/dev/null 2>&1 || wf_die "$1 is required but not on PATH"; }

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

wf_slug() {
  printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//' | cut -c1-40 | sed -E 's/-+$//'
}

# Branch convention: <type>/<issue>-<slug>. The issue number is the only contract the worker hook relies on.
wf_issue_from_branch() { printf '%s\n' "$1" | sed -nE 's#^[a-z]+/([0-9]+)-.*#\1#p'; }

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
wf_wait_agent() {
  local pane="$1" i=0
  agent_status=""
  while [ $i -lt 12 ]; do
    agent_status=$(herdr agent list 2>/dev/null | jq -r --arg p "$pane" '.result.agents[] | select(.pane_id == $p) | .agent_status' 2>/dev/null | head -n 1)
    [ -n "$agent_status" ] && break
    i=$((i+1)); sleep 5
  done
  [ -n "$agent_status" ] || wf_warn "no claude agent detected in pane $pane after 60s; inspect the pane"
  # agent start moves focus to the new pane; give it back to the orchestrator.
  if [ -n "${HERDR_WORKSPACE_ID:-}" ]; then herdr workspace focus "$HERDR_WORKSPACE_ID" >/dev/null 2>&1 || true; fi
}

# Start `claude <args...>` as Herdr agent $2 in pane $1 and wait for it. Sets agent_status.
wf_start_agent() {
  local pane="$1" name="$2"; shift 2
  # The session starts working immediately (its first turn is a skill), so Herdr's "ready for input"
  # wait can time out although the agent is fine. Ignore that result and detect the agent ourselves.
  herdr agent start "$name" --kind claude --pane "$pane" --timeout 30000 -- "$@" >/dev/null 2>&1 || true
  wf_wait_agent "$pane"
}
