#!/usr/bin/env bash
# Shared helpers for planner scripts. Sourced, not executed. Bash 3.2 compatible.
wf_die() { printf 'error: %s\n' "$*" >&2; exit 1; }
wf_warn() { printf 'warning: %s\n' "$*" >&2; }
wf_kv() { printf '%s: %s\n' "$1" "$2"; }
wf_need() { command -v "$1" >/dev/null 2>&1 || wf_die "$1 is required but not on PATH"; }
wf_branch() { git rev-parse --abbrev-ref HEAD 2>/dev/null || true; }
# Planning branches are plan/<slug>; the slug is the only state contract with the orchestrator.
wf_plan_slug() { printf '%s\n' "$(wf_branch)" | sed -nE 's#^plan/(.+)$#\1#p'; }
# The topic or issue travels in git's branch description (set by the orchestrator's plan.sh).
wf_plan_desc() { git config "branch.$(wf_branch).description" 2>/dev/null || true; }
wf_plan_issue() {
  if [ -n "${WF_PLAN_ISSUE:-}" ]; then printf '%s\n' "$WF_PLAN_ISSUE"; else wf_plan_desc | sed -nE 's/^issue: #([0-9]+).*/\1/p' | head -n 1; fi
}
wf_plan_topic() { wf_plan_desc | sed -nE 's/^topic: (.*)$/\1/p' | head -n 1; }
wf_base_branch() {
  if [ -n "${WF_BASE_BRANCH:-}" ]; then printf '%s\n' "$WF_BASE_BRANCH"; return; fi
  local ref; ref=$(git symbolic-ref -q --short refs/remotes/origin/HEAD 2>/dev/null || true)
  if [ -n "$ref" ]; then printf '%s\n' "${ref#origin/}"; return; fi
  gh repo view --json defaultBranchRef -q .defaultBranchRef.name 2>/dev/null || printf 'main\n'
}
wf_repo_nwo() { gh repo view --json nameWithOwner -q .nameWithOwner; }
# Root of the main checkout, even when called from a linked worktree.
wf_main_root() {
  local common
  common=$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null) || wf_die "not inside a git repository"
  dirname "$common"
}
wf_slug() {
  printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//' | cut -c1-40 | sed -E 's/-+$//'
}
wf_notify() {
  [ "${HERDR_ENV:-}" = 1 ] || return 0
  herdr notification show "$1" --body "${2:-}" --sound "${3:-done}" >/dev/null 2>&1 || true
}
# GitHub's numeric database id of issue $1 (dependency and sub-issue APIs want it, not the number).
wf_issue_db_id() { gh api "repos/$(wf_repo_nwo)/issues/$1" --jq .id 2>/dev/null; }
