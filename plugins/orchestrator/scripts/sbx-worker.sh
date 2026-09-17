#!/usr/bin/env bash
# Run a worker session inside a Docker Sandbox (sbx) for one worktree.
# Usage: sbx-worker.sh <worktree-path> [-- <claude args...>]
# The sandbox is named wf-<repo>-<branch>. Plugins are installed inside the sandbox from the
# marketplace named in WF_MARKETPLACE (default CalvinDittkrist/workflows).
set -euo pipefail
path="${1:-}"; [ -n "$path" ] || { echo "usage: sbx-worker.sh <worktree-path> [-- <claude args...>]" >&2; exit 1; }
shift; [ "${1:-}" = "--" ] && shift
command -v sbx >/dev/null 2>&1 || { echo "error: sbx (Docker Sandboxes) is not installed" >&2; exit 1; }
path=$(cd "$path" && pwd)
repo=$(basename "$(dirname "$(git -C "$path" rev-parse --path-format=absolute --git-common-dir)")")
branch=$(git -C "$path" rev-parse --abbrev-ref HEAD | tr '/' '-')
name="wf-$repo-$branch"
market="${WF_MARKETPLACE:-CalvinDittkrist/workflows}"

if ! sbx ls 2>/dev/null | grep -q "^$name\b"; then
  # Mount only this worktree read-write; the shared skills store stays read-only.
  sbx create claude "$path" --name "$name" --skills readonly -e WF_MODE -e WF_ISSUE -e WF_BASE_BRANCH -q
  sbx exec "$name" sh -c "claude plugin marketplace add '$market' >/dev/null && claude plugin install worker@workflows --scope user >/dev/null && claude plugin install repo-standards@workflows --scope user >/dev/null" \
    || echo "warning: plugin install inside sandbox failed; the worker skills may be missing" >&2
fi
exec sbx run --name "$name" -- "$@"
