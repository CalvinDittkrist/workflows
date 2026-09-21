#!/usr/bin/env bash
# The context checkpoint: how full this session's context window is, and whether it is time to hand over.
# Usage: checkpoint.sh [review|ci]
# The size is read from the value the pane's status line writes into this worktree's git directory
# (ADR 0020). With a stage it is the checkpoint that stage's skill runs on entry, wherever the context came
# from, and a `handoff: yes` carries the procedure the answer asks for; without one it only reports, which is
# how a maintainer reads the size by hand (ADR 0031).
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

# The stages whose skill measures on entry. The same two a handoff resumes at, because a checkpoint hands
# over before the work of the stage it stands in.
stages="review ci"
stage="${1:-}"
[ $# -le 1 ] || wf_die "usage: checkpoint.sh [$(printf '%s' "$stages" | tr ' ' '|')]"
if [ -n "$stage" ]; then
  case " $stages " in *" $stage "*) ;; *) wf_die "'$stage' is not a stage with a checkpoint; usage: checkpoint.sh [$(printf '%s' "$stages" | tr ' ' '|')]" ;; esac
fi

here=$(cd "$(dirname "$0")" && pwd)
value="$(wf_state_dir)/context"
record="$(wf_state_dir)/handoff"
threshold="${WF_HANDOFF_TOKENS:-100000}"
max_age="${WF_CONTEXT_MAX_AGE:-900}"
printf '%s' "$threshold" | grep -Eq '^[1-9][0-9]*$' || wf_die "WF_HANDOFF_TOKENS='$threshold' is not a positive number of tokens, e.g. WF_HANDOFF_TOKENS=100000"
printf '%s' "$max_age" | grep -Eq '^[1-9][0-9]*$' || wf_die "WF_CONTEXT_MAX_AGE='$max_age' is not a positive number of seconds, e.g. WF_CONTEXT_MAX_AGE=900"

# What a `handoff: yes` asks the reader to do. It is printed by the script and not by the skill, because a
# context that entered the stage on its own — a session restarted by hand, a `/worker:work` after a crash —
# never read the driver's text and would otherwise find the answer without the procedure.
procedure() {
  cat <<PROCEDURE
next: hand the $stage stage over to a fresh context and do no work in it.
  1. Commit everything first: the note describes the branch, and handoff.sh refuses a working tree whose
     changes the next context would not see.
  2. Pipe the note into "$here/handoff.sh $stage" with a quoted heredoc (<<'NOTE', never an unquoted one: the
     note quotes findings, paths and commands, and the shell would expand \$x and backticks in it). It needs
     four sections, each a '## ' heading with text under it, in this order: decisions (what you decided and
     why, one line each), rejected (what you tried or considered and did not do, with the reason), verified
     (what you ran and what it said), open (what is unfinished, unsure or a known limit). The script appends
     the base, the commits and the diffstat itself, so the note repeats none of them.
  3. When it prints 'handoff: started for pane <id>', say one line to the user and end your turn without
     another tool call. Never send /clear yourself, and never keep working after handing over.
PROCEDURE
}

# Three keys, always in this order, so every case reads the same way; `reason` explains the cases that are
# not a plain measurement, `details` carries what a real measurement adds, and a stage entrance that hands
# over ends with the procedure, so the last thing its reader sees is what to do.
details=""
report() {
  wf_kv context_tokens "$1"
  wf_kv threshold "$threshold"
  wf_kv handoff "$2"
  [ -z "${3:-}" ] || wf_kv reason "$3"
  [ -z "$details" ] || printf '%s\n' "$details"
  if [ "$2" = yes ] && [ -n "$stage" ]; then procedure; fi
}

