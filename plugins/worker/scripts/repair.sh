#!/usr/bin/env bash
# The repair record: how many CI repair rounds this pull request has had (ADR 0018). The CI stage can hand
# over between two repair rounds (ADR 0032), and a fresh context counts from zero, so the limit is kept by
# this script and not in the worker's head.
# Usage: repair.sh round   count a repair round for this branch's pull request; refuses past the limit
#        repair.sh print   what the record says, counting nothing; the CI stage's brief reads this
#        repair.sh reset   start this pull request's count again, for a round somebody asked for
# A repair round is a fix for failed checks, a merge of the base after a conflict, or a round of
# /worker:address-reviews the CI stage drove. Waiting for CI is none of them: a `waiting` answer runs
# pr-wait.sh again, not the stage, so it counts nothing. Nor is a round somebody asked for by hand: the
# limit bounds the pipeline's own loop, and a maintainer who reads the pull request and requests changes
# has given it a new mandate that the rounds its checks once needed must not refuse. That is what reset
# is for.
#
# Which round this is, however, is not the session's to judge: the count is the one bound on an
# unattended repair loop, and a stage that could start it again whenever it read itself as asked for
# would be no bound at all. WF_REVIEW_MANDATE is the driver's word that this session was started to
# answer a review — a driver that starts a session for a review that asks for changes sets it —
# and without it reset keeps the count and says so, so the address-reviews skill may call it either
# way and the script decides.
#
# The word names the review it stands for (the time it was submitted), because one review is one new
# mandate and not one per round: the record keeps the mandate its count was started for, and a reset
# that names that same mandate again keeps the count. The session started for a review therefore has
# the rounds of that one review and no way to grant itself more — the CI stage of that very session
# invokes this skill again on the next review comments, and each of those rounds is the pipeline's
# own.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

# The one dispatch: a call that names no subcommand of this script is answered with the usage before it
# costs a GitHub round trip.
usage="usage: repair.sh round | repair.sh print | repair.sh reset"
[ $# -eq 1 ] || wf_die "$usage"
case "$1" in round | print | reset) command="$1" ;; *) wf_die "$usage" ;; esac

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
mandate=""
if [ -n "$pr" ] && [ -f "$record" ] && [ "$(wf_record_field "$record" pr)" = "$pr" ]; then
  counted=$(wf_record_field "$record" rounds)
  # This file is the whole guard, so a count it cannot read is refused rather than read as zero: a damaged
  # record would otherwise hand the pull request a fresh set of rounds.
  printf '%s' "$counted" | grep -Eq '^[0-9]+$' ||
    wf_die "the repair record $record names pull request #$pr but its rounds header reads '$counted', not a count; delete the file to start this pull request's count again, and say so when you report"
  taken="$counted"
  # The mandate the count was last started for, so the same one cannot start it again.
  mandate=$(wf_record_field "$record" mandate)
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

# write puts the count of this pull request down, in one move, with no block under the headers: the
# count and the mandate it was started for are the whole record. A round carries the mandate through
# unchanged, so counting a round never hands the review that was answered a second start.
write() {
  mkdir -p "$(dirname "$record")"
  printf 'pr: %s\nrounds: %s\nmandate: %s\nat: %s\n\n' "$pr" "$1" "$2" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$record.tmp"
  mv "$record.tmp" "$record"
  taken="$1"
  mandate="$2"
}

case "$command" in
  round)
    [ -n "$pr" ] ||
      wf_die "branch $(wf_branch) has no open pull request, so there is no repair round to count; open it with /worker:pr first"
    next=$((taken + 1))
    [ "$next" -le "$limit" ] ||
      wf_die "pull request #$pr has had $taken of $limit repair rounds (WF_CI_REPAIR_ROUNDS), so the pipeline stops here: report the failing checks or the unresolved review threads to the maintainer instead of repairing again"
    write "$next" "$mandate"
    report
    [ "$taken" -lt "$limit" ] ||
      printf 'next: this is the last repair round for pull request #%s; if it does not end green, stop and report to the maintainer.\n' "$pr"
    ;;
  reset)
    [ -n "$pr" ] ||
      wf_die "branch $(wf_branch) has no open pull request, so there is no repair record to start again"
    asked="${WF_REVIEW_MANDATE:-}"
    # The word goes into the record, so it is a header line and nothing else: one word of the shape the
    # factory writes, a timestamp, and never a newline that would write a header of its own. The match
    # is on the whole value and not line by line, which is what a grep of a multi-line value would be.
    case "$asked" in
      *[!A-Za-z0-9:._+-]*)
        wf_die "WF_REVIEW_MANDATE='$asked' is no name for the review this session answers; the driver passes the time it was submitted, e.g. WF_REVIEW_MANDATE=2026-09-22T10:00:00Z" ;;
    esac
    if [ -z "$asked" ]; then
      report
      wf_kv repair_reset "no; this session was not started to answer a review (WF_REVIEW_MANDATE is unset), so the count of pull request #$pr stands: the limit bounds the repair rounds the pipeline drives itself"
    elif [ "$asked" = "$mandate" ]; then
      report
      wf_kv repair_reset "no; the count of pull request #$pr was already started again for this review ($asked), and the rounds since are this pipeline's own"
    else
      write 0 "$asked"
      report
      wf_kv repair_reset "yes; this session was started to answer the review of $asked, so pull request #$pr has its rounds again"
    fi
    ;;
  print) report ;;
esac
