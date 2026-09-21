#!/usr/bin/env bash
# The detached half of the handoff (ADR 0029): clear this pane's session and send the driver command back.
# Usage: handoff-resume.sh <pane> <session-id-before> <stage> <driver-command> [note-record]
# Started by handoff.sh with nohup, because the session it clears is the one that started it: a worker
# cannot clear itself from inside a turn. It waits for that worker's turn to settle and sends `/clear` into
# that session and no other, never before its turn has ended — a pane that has moved on, waits on a dialog or
# is still working keeps its context and the maintainer is told. The driver command follows two signals and
# no guess: the pane reports a session id other than the one it was given, so a fresh context really started,
# and the record is marked, so that context really has the note. Nothing it does is silent: a pane that starts
# no fresh session gets a second `/clear` and then a Herdr notification for the maintainer, and a pane that
# never took the note is reported instead of driven, never a driver command into a context without it.
set -uo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

pane="${1:-}"; before="${2:-}"; stage="${3:-}"; cmd="${4:-}"; record="${5:-}"
[ -n "$pane" ] && [ -n "$before" ] && [ -n "$stage" ] && [ -n "$cmd" ] ||
  wf_die "usage: handoff-resume.sh <pane> <session-id-before> <stage> <driver-command> [note-record]"

# How long a fresh session may take to report itself, and how often the pane is asked. Both are knobs of the
# wait only: nothing here decides anything by time except when to try again. A value that is not a positive
# number falls back to the default rather than ending the handoff, because a detached process has nobody to
# tell; zero is one of them, since `sleep 0` would turn the wait into a loop hammering herdr.
ms="${WF_HANDOFF_SESSION_MS:-60000}"; printf '%s' "$ms" | grep -Eq '^[1-9][0-9]*$' || ms=60000
limit=$(( ms / 1000 )); [ "$limit" -gt 0 ] || limit=1
poll="${WF_HANDOFF_POLL_SECONDS:-1}"; printf '%s' "$poll" | grep -Eq '^([1-9][0-9]*|[0-9]*\.[0-9]*[1-9][0-9]*)$' || poll=1
attempts=2  # the first /clear, and one retry for the pane that swallowed it

session() { wf_agent_session "$pane"; }
# The hook marks the record as it injects the note, so a marked record says a context somewhere has the note
# already. That context is not necessarily the one this handover is starting — any session started in this
# worktree fires the hook — so a marked record is never a reason to drive the pane; it is a reason to stop,
# because clearing again would throw the note away and the driver command would reach a context without it.
taken() { [ -n "$record" ] && [ -f "$record" ] && [ -n "$(wf_record_field "$record" injected)" ]; }
stop() { wf_notify "$1" "$2 The note is on disk: clear the pane by hand if it still holds the old context, then send $cmd there to resume at the $stage stage." alert; exit 1; }
# Everything this script sends is keystrokes, and keystrokes carry an Enter: typed into a permission dialog
# they answer a question the maintainer has not read, typed into a working turn they queue behind work nobody
# asked for. herdr's wait ends on `blocked` and on its timeout too, so the pane's state is read immediately
# before every prompt and never once for all of them — between two of them a minute passes, and the keystroke
# before may be what opened the dialog the next would answer.
require_ended() {
  status=$(wf_agent_status "$pane")
  case "$status" in
    idle|done) ;;
    *) stop "Handoff did not $1 pane $pane" "The pane is ${status:-in no state herdr can name} instead of at the end of its turn, so nothing was typed into it." ;;
  esac
}

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
  require_ended clear
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

# The fresh context is up; the driver command is what tells it to read the note and carry on. A pane that is
# busy with its own start-up gets the moment it needs.
herdr agent wait "$pane" --timeout 60000 >/dev/null 2>&1 || true
# That the note reached it is the second signal, and it is read rather than assumed: a hook that produced
# nothing — no `jq`, an unwritable git dir — leaves a context without the issue and without its stage, and the
# driver command would start it at stage 1 on a branch that already carries the work. The mark is the hook's
# own, so waiting for it is waiting for the hook to have run.
if [ -n "$record" ]; then
  deadline=$(( $(date +%s) + limit ))
  while ! taken; do
    [ "$(date +%s)" -lt "$deadline" ] ||
      stop "Handoff note never reached pane $pane" "A fresh context started there, but nothing injected the note, so the driver command was not sent."
    sleep "$poll"
  done
fi
# The driver command is a keystroke like the `/clear` was, and the wait above ends on `blocked` too: a fresh
# context that opened a trust dialog, or a maintainer who took the pane while the note was landing, must not
# have this Enter answer it. The pane is asked once more, as late as possible, and it must still be the
# context this handover started — not a third one that came up after it.
require_ended resume
fresh=$(session)
[ -n "$fresh" ] && [ "$fresh" != "$before" ] ||
  stop "Handoff did not resume pane $pane" "The pane reports ${fresh:-no session} instead of the fresh context this handover started, so the driver command was not sent."
herdr agent prompt "$pane" "$cmd" >/dev/null 2>&1 ||
  wf_notify "Handoff could not resume pane $pane" "The context was cleared but $cmd was refused; send it by hand to resume at the $stage stage." alert
