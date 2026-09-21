#!/usr/bin/env bash
# The context checkpoint: how full this session's context window is, and whether it is time to hand over.
# Usage: checkpoint.sh
# The size is read from the value the pane's status line writes into this worktree's git directory
# (ADR 0020). This reports; nothing in the pipeline acts on the verdict yet.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

value="$(wf_state_dir)/context"
threshold="${WF_HANDOFF_TOKENS:-120000}"
max_age="${WF_CONTEXT_MAX_AGE:-900}"
printf '%s' "$threshold" | grep -Eq '^[1-9][0-9]*$' || wf_die "WF_HANDOFF_TOKENS='$threshold' is not a positive number of tokens, e.g. WF_HANDOFF_TOKENS=120000"
printf '%s' "$max_age" | grep -Eq '^[1-9][0-9]*$' || wf_die "WF_CONTEXT_MAX_AGE='$max_age' is not a positive number of seconds, e.g. WF_CONTEXT_MAX_AGE=900"

# Three keys, always in this order, so every case reads the same way; `reason` explains the cases that are
# not a plain measurement.
report() { wf_kv context_tokens "$1"; wf_kv threshold "$threshold"; wf_kv handoff "$2"; [ -z "${3:-}" ] || wf_kv reason "$3"; }

# Outside a Herdr pane no status line runs, so nothing writes the value: a session started by hand, and a
# worker inside the Docker sandbox, which carries no Herdr of its own. That absence is expected there, so it
# is reported as unknown rather than turned into a handoff verdict.
if [ "${HERDR_ENV:-}" != 1 ]; then
  report unavailable unavailable "this session runs outside a Herdr pane (HERDR_ENV is not 1), so no status line measures its context"
  exit 0
fi

if [ ! -f "$value" ]; then
  report unknown yes "no context size recorded in $value; the pane's status line has written none, so the size cannot be seen and handing over is the safe answer"
  exit 0
fi

tokens=$(wf_record_field "$value" total_input_tokens)
window=$(wf_record_field "$value" context_window_size)
at=$(wf_record_field "$value" at)
if ! printf '%s' "$tokens" | grep -Eq '^[0-9]+$'; then
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

if [ "$tokens" -ge "$threshold" ]; then
  report "$tokens" yes "at or past the handoff threshold"
else
  report "$tokens" no
fi
wf_kv measured "$at ($age s ago)"
# The model's window, as the status line read it. Not the pane's percentage, which is against the window the
# session compacts at, so the key says whose window this is.
if printf '%s' "$window" | grep -Eq '^[1-9][0-9]*$'; then wf_kv model_context_window "$window ($((tokens * 100 / window)) % of it used)"; fi
