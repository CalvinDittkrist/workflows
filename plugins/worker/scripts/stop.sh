#!/usr/bin/env bash
# The final report of a session that stops after the stage WF_STOP_AFTER names (ADR 0043), printed for the
# driver to give as it stands: a first line that opens with ready:, then the facts the stage that follows on
# the factory's side needs, read from git, from GitHub and from this worktree's records, never from memory.
#   implement  the commits of the branch, on a clean tree
#   gate       the gate result, which has to be a pass recorded for this head
#   review     the recorded panel summary and the gate result
#   pr         the pull request of the branch
# A stage that has not got that far is refused with what is missing, so a stop is never reported early.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"
here=$(dirname "$0")

stage=$(wf_stop_after)
[ -n "$stage" ] || wf_die "WF_STOP_AFTER is not set, so this session runs the whole pipeline and stops after no stage; carry on with the next stage"

clean() {
  local dirty; dirty=$(wf_dirty_tree)
  [ -z "$dirty" ] || wf_die "the working tree has uncommitted changes, and a stop reports commits; commit them and run stop.sh again:
$dirty"
}

gate_passed() {
  [ "$("$here/gate.sh" verdict)" = pass ] ||
    wf_die "the gate has no pass recorded for this head; run the worker's gate.sh run, fix what it reports until it passes, and run stop.sh again"
}

case "$stage" in
  implement)
    clean
    commits=$(git log --reverse --format='%h %s' "$(wf_merge_base)..HEAD")
    [ -n "$commits" ] || wf_die "the branch has no commits beyond $(wf_base_ref); commit the work and run stop.sh again"
    printf 'ready: stopped after implement\n'
    printf 'commits:\n'; printf '%s\n' "$commits" | sed 's/^/  /'
    ;;
  gate)
    clean
    gate_passed
    printf 'ready: stopped after gate\n'
    "$here/gate.sh" print
    ;;
  review)
    clean
    [ -f "$(wf_state_dir)/panel" ] ||
      wf_die "no panel summary is recorded; the review stage ends by recording it with panel.sh record, so finish that stage and run stop.sh again"
    printf 'ready: stopped after review\n'
    "$here/panel.sh" print
    "$here/gate.sh" print
    ;;
  pr)
    pr=$(wf_pr_for_branch)
    [ -n "$pr" ] || wf_die "branch $(wf_branch) has no open pull request; open it with /worker:pr and run stop.sh again"
    url=$(gh pr view "$pr" --json url -q .url) || wf_die "cannot read the URL of pull request #$pr; fix gh and run stop.sh again"
    printf 'ready: %s\n' "$url"
    ;;
esac
