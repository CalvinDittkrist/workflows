#!/usr/bin/env bash
# Print the review range and a compact change summary for the current branch.
set -uo pipefail
. "$(dirname "$0")/lib.sh"
ref=$(wf_base_ref)
mb=$(wf_merge_base)
wf_kv issue "#$(wf_issue)"
wf_kv branch "$(wf_branch)"
wf_kv base "$ref (merge-base $(git rev-parse --short "$mb"))"
wf_kv range "$mb...HEAD"
wf_kv commits "$(git rev-list --count "$mb..HEAD")"
wf_kv uncommitted "$(git status --porcelain | wc -l | tr -d ' ') files"
printf 'files:\n'; git diff --stat "$mb" HEAD | sed 's/^/  /'
