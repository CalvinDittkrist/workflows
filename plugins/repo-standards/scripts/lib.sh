#!/usr/bin/env bash
# Helpers shared by the repo-standards scripts. Sourced, never run.

# has <dir> <name>: a file or directory of exactly this name is in dir. macOS file systems ignore case,
# so [ -e ] would accept claude.md for CLAUDE.md.
has() { local e; for e in "$1"/*; do [ "${e##*/}" = "$2" ] && return 0; done; return 1; }
# first_of <dir> <name>...: the first name present in dir, exact case.
first_of() { local dir=$1 n; shift; for n in "$@"; do has "$dir" "$n" && { printf '%s' "$n"; return; }; done; }
# The standardisation run: the six finding categories, one per auditor, in report order, and the state
# directory inside the git directory, so the audit never changes the working tree.
# shellcheck disable=SC2034 # used by the scripts that source this file
WF_CATEGORIES="files agent-config docs tests-ci workspace security"
state_dir() {
  local d
  d=$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null) || { printf 'error: not inside a git repository; run git init first\n' >&2; return 1; }
  printf '%s/standardize' "$d"
}