# The one skip a context that a handoff started gets at the stage it was started for. The context value is
# per worktree (ADR 0020), so at the first checkpoint of a fresh context it still describes the context that
# is gone and is over the threshold by definition: without this the entrance would hand over again and the
# handovers would loop without doing any work. The skip is bound to the session the SessionStart hook gave
# the note to, so a session that merely finds the record — one restarted by hand, a forced claim that adopted
# the branch — never inherits it, and it is spent at the first entrance so every later checkpoint of that
# session measures. Prints the reason and succeeds when it grants the skip.
claim_skip() {
  local injected session
  [ -f "$record" ] || return 1
  [ "$(wf_record_field "$record" stage)" = "$stage" ] || return 1
  [ -n "$(wf_record_field "$record" injected)" ] || return 1
  [ -z "$(wf_record_field "$record" skip_used)" ] || return 1
  injected=$(wf_record_field "$record" injected_session)
  [ -n "$injected" ] || return 1
  [ -n "${HERDR_PANE_ID:-}" ] || return 1
  command -v herdr >/dev/null 2>&1 || return 1
  session=$(wf_agent_session "${HERDR_PANE_ID}")
  [ -n "$session" ] && [ "$session" = "$injected" ] || return 1
  # Spending the skip is a mark in the record, written the way the hook writes its own: a header prepended in
  # one move. A mark that cannot be written is a warning and not a refusal — refusing here would measure the
  # stale value of the context that is gone and start the very loop the skip exists to prevent, so this one
  # fails open and says so.
  if ! { printf 'skip_used: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"; cat "$record"; } > "$record.tmp" 2>/dev/null ||
     ! mv "$record.tmp" "$record" 2>/dev/null; then
    rm -f "$record.tmp"
    wf_warn "the handoff record in $record could not be marked as entered, so this session may skip the $stage checkpoint again; it measures normally once the record is writable"
  fi
  printf 'this context was started by the handoff that resumes at the %s stage, and a context does one unit of work before it may hand over again; every later checkpoint of this session measures' "$stage"
}

# Outside a Herdr pane no status line runs, so nothing writes the value: a session started by hand, and a
# worker inside the Docker sandbox, which carries no Herdr of its own. That absence is expected there, so it
# is reported as unknown rather than turned into a handoff verdict.
if [ "${HERDR_ENV:-}" != 1 ]; then
  report unavailable unavailable "this session runs outside a Herdr pane (HERDR_ENV is not 1), so no status line measures its context"
  exit 0
fi

tokens=unknown; window=""; at=""
if [ -f "$value" ]; then
  window=$(wf_record_field "$value" context_window_size)
  at=$(wf_record_field "$value" at)
  read_tokens=$(wf_record_field "$value" total_input_tokens)
  if printf '%s' "$read_tokens" | grep -Eq '^[0-9]+$'; then tokens="$read_tokens"; fi
fi

# Before any verdict: the skip comes first because it answers the cases the measurement cannot see, the
# stale value of the context that has just been replaced among them.
if [ -n "$stage" ] && skip=$(claim_skip); then
  report "$tokens" no "$skip"
  exit 0
fi

if [ ! -f "$value" ]; then
  report unknown yes "no context size recorded in $value; the pane's status line has written none, so the size cannot be seen and handing over is the safe answer"
  exit 0
fi
if [ "$tokens" = unknown ]; then
  report unknown yes "$value carries no readable token count, so the size cannot be seen and handing over is the safe answer"
  exit 0
fi

# A value nobody refreshed describes an earlier turn: the status line renders on every turn, so a value older
# than the limit means the measurement stopped, not that the context stopped growing.
epoch=""; [ -z "$at" ] || epoch=$(wf_epoch "$at" 2>/dev/null || true)
if [ -z "$epoch" ]; then
  report "$tokens" yes "$value carries no readable time, so nothing says this size is the current one; handing over is the safe answer"
  exit 0
fi
age=$(( $(date -u +%s) - epoch ))
if [ "$age" -gt "$max_age" ]; then
  report "$tokens" yes "the size was recorded $age s ago, past the $max_age s limit, so it describes an earlier turn; handing over is the safe answer"
  exit 0
fi

details="measured: $at ($age s ago)"
# The model's window, as the status line read it. Not the pane's percentage, which is against the window the
# session compacts at, so the key says whose window this is.
if printf '%s' "$window" | grep -Eq '^[1-9][0-9]*$'; then
  details="$details
model_context_window: $window ($((tokens * 100 / window)) % of it used)"
fi

if [ "$tokens" -ge "$threshold" ]; then
  report "$tokens" yes "at or past the handoff threshold"
else
  report "$tokens" no
fi
