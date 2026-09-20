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
# Branch-safe slug: URLs dropped, German umlauts transliterated, ASCII lower-case, at most 40 chars.
wf_slug() {
  printf '%s' "$1" | sed -E 's#https?://[^ ]*##g' \
    | sed -e 's/ä/ae/g; s/ö/oe/g; s/ü/ue/g; s/Ä/ae/g; s/Ö/oe/g; s/Ü/ue/g; s/ß/ss/g' \
    | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//' | cut -c1-40 | sed -E 's/-+$//'
}
wf_notify() {
  [ "${HERDR_ENV:-}" = 1 ] || return 0
  herdr notification show "$1" --body "${2:-}" --sound "${3:-done}" >/dev/null 2>&1 || true
}
# An issue number with an optional leading #, or a refusal naming what was passed.
wf_issue_num() { local n="${1#\#}"; printf '%s' "$n" | grep -Eq '^[0-9]+$' || wf_die "issue must be a number, got '$1'"; printf '%s' "$n"; }
# GitHub's numeric database id of issue $1 (dependency and sub-issue APIs want it, not the number).
wf_issue_db_id() { gh api "repos/$(wf_repo_nwo)/issues/$1" --jq .id 2>/dev/null; }

# --- The acceptance of a spec (accept-facts.sh, accept-close.sh, accept-due.sh) ---

# Issue $2 of repository $1 as JSON, or a refusal naming it.
wf_issue_json() { gh api "repos/$1/issues/$2" 2>/dev/null || wf_die "could not read issue #$2 in $1; does it exist, and is gh authenticated for this repository?"; }
# The native sub-issues of issue $2 of repository $1 as one JSON array, empty where there are none.
# Fails (non-zero, no output) where the API is unavailable, which is not the same as a spec without tickets.
wf_sub_issues() {
  local raw   # captured first: through a pipe the status would be jq's, which turns an outage into "no tickets"
  raw=$(gh api --paginate "repos/$1/issues/$2/sub_issues?per_page=100" 2>/dev/null) || return 1
  printf '%s' "$raw" | jq -s -c 'add // []'
}
# Refuse anything but an open spec issue. $1 the number, $2 its JSON.
wf_require_open_spec() {
  local labels; labels=$(printf '%s' "$2" | jq -r '[.labels[]?.name] | join(",")')
  case ",$labels," in
    *,spec,*) ;;
    *) wf_die "#$1 is not labelled spec (labels: ${labels:--}); an acceptance judges a spec against the code. Open a planning session on the issue to triage it." ;;
  esac
  [ "$(printf '%s' "$2" | jq -r .state)" = open ] || wf_die "#$1 is closed; it was accepted already. Reopen it to accept it again."
}
# Refuse a worktree that is not the base branch as it is now: the acceptance judges the code on the base
# branch, and a planning worktree branched off before the tickets merged would show the checker old code.
# $1 the base branch. A fetch that fails costs the comparison, not the run.
wf_require_base_up_to_date() {
  local ref="origin/$1" behind ahead
  # The explicit refspec updates refs/remotes/origin/<base>; fetching the branch by name only writes FETCH_HEAD.
  git fetch --quiet origin "+refs/heads/$1:refs/remotes/origin/$1" 2>/dev/null \
    || wf_warn "could not fetch $ref; this worktree may be older than the base branch"
  git rev-parse --verify --quiet "$ref" >/dev/null || { wf_warn "no $ref here; the code the checker reads may be older than the base branch"; return 0; }
  behind=$(git rev-list --count "HEAD..$ref" 2>/dev/null || echo 0)
  ahead=$(git rev-list --count "$ref..HEAD" 2>/dev/null || echo 0)
  [ "$ahead" = 0 ] || wf_warn "this worktree has $ahead commit(s) that $ref does not; the checker reads them as if they were merged"
  [ "$behind" = 0 ] && return 0
  # A planning branch carries no commits, so a fast-forward is the normal fix; a diverged one needs a rebase.
  [ "$ahead" = 0 ] || wf_die "this worktree is $behind commit(s) behind $ref and has $ahead of its own, so the checker would judge the spec against old code. Capture the commits (/planner:prototype) or drop them, then: git rebase $ref"
  wf_die "this worktree is $behind commit(s) behind $ref, so the checker would judge the spec against old code. Update it first: git merge --ff-only $ref"
}
