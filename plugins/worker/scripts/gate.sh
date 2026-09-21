#!/usr/bin/env bash
# The gate result, run once per review round and handed to the reviewers as a fact (ADR 0019).
# Usage: gate.sh run     run the gate, record the result for this head, print the output only when it failed
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
# at length, and `panel.sh round` gates on this word, so the two cannot drift apart.
gate_state() {
  local commit
  [ -f "$record" ] || { printf 'none\n'; return; }
  commit=$(field commit)
  if [ -z "$commit" ] || [ "$commit" != "$(git rev-parse HEAD 2>/dev/null)" ] || [ "$(field dirty)" != no ]; then
    printf 'none\n'; return
  fi
  if [ "$(field status)" = 0 ]; then printf 'pass\n'; else printf 'fail\n'; fi
}

case "${1:-}" in
  run)
    wf_need make
    commit=$(git rev-parse HEAD 2>/dev/null) || wf_die "this branch has no commit to record a gate run at"
    dirty=no; [ -z "$(git status --porcelain)" ] || dirty=yes
    started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    begin=$(date +%s)
    mkdir -p "$(dirname "$record")"
    set +e
    ( cd "$(git rev-parse --show-toplevel)" && "${gate_cmd[@]}" ) > "$log.tmp" 2>&1
    status=$?
    set -e
    mv "$log.tmp" "$log"
    # One file, written in one move: the headers, an empty line, then the tail of the output verbatim.
    # Nothing is parsed out of that output; the gate command is fixed, its output shape is per repository.
    { printf 'commit: %s\ndirty: %s\nstatus: %s\nstarted: %s\nduration: %s\ncommand: %s\nlog: %s\n\n' \
        "$commit" "$dirty" "$status" "$started" "$(( $(date +%s) - begin ))" "${gate_cmd[*]}" "$log"
      tail -n "$tail_lines" "$log"; } > "$record.tmp"
    mv "$record.tmp" "$record"
    # A failing gate is read here, in the call that ran it, instead of being run a second time for its
    # output. A passing one is not: this runs in the worker's own context once per round, and the whole
    # output of a passing gate is 36 KB of "ok" lines nobody reads, in the context the budget is kept in.
    [ "$status" = 0 ] || cat "$log"
    note=""; [ "$dirty" = no ] || note=", with a dirty working tree, so no reader counts it for that commit"
    wf_kv gate_recorded "$(gate_outcome "$status") at $(git rev-parse --short "$commit")$note"
    wf_kv gate_log "$log (the full output)"
    exit "$status"
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
  *) wf_die "usage: gate.sh run | gate.sh print | gate.sh verdict" ;;
esac
