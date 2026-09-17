#!/usr/bin/env bash
# Print the session facts a worker skill needs (mode, issue, base, reviewer panel) as key: value lines.
set -uo pipefail
. "$(dirname "$0")/lib.sh"
wf_kv mode "${WF_MODE:-manual}"
wf_kv issue "#$(wf_issue)"
wf_kv base "$(wf_base_branch)"
wf_kv reviewers "${WF_REVIEWERS:-code,security,docs,tests,senior}"
wf_kv max_rounds "${WF_REVIEW_ROUNDS:-3}"
