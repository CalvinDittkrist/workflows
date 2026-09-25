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
# The ref this branch is reviewed against, and the commit it forked from. Every stage that names a range
# resolves it through these two, so the range in a brief, in a handoff note and in the diff context is one
# range: the remote base when it is fetched, the local branch when it is not.
wf_base_ref() {
  local base ref; base=$(wf_base_branch); ref="origin/$base"
  git rev-parse -q --verify "$ref" >/dev/null 2>&1 || ref="$base"
  printf '%s\n' "$ref"
}
wf_merge_base() {
  local ref; ref=$(wf_base_ref)
  git merge-base "$ref" HEAD 2>/dev/null || printf '%s\n' "$ref"
}
# The stages a checkpoint hands over before, and the stages a handoff resumes at: one list, because the two
# are the same contract read from both ends — the checkpoint accepts the stage, handoff.sh has to accept the
# word the model then pipes the note with. Both are a stage boundary at which everything the next context
# needs is in git, in GitHub or in a record of this worktree.
wf_handoff_stages() { printf 'review ci\n'; }
# Whether a word is one of a space-separated list. `case " $list " in *" $word "*)` answers yes for several
# words of the list at once as well, so a quoted "review ci" passed for a stage; this compares word by word.
wf_in_list() {
  local item
  for item in $2; do [ "$item" = "$1" ] && return 0; done
  return 1
}
# The agent session id herdr reports for a pane: the one signal that tells one Claude context in a pane from
# the next, which is what a handoff confirms itself with (ADR 0029). Empty when herdr knows no agent there.
wf_agent_session() {
  herdr agent get "$1" 2>/dev/null | jq -r '.result.agent.agent_session.value // empty' 2>/dev/null || true
}
# The state herdr reports for a pane's agent: idle, working, blocked, done or unknown. Empty when herdr knows
# no agent there.
wf_agent_status() {
  herdr agent get "$1" 2>/dev/null | jq -r '.result.agent.agent_status // empty' 2>/dev/null || true
}
# The reviewer panel of this session: the configured list, or the five reviewers the worker ships with.
wf_reviewers() { printf '%s\n' "${WF_REVIEWERS:-code,security,docs,tests,senior}"; }
# How many review rounds this session runs at most. The facts print it and panel.sh counts recorded rounds
# against it, so the limit holds across the contexts one review may be spread over. A limit that is no
# number is refused here rather than at the comparison that reads it: a failing `[` reads as false, which
# would end the panel after one round or drop the limit altogether, and say so in no line a reader sees.
wf_review_rounds() {
  local n="${WF_REVIEW_ROUNDS:-3}"
  printf '%s' "$n" | grep -Eq '^[1-9][0-9]*$' || wf_die "WF_REVIEW_ROUNDS='$n' is not a positive number of rounds, e.g. WF_REVIEW_ROUNDS=3"
  printf '%s\n' "$n"
}
# The uncommitted changes of this worktree, indented for a refusal that lists them, and empty when there are
# none. Three writes refuse a dirty tree — a handoff, a round record and the summary — because each of them
# describes a commit; what the three share is the listing, not the sentence that says why.
wf_dirty_tree() {
  local dirty; dirty=$(git status --porcelain)
  [ -z "$dirty" ] || printf '%s' "$dirty" | sed 's/^/  /'
}
# A commit as a reader wants it, and as the caller has it when git cannot resolve it any more: a record
# names a commit an amend or a rebase may have taken away, and a brief still has to be able to print it.
wf_short() { git rev-parse --short "$1" 2>/dev/null || printf '%s\n' "$1"; }
# How long one call of a waiting script blocks: below the ten-minute ceiling of a Bash tool call, which the
# skills set as the call's timeout, so the script answers before the call is cut off (pr-wait.sh, gate.sh).
wf_wait_slice() { printf '%s\n' "${WF_WAIT_SLICE:-540}"; }
# Where a worker stage leaves a fact for the next one (ADR 0018): this worktree's own git directory, never
# the common one, so the workers of two issues in two worktrees keep separate records. Removed with the
# worktree, which is what a pipeline run lives in.
wf_state_dir() {
  local d
  d=$(git rev-parse --path-format=absolute --git-dir 2>/dev/null) || wf_die "not inside a git repository"
  printf '%s/worker' "$d"
}
# A record a worker stage leaves for the next one (ADR 0018): headers, an empty line, then the block it
# carries. Every record in this plugin is read through these two, so their formats cannot drift apart.
# A field is read from the headers only: the block below them quotes gate output, reviewer text or a
# handoff note, none of which the stage that reads a header wrote, and a line of it that looks like a
# header is text in a block, not a fact about the record.
wf_record_field() { sed -n "/^$/q; s/^$2: //p" "$1" | head -1; }
wf_record_body() { sed '1,/^$/d' "$1"; }
wf_repo_owner() { gh repo view --json owner -q .owner.login; }
wf_repo_name() { gh repo view --json name -q .name; }
wf_notify() {
  [ "${HERDR_ENV:-}" = 1 ] || return 0
  herdr notification show "$1" --body "${2:-}" --sound "${3:-done}" >/dev/null 2>&1 || true
}
# ISO-8601 UTC timestamp (2026-09-17T18:45:09Z) to epoch seconds; macOS and GNU date.
wf_epoch() { date -u -j -f '%Y-%m-%dT%H:%M:%SZ' "$1" +%s 2>/dev/null || date -u -d "$1" +%s 2>/dev/null; }
# PR number for the current branch, or empty when it has none. A gh that cannot answer is said as itself
# (gh documents exit 4 for "authentication required"), never as a branch without a pull request: the two
# ask for different fixes, and a caller that guesses sends the worker to open a second pull request.
wf_pr_for_branch() {
  local branch answer status
  branch=$(wf_branch)
  answer=$(gh pr list --head "$branch" --state open --json number -q '.[0].number') || {
    status=$?
    wf_die "gh could not list the open pull requests of branch $branch (gh exit $status); fix gh itself — 'gh auth status' for exit 4, the network otherwise — and run this again"
  }
  printf '%s' "$answer"
}

