#!/usr/bin/env bash
# Ensure the workflow's label vocabulary exists in this repository. Idempotent; prints what it created.
set -euo pipefail
. "$(dirname "$0")/lib.sh"
wf_need gh; wf_need jq
have=$(gh label list --json name --limit 100 2>/dev/null | jq -r '.[].name') || wf_die "could not list labels; is gh authenticated for this repository?"
created=""
# name|color|description. bug and enhancement are GitHub defaults but not guaranteed to exist.
while IFS='|' read -r name color desc; do
  [ -n "$name" ] || continue
  if ! printf '%s\n' "$have" | grep -qx "$name"; then
    gh label create "$name" --color "$color" --description "$desc" >/dev/null 2>&1 || wf_warn "could not create label $name"
    created="${created:+$created,}$name"
  fi
done <<LABELS
ready-for-agent|0E8A16|Fully specified; an agent can take it
needs-triage|FBCA04|A maintainer has to evaluate this
needs-info|D876E3|Waiting on the reporter
ready-for-human|1D76DB|Needs a human to implement
wontfix|FFFFFF|Will not be actioned; the closing comment says why
spec|5319E7|Spec issue; its tickets carry the work
bug|D73A4A|Something is broken
enhancement|A2EEEF|New feature or improvement
LABELS
wf_kv labels "ok"
wf_kv created "${created:-none}"
