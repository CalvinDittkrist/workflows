#!/usr/bin/env bash
# Helpers shared by the repo-standards scripts. Sourced, never run.

# has <dir> <name>: a file or directory of exactly this name is in dir. macOS file systems ignore case,
# so [ -e ] would accept claude.md for CLAUDE.md.
has() { local e; for e in "$1"/*; do [ "${e##*/}" = "$2" ] && return 0; done; return 1; }
# first_of <dir> <name>...: the first name present in dir, exact case.
first_of() { local dir=$1 n; shift; for n in "$@"; do has "$dir" "$n" && { printf '%s' "$n"; return; }; done; }
