#!/usr/bin/env bash
# Move the prototype in this worktree to its own pushed branch and return to the plan branch, which stays clean.
# Usage: capture-prototype.sh <name>
set -euo pipefail
. "$(dirname "$0")/lib.sh"
wf_need git; wf_need gh
name=$(wf_slug "${1:-}"); [ -n "$name" ] || wf_die "usage: capture-prototype.sh <name>"
slug=$(wf_plan_slug); [ -n "$slug" ] || wf_die "not in a plan/<slug> worktree"
plan=$(wf_branch)
[ -n "$(git status --porcelain)" ] || wf_die "nothing to capture: the worktree is clean"
branch="prototype/$slug-$name"
git rev-parse -q --verify "refs/heads/$branch" >/dev/null 2>&1 && wf_die "branch $branch already exists; pick another name"
git checkout -q -b "$branch"
git add -A
git commit -qm "prototype: $name" -m "Throwaway code from planning session $slug. Not for merging."
git push -q -u origin "$branch" || { git checkout -q "$plan"; wf_die "push failed; the commit is on local branch $branch"; }
git checkout -q "$plan"
wf_kv branch "$branch"
wf_kv url "https://github.com/$(wf_repo_nwo)/tree/$branch"
wf_kv note "link the URL from the issue; git checkout $branch brings the code back into this worktree"
