#!/usr/bin/env bash
# The gate result, run once per review and handed to the reviewers as a fact (ADR 0019).
# Usage: gate.sh run     start the gate for this head, detached from the call, and wait one slice for it: on its
#                        end print the record, and the output only when it failed; while it lasts exit 3
#        gate.sh wait    wait one more slice for the gate in flight, and answer the same way
#        gate.sh print   the gate block the review and pull request briefs carry
#        gate.sh verdict pass, fail or none for this head, the one word another script gates on
# The record lives beside the panel summary in this worktree's git directory (ADR 0018), so the workers of
# two issues never overwrite each other's gate result.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

# One command for every repository, fixed by ADR 0008, so nothing here is configurable.
gate_cmd=(make check)
record="$(wf_state_dir)/gate"
log="$(wf_state_dir)/gate.log"
tail_lines=10  # five reviewers read this block; the full output is one file read away

# The output is the repository's, not this script's: it is quoted into a brief, so it is indented by two
# spaces. A line of it that imitates a key of this block therefore cannot be read as one.
print_tail() {
  # A tail without text is stated, not shown as a bare key a reader would have to tell apart from a
  # truncation. It says what it knows: these lines carry nothing. Whether the gate printed anything at all
  # is a question for the log, because the record keeps the last lines only.
  if ! wf_record_body "$record" | grep -q .; then
    wf_kv gate_output_tail "(blank: the last $tail_lines lines of the output carry no text; gate_log has all of it)"
    return
  fi
  printf 'gate_output_tail:\n'; wf_record_body "$record" | sed 's/^./  &/'
}

field() { wf_record_field "$record" "$1"; }

# The same phrasing of an exit status wherever a reader meets it.
gate_outcome() { if [ "$1" = 0 ]; then printf 'pass (exit 0)'; else printf 'fail (exit %s)' "$1"; fi; }

# What the record says about this head, in one word. A record is only ever read for the commit it was taken
# at on a clean tree: an older one says nothing about this head, and one taken on a dirty working tree says
# nothing about any commit, so both read as "none" rather than as a result. `print` states the same decision
# at length, and `panel.sh record` gates on this word, so the two cannot drift apart.
gate_state() {
  local commit
  [ -f "$record" ] || { printf 'none\n'; return; }
  commit=$(field commit)
  if [ -z "$commit" ] || [ "$commit" != "$(git rev-parse HEAD 2>/dev/null)" ] || [ "$(field dirty)" != no ]; then
    printf 'none\n'; return
  fi
  if [ "$(field status)" = 0 ]; then printf 'pass\n'; else printf 'fail\n'; fi
}

# The gate runs detached from the call that starts it: a Bash tool call ends at a ceiling of ten minutes,
# and a full gate on a slow host lasts longer (issue #108). `run` starts it and `wait` waits for it, each
# for one slice below that ceiling, so no call is ever cut off and the gate never is either; whichever
# call sees it end reports the record the detached run wrote, exactly as a run in the call would have.
# `running` names the run in flight, so a second `run` joins it instead of starting another make beside it.
running="$(wf_state_dir)/gate.running"
slice="${WF_WAIT_SLICE:-540}"  # seconds one call waits, the same slice pr-wait.sh keeps under the ceiling

# The run itself, in a subshell that outlives the call. It ignores the hangup of a closing terminal, and
# its record carries the id of its run, which is how a waiter tells this run's record from an older one.
detached_run() {
  local id=$1 commit=$2 dirty=$3 started=$4 begin status
  trap '' HUP
  begin=$(date +%s)
  set +e
  ( cd "$(git rev-parse --show-toplevel)" && "${gate_cmd[@]}" ) < /dev/null > "$log.tmp" 2>&1
  status=$?
  set -e
  mv "$log.tmp" "$log"
  # One file, written in one move: the headers, an empty line, then the tail of the output verbatim.
  # Nothing is parsed out of that output; the gate command is fixed, its output shape is per repository.
  { printf 'commit: %s\ndirty: %s\nstatus: %s\nstarted: %s\nduration: %s\ncommand: %s\nlog: %s\nrun: %s\n\n' \
      "$commit" "$dirty" "$status" "$started" "$(( $(date +%s) - begin ))" "${gate_cmd[*]}" "$log" "$id"
    tail -n "$tail_lines" "$log"; } > "$record.tmp"
  mv "$record.tmp" "$record"
}

