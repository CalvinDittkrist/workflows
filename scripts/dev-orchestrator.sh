#!/usr/bin/env bash
# Start the orchestrator from this checkout inside a Herdr pane. The sessions it opens get the checkout's
# planner and worker plugins through the per-session args, so nothing needs to be installed or released.
# Usage: scripts/dev-orchestrator.sh [extra claude flags]
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
export WF_PLANNER_CLAUDE_ARGS="--plugin-dir $root/plugins/planner${WF_PLANNER_CLAUDE_ARGS:+ $WF_PLANNER_CLAUDE_ARGS}"
export WF_WORKER_CLAUDE_ARGS="--plugin-dir $root/plugins/worker${WF_WORKER_CLAUDE_ARGS:+ $WF_WORKER_CLAUDE_ARGS}"
cd "$root"
exec claude --plugin-dir "$root/plugins/orchestrator" --agent orchestrator "$@"
