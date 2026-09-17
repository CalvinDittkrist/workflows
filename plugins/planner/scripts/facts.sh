#!/usr/bin/env bash
# Print the session facts a planner skill needs as key: value lines.
set -uo pipefail
. "$(dirname "$0")/lib.sh"
slug=$(wf_plan_slug)
wf_kv plan "${slug:-none (not a plan/<slug> worktree)}"
issue=$(wf_plan_issue); topic=$(wf_plan_topic)
[ -n "$issue" ] && wf_kv issue "#$issue"
[ -n "$topic" ] && wf_kv topic "$topic"
wf_kv base "$(wf_base_branch)"
if [ -f docs/glossary.md ]; then wf_kv glossary "docs/glossary.md"; else wf_kv glossary "missing (spec lists new terms)"; fi
if [ -d docs/adr ]; then wf_kv adrs "docs/adr ($(find docs/adr -name '[0-9][0-9][0-9][0-9]-*.md' | wc -l | tr -d ' '))"; else wf_kv adrs "missing"; fi