# A test hunt works on a branch hunt/tests-<date>, which names no issue (ADR 0045): the hunt record in this
# worktree's git directory stands where the issue stands for a ticket.
wf_is_hunt_branch() { case "$(wf_branch)" in hunt/*) return 0 ;; *) return 1 ;; esac; }
# The issue as a brief prints it: the number, or `none` for a branch that works none.
wf_issue_label() {
  local issue; issue=$(wf_issue)
  if [ -n "$issue" ]; then printf '#%s\n' "$issue"
  elif wf_is_hunt_branch; then printf 'none (a test hunt: the hunt record stands for the issue)\n'
  else printf 'none\n'; fi
}

# The test files of the repository in the current directory, one tracked path per line, by the fixed
# conventions of a test hunt. The orchestrator plugin's lib.sh carries the same rule, because it refuses a
# hunt in a repository without test files; a test fails when the two copies find different files.
# shellcheck disable=SC2034  # read by hunt.sh
wf_test_file_rule='test_*.py, *_test.py, *_test.go, *.test.* and *.spec.* (JavaScript and TypeScript), and code files in a tests or spec directory; fixtures, testdata, __snapshots__, node_modules and vendor directories are skipped'
wf_test_files() { git -c core.quotePath=false ls-files 2>/dev/null | wf_test_paths; }
# The paths on stdin that are test files by that rule, one per line.
wf_test_paths() {
  awk '{
    n = split($0, part, "/"); name = part[n]; indir = 0
    for (i = 1; i < n; i++) {
      if (part[i] ~ /^(fixtures|testdata|__snapshots__|node_modules|vendor)$/) next
      if (part[i] == "tests" || part[i] == "spec") indir = 1
    }
    if (name ~ /^test_.*\.py$/ || name ~ /_test\.py$/ || name ~ /_test\.go$/ || name ~ /\.(test|spec)\.(js|jsx|ts|tsx|mjs|cjs|mts|cts)$/) { print; next }
    if (indir && name ~ /\.(py|go|js|jsx|ts|tsx|mjs|cjs|mts|cts|rb|sh|bash|java|kt|rs|php|cs|swift|ex|exs)$/) print
  }'
}
