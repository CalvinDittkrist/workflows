#!/usr/bin/env bash
# The repair record: how many CI repair rounds this pull request has had (ADR 0018). The CI stage can hand
# over between two repair rounds (ADR 0032), and a fresh context counts from zero, so the limit is kept by
# this script and not in the worker's head.
# Usage: repair.sh round   count a repair round for this branch's pull request; refuses past the limit
#        repair.sh print   what the record says, counting nothing; the CI stage's brief reads this
# A repair round is a fix for failed checks or a round of /worker:address-reviews. Waiting for CI is
# neither: a `waiting` answer runs pr-wait.sh again, not the stage, so it counts nothing.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

# The one dispatch: a call that names no subcommand of this script is answered with the usage before it
# costs a GitHub round trip.
usage="usage: repair.sh round | repair.sh print"
[ $# -eq 1 ] || wf_die "$usage"
case "$1" in round | print) command="$1" ;; *) wf_die "$usage" ;; esac

# The pull request the record belongs to is read from GitHub, so a missing gh is said as itself and never
# mistaken for a branch without a pull request.
wf_need gh
record="$(wf_state_dir)/repair"
limit="${WF_CI_REPAIR_ROUNDS:-3}"
printf '%s' "$limit" | grep -Eq '^[1-9][0-9]*$' ||
  wf_die "WF_CI_REPAIR_ROUNDS='$limit' is not a positive number of repair rounds, e.g. WF_CI_REPAIR_ROUNDS=3"

# The record belongs to the pull request it names, which is the one this branch has open. A second pull
# request for the branch — opened after the first was closed, or for other work of this worktree — carries
# another number, so the record reads as none and the count starts at one: its checks have failed no round
# yet. Reading it costs one GitHub call and never the model's memory of which pull request this is.
pr=$(wf_pr_for_branch)
taken=0
if [ -n "$pr" ] && [ -f "$record" ] && [ "$(wf_record_field "$record" pr)" = "$pr" ]; then
  counted=$(wf_record_field "$record" rounds)
  # This file is the whole guard, so a count it cannot read is refused rather than read as zero: a damaged
  # record would otherwise hand the pull request a fresh set of rounds.
  printf '%s' "$counted" | grep -Eq '^[0-9]+$' ||
    wf_die "the repair record $record names pull request #$pr but its rounds header reads '$counted', not a count; delete the file to start this pull request's count again, and say so when you report"
  taken="$counted"
fi

# Three keys, always in this order, so a round and a plain reading are read the same way.
report() {
  if [ -n "$pr" ]; then
    wf_kv repair_pr "#$pr"
  else
    wf_kv repair_pr "none; branch $(wf_branch) has no open pull request, so no repair record is read"
  fi
  wf_kv repair_rounds_taken "$taken"
  wf_kv repair_limit "$limit"
}

case "$command" in
  round)
    [ -n "$pr" ] ||
      wf_die "branch $(wf_branch) has no open pull request, so there is no repair round to count; open it with /worker:pr first"
    next=$((taken + 1))
    [ "$next" -le "$limit" ] ||
      wf_die "pull request #$pr has had $taken of $limit repair rounds (WF_CI_REPAIR_ROUNDS), so the pipeline stops here: report the failing checks or the unresolved review threads to the maintainer instead of repairing again"
    mkdir -p "$(dirname "$record")"
    # One file, written in one move, with no block under the headers: the count is the whole record.
    printf 'pr: %s\nrounds: %s\nat: %s\n\n' "$pr" "$next" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$record.tmp"
    mv "$record.tmp" "$record"
    taken="$next"
    report
    [ "$taken" -lt "$limit" ] ||
      printf 'next: this is the last repair round for pull request #%s; if it does not end green, stop and report to the maintainer.\n' "$pr"
    ;;
  print) report ;;
esac
