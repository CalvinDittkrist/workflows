#!/usr/bin/env bash
# Print the session facts a worker skill needs (mode, issue, base, reviewer panel) as key: value lines.
set -uo pipefail
. "$(dirname "$0")/lib.sh"
wf_kv mode "${WF_MODE:-manual}"
wf_kv issue "#$(wf_issue)"
wf_kv base "$(wf_base_branch)"
wf_kv reviewers "${WF_REVIEWERS:-code,security,docs,tests,senior}"
wf_kv max_rounds "${WF_REVIEW_ROUNDS:-3}"

# A claim starts a worker with background tasks disabled, so a subagent's report is the result of the Agent
# call; a session started or restarted by hand has no such setting and its subagents run in the background,
# where ending the turn is how the agent waits (ADR 0016). The truthy set is the one Claude Code itself applies to a
# boolean environment variable (2.1.278: whitespace removed, lowercased, then matched against 1, true, yes, on).
case "$(printf '%s' "${CLAUDE_CODE_DISABLE_BACKGROUND_TASKS:-}" | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')" in
  1|true|yes|on) wf_kv subagents "foreground" ;;
  *) wf_kv subagents "background" ;;
esac
