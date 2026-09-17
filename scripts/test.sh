#!/usr/bin/env bash
# Local CI: lint, validate, test. Same steps as .github/workflows/ci.yml.
set -euo pipefail
cd "$(dirname "$0")/.."
if command -v shellcheck >/dev/null 2>&1; then shellcheck -s bash plugins/*/scripts/*.sh; else echo "skip: shellcheck not installed (brew install shellcheck)"; fi
for f in plugins/*/scripts/*.sh; do bash -n "$f"; done
claude plugin validate . --strict
for p in plugins/*; do [ -f "$p/.claude-plugin/plugin.json" ] && claude plugin validate "$p" --strict; done
for d in plugins/*/skills plugins/*/agents; do [ -d "$d" ] && claude plugin validate "$d" --strict; done
python3 -m unittest discover -s tests -v
