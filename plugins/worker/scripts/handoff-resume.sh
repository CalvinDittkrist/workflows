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
# The hook marks the record as it injects the note, so this is the other half of the confirmation: a context
# that has the note exists, even when the session id could not be read. From here on the pane needs the
# driver command and nothing else — a `/clear` would throw the note away instead of delivering it.
taken() { [ -n "$record" ] && [ -f "$record" ] && [ -n "$(wf_record_field "$record" injected)" ]; }

# The worker that asked for the handoff is still finishing its turn. `/clear` typed into a working agent
# would land in the queue of that turn, so the wait is first and everything else follows it. Without
# `--until` herdr waits for the first settled state, which is the three of them: a worker that ends its turn
# settles as `done`, not as `idle`, and a wait for `idle` alone sits through the whole timeout (measured in
# the live run of this change).
herdr agent wait "$pane" --timeout 600000 >/dev/null 2>&1 || true

# `/clear` goes to the context that asked for it and to no other. The wait ends on a settled state, but it
# also ends on its timeout, and a pane the maintainer has taken over in the meantime holds someone else's
# work: clearing that would destroy a context this handover never created.
now=$(session)
if [ -n "$now" ] && [ "$now" != "$before" ] && ! taken; then
  wf_notify "Handoff did not clear pane $pane" "The pane holds a session other than the one that asked for the handover, so nothing was cleared. The note is on disk; send $cmd to a fresh context there to resume at the $stage stage." alert
  exit 1
fi

attempt=1
while ! taken; do
  herdr agent prompt "$pane" "/clear" >/dev/null 2>&1 || true
  deadline=$(( $(date +%s) + limit ))
  while :; do
    now=$(session)
    if [ -n "$now" ] && [ "$now" != "$before" ]; then break 2; fi
    if taken; then break 2; fi
    [ "$(date +%s)" -lt "$deadline" ] || break
    sleep "$poll"
  done
  if [ "$attempt" -ge "$attempts" ]; then
    wf_notify "Handoff stalled in pane $pane" "No fresh session after $attempts /clear attempts. The handoff note is recorded; clear the pane by hand and send $cmd to resume at the $stage stage." alert
    exit 1
  fi
  attempt=$(( attempt + 1 ))
done

# The fresh context is up and its SessionStart hook has injected the note; the driver command is what tells
# it to read that note and carry on. A pane that is busy with its own start-up gets the moment it needs.
herdr agent wait "$pane" --timeout 60000 >/dev/null 2>&1 || true
herdr agent prompt "$pane" "$cmd" >/dev/null 2>&1 ||
  wf_notify "Handoff could not resume pane $pane" "The context was cleared but $cmd was refused; send it by hand to resume at the $stage stage." alert
