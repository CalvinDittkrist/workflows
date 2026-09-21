#!/usr/bin/env bash
# The gate result, run once per review round and handed to the reviewers as a fact (ADR 0019).
# Usage: gate.sh run     run the gate, print its output, record the result for this head
#        gate.sh print   the gate block the review and pull request briefs carry
# The record lives beside the panel summary in this worktree's git directory (ADR 0018), so the workers of
# two issues never overwrite each other's gate result.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

# One command for every repository, fixed by ADR 0008, so nothing here is configurable.
gate_cmd=(make check)
record="$(wf_state_dir)/gate"
log="$(wf_state_dir)/gate.log"
tail_lines=20

# The output is the repository's, not this script's: it is quoted into a brief, so it is indented by two
# spaces. A line of it that imitates a key of this block therefore cannot be read as one.
print_tail() { printf 'gate_output_tail:\n'; sed '1,/^$/d' "$record" | sed 's/^/  /'; }

field() { sed -n "s/^$1: //p" "$record" | head -1; }

# The same phrasing of an exit status wherever a reader meets it.
gate_outcome() { if [ "$1" = 0 ]; then printf 'pass (exit 0)'; else printf 'fail (exit %s)' "$1"; fi; }

case "${1:-}" in
  run)
    commit=$(git rev-parse HEAD 2>/dev/null) || wf_die "this branch has no commit to record a gate run at"
    dirty=no; [ -z "$(git status --porcelain)" ] || dirty=yes
    started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    begin=$(date +%s)
    mkdir -p "$(dirname "$record")"
    # tee, so the caller reads a failing gate in this call instead of running it again for the output.
    set +e
    ( cd "$(git rev-parse --show-toplevel)" && "${gate_cmd[@]}" ) 2>&1 | tee "$log.tmp"
    status=${PIPESTATUS[0]}
    set -e
    mv "$log.tmp" "$log"
    # One file, written in one move: the headers, an empty line, then the tail of the output verbatim.
    # Nothing is parsed out of that output; the gate command is fixed, its output shape is per repository.
    { printf 'commit: %s\ndirty: %s\nstatus: %s\nstarted: %s\nduration: %s\ncommand: %s\nlog: %s\n\n' \
        "$commit" "$dirty" "$status" "$started" "$(( $(date +%s) - begin ))" "${gate_cmd[*]}" "$log"
      tail -n "$tail_lines" "$log"; } > "$record.tmp"
    mv "$record.tmp" "$record"
    note=""; [ "$dirty" = no ] || note=", with a dirty working tree, so no reader counts it for that commit"
    wf_kv gate_recorded "$(gate_outcome "$status") at $(git rev-parse --short "$commit")$note"
    exit "$status"
    ;;
  print)
    if [ ! -f "$record" ]; then
      wf_kv gate_result "none recorded for this head; run the worker's gate.sh run before the reviewers"
      exit 0
    fi
    commit=$(field commit)
    # A record is only ever read for the commit it was taken at: an older one says nothing about this head,
    # and one taken on a dirty working tree says nothing about any commit. Neither is shown in its place.
    if [ "$commit" != "$(git rev-parse HEAD)" ]; then
      wf_kv gate_result "none for this head; the newest record is for $(git rev-parse --short "$commit" 2>/dev/null || printf '%s' "$commit"), which is not this head, so run the gate again"
    elif [ "$(field dirty)" != no ]; then
      wf_kv gate_result "none for this head; the newest record ran with a dirty working tree, so it belongs to no commit; commit and run the gate again"
    else
      wf_kv gate_result "$(gate_outcome "$(field status)") at $(git rev-parse --short "$commit")"
      wf_kv gate_command "$(field command)"
      wf_kv gate_started "$(field started), $(field duration) s"
      wf_kv gate_log "$(field log) (the full output)"
      print_tail
    fi
    ;;
  *) wf_die "usage: gate.sh run | gate.sh print" ;;
esac
