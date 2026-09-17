#!/usr/bin/env bash
# Detached cleanup after a yolo merge: remove worktree (and Herdr workspace), delete branches.
# Usage: cleanup-self.sh <main-root> <worktree-path> <branch> [<herdr-workspace-id>]
set -uo pipefail
main="$1"; path="$2"; branch="$3"; ws="${4:-}"
sleep 5
cd "$main" || exit 1
if [ -n "$ws" ] && [ "${HERDR_ENV:-}" = 1 ]; then herdr worktree remove --workspace "$ws" --force >/dev/null 2>&1 || git worktree remove --force "$path"; else git worktree remove --force "$path"; fi
git worktree prune
git branch -D "$branch" >/dev/null 2>&1 || true
git push origin --delete "$branch" >/dev/null 2>&1 || true
git fetch -q --prune origin || true
