#!/usr/bin/env bash
# Print the session facts a worker skill needs (mode, issue, base, reviewer panel) as key: value lines.
set -uo pipefail
. "$(dirname "$0")/lib.sh"
wf_kv mode "${WF_MODE:-manual}"
wf_kv issue "#$(wf_issue)"
wf_kv base "$(wf_base_branch)"
wf_kv reviewers "$(wf_reviewers)"
wf_kv max_rounds "${WF_REVIEW_ROUNDS:-3}"

# A claim starts a worker with background tasks disabled, so a subagent's report is the result of the Agent
# call; a session started or restarted by hand has no such setting and its subagents run in the background,
# where ending the turn is how the agent waits (ADR 0017). The truthy set is the one Claude Code itself applies to a
# boolean environment variable (2.1.278: whitespace removed, lowercased, then matched against 1, true, yes, on).
case "$(printf '%s' "${CLAUDE_CODE_DISABLE_BACKGROUND_TASKS:-}" | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')" in
  1|true|yes|on) wf_kv subagents "foreground" ;;
  *) wf_kv subagents "background" ;;
esac

# A handoff is pending once the SessionStart hook has injected its note into a fresh context (ADR 0029):
# this line is how that context's driver learns which stage it starts at, the stages before it having run
# in a context that is gone. Nothing is printed without a handoff, and nothing while the note is still on
# its way to the next context, where it would describe this one instead of it. The next handoff replaces
# the record, and the worktree takes the last one with it.
if state=$(wf_state_dir 2>/dev/null) && [ -f "$state/handoff" ] && [ -n "$(wf_record_field "$state/handoff" injected)" ]; then
  wf_kv resume_stage "$(wf_record_field "$state/handoff" stage)"
fi
