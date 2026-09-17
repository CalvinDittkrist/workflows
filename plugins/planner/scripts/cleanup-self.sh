#!/usr/bin/env bash
# Detached cleanup after finish: remove the worktree (and Herdr workspace) and the local plan branch.
# Usage: cleanup-self.sh <main-root> <worktree-path> <branch> [<herdr-workspace-id>]
set -uo pipefail
main="$1"; path="$2"; branch="$3"; ws="${4:-}"
sleep 5
cd "$main" || exit 1
if [ -n "$ws" ] && [ "${HERDR_ENV:-}" = 1 ]; then herdr worktree remove --workspace "$ws" --force >/dev/null 2>&1 || git worktree remove --force "$path"; else git worktree remove --force "$path"; fi
git worktree prune
git branch -D "$branch" >/dev/null 2>&1 || true
