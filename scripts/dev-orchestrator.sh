#!/usr/bin/env bash
# Start the orchestrator from this checkout inside a Herdr pane. The sessions it opens (planner, worker)
# get the checkout's plugins through WF_CLAUDE_ARGS, so nothing needs to be installed or released.
# Usage: scripts/dev-orchestrator.sh [extra claude flags]
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
export WF_CLAUDE_ARGS="--plugin-dir $root/plugins/planner --plugin-dir $root/plugins/worker${WF_CLAUDE_ARGS:+ $WF_CLAUDE_ARGS}"
cd "$root"
exec claude --plugin-dir "$root/plugins/orchestrator" --agent orchestrator "$@"
