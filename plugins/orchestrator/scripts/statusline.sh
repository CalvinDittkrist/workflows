#!/usr/bin/env bash
# The status line of a worker pane: issue, mode and context size, and the context value the worker reads.
# Usage: statusline.sh [compact-trigger]   configured by claim.sh as the session's statusLine.command;
#        Claude Code pipes its status line JSON in. The argument is the context size the session compacts at,
#        when it is set: the size is shown against the smaller of it and the model's window.
# It is rendered on every turn, so it prints a line whatever the input is: a missing field costs a segment,
# never the line. The value it writes is the contract with the worker plugin (ADR 0020); no code is shared.
# No -e: this runs on every render and a lookup that fails costs a segment of the line, never the line.
set -uo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"  # functions only; the branch convention lives there and is not copied here

input=$(cat)

# Everything the line and the value need, in one jq call, because the render is in the way of the terminal.
# Empty for anything the input does not carry; the numeric checks below fork nothing.
fields=$(printf '%s' "$input" | jq -r '[
    (.context_window.total_input_tokens // ""),
    (.context_window.context_window_size // ""),
    (.cwd // .workspace.current_dir // "")
  ] | @tsv' 2>/dev/null) || fields=""
IFS=$'\t' read -r tokens window cwd <<EOF
$fields
EOF
num() { case "${1:-}" in "" | *[!0-9]*) return 1 ;; *) return 0 ;; esac; }
num "$tokens" || tokens=""
num "$window" || window=""

# The value belongs to the worktree the session works in, which is the directory the input names. When that
# directory cannot be entered, nothing is written: the process's own directory is some other repository, and a
# size written there would be read by a worker it does not describe.
here_ok=1
[ -z "$cwd" ] || cd "$cwd" 2>/dev/null || here_ok=0

# Issue and mode come from the session settings claim.sh writes; the branch answers for a session started by
# hand, and a pane whose branch names no issue still gets a line.
issue="${WF_ISSUE:-}"
[ -n "$issue" ] || issue=$(wf_issue_from_branch "$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)")

# The value file: the latest context size with the time it was read, in this worktree's own git directory, so
# the workers of two issues never read each other's size. Written whole, by a move, because the worker's
# checkpoint may read it while a render writes it, and the temp file goes with a render Claude Code cuts
# short. Nothing is written before the first response of the session, when the input carries no numbers: an
# invented zero would read as a nearly empty context.
if [ "$here_ok" = 1 ] && [ -n "$tokens" ] && [ "$tokens" -gt 0 ]; then
  if gitdir=$(git rev-parse --path-format=absolute --git-dir 2>/dev/null); then
    dir="$gitdir/worker"
    tmp="$dir/context.$$.tmp"
    trap 'rm -f "$tmp" 2>/dev/null' EXIT HUP INT TERM
    if mkdir -p "$dir" 2>/dev/null &&
      { printf 'total_input_tokens: %s\n' "$tokens"
        [ -z "$window" ] || printf 'context_window_size: %s\n' "$window"
        printf 'at: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
      } > "$tmp" 2>/dev/null; then
      mv "$tmp" "$dir/context" 2>/dev/null
    fi
  fi
fi

# The line itself: what the maintainer sees of this pane across the whole workspace, so it is short and the
# context size is the part that changes. The size is shown against the size this session really reaches: the
# model's window, or the compact trigger given as the argument when that is smaller. A session on a
# million-token model that compacts at 160k is at a sixth of its window there, and 6 % would read as room it
# does not have.
shown="$window"
if num "${1:-}" && [ "${1:-0}" -gt 0 ]; then
  { [ -n "$shown" ] && [ "$shown" -le "$1" ]; } || shown="$1"
fi
if [ -z "$tokens" ]; then
  context="context n/a"
  command -v jq >/dev/null 2>&1 || context="context n/a (jq missing)"
elif [ -n "$shown" ] && [ "$shown" -gt 0 ]; then
  context=$(printf '%sk/%sk (%s%%)' "$((tokens / 1000))" "$((shown / 1000))" "$((tokens * 100 / shown))")
else
  context=$(printf '%sk' "$((tokens / 1000))")
fi
printf '%s · %s · %s\n' "${issue:+#}${issue:-no issue}" "${WF_MODE:-mode?}" "$context"
