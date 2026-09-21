#!/usr/bin/env bash
# The detached half of the handoff (ADR 0021): clear this pane's session and send the driver command back.
# Usage: handoff-resume.sh <pane> <session-id-before> <stage> <driver-command> [note-record]
# Started by handoff.sh with nohup, because the session it clears is the one that started it: a worker
# cannot clear itself from inside a turn. It waits for that worker's turn to settle, sends `/clear` into that
# session and no other — a pane that has moved on keeps its context and the maintainer is told — and only when
# the pane reports a session id other than the one it was given — the one signal that a fresh context really
# started — sends the driver command. Nothing it does is silent: a pane that never starts one gets a second
# `/clear` and then a Herdr notification for the maintainer, never a driver command into the old context.
set -uo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

pane="${1:-}"; before="${2:-}"; stage="${3:-}"; cmd="${4:-}"; record="${5:-}"
[ -n "$pane" ] && [ -n "$before" ] && [ -n "$stage" ] && [ -n "$cmd" ] ||
  wf_die "usage: handoff-resume.sh <pane> <session-id-before> <stage> <driver-command> [note-record]"

# How long a fresh session may take to report itself, and how often the pane is asked. Both are knobs of the
# wait only: nothing here decides anything by time except when to try again. A value that is not a number
# falls back to the default rather than ending the handoff, because a detached process has nobody to tell.
ms="${WF_HANDOFF_SESSION_MS:-60000}"; printf '%s' "$ms" | grep -Eq '^[1-9][0-9]*$' || ms=60000
limit=$(( ms / 1000 )); [ "$limit" -gt 0 ] || limit=1
poll="${WF_HANDOFF_POLL_SECONDS:-1}"; printf '%s' "$poll" | grep -Eq '^[0-9]+(\.[0-9]+)?$' || poll=1
attempts=2  # the first /clear, and one retry for the pane that swallowed it

session() { wf_agent_session "$pane"; }
# The hook marks the record as it injects the note, so a marked record says a context somewhere has the note
# already. That context is not necessarily the one this handover is starting — any session started in this
# worktree fires the hook — so a marked record is never a reason to drive the pane; it is a reason to stop,
# because clearing again would throw the note away and the driver command would reach a context without it.
taken() { [ -n "$record" ] && [ -f "$record" ] && [ -n "$(wf_record_field "$record" injected)" ]; }
stop() { wf_notify "$1" "$2 The note is on disk: clear the pane by hand if it still holds the old context, then send $cmd there to resume at the $stage stage." alert; exit 1; }

# The worker that asked for the handoff is still finishing its turn. `/clear` typed into a working agent
# would land in the queue of that turn, so the wait is first and everything else follows it. Without
# `--until` herdr waits for the first of its settled states — `idle`, `done` or `blocked` — and a worker that
# ends its turn settles as `done`: a wait for `idle` alone sat through the whole timeout and the pane was
# never cleared (measured in the live run of this change).
herdr agent wait "$pane" --timeout 600000 >/dev/null 2>&1 || true

# `/clear` goes to the context that asked for it and to no other. The wait ends on a settled state, but it
# also ends on its timeout, and a pane the maintainer has taken over in the meantime holds someone else's
# work. An id that cannot be read counts as someone else's: `handoff.sh` read one seconds ago, so a pane that
# names none now is a pane nothing can reason about.
now=$(session)
[ "$now" = "$before" ] ||
  stop "Handoff did not clear pane $pane" "The pane reports ${now:-no session} instead of the context that asked for the handover, so nothing was cleared."
! taken ||
  stop "Handoff note went to another context" "A session this handover did not start has taken the note, so pane $pane was left alone."

attempt=1
while :; do
  herdr agent prompt "$pane" "/clear" >/dev/null 2>&1 || true
  # The fresh session reports itself under a new id. Nothing else confirms it: a `/clear` that was swallowed
  # and a `/clear` that worked look the same from here, and the driver command must never reach the context
  # that asked for the handover.
  deadline=$(( $(date +%s) + limit ))
  while :; do
    now=$(session)
    if [ -n "$now" ] && [ "$now" != "$before" ]; then break 2; fi
    [ "$(date +%s)" -lt "$deadline" ] || break
    sleep "$poll"
  done
  ! taken ||
    stop "Handoff note went to another context" "The note was injected while pane $pane still reported the context that asked for the handover, so the fresh context it was meant for is not the one that has it."
  [ "$attempt" -lt "$attempts" ] ||
    stop "Handoff stalled in pane $pane" "No fresh session after $attempts /clear attempts."
  attempt=$(( attempt + 1 ))
done

# The fresh context is up and its SessionStart hook has injected the note; the driver command is what tells
# it to read that note and carry on. A pane that is busy with its own start-up gets the moment it needs.
herdr agent wait "$pane" --timeout 60000 >/dev/null 2>&1 || true
herdr agent prompt "$pane" "$cmd" >/dev/null 2>&1 ||
  wf_notify "Handoff could not resume pane $pane" "The context was cleared but $cmd was refused; send it by hand to resume at the $stage stage." alert
