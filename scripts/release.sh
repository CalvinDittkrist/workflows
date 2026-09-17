#!/usr/bin/env bash
# Tag a plugin release: bump version in plugin.json first, commit, then run this.
# Usage: release.sh <plugin> [--push]
set -euo pipefail
cd "$(dirname "$0")/.."
plugin="${1:?usage: release.sh <plugin> [--push]}"; shift || true
claude plugin validate "plugins/$plugin" --strict
claude plugin tag "plugins/$plugin" -m "$plugin v%s" "$@"