# Whether the run `running` names is still at work: its process is alive and is this script, which a pid
# the system handed to something else after a reboot or a kill is not.
run_alive() {
  local pid
  pid=$(wf_record_field "$running" pid)
  [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null && ps -p "$pid" -o command= 2>/dev/null | grep -q 'gate\.sh'
}

# The record `running` waits for has been written: the record names the same run.
run_recorded() { [ -f "$record" ] && [ "$(field run)" = "$(wf_record_field "$running" run)" ]; }

# What a finished run says, read from its record alone, so every call reports it the same way.
report_record() {
  local status commit
  status=$(field status); commit=$(field commit)
  # A failing gate is read here, in the call that saw it end, instead of being run a second time for its
  # output. A passing one is not: this runs in the worker's own context, and the whole
  # output of a passing gate is 36 KB of "ok" lines nobody reads, in the context the budget is kept in.
  [ "$status" = 0 ] || cat "$(field log)"
  note=""; [ "$(field dirty)" = no ] || note=", with a dirty working tree, so no reader counts it for that commit"
  wf_kv gate_recorded "$(gate_outcome "$status") at $(wf_short "$commit")$note"
  wf_kv gate_log "$(field log) (the full output)"
  exit "$status"
}

# Wait one slice for the run in flight, then say how it stands: its record once it ends, a `running` line
# and the call to make next while it lasts, and an error when its process is gone and left no record.
wait_for_run() {
  local since; since=$(date +%s)
  while :; do
    if run_recorded; then rm -f "$running"; report_record; fi
    if ! run_alive; then
      # It may have written its record and exited between the two questions.
      if run_recorded; then rm -f "$running"; report_record; fi
      local at
      at=$(wf_short "$(wf_record_field "$running" commit)")
      rm -f "$running"
      wf_die "the gate started at $at ended without a record, so it was killed or its host went down; run the worker's gate.sh run again"
    fi
    if [ $(( $(date +%s) - since )) -ge "$slice" ]; then
      wf_kv gate_running "at $(wf_short "$(wf_record_field "$running" commit)") since $(wf_record_field "$running" started), $(( $(date +%s) - $(wf_record_field "$running" begin) )) s so far"
      printf "help: the gate runs on without this call; run the worker's gate.sh wait with the Bash tool timeout set to 600000 ms until it reports gate_recorded\n"
      exit 3
    fi
    sleep 1
  done
}

case "${1:-}" in
  run)
    wf_need make
    commit=$(git rev-parse HEAD 2>/dev/null) || wf_die "this branch has no commit to record a gate run at"
    dirty=no; [ -z "$(git status --porcelain)" ] || dirty=yes
    mkdir -p "$(dirname "$record")"
    if [ -f "$running" ] && ! run_recorded && run_alive; then
      # One gate at a time in a worktree: a second make beside the first would race it for the same files.
      # The run in flight answers this call when it is the one this call would start.
      [ "$(wf_record_field "$running" commit)" = "$commit" ] && [ "$(wf_record_field "$running" dirty)" = "$dirty" ] ||
        wf_die "a gate is running at $(wf_short "$(wf_record_field "$running" commit)") since $(wf_record_field "$running" started), not at this head; run the worker's gate.sh wait until it ends, then gate.sh run again"
      wait_for_run
    fi
    started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    begin=$(date +%s)
    id="$begin-$$"
    # Twice detached: `set -m` gives the run a process group of its own, so a signal to the call's group
    # misses it, and the subshell around it exits at once, so the run is no child of the call either.
    pid=$(set -m; ( detached_run "$id" "$commit" "$dirty" "$started" ) < /dev/null > /dev/null 2>&1 & printf '%s' "$!")
    printf 'pid: %s\nrun: %s\ncommit: %s\ndirty: %s\nstarted: %s\nbegin: %s\n' "$pid" "$id" "$commit" "$dirty" "$started" "$begin" > "$running.tmp"
    mv "$running.tmp" "$running"
    wait_for_run
    ;;
  wait)
    # Nothing in flight: the newest record is the answer, so a call after the end reads the same result.
    if [ ! -f "$running" ]; then
      [ -f "$record" ] || wf_die "no gate is running and none is recorded; start one with the worker's gate.sh run"
      report_record
    fi
    wait_for_run
    ;;
  print)
    commit=""; [ ! -f "$record" ] || commit=$(field commit)
    # Why the record is no result for this head, in the words of the case it is: the decision itself is
    # gate_state's, so the block and the one word can never say different things about the same record.
    if [ "$(gate_state)" != none ]; then
      wf_kv gate_result "$(gate_outcome "$(field status)") at $(git rev-parse --short "$commit")"
      wf_kv gate_command "$(field command)"
      wf_kv gate_started "$(field started), $(field duration) s"
      wf_kv gate_log "$(field log) (the full output)"
      print_tail
    elif [ -n "$commit" ] && [ "$(field dirty)" = no ] && [ "$(field status)" = 0 ] && git merge-base --is-ancestor "$commit" HEAD 2>/dev/null; then
      # A pass at an earlier commit of this branch is the fact the rounds after the first are briefed with:
      # the fixes of a round are read by the next round and gated once, before the summary, so the block
      # says how far the head is from the commit the gate ran on instead of hiding a pass behind "none".
      wf_kv gate_result "pass (exit 0) at $(wf_short "$commit"), $(git rev-list --count "$commit..HEAD") commit(s) since, which it did not run on; the gate runs again before the summary"
      wf_kv gate_command "$(field command)"
      wf_kv gate_started "$(field started), $(field duration) s"
      wf_kv gate_log "$(field log) (the full output)"
      print_tail
    elif [ ! -f "$record" ]; then
      wf_kv gate_result "none recorded for this head; run the worker's gate.sh run before the reviewers"
    elif [ -z "$commit" ]; then
      wf_kv gate_result "none recorded for this head; the record names no commit, so run the worker's gate.sh run again"
    elif [ "$commit" != "$(git rev-parse HEAD)" ]; then
      wf_kv gate_result "none for this head; the newest record is for $(wf_short "$commit"), which is not this head, so run the gate again"
    elif [ "$(field dirty)" != no ]; then
      wf_kv gate_result "none for this head; the newest record ran with a dirty working tree, so it belongs to no commit; commit what belongs to the change, ignore or remove what does not, and run the gate again"
    else
      # gate_state owns the decision, so a case it grows that this chain does not name is reported as what it
      # is rather than as the last case that happened to be written here.
      wf_kv gate_result "none for this head; the newest record does not answer for it, so run the worker's gate.sh run again"
    fi
    ;;
  # The one word another script gates on, so it reads a contract rather than scraping the brief.
  verdict) gate_state ;;
  *) wf_die "usage: gate.sh run | gate.sh wait | gate.sh print | gate.sh verdict" ;;
esac
