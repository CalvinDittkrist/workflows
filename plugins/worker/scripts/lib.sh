#!/usr/bin/env bash
# Shared helpers for worker scripts. Sourced, not executed. Bash 3.2 compatible.
wf_die() { printf 'error: %s\n' "$*" >&2; exit 1; }
wf_warn() { printf 'warning: %s\n' "$*" >&2; }
wf_kv() { printf '%s: %s\n' "$1" "$2"; }
wf_need() { command -v "$1" >/dev/null 2>&1 || wf_die "$1 is required but not on PATH"; }
# A plan branch (plan/<slug>) carries a topic, so its slug may start with a number without being an issue.
wf_issue_from_branch() { printf '%s\n' "$1" | sed -nE '\#^plan/#d; s#^[a-z]+/([0-9]+)-.*#\1#p'; }
wf_branch() { git rev-parse --abbrev-ref HEAD 2>/dev/null || true; }
wf_issue() {
  if [ -n "${WF_ISSUE:-}" ]; then printf '%s\n' "$WF_ISSUE"; else wf_issue_from_branch "$(wf_branch)"; fi
}
wf_base_branch() {
  if [ -n "${WF_BASE_BRANCH:-}" ]; then printf '%s\n' "$WF_BASE_BRANCH"; return; fi
  local ref; ref=$(git symbolic-ref -q --short refs/remotes/origin/HEAD 2>/dev/null || true)
  if [ -n "$ref" ]; then printf '%s\n' "${ref#origin/}"; return; fi
  gh repo view --json defaultBranchRef -q .defaultBranchRef.name 2>/dev/null || printf 'main\n'
}
# The reviewer panel of this session: the configured list, or the five reviewers the worker ships with.
wf_reviewers() { printf '%s\n' "${WF_REVIEWERS:-code,security,docs,tests,senior}"; }
# Where a worker stage leaves a fact for the next one (ADR 0018): this worktree's own git directory, never
# the common one, so the workers of two issues in two worktrees keep separate records. Removed with the
# worktree, which is what a pipeline run lives in.
wf_state_dir() {
  local d
  d=$(git rev-parse --path-format=absolute --git-dir 2>/dev/null) || wf_die "not inside a git repository"
  printf '%s/worker' "$d"
}
wf_repo_owner() { gh repo view --json owner -q .owner.login; }
wf_repo_name() { gh repo view --json name -q .name; }
wf_notify() {
  [ "${HERDR_ENV:-}" = 1 ] || return 0
  herdr notification show "$1" --body "${2:-}" --sound "${3:-done}" >/dev/null 2>&1 || true
}
# ISO-8601 UTC timestamp (2026-09-17T18:45:09Z) to epoch seconds; macOS and GNU date.
wf_epoch() { date -u -j -f '%Y-%m-%dT%H:%M:%SZ' "$1" +%s 2>/dev/null || date -u -d "$1" +%s 2>/dev/null; }
# PR number for the current branch, or empty.
wf_pr_for_branch() { gh pr list --head "$(wf_branch)" --state open --json number -q '.[0].number' 2>/dev/null || true; }
